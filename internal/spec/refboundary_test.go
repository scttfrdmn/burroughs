package spec

import (
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestReferenceBoundaryRoundTrips is the falsifiable control on #196/#197's widening: a
// reference-typed argument crosses the harness→engine boundary and comes back, and this test
// asserts the identity survives — not merely that the vector's Kind reaches KindAssertReturn's
// scoring arm.
//
// **Run against real corpus vectors, not hand-built fixtures**, per this package's own
// fixture-provenance discipline: `extern.wast`/`table_get.wast`/`ref_null.wast` are the vendored
// suite's own files, so a pass here is a pass against the oracle rather than against an
// assumption about it.
func TestReferenceBoundaryRoundTrips(t *testing.T) {
	requireSuite(t)

	cases := []struct {
		name string // subtest name
		file string
		line int // the specific vector's source line, for the failure message only
	}{
		// `(invoke "internalize" (ref.extern 1)) (ref.host 1)` and its neighbours are out of
		// scope (ref.host is not a corpus shape #196/#197 admits), but `extern.wast`'s
		// `externalize-i` results ARE in scope: `(assert_return (invoke "externalize-i"
		// (i32.const 0)) (ref.null extern))` exercises a *result* null, and the file's
		// `init` at :37 exercises a `ref.extern N` *argument* — both round through
		// toInterpValue/fromInterpValue.
		{"externref identity argument then null result", "extern.wast", 37},
		// `table_get.wast`'s `init` writes a specific externref identity via a `table.set`
		// whose value came in as an Invoke argument, and a later `get` reads exactly that
		// identity back out through a *result* — the round trip #197 exists for.
		{"externref identity through table.set/table.get", "table_get.wast", 21},
		// `ref_null.wast`'s first module: a null of each of two distinct heaptypes
		// (funcref-shaped "func" and externref-shaped "extern"), both as results.
		{"ref.null func and ref.null extern results", "ref_null.wast", 17},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := ParseFile(filepath.Join(suiteDir, c.file))
			if err != nil {
				t.Fatalf("parse %s: %v", c.file, err)
			}
			r := run(s)
			// The file's own vectors must pass on the nose — a failure here means the round
			// trip lost or corrupted the identity, which is exactly the defect this control
			// exists to catch. Print every fail so a regression names its own line rather
			// than the file's aggregate count.
			for _, fs := range r.Buckets {
				for _, f := range fs {
					// **A decline is not a verdict, so it is not evidence about this round
					// trip.** `table_get.wast` carries five `assert_invalid` vectors whose
					// opcode slice 1 of the validator (#9) does not type; they are fails in
					// their own named bucket on the board, and they say nothing about whether
					// an externref identity survived table.set/table.get. Filtered on
					// Failure.Declined rather than on the five line numbers, because an
					// enumerated exclusion would inherit today's sample and go quietly wrong
					// the next time the corpus or the slice boundary moves.
					if f.Declined {
						continue
					}
					t.Errorf("%s:%d unexpected fail: want %s, got %s", c.file, f.Line, f.Expect, f.Got)
				}
			}
			if r.Pass == 0 {
				t.Fatalf("%s: 0 passes — the vacuity check (comparisons need a floor): "+
					"this file's reference vectors did not run at all", c.file)
			}
		})
	}
}

// TestRefExternIdentitySurvivesCorruption is the falsification half of the round-trip control
// above: corrupt one field of the boundary encoding and confirm a *specific* vector fails,
// then the corruption is reverted (the mutation lives in this test only, never in the
// production code it targets) — per this project's own "a control isn't born until it has been
// watched die" discipline.
//
// **Scoped to Val.Matches directly** rather than to the full harness, because the property
// under test — "an externref identity comparison is exact, not vacuously true" — is Matches's
// own contract, and the corpus-level round-trip test above already exercises the full pipeline
// for the non-corrupted case. Testing both the unit and the integration is deliberate: a defect
// in Matches alone would not necessarily fail the corpus test if compensating errors existed
// elsewhere (the same "correlated errors" risk this project's memory warns about), so this
// isolates the one function.
func TestRefExternIdentitySurvivesCorruption(t *testing.T) {
	want := Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 5}
	got := Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 5}
	if !want.Matches(got) {
		t.Fatalf("identical externref identities (%d, %d) do not match; the control's own "+
			"positive case is broken before any mutation runs", want.Extern, got.Extern)
	}

	// The mutation: corrupt got's identity to a different value. A control that cannot fail
	// here is asserting nothing about identity at all — see this project's own "print the
	// diff, then run" discipline; the corruption is named explicitly rather than derived, so
	// a reader can see exactly what changed.
	corrupted := got
	corrupted.Extern = 6
	if want.Matches(corrupted) {
		t.Fatalf("Matches(%d) against a corrupted identity %d returned true; identity "+
			"comparison is not exact", want.Extern, corrupted.Extern)
	}

	// The revert: got is unchanged (Go values are copied, not aliased, so this line
	// documents the invariant rather than repairing anything — corrupted was always a copy).
	if !want.Matches(got) {
		t.Fatalf("original got no longer matches after building a corrupted copy; the test " +
			"itself mutated shared state")
	}
}

