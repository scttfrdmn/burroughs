package binary

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The accept direction of 0006's property 1, and the reason it exists is that this file's
// neighbour said it could not (grave #495).
//
// `optable_agreement_test.go` covers property 1 in one direction — every opcode the decoder admits
// in a constant expression must *exist* in decode.ml and must not be an arm the reference rejects —
// and its header explains the missing half like this:
//
//	the table records *existence and immediate shape*; it does not record const-legality,
//	which is a validation-layer fact the reference does not encode either (its `const s` is
//	the full instruction grammar, decode.ml:983)
//
// **The subordinate clause is true of decode.ml and false of the reference.** `decode.ml` is the
// parser; const-legality is a *validation* fact, so of course it is not there — and the reference
// encodes it in the file that owns validation, `valid.ml`, as a ten-line pattern match called
// `is_const` (`:1029-1038`). The claim was checked against the wrong authority and then stated as
// a property of the whole reference, which is the shape *a valid citation does not certify its
// sentence* has when the citation is to a file: `decode.ml:983` resolves, says what it is quoted as
// saying, and the sentence built on it generalizes past what it can support.
//
// The cost of the unasserted half was 296 wrongly-admitted arms (#471), and the board saw 2 of them.
//
// # The join, and why it is three files rather than a transcription
//
// `is_const` matches on *AST constructors*, and the generated table speaks decode.ml's *mnemonics*.
// Nothing in either file connects the two, so the classification is recovered by joining a third
// authority — `mnemonics.ml`, whose 499 one-line bindings are exactly the mnemonic→constructor map
// (`let struct_new x = StructNew (x, Explicit)`). That is the same three-authority join
// `internal/validate`'s `signature` performs for typing, and it is licensed for the same reason:
// the alternative is hand-classifying opcodes by name, whose errors are accept-direction and
// invisible on a board (§9 G-3's rider).
//
// **A pattern carries conditions a predicate drops, and `Binary` is the specimen.** Eight of
// `is_const`'s alternatives name a constructor with a wildcard argument, so the constructor *head*
// decides. The two `Binary` alternatives do not: they admit `I32Op.(Add | Sub | Mul)` and the
// I64 trio, and nothing else in a family of 60-odd. So a head-keyed join would admit `i32.div_s`
// in a global initializer — the exact accept-direction defect this file is the control for. The
// arms are therefore expanded to fully-applied form and compared as text against `mnemonics.ml`'s
// right-hand sides, which is why `expandAlternatives` exists rather than a set of head names.
//
// # Watched die
//
// Four mutations, run before the file was committed, because *a control isn't born until it's
// watched die* — and the last two matter most, because they falsify design claims rather than data:
//
//   - **`{0xFB, 0x1C}` → `{0xFB, 0x1D}`** (ref.i31 dropped, `i31.get_s` admitted in its place — the
//     plausible off-by-one). Both directions named, by mnemonic, and
//     `TestConstPrefixedOpsAreIsConstsPrefixedArms` named the drop as well but *not* the addition:
//     its member count was still nine, so the reference join is what sees a swap.
//   - **`{0xFB, 0x06}` → `{0xFB, 0x09}`** (array.new transcribed as array.new_data, the confusion
//     `TestConstPrefixedOpsAreIsConstsPrefixedArms`' doc names). Named here, and the all-gates-on
//     board moved too — 65109 pass → 64998, 0 fail → 111 — so this one had an existing oracle.
//   - **`arms.heads[head] = true` added beside the applied expansion**, collapsing `Binary` to its
//     head, which is the implementation the file comment above argues against. 38 arms named,
//     `i32.div_s` first. Without that finding the paragraph on `expandAlternatives` would be a
//     plausible argument for a line nothing depends on.
//   - **`prefixed`'s const call site removed** — the pre-#471 engine restored, the actual defect
//     rather than a transcription of it. `TestPrefixedNonConstOpsGetTheConstVerdict` named 296 arms.
//     **Every test above passed.** That is the finding, and it is why the fourth test exists: three
//     controls that read tables agree perfectly with a decoder that consults no table at all. The
//     table was never the bug — *a control can test the helper while nothing calls the path* — and a
//     file whose whole content was the reference join would have shipped saying `constPrefixedOps`
//     is right, which it was, about an engine that ignored it.
//
// # What it still cannot see
//
// `GlobalGet` is admitted *conditionally* — `mut = Cons`, an immutable global — and that condition
// is not a decoder fact at all. The decoder admits 0x23 unconditionally and
// `internal/validate`'s `checkConstGlobals` owns the mutability half; this control therefore counts
// `GlobalGet` as admitted and asserts nothing about the condition. That split is the declared
// layering debt, stated at `internal/validate/module.go`'s `checkConstGlobals`, not a gap here.

