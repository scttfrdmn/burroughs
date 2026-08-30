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
	//
	// **The *base* revision, once the table became composable.** A composed table's other
	// authorities are in Overlays, each with the clause it is consulted for, because a
	// header naming one SHA over rows read from two files is a provenance stamp that is
	// wrong about half its subject — worse than none, for PinnedRev's reason.
	SourceSHA string
	// SourcePath is the base decoder's repo-relative path, stamped by the caller that
	// opened it through StampPath — `Extract` is handed a string and cannot know where it
	// came from.
	//
	// Beside SourceSHA because it has the same standing and the same failure mode: two
	// pins license a file called `interpreter/binary/decode.ml`, so a revision without a
	// path is a provenance stamp that cannot say which file it is about. It is *also*
	// load-bearing downstream, which SourceSHA is not — `regionAuthority` in the emitted
	// table is built from this and the overlays' paths, and that map is what lets a
	// control in package binary re-derive a region's rejection shape from the decoder
	// that actually defined it rather than from whichever decoder it happened to open.
	SourcePath string
	// Overlays is the provenance of every consulted proposal pin, in the order composed.
	// Empty for a single-source extraction.
	Overlays []OverlayProvenance
	// Arms, sorted by (Prefix, Code) so the generated output is stable and a diff
	// means a real change rather than a map iteration order.
	Arms []Arm
	// Regions is every prefix region the table has a sub-table for — 0x00 for the
	// single-byte space, then one entry per escape — sorted.
	//
	// **Derived from the source's *structure*, never from the arms**, and the distinction
	// is the whole reason the field exists. checkFloors reads it to decide what to check, so
	// deriving it from `Arms` would make an empty table declare an empty domain and check
	// nothing: the floors would agree with a source they had failed to parse, which is the
	// exact vacuity grave #407 named and 0007 condition 1 exists to close. A region is
	// declared by a `| 0xNN -> … (match … with` head being *present*, whatever it yields.
	Regions []byte
	// Readers is the sub-opcode reader each region's `match` head names, verbatim, keyed by
	// prefix. Evidence, not configuration — see subOpcodeReaders.
	Readers map[byte]string
}

// OverlayProvenance is one consulted proposal pin, as the generated header states it.
type OverlayProvenance struct {
	// Name is the proposal, for messages and the header: "threads".
	Name string
	// Path is the file read, repo-root-relative — and it is in the header because both
	// pins license a file called `interpreter/binary/decode.ml`, so the revision alone
	// does not say which one a row came from.
	Path string
	// SHA is that pin's revision.
	SHA string
	// Region is the one prefix region this pin was consulted for.
	Region byte
	// Clause is where the licence for that consultation is written down, so the header
	// carries the citation rather than the reader having to find it.
	Clause string
}

// Floors are the minimum arm counts per region, and they are condition 1 of 0007.
//
// This is *not* a sanity check on the parser's quality — it is the control on the
// failure the unrecognized-arm error cannot see. An extractor that recognizes nothing
// (moved file, changed indentation, upstream refactor) produces zero arms and zero
// unrecognized lines, and a drift check comparing an empty table against an empty
// committed table agrees perfectly: a green with the mechanism intact and asserting
// nothing. That is grave #407's shape relocated into a code generator.
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
//
// # Whose regions these are, which is the half that was wrong
//
// This map was **hand-scoped to the core pin's four regions and applied to whatever table
// it was handed**, and the second pin falsified it immediately: the threads pin's baseline
// predates GC, so extracting it reported `prefix 0xfb yielded 0 arms, floor is 20` — a
// vacuity error about a region that authority correctly does not have. Grave
// [#495](https://github.com/scttfrdmn/burroughs/issues/495)'s family: generic discovery,
// hand-scoped bound. (Diagnosis: Scott, on #524's re-scope.)
//
// The repair is not a fifth entry. It is that the *domain* is derived at both ends and the
// two ends are different questions:
//
//   - **Per source** (checkFloors): every region the source declares must have a floor here
//     and meet it. A region this map has no entry for is an error, so a new upstream region
//     cannot arrive unfloored — and a region the source does not declare is not checked,
//     which is what makes the threads pin extractable.
//   - **Composed** (checkFloorDomain): the composed table's region set must *equal* this
//     map's key set. That is the direction that catches a region vanishing upstream, which
//     the per-source rule cannot: no region, no check, no complaint.
//
// Neither is redundant, and the pair is exercised separately in
// TestVacuityIsCaughtByTheNamedMechanism for the reason that test's own doc gives.
var Floors = map[byte]int{
	0x00: 150, // single-byte (measured 218, incl. 3 prefix escapes)
	0xfb: 20,  // GC          (measured 31)
	0xfc: 12,  // misc        (measured 18)
	0xfd: 200, // SIMD        (measured 275)
	0xfe: 50,  // atomics     (measured 67, threads pin at cc535ad)
}

