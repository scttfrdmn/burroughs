// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs"
)

// TestConcurrentCallsIntoANestedComponentDoNotInterleaveAResultStore is ADR 0101's result-store amendment
// on the PUBLIC path: two concurrent `Component.Call`s into a parent that awaits a nested child component.
//
// `xc.wasm`'s parent `run(id)` awaits the child's `work(id)`. `work(2)` yields 3000 times before
// returning 102, and `work(1)` returns 101 at once. Caller 1 then spins over caller 2's result word
// while executing, and reports in bit 16 whether that word changed under it. wasmtime 49.0.2 returns
// 101 and 102, 3 of 3 each.
//
// **What it can and cannot catch.** Its passing direction is deterministic: under the fix the child's
// store into the parent waits for the parent's run slot, so caller 1 can never see it land. Its failing
// direction depends on timing. Caller 2's child must resolve during caller 1's spin, and only an embedder
// hook could gate that, which the public API does not have (ADR 0096). The deterministic witness for the
// same path is `internal/component`'s `TestAChildsResultStoreWaitsForTheParentsExecutingTask`, which gates
// the child on a test host import. This one holds the public door to the same answer.
func TestConcurrentCallsIntoANestedComponentDoNotInterleaveAResultStore(t *testing.T) {
	wasm, err := os.ReadFile("internal/component/testdata/xc.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer c.Close()
	type out struct {
		v   uint32
		err error
	}
	call := func(id uint32, ch chan<- out) {
		res, e := c.Call(context.Background(), "run", burroughs.ComponentU32(id))
		if e != nil {
			ch <- out{err: e}
			return
		}
		v, _ := res[0].U32()
		ch <- out{v: v}
	}
	r2, r1 := make(chan out, 1), make(chan out, 1)
	go call(2, r2)
	go call(1, r1)
	for _, w := range []struct {
		id   uint32
		ch   chan out
		want uint32
	}{{1, r1, 101}, {2, r2, 102}} {
		select {
		case o := <-w.ch:
			if o.err != nil {
				t.Fatalf("run(%d): %v", w.id, o.err)
			}
			if o.v != w.want {
				t.Errorf("run(%d) returned %#x; wasmtime returns %d. Bit 16 set means caller 1 saw the "+
					"child's result store land while it executed", w.id, o.v, w.want)
			}
		case <-time.After(60 * time.Second):
			t.Fatalf("run(%d) never returned", w.id)
		}
	}
}