var (
	refValidML     = filepath.Join("..", "..", testenv.RefValidML)
	refMnemonicsML = filepath.Join("..", "..", testenv.RefMnemonicsML)
)

// isConstArms is `is_const`'s pattern, read from valid.ml and expanded.
//
// `heads` are the alternatives whose argument is a wildcard, so the constructor name decides;
// `applied` are the fully-applied ones (`Binary (I32 I32Op.Add)`) compared as normalized text;
// `conditional` are the arms with a guard body rather than `-> true`, which is `GlobalGet` alone.
type isConstArms struct {
	heads       map[string]bool
	applied     map[string]bool
	conditional map[string]bool
}

// parseIsConst reads the `is_const` function out of valid.ml.
//
// Derived from the function's own text rather than from a list of nine constructors, because a
// list is a transcription and an upstream tenth arm would leave it silently short — the *reverse*
// of #471's defect and equally invisible, since a const set narrower than the reference's rejects
// valid modules that no vector can catch.
func parseIsConst(tb testing.TB) isConstArms {
	tb.Helper()

	src := testenv.RequireSpecRef(tb, refValidML)
	lines := strings.Split(src, "\n")

	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "let is_const ") {
			start = i
			break
		}
	}
	if start < 0 {
		tb.Fatalf("no `let is_const` in %s: the authority for const-legality has moved or been "+
			"renamed, and a walk that cannot find it would report agreement with an empty set",
			refValidML)
	}

	// The body runs to `| _ -> false`, the catch-all every OCaml match of this shape ends with.
	end := -1
	for i := start; i < len(lines); i++ {
		if strings.Contains(lines[i], "| _ -> false") {
			end = i
			break
		}
	}
	if end < 0 {
		tb.Fatalf("`is_const` at %s:%d has no `| _ -> false` catch-all: the function's shape has "+
			"changed and this parse cannot bound its body", refValidML, start+1)
	}
	body := strings.Join(lines[start:end+1], "\n")

	arms := isConstArms{
		heads:       map[string]bool{},
		applied:     map[string]bool{},
		conditional: map[string]bool{},
	}

	// The unconditional half: everything between `match e.it with` and the first `-> true`.
	_, after, ok := strings.Cut(body, "match e.it with")
	if !ok {
		tb.Fatalf("`is_const` at %s:%d has no `match e.it with`", refValidML, start+1)
	}
	unconditional, rest, ok := strings.Cut(after, "-> true")
	if !ok {
		tb.Fatalf("`is_const` at %s:%d has no `-> true` arm: every alternative this control reads "+
			"is on that arm, so its absence means the function is shaped differently now",
			refValidML, start+1)
	}
	for _, alt := range splitAlternatives(unconditional) {
		head, detail := cutHead(alt)
		if head == "" {
			continue
		}
		if detail == "" || detail == "_" {
			arms.heads[head] = true
			continue
		}
		for _, a := range expandAlternatives(normalizeOCaml(alt)) {
			arms.applied[a] = true
		}
	}

	// The conditional half: arms after `-> true` whose body is a guard rather than `true`.
	// `GlobalGet x -> let GlobalT (mut, _t) = global c x in mut = Cons` is the only one at the
	// pin, and it is read from the text so a second would be picked up rather than dropped.
	for _, alt := range splitAlternatives(rest) {
		head, _ := cutHead(alt)
		if head == "" || head == "_" {
			continue
		}
		arms.conditional[head] = true
	}
	return arms
}

