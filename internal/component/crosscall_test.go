// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// crossCallReports collects the parent's `report(kind, value)` calls in order.
type crossCallReports struct {
	mu   sync.Mutex
	got  [][2]uint32
	tick []uint32 // the `id` the CHILD received, per call
}

func (r *crossCallReports) report(kind, value uint32) {
	r.mu.Lock()
	r.got = append(r.got, [2]uint32{kind, value})
	r.mu.Unlock()
}

func (r *crossCallReports) addTick(id uint32) {
	r.mu.Lock()
	r.tick = append(r.tick, id)
	r.mu.Unlock()
}

func (r *crossCallReports) valueFor(kind uint32) (uint32, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.got {
		if g[0] == kind {
			return g[1], true
		}
	}
	return 0, false
}

func (r *crossCallReports) snapshot() ([][2]uint32, []uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]uint32(nil), r.got...), append([]uint32(nil), r.tick...)
}

// crossCallHost builds a host for `call-wat-parent/composed.wasm`: the child's `tick` (async) and `note`
// (sync), plus the parent's `report`. `deferTick` controls whether `tick` resolves inline or from another
// goroutine after a delay.
func crossCallHost(t *testing.T, r *crossCallReports, deferTick bool, tickValue uint32) *Host {
	t.Helper()
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			params := onStart()
			if len(params) > 0 {
				r.addTick(uint32(params[0].Bits))
			}
			if deferTick {
				go func() {
					time.Sleep(20 * time.Millisecond)
					onResolve(canon.U32(tickValue))
				}()
			} else {
				onResolve(canon.U32(tickValue))
			}
			return func() {}, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil },
		"report": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			if len(args) >= 2 {
				r.report(uint32(args[0].Bits), uint32(args[1].Bits))
			}
			return nil, nil
		},
	}
	return h
}

