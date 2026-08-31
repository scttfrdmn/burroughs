// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The natural-width authority controls, and why `naturalWidth` needs one at all.
//
// `naturalWidth` reads a number out of a mnemonic. That is `sig.go`'s move and it is here for
// `sig.go`'s reason — a hand-written row per opcode is 45 chances to be wrong — but the reason
// carries the obligation with it: the *derivation* can be wrong in exactly the way a transcription
// can, once, for all 45 rows, and its errors point the same direction. An over-wide natural admits
// an over-aligned access, which is `assert_invalid` reported as valid: the accept-direction failure
// no board can see (§9 G-3), and the failure #306 exists because of.
//
// So the width is re-derived here from the authority, by a path that shares nothing with the
// implementation:
//
//   - **`syntax/mnemonics.ml`** gives each of the 45 constructors its family, its `ty` and its
//     `pack`. This is the same file `TestVecFamiliesMatchTheReference` already reads, so the parse
//     target is not new — a fetched artifact, machine-readable, and the reference's own record of
//     which opcode carries which pack.
//   - **`valid/valid.ml`** gives each family its `get_sz`, captured as text and matched against the
//     four forms the reference uses. An unrecognized form is a **fatal** error rather than a skipped
//     row: a family whose `get_sz` this test cannot read is a family whose width it must not claim to
//     have checked, and silently passing it is the shape where a control's domain shrinks without its
//     verdict changing.
//   - **`packed_size`, `num_size` and `vec_size` are transcribed** — nine numbers, in `refSizes`
//     below — and that is the one place this file does not derive, deliberately. The
//     derive-don't-transcribe rule is about errors that are *accept-direction and invisible*: a
//     wrong row in `naturalWidth` admits an over-aligned access and the board scores it green. A
//     wrong number **here** cannot do that. It disagrees with an implementation derived by a
//     different path, and the test fails. So licensing `syntax/pack.ml` and `syntax/types.ml` as two
//     more authorities would buy a governance step and no coverage, and the honest form of the rule
//     is to say which direction the risk points rather than to apply it everywhere by reflex.
//     `refSizes` is keyed by the reference's constructor names, so an upstream `Pack128` arrives as
//     a missing key and a fatal error rather than as a default.
//
// The domain is checked in both directions against `binary.HasMemarg`, which is the *engine's* claim
// about which rows carry a memarg. A row the reference constructs and the table does not mark is a
// generated-table defect; a row the table marks and the reference does not construct is a mnemonic
// this file cannot bound. Either way the alignment rule has a row it is not applying, and a count
// alone would not say which.

// refMemop is one memarg-carrying constructor as `mnemonics.ml` writes it.
type refMemop struct {
	name   string // `i32_load8_s`, the table's own spelling
	family string // `Load`, `VecLoadLane`, …
	ty     string // `I32T`, `V128T`
	pack   string // the pack expression, verbatim: `None`, `Some (Pack8, S)`, `Pack16`, `()`
}