// splitAlternatives splits an OCaml pattern on `|` at paren depth zero.
//
// Depth-aware because `Binary (Value.I32 I32Op.(Add | Sub | Mul))` contains two `|` that are not
// alternative separators — and a naive `strings.Split` on them yields three fragments that parse
// as nothing and drop the whole arm, which is a *narrower* const set: an accept-direction failure
// in the control written to find accept-direction failures.
func splitAlternatives(s string) []string {
	var out []string
	depth, last := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[last:i]))
				last = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[last:]))

	kept := out[:0]
	for _, alt := range out {
		// An arm's body ends up in the tail fragment (`-> let GlobalT ... in mut = Cons`); keep
		// only the pattern before the arrow.
		if pat, _, ok := strings.Cut(alt, "->"); ok {
			alt = strings.TrimSpace(pat)
		}
		if alt != "" {
			kept = append(kept, alt)
		}
	}
	return kept
}

// cutHead splits a pattern into its constructor name and the rest.
func cutHead(alt string) (head, detail string) {
	alt = strings.TrimSpace(alt)
	i := strings.IndexAny(alt, " (")
	if i < 0 {
		return alt, ""
	}
	return alt[:i], strings.TrimSpace(alt[i:])
}

// normalizeOCaml puts a pattern and a mnemonics.ml right-hand side into one spelling so they can be
// compared as text: single-spaced, and with the `Value.` module qualifier dropped.
//
// The qualifier is the only spelling difference between the two files at the pin — `valid.ml` opens
// less of `Value` than `mnemonics.ml` does — and normalizing it away is safe *because it is
// checked*: a comparison that silently found nothing in common would be caught by the exact
// membership counts below rather than passing as agreement.
func normalizeOCaml(s string) string {
	s = strings.ReplaceAll(s, "Value.", "")
	return strings.Join(strings.Fields(s), " ")
}

// expandAlternatives rewrites OCaml's `Mod.(A | B | C)` group syntax into one string per member.
//
// `Binary (I32 I32Op.(Add | Sub | Mul))` becomes the three fully-applied forms mnemonics.ml writes
// out one per line, which is what makes the extended-const six derivable rather than transcribed.
func expandAlternatives(s string) []string {
	open := strings.Index(s, ".(")
	if open < 0 {
		return []string{s}
	}
	// The matching close paren for the group.
	depth, closeAt := 0, -1
	for i := open + 1; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				closeAt = i
			}
		}
		if closeAt >= 0 {
			break
		}
	}
	if closeAt < 0 {
		return []string{s}
	}
	members := splitAlternatives(s[open+2 : closeAt])
	out := make([]string, 0, len(members))
	for _, m := range members {
		// `I32Op.(Add | Sub | Mul)` → `I32Op.Add`, keeping the module prefix that precedes `.(`.
		expanded := s[:open+1] + m + s[closeAt+1:]
		out = append(out, expandAlternatives(normalizeOCaml(expanded))...)
	}
	return out
}

// mnemonicConstructors is mnemonics.ml's mnemonic→constructor map, normalized.
//
// The key is the mnemonic decode.ml uses and `opInfo.mnemonic` carries, so this is the join key
// between the two authorities. The value is the whole right-hand side, because `Binary` needs the
// applied form and a head alone would drop the condition (see the file comment).
func mnemonicConstructors(tb testing.TB) map[string]string {
	tb.Helper()

	src := testenv.RequireSpecRef(tb, refMnemonicsML)
	lines := strings.Split(src, "\n")

	out := map[string]string{}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if !strings.HasPrefix(ln, "let ") {
			continue
		}
		decl, rhs, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		// A binding whose right-hand side is on the next line — every load and store is written
		// that way. Joined rather than skipped: skipping is how a join's coverage quietly falls,
		// and these are 60-odd of the 499 bindings.
		if strings.TrimSpace(rhs) == "" && i+1 < len(lines) {
			i++
			rhs = lines[i]
		}
		name := strings.Fields(strings.TrimPrefix(decl, "let "))
		if len(name) == 0 {
			continue
		}
		out[name[0]] = normalizeOCaml(rhs)
	}
	if len(out) < 400 {
		tb.Fatalf("parsed %d bindings from %s; there are 499 `let` lines at the pin, and a join "+
			"table this short would classify most of the opcode space as non-const by default — "+
			"which is the silent direction", len(out), refMnemonicsML)
	}
	return out
}