// TestRefNullMatchesAcrossTwoHeaptypes pins Matches's reference half against `assert_ref_pat`
// (`interpreter/script/runner.ml:464-476`) arm for arm, and **the two cross-family null rows are
// inverted from what this test asserted before grave #266**: a null of one Kind *does* match a
// null expectation of the other, because the reference has exactly one null reference value and
// it carries no heaptype (`runtime/value.ml:20`, `type ref_ += NullRef`, nullary; `:112` types it
// `(Null, BotHT)` whatever produced it; `:151` makes it every nullable type's default). The old
// rows asserted that Kind discriminates two nulls — falsifiable, watched to die, and wrong, which
// is #266's own lesson: watching a control fail proves its assertion is *live*, never that it is
// *correct*, and the correct rule was already written down two files over, in RefClass's own doc
// comment, while this test asserted its opposite.
//
// Scoped to the space rather than to the rows a repair needs: every RefClass legal as a `want`
// crossed with both reference Kinds, against every shape `fromInterpValue` can produce, with each
// expectation derived from the reference's own arm and cited. The `covered` map at the end of the
// function is the vacuity check — a sixth RefClass member arriving later fails this test rather
// than sliding past a matrix that predates it.
func TestRefNullMatchesAcrossTwoHeaptypes(t *testing.T) {
	// The `got` shapes, exhaustive over fromInterpValue's four returns for a reference result.
	// funcNull's Kind is the *placeholder* one fromInterpValue assigns a null of a type valKind
	// cannot name (grave #266's own arm), so every row it appears in is simultaneously the check
	// that the placeholder is unobservable.
	//
	// The non-null shapes carry a Payload since 0039, and they must: `RefPat.admits` refuses
	// `PayloadNone` on the way in, because a non-null result whose constructor the engine could not
	// name is an engine inconsistency the authority has no arm for. A fixture that left it at the zero
	// value would therefore be asserting against a shape `fromInterpValue` does not produce, and every
	// pattern row would read false for the wrong reason.
	var (
		funcNull   = Val{Kind: KindFuncRef, Class: RefLiteralNull}
		externNull = Val{Kind: KindExternRef, Class: RefLiteralNull}
		funcVal    = Val{Kind: KindFuncRef, Class: RefConcrete, Payload: PayloadFunc}
		extern3    = Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 3, Payload: PayloadHost}
		extern4    = Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 4, Payload: PayloadHost}

		// The two shapes an **aggregate** result takes, added for ADR 0040: same RefConcrete return
		// from `refVal`, differing only in the (Kind, Payload) pair, which is the axis the six
		// aggregate patterns are decided on. `bareI31` is `extern.wast:53`'s own result —
		// `externalize-ii` round-trips through `extern.convert_any`/`any.convert_extern` and hands
		// back the *unwrapped* i31 at static type `anyref`. `externI31` is `externalize-i`'s —
		// still wrapped, static type `externref` — and the corpus asserts that one only against the
		// `(ref.extern)` wildcard, which is why the two rows below can disagree with each other
		// without any vector noticing.
		bareI31   = Val{Kind: KindAnyRef, Class: RefConcrete, Payload: PayloadI31}
		externI31 = Val{Kind: KindExternRef, Class: RefConcrete, Payload: PayloadI31}
	)

	type row struct {
		name string
		want Val
		got  Val
		ok   bool
		why  string
	}
	rows := []row{
		// `NullPat _, Value.NullRef -> true` (runner.ml:476) — unconditional in the pattern's
		// heaptype, which is the whole of #266. These four rows and the two bare-`(ref.null)` rows
		// below are all this one arm — six null-vs-null rows, one authority.
		{"null func / null func", funcNull, funcNull, true, "runner.ml:476, NullPat _ vs NullRef"},
		{"null extern / null extern", externNull, externNull, true, "runner.ml:476"},
		{"null func / null extern", funcNull, externNull, true, "runner.ml:476 is heaptype-blind (#266)"},
		{"null extern / null func", externNull, funcNull, true, "runner.ml:476 is heaptype-blind (#266)"},

		// A `ref.null` expectation against a non-null result: no arm matches, OCaml's catch-all
		// answers false. Both Kinds, all three non-null shapes.
		{"null func / non-null func", funcNull, funcVal, false, "no NullPat arm for FuncRef"},
		{"null extern / ref.extern 3", externNull, extern3, false, "no NullPat arm for ExternRef"},
		{"null func / ref.extern 3", funcNull, extern3, false, "no NullPat arm for ExternRef"},

		// The bare `(ref.null)` expectation (AnyNull). Its rows are identical to the keyworded
		// spelling's above — that identity is what licensed deleting Matches's AnyNull fast path,
		// and pinning it here is what keeps the deletion from resting on an argument alone.
		{"bare (ref.null) / null func", bareNull(), funcNull, true, "literal_null's bare arm, parser.mly:1519"},
		{"bare (ref.null) / null extern", bareNull(), externNull, true, "null of any heap type"},
		{"bare (ref.null) / non-null func", bareNull(), funcVal, false, "still a NullPat: FuncRef refused"},
		{"bare (ref.null) / ref.extern 3", bareNull(), extern3, false, "still a NullPat: ExternRef refused"},

		// `RefTypePat FuncHT, Instance.FuncRef _ -> true` and **no NullRef arm** — so a
		// `(ref.func)` expectation refuses a null. This is the asymmetry that makes the null
		// dispatch's RefTypePattern arm a Kind test rather than a bare `true`.
		{"(ref.func) / non-null func", refPat(PatFunc), funcVal, true, "RefTypePat FuncHT vs FuncRef"},
		{"(ref.func) / null func", refPat(PatFunc), funcNull, false, "no RefTypePat FuncHT arm for NullRef"},
		{"(ref.func) / null extern", refPat(PatFunc), externNull, false, "no RefTypePat FuncHT arm for NullRef"},
		{"(ref.func) / ref.extern 3", refPat(PatFunc), extern3, false, "no RefTypePat FuncHT arm for ExternRef"},

		// `RefTypePat ExternHT, _ -> true` (runner.ml:475) — admits anything of this Kind,
		// null included, which is the other half of the asymmetry.
		{"(ref.extern) / ref.extern 3", refPat(PatExtern), extern3, true, "RefTypePat ExternHT, _ -> true"},
		{"(ref.extern) / ref.extern 4", refPat(PatExtern), extern4, true, "identity is not compared by a pattern"},
		{"(ref.extern) / null extern", refPat(PatExtern), externNull, true, "runner.ml:475 admits NullRef"},
		{"(ref.extern) / null func", refPat(PatExtern), funcNull, true, "admits a null whatever Kind tags it"},
		// **The divergence this row recorded is retired, and the row is how that was noticed.**
		// It used to read `false`, with the deviation named: `RefTypePat ExternHT, _ -> true`
		// admits a *FuncRef* and the harness's Kind comparison refused one. #441's fork removed
		// that comparison, so the answer is the reference's now — and the change arrived here
		// *without being aimed here*, which is what the row was written for. The paragraph then
		// said "if a future proposal makes the shape reachable, this row is where the question is
		// already written down"; what made it reachable was a repair one screen away, and the row
		// paid for itself by failing.
		//
		// Still no vector can distinguish the two readings — `(assert_return (invoke "f")
		// (ref.extern))` against a function returning a funcref does not type-check — so this is an
		// **accept-direction** widening with no corpus witness, and it is stated in ADR 0040's
		// consequences rather than left to be inferred from a board that cannot move on it.
		{"(ref.extern) / non-null func", refPat(PatExtern), funcVal, true, "runner.ml:475's wildcard, agreed with since ADR 0040"},

		// **The aggregate patterns, and the divergence ADR 0040 uncovered by removing the gate that
		// cancelled it.** In the reference, externalization is a *constructor*: an externalized i31
		// is `Extern.ExternRef (I31Ref …)`, so `RefTypePat I31HT`'s arm — which matches `I31Ref _` —
		// does not match it, and only `RefTypePat AnyHT`'s catch-all does. Here externalization
		// rides on Kind and the payload underneath survives, so `admits` sees `PayloadI31` either
		// way and cannot tell the two apart. The old Kind comparison hid that by refusing the
		// externalized row for an unrelated reason (want anyref, got externref) and the two errors
		// cancelled; the fork removed one of them.
		//
		// So these two rows read the same and the authority reads them differently, and that is
		// filed as #451 with the payload nesting as its subject rather than patched here — the fix
		// is a change to what a Val *carries*, not to what `admits` concludes from what it has.
		// 0 corpus vectors either way, by the same argument as the `(ref.extern)` row above:
		// `externalize-i`'s results are asserted against `(ref.extern)` alone.
		{"(ref.i31) / bare i31", refPat(PatI31), bareI31, true, "RefTypePat I31HT vs I31Ref — extern.wast:53's own shape"},
		{"(ref.i31) / externalized i31", refPat(PatI31), externI31, true, "DIVERGENCE (#451): the reference refuses this, we admit it"},
		// The other half of the same removal, in the other direction: `(ref.any)` against an
		// externalized value is `RefTypePat AnyHT, _ -> true` after the FuncRef arm, and the Kind
		// comparison used to refuse it. **This row was a real disagreement and now agrees** — the
		// paragraph `RefPat.admits` carried about it is corrected in the same diff.
		{"(ref.any) / externalized i31", refPat(PatAny), externI31, true, "runner.ml:468's AnyHT catch-all, agreed with since ADR 0040"},
		{"(ref.func) / bare i31", refPat(PatFunc), bareI31, false, "RefTypePat FuncHT has no I31Ref arm"},

		// `RefResult (RefPat r)` compares two concrete references by identity.
		{"ref.extern 3 / ref.extern 3", extern3, extern3, true, "identity equal"},
		{"ref.extern 3 / ref.extern 4", extern3, extern4, false, "identity differs"},
		{"ref.extern 3 / null extern", extern3, externNull, false, "a null is not a concrete reference"},
		{"ref.extern 3 / null func", extern3, funcNull, false, "a null is not a concrete reference"},
		// The `why` here read "wrong Kind" until ADR 0040, and the answer is unchanged while the
		// reason is not: nothing compares the two kinds any more, and this is false because a
		// RefConcrete "some non-null reference" is not an identity to compare against. A row whose
		// stated reason names a mechanism the code no longer has is the same defect as a comment
		// asserting a property the code lacks — it makes a reader confirm the wrong thing.
		{"ref.extern 3 / non-null func", extern3, funcVal, false, "an identity expectation needs a RefExternIdentity result"},
	}

	for _, r := range rows {
		if got := r.want.Matches(r.got); got != r.ok {
			t.Errorf("%s: Matches(%s, %s) = %v, want %v (%s)",
				r.name, r.want, r.got, got, r.ok, r.why)
		}
	}

	// The vacuity check: assert the matrix covers every RefClass that can legally be a `want`,
	// deriving the domain from RefClass's own members rather than from the rows above. Without
	// this, a sixth member — or a change making RefConcrete expectation-legal — leaves a matrix
	// that agrees perfectly about a smaller space than its name claims.
	covered := map[RefClass]bool{}
	for _, r := range rows {
		covered[r.want.Class] = true
	}
	for c := RefNone; c <= RefConcrete; c++ {
		switch c {
		case RefNone, RefConcrete:
			// Not legal as a `want`: RefNone means "not a reference Val" and RefConcrete is
			// result-only (fromInterpValue's own doc comment). Asserted rather than skipped, so
			// a future change that starts producing either as an expectation trips here.
			if covered[c] {
				t.Errorf("RefClass %d appears as a `want` in this matrix but is documented "+
					"result-only; either the doc comment or the row is now wrong", c)
			}
		case RefLiteralNull, RefExternIdentity, RefTypePattern:
			if !covered[c] {
				t.Errorf("RefClass %d is legal as a `want` and no row covers it; the matrix "+
					"is scoped to fewer shapes than its name claims", c)
			}
		}
	}
	if n := len(rows); n < 20 {
		t.Errorf("matrix has %d rows; the arm table it mirrors needs at least 20 "+
			"(4 null-vs-null, 3 null-vs-non-null, 4 bare, 4 per RefTypePat Kind, 5 identity)", n)
	}
}