// TestACrossComponentAsyncCallCarriesParamsAndResult is #888's witness: a composed parent's async
// `canon lower` whose callee is **another component's async lift** (ADR 0095).
//
// # Why no composed artefact could witness this before
//
// Every composed artefact in the tree **cancels**: #862 built them to measure cancellation. The only
// other composed test (`resultlist_test.go`) asserts load and instantiate without calling. So the
// capability had no end-to-end arm, and a cancelling witness would have failed on #892 — the model waits
// for resolution on a sync-lowered `subtask.cancel` and Burroughs returns BLOCKED — for a reason that is
// not about the call. `call-wat-parent/` is the completing path, authored for this.
//
// # What it establishes, and why the value matters more than the success
//
// The value crosses **two** boundaries: the host's `tick` resolves the child's import, the child's
// `task.return` resolves the parent's subtask, and the parent's own sync lift returns it to the host. So
// `go() == tickValue` is a claim about the whole chain.
//
// It is deliberately **not** 0, not the `id` the parent passes, and not 1. The one piece of this slice
// with no prior code path is lifting the child's flat result into the parent's component value, and a
// mis-lift is a value of the right type with the wrong contents — a 0 would have passed against a
// zero-initialised read, and the `id` would have passed against an implementation that echoed the
// argument.
//
// `tick` also records the `id` it received, so the param direction is asserted too. A witness that only
// checked the result would pass against an engine that dropped the parent's arguments and had the child
// return a constant.
func TestACrossComponentAsyncCallCarriesParamsAndResult(t *testing.T) {
	// The parent passes id=7 (committed in parent.wat) and the host resolves with 49 — distinct from the
	// id, from 0, and from 1, for the reasons in the doc comment.
	const (
		wantID    = 7
		tickValue = 49
	)
	for _, tc := range []struct {
		name   string
		defer_ bool
	}{
		// Both arms are kept even though they reach the same engine path, and **that sameness is the
		// finding**: a `wit-bindgen` child's lift always returns WAIT, so the parent's lower sees STARTED
		// whatever the host's latency. The inline-resolving host does not shorten the chain. Asserted
		// below as the state, so if a future child does resolve inline this test says so rather than
		// silently changing which arm it covers.
		{"tick_resolves_inline", false},
		{"tick_resolves_later", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			b, err := os.ReadFile("testdata/asynclift/call-wat-parent/composed.wasm")
			if err != nil {
				t.Fatalf("the committed composed artefact is missing: %v", err)
			}
			r := &crossCallReports{}
			in, err := InstantiateWithHost(b, crossCallHost(t, r, tc.defer_, tickValue))
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()

			res, cErr := in.CallValues("go")
			reports, ticks := r.snapshot()
			t.Logf("reports=%v ticks=%v", reports, ticks)
			if cErr != nil {
				// **Two repairs reach this line, and the message names both rather than presuming one.**
				// A first draft asserted the pre-#888 cause here; neutering showed the sync-result repair
				// fails the same assertion with a different error, and the reports above distinguish
				// them — an empty `reports` means the call never happened, a populated one means it did
				// and only the return path failed.
				//
				//   - `link refused: core import ""::"run" is not provided (stub host)` — the lower could
				//     not express a non-host callee. The import was never unwired; it resolved to the
				//     child's lift. Reports will be empty. (This message read `import ::run` before
				//     #888 re-worded it; see `refuse`.)
				//   - `declares a u32 result but the guest returned 0 flat value(s)` — a sync lift's
				//     results were discarded between the core func and the caller. Reports will show the
				//     cross-component call completing.
				t.Fatalf("go() refused (%v) with reports=%v — see this line's comment for which repair "+
					"each error points at", cErr, reports)
			}

			// ## The result crossed both boundaries
			if len(res) != 1 {
				t.Fatalf("go() returned %d values, want 1", len(res))
			}
			if got, ok := res[0].U32(); !ok || got != tickValue {
				t.Errorf("go() = %v, want u32 %d — the child's task.return value must reach the parent's "+
					"retptr and then its caller", res[0], tickValue)
			}

			// ## The params crossed the other way
			if len(ticks) != 1 || ticks[0] != wantID {
				t.Errorf("the child's `tick` saw ids %v, want exactly [%d] — the parent's argument must "+
					"reach the child, and a result-only assertion would pass against an engine that "+
					"dropped it", ticks, wantID)
			}

			// ## WHICH arm the engine took, so the coverage cannot drift silently
			state, ok := r.valueFor(1)
			if !ok {
				t.Fatal("the parent reported no lower state (kind 1); the reading cannot say which arm " +
					"this is")
			}
			if state != uint32(subtaskStarted) {
				t.Errorf("the lower returned state %d, want %d (STARTED) — a `wit-bindgen` child's lift "+
					"always returns WAIT, so the parent's lower cannot see RETURNED inline. If this now "+
					"reads 2, the inline arm became reachable and it has no witness yet",
					state, subtaskStarted)
			}
			// kind 3 is the post-park read; kind 2 would be the inline arm, which is unreachable here.
			if _, inline := r.valueFor(2); inline {
				t.Error("the parent took its INLINE arm (kind 2), which this child cannot reach — the " +
					"fixture or the child changed, and the park arm is now unwitnessed")
			}
			if v, ok := r.valueFor(3); !ok || v != tickValue {
				t.Errorf("post-park read (kind 3) = %d/%v, want %d", v, ok, tickValue)
			}
		})
	}
}

// TestASyncLiftsResultsReachItsCaller pins the repair that #888 found twice from opposite directions.
//
// `invokeWith`'s sync arm was `_, err := f.core.inst.Invoke(...); return nil, err`, with a comment saying
// a sync lift "moves no values through this path" — true of the callers that existed, false of the path.
//
// Two consumers needed them. `CallValues` on a sync export declaring a result refused with *"the guest
// returned 0 flat value(s); one is expected"* **when the guest had returned one**. And the sync
// cross-component arm hands its caller whatever `invokeWith` returns, so a sync guest-to-guest call would
// have silently produced no result — a wrong value reported as success, which is the worse of the two.
//
// The witness is `call-wat-parent`'s own `go`, which is a **sync** lift returning `u32`: the assertion
// above that `go() == 49` is exactly this repair, and this test states it separately so the fact has a
// name rather than riding inside a cross-component claim.
func TestASyncLiftsResultsReachItsCaller(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/call-wat-parent/composed.wasm")
	if err != nil {
		t.Fatalf("the committed composed artefact is missing: %v", err)
	}
	r := &crossCallReports{}
	in, err := InstantiateWithHost(b, crossCallHost(t, r, false, 49))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	res, err := in.CallValues("go")
	if err != nil {
		t.Fatalf("go(): %v", err)
	}
	// The export's declared result is what `CallValues` checks against, so a discarded result surfaces as
	// an arity complaint about the guest rather than as a missing value — which is why the pre-repair
	// failure named the guest and not the engine.
	if len(res) != 1 {
		t.Fatalf("a sync lift declaring a u32 result returned %d values; its results were discarded "+
			"between the core func and the caller", len(res))
	}
}

