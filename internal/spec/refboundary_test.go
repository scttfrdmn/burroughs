package spec

import (
	"path/filepath"
	"testing"
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
	var (
		funcNull   = Val{Kind: KindFuncRef, Class: RefLiteralNull}
		externNull = Val{Kind: KindExternRef, Class: RefLiteralNull}
		funcVal    = Val{Kind: KindFuncRef, Class: RefConcrete}
		extern3    = Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 3}
		extern4    = Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: 4}
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
		{"(ref.func) / non-null func", refPat(KindFuncRef), funcVal, true, "RefTypePat FuncHT vs FuncRef"},
		{"(ref.func) / null func", refPat(KindFuncRef), funcNull, false, "no RefTypePat FuncHT arm for NullRef"},
		{"(ref.func) / null extern", refPat(KindFuncRef), externNull, false, "no RefTypePat FuncHT arm for NullRef"},
		{"(ref.func) / ref.extern 3", refPat(KindFuncRef), extern3, false, "no RefTypePat FuncHT arm for ExternRef"},

		// `RefTypePat ExternHT, _ -> true` (runner.ml:475) — admits anything of this Kind,
		// null included, which is the other half of the asymmetry.
		{"(ref.extern) / ref.extern 3", refPat(KindExternRef), extern3, true, "RefTypePat ExternHT, _ -> true"},
		{"(ref.extern) / ref.extern 4", refPat(KindExternRef), extern4, true, "identity is not compared by a pattern"},
		{"(ref.extern) / null extern", refPat(KindExternRef), externNull, true, "runner.ml:475 admits NullRef"},
		{"(ref.extern) / null func", refPat(KindExternRef), funcNull, true, "admits a null whatever Kind tags it"},
		// **A stated divergence, not an oversight.** `RefTypePat ExternHT, _ -> true` admits a
		// *FuncRef* too, where this harness's Kind comparison refuses it. The deviation has no
		// witness and cannot acquire one: `(assert_return (invoke "f") (ref.extern))` against a
		// function returning a funcref does not type-check, so no vector can distinguish the two
		// readings. Recorded as a row with the reference's answer named, rather than left out,
		// because an undocumented deviation is indistinguishable from a defect nobody noticed —
		// and if a future proposal makes the shape reachable, this row is where the question is
		// already written down.
		{"(ref.extern) / non-null func", refPat(KindExternRef), funcVal, false, "diverges: runner.ml:475 would admit it; no vector can type-check the shape"},

		// `RefResult (RefPat r)` compares two concrete references by identity.
		{"ref.extern 3 / ref.extern 3", extern3, extern3, true, "identity equal"},
		{"ref.extern 3 / ref.extern 4", extern3, extern4, false, "identity differs"},
		{"ref.extern 3 / null extern", extern3, externNull, false, "a null is not a concrete reference"},
		{"ref.extern 3 / null func", extern3, funcNull, false, "a null is not a concrete reference"},
		{"ref.extern 3 / non-null func", extern3, funcVal, false, "wrong Kind"},
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
	// A pattern's heaptype is real and must survive.
	for _, tc := range []struct {
		k    ValKind
		want string
	}{
		{KindFuncRef, "(ref.funcref)"},
		{KindExternRef, "(ref.externref)"},
	} {
		if got := refPat(tc.k).String(); got != tc.want {
			t.Errorf("refPat(%v).String() = %q, want %q — a type pattern's heaptype is a real "+
				"distinction (assert_ref_pat's two RefTypePat arms differ by it)", tc.k, got, tc.want)
		}
	}
}

// bareNull is the bare `(ref.null)` expectation — readRefConst's own construction for
// `literal_null`'s heaptype-free arm (see Val.AnyNull). A helper rather than a var, so each row
// gets its own copy and no row can mutate another's expectation.
func bareNull() Val {
	return Val{Kind: KindFuncRef, Class: RefLiteralNull, AnyNull: true}
}

// refPat is the bare `(ref.func)` / `(ref.extern)` type-pattern expectation for a given Kind.
func refPat(k ValKind) Val {
	return Val{Kind: k, Class: RefTypePattern}
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