// TestNullRendersWithoutAHeaptype is grave #266's *message* half. Matches ignoring a null's Kind
// and String printing it are the same divergence read at two sites, and only one of them has a
// suite witness: a mismatch message's text is past the end of every expected string the oracle
// reads, so the invented-bits class (grave #36) applies in full and a print-check is the only
// thing that can see it. `runtime/value.ml:322` is `| NullRef -> "null"` — no heaptype, because
// there is none to print.
//
// The complement is asserted in the same test on purpose: a *pattern* does carry a real heaptype
// (`(ref.func)` and `(ref.extern)` are different expectations, per assert_ref_pat's own two arms),
// so a fix that suppressed the heaptype everywhere would be over-broad. One test, both directions,
// so neither can be satisfied by breaking the other.
func TestNullRendersWithoutAHeaptype(t *testing.T) {
	for _, k := range []ValKind{KindFuncRef, KindExternRef} {
		for _, anyNull := range []bool{false, true} {
			v := Val{Kind: k, Class: RefLiteralNull, AnyNull: anyNull}
			if got := v.String(); got != "ref.null" {
				t.Errorf("Val{Kind: %v, AnyNull: %v}.String() = %q, want %q — a null carries no "+
					"heaptype to name (value.ml:322)", k, anyNull, got, "ref.null")
			}
		}
	}
	// A pattern's heaptype is real and must survive — and **it is printed back in the corpus's own
	// spelling**, which is grave #445, corrected here. This loop used to hold two hand-written rows
	// expecting `(ref.funcref)` and `(ref.externref)`: those are *val type* names, and the grammar's
	// pattern arms are over heaptypes (`(ref.func)`, `(ref.extern)`), so a mismatch message quoted an
	// expectation in a syntax no vector can contain. It was invisible while Kind was the only thing a
	// pattern carried, because there was nothing else to print — and the right spelling was ten lines
	// down this file the whole time, in a test-case *label* that nothing asserted. The rows are derived
	// from `refPatterns` now, whose keys are the strings the parser matches, so the expectation comes
	// from where the vectors do; all eight arms are covered rather than two.
	for spelling, r := range refPatterns {
		want := "(" + spelling + ")"
		if got := refPat(r.pat).String(); got != want {
			t.Errorf("refPat(%v).String() = %q, want %q — a type pattern's heaptype is a real "+
				"distinction (assert_ref_pat's eight RefTypePat arms differ by it), and the printed "+
				"form is the spelling the vector wrote", r.pat, got, want)
		}
	}
	if n := len(refPatterns); n < 8 {
		t.Errorf("refPatterns has %d rows; the grammar's `result` production has 8 RefTypePat arms "+
			"(parser.mly:1517-1530), and a loop over a drained table agrees about nothing", n)
	}
}

