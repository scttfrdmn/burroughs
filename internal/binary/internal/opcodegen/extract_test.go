package opcodegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// refPath is the vendored authority, from this package's directory.
var refPath = filepath.Join("..", "..", "..", "..", testenv.RefDecodeML)

func refSource(tb testing.TB) string {
	tb.Helper()
	return testenv.RequireSpecRef(tb, refPath)
}

// TestExtractMatchesMeasuredShape pins the counts, and the numbers are the point.
//
// Not a floor — the floors are ErrVacuous's business, and a floor cannot tell 542 from
// 543. This asserts the exact shape measured at bdd7164, so an upstream revision that
// adds an opcode fails here with a diff rather than passing quietly with a bigger table.
// The pin is a *revision* pin, so an exact count is the honest assertion: nothing about
// this file can change without the SHA changing.
//
// The four figures were each verified against decode.ml by independent enumeration, not
// taken from the extractor's own output — the numbers in decision 0007's first draft
// (201/29/18/256) were wrong in two different ways and are corrected there: 201 counted
// arm *lines*, which under-counts because one line can name eleven opcodes
// (decode.ml:601), and 256 assumed the SIMD sub-opcode was a byte, where the reference
// runs to 0x113.
func TestExtractMatchesMeasuredShape(t *testing.T) {
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	counts := map[byte]int{}
	for _, a := range tab.Arms {
		counts[a.Prefix]++
	}
	// 218 single-byte, not 215: the three prefix escapes are arms too (Arm.Escape),
	// added when #33's agreement test found them missing from the single-byte table.
	want := map[byte]int{0x00: 218, 0xfb: 31, 0xfc: 18, 0xfd: 275}
	for _, p := range []byte{0x00, 0xfb, 0xfc, 0xfd} {
		if counts[p] != want[p] {
			t.Errorf("prefix %#02x: got %d arms, want %d", p, counts[p], want[p])
		}
	}
	if len(tab.Arms) != 542 {
		t.Errorf("total arms: got %d, want 542", len(tab.Arms))
	}
}

