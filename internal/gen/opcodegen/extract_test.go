package opcodegen

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// refPath is the vendored authority, from this package's directory.
// refPath names the vendored authority. Only the *name* matters: RequireSpecRef resolves
// it from the repo root and uses this to select the file's size floor, so this is no longer
// a claim about where this package sits. It was one, and the claim was wrong for a while
// without failing — see gen.FromRoot.
var refPath = testenv.RefDecodeML

func refSource(tb testing.TB) string {
	tb.Helper()
	return testenv.RequireSpecRef(tb, refPath)
}

// shippedArms is every arm the composed table carries, paired with the lines of the decoder it
// was read from — the domain of any control whose subject is "the readers this table uses".
//
// # It exists because two controls had `refSource` where they needed this
//
// `TestEveryReaderIsCalledNotPassed` and `TestEveryReaderIsInTheVocabulary` are both scoped to
// the *space of readers the table's arms use*, and both read the core pin alone. That was a
// complete domain until the 0xfe region arrived, and then it was 67 arms short with nothing
// saying so — the shape of grave
// [#495](https://github.com/scttfrdmn/burroughs/issues/495) and of `Floors`' own bug two files
// over: a generic mechanism with a hand-spelled domain, where the mechanism keeps working and
// the domain silently stops covering the subject.
//
// The reader it was missing is not hypothetical. `expect 0x00 s` is in the 0xfe region and
// nowhere else, it is the one immediate whose content is a *constraint* (ImmZeroByte), and the
// vocabulary control could not have objected to it even in domain — see reCall's own note.
//
// # The composed table, not the pin set
//
// The first version of this walked each pin's *whole* extraction, and that was wrong in the
// other direction: it reported `var`, `block_type`, and `value_type` as readers outside the
// vocabulary, all three from the threads pin's single-byte region, which is a three-proposal-old
// spelling of `idx`/`blocktype`/`valtype` — and every one of those arms is dropped unread by
// Compose. Nine failures, none of them about anything this table ships. *Consultation is
// clause-scoped, never wholesale* (contract §9 G-2) applies to a control's domain exactly as it
// applies to the composition: the arms are the ones that crossed, and Arm.Path is what says
// which file each one's line number is in.
func shippedArms(tb testing.TB) (*Table, map[string][]string) {
	tb.Helper()
	requireEveryPinFetched(tb)
	tab, err := BuildFromPins()
	if err != nil {
		tb.Fatalf("BuildFromPins: %v", err)
	}
	sources := map[string][]string{}
	for _, a := range tab.Arms {
		if _, read := sources[a.Path]; !read {
			sources[a.Path] = strings.Split(testenv.RequireSpecRef(tb, a.Path), "\n")
		}
	}
	if len(sources) < 2 {
		tb.Fatalf("the shipped arms name %d decoder(s), want >=2: a reader control over one pin "+
			"cannot see a reader the other pin's region is the only user of", len(sources))
	}
	return tab, sources
}