// TestNaturalWidthsMatchTheReference re-derives all 45 natural widths from the reference and
// compares them to `naturalWidth`'s.
func TestNaturalWidthsMatchTheReference(t *testing.T) {
	rows := parseRefMemops(t)
	getSz := parseGetSzForms(t)

	// Vacuity, three ways, because this test compares two derived sets and an empty one on either
	// side agrees with anything. The 45 is the *authority's* count and it is pinned exactly rather
	// than floored: a floor bounds the catastrophic case — a moved file, a broken regexp — and says
	// nothing about six rows quietly dropped by a tightened pattern.
	const wantRows = 45
	if len(rows) != wantRows {
		t.Fatalf("parsed %d memarg constructors from %s, want %d (23 core loads and stores, 22 "+
			"vector); a different count means the reference's set moved or this parse narrowed",
			len(rows), testenv.RefMnemonicsML, wantRows)
	}
	// Six families, and the six are the reference's own: Load, Store, VecLoad, VecStore,
	// VecLoadLane, VecStoreLane. Pinned because `parseGetSzForms` keys on `check_memop` call sites,
	// and a rename upstream would produce a short map that reads as "no such family" per row.
	const wantFamilies = 6
	if len(getSz) != wantFamilies {
		t.Fatalf("parsed %d check_memop call sites from %s, want %d; the families are the six arms "+
			"that call it", len(getSz), testenv.RefValidML, wantFamilies)
	}

	for _, row := range rows {
		form, ok := getSz[row.family]
		if !ok {
			t.Fatalf("%s: %s has no check_memop call site in %s, so its get_sz is unknown and its "+
				"natural width cannot be derived", row.name, row.family, testenv.RefValidML)
		}
		sz, hasSz, err := applyGetSz(form, row.pack)
		if err != nil {
			// Fatal, not an Errorf that moves on: see the header. A get_sz form this test cannot
			// read is a row it must not report as checked.
			t.Fatalf("%s (%s): %v", row.name, row.family, err)
		}

		// `check_memop`'s own branch: a pack decides, otherwise the value type does. The two
		// lookups share one table because the reference's three size functions are disjoint on
		// their key sets — no constructor is both a packsize and a valtype — so a single map
		// cannot silently answer the wrong question.
		key := row.ty
		if hasSz {
			key = sz
		}
		want, ok := refSizes[key]
		if !ok {
			t.Fatalf("%s: %q has no size in refSizes, so the reference grew a constructor this "+
				"test does not know; add it from pack.ml or types.ml rather than defaulting",
				row.name, key)
		}

		got, hasWidth := naturalWidth(row.name)
		if !hasWidth {
			t.Errorf("naturalWidth(%q) declines, and the reference gives it %d bytes — a memarg row "+
				"with no width reaches errNoNaturalWidth and its alignment goes unchecked",
				row.name, want)
			continue
		}
		if got != want {
			t.Errorf("naturalWidth(%q) = %d, reference = %d (%s, ty %s, pack %s). A width that is "+
				"too large admits an over-aligned access, which is an invalid module reported valid",
				row.name, got, want, row.family, row.ty, row.pack)
		}
	}
}

// TestEveryMemargRowHasANaturalWidth is the domain check, and it is what makes
// `errNoNaturalWidth` an unreachable internal error rather than a hope.
//
// Both directions. `binary.HasMemarg` is the engine's claim about which rows carry a memarg, and it
// is the predicate `checkAlignment` gates on, so it — not the reference — decides which mnemonics
// this package must be able to bound. The reference's set is checked against it in the other
// direction, because a row the table forgot to mark is an access whose alignment is never looked at
// and whose absence no per-row assertion above would notice.
func TestEveryMemargRowHasANaturalWidth(t *testing.T) {
	engine := map[string]bool{}
	for op := range uint32(256) {
		if !binary.HasMemarg(0, op) {
			continue
		}
		name, ok := binary.OpMnemonic(op)
		if !ok || name == "" {
			t.Errorf("core opcode %#02x carries a memarg and has no mnemonic, so nothing can bound "+
				"its alignment", op)
			continue
		}
		engine[name] = true
	}
	// The prefixed regions, all four, rather than 0xfd alone: the domain is "rows the table marks",
	// and scoping the sweep to the region that has them today is a control scoped to the current
	// sample. Threads (0xfe) will bring memargs of its own.
	for _, prefix := range []byte{0xfb, 0xfc, 0xfd, 0xfe} {
		for op := range uint32(1024) {
			if !binary.HasMemarg(prefix, op) {
				continue
			}
			name, _, ok := binary.PrefixedOp(prefix, op)
			if !ok || name == "" {
				t.Errorf("opcode %#02x %#02x carries a memarg and has no mnemonic", prefix, op)
				continue
			}
			engine[name] = true
		}
	}

	// The floor is the reference's count, restated here as the pin the *sweep* needs: the loops
	// above bound `op` by hand, so a region growing past 1024 sub-opcodes would silently stop being
	// swept, and only a count catches that.
	//
	// 76 = the core pin's 45 (23 core loads and stores, 22 vector) + the threads pin's 31. Decomposed
	// rather than restated as one number, because the two halves have different authorities and a
	// single 76 would let a loss on one side be paid for by a gain on the other.
	const (
		wantCoreRows   = 45
		wantAtomicRows = 31
		wantRows       = wantCoreRows + wantAtomicRows
	)
	if len(engine) != wantRows {
		t.Errorf("swept %d memarg rows out of the table, want %d (%d core + %d atomic) — either a row "+
			"moved past this sweep's opcode bounds or the table's memarg set changed",
			len(engine), wantRows, wantCoreRows, wantAtomicRows)
	}

	for name := range engine {
		if _, ok := naturalWidth(name); !ok {
			t.Errorf("the table marks %q as carrying a memarg and naturalWidth declines it, so "+
				"checkAlignment reaches errNoNaturalWidth — an internal error documented as "+
				"unreachable, reached", name)
		}
	}
	// The other direction, per pin. Both authorities, because the sweep above walks the *composed*
	// table and a row missing from it is missing whichever pin contributed it — and because a
	// one-pin check here would have gone on passing while the 0xfe region's 31 rows were absent from
	// the engine's predicate entirely.
	modes, _ := parseThreadsMemopModes(t)
	for _, rows := range [][]refMemop{parseRefMemops(t), parseRefAtomicMemops(t, modes)} {
		for _, row := range rows {
			if !engine[row.name] {
				t.Errorf("the reference constructs %q with a memarg and the generated table does not "+
					"mark it, so its alignment is never checked: HasMemarg gates checkAlignment, and a "+
					"row missing from that predicate is a rule that silently does not apply", row.name)
			}
		}
	}
}

