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
	// The waitset synth guest, because its lifts are SYNC lifts: it reaches a host import without going
	// through the async-lift loop, so the measurement is not blocked by the at-most-one assertion the
	// slice is about to change. Measuring `Invoke`'s thread assignment needs no async lift at all — the
	// question is about the engine's threading, not about lifts.
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}

	var mu sync.Mutex
	var seen []interp.ThreadID
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var released sync.Once

	h := NewHost(io.Discard, io.Discard, nil)
	var calls int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
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
	ci := coreInstanceWithExport(in, "run")
	if ci == nil {
		t.Fatal("no core instance exports run")
	}

	// **Both callers must be `run`, because `run` is the export that calls `op`.** A first draft used
	// `run` and `sibling`, and only one caller ever reached the import — `sibling` returns 42 without
	// touching a host import, so the pair could not be compared. The failure was legible ("only 1 of 2
	// callers reached the host import") rather than a false agreement, which is the arm's own vacuity
	// guard working.
	//
	// The second `Invoke` proceeds only once the first has PARKED in `waitable-set.wait`, whose
	// `c.Blocking` excursion releases the engine thread (the H-1/H-4 sibling-progress property). So this
	// pair is concurrent in the sense that matters: both callers are inside the instance at once.
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := ci.Invoke("run")
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
		case <-done:
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