// subOpcodeReaders is the reader each prefix region's `match` head names in the authority
// it was read from — and it is **evidence, deliberately not followed**.
//
// The four core regions read their sub-opcode with `u32` (a LEB128). The threads pin's 0xfe
// region reads it with `op`, which is `let op s = byte s` (spec-threads/binary/decode.ml:219)
// — a single raw byte. The two readings are not equivalent and a real input tells them apart:
// `0xfe 0xce 0x00` is a non-minimal but well-formed LEB128 encoding of 0x4e, which the u32
// reading decodes as `i64.atomic.rmw32.cmpxchg_u` and the byte reading rejects as opcode 0xce.
//
// **Burroughs takes u32.** Scott's ruling on #524: *"the standard outranks the snapshot"* —
// the threads proposal was merged, the merged text reads a u32 like every other region, and
// this pin is a snapshot of the proposal as it stood at cc535ad. Taking `byte` for 0xfe alone
// would make our own decoder inconsistent across four regions for no observable gain.
//
// It was tempting to record that as free, on the theory that Wasm requires canonical LEB
// encodings and no valid input could tell the two apart. That premise is false, and this tree
// says so in its own corpus: `binary-leb128.wast`'s first line is *"Unsigned LEB128 can have
// non-minimal length"*, and `binary.go`'s `uleb` mirrors the reference by checking the width
// budget and the value range but never minimality. So the choice is a real choice between two
// readings, recorded as one — and the control is
// TestAtomicSubOpcodeIsReadAsALEB, which asserts `0xfe 0xce 0x00` decodes as 0x4e. That
// control is aimed at a mistake this tree has made before (a sub-opcode assumed to be a byte
// undercounted the SIMD region by 19 arms in 0007's first draft) rather than at a shape.
//
// The map's key set is asserted equal to the composed table's non-zero region set, for
// Floors' reason: a region whose reader nobody recorded is a region read on a guess.
var subOpcodeReaders = map[byte]string{
	0xfb: "u32",
	0xfc: "u32",
	0xfd: "u32",
	0xfe: "op",
}

// prefixBlock describes one `| 0xNN -> (match <reader> s with ...)` region.
type prefixBlock struct {
	prefix byte
	start  int // 0-indexed line of the `| 0xNN ->` head
	end    int // 0-indexed line after the block's closing paren
	// reader is the sub-opcode reader the region's `match` head names, verbatim —
	// `u32` in every core region, `op` in the threads pin's 0xfe. **Recorded and
	// deliberately not followed**, which is the only field here that is evidence
	// rather than structure: see subOpcodeReaders for the choice and the control.
	reader string
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

	// The regions the *source declares*, which is `| 0xNN -> … (match … with` heads plus the
	// single-byte space the located `let rec instr s =` is itself. Read from the blocks rather
	// than from the arms, for the reason Table.Regions gives: a table derived domain is an
	// empty domain when the parse fails, and an empty domain checks nothing.
	regions := []byte{0x00}
	readers := map[byte]string{0x00: topLevelReader(lines, lo)}
	for _, b := range blocks {
		regions = append(regions, b.prefix)
		readers[b.prefix] = b.reader
	}
	slices.Sort(regions)

	t := &Table{SourceSHA: sha, Arms: arms, Regions: regions, Readers: readers}
	sortArms(t.Arms)
	if err := t.checkFloors(); err != nil {
		return nil, err
	}
	if err := t.checkDuplicates(); err != nil {
		return nil, err
	}
	if err := t.checkJoinKeysAreAccountedFor(); err != nil {
		return nil, err
	}
	return t, nil
}

