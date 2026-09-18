// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The gate:async async-lower witnesses run against the oracle (canon/testdata/fixtures.json) through a
// production-faithful CanonCaller (interp.NewCanonCallerForTest — the SAME construction callAdapter uses,
// over a real memory), not a convenience caller. The sync-resolving arm (2a-i-A, async_lowers) returns
// [RETURNED] and lowers the result to the retptr; the blocking arm (2a-i-B, async_lower_blocking) returns
// [state|(subtaski<<4)] and leaves the retptr untouched (the callee started but did not resolve inline —
// the waitable-set loop that resolves it and delivers the event is 2a-i-B-2).

type asyncLowerFixture struct {
	Name       string `json:"name"`
	Args       []int  `json:"args"`
	ResultKind string `json:"result_kind"`
	RetValue   int64  `json:"ret_value"`
	Ret        []int  `json:"ret"`
	Retptr     *int   `json:"retptr"`
	MemoryHex  string `json:"memory_hex"`
}

type asyncLowerBlockingFixture struct {
	Name         string `json:"name"`
	Args         []int  `json:"args"`
	ResultKind   string `json:"result_kind"`
	Packed       []int  `json:"packed"`
	Subtaski     int    `json:"subtaski"`
	StateAtLower int    `json:"state_at_lower"`
	Retptr       *int   `json:"retptr"`
}

func loadAsyncLowers(t *testing.T) []asyncLowerFixture {
	t.Helper()
	var doc struct {
		AsyncLowers []asyncLowerFixture `json:"async_lowers"`
	}
	loadFixtures(t, &doc)
	if len(doc.AsyncLowers) == 0 {
		t.Fatal("no async_lowers in fixtures.json")
	}
	return doc.AsyncLowers
}

func loadAsyncLowerBlocking(t *testing.T) []asyncLowerBlockingFixture {
	t.Helper()
	var doc struct {
		AsyncLowerBlocking []asyncLowerBlockingFixture `json:"async_lower_blocking"`
	}
	loadFixtures(t, &doc)
	if len(doc.AsyncLowerBlocking) == 0 {
		t.Fatal("no async_lower_blocking in fixtures.json")
	}
	return doc.AsyncLowerBlocking
}

func loadFixtures(t *testing.T, v any) {
	t.Helper()
	b, err := os.ReadFile("canon/testdata/fixtures.json")
	if err != nil {
		t.Fatalf("reading fixtures: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parsing fixtures: %v", err)
	}
}

// resultValue builds the canon.Value a case's callee resolves with, from the fixture's kind + value.
func resultValue(t *testing.T, kind string, v int64) (canon.Value, bool) {
	t.Helper()
	switch kind {
	case "":
		return canon.Value{}, false // empty result
	case "u32":
		return canon.U32(uint32(v)), true
	case "u64":
		return canon.U64(uint64(v)), true
	default:
		t.Fatalf("async-lower fixture result kind %q not handled by this test", kind)
		return canon.Value{}, false
	}
}

func resultSize(kind string) uint64 {
	switch kind {
	case "u32":
		return 4
	case "u64":
		return 8
	}
	return 0
}

// TestAsyncLowerMatchesTheOracle is the sync-resolving arm's fixture-match: for each async_lowers case, an
// impl that resolves inline (onStart then onResolve) makes the wrapper return [RETURNED] and lower the
// resolved value to the retptr, byte-for-byte with the model.
func TestAsyncLowerMatchesTheOracle(t *testing.T) {
	for _, fx := range loadAsyncLowers(t) {
		t.Run(fx.Name, func(t *testing.T) {
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			v, hasResult := resultValue(t, fx.ResultKind, fx.RetValue)
			impl := func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
				onStart()
				onResolve(v) // resolves inline
				return func() {}, nil
			}
			const retptr = 16 // a clear region past the params
			args := make([]interp.Value, 0, len(fx.Args)+1)
			for _, a := range fx.Args {
				args = append(args, interp.I32(int32(a)))
			}
			if hasResult {
				args = append(args, interp.I32(retptr))
			}
			ret, err := asyncLowerFunc(impl, hasResult, newAsyncHandles())(cc, args)
			if err != nil {
				t.Fatalf("wrapper returned error on the sync-resolving arm: %v", err)
			}
			if len(ret) != len(fx.Ret) || (len(ret) == 1 && ret[0].Int32() != int32(fx.Ret[0])) {
				t.Errorf("packed return = %v, want %v (fixture)", ret, fx.Ret)
			}
			if !hasResult {
				return // empty result: no retptr write to check
			}
			size := resultSize(fx.ResultKind)
			got, err := cc.Read(retptr, size)
			if err != nil {
				t.Fatalf("reading retptr: %v", err)
			}
			mem, err := hex.DecodeString(fx.MemoryHex)
			if err != nil {
				t.Fatalf("decoding fixture memory: %v", err)
			}
			want := mem[*fx.Retptr : *fx.Retptr+int(size)]
			if !bytes.Equal(got, want) {
				t.Errorf("retptr bytes = %x, want %x (model's lowered result)", got, want)
			}
		})
	}
}