// requireEveryPinFetched runs the licensed door over *every* pin's decoder, so a composed
// table's drift check cannot proceed on a half-fetched tree.
//
// Derived from the pin set rather than listing the two paths, for DecodeMLFor's reason: the
// list already exists, and a second copy is a claim that drifts. The direction it asserts is
// the one BuildFromPins' own `seen < 2` floor cannot — that floor fires on a pin set with no
// licence, this fires on a licence whose file is absent, and only one of those is a bug in
// the tree rather than in the box.
func requireEveryPinFetched(tb testing.TB) {
	tb.Helper()
	pins := 0
	for _, pin := range testenv.RefPins() {
		path, ok := DecodeMLFor(pin)
		if !ok {
			continue
		}
		testenv.RequireSpecRef(tb, path)
		pins++
	}
	if pins < 2 {
		tb.Fatalf("%d pins license a decoder; the composed table needs core and threads. A "+
			"presence check over one pin passes on a tree missing the other", pins)
	}
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
//
// Over **every** pin's decoder, not the core pin's: see everyDecoderSource for the 67 arms this
// was blind to and why the domain is derived.
func TestEveryReaderIsCalledNotPassed(t *testing.T) {
	// The legal predecessors, each a deliberate entry. An unlisted one means a reader
	// is being consumed by something other than a binding — most likely a combinator,
	// which changes the immediate's count or width.
	legal := map[string]bool{
		// `let x = memop s`, and also `let x = at idx s`: the `at` wrapper is inside the mask
		// pattern (`at idx s` is its own, longer entry in immPatterns), so it is consumed with
		// the reader and never appears as a predecessor.
		"=": true,
		// `((if bit 0 flags then Null else NoNull), heaptype s)` — br_on_cast's tuple.
		"NoNull),": true,
		// `| 0x03 -> expect 0x00 s "zero flag expected"; atomic_fence` — the reader is the
		// first thing the arm does, so the token before it is the arm's own arrow, and an arrow
		// is not a consumer.
		//
		// New with the *composed* domain rather than with the widened regexp: no core arm puts
		// a reader directly after its arrow, which is why a list that was complete over 542
		// arms was one entry short over 610.
		"->": true,
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
	scanned := 0
	witnessed := map[string]int{}
	tab, sources := shippedArms(t)
	{
		for _, a := range tab.Arms {
			lines := sources[a.Path]
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
					scanned++
					witnessed[tok]++
					if !legal[tok] {
						t.Errorf("%s:%d (%#02x/%#x): reader %q is preceded by %q, not a binding — "+
							"it is being passed to something rather than called, so the extracted immediate "+
							"is probably not the real shape (this is the i8x16_shuffle defect's class)\n\t%s",
							a.Path, a.Line, a.Prefix, a.Code, p.imm, tok, strings.TrimSpace(rhs))
					}
				}
			}
		}
	}
	// A floor on what was looked at, because every failure mode of this control except a
	// real defect ends in *fewer* readers scanned: a joinRHS that reassembles nothing, an
	// immPatterns that matches nothing, a domain that lost a pin. Zero readers agrees with
	// every source there is.
	if scanned < 200 {
		t.Errorf("scanned %d reader calls across the shipped arms, want >=200 (measured 235) — the "+
			"predecessors all came out legal, but over a population this small that is the scan "+
			"reporting its own reach", scanned)
	}
	// Every licence must have a witness. An allow list is a set of claims about the authority,
	// and an entry nothing matches is a claim about a shape that has gone — which reads to the
	// next author as a shape that is present and permitted. Same argument as the per-pin
	// coverage guards on the citation joins, and it is checkJoinKeyDomain's direction applied
	// to a test's own table.
	//
	// **It found two of five dead on its first run**, and neither was dead by upstream drift:
	// `at` cannot be a predecessor because every `at …` form is its own longer mask pattern, so
	// the wrapper is consumed with the reader; `<arrowend>` cannot fire because joinRHS starts
	// at the arm's head line, so something always precedes. Both were unwitnessable *by
	// construction* and had been since they were written — which is the case a "the list is
	// still accurate" reading cannot reach, because the entries describe real shapes in the
	// authority that this scan structurally never sees. The live entry for what `<arrowend>`
	// was written for is `->`.
	for tok := range legal {
		if witnessed[tok] == 0 {
			t.Errorf("legal predecessor %q has no witness among %d reader calls: the shape it "+
				"licenses is not in the authority any more, so the entry permits nothing and "+
				"documents something untrue", tok, scanned)
		}
	}
	t.Logf("%d reader calls scanned across %d shipped arms; predecessors %v",
		scanned, len(tab.Arms), witnessed)
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
//
// Over every pin's decoder (everyDecoderSource), and with reCall widened past the shape it was
// written against — the two ways this control was narrower than its own name.
func TestEveryReaderIsInTheVocabulary(t *testing.T) {
	// Constructs that take `s` and are not immediates: the sequence terminator, the
	// two error reporters, and the if/else lookahead. Enumerated because each is a
	// deliberate exclusion, and an unlisted one is a reader we are silently dropping.
	allowed := map[string]bool{
		"end_":    true, // expect 0x0b s — the sequence terminator
		"error":   true, // the two error reporters
		"illegal": true,
		"peek":    true, // the if/else lookahead
		// `| 0xfe -> … (match op s with` — a prefix escape's *dispatch key*, which is a
		// sub-opcode and not an immediate. The core escapes spell it `u32 s` and are masked as
		// if it were one, which is a pre-existing untruth this entry does not extend: which
		// reader each region names is subOpcodeReaders' subject, checked there against the
		// authority, and the choice between the two readings is recorded there too.
		"op": true,
	}
	// `expect <byte> s` is a byte *matcher*, and whether one is an immediate or a structural
	// terminator is decided by which byte — so the exclusion is per byte, listed here, and
	// `expect` is deliberately not in `allowed` above.
	//
	// 0x00 in `atomic.fence` is the instruction's entire immediate (ImmZeroByte) and is masked
	// out before this scan runs; 0x05 is the ELSE that closes an `if`, which the structural arm
	// consumes and the table does not record. Licensing the bare identifier would have covered
	// both and licensed a future `expect 0x07 s` that is an immediate — the exact reporting
	// failure this control exists to prevent, admitted through its own allow list.
	terminatorExpects := map[string]string{
		"0x05": "the ELSE opcode closing an `if` (spec/binary/decode.ml:371) — consumed by the " +
			"structural arm, whose immediates are hand-cited in TestIrregularArmsHaveCitedShapes",
	}
	// `<ident> s`, and `<ident> <literal> s` — the second alternative is the widening, and
	// the reader that motivated it is `expect 0x00 s`.
	//
	// The old pattern's implicit premise was that a reader takes the stream and nothing else,
	// so the token before `s` is always the reader's own name. `expect 0x00 s "zero flag
	// expected"` breaks it: the token before `s` is `0x00`, the pattern does not match, and a
	// reader outside the vocabulary would have gone unreported by the control whose whole job
	// is to report exactly that. ImmZeroByte's doc records how that was found — not by this
	// control firing, but by reading an arm that came out with an empty immediate list.
	//
	// The literal is `[0-9]\w*`, which covers `0x00` and a decimal count, and it does *not*
	// swallow `repeat 16 laneidx s`: the numeric group cannot match `laneidx`, so the scan
	// finds `laneidx s` where it always did, and the neighbouring control's reading of that
	// arm is unchanged.
	reCall := regexp.MustCompile(`\b([a-z_][a-z0-9_']*)(?:\s+([0-9]\w*))?\s+s\b`)
	arms, expects := 0, 0
	tab, sources := shippedArms(t)
	{
		for _, a := range tab.Arms {
			lines := sources[a.Path]
			rhs := joinRHS(lines, a.Line-1)
			masked := []byte(rhs)
			for _, p := range immPatterns {
				for _, loc := range p.pat.FindAllIndex(masked, -1) {
					for k := loc[0]; k < loc[1]; k++ {
						masked[k] = ' '
					}
				}
			}
			arms++
			for _, m := range reCall.FindAllStringSubmatch(string(masked), -1) {
				if m[1] == "expect" {
					expects++
					if _, licensed := terminatorExpects[m[2]]; !licensed {
						t.Errorf("%s:%d (%#02x/%#x): `expect %s s` matches a byte no entry in "+
							"terminatorExpects accounts for — if the byte is the instruction's "+
							"immediate it belongs in vocab and immPatterns, and if the arm consumes "+
							"it structurally say so there with the citation",
							a.Path, a.Line, a.Prefix, a.Code, m[2])
					}
					continue
				}
				if !allowed[m[1]] {
					t.Errorf("%s:%d (%#02x/%#x): reader %q is not in the immediate vocabulary; "+
						"add it to vocab and immPatterns, or add it to this test's allowed set with a reason",
						a.Path, a.Line, a.Prefix, a.Code, m[1])
				}
			}
		}
	}
	// This control passes by finding *nothing*, which is the verdict a lost domain also
	// produces. The floor is on the arms walked, since that is the population whose emptiness
	// would be invisible.
	if arms < 600 {
		t.Errorf("walked %d arms across the pin set, want >=600 — a clean scan over a population "+
			"this small is the domain reporting its own size, not the vocabulary's completeness", arms)
	}
	// The `expect` branch above is the widening's whole subject, so its population is asserted
	// separately: at zero it would be the old regexp back, passing because it matches nothing.
	if expects == 0 {
		t.Error("no `expect <byte> s` call survived masking, so the branch that tells a terminator " +
			"from an immediate never ran — which is the state the unwidened reCall was in, and it " +
			"passed")
	}
	t.Logf("%d arms walked, %d unmasked `expect` calls classified", arms, expects)
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
// asserting nothing — grave #407 relocated into a code generator.
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
//
// # The third mechanism, and why one case had to change path
//
// `composed` cases run the mutation through the *composed* table, and they exist because the
// floor map's domain was wrong and the repair moved a case from one mechanism to another. When
// `Floors` was iterated over its own keys, gutting the SIMD region fired a per-source floor; now
// that checkFloors walks the regions the source *declares* — the change that makes the threads
// pin extractable, since its baseline correctly has no 0xfb — a vanished region is not checked
// per source at all. The same mutation still has to be caught, by checkFloorDomain, and the case
// that used to name `byFloors` now names `byDomain`.
//
// A `composed` case therefore asserts *both* halves: the per-source path must come out silent,
// and the composed path must fire. Without the first half the case would pass whether or not the
// mechanism it names is the one running — the way it did before this test was split by message —
// and the two halves together are what makes "neither check is redundant" an assertion rather
// than a remark in Floors' doc.
func TestVacuityIsCaughtByTheNamedMechanism(t *testing.T) {
	src := refSource(t)

	const (
		byFloors = "floor is" // checkFloors' message
		byLocate = "could not locate"
		byDomain = "has no such region" // checkFloorDomain's message
	)
	cases := []struct {
		name string
		by   string
		// composed runs the mutation through Compose with the real overlays, and asserts the
		// per-source extraction of the same mutated source is silent.
		composed bool
		mung     func(string) string
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
			// The whole SIMD region vanishes, head and all. The rest of the table still
			// yields 266 arms (measured, printed from the per-source extraction this case
			// asserts is silent), so any global "is the table non-empty" check passes, and
			// **so does every per-source floor**: no `| 0xfd ->` head means no declared
			// region, no floor lookup, and nothing to fall short of. This is the case
			// checkFloorDomain exists for, and it is why a floor map keyed by region is not
			// enough on its own — the region that disappears is the one nobody checks.
			name:     "SIMD region gutted, total still plausible",
			by:       byDomain,
			composed: true,
			mung: func(s string) string {
				i := strings.Index(s, "  | 0xfd ->")
				j := strings.Index(s, "and instr_block s =")
				return s[:i] + strings.Repeat("\n", 10) + s[j:]
			},
		},
		{
			// The region is still *declared* and its arms are unrecognizable: the head and
			// its `(match u32 s with` survive, so the region is in Regions and its floor is
			// looked up, and it comes out at 0 against a floor of 200.
			//
			// Kept beside the case above because the two are different failures wearing one
			// name — a region that *vanishes* and a region that is *gutted in place* — and
			// only the second is a per-source floor's business. This is the case that carries
			// the claim Floors' doc makes, that the floors are per-region because a whole
			// region can go while the total stays plausible: measured at 267 arms over all
			// four declared regions with the 0xfd floor disarmed, which is how the figure in
			// the case above it was taken too.
			name: "SIMD region declared and its arms unrecognizable",
			by:   byFloors,
			mung: func(s string) string {
				i := strings.Index(s, "  | 0xfd ->")
				j := strings.Index(s, "and instr_block s =")
				return s[:i] + strings.ReplaceAll(s[i:j], "\n    | 0x", "\n    (* x *) | Q") + s[j:]
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
			munged := c.mung(src)
			if munged == src {
				t.Fatal("mutation did not apply: its anchor text changed upstream, and a case that " +
					"mutates nothing asserts that the real source is caught by a vacuity check")
			}
			tab, err := Extract(munged, "test")
			if c.composed {
				// The half that makes this case exercise the mechanism it names rather than
				// merely reach it: per source the mutation must be *invisible*.
				if err != nil {
					t.Fatalf("the per-source extraction caught this, with %v — then the composed "+
						"path below would report the same error and this case would pass without "+
						"checkFloorDomain running at all", err)
				}
				tab, err = composeOverMutatedBase(t, tab)
			}
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
					"\tall three controls report ErrVacuous, so this case is not exercising the one "+
					"it names", c.by, err)
			}
		})
	}
}

