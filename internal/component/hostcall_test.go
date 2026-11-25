// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// instantiateHostCallOwn brings up host-call-own.wasm against th, in one gate state.
func instantiateHostCallOwn(t *testing.T, th *thingHost, gate string) *Instantiated {
	t.Helper()
	t.Setenv(asyncGateEnv, "1")
	t.Setenv(cmThreadsGateEnv, gate)
	wasm, err := os.ReadFile("testdata/host-call-own.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(wasm, th.Host)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	return in
}

// TestAHostCallIsAnsweredAtTaskReturnWhileTheTaskRuns is E2-in-a's host witness (ADR 0136): a host call of an async
// export taking an `own<thing>` and returning `result<own<thing>, u32>` is answered at the guest's `task.return`,
// with its own back in the host's table, while the guest's task is still running; and the task then runs on to its
// exit, which is what the call returns at.
//
// **The place, not a duration**: when the answer arrives, the guest is parked on a future nobody has written, which
// only `release` writes. So the call has not returned, and the guest's `exited` reads 0, by construction. Then
// `release` writes the future, the guest's callback EXITs, and the call returns.
//
// The own crosses both ways: the guest's `poke` reads the id of the object the host minted, so the callee's table
// held it at the index the guest was handed, and the answer's own, taken from the host's table, is that object.
//
// **NEUTER, run:** taskReturn skips the hook, and the host is never answered (this fails on its bound, the guest
// parked as designed). The own arm refuses every own, and the call ends before an answer, its task.return refused.
// lowerPending patches the host's index without moving the handle, and the guest's borrow of it traps.
func TestAHostCallIsAnsweredAtTaskReturnWhileTheTaskRuns(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("gate="+gate, func(t *testing.T) {
			th := newThingHost()
			in := instantiateHostCallOwn(t, th, gate)
			defer in.Close()

			held := newHostTable()
			want := &thing{id: 41}
			idx := held.mint(th.res, want)

			answered := make(chan []canon.Value, 1)
			done := make(chan error, 1)
			go func() {
				done <- in.callReturning(context.Background(), "handle", held,
					func(r []canon.Value) { answered <- r }, canon.Own(th.res.rt, uint32(idx)))
			}()

			var res []canon.Value
			select {
			case res = <-answered:
			case err := <-done:
				t.Fatalf("the call ended before the host was answered: %v", err)
			case <-time.After(optionABound):
				t.Fatalf("the host was not answered within %s; the guest returned and parked", optionABound)
			}

			// The place: answered, and the task has not exited.
			select {
			case err := <-done:
				t.Fatalf("the call returned (%v) when the host was answered; the guest is parked on a future "+
					"only release writes, so its task cannot have exited", err)
			default:
			}
			if got, err := callU32(in, "exited"); err != nil || got != 0 {
				t.Fatalf("exited = %d, %v at the answer; want 0, the guest parked past its task.return", got, err)
			}

			// The answer: ok(own), and the own is the host's object, back in the host's table.
			if len(res) != 1 {
				t.Fatalf("answered with %d values, want 1", len(res))
			}
			c, payload, ok := res[0].Case()
			if !ok || c != "ok" || payload == nil {
				t.Fatalf("answered %v, want ok(own<thing>)", res[0])
			}
			h, ok := payload.OwnHandle()
			if !ok {
				t.Fatalf("the ok payload is %s, not an own", payload.Type)
			}
			got, err := takeAs[*thing](held, th.res, int32(h))
			if err != nil {
				t.Fatalf("the answer's own %d is not in the host's table: %v", h, err)
			}
			if got != want {
				t.Fatalf("the answer's own holds %+v, want the object the host passed (%+v)", got, want)
			}
			if id, err := callU32(in, "id"); err != nil || id != 41 {
				t.Fatalf("the guest's borrow read id %d, %v; want 41, the host's object in the callee's table", id, err)
			}

			// Released, the task runs on to its exit, and the call returns then.
			if code, err := callU32(in, "release"); err != nil || code != 0 {
				t.Fatalf("release's future.write = %d, %v; want 0 (COMPLETED), the guest's read pending", code, err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("the call ended with %v after the guest's exit", err)
				}
			case <-time.After(optionABound):
				t.Fatalf("the call did not return within %s of release", optionABound)
			}
			if got, err := callU32(in, "exited"); err != nil || got != 1 {
				t.Fatalf("exited = %d, %v after the call returned; want 1", got, err)
			}
			in.Close()
			in.Close() // a second Close probes that the first closed everything
		})
	}
}

// TestAnOwnResultIsRefusedWithoutAHostTable reads host-call-own's `make`, an async export whose result carries an own
// the guest mints through the host's import (ADR 0136). An embedder's CallValues has no table for the own to enter,
// so it is refused by name before the guest runs, and `made` stays 0. On main the guest ran and its task.return
// trapped, blaming the guest for the host's limit and locking the store. A host call, with a table, is answered
// with that own: the thing the host's `make` minted, id 101. And callReturning refuses a sync lift, which has no
// task.return to be answered at.
//
// **NEUTER, run:** drop CallValues' refusal, and the call reaches the guest, whose task.return traps: "task.return's
// variant{ok(own<rt=1>), error(u32)} value: … its caller has no table to move it into".
func TestAnOwnResultIsRefusedWithoutAHostTable(t *testing.T) {
	th := newThingHost()
	in := instantiateHostCallOwn(t, th, "0")
	defer in.Close()

	_, err := in.CallValues("make")
	if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "returns an own") {
		t.Fatalf("CallValues(make) = %v; want the own result refused by name before the call", err)
	}
	if made, merr := callU32(in, "made"); merr != nil || made != 0 {
		t.Fatalf("made = %d, %v; want 0, the guest never ran", made, merr)
	}

	held := newHostTable()
	var res []canon.Value
	if cerr := in.callReturning(context.Background(), "make", held, func(r []canon.Value) { res = r }); cerr != nil {
		t.Fatalf("callReturning(make) = %v", cerr)
	}
	if len(res) != 1 {
		t.Fatalf("make answered with %d values, want 1", len(res))
	}
	c, payload, ok := res[0].Case()
	if !ok || c != "ok" || payload == nil {
		t.Fatalf("make answered %v, want ok(own<thing>)", res[0])
	}
	h, _ := payload.OwnHandle()
	got, err := takeAs[*thing](held, th.res, int32(h))
	if err != nil || got.id != 101 {
		t.Fatalf("make's own %d in the host's table: %+v, %v; want the thing the host's import minted, id 101", h, got, err)
	}
	if left := liveObjectsOf[*thing](in.w.hostRes); left != 0 {
		t.Fatalf("the guest's table still holds %d things; its own moved to the host's table", left)
	}

	err = in.callReturning(context.Background(), "exited", nil, func([]canon.Value) {})
	if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "sync lift") {
		t.Fatalf("callReturning(exited) = %v; want a sync lift refused by name", err)
	}
}
