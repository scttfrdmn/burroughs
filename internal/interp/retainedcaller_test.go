// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestARetainedCallersWritesSurviveARelocatingGrow is the witness ADR 0073's **decision 6** never had
// (#916).
//
// # Why this was missing, and why the gap is not where it looks
//
// ADR 0073 has two exclusions and they guard different agents:
//
//  1. **The world count.** A relocating grow refuses while any world holds an agent besides the grower —
//     `relocate` returns false unless `soleAgentLocked(self)` for every world. That arm is witnessed by
//     `TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage`, whose sibling is a host call
//     **parked inside the guest** and which ignores its `Caller` entirely.
//  2. **`growMu` as an `RWMutex`** — decision 6. `memory.go`'s own field comment says why it is not a
//     plain `Mutex`: *"a retained `Caller` is an agent no `world` count sees … and the relocating arm
//     must exclude it some other way."*
//
// So the arm with a test is the one the count already handles, and the arm the **read lock** exists for
// had none. Searched on the #915 review: of every test in this package that both spawns a goroutine and
// calls `.Write(`, there was one, and it never grows.
//
// **It is reachable today, not only in theory**: the public core API hands a host function a `*Caller`,
// and nothing refuses one that is kept. `Caller`'s own doc says so — *"Nothing here refuses a retained
// `Caller`."*
//
// # What makes the relocation happen rather than refuse
//
// Two things, and both are load-bearing:
//
//   - `withoutReservation` puts the memory on the allocator's fallback path, because a reserved memory
//     grows by reslicing into its capacity and **never relocates** (ADR 0076's M-1). The `cap == len`
//     guard below is what says so if that ever stops being true.
//   - The host call that produces the `Caller` **returns** before the grow. Its agent leaves the world
//     count, so `soleAgentLocked` is satisfied and the grow takes the relocating arm — while the
//     retained `Caller` is still writing. That is the whole hazard: an agent the count cannot see.
//
// # The detector is the write's own payload, read back
//
// Each iteration writes a fresh value and reads it back through the same retained `Caller`. A write that
// landed in the abandoned image is **not** in the image the following read resolves, so the readback
// mismatches — *read a write's payload back rather than its status flag.* That is a per-iteration verdict
// rather than a final-state comparison, which a later write would otherwise mask.
//
// # `-race` is the reliable channel, and the lost-write assertion is the opportunistic one
//
// Measured, not assumed. With the read lock removed: `-race` reports the race **5 times out of 5**,
// because the writer's `copy` into the old array and `relocate`'s `copy` out of it are a data race on the
// same bytes and the detector does not need the bad interleaving to actually occur. The **lost write**
// needs that interleaving — resolve the old image, relocation completes, write lands in the abandoned
// array — and after this test's start synchronisation was fixed it stopped appearing within six runs.
//
// Both are kept, and which is which is stated rather than left for someone to discover. Tuning the loop
// until a probabilistic detector looked deterministic was the alternative and is refused: a verdict that
// depends on luck should be labelled, not disguised. `make race` runs this package, so the channel that
// discriminates is the one CI exercises.
func TestARetainedCallersWritesSurviveARelocatingGrow(t *testing.T) {
	withoutReservation(t)

	var (
		mu       sync.Mutex
		retained *Caller
	)
	grab := make(chan struct{})
	in := hostLink(t, `(module
	  (import "h" "grab" (func $grab))
	  (memory 1)
	  (func (export "grab") (call $grab))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`,
		binary.Features{}, hostImports(map[string]Extern{
			// Captures its caller and **returns**, so the agent leaves the world count while the caller
			// stays usable. A parked host call would be the other test's sibling and would make the grow
			// refuse instead of relocate.
			"grab": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				mu.Lock()
				retained = c
				mu.Unlock()
				close(grab)
				return nil, nil
			}),
		}))

	if len(in.mems) != 1 || in.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(in.mems))
	}
	mem := in.mems[0]
	if held := mem.img.Load().bytes; cap(held) != len(held) {
		t.Fatalf("the allocator gave a %d-byte memory %d bytes of capacity, so the grow below reslices "+
			"instead of relocating and this test asserts nothing about the arm it names. Rebuild the "+
			"fixture so the relocating arm is reached — do not delete the test", len(held), cap(held))
	}

	if _, err := in.Invoke("grab"); err != nil {
		t.Fatalf("grab: %v", err)
	}
	<-grab
	mu.Lock()
	c := retained
	mu.Unlock()
	if c == nil {
		t.Fatal("the host function did not hand back a Caller")
	}

	var (
		stop      atomic.Bool
		writes    atomic.Int64
		mismatch  atomic.Int64
		writeErrs atomic.Int64
		readErrs  atomic.Int64
	)
	// **The writer signals its first round-trip, and the grows wait for it.**
	//
	// Without this the eight grows below could finish before the writer goroutine was ever scheduled,
	// and `stop` would be set before it ran at all — which is exactly what happened on CI's arm64 runner
	// while this passed locally. The write-count floor caught it ("the writer completed no iterations"),
	// so the test failed rather than reporting a green over an overlap that never occurred. Fixing the
	// synchronisation is the repair; the floor is why it was a failure and not a false pass.
	started := make(chan struct{})
	var startOnce sync.Once

	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := uint32(1); !stop.Load(); v++ {
			if err := c.Write(0, le32(v)); err != nil {
				writeErrs.Add(1)
				return
			}
			got, err := c.Read(0, 4)
			if err != nil {
				readErrs.Add(1)
				return
			}
			if uint32(got[0])|uint32(got[1])<<8|uint32(got[2])<<16|uint32(got[3])<<24 != v {
				// The lost write: this agent cannot read its own store back, which no memory model
				// permits and which is the outcome ADR 0073 exists to prevent.
				mismatch.Add(1)
			}
			writes.Add(1)
			startOnce.Do(func() { close(started) })
		}
	}()

	// Wait for the writer to be live before growing, so the relocation overlaps a write in progress
	// rather than racing an unscheduled goroutine.
	select {
	case <-started:
	case <-done:
		// The writer exited before completing a round-trip; its error counters below say why, and the
		// floors turn that into a failure rather than a vacuous pass.
	}

	// Grow while the retained caller is writing. Several times, because the relocation is the moment the
	// hazard exists and one attempt is one chance to overlap.
	var relocations int
	for i := range 8 {
		out, err := in.Invoke("up")
		if err != nil {
			stop.Store(true)
			<-done
			t.Fatalf("grow %d: %v", i, err)
		}
		if out[0].Int32() < 0 {
			stop.Store(true)
			<-done
			t.Fatalf("grow %d was refused (-1). A retained Caller is in no world count, so it must not "+
				"block the relocating arm — if it now does, this test's premise is gone and the "+
				"exclusion it checks has moved", i)
		}
		relocations++
	}
	stop.Store(true)
	<-done

	// Floors first: a test that wrote nothing, or never grew, proves nothing.
	if writes.Load() == 0 {
		t.Fatal("the writer completed no iterations; this test measured nothing")
	}
	if relocations == 0 {
		t.Fatal("no grow succeeded; the relocating arm was never taken")
	}
	if writeErrs.Load() != 0 || readErrs.Load() != 0 {
		t.Fatalf("the retained caller's accessors failed (%d write, %d read errors) — a retained Caller's "+
			"accessors must keep working across a grow; see Caller's doc",
			writeErrs.Load(), readErrs.Load())
	}
	if n := mismatch.Load(); n != 0 {
		t.Fatalf("%d of %d writes through a retained Caller were lost across %d relocating grow(s). "+
			"The agent could not read its own store back: the write landed in the image the relocation "+
			"abandoned. This is ADR 0073 decision 6's hazard, and growMu's read lock is what excludes it",
			n, writes.Load(), relocations)
	}
	t.Logf("RETAINED-CALLER %d write/readback round-trips across %d relocating grow(s), 0 lost",
		writes.Load(), relocations)
}
