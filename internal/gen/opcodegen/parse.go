package opcodegen

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Table is one extraction's result: the arms, plus the provenance needed to audit it.
type Table struct {
	// SourceSHA is the reference revision the arms were read from. Stamped, not
	// deduced: a generated artifact whose provenance needs git archaeology has
	// hearsay for authority (0007, condition 3).
	SourceSHA string
	// Arms, sorted by (Prefix, Code) so the generated output is stable and a diff
	// means a real change rather than a map iteration order.
	Arms []Arm
}

// Floors are the minimum arm counts per region, and they are condition 1 of 0007.
//
// This is *not* a sanity check on the parser's quality — it is the control on the
// failure the unrecognized-arm error cannot see. An extractor that recognizes nothing
// (moved file, changed indentation, upstream refactor) produces zero arms and zero
// unrecognized lines, and a drift check comparing an empty table against an empty
// committed table agrees perfectly: a green with the mechanism intact and asserting
// nothing. That is grave #29's shape relocated into a code generator.
//
// The floors are set below the counts measured at bdd7164 with room for upstream to
// *remove* an opcode without a false alarm, but far above zero and far above "a
// handful" — because a parser that finds 3 of 218 arms is as broken as one that finds
// none, and would pass any non-empty check. A floor, not a non-nil test, is the
// difference.
//
// The parenthesised measurements are the *real* counts, which are not the ones decision
// 0007 first recorded: 201/29/18/256 counted arm lines and assumed the SIMD sub-opcode
// was a byte. Corrected in the ADR's appended Correction section; kept here because a
// floor whose comment cites a wrong count invites someone to "fix" the floor.
var Floors = map[byte]int{
	0x00: 150, // single-byte (measured 218, incl. 3 prefix escapes)
	0xfb: 20,  // GC          (measured 31)
	0xfc: 12,  // misc        (measured 18)
	0xfd: 200, // SIMD        (measured 275)
}

// prefixBlock describes one `| 0xNN -> (match u32 s with ...)` region.
type prefixBlock struct {
	prefix byte
	start  int // 0-indexed line of the `| 0xNN ->` head
	end    int // 0-indexed line after the block's closing paren
}

var (
	rePrefixHead = regexp.MustCompile(`^\s*\|\s*(0x[0-9a-f]{2})(?:\s+as\s+\w+)?\s*->\s*$`)
	reInstrStart = regexp.MustCompile(`^let rec instr s =`)
	reInstrEnd   = regexp.MustCompile(`^and instr_block s =`)
	// The fallthrough arms: `| n -> illegal ...` and `| b -> illegal s pos b`. These
	// are the reference's "everything else is illegal" cases, deliberately recognized
	// and skipped rather than parsed as opcodes — they bind a variable, not a byte.
	reFallthrough = regexp.MustCompile(`^\s*\|\s*[a-z]\s*->\s*illegal2?\s`)
	// A bare `| _ ->` catch-all, same story.
	reWildcard = regexp.MustCompile(`^\s*\|\s*_\s*->`)
)

// Extract reads decode.ml's source and returns the table.
//
// sha is recorded verbatim into the result; the caller is responsible for it being
// the revision src was read from (scripts/fetch-spec-ref.sh pins and verifies it).
func Extract(src, sha string) (*Table, error) {
	lines := strings.Split(src, "\n")

	lo, hi := -1, -1
	for i, l := range lines {
		if lo < 0 && reInstrStart.MatchString(l) {
			lo = i
		}
		if lo >= 0 && reInstrEnd.MatchString(l) {
			hi = i
			break
		}
	}
	if lo < 0 || hi < 0 {
		// Not an unrecognized *arm* — the function itself is gone. Distinguished from
		// errUnrecognized because the diagnosis differs: this is "upstream moved the
		// decoder", not "upstream changed one arm".
		return nil, fmt.Errorf("%w: could not locate `let rec instr s =` .. `and instr_block s =` (found %d..%d)",
			ErrVacuous, lo, hi)
	}

	blocks := findPrefixBlocks(lines, lo, hi)

	var arms []Arm
	for i := lo; i < hi; i++ {
		prefix, inBlock := byte(0), false
		for _, b := range blocks {
			if i == b.start {
				// The `| 0xfd ->` head. Its RHS is a nested match, so parseArm cannot
				// read it — but the *escape itself* is a single-byte fact and belongs in
				// the single-byte table. Emitted here rather than skipped: see Arm.Escape
				// for why its absence was a defect and how it stayed hidden.
				arms = append(arms, Arm{Code: uint32(b.prefix), Escape: true, Line: i + 1})
				inBlock = true
				break
			}
			if i > b.start && i < b.end {
				prefix, inBlock = b.prefix, false
				break
			}
		}
		if inBlock {
			continue
		}

		got, used, err := parseArm(lines, i, prefix)
		if err != nil {
			return nil, err
		}
		arms = append(arms, got...)
		i += used - 1 // used >= 1; skip a continued head's tail lines
	}

	t := &Table{SourceSHA: sha, Arms: arms}
	sortArms(t.Arms)
	if err := t.checkFloors(); err != nil {
		return nil, err
	}
	if err := t.checkDuplicates(); err != nil {
		return nil, err
	}
	return t, nil
}