// refMemopRe matches one memarg constructor. The trailing `i` is the lane forms' extra parameter,
// and `ty`/`pack` are captured as written so `applyGetSz` reads the reference's own expression
// rather than this file's summary of it.
var refMemopRe = regexp.MustCompile(
	`let (\w+) x align offset(?: i)? =\s*\n\s*(Load|Store|VecLoad|VecStore|VecLoadLane|VecStoreLane) ` +
		`\(x, \{ty = (\w+); align; offset; pack = ([^}]*)\}`)

// parseRefMemops reads the memarg constructors out of `mnemonics.ml`.
func parseRefMemops(tb testing.TB) []refMemop {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.RefMnemonicsML)
	var rows []refMemop
	for _, m := range refMemopRe.FindAllStringSubmatch(src, -1) {
		rows = append(rows, refMemop{
			name:   m[1],
			family: m[2],
			ty:     m[3],
			pack:   strings.TrimSpace(m[4]),
		})
	}
	return rows
}

// refAtomicMemopRe matches one atomic memarg constructor in the threads pin's `operators.ml` —
// which is `mnemonics.ml` under the name it had at that baseline (see `fetch-threads-ref.sh`).
//
// Three differences from the core pattern above, all of them the older baseline's spelling rather
// than anything about atomics: the constructors take no memory index (`x`) because the baseline
// predates multi-memory, the record carries an `ordering` field, and the pack is a bare
// `Some Pack8` rather than the pair `Some (Pack8, S)`. The `rmwop` parameter is the one real shape
// difference — `AtomicRmw` carries the operator beside the memop, which is the same fact the opcode
// table records as `operator`.
//
// The family is captured as `(\w+)` rather than alternated against a list, so a constructor family
// this test does not know about arrives as an unrecognized mode below rather than as a row that
// silently fails to match.
var refAtomicMemopRe = regexp.MustCompile(
	`let (\w+) (?:rmwop )?align offset =\s*\n\s*(\w+) \(?(?:rmwop, )?\{ty = (\w+); align; offset; ` +
		`pack = ([^;]+); ordering`)

// parseRefAtomicMemops reads the threads pin's memarg constructors and keeps the ones `valid.ml`
// checks in `Atomic` mode.
//
// **The filter is the licence.** The threads pin's `operators.ml` also holds that baseline's *core*
// load and store constructors, and reading those would be the wholesale consultation §9 G-2 forbids
// — the baseline predates memory64 and multi-memory, so its core rows disagree with this engine's
// on facts nothing here is asking about. Scoping by "the families the reference itself checks in
// Atomic mode" derives the clause boundary from the authority instead of from a list written here,
// which is the same move `consultedClauses` makes one package over for the 0xfe region.
func parseRefAtomicMemops(tb testing.TB, modes map[string]string) []refMemop {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.ThreadsRefOperatorsML)
	var rows []refMemop
	for _, m := range refAtomicMemopRe.FindAllStringSubmatch(src, -1) {
		if modes[m[2]] != "Atomic" {
			continue
		}
		rows = append(rows, refMemop{
			name:   m[1],
			family: m[2],
			ty:     m[3],
			pack:   strings.TrimSpace(m[4]),
		})
	}
	return rows
}