// TestToInterpValueRefusesEveryUnpassableShape is the control for the second-opinion check
// `toInterpValue`'s doc comment claims. It claimed it for two shapes and had it for one: a bare
// `(ref.null)` carries Class RefLiteralNull, so it reached the `interp.NullRef(t)` line and became
// a concrete funcref null built from a placeholder Kind — the silently-wrong Value the comment
// says a caller bypassing `isPassable` is protected from. Latent, both `Command.Args` sites in
// wast.go filtering on `isPassable` first, which is exactly why nothing caught it: the guard that
// makes the defect unreachable also makes it unobservable.
//
// **Both columns are authored from the semantics, not read out of either function.** The first
// draft of this test derived its expectations from `isPassable` and asserted the two agreed, which
// was wrong twice over: it is the echo grave (#106) — a premise measured with one of the two things
// being compared — and it is also *false*, because the two predicates legitimately disagree at
// `RefConcrete`. isPassable admits that shape (nothing about the grammar makes it an expectation)
// and toInterpValue refuses it (it is a result-only shape with no argument spelling). An agreement
// test would have had to be wrong about one of them to pass.
//
// The derivation did earn its keep before being replaced: asserting agreement is what surfaced the
// two NaN classes, which a hand-listed set of *reference* shapes would have missed entirely.
func TestToInterpValueRefusesEveryUnpassableShape(t *testing.T) {
	nanLane := Val{Kind: KindV128, LaneBits: 32, Lanes: []Val{
		{Kind: KindF32, LaneBits: 32, NaN: NaNCanonical},
		{Kind: KindF32, LaneBits: 32},
		{Kind: KindF32, LaneBits: 32},
		{Kind: KindF32, LaneBits: 32},
	}}
	exactLanes := Val{Kind: KindV128, LaneBits: 32, Lanes: []Val{
		{Kind: KindI32, LaneBits: 32, Bits: 1},
		{Kind: KindI32, LaneBits: 32, Bits: 2},
		{Kind: KindI32, LaneBits: 32, Bits: 3},
		{Kind: KindI32, LaneBits: 32, Bits: 4},
	}}
	shapes := []struct {
		name        string
		v           Val
		passable    bool // isPassable: is this a shape the *grammar* allows in argument position?
		convertible bool // toInterpValue: can a concrete interp.Value be built from it?
		why         string
	}{
		// Predicates: no single value to pass, refused by both.
		{"(ref.func) type pattern", refPat(PatFunc), false, false, "names any funcref, not one"},
		{"(ref.extern) type pattern", refPat(PatExtern), false, false, "names any externref, not one"},
		{"nan:canonical f32", Val{Kind: KindF32, NaN: NaNCanonical}, false, false, "a set of bit patterns; Bits is 0"},
		{"nan:arithmetic f64", Val{Kind: KindF64, NaN: NaNArithmetic}, false, false, "a set of bit patterns; Bits is 0"},
		{"v128 with a nan:canonical lane", nanLane, false, false, "one lane is a predicate, so the vector is"},
		// Concrete but untypeable: one value, no heaptype to build it with.
		{"bare (ref.null)", bareNull(), false, false, "one value, but interp.NullRef needs a type"},
		// Concrete and typeable: both admit.
		{"ref.null func", Val{Kind: KindFuncRef, Class: RefLiteralNull}, true, true, "a null of a named heaptype"},
		{"ref.null extern", Val{Kind: KindExternRef, Class: RefLiteralNull}, true, true, "a null of a named heaptype"},
		{"ref.extern 7", Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 7}, true, true, "an opaque identity"},
		{"i32 42", Val{Kind: KindI32, Bits: 42}, true, true, "an exact numeric"},
		{"v128 of exact lanes", exactLanes, true, true, "every lane concrete"},
		// **The one legitimate disagreement**, and the reason this test has two columns rather than
		// one. A non-null funcref result is not an argument spelling any vector can write, so the
		// grammar has nothing to refuse while toInterpValue has nothing to build.
		{"RefConcrete funcref", Val{Kind: KindFuncRef, Class: RefConcrete}, true, false, "result-only shape"},
	}
	var passableN, convertibleN, disagree int
	for _, s := range shapes {
		if got := s.v.isPassable(); got != s.passable {
			t.Errorf("%s: isPassable() = %v, want %v (%s)", s.name, got, s.passable, s.why)
		}
		if _, got := toInterpValue(s.v); got != s.convertible {
			t.Errorf("%s: toInterpValue ok = %v, want %v (%s)", s.name, got, s.convertible, s.why)
		}
		if s.passable {
			passableN++
		}
		if s.convertible {
			convertibleN++
		}
		if s.passable != s.convertible {
			disagree++
		}
	}
	// Three floors, because each covers a way this table could agree about nothing: an all-refused
	// table (the vacuity failure), an all-admitted one, and — the specific one this test exists to
	// hold — a table that has quietly lost the row where the two predicates differ, which is the
	// row that establishes they are two predicates at all.
	if passableN == 0 || convertibleN == 0 {
		t.Errorf("degenerate partition: %d passable, %d convertible — a control that only ever "+
			"sees one side of a predicate is not testing the predicate", passableN, convertibleN)
	}
	if disagree == 0 {
		t.Errorf("no row where isPassable and toInterpValue differ; without one, this table is " +
			"consistent with the two being a single predicate and the second opinion being " +
			"dischargeable by calling isPassable — which it is not (see RefConcrete)")
	}
}