// TestValKindNameIsTotal keeps `valKindName` from going partial again.
//
// It was partial until #888: it named only the kinds its first consumer refused, so `string` and every
// numeric fell to `valkind(N)`. The cross-component refusal's own witness caught the message reading
// `valkind(12)` — which means the repair was found by a test rather than by review, and nothing would
// have caught the *next* kind added to the enum.
//
// **The domain is derived, not enumerated.** Listing the kinds here would be the same literal-duplicating
// -a-type's-property defect one level up: correct once, and then a second place to forget. The range runs
// from the first constant to the last, both named, so adding a kind to the enum extends this test's
// population automatically and a kind with no name fails it.
func TestValKindNameIsTotal(t *testing.T) {
	var unnamed []string
	for k := VBool; k <= VUnresolvedAlias; k++ {
		if name := valKindName(k); strings.HasPrefix(name, "valkind(") {
			unnamed = append(unnamed, name)
		}
	}
	if len(unnamed) != 0 {
		t.Errorf("these kinds have no name, so any refusal naming one is unreadable: %v\n"+
			"Add them to valKindName rather than to a second namer beside it (grave #885's lesson: the "+
			"duplication is the defect and the missing case is its symptom).", unnamed)
	}
	// A coverage count, printed rather than assumed: a loop whose bounds were wrong would pass the
	// assertion above by checking nothing, which is the failure mode a bare green cannot show.
	t.Logf("valKindName is total over %d kinds (VBool..VUnresolvedAlias)", int(VUnresolvedAlias-VBool)+1)
}

// TestACrossComponentResultShapeRefusesByName pins the scope boundary ADR 0095 draws.
//
// A child's `task.return` hands over flat core values **in the child's own ABI**. A scalar is the value;
// anything aggregate is a *pointer into the child's memory*, which the parent cannot read — two
// components, two memories, two allocators — so carrying one means copying through the parent's `realloc`
// with the child's memory as the source. That is real work with its own witnesses and it is not what #888
// registered.
//
// So an aggregate refuses **by name**, and the refusal names the kind so a reader knows which shape to
// build next. Asserted directly on the lift rather than through a guest, because no composed artefact in
// the tree returns an aggregate — and *a negative claim buys a branch an exemption* only if something
// checks the exemption.
func TestACrossComponentResultShapeRefusesByName(t *testing.T) {
	t.Run("scalar_is_carried", func(t *testing.T) {
		sig := &FuncType{Result: &ValType{Kind: VU32}}
		v, err := crossComponentValue(sig, []interp.Value{interp.I32(49)})
		if err != nil {
			t.Fatalf("a u32 result: %v, want carried", err)
		}
		got, ok := v.U32()
		if !ok || got != 49 {
			t.Errorf("lifted %v, want u32 49", v)
		}
	})

	t.Run("no_result_is_carried", func(t *testing.T) {
		// "Returns nothing" is a shape this carries perfectly, and it is grave #885's guest exactly — so
		// it must not land in the refusal arm with the aggregates.
		if _, err := crossComponentValue(&FuncType{}, nil); err != nil {
			t.Errorf("a no-result signature: %v, want carried", err)
		}
	})

	t.Run("string_refuses_naming_the_kind", func(t *testing.T) {
		sig := &FuncType{Result: &ValType{Kind: VString}}
		_, err := crossComponentValue(sig, []interp.Value{interp.I32(0)})
		if !errors.Is(err, ErrCrossComponentResult) {
			t.Fatalf("a string result: %v, want ErrCrossComponentResult", err)
		}
		// The kind must be in the message. A bare "unsupported" would leave the next author guessing
		// which shape to build, which is the difference between a boundary and a dead end.
		if !strings.Contains(err.Error(), "string") {
			t.Errorf("the refusal does not name the kind: %v", err)
		}
	})

	t.Run("wrong_arity_refuses", func(t *testing.T) {
		sig := &FuncType{Result: &ValType{Kind: VU32}}
		if _, err := crossComponentValue(sig, nil); !errors.Is(err, ErrCrossComponentResult) {
			t.Errorf("a u32 result with no flat values: %v, want ErrCrossComponentResult", err)
		}
	})
}