// TestAtomicNaturalWidthsMatchTheThreadsReference is TestNaturalWidthsMatchTheReference for the 31
// atomic memarg rows, against the pin that defines them.
//
// Its own test rather than more rows in the first, because the two share no input: a different pin,
// a different constructor spelling, a different `check_memop` signature. What they do share is
// `applyGetSz` and `refSizes`, which are the reference's arithmetic and not either pin's text.
func TestAtomicNaturalWidthsMatchTheThreadsReference(t *testing.T) {
	modes, forms := parseThreadsMemopModes(t)
	rows := parseRefAtomicMemops(t, modes)

	// Pinned exactly, not floored, for the first test's reason: a floor catches a moved file and
	// says nothing about six rows a tightened pattern quietly dropped. 31 is the *authority's*
	// count — 3 notify/wait, 7 loads, 7 stores, 7 rmw, 7 rmw.cmpxchg — and it is also the number of
	// distinct memarg mnemonics the 0xfe region contributes, the 42 rmw encodings collapsing onto 14
	// constructors because the operator is a separate immediate.
	const wantRows = 31
	if len(rows) != wantRows {
		t.Fatalf("parsed %d atomic memarg constructors from %s, want %d; a different count means the "+
			"proposal's set moved or this parse narrowed", len(rows), testenv.ThreadsRefOperatorsML, wantRows)
	}

	for _, row := range rows {
		sz, hasSz, err := applyGetSz(forms[row.family], row.pack)
		if err != nil {
			// Fatal for the first test's reason: a get_sz form this test cannot read is a row it
			// must not report as checked.
			t.Fatalf("%s (%s): %v", row.name, row.family, err)
		}
		key := row.ty
		if hasSz {
			key = sz
		}
		want, ok := refSizes[key]
		if !ok {
			t.Fatalf("%s: %q has no size in refSizes, so the proposal names a constructor this test "+
				"does not know; add it rather than defaulting", row.name, key)
		}
		got, hasWidth := naturalWidth(row.name)
		if !hasWidth {
			t.Errorf("naturalWidth(%q) declines, and the reference gives it %d bytes — a memarg row "+
				"with no width reaches errNoNaturalWidth and its alignment goes unchecked",
				row.name, want)
			continue
		}
		if got != want {
			t.Errorf("naturalWidth(%q) = %d, reference = %d (%s, ty %s, pack %s). Under the atomic "+
				"rule a wrong width rejects the *legal* alignment and accepts an illegal one, so this "+
				"is wrong in both directions at once", row.name, got, want, row.family, row.ty, row.pack)
		}
	}
	t.Logf("%d atomic memarg rows re-derived from %s", len(rows), testenv.ThreadsRefOperatorsML)
}

// TestAtomicModeMatchesTheThreadsReference is what makes `atomicAccess`'s claim checked.
//
// `atomicAccess` keys the equality rule on the mnemonic's `atomic` component; the reference keys it
// on the constructor family. This walks both directions of that correspondence — every Atomic-mode
// constructor must satisfy the predicate, and none of the core pin's 45 must — so the *substitution*
// is the thing under test rather than the rule it selects.
//
// The reverse direction runs over the **core** pin's rows and not the threads pin's core rows: those
// are the constructors this engine's non-atomic mnemonics come from, and the threads baseline's
// copies of them are outside the consulted clause.
func TestAtomicModeMatchesTheThreadsReference(t *testing.T) {
	modes, _ := parseThreadsMemopModes(t)

	// Both modes present, or the correspondence below is one-sided: a `valid.ml` this parse read as
	// all-Atomic would pass the forward direction on every row and mean nothing.
	atomic, nonAtomic := 0, 0
	for _, mode := range modes {
		switch mode {
		case "Atomic":
			atomic++
		case "NonAtomic":
			nonAtomic++
		default:
			t.Errorf("%s gives a check_memop call the mode %q, which is neither of the two the "+
				"reference's `mem_mode` declares — a third mode means the rule has a branch this "+
				"package does not implement", testenv.ThreadsRefValidML, mode)
		}
	}
	if atomic != 6 || nonAtomic != 6 {
		t.Errorf("parsed %d Atomic and %d NonAtomic check_memop arms from %s, want 6 and 6",
			atomic, nonAtomic, testenv.ThreadsRefValidML)
	}

	// **Both spellings of every row.** The reference writes `i32_atomic_load` and this test fed
	// exactly that, which is the spelling every one of `checkMemop`'s call sites happens to pass for
	// an atomic row; `signature` passes the dotted form for its own two arms, and the predicate
	// answered those two spellings differently (`atomicAccess`' doc section has the printed pair).
	// One spelling was therefore enough to check today's paths and not enough to check the
	// *predicate*, whose claim is about a name rather than about a separator. Asking both is a
	// predicate over rows already parsed rather than a second instrument.
	spellings := func(name string) []string {
		return []string{name, strings.ReplaceAll(name, "_", ".")}
	}
	for _, row := range parseRefAtomicMemops(t, modes) {
		for _, name := range spellings(row.name) {
			if !atomicAccess(name) {
				t.Errorf("%s is checked in Atomic mode (%s) and atomicAccess declines it as %q, so "+
					"its alignment is bounded rather than fixed: an under-aligned access the "+
					"reference rejects is accepted, which no vector in the corpus the harness runs "+
					"can see (#537)", row.name, row.family, name)
			}
		}
	}
	for _, row := range parseRefMemops(t) {
		for _, name := range spellings(row.name) {
			if atomicAccess(name) {
				t.Errorf("%s is checked in NonAtomic mode (%s, %s) and atomicAccess claims it as "+
					"%q, so a legal under-aligned access is rejected — 62 corpus vectors expect the "+
					"ceiling rule and this would answer them with the wrong string",
					row.name, row.family, testenv.RefMnemonicsML, name)
			}
		}
	}
}