// bareNull is the bare `(ref.null)` expectation — readRefConst's own construction for
// `literal_null`'s heaptype-free arm (see Val.AnyNull). A helper rather than a var, so each row
// gets its own copy and no row can mutate another's expectation.
func bareNull() Val {
	return Val{Kind: KindFuncRef, Class: RefLiteralNull, AnyNull: true}
}

// refPat is the bare `(ref.<ht>)` type-pattern expectation for a given pattern.
//
// **The Kind comes out of `refPatterns`, not out of an argument.** 0039 made the pattern and the Kind
// two fields, and a fixture that set them independently could pair them in a way `readRefConst` never
// produces — which would make every row below a test of a Val the reader cannot build. Looking the
// Kind up in the reader's own table is what keeps these rows about `Matches` rather than about a
// hand-assembled shape; the panic is for a pattern the table has no row for, which is
// TestEveryRefPatHasASpelling's subject and not something a row here should limp past.
func refPat(p RefPat) Val {
	for _, r := range refPatterns {
		if r.pat == p {
			return Val{Kind: r.kind, Class: RefTypePattern, Pat: p}
		}
	}
	panic("refPat: no spelling in refPatterns for " + p.String())
}

// TestRefFuncAsArgumentIsOutOfScope pins the judgment call this task's own investigation made
// explicit: `ref.func` as an invoke *argument* (as opposed to a module-body instruction, which
// `internal/text` already reads) is measured at 0 vectors in the corpus, so readRefConst
// declines it rather than resolving a symbolic name against an instance's index space. This
// test is the negative control — it must keep failing to parse, and a future corpus update that
// starts using this shape would fail *this* test rather than silently mis-parsing.
func TestRefFuncAsArgumentIsOutOfScope(t *testing.T) {
	for _, src := range []string{`(ref.func 0)`, `(ref.func $f)`} {
		n, err := newParser([]byte(src)).parseNode()
		if err != nil {
			t.Fatalf("%s does not parse as a node: %v", src, err)
		}
		if _, ok := readRefConst(n); ok {
			t.Errorf("readRefConst(%q) succeeded; ref.func as a literal argument was measured "+
				"at 0 corpus vectors and is documented as out of scope — if the corpus now "+
				"uses this shape, widen readRefConst deliberately rather than let this test "+
				"silently start passing", src)
		}
	}
}