// composeOverMutatedBase composes the real overlay set over a base extracted from mutated
// source, through the same Compose call the generator reaches.
//
// The overlays come from pinSources rather than being built here, which is the point: a control
// that assembled its own overlay list would be checking a second implementation of the
// composition and reporting on the first.
func composeOverMutatedBase(tb testing.TB, base *Table) (*Table, error) {
	tb.Helper()
	requireEveryPinFetched(tb)
	real_, overlays, err := pinSources()
	if err != nil {
		tb.Fatalf("pinSources: %v", err)
	}
	if len(overlays) == 0 {
		tb.Fatal("pinSources returned no overlays: Compose would then be handed the mutated base " +
			"alone, and a composed-path case would be exercising the per-source path")
	}
	// The stamps travel with the authority the base *would* have had; only its arms and regions
	// are the mutation's.
	base.SourceSHA, base.SourcePath = real_.SourceSHA, real_.SourcePath
	return Compose(base, overlays...)
}

// TestEveryArmCitesItsOwnAuthority resolves all 610 rows' citations, over the composed table.
//
// # The defect it was written for
//
// `refLine` is the generated table's audit trail — "any row can be audited against the
// authority without a search" — and for one row it pointed at the wrong arm in the wrong file.
// The 0xfe escape lives in the single-byte region, whose authority is the core pin, and its line
// number came from the threads pin: `0xfe: {escape: true, refLine: 780}`, where core
// decode.ml:780 is `v128_store16_lane`. A citation that resolves to something plausible is worse
// than one that dangles, and no control could have seen it, because every control that had a
// path had spelled that path itself. Grave
// [#529](https://github.com/scttfrdmn/burroughs/issues/529); the repair is Arm.Path, and this is
// the check on it.
//
// # Why it is over the composed table and per arm
//
// The failure only exists after composition — per extraction, one call reads one file and every
// line number is in it, so a per-source version of this control would pass on the two pins
// separately and say nothing about the artifact that ships. It is per *arm* rather than per
// region for the same reason the defect exists: the region is the wrong unit exactly where a
// composition moves a row across one.
//
// The predicate resolves the citation rather than merely opening the file — the line must be an
// arm head, and the arm's own code must appear in that head. `decode.ml:780 exists` is true of
// both pins and is what the old comment was effectively asserting.
func TestEveryArmCitesItsOwnAuthority(t *testing.T) {
	requireEveryPinFetched(t)
	tab, err := BuildFromPins()
	if err != nil {
		t.Fatalf("BuildFromPins: %v", err)
	}
	auth := tab.Authority()
	// Read each authority once; keyed by the path the *arms* name, so a path no arm names is
	// never opened and a path no pin licenses fails loudly at the read.
	sources := map[string][]string{}
	crossRegion := 0
	for _, a := range tab.Arms {
		if a.Path == "" {
			t.Fatalf("arm %#02x/%#x cites line %d of no file: Arm.Path is the citation's other "+
				"half and nothing stamped it", a.Prefix, a.Code, a.Line)
		}
		if a.Path != auth[a.Prefix] {
			crossRegion++
		}
		lines, read := sources[a.Path]
		if !read {
			lines = strings.Split(testenv.RequireSpecRef(t, a.Path), "\n")
			sources[a.Path] = lines
		}
		if a.Line < 1 || a.Line > len(lines) {
			t.Errorf("arm %#02x/%#x cites %s:%d, which has %d lines",
				a.Prefix, a.Code, a.Path, a.Line, len(lines))
			continue
		}
		// The head may wrap across lines (parseArm's reHeadContinues), so the citation is
		// resolved against the joined head rather than the single line — the same joining the
		// extractor did when it recorded this number.
		head := lines[a.Line-1]
		for i := a.Line; reHeadContinues.MatchString(head) && i < len(lines); i++ {
			head += " " + strings.TrimSpace(lines[i])
		}
		found := false
		for _, m := range reHexCode.FindAllStringSubmatch(head, -1) {
			if v, err := strconv.ParseUint(m[1], 16, 32); err == nil && uint32(v) == a.Code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("arm %#02x/%#x cites %s:%d, whose head names no such code — a citation that "+
				"resolves to the wrong arm reads as an audited one:\n\t%s",
				a.Prefix, a.Code, a.Path, a.Line, strings.TrimSpace(head))
		}
	}
	// The cross-region rows are the population this control exists for, and a zero here would
	// mean it had been reduced to re-checking what a single extraction already guarantees. One
	// per overlaid region: the escape arm.
	if got, want := crossRegion, len(tab.Overlays); got != want {
		t.Errorf("%d arms whose authority differs from their region's, want %d (one escape arm per "+
			"overlay) — at 0 this control is asserting only what a per-source version would", got, want)
	}
	if len(sources) < 2 {
		t.Errorf("resolved citations against %d file(s), want >=2: a composed table whose arms all "+
			"name one authority is not the table this checks", len(sources))
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
		// Extract is handed a string and cannot know its path; Emit refuses a region with no
		// authority and an arm citing no file, so the stamp its caller would apply is applied
		// here too — through StampPath, which is the only way to set one without the other.
		tab.StampPath(refPath)
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
//
// The table is minimal but not *under*-specified: the path and Regions are set because Emit
// refuses a region with no authority path *and* an arm citing no file, and a fixture that
// tripped either refusal instead would pass this test while proving nothing about immediates.
// Same reason the mnemonic is a real one — the failure has to be the declared one. The stamp
// comes after the arms exist, since StampPath walks them.
func TestEmitRejectsAnImmediateWithNoIdentifier(t *testing.T) {
	tab := &Table{SourceSHA: "test", Regions: []byte{0x00}, Arms: []Arm{
		{Prefix: 0, Code: 0x28, Mnemonic: "i32_load", Imms: []Imm{"a_reader_nobody_declared"}, Line: 1},
	}}
	tab.StampPath(refPath)
	_, err := tab.Emit()
	if err == nil {
		t.Fatal("Emit accepted an immediate with no generated identifier; it would have written " +
			"an empty imms list, silently narrowing the shape")
	}
	if !strings.Contains(err.Error(), "a_reader_nobody_declared") {
		t.Fatalf("Emit failed with %v, which does not name the undeclared immediate — the "+
			"fixture tripped some other refusal and this test is asserting nothing about the "+
			"vocabulary coupling it is named for", err)
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
//
// **Through BuildFromPins, not Extract.** It read the core pin alone until the atomics region
// landed, and then reported the committed 610-arm table as disagreeing with a 542-arm
// extraction — a drift failure whose content was "you added a region", said in the voice of
// "upstream moved". That is the coupling BuildFromPins exists to hold: the generator and its
// drift check compose the table by one function, or the check measures a table nobody ships.
func TestCommittedTableMatchesTheReference(t *testing.T) {
	requireEveryPinFetched(t)
	sha, err := gen.PinnedRefRev()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := BuildFromPins()
	if err != nil {
		t.Fatalf("BuildFromPins: %v", err)
	}
	want, err := tab.Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	path, err := gen.FromRoot(Output)
	if err != nil {
		t.Fatal(err)
	}
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
