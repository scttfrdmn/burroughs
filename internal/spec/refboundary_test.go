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

// TestRefNullMatchesAcrossTwoHeaptypes is the requested two-heaptype control: a null of one
// static Kind and a null of the other both satisfy their own class's Matches contract, and
// (per RefClass's own doc comment, citing the reference's Kind-blind null semantics) a null
// argument's *specific* spelling does not leak into a mismatched comparison the wrong
// direction — Kind is still checked (a KindFuncRef null must not match a KindExternRef
// expectation), only the *heaptype spelling within one Kind* is deliberately not retained.
func TestRefNullMatchesAcrossTwoHeaptypes(t *testing.T) {
	funcNull := Val{Kind: KindFuncRef, Class: RefLiteralNull}
	externNull := Val{Kind: KindExternRef, Class: RefLiteralNull}

	if !funcNull.Matches(funcNull) {
		t.Errorf("a funcref null does not match itself")
	}
	if !externNull.Matches(externNull) {
		t.Errorf("an externref null does not match itself")
	}
	// The cross-Kind check: two different reference kinds, both null, must NOT match — Kind
	// is a real distinction even though heaptype-within-a-Kind is not. Corrupting Matches to
	// drop the Kind check entirely (want.Kind != got.Kind's early return) would make this
	// fail, which is the falsification for *this* row.
	if funcNull.Matches(externNull) {
		t.Errorf("a funcref null matched an externref null expectation; Kind is being " +
			"ignored, not just heaptype-within-Kind")
	}
	if externNull.Matches(funcNull) {
		t.Errorf("an externref null matched a funcref null expectation; Kind is being " +
			"ignored, not just heaptype-within-Kind")
	}
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