// findPrefixBlocks locates the `(match u32 s with` regions and their extents.
//
// The extent is found by paren depth from the `(match`, not by a closing-paren
// heuristic on column position: indentation is a style choice upstream may change,
// while paren balance is the language's own structure.
func findPrefixBlocks(lines []string, lo, hi int) []prefixBlock {
	var out []prefixBlock
	for i := lo; i < hi; i++ {
		m := rePrefixHead.FindStringSubmatch(lines[i])
		if m == nil || i+1 >= hi || !reMatchU32.MatchString(lines[i+1]) {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(m[1], "0x"), 16, 8)
		if err != nil {
			continue
		}
		depth, end := 0, hi
		for j := i + 1; j < hi; j++ {
			depth += strings.Count(lines[j], "(") - strings.Count(lines[j], ")")
			if depth <= 0 {
				end = j + 1
				break
			}
		}
		out = append(out, prefixBlock{prefix: byte(v), start: i, end: end})
	}
	return out
}

// parseArm reads the arm at lines[i], returning one Arm per code in an alternation
// (`| 0x06 | 0x07 as b -> illegal ...` yields two) and the number of lines the arm's
// *head* occupied, so the caller does not re-read a continuation as an arm of its own.
//
// Lines that are not arms yield nothing. Lines that *look* like arms and cannot be
// understood are errUnrecognized — never skipped, which is the property that makes
// this extraction trustworthy in a way a careful reading is not.
func parseArm(lines []string, i int, prefix byte) ([]Arm, int, error) {
	line := lines[i]
	if reFallthrough.MatchString(line) || reWildcard.MatchString(line) {
		return nil, 1, nil
	}
	// An alternation head may wrap: `| 0xc5 | ... | 0xcb` / `| 0xcc | ... as b -> ...`
	// (decode.ml:601-602, the only such arm at bdd7164 — but joining is scoped to the
	// shape, not to that one instance). Join forward while the line is nothing but
	// codes, so the regexp below sees one head. Bounded by the arrow's arrival.
	head, used := line, 1
	for reHeadContinues.MatchString(head) && i+used < len(lines) {
		head += " " + strings.TrimSpace(lines[i+used])
		used++
	}
	m := reArmHead.FindStringSubmatch(head)
	if m == nil {
		// Only complain about lines that plausibly *are* arms. A comment, a `let`, or
		// a continuation line is not an unrecognized arm — it is not an arm.
		if strings.HasPrefix(strings.TrimSpace(line), "| 0x") {
			return nil, used, fmt.Errorf("%w: decode.ml:%d: %s", errUnrecognized, i+1, strings.TrimSpace(head))
		}
		return nil, 1, nil
	}

	rhs := strings.TrimSpace(m[2])
	// A multi-line arm: the RHS continues on following lines until the next arm head
	// or a dedent to the enclosing match. Collect it, because the immediates live
	// there (blocktype/instr_block/end_ for block, loop, if, try_table).
	if rhs == "" || strings.HasSuffix(rhs, "->") {
		var b strings.Builder
		b.WriteString(rhs)
		for j := i + used; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, "| ") || strings.HasPrefix(t, ")") {
				break
			}
			b.WriteString(" ")
			b.WriteString(t)
			used++
		}
		rhs = b.String()
	}

	codes := reHexCode.FindAllStringSubmatch(m[1], -1)
	if len(codes) == 0 {
		return nil, used, fmt.Errorf("%w: decode.ml:%d: no opcode in arm head %q", errUnrecognized, i+1, m[1])
	}

	imms := parseImms(rhs)

	out := make([]Arm, 0, len(codes))
	for _, c := range codes {
		v, err := strconv.ParseUint(c[1], 16, 32)
		if err != nil {
			return nil, used, fmt.Errorf("%w: decode.ml:%d: bad opcode %q", errUnrecognized, i+1, c[0])
		}
		a := Arm{Prefix: prefix, Code: uint32(v), Imms: imms, Line: i + 1}
		switch {
		case strings.Contains(rhs, "illegal"):
			a.Illegal, a.Imms = true, nil
		case strings.Contains(rhs, "error s pos"):
			a.Reason, a.Imms = errorText(rhs), nil
		default:
			a.Mnemonic = mnemonicOf(rhs, uint32(v))
			if a.Mnemonic == "" {
				return nil, used, fmt.Errorf("%w: decode.ml:%d: no mnemonic in %q", errUnrecognized, i+1, rhs)
			}
		}
		out = append(out, a)
	}
	return out, used, nil
}

// parseImms extracts the immediate sequence in reading order.
//
// Order is load-bearing and is taken from *position in the source text*, because that
// is the order the reference reads them in: `let x, a, o = memop s in let i = laneidx
// s in ...` reads the memarg before the lane. Getting the order wrong shifts every
// subsequent byte, which does not fail loudly — it surfaces elsewhere as a size
// mismatch or a bogus opcode (0006).
func parseImms(rhs string) []Imm {
	// Mask matched spans as they are consumed, so a longer pattern's text cannot be
	// re-matched by a shorter one. immPatterns is ordered longest-first, which is what
	// makes `vec valtype s` win over `valtype s` at the same position.
	masked := []byte(rhs)
	var hits []immHit
	for _, p := range immPatterns {
		for _, loc := range p.pat.FindAllIndex(masked, -1) {
			hits = append(hits, immHit{at: loc[0], imm: p.imm})
			for k := loc[0]; k < loc[1]; k++ {
				masked[k] = ' '
			}
		}
	}
	slices.SortFunc(hits, func(a, b immHit) int { return cmp.Compare(a.at, b.at) })
	out := make([]Imm, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.imm)
	}
	return out
}

// immHit is one immediate reader found in an arm's RHS, with its source position —
// position is what establishes reading order.
type immHit struct {
	at  int
	imm Imm
}