// TestEveryRefPatHasASpelling and TestInterpPayloadsCoverTheEngineVocabulary are **the harness half
// of Scott's condition on the 0039 stamp**, quoted so the condition and the code that answers it sit
// together: *"the payload kind is handled exhaustively at both boundaries, with a test that enumerates
// the kinds from the type's own definition and fails on any unmapped one. No `default` case that
// silently absorbs a future member — an enum whose whole purpose is to grow must fail loudly the first
// time it does."*
//
// Both derive their domain the same way: count up from the zero value to the `iota`-maintained
// sentinel declared inside the enum's own const block. That is what makes them enumerate *the type's*
// definition rather than a list of members someone remembered — a ninth `RefPat` added above
// `patPastEnd` widens this loop in the same commit that declares it, and fails here until its row
// exists.
func TestEveryRefPatHasASpelling(t *testing.T) {
	// PatNone first, in the other direction: it is the zero value and **not** a pattern, so a table
	// row for it would mean `readRefConst` can return a RefTypePattern that admits by accident.
	for spelling, r := range refPatterns {
		if r.pat == PatNone {
			t.Errorf("refPatterns[%q] maps to PatNone, the zero value — a spelling that reads as "+
				"'not a pattern' makes `admits` the wrong question about a Val the reader built", spelling)
		}
	}

	spelled := map[RefPat]string{}
	for spelling, r := range refPatterns {
		if prev, dup := spelled[r.pat]; dup {
			t.Errorf("refPatterns has two spellings for %v (%q and %q); the grammar's arms are "+
				"one-to-one with its keywords, so one of these rows is a typo that silently shadows "+
				"the other", r.pat, prev, spelling)
		}
		spelled[r.pat] = spelling
	}

	for p := PatNone + 1; p < patPastEnd; p++ {
		spelling, ok := spelled[p]
		if !ok {
			t.Errorf("RefPat %v (ordinal %d) has no row in refPatterns — every member of this enum "+
				"is one of `parser.mly:1517-1530`'s RefTypePat arms, so an unspelled member is a "+
				"pattern the reader declines and a vector scored `unsupported`", p, p)
			continue
		}
		// The spelling and String() must agree, which is what catches a member that got a table row
		// but no String arm: `String`'s fallthrough answers "no-pattern" for anything it does not
		// name, and a mismatch message quoting `(ref.no-pattern)` is the invented-bits class again.
		if want := "ref." + p.String(); spelling != want {
			t.Errorf("refPatterns spells RefPat ordinal %d %q while its String is %q (so %q); the "+
				"table and the printer disagree about the same member, and only one of them appears "+
				"in a mismatch message", p, spelling, p.String(), want)
		}
	}

	if n := len(refPatterns); n != int(patPastEnd)-1 {
		t.Errorf("refPatterns has %d rows for %d patterns (patPastEnd %d, less the PatNone zero "+
			"value); the counts are asserted as well as the membership because a table drained to "+
			"empty satisfies every loop above", n, int(patPastEnd)-1, patPastEnd)
	}
}