// StampPath records the file an extraction was read from, on the table and on every arm it
// produced.
//
// One call for both, because they are one fact and were briefly two. Table.SourcePath alone
// leaves Arm.Line as a line number with no file, which is harmless per extraction — one call,
// one file — and wrong the moment Compose moves an overlay's escape arm into the base's
// single-byte region. See Arm.Path for the row that shipped citing the wrong decoder, and
// TestEveryArmCitesItsOwnAuthority for what now resolves all 610 of them.
//
// A method rather than two assignments at each call site so that the redundant-looking field
// cannot be forgotten by the next reader who notices it is redundant.
func (t *Table) StampPath(path string) {
	t.SourcePath = path
	for i := range t.Arms {
		t.Arms[i].Path = path
	}
}

// topLevelReader is the reader `let rec instr s =` dispatches the *opcode byte* with.
//
// Recorded for symmetry with the sub-opcode readers and not for a choice: both pins spell it
// `match op s with`, and the opcode itself is a byte in every version of the format, so there
// is nothing here to rule on. It is in Table.Readers so that region 0x00 is not the one entry
// whose provenance is an assumption — and so the assertion that every region has a recorded
// reader can be stated over the whole region set rather than over the region set minus one.
func topLevelReader(lines []string, lo int) string {
	for i := lo; i < min(lo+4, len(lines)); i++ {
		if m := reMatchHead.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

// findPrefixBlocks locates the `(match <reader> s with` regions and their extents.
//
// The extent is found by paren depth from the `(match`, not by a closing-paren
// heuristic on column position: indentation is a style choice upstream may change,
// while paren balance is the language's own structure.
//
// # The `(match` is searched for, not required on the next line
//
// It was required on the next line until the second pin arrived, and that adjacency was
// an accident of the core pin's layout rather than a fact about the grammar. The threads
// pin writes:
//
//	| 0xfe ->
//	  let open Values in                  (* spec-threads/binary/decode.ml:780-782 *)
//	  (match op s with
//
// so an adjacency test finds no block, every arm inside it is attributed to the
// *single-byte* region, and the extraction dies on a duplicate — `prefix=0x00 code=0x00
// appears at decode.ml:783 and :240`. **Which is the good failure**: the duplicate check
// caught a mis-parse that would otherwise have added 68 phantom single-byte opcodes, and
// it is worth noting that neither the floors nor errUnrecognized would have seen it.
//
// The search is bounded by scanBlockHead's own rules rather than by a line count, so a
// region separated from its head by two `let`s is found and a `| 0xNN ->` head that opens
// something else entirely is not.
func findPrefixBlocks(lines []string, lo, hi int) []prefixBlock {
	var out []prefixBlock
	for i := lo; i < hi; i++ {
		m := rePrefixHead.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		matchLine, reader, ok := scanBlockHead(lines, i, hi)
		if !ok {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(m[1], "0x"), 16, 8)
		if err != nil {
			continue
		}
		// Paren depth is counted from the `(match`, not from the head: the preamble lines
		// between them are balanced, but starting the walk one line after the head made the
		// first *arm* line the depth-0 line whenever a preamble was present.
		depth, end := 0, hi
		for j := matchLine; j < hi; j++ {
			depth += strings.Count(lines[j], "(") - strings.Count(lines[j], ")")
			if j > matchLine && depth <= 0 {
				end = j + 1
				break
			}
		}
		out = append(out, prefixBlock{prefix: byte(v), start: i, end: end, reader: reader})
	}
	return out
}

// scanBlockHead decides whether the `| 0xNN ->` head at lines[i] opens a sub-table, and
// returns the `(match` line's index and the reader it names.
//
// The scan accepts only reBlockPreamble lines between the head and the `(match`, which is
// what keeps this from being "look ahead a few lines and hope": a head followed by anything
// else is not a block, and the arms that follow it are then attributed to the enclosing
// region, where the duplicate check has a view of them. Failing into a loud error beats
// guessing at an extent.
func scanBlockHead(lines []string, i, hi int) (matchLine int, reader string, ok bool) {
	for j := i + 1; j < hi; j++ {
		if m := reMatchHead.FindStringSubmatch(lines[j]); m != nil {
			return j, m[1], true
		}
		if !reBlockPreamble.MatchString(lines[j]) {
			return 0, "", false
		}
	}
	return 0, "", false
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
			a.Operator = operatorOf(rhs)
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