// threadsCallRe captures the mode, `ty_size` and `get_sz` of a `check_memop` call at the threads
// baseline. The mode is the parameter this pin adds and the core pin does not have.
var threadsCallRe = regexp.MustCompile(
	`check_memop (\w+) c \w+ (?:num_size|vec_size) (\([^)]*\)) e\.at`)

// parseThreadsMemopModes maps each family to its `check_memop` mode and `get_sz` form.
//
// Line-walked with the current arm tracked, for `parseGetSzForms`' reason and not merely by
// symmetry: a pattern that can cross the arm boundary it is keying on already mispaired a get_sz
// once in this file, and it produced a complete-looking map rather than an error.
func parseThreadsMemopModes(tb testing.TB) (modes, forms map[string]string) {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.ThreadsRefValidML)
	modes, forms = map[string]string{}, map[string]string{}
	arm := ""
	for i, line := range strings.Split(src, "\n") {
		if m := armRe.FindStringSubmatch(line); m != nil {
			arm = m[1]
		}
		m := threadsCallRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if arm == "" {
			tb.Errorf("%s:%d calls check_memop outside any arm, so its mode cannot be attributed",
				testenv.ThreadsRefValidML, i+1)
			continue
		}
		if prev, dup := modes[arm]; dup && prev != m[1] {
			tb.Errorf("%s:%d gives arm %s the mode %q after %q — one arm, two modes",
				testenv.ThreadsRefValidML, i+1, arm, m[1], prev)
		}
		modes[arm], forms[arm] = m[1], m[2]
	}
	return modes, forms
}

var (
	// armRe matches the head of a top-level instruction arm: `  | VecLoadLane (x, memop, i) ->`.
	armRe = regexp.MustCompile(`^\s+\| (\w+)[ (]`)
	// callRe captures `check_memop`'s `ty_size` and `get_sz` arguments. The get_sz is a lambda or a
	// parenthesized combinator — `(Lib.Option.map fst)`, with the parens, which is how the reference
	// writes it.
	callRe = regexp.MustCompile(`check_memop c memop (num_size|vec_size) (\([^)]*\)) e\.at`)
)

// parseGetSzForms maps each family to the `get_sz` expression `valid.ml` passes for it.
//
// **Line-walked with the current arm tracked, rather than one regexp spanning both.** The spanning
// version is what was written first and it silently mispaired: `(Lib.Option.map fst)` did not match
// a pattern expecting it without parens, so the lazy `.*?` ran on past the end of `Load`'s arm and
// handed `Load` the `(fun sz -> sz)` belonging to **Store** — a wrong pairing that produced a
// complete-looking map of four entries and no error. The count pin caught it (four of six), which is
// the only reason it was not a green. A pattern that can cross the boundary it is keying on will,
// and the fix is a walk that cannot: an arm head resets the family, so a `check_memop` call is
// attributed to the arm it is lexically inside or to nothing.
func parseGetSzForms(tb testing.TB) map[string]string {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.RefValidML)
	forms := map[string]string{}
	arm := ""
	for i, line := range strings.Split(src, "\n") {
		if m := armRe.FindStringSubmatch(line); m != nil {
			arm = m[1]
		}
		m := callRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if arm == "" {
			tb.Errorf("%s:%d calls check_memop outside any arm, so its get_sz cannot be "+
				"attributed", testenv.RefValidML, i+1)
			continue
		}
		if prev, dup := forms[arm]; dup && prev != m[2] {
			tb.Errorf("%s:%d gives arm %s a second get_sz %q after %q — one arm, two forms",
				testenv.RefValidML, i+1, arm, m[2], prev)
		}
		forms[arm] = m[2]
	}
	return forms
}