// TestSingleByteSpaceIsAccountedFor is the completeness control, and it is scoped to the
// space rather than to the extractor's output.
//
// Every one of the 256 single-byte values is either named by an arm, or a prefix, or
// falls to the reference's catch-all. Asserting the partition covers all 256 is what
// makes "215 arms" a statement about the opcode space instead of a statement about how
// many lines the parser happened to like (CLAUDE.md — scope controls to the space).
func TestSingleByteSpaceIsAccountedFor(t *testing.T) {
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	named := map[uint32]bool{}
	for _, a := range tab.Arms {
		if a.Prefix == 0 {
			named[a.Code] = true
		}
	}
	// Derived, never enumerated. This literally used to be
	// `map[uint32]bool{0xfb: true, 0xfc: true, 0xfd: true}`, and that list was the
	// blind spot: the escapes were missing from the extracted table, and the one
	// control walking all 256 bytes scored the hole as expected because it carried
	// its own copy of the answer. #33's agreement test found it from outside. A
	// hardcoded exception list in a totality check is a hole with a comment.
	prefixes := map[uint32]bool{}
	for _, a := range tab.Arms {
		if a.Prefix == 0 && a.Escape {
			prefixes[a.Code] = true
			delete(named, a.Code)
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("no prefix escapes extracted: the partition below would then class every " +
			"prefix byte as catch-all territory, which is how their absence hid (Arm.Escape)")
	}
	// Every escape must have a populated sub-table, and every sub-table an escape —
	// the two halves of the dispatch, checked against each other rather than against
	// a list.
	regions := map[byte]int{}
	for _, a := range tab.Arms {
		if a.Prefix != 0 {
			regions[a.Prefix]++
		}
	}
	for c := range prefixes {
		if regions[byte(c)] == 0 {
			t.Errorf("escape %#02x has no sub-table arms", c)
		}
	}
	for p, n := range regions {
		if !prefixes[uint32(p)] {
			t.Errorf("sub-table %#02x has %d arms but no escape arm in the single-byte space", p, n)
		}
	}
	var unaccounted []uint32
	for i := range 256 {
		c := uint32(i)
		if !named[c] && !prefixes[c] {
			unaccounted = append(unaccounted, c)
		}
	}
	// The reference's catch-all covers exactly these: 0xd7-0xfa and 0xfe-0xff.
	// They are *not* in the table, and that absence is the fact — an opcode reaching
	// `| b -> illegal s pos b` is unknown to the grammar, which is different from an
	// opcode the grammar names as illegal (those are in the table, Illegal: true).
	if len(named)+len(prefixes)+len(unaccounted) != 256 {
		t.Fatalf("partition does not cover the space: %d named + %d prefixes + %d unaccounted",
			len(named), len(prefixes), len(unaccounted))
	}
	if got, want := len(unaccounted), 38; got != want {
		t.Errorf("catch-all opcodes: got %d, want %d (%v)", got, want, unaccounted)
	}
}

// TestEveryReaderIsCalledNotPassed is the control for the i8x16_shuffle defect's *class*,
// and it exists in this shape because the obvious version of it did not work.
//
// The defect: `repeat 16 laneidx s` (decode.ml:699) matched the pattern `laneidx s`, so
// the shuffle mask extracted as *one* lane byte instead of sixteen — fifteen lost bytes
// that would shift every following instruction, invisible on the board because every
// vector bearing on the table is assert_malformed.
//
// The first control written for it asserted that no unmatched `<ident> s` call survives
// masking. It passed with the fix removed — because removing the `repeat 16` pattern lets
// the *shorter* `laneidx s` match and mask the same text, leaving nothing unmatched. A
// control blind to the exact defect it was written for is a control in name only, and the
// blindness was structural: the check could only see readers that survive masking, never
// one whose territory a shorter pattern had taken over.
//
// So the invariant is the one the defect actually violates: a matched reader must be
// *called*, not *passed as an argument*. Measured over all 542 arms, a reader is preceded
// by exactly one of five tokens — `=` (a binding), `at` (the position wrapper),
// `NoNull),` (br_on_cast's tuple), `(match` (a prefix escape's dispatch key), or the
// arm's arrow. `repeat 16 laneidx s` puts `16`
// there instead, which is the tell that `laneidx` is an argument to a combinator and the
// real immediate is whatever the combinator does with it. Any *new* upstream combinator
// wrapping a reader trips this the same way, which is the point of scoping to the token
// rather than to `repeat`.
func TestEveryReaderIsCalledNotPassed(t *testing.T) {
	src := refSource(t)
	lines := strings.Split(src, "\n")
	tab, err := Extract(src, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// The legal predecessors, each a deliberate entry. An unlisted one means a reader
	// is being consumed by something other than a binding — most likely a combinator,
	// which changes the immediate's count or width.
	legal := map[string]bool{
		"=":          true, // let x = memop s
		"at":         true, // let x = at idx s
		"NoNull),":   true, // ((if bit 0 flags then Null else NoNull), heaptype s)
		"<arrowend>": true, // the reader opens the RHS
		// `| 0xfd -> (match u32 s with` — the prefix escapes. The reader is called, and
		// its result is a *dispatch key* rather than an immediate, so the arm carries no
		// Imms and this predecessor is the tell that says why. Added when Arm.Escape
		// started emitting arms at these lines: the test then scanned three RHSs it had
		// never seen and objected, correctly, that something unlisted was consuming a
		// reader. Widening the list rather than special-casing the escape keeps the
		// question the test asks unchanged — "who consumes this reader, and is that a
		// binding" — with one more answer that is allowed.
		"(match": true,
	}
	reLastTok := regexp.MustCompile(`([^\s]+)\s*$`)
	for _, a := range tab.Arms {
		rhs := joinRHS(lines, a.Line-1)
		// Mask as parseImms does, so this checks the hits extraction actually *uses*.
		// Scanning unmasked text instead makes the test fail on the correct source —
		// `laneidx s` still matches inside `repeat 16 laneidx s` even though the longer
		// pattern claims it — which is a test reporting a defect in itself as a defect
		// in the thing measured.
		masked := []byte(rhs)
		for _, p := range immPatterns {
			for _, loc := range p.pat.FindAllIndex(masked, -1) {
				tok := "<arrowend>"
				if m := reLastTok.FindStringSubmatch(string(masked[:loc[0]])); m != nil {
					tok = m[1]
				}
				for k := loc[0]; k < loc[1]; k++ {
					masked[k] = ' '
				}
				if !legal[tok] {
					t.Errorf("decode.ml:%d (%#02x/%#x): reader %q is preceded by %q, not a binding — "+
						"it is being passed to something rather than called, so the extracted immediate "+
						"is probably not the real shape (this is the i8x16_shuffle defect's class)\n\t%s",
						a.Line, a.Prefix, a.Code, p.imm, tok, strings.TrimSpace(rhs))
				}
			}
		}
	}
}

// TestEveryReaderIsInTheVocabulary asserts the vocabulary is complete over the corpus:
// after masking every known reader, no `<ident> s` call survives except the known
// non-immediate constructs.
//
// Kept alongside the test above because the two catch different things. This one catches
// a reader upstream *adds* that we have no pattern for; that one catches a reader whose
// text a shorter pattern is already claiming. The shuffle defect was in the second class
// only, which is why this test alone was insufficient — and both are needed, because a
// brand-new reader leaves text unmatched and trips nothing else.
func TestEveryReaderIsInTheVocabulary(t *testing.T) {
	src := refSource(t)
	lines := strings.Split(src, "\n")
	tab, err := Extract(src, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Constructs that take `s` and are not immediates: the sequence terminator, the
	// two error reporters, and the if/else lookahead. Enumerated because each is a
	// deliberate exclusion, and an unlisted one is a reader we are silently dropping.
	allowed := map[string]bool{"end_": true, "error": true, "illegal": true, "peek": true}
	reCall := regexp.MustCompile(`\b([a-z_][a-z0-9_']*)\s+s\b`)
	for _, a := range tab.Arms {
		rhs := joinRHS(lines, a.Line-1)
		masked := []byte(rhs)
		for _, p := range immPatterns {
			for _, loc := range p.pat.FindAllIndex(masked, -1) {
				for k := loc[0]; k < loc[1]; k++ {
					masked[k] = ' '
				}
			}
		}
		for _, m := range reCall.FindAllStringSubmatch(string(masked), -1) {
			if !allowed[m[1]] {
				t.Errorf("decode.ml:%d (%#02x/%#x): reader %q is not in the immediate vocabulary; "+
					"add it to vocab and immPatterns, or add it to this test's allowed set with a reason",
					a.Line, a.Prefix, a.Code, m[1])
			}
		}
	}
}

// joinRHS reassembles an arm's right-hand side the way parseArm does.
func joinRHS(lines []string, i int) string {
	var b strings.Builder
	b.WriteString(lines[i])
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "| ") || strings.HasPrefix(t, ")") {
			break
		}
		b.WriteString(" ")
		b.WriteString(t)
	}
	return b.String()
}

// TestIrregularArmsHaveCitedShapes is condition 2 of decision 0007.
//
// The four structural arms recurse through instr_block and need hand-written readers
// under any mechanism, which makes them exactly the facts the extractor does not
// machine-check against a *shape* — so they are cited to their decode.ml lines and the
// citation is verified here, in the scheme TestFixtureProvenance already enforces for
// fixtures. Hand-written and uncited is the category 0007 exists to abolish.
//
// Each row cites the arm head's line and the immediate sequence read from it. The check
// resolves the citation: the line must be the arm it claims, and the extracted shape must
// be the one written down. A citation nobody re-checks is a claim (#37).
func TestIrregularArmsHaveCitedShapes(t *testing.T) {
	src := refSource(t)
	lines := strings.Split(src, "\n")
	tab, err := Extract(src, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cited := []struct {
		code     uint32
		line     int    // decode.ml line of the arm head
		head     string // verbatim text of that line
		mnemonic string
		imms     []Imm
		note     string
	}{
		{
			0x02, 361, "| 0x02 ->", "block",
			[]Imm{ImmBlockType, ImmBlock},
			"blocktype, then instr_block, then end_ (the END is the terminator, not an immediate)",
		},
		{
			0x03, 366, "| 0x03 ->", "loop",
			[]Imm{ImmBlockType, ImmBlock},
			"identical shape to block; the difference is semantic, not syntactic",
		},
		{
			0x04, 371, "| 0x04 ->", "if_",
			[]Imm{ImmBlockType, ImmBlock, ImmBlock},
			"two blocks, but the SECOND IS CONDITIONAL: read only when peek = 0x05 (ELSE). " +
				"The reader must not require it — decode.ml:374-382 branches, and the else-less " +
				"form ends at END with es2 = []. The table records the maximal shape; the " +
				"conditionality is the hand-written reader's business, and this note is why.",
		},
		{
			0x1f, 412, "| 0x1f ->", "try_table",
			[]Imm{ImmBlockType, ImmCatchVec, ImmBlock},
			"blocktype, then vec(catch) — itself a tagged union, decode.ml:340 — then the body",
		},
	}

	byCode := map[uint32]Arm{}
	for _, a := range tab.Arms {
		if a.Prefix == 0 {
			byCode[a.Code] = a
		}
	}
	for _, c := range cited {
		// Resolve the citation against the source before trusting anything derived
		// from it: the line must say what the row says it says.
		if c.line-1 >= len(lines) || strings.TrimSpace(lines[c.line-1]) != c.head {
			got := "<past EOF>"
			if c.line-1 < len(lines) {
				got = strings.TrimSpace(lines[c.line-1])
			}
			t.Errorf("citation does not resolve: decode.ml:%d is %q, cited as %q", c.line, got, c.head)
			continue
		}
		a, ok := byCode[c.code]
		if !ok {
			t.Errorf("opcode %#02x absent from the table", c.code)
			continue
		}
		if a.Line != c.line {
			t.Errorf("opcode %#02x: extracted from decode.ml:%d, cited at :%d", c.code, a.Line, c.line)
		}
		if a.Mnemonic != c.mnemonic {
			t.Errorf("opcode %#02x: mnemonic %q, cited as %q", c.code, a.Mnemonic, c.mnemonic)
		}
		if fmt.Sprint(a.Imms) != fmt.Sprint(c.imms) {
			t.Errorf("opcode %#02x: imms %v, cited as %v", c.code, a.Imms, c.imms)
		}
	}
}

// TestMultiMnemonicArmSplitsByCode pins br_on_cast/br_on_cast_fail, the one arm whose
// two opcodes build *different* instructions (decode.ml:640-646).
//
// A single label per arm would be wrong by construction here, and it was: both codes
// reported the OCaml keyword `if`, because the mnemonic sits inside a conditional the
// generic path walked into. Its sibling defect is in the same function — the four
// structural arms reported `end_`, the statement before the constructor.
func TestMultiMnemonicArmSplitsByCode(t *testing.T) {
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := map[uint32]string{0x18: "br_on_cast", 0x19: "br_on_cast_fail"}
	for _, a := range tab.Arms {
		if a.Prefix != 0xfb {
			continue
		}
		if w, ok := want[a.Code]; ok {
			if a.Mnemonic != w {
				t.Errorf("0xfb/%#x: mnemonic %q, want %q", a.Code, a.Mnemonic, w)
			}
			delete(want, a.Code)
		}
	}
	for c := range want {
		t.Errorf("0xfb/%#x absent from the table", c)
	}
}

// TestVacuityIsCaughtByTheNamedMechanism is condition 1 of decision 0007, and it is the
// control the unrecognized-arm error cannot be.
//
// The failure mode: an upstream refactor the parser does not recognize yields zero arms
// and zero unrecognized lines, the generator writes an empty table, and the drift check
// compares empty against empty and agrees. A green with the mechanism fully intact,
// asserting nothing — grave #29 relocated into a code generator.
//
// Each case names *which* control must catch it, and the check is not errors.Is.
// Both controls report ErrVacuous, so errors.Is cannot tell them apart — and that is
// not a nitpick: the first version of this test had four cases under a name promising
// the floors, and disarming the floors (setting them all to 0) left two of the four
// green, because those two were being caught by the function-location check instead.
// The pass count was right and the coverage was not, which is #34's lesson wearing this
// package's clothes: when a partition's members share an error value, assert the
// discriminating field. Here that is the message text, which names the mechanism.
//
// Falsified by *inducing* each case against the real source, not by reasoning about it.
func TestVacuityIsCaughtByTheNamedMechanism(t *testing.T) {
	src := refSource(t)

	const (
		byFloors = "floor is" // checkFloors' message
		byLocate = "could not locate"
	)
	cases := []struct {
		name string
		by   string
		mung func(string) string
	}{
		{
			// The function is still there; every arm is unrecognizable. This is the
			// case that motivates the whole control: no arm *looks* like an arm, so
			// errUnrecognized cannot fire, and an arm count is the only witness.
			name: "arms renamed away, function intact",
			by:   byFloors,
			mung: func(s string) string { return strings.ReplaceAll(s, "  | 0x", "  (* x *) | Q") },
		},
		{
			// Only the SIMD region breaks. The single-byte region still yields 215
			// arms, so any global "is the table non-empty" check passes — measured at
			// 264 arms with the floors disarmed. The floors are per-region precisely
			// because a whole region can vanish while the total stays plausible.
			name: "SIMD region gutted, total still plausible",
			by:   byFloors,
			mung: func(s string) string {
				i := strings.Index(s, "  | 0xfd ->")
				j := strings.Index(s, "and instr_block s =")
				return s[:i] + strings.Repeat("\n", 10) + s[j:]
			},
		},
		{
			// An upstream rename of the decoder entry point. Caught by the locate
			// check, *not* by the floors — verified by disarming the floors and
			// watching this case stay green.
			name: "instr function renamed",
			by:   byLocate,
			mung: func(s string) string { return strings.ReplaceAll(s, "let rec instr s =", "let rec instruction s =") },
		},
		{
			// A truncated fetch, which testenv's byte floor also guards — two
			// controls, two diagnoses, and this proves the extractor does not depend
			// on the other having run. Also caught by locate: the truncation removes
			// the `and instr_block s =` terminator.
			name: "source truncated mid-instr",
			by:   byLocate,
			mung: func(s string) string {
				i := strings.Index(s, "let rec instr s =")
				return s[:i+400]
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab, err := Extract(c.mung(src), "test")
			n := -1
			if tab != nil {
				n = len(tab.Arms)
			}
			if !errors.Is(err, ErrVacuous) {
				t.Fatalf("got err=%v (arms=%d), want ErrVacuous — a broken source that yields a "+
					"small or empty table passes the drift check against an empty commit", err, n)
			}
			if !strings.Contains(err.Error(), c.by) {
				t.Fatalf("caught by the wrong mechanism: want a message containing %q, got %v\n"+
					"\tboth controls report ErrVacuous, so this case is not exercising the one it names",
					c.by, err)
			}
		})
	}
}

// TestUnrecognizedArmIsAnErrorNotASkip pins the other half: a line that *is* an arm and
// cannot be parsed must break the build, never be omitted.
//
// This is the property that makes machine extraction trustworthy where a careful reading
// is not — the failure mode is inverted from silent undercoverage to a loud stop.
func TestUnrecognizedArmIsAnErrorNotASkip(t *testing.T) {
	src := refSource(t)
	// A shape the grammar does not cover: a range pattern. Chosen because it is real
	// OCaml an upstream author could plausibly write, not a nonsense string.
	munged := strings.Replace(src, "  | 0x28 ->", "  | 0x28 .. 0x2a ->", 1)
	if munged == src {
		t.Fatal("mutation did not apply: the anchor line changed upstream")
	}
	if _, err := Extract(munged, "test"); !errors.Is(err, errUnrecognized) {
		t.Fatalf("got err=%v, want errUnrecognized: an arm the extractor cannot read must "+
			"stop the build, not shrink the table by one opcode", err)
	}
}

// TestEmitIsDeterministic guards the drift check's own premise.
//
// The check compares generated text against a committed file, so a generator whose
// output varies between runs turns that comparison into a coin flip — Go's map iteration
// order would do it. Two extractions, byte-identical, or the control below is measuring
// noise.
func TestEmitIsDeterministic(t *testing.T) {
	src := refSource(t)
	var prev string
	for i := range 3 {
		tab, err := Extract(src, "test")
		if err != nil {
			t.Fatalf("Extract: %v", err)
		}
		got, err := tab.Emit()
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		if i > 0 && got != prev {
			t.Fatal("Emit is not deterministic across runs; the drift check would be a coin flip")
		}
		prev = got
	}
}

// TestEmitRejectsAnImmediateWithNoIdentifier proves the vocabulary/table coupling fails
// loudly rather than emitting a narrower shape than the authority's.
func TestEmitRejectsAnImmediateWithNoIdentifier(t *testing.T) {
	tab := &Table{SourceSHA: "test", Arms: []Arm{
		{Prefix: 0, Code: 0x28, Mnemonic: "i32_load", Imms: []Imm{"a_reader_nobody_declared"}, Line: 1},
	}}
	if _, err := tab.Emit(); err == nil {
		t.Fatal("Emit accepted an immediate with no generated identifier; it would have written " +
			"an empty imms list, silently narrowing the shape")
	}
}

// TestCommittedTableMatchesTheReference is condition 4 of decision 0007: drift is a
// build failure, not a diff nobody ordered.
//
// It re-runs the extraction against the pinned reference and compares the result against
// the committed file byte for byte. Cheap precisely because mechanism B needs no
// toolchain — the property that recommended B is what makes its continuous check
// affordable.
//
// It cannot live in `make check`, which must pass on a fresh clone with nothing
// vendored; it runs in `make opcode-drift` and in CI, and it refuses to run without the
// reference rather than skipping, because a drift check that skips reports agreement
// with an authority it never read.
func TestCommittedTableMatchesTheReference(t *testing.T) {
	src := refSource(t)
	sha, err := gen.PinnedRev(filepath.Join("..", "..", "..", "..", "scripts", "fetch-spec-ref.sh"))
	if err != nil {
		t.Fatal(err)
	}
	tab, err := Extract(src, sha)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want, err := tab.Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	path := filepath.Join("..", "..", "optable.go")
	gotB, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Compare against gofmt'd output, since the committed file is formatted.
	wantFmt, err := gen.GofmtSource(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotB) != wantFmt {
		t.Errorf("internal/binary/optable.go disagrees with the reference at %s.\n"+
			"Regenerate with: make opcodes\n"+
			"committed %d bytes, extracted %d bytes", sha, len(gotB), len(wantFmt))
		if d := firstDiff(string(gotB), wantFmt); d != "" {
			t.Errorf("first difference:\n%s", d)
		}
	}
}

func firstDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(as), len(bs)) {
		x, y := "<eof>", "<eof>"
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return fmt.Sprintf("  line %d committed: %s\n  line %d extracted: %s", i+1, x, i+1, y)
		}
	}
	return ""
}