// TestUnnameableRefIsInTheReferenceHalf is ADR 0040's sentinel control: the four properties
// KindUnnameableRef's doc comment claims for it, each asserted where a wrong answer would be quiet.
//
// The properties are separable on purpose. `isRef` true is the *only* claim the sentinel makes about
// its value, and Matches' reference/numeric fork reads exactly that predicate — a sentinel outside
// the reference half would send an unnameable reference down the numeric path, where `want.Kind !=
// got.Kind` would refuse it and reintroduce the gate ADR 0040 removed for precisely the population
// that cannot spell its own type. `valType` refusing it is the other side: that refusal is what keeps
// a Kind born from `valKind`'s *inability to name a type* from being handed back to the engine as a
// type, and it has to be checked against a kind that does convert, or "refuses" would be indis-
// tinguishable from "converts nothing".
//
// **What this test does not bound**: a ValKind appended *after* the sentinel falls outside the loop
// below, which counts up to it. ValKind has no `iota`-maintained past-end sentinel the way `RefPat`
// and `interp.RefPayload` do (TestEveryRefPatHasASpelling's own domain argument), so the instrument
// for a new member is `exhaustive` — `String` and `valType` both name every case, so a new kind
// cannot compile without a stated reading in each. Named rather than left implicit: a loop bounded by
// the member it is about is a domain that stops growing when the enum does not.
func TestUnnameableRefIsInTheReferenceHalf(t *testing.T) {
	if !KindUnnameableRef.isRef() {
		t.Errorf("KindUnnameableRef.isRef() is false — Matches forks on this predicate, so an "+
			"unnameable reference would be compared as a number and refused on the kind inequality "+
			"ADR 0040 deleted; %d must sit in the reference half", KindUnnameableRef)
	}

	// The discriminating pair for the argument-side refusal: every *other* reference kind converts.
	for _, k := range []ValKind{KindFuncRef, KindExternRef, KindAnyRef} {
		if _, ok := valType(k); !ok {
			t.Errorf("valType(%v) refuses a reference kind the harness can name; the sentinel's own "+
				"refusal below then says nothing, because a function that converts nothing refuses "+
				"everything", k)
		}
	}
	if tv, ok := valType(KindUnnameableRef); ok {
		t.Errorf("valType(KindUnnameableRef) returned %v — this Kind exists because `valKind` could "+
			"not name a type, so returning one here invents the answer that function declined to "+
			"give, and `toInterpValue` would hand it to the engine to check against a real signature",
			tv)
	}

	// Distinct spellings, over the members from the zero value up to the sentinel. A copy-pasted
	// String arm returning "anyref" would restore exactly the fabrication ADR 0040 removed — the
	// sentinel printing as a type the value does not have — and nothing else here would notice.
	spelled := map[string]ValKind{}
	for k := KindI32; k <= KindUnnameableRef; k++ {
		s := k.String()
		if s == "unknown" {
			t.Errorf("ValKind ordinal %d prints as %q, the String fallthrough — a kind with no arm "+
				"of its own reaches a mismatch message as a non-answer", k, s)
		}
		if prev, dup := spelled[s]; dup {
			t.Errorf("ValKind ordinals %d and %d both print as %q; two kinds with one spelling make "+
				"a message name a type the value may not have", prev, k, s)
		}
		spelled[s] = k
	}

	// The behavioural claim is already witnessed next door and is not restated here: the
	// `(ref.i31) / bare i31` row in TestRefNullMatchesAcrossTwoHeaptypes pairs a *sentinel* want with
	// an `anyref` got and expects true, which is only reachable through the reference family — the
	// numeric one refuses an unequal pair on its first line. A second copy of that assertion here
	// would be a control testing a helper rather than the path — grave #246's shape, whose own control
	// (`TestRefLocalDefaultsToNull`) states the rule.
}