// TestAsyncLowerBlockingMatchesTheOracleLeavingRetptrUnchanged is the blocking arm's return-side match: an
// impl that STARTS but defers resolution (never calls onResolve) makes the wrapper register a subtask and
// return [state|(subtaski<<4)], byte-for-byte with async_lower_blocking's `packed`, and leaves the retptr
// untouched (the ordering caution — onResolve is the only writer, so a blocked callee lowers nothing). The
// wake-side event delivery is 2a-i-B-2; this slice pins only the return.
func TestAsyncLowerBlockingMatchesTheOracleLeavingRetptrUnchanged(t *testing.T) {
	for _, fx := range loadAsyncLowerBlocking(t) {
		t.Run(fx.Name, func(t *testing.T) {
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			hasResult := fx.ResultKind != ""
			const retptr = 16
			sentinel := []byte{0xDE, 0xAD, 0xBE, 0xEF}
			if hasResult {
				if werr := cc.Write(retptr, sentinel); werr != nil {
					t.Fatalf("seeding retptr: %v", werr)
				}
			}
			blocking := func(_ *interp.CanonCaller, onStart func() []interp.Value, _ func(canon.Value)) (func(), error) {
				onStart()             // STARTED
				return func() {}, nil // DEFER: never resolves inline — the blocking arm
			}
			args := make([]interp.Value, 0, len(fx.Args)+1)
			for _, a := range fx.Args {
				args = append(args, interp.I32(int32(a)))
			}
			if hasResult {
				args = append(args, interp.I32(retptr))
			}
			ret, err := asyncLowerFunc(blocking, hasResult, newAsyncHandles())(cc, args)
			if err != nil {
				t.Fatalf("wrapper returned error on the blocking arm: %v", err)
			}
			if len(ret) != len(fx.Packed) || (len(ret) == 1 && ret[0].Int32() != int32(fx.Packed[0])) {
				t.Errorf("packed return = %v, want %v (fixture: state %d | subtaski %d)",
					ret, fx.Packed, fx.StateAtLower, fx.Subtaski)
			}
			if !hasResult {
				return
			}
			got, err := cc.Read(retptr, 4)
			if err != nil {
				t.Fatalf("reading retptr: %v", err)
			}
			if !bytes.Equal(got, sentinel) {
				t.Errorf("retptr = %x after a blocked lower, want %x unchanged — a blocked callee must not "+
					"half-lower a result", got, sentinel)
			}
		})
	}
}

// TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt witnesses the narrowing per the #732-guard: gate on,
// an async-lower-only component is permitted through to the walk, while the surface the arm does not
// execute — an async built-in (the waitable-set loop is 2a-i-B-2), an async lift — still refuses by name.
// Both halves are witnessed (the refusal firing, not just the permit passing).
func TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1") // gate on

	lowerOnly := &Component{Canons: []Canon{{Kind: CanonLower, Opts: CanonOpts{Async: true}}}}
	if err := gateAsync(lowerOnly); err != nil {
		t.Errorf("gate on, async-lower-only: gateAsync refused (%v), want permit — the lower arms execute", err)
	}

	builtin := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x17}}} // future.write (genuinely unbuilt)
	if err := gateAsync(builtin); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, async built-in: err = %v, want ErrAsyncNotImplemented (refuse by name — 2a-i-B-2)", err)
	}

	// A no-callback (STACKFUL) async lift stays unbuilt (ADR 0086's stackless-vs-stackful choice: no guest
	// lifts stackfully), so it still refuses by name. The stackless (callback) arm is built — covered by
	// TestAsyncLiftExitOnlyResolvesViaTaskReturn, where a WITH-callback lift binds and runs.
	lift := &Component{Canons: []Canon{{Kind: CanonLift, Opts: CanonOpts{Async: true}}}}
	if err := gateAsync(lift); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, no-callback (stackful) async lift: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}

	// Increment 3: future.read (0x16) is permitted; future.write (0x17) still refuses by name.
	fread := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x16}}}
	if err := gateAsync(fread); err != nil {
		t.Errorf("gate on, future.read: gateAsync refused (%v), want permit — future.read is built", err)
	}
	fwrite := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x17}}} // future.write (unbuilt)
	if err := gateAsync(fwrite); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, future.write: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}

	// Stream write side (increment 3): stream.write (0x10) is permitted; stream.read (0x0f) still refuses.
	swrite := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x10}}}
	if err := gateAsync(swrite); err != nil {
		t.Errorf("gate on, stream.write: gateAsync refused (%v), want permit — stream.write is built", err)
	}
	sread := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x0f}}} // stream.read (unbuilt; guest doesn't bind it)
	if err := gateAsync(sread); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, stream.read: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}
	// stream.new (0x0e, increment 4): permitted — the guest mints both ends over one shared stream.
	snew := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x0e}}}
	if err := gateAsync(snew); err != nil {
		t.Errorf("gate on, stream.new: gateAsync refused (%v), want permit — stream.new is built", err)
	}
	// stream.drop-readable (0x13) and stream.drop-writable (0x14, increment 4): permitted — the guest drops
	// both ends after the copy. stream.cancel-read (0x11) remains refused (cancellation is a later increment).
	for _, op := range []byte{0x13, 0x14} {
		dropc := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: op}}}
		if err := gateAsync(dropc); err != nil {
			t.Errorf("gate on, stream drop %#x: gateAsync refused (%v), want permit — the drops are built", op, err)
		}
	}
	// stream.cancel-read (0x11, increment 4): permitted — the first running production of CANCELLED.
	scancelr := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x11}}}
	if err := gateAsync(scancelr); err != nil {
		t.Errorf("gate on, stream.cancel-read: gateAsync refused (%v), want permit — cancel-read is built", err)
	}
	// stream.cancel-write (0x12, increment 4): permitted — cancel-read's symmetric twin, now built.
	scancelw := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x12}}}
	if err := gateAsync(scancelw); err != nil {
		t.Errorf("gate on, stream.cancel-write: gateAsync refused (%v), want permit — cancel-write is built", err)
	}
	// subtask.cancel (0x06, increment 4): permitted — the subtask substrate's cancel path is built.
	subcancel := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x06}}}
	if err := gateAsync(subcancel); err != nil {
		t.Errorf("gate on, subtask.cancel: gateAsync refused (%v), want permit — subtask.cancel is built", err)
	}
	// subtask.drop (0x0d, increment 4): permitted — traps unless resolve-delivered.
	subdrop := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x0d}}}
	if err := gateAsync(subdrop); err != nil {
		t.Errorf("gate on, subtask.drop: gateAsync refused (%v), want permit — subtask.drop is built", err)
	}
	// waitable-set.poll (0x21, increment 4): permitted — wait minus the park, the last op in the tail.
	wpoll := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x21}}}
	if err := gateAsync(wpoll); err != nil {
		t.Errorf("gate on, waitable-set.poll: gateAsync refused (%v), want permit — poll is built", err)
	}
	// future.write (0x17) remains refused — a genuinely unbound op (the #734 guest binds no future writes).
	fwrite2 := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x17}}}
	if err := gateAsync(fwrite2); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, future.write: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}

	// Future AND stream value types are now permitted (both are handle types with a built op — future.read,
	// stream.write); the unbuilt operations on them refuse at the built-in check, not at the type.
	futVal := &Component{Types: []TypeDef{{Kind: TDVal, Val: ValType{Kind: VFuture, Elem: &ValType{Kind: VU32}}}}}
	if err := gateAsync(futVal); err != nil {
		t.Errorf("gate on, future value type: gateAsync refused (%v), want permit", err)
	}
	streamVal := &Component{Types: []TypeDef{{Kind: TDVal, Val: ValType{Kind: VStream, Elem: &ValType{Kind: VU8}}}}}
	if err := gateAsync(streamVal); err != nil {
		t.Errorf("gate on, stream value type: gateAsync refused (%v), want permit — stream.write is built", err)
	}
}

// TestSynthAsyncLowerBindsAndReachesTheWrapper is the binding-branch end-to-end witness: a real-bytes
// async-lower-only component (testdata/async-lower-synth.wasm), gate on, instantiates and its async lower
// binds through the SEPARATE async-impl source to the wrapper — so Call("run") reaches the impl with the
// lifted param. Its impl resolves inline (the sync-resolving arm).
func TestSynthAsyncLowerBindsAndReachesTheWrapper(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1") // gate on
	b, err := os.ReadFile("testdata/async-lower-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	called := false
	var gotX int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			params := onStart()
			called = true
			if len(params) > 0 {
				gotX = params[0].Int32()
			}
			onResolve(canon.U32(107)) // resolves inline
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("gate on, async-lower-only synth: instantiate refused (%v), want permit + bind", err)
	}
	defer in.Close()
	if err := in.Call("run"); err != nil {
		t.Fatalf("Call(run): %v", err)
	}
	if !called {
		t.Error("the async lower's impl was not called — the binding did not route to the async wrapper")
	}
	if gotX != 7 {
		t.Errorf("impl got x=%d, want 7 — the wrapper did not pass the lifted param", gotX)
	}
}

