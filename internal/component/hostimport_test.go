// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// inlineReading is wasmtime's reading of the same arm. Parsed rather than restated, so the expected
// values cannot drift from what the oracle actually said.
const inlineReading = "testdata/asynclift/inline.reading"

// wantInline pulls `run(n)`'s expected result out of the committed reading.
//
// It FAILS rather than defaults when the line is absent: a witness that falls back to a built-in
// expectation is checking itself.
func wantInline(t *testing.T, n string) string {
	t.Helper()
	b, err := os.ReadFile(inlineReading)
	if err != nil {
		t.Fatalf("the committed inline reading is missing, so this witness has no oracle: %v", err)
	}
	// `OUTCOME   inline: run(1)=Ok((101,)) run(2)=Ok((102,))`
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.Contains(ln, "run("+n+")=Ok((") {
			continue
		}
		rest := ln[strings.Index(ln, "run("+n+")=Ok((")+len("run("+n+")=Ok(("):]
		if i := strings.Index(rest, ","); i >= 0 {
			return rest[:i]
		}
	}
	t.Fatalf("the committed reading has no run(%s) result; regenerate it with build.sh", n)
	return ""
}

// TestHostCanSupplyABareWorldLevelImport is #870's witness.
//
// # The defect, which was the KIND and not the missing case
//
// `stubHost` answers every import with a stub *instance*. A bare world-level function import —
// `(import "tick" (func …))`, which the committed suspending guest declares — therefore produced a
// `compDef` whose `.fn` was nil, and `walk.go`'s impl lookup is guarded on `fn != nil`. So **the lookup
// never ran at all**: not for an import the host provides, and not for one it does not.
//
// Fixed where the sort is known, so the existing lookup does the work keyed on the import's own name,
// with no second dispatch path. The key is the bare name with both premises checked against the spec at
// `CANON_PIN`: import `externname`s are strongly-unique (Binary.md), and a `plainname`'s charset is
// alphanumerics and `-` only (Explainer.md), so no bare name can collide with an `instance::export` key.
//
// # Why the result is read from a committed reading
//
// Every other reading in `testdata/asynclift/` uses the rendezvous `tick`, which DEFERS — so when
// Burroughs first ran this guest with an inline host impl, its output had nothing to check against.
// `inline.reading` is wasmtime on the same arm. **Burroughs' own number is not evidence until the
// reference agrees.**
func TestHostCanSupplyABareWorldLevelImport(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	t.Run("a_provided_bare_import_is_entered_and_its_value_comes_back", func(t *testing.T) {
		for _, id := range []uint32{1, 2} {
			h := NewHost(io.Discard, io.Discard, nil)
			var entered, gotArg int32
			// Keyed on the import's OWN name. Before #870 no spelling worked, because the lookup was
			// never reached.
			h.asyncImpls = map[string]asyncLowerImpl{
				"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
					params := onStart()
					atomic.AddInt32(&entered, 1)
					if len(params) > 0 {
						atomic.StoreInt32(&gotArg, params[0].Int32())
					}
					// The same derivation wasmtime's inline arm uses, so the two are comparable.
					onResolve(canon.U32(uint32(atomic.LoadInt32(&gotArg)) + 100))
					return func() {}, nil
				},
			}
			in, iErr := InstantiateWithHost(b, h)
			if iErr != nil {
				t.Fatalf("run(%d): instantiate: %v", id, iErr)
			}
			got, cErr := in.CallValues("run", canon.U32(id))
			in.Close()
			if cErr != nil {
				t.Fatalf("run(%d): %v", id, cErr)
			}
			// 1. The host import was ENTERED — the half that was impossible before, and asserted on its
			//    own because a correct result could in principle arrive without it.
			if atomic.LoadInt32(&entered) != 1 {
				t.Errorf("run(%d): the host impl was entered %d time(s), want 1", id, atomic.LoadInt32(&entered))
			}
			// 2. The guest's argument reached the host, which is what makes the derived value meaningful.
			if uint32(atomic.LoadInt32(&gotArg)) != id {
				t.Errorf("run(%d): the host saw argument %d", id, atomic.LoadInt32(&gotArg))
			}
			// 3. The value came back, matching WASMTIME's committed reading rather than a literal here.
			if len(got) != 1 {
				t.Fatalf("run(%d) returned %d values, want 1", id, len(got))
			}
			v, ok := got[0].U32()
			if !ok {
				t.Fatalf("run(%d) returned kind %v, want u32", id, got[0].Type.Kind)
			}
			if want := wantInline(t, itoa(id)); itoa(v) != want {
				t.Errorf("run(%d) = %d, wasmtime's committed reading says %s", id, v, want)
			}
		}
	})

	t.Run("an_unprovided_bare_import_is_refused_by_its_own_name", func(t *testing.T) {
		// The refusal must name `tick`. It used to say `actual::0` — wit-component's index-shaped
		// indirection, which tells a reader nothing about which import was missing. This arm is what keeps
		// the fix from being a permissiveness change: the point is to let a host SUPPLY an import, not to
		// stop refusing the ones it did not.
		h := NewHost(io.Discard, io.Discard, nil) // no asyncImpls at all
		in, iErr := InstantiateWithHost(b, h)
		if iErr != nil {
			// Refusing at instantiate is acceptable; what matters is that the name is `tick`.
			if !strings.Contains(iErr.Error(), "tick") {
				t.Fatalf("refused at instantiate, but not naming the import: %v", iErr)
			}
			return
		}
		defer in.Close()
		_, cErr := in.CallValues("run", canon.U32(1))
		if cErr == nil {
			t.Fatal("run() succeeded with no host impl for `tick`; an unprovided import must be refused")
		}
		if !strings.Contains(cErr.Error(), "tick") {
			t.Errorf("refused, but not by the import's own name — a reader cannot tell which import is "+
				"missing:\n%v", cErr)
		}
		// `import actual::`, not `actual::` anywhere: the repair names the component-level import as the
		// SUBJECT and keeps the core coordinates as a trailing diagnostic, which a reader debugging the link
		// wants. So the assertion is about subject position — the regression is the indirection being the
		// thing refused, not the indirection being mentioned.
		if strings.Contains(cErr.Error(), "import actual::") {
			t.Errorf("the refusal's subject is still wit-component's index indirection rather than the "+
				"import:\n%v", cErr)
		}
	})

	t.Run("a_deferring_import_parks_and_completes_when_resolved", func(t *testing.T) {
		// # This arm was re-pointed, and the old subject is why
		//
		// It asserted that a deferring import **reached the unbuilt park and refused by name**
		// (`ErrAsyncNotImplemented`, "park/yield"), pinning that removing blocker 1 exposed blocker 2.
		// Its own failure message named its successor: *"if it now can, #871 has landed and this arm is
		// the thing to update"*. #871 landed, so the subject is gone — and it went loudly, by this arm
		// failing, rather than by anyone remembering to come back.
		//
		// Re-pointed at what it was really protecting: that a deferring import **parks** rather than
		// silently succeeding or hanging. That claim outlives the refusal, so it is the one kept —
		// strictly stronger now, because the park must also *end correctly*.
		h := NewHost(io.Discard, io.Discard, nil)
		resolvers := make(chan func(canon.Value), 4)
		var entered int32
		h.asyncImpls = map[string]asyncLowerImpl{
			"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
				params := onStart()
				atomic.AddInt32(&entered, 1)
				arg := uint32(0)
				if len(params) > 0 {
					arg = uint32(params[0].Int32())
				}
				// Deferred: the guest MUST park, because nothing resolves before this returns.
				resolvers <- func(canon.Value) { onResolve(canon.U32(arg + 100)) }
				return func() {}, nil
			},
		}
		in, iErr := InstantiateWithHost(b, h)
		if iErr != nil {
			t.Fatalf("instantiate: %v", iErr)
		}
		defer in.Close()

		done := make(chan struct{})
		var got []canon.Value
		var cErr error
		go func() { got, cErr = in.CallValues("run", canon.U32(1)); close(done) }()

		// Resolve only once the import has been entered, so the resolution cannot beat the park. If it
		// could, this arm would pass without a park ever happening — the vacuity the deferral is for.
		select {
		case resolve := <-resolvers:
			resolve(canon.Value{})
		case <-time.After(5 * time.Second):
			t.Fatal("the host impl was never entered, so no park was reached")
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the lift never resumed after its waitable set was signalled — a park did not wake")
		}
		if cErr != nil {
			t.Fatalf("a parked lift failed to complete after resolution: %v", cErr)
		}
		if atomic.LoadInt32(&entered) != 1 {
			t.Errorf("the host impl was entered %d time(s), want 1", atomic.LoadInt32(&entered))
		}
		// Against wasmtime's committed reading, like the first arm: a park that resumes with the WRONG
		// value is the failure a completion-only check cannot see.
		if len(got) != 1 {
			t.Fatalf("run(1) returned %d values, want 1", len(got))
		}
		v, ok := got[0].U32()
		if !ok {
			t.Fatalf("run(1) returned kind %v, want u32", got[0].Type.Kind)
		}
		if want := wantInline(t, "1"); itoa(v) != want {
			t.Errorf("a resumed park returned %d, wasmtime's committed reading says %s", v, want)
		}
	})
}