func TestInterpPayloadsCoverTheEngineVocabulary(t *testing.T) {
	// The domain is the *engine's* enum, bounded by `interp.PayloadPastEnd` — which is exported for
	// exactly this, the harness and the public boundary both needing to name the bound to derive it.
	// PayloadNone is included: it is a real answer here (a null, or a constructor the engine could not
	// determine), so an unmapped zero value would be `refVal`'s silent fallback rather than a Val.
	for p := interp.RefPayload(0); p < interp.PayloadPastEnd; p++ {
		if _, ok := interpPayloads[p]; !ok {
			t.Errorf("interp.RefPayload %v (ordinal %d) has no row in interpPayloads — `refVal` would "+
				"report it as PayloadNone, which makes every `(ref.<ht>)` pattern refuse a result the "+
				"engine named correctly, and the vector fails without naming why", p, p)
		}
	}
	if _, ok := interpPayloads[interp.PayloadPastEnd]; ok {
		t.Errorf("interpPayloads has a row for interp.PayloadPastEnd, which is a bound and not a " +
			"payload kind; a row for it would make the loop above pass for a member that does not exist")
	}
	if n := len(interpPayloads); n != int(interp.PayloadPastEnd) {
		t.Errorf("interpPayloads has %d rows for %d engine payload kinds; the count is asserted as "+
			"well as the membership so a table with extra rows outside the domain is visible too",
			n, int(interp.PayloadPastEnd))
	}

	// The other direction. This package's own `RefPayload` exists **only** to receive the engine's, so
	// a member of it that nothing maps onto is not a widening waiting for a consumer — it is a member
	// the harness can write into a Val and the engine can never produce, which would make a mismatch
	// message name a constructor no result has.
	hit := map[RefPayload]interp.RefPayload{}
	for e, s := range interpPayloads {
		if prev, dup := hit[s]; dup {
			t.Errorf("interpPayloads maps both %v and %v onto %v; two engine constructors collapsing "+
				"into one makes `admits` answer about the wrong one, and neither vector can tell",
				prev, e, s)
		}
		hit[s] = e
		if s >= payloadPastEnd {
			t.Errorf("interpPayloads maps %v onto ordinal %d, past this package's own vocabulary "+
				"(payloadPastEnd %d)", e, s, payloadPastEnd)
		}
	}
	for p := RefPayload(0); p < payloadPastEnd; p++ {
		if _, ok := hit[p]; !ok {
			t.Errorf("spec.RefPayload %v (ordinal %d) is in nothing's image; no engine result can "+
				"carry it", p, p)
		}
	}
}