// dispatchableOps is every opcode identity `instr` and `prefixed` can dispatch on: the single-byte
// arms that are neither absent, illegal, escapes nor delimiters, plus every prefixed arm that is
// neither illegal nor an escape.
//
// **This is the domain 0006's property 1 named** — *"every one of the 256 single-byte opcodes and
// every tracked multi-byte prefix"* — derived from the two dispatch structures rather than
// enumerated, so a region growing upstream widens the walks instead of leaving them at today's size.
func dispatchableOps(tb testing.TB) [][2]uint32 {
	tb.Helper()

	var out [][2]uint32
	for code, info := range opTable {
		if info.illegal || info.escape || info.reason != "" {
			continue
		}
		out = append(out, opKey(0x00, code))
	}
	for prefix, region := range prefixRegions {
		if prefix == 0x00 {
			continue // the "no prefix" pseudo-entry, which is opTable itself
		}
		for sub, info := range region {
			if info.illegal || info.escape {
				continue
			}
			out = append(out, opKey(prefix, sub))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// TestConstLegalSpaceMatchesIsConst is property 1 in **both** directions, over the whole
// dispatchable space, against the authority that actually encodes const-legality.
//
// The reject direction (the decoder admits something the reference does not) is #471's defect and
// was worth 296 arms. The accept direction (the reference admits something the decoder does not) is
// #109's defect and was worth nine valid modules; no vector in the suite can see it, which is why
// the two directions are one test rather than two — the same derivation answers both, and splitting
// them would let one be added without the other.
func TestConstLegalSpaceMatchesIsConst(t *testing.T) {
	arms := parseIsConst(t)
	ctors := mnemonicConstructors(t)
	mine := constLegalOps(t)
	domain := dispatchableOps(t)

	// The membership counts, stamped rather than deduced: nine wildcard heads, six fully-applied
	// `Binary` forms, one conditional arm. Pinned because every assertion below is a comparison
	// *against* this set, so a parse that silently produced fewer would report the decoder as
	// over-admitting and name the wrong cause.
	const (
		wantHeads       = 9 // Const VecConst RefNull RefFunc RefI31 StructNew ArrayNew ArrayNewFixed ExternConvert
		wantApplied     = 6 // I32 and I64 × Add Sub Mul — extended-const's set, derived
		wantConditional = 1 // GlobalGet, whose `mut = Cons` guard belongs to internal/validate
	)
	if len(arms.heads) != wantHeads || len(arms.applied) != wantApplied ||
		len(arms.conditional) != wantConditional {
		t.Fatalf("is_const parsed as %d wildcard heads / %d applied / %d conditional, want %d/%d/%d\n\t"+
			"heads=%v applied=%v conditional=%v\n\t"+
			"the pattern's shape at the pin is the premise of every comparison below",
			len(arms.heads), len(arms.applied), len(arms.conditional),
			wantHeads, wantApplied, wantConditional,
			sortedKeys(arms.heads), sortedKeys(arms.applied), sortedKeys(arms.conditional))
	}

	joined, refConst := 0, map[[2]uint32]bool{}
	var unjoined []string
	for _, k := range domain {
		info, ok := infoFor(k)
		if !ok {
			t.Errorf("%s is dispatchable but has no table entry: dispatchableOps and infoFor "+
				"disagree about the same two structures", renderKey(k))
			continue
		}
		rhs, ok := ctors[info.mnemonic]
		if !ok {
			unjoined = append(unjoined, info.mnemonic)
			continue
		}
		joined++
		head, _ := cutHead(rhs)
		if arms.heads[head] || arms.conditional[head] || arms.applied[rhs] {
			refConst[k] = true
		}
	}

	// The join's coverage, asserted as the *count that resolved* rather than as a required zero
	// of failures. Both spellings are the same fact; only this one is a measurement — a demanded
	// zero can be satisfied by a domain that shrank, and this cannot.
	//
	// It comes out total, and that was not the forecast: the pre-registered figure was four
	// unjoined arms (block, loop, if, try_table, guessed to be built inline by decode.ml). The
	// forecast was wrong for a reason worth the sentence, because it is *why* the join is exact —
	// `opInfo.mnemonic` is not a rendering of the instruction's name, it is **mnemonics.ml's
	// binding identifier, carried through the generator verbatim**, down to the OCaml keyword
	// dodge: the table spells 0x04 `if_` (optable.go:77) because line 29 of mnemonics.ml does.
	// So the join key is upstream's own identifier and every arm resolves.
	//
	// Which leaves the assertion doing real work in one direction: an arm that stops resolving
	// means the generated table's vocabulary and mnemonics.ml have diverged — a rename upstream
	// with a stale regeneration — and the arm would then be classified non-const by omission,
	// rejecting valid modules in the direction no vector sees.
	if joined != len(domain) {
		sort.Strings(unjoined)
		t.Errorf("%d of %d dispatchable arms joined through mnemonics.ml, want all of them; "+
			"unresolved mnemonics: %v\n\t"+
			"`opInfo.mnemonic` is mnemonics.ml's binding identifier, so a name that does not "+
			"resolve means the generated table is stale against %s — and an unjoined arm is "+
			"classified non-const by omission, which rejects valid modules",
			joined, len(domain), unjoined, refMnemonicsML)
	}

	for _, k := range domain {
		switch {
		case refConst[k] && !mine[k]:
			t.Errorf("%s is const-legal in the reference (is_const, %s) and not in this decoder: "+
				"a constant expression using it is *valid* and would be rejected — the "+
				"accept-direction failure #109 was, which no assert_malformed vector can see "+
				"(§9 G-3's rider)", renderKey(k), refValidML)
		case mine[k] && !refConst[k]:
			t.Errorf("%s is const-legal in this decoder and not in the reference (is_const, %s): "+
				"a constant expression using it must be refused with `constant expression "+
				"required` — this is #471's defect, which was worth 296 arms against 2 board rows",
				renderKey(k), refValidML)
		}
	}

	if len(refConst) != len(mine) {
		t.Errorf("the reference admits %d dispatchable arms in a constant expression, this decoder "+
			"admits %d: the per-arm findings above name which", len(refConst), len(mine))
	}
	if !t.Failed() {
		t.Logf("%d dispatchable arms (%d joined through mnemonics.ml), %d const-legal in both the "+
			"reference and this decoder", len(domain), joined, len(refConst))
	}
}

// TestConstLegalSpaceIsTheWholeDispatchableSpace pins the domain the walks reach, which is the half
// of grave #495 no per-arm assertion can state.
//
// The defect was not a wrong verdict about an arm — it was a *domain* 305 arms narrower than the one
// 0006's property 1 named, fixed there by a `map[byte]bool` and therefore invisible to review: there
// was no list to be missing an entry. So the repair needs an assertion about the size of the space,
// printed and pinned, in the direction the type used to bound.
func TestConstLegalSpaceIsTheWholeDispatchableSpace(t *testing.T) {
	domain := dispatchableOps(t)

	single, prefixed := 0, 0
	for _, k := range domain {
		if k[0] == 0x00 {
			single++
			continue
		}
		prefixed++
	}

	// Stamped at the pin, and the prefixed figure is the one that matters: it is what a byte-keyed
	// helper could not name. Both halves are exact — *floors bound the catastrophic case; only an
	// exact count sees a small silent loss*, and a region shrinking by one arm is exactly that.
	const (
		wantSingle   = 192
		wantPrefixed = 305
	)
	if single != wantSingle || prefixed != wantPrefixed {
		t.Errorf("the dispatchable space is %d single-byte + %d prefixed arms, want %d + %d: "+
			"re-base in the PR that moves it, and if the prefixed figure *fell*, check that a "+
			"region was not dropped from prefixRegions — the const walks would then narrow "+
			"silently, which is grave #495's mechanism", single, prefixed, wantSingle, wantPrefixed)
	}

	// The const-legal space is a subset of what the decoder can dispatch on. A member outside it
	// would be an opcode admitted in a constant position that no dispatch path can reach.
	in := map[[2]uint32]bool{}
	for _, k := range domain {
		in[k] = true
	}
	for k := range constLegalOps(t) {
		if !in[k] {
			t.Errorf("const-legal %s is not in the dispatchable space: it is absent, illegal, an "+
				"escape or a delimiter, so nothing can reach the const check for it", renderKey(k))
		}
	}
	t.Logf("dispatchable space: %d single-byte + %d prefixed = %d arms", single, prefixed, len(domain))
}

// TestConstPrefixedOpsAreIsConstsPrefixedArms is the names-are-not-encodings join for the nine, in
// `TestExtendedConstOpsAreTheProposalsSix`'s shape.
//
// The comparison above works entirely in mnemonic space, so it would agree with a `constPrefixedOps`
// whose *keys* were wrong in a compensating way — a transcription slip putting `0xFB 0x09`
// (`array.new_data`) where `0xFB 0x06` (`array.new`) belongs is a wrong opcode with a right-looking
// comment, and `array.new_data` is precisely one of the two arms `array.wast:302,315` use. So each
// key is read back through the public accessor and its mnemonic asserted.
//
// It also checks the map comment's *gate* claim, which is load-bearing for the ordering: the comment
// says these nine read no gate here because `prefixed` has already declined them by name, and that
// is only true while every one of them is a `gateFor` row.
func TestConstPrefixedOpsAreIsConstsPrefixedArms(t *testing.T) {
	want := map[string][2]uint32{
		"struct_new":         {0xFB, 0x00},
		"struct_new_default": {0xFB, 0x01},
		"array_new":          {0xFB, 0x06},
		"array_new_default":  {0xFB, 0x07},
		"array_new_fixed":    {0xFB, 0x08},
		"any_convert_extern": {0xFB, 0x1A},
		"extern_convert_any": {0xFB, 0x1B},
		"ref_i31":            {0xFB, 0x1C},
		"v128_const":         {0xFD, 0x0C},
	}
	if len(constPrefixedOps) != len(want) {
		t.Errorf("constPrefixedOps has %d members, want %d: the reference's prefixed const arms are "+
			"nine because three of its constructors cover two opcodes each (StructNew, ArrayNew, "+
			"ExternConvert) — a count taken from is_const's arms alone reads six",
			len(constPrefixedOps), len(want))
	}
	for mnemonic, key := range want {
		// Rendered by the production formatter here, deliberately — unlike `hex2`, which spells the
		// reference's `%02x` by hand because a test that formats with the code under test cannot
		// catch it. That rule is about the *rendering* being the subject; here the subject is the
		// key, and this string is only how the failure names it.
		id := opID{prefix: byte(key[0]), sub: key[1]}
		if !constPrefixedOps[opKey(id.prefix, id.sub)] {
			t.Errorf("constPrefixedOps lacks %s (%s): the reference admits it in a constant "+
				"expression, so omitting it rejects a valid module", id, mnemonic)
			continue
		}
		got, _, ok := PrefixedOp(id.prefix, id.sub)
		if !ok {
			t.Errorf("%s is in constPrefixedOps but the table has no arm for it", mnemonic)
			continue
		}
		if got != mnemonic {
			t.Errorf("constPrefixedOps' comment calls %s %q; the table calls it %q — a key is a "+
				"transcription and the comment beside it cannot check itself", id, mnemonic, got)
		}
	}
	for k := range constPrefixedOps {
		if _, ok := gateFor(byte(k[0]), k[1]); !ok {
			t.Errorf("%s is const-legal and prefixed but not a gateFor row: constPrefixedOps' "+
				"comment says these read no gate because `prefixed` has already declined them by "+
				"name, and that argument holds only while every one of them is mapped", renderKey(k))
		}
	}
}

// TestPrefixedNonConstOpsGetTheConstVerdict is the same walk down the **production path**, and it is
// here because the three tests above all read tables.
//
// `constPrefixedOps` agreeing with `is_const` says nothing about whether `prefixed` consults it —
// *a control can test the helper while nothing calls the path* — and #471's defect was exactly a
// missing call, not a wrong table. So this builds a real image per arm, runs `decodeConstExpr`, and
// asks for the verdict.
//
// It is `TestEveryNonConstByteGetsTheRightVerdict`'s released half, over the 305 prefixed arms that
// walk reaches only as far as the escape byte. Two things are asserted per arm and the second is the
// one with a history: the error must be `ErrConstExprRequired`, and it must **not** say `malformed`
// — a well-formed module refused in the malformed voice is the §9 G-3 shape, and 0008's order puts
// the const verdict last precisely so a real truncation can outrank it.
func TestPrefixedNonConstOpsGetTheConstVerdict(t *testing.T) {
	d := constVerdictDecoder(t) // every gate on: a decline would answer a different question
	c := &instrCtx{d: d}

	var total, nonConst, legal, declined int
	for _, k := range dispatchableOps(t) {
		if k[0] == 0x00 {
			continue
		}
		total++
		info, ok := infoFor(k)
		if !ok {
			continue // membership is the reference join's assertion, not this one's
		}

		// `<prefix> <uleb sub> <immediates> END`, with the immediates *measured* off the
		// production reader rather than encoded here — `wellFormedExpr`'s reason, applied to the
		// prefixed path: a builder that knew each width would be another copy of the fact the
		// generated table exists to hold, and would agree with a wrong reader by construction.
		img, built := wellFormedPrefixedExpr(c, byte(k[0]), k[1], info)
		if !built {
			declined++
			continue
		}
		err := constExprErr(d, &reader{b: img, eof: ErrPayloadEnd})

		if constPrefixedOps[k] {
			if err != nil {
				t.Errorf("%s: % x is a constant expression the reference admits (is_const), and it "+
					"was refused with %v — the accept-direction failure, invisible to every "+
					"assert_malformed vector", renderKey(k), img, err)
			}
			legal++
			continue
		}

		if !errors.Is(err, ErrConstExprRequired) {
			t.Errorf("%s: % x is a well-formed expression containing a non-const instruction; want "+
				"ErrConstExprRequired, got %v\n\t"+
				"this is #471 itself: `prefixed` did not consult the const table at all, so all 305 "+
				"arms here decoded clean", renderKey(k), img, err)
		}
		if err != nil && strings.Contains(err.Error(), "malformed") {
			t.Errorf("%s: error %q says malformed for a module the spec calls well-formed", renderKey(k), err)
		}
		nonConst++
	}

	// The coverage guard, as an exact partition rather than a fraction, because a *decline* is this
	// walk's silent failure: `wellFormedPrefixedExpr` returning false skips an arm with no error, and
	// a builder that declined everywhere would leave a passing test that asked nothing. It comes out
	// clean — 296 + 9 + 0 — and the zero is worth pinning rather than tolerating, since it says the
	// immediates of every arm in three sub-tables are readable from a uniform fill.
	//
	// 296 is #471's own figure, arrived at from the production path here rather than by counting the
	// table: the same number the issue reported.
	const (
		wantNonConst = 296
		wantLegal    = 9
		wantDeclined = 0
	)
	if nonConst != wantNonConst || legal != wantLegal || declined != wantDeclined {
		t.Errorf("prefixed const walk: %d refused / %d admitted / %d declined an image, want %d/%d/%d\n\t"+
			"a rise in `declined` is the silent direction — a skipped arm is an unasserted arm",
			nonConst, legal, declined, wantNonConst, wantLegal, wantDeclined)
	}
	if got := nonConst + legal + declined; got != total {
		t.Errorf("classified %d of %d prefixed arms: the difference is arms `infoFor` could not "+
			"resolve, which leave the loop counted by nothing", got, total)
	}
	t.Logf("%d prefixed arms refused as non-const, %d admitted, %d declined an image",
		nonConst, legal, declined)
}

// wellFormedPrefixedExpr is `wellFormedExpr` for a sub-table arm.
//
// Separate rather than a widened signature, because the two differ in the byte they hand `imms`:
// the single-byte path passes the opcode (`immBlock` and friends need it) and `prefixed` passes
// 0x00. That is `immsOpArg`'s distinction, and collapsing the two builders would have to pick one
// and be wrong on the other half of the space.
func wellFormedPrefixedExpr(c *instrCtx, prefix byte, sub uint32, info opInfo) ([]byte, bool) {
	for _, fill := range []byte{0x00, 0x70} {
		buf := make([]byte, 64)
		for i := range buf {
			buf[i] = fill
		}
		r := &reader{b: buf, eof: ErrPayloadEnd}
		if err := c.imms(r, 0x00, info.imms); err != nil {
			continue
		}
		img := append([]byte{prefix}, ulebBytes(sub)...)
		img = append(img, buf[:r.off]...)
		return append(img, opEnd), true
	}
	return nil, false
}

// sortedKeys is for failure messages: a set printed in map order reads as a different set each run.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