// applyGetSz evaluates one of the reference's four `get_sz` forms against a constructor's `pack`
// expression, returning the `packsize` name and whether there is one.
//
// **Four forms, matched exactly, and anything else is an error.** The alternative — a default arm
// guessing from the pack's shape — would make an upstream change to `get_sz` invisible: the guess
// would keep producing plausible widths, and the test would keep agreeing with an implementation
// derived from the same guess. That is the shape where two mechanisms share one error and the delta
// between them stays zero.
func applyGetSz(form, pack string) (sz string, has bool, err error) {
	switch form {
	case "(Lib.Option.map fst)":
		// `pack : (packsize * _) option` → the packsize, or nothing.
		if pack == "None" {
			return "", false, nil
		}
		if inner, ok := cutSome(pack); ok {
			first, _, found := strings.Cut(strings.Trim(inner, "()"), ",")
			if !found {
				return "", false, fmt.Errorf("get_sz %q wants a pair under Some, got %q", form, pack)
			}
			return strings.TrimSpace(first), true, nil
		}
		return "", false, fmt.Errorf("get_sz %q wants None or Some _, got %q", form, pack)

	case "(fun sz -> sz)":
		// `pack : packsize option` → itself.
		if pack == "None" {
			return "", false, nil
		}
		if inner, ok := cutSome(pack); ok {
			return strings.TrimSpace(inner), true, nil
		}
		return "", false, fmt.Errorf("get_sz %q wants None or Some _, got %q", form, pack)

	case "(fun _ -> None)":
		// `VecStore`'s: always the value type's own size, whatever the pack is.
		return "", false, nil

	case "(fun sz -> Some sz)":
		// The lane forms' `pack : packsize`, bare rather than optional.
		if strings.HasPrefix(pack, "Some") || pack == "None" {
			return "", false, fmt.Errorf("get_sz %q wants a bare packsize, got %q", form, pack)
		}
		return strings.TrimSpace(pack), true, nil
	}
	return "", false, fmt.Errorf("unrecognized get_sz form %q — this test knows four, and a fifth "+
		"means the reference's memop typing changed in a way naturalWidth may not have followed",
		form)
}

// cutSome peels `Some x` down to `x`.
func cutSome(pack string) (string, bool) {
	rest, ok := strings.CutPrefix(pack, "Some ")
	return strings.TrimSpace(rest), ok
}

// refSizes is `packed_size` (`syntax/pack.ml:9`), `num_size` and `vec_size`
// (`syntax/types.ml:59,63`), in bytes, keyed by the reference's constructor names.
//
// One map for three functions because their key sets are disjoint, and the header above says why
// these nine numbers are transcribed where everything else in this file is parsed. Nothing here is
// a claim about Wasm that `naturalWidth` is not independently making from a different input: a
// wrong row disagrees and the test fails.
var refSizes = map[string]uint64{
	// packed_size
	"Pack8": 1, "Pack16": 2, "Pack32": 4, "Pack64": 8,
	// num_size, core-pin spelling
	"I32T": 4, "F32T": 4, "I64T": 8, "F64T": 8,
	// num_size, threads-pin spelling — the same four numbers under the names that baseline gave the
	// constructors. Two spellings rather than one normalized key because a rename upstream should
	// arrive as a missing key here, and normalizing `I32Type` to `I32T` in the parse would be this
	// file quietly agreeing that the two are the same type without either pin saying so. F32/F64
	// have no entry: no atomic constructor names a float, and a key nothing looks up is a claim with
	// no witness.
	"I32Type": 4, "I64Type": 8,
	// vec_size
	"V128T": 16,
}
