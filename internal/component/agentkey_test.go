// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestConcurrentHostCallsShareOneThreadID is the measurement #869's design turns on, and it **refutes**
// the premise the slice was about to be built from.
//
// # The premise, and why it was not safe to assume
//
// #869's plan was to key the current lift task by the calling agent — `CanonCaller.Thread()` — in place
// of one slot per instance. That is only safe if two concurrent calls into one instance present
// *different* thread IDs to the built-ins. I had argued they must, from a true but insufficient fact:
// one lift loop drives one task on its caller's goroutine, so tasks never interleave *within* a
// goroutine.
//
// **That proves nothing about what the built-ins see.** Interleaving within a goroutine and identity
// across goroutines are different questions, and only the second decides the key.
//
// # What it measures
//
// Two concurrent `Invoke`s on one instance, each recording the `Thread()` its host import is handed.
// **They are the same.** `Instance.invokeIndex` runs every call on `&in.host` — one thread per instance,
// named directly in its own comment — so an embedder calling from two goroutines is two goroutines on one
// engine thread, not two agents.
//
// # The consequence, which is the whole reason this is committed
//
// Keying by `Thread()` would have put both tasks in **one** map entry. With the cross-agent trap lifted,
// the second task would overwrite the first's slot and `task.return` would resolve the wrong task —
// **a silently crossed result where there used to be a trap.** That is the exact failure #869 exists to
// prevent, and it would have been introduced by the change meant to prevent it.
//
// So this is a witness and not a probe: a later change to how `Invoke` assigns threads must not be able
// to quietly make per-agent keying look viable again. If this test ever fails, the key choice in
// `asyncHandles` is back open, and the right response is to re-read #869's reasoning rather than to
// relax the assertion.
func TestConcurrentHostCallsShareOneThreadID(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	// **The `suspending` guest, an async lift that parks in its imported `tick`** (#1040's re-reading). This
	// test used the waitset synth guest, a SYNC lift, chosen so the measurement would not meet an at-most-one
	// async-lift assertion that has since been lifted (`TestASecondLiftIsPermittedWhileTheFirstIsParked`).
	//
	// **That guest's pair stopped being concurrent on 2026-10-16**, and nothing noticed. Since #1005, a sync
	// `waitable-set.wait` with no Thread to satisfy it traps at the reference's sync tail, so the first caller
	// trapped right after entering `op`. The second caller reached the import only because the instance kept
	// running after a trap. The store lockdown refuses that second call, and the test failed with "only 1 of
	// 2 callers". The async lift parks without trapping, so both callers are genuinely in flight again, and
	// both calls are now checked to succeed.
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	var mu sync.Mutex
	var seen []interp.ThreadID
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var released sync.Once

	h := NewHost(io.Discard, io.Discard, nil)
	var calls int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			mu.Lock()
			seen = append(seen, c.Thread())
			mu.Unlock()
			n := atomic.AddInt32(&calls, 1)
			entered <- struct{}{}
			// **Both calls are held inside the import at once**, so the two IDs are compared while both
			// callers are genuinely in flight. Sampling them sequentially would compare one caller's ID
			// with a later caller's and could agree for the trivial reason that the first had finished.
			if n >= 2 {
				released.Do(func() { close(release) })
			}
			go func() {
				<-release
				onResolve(canon.U32(uint32(n)))
			}()
			return func() {}, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	// **Both callers are `run`, because `run` is the export that calls `tick`.** A first draft used `run`
	// and `sibling`, and only one caller ever reached the import. The failure was legible ("only 1 of 2
	// callers reached the host import") rather than a false agreement, which is the arm's own vacuity guard
	// working, and it worked again when the lockdown refused a call after a trap (#1040).
	//
	// The second call proceeds only once the first has PARKED in `tick`, which releases the instance's run
	// slot (ADR 0101). So this pair is concurrent in the sense that matters: both callers are in flight in
	// the instance at once.
	//
	// **Through `CallValues`, not the core instance's `Invoke`, since #975.** The core door has no run
	// slot, so two core `Invoke`s are two agents on one unshared memory (ADR 0073), and the race detector
	// reported their writes on Windows. The component door orders them. `CallValues` reaches the same
	// `invokeIndex` on the same `&in.host`, so the measurement is unchanged by the move.
	done := make(chan error, 2)
	for i := range 2 {
		go func() {
			_, e := in.CallValues("run", canon.U32(uint32(i)))
			done <- e
		}()
	}

	// Wait for both callers to be inside the import before reading what they saw.
	for range 2 {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			mu.Lock()
			got := len(seen)
			mu.Unlock()
			t.Fatalf("only %d of 2 callers reached the host import, so there is no concurrent pair to "+
				"compare", got)
		}
	}
	mu.Lock()
	ids := append([]interp.ThreadID(nil), seen...)
	mu.Unlock()

	for range 2 {
		select {
		case e := <-done:
			// Checked since #1040: an unchecked caller error is how a trapping first call passed here unseen.
			if e != nil {
				t.Fatalf("a caller failed: %v", e)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a caller never returned")
		}
	}

	if len(ids) != 2 {
		t.Fatalf("recorded %d thread IDs, want 2", len(ids))
	}
	// The finding. Asserted as EQUALITY because that is what was measured — not as a preference.
	if ids[0] != ids[1] {
		t.Fatalf("two concurrent callers presented DIFFERENT thread IDs (%v, %v).\n\n"+
			"This refutes the measurement #869's key choice rests on. `Instance.invokeIndex` ran every "+
			"call on `&in.host` when this was written, so concurrent host callers shared one engine "+
			"thread and keying the current lift task by `Thread()` would have collapsed two tasks into "+
			"one slot. If that has changed, per-agent keying may now be viable and the choice recorded "+
			"on #869 is back open — re-derive it, do not relax this assertion.", ids[0], ids[1])
	}
	t.Logf("two concurrent host callers both reported thread ID %v — one engine thread per instance", ids[0])
}