// TestSubtaskCancelProducesCancelledStates pins subtask.cancel (0x06) against the committed oracle, driving
// the whole path at unit level: a deferring async-lowered impl parks a subtask, then subtask.cancel invokes
// the impl's onCancel, which resolves it. The terminal state is chosen by whether the subtask had started —
// CANCELLED_BEFORE_STARTED (3) if not, CANCELLED_BEFORE_RETURNED (4) if so — the cancel-resolution path the
// re-check found missing (before this increment onResolve produced only RETURNED). A callee that does not
// resolve during the cancel returns BLOCKED (the model yields; Burroughs has no yield). This test exercises
// async_lower.go's new cancel branch AND subtaskCancel together, not one in isolation.
func TestSubtaskCancelProducesCancelledStates(t *testing.T) {
	var doc struct {
		SubtaskCancel struct {
			StartedCancelRet     []int `json:"started_cancel_ret"`
			StartedStateAtLower  int   `json:"started_state_at_lower"`
			StartingCancelRet    []int `json:"starting_cancel_ret"`
			StartingStateAtLower int   `json:"starting_state_at_lower"`
			AsyncCancelRet       []int `json:"async_cancel_ret"`
		} `json:"subtask_cancel"`
	}
	loadFixtures(t, &doc)
	fx := doc.SubtaskCancel

	// lower a deferring impl; return the parked subtask's index, its state at lower, and the table/caller.
	lower := func(t *testing.T, callOnStart, inlineResolve bool) (*asyncHandles, *interp.CanonCaller, uint32, int) {
		t.Helper()
		h := newAsyncHandles()
		cc, err := interp.NewCanonCallerForTest(1)
		if err != nil {
			t.Fatalf("caller: %v", err)
		}
		impl := func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			if callOnStart {
				onStart() // STARTING -> STARTED
			}
			return func() {
				if inlineResolve {
					onResolve(canon.Value{}) // the callee resolves on cancel; value ignored on the cancel path
				}
			}, nil
		}
		lowerRet, lerr := asyncLowerFunc(impl, false, h)(cc, nil)
		if lerr != nil {
			t.Fatalf("async lower: %v", lerr)
		}
		packed := uint32(lowerRet[0].Int32())
		return h, cc, packed >> 4, int(packed & 0xf)
	}

	cancel := func(t *testing.T, h *asyncHandles, cc *interp.CanonCaller, subtaski uint32) uint32 {
		t.Helper()
		ret, err := subtaskCancel(h)(cc, []interp.Value{interp.I32(int32(subtaski))})
		if err != nil {
			t.Fatalf("subtask.cancel: %v", err)
		}
		return uint32(ret[0].Int32())
	}

	// STARTED -> CANCELLED_BEFORE_RETURNED (4)
	h, cc, sti, sal := lower(t, true, true)
	if sal != fx.StartedStateAtLower {
		t.Errorf("state at lower = %d, want %d (STARTED)", sal, fx.StartedStateAtLower)
	}
	if got := cancel(t, h, cc, sti); int(got) != fx.StartedCancelRet[0] {
		t.Errorf("started cancel = %d, want %d (CANCELLED_BEFORE_RETURNED)", got, fx.StartedCancelRet[0])
	}

	// STARTING -> CANCELLED_BEFORE_STARTED (3)
	h, cc, sti, sal = lower(t, false, true)
	if sal != fx.StartingStateAtLower {
		t.Errorf("state at lower = %d, want %d (STARTING)", sal, fx.StartingStateAtLower)
	}
	if got := cancel(t, h, cc, sti); int(got) != fx.StartingCancelRet[0] {
		t.Errorf("starting cancel = %d, want %d (CANCELLED_BEFORE_STARTED)", got, fx.StartingCancelRet[0])
	}

	// callee does not resolve during the cancel -> BLOCKED (the yield maps to BLOCKED, not a spin)
	h, cc, sti, _ = lower(t, true, false)
	if got := cancel(t, h, cc, sti); got != uint32(fx.AsyncCancelRet[0]) {
		t.Errorf("async cancel = %#x, want %#x (BLOCKED)", got, uint32(fx.AsyncCancelRet[0]))
	}
}
