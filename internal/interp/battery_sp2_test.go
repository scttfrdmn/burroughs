// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSP2AParkedAgentTouchesNoGuestMemoryDuringTheStop is the litmus battery case
// `sp2-a-parked-agent-touches-no-guest-memory-during-the-stop` (contract §3 SP-2): a thread blocked in a
// host call counts as at a safepoint, and it cannot touch guest memory until it re-enters through a
// boundary that observes the stop.
//
// N agents park in a blocking host call whose RETURN PATH writes a known naturally-aligned word (the
// host-side write to guest memory ADR 0054 does not reach — SP-2 is a protocol claim, not a weak-memory
// reorder, arbiter NEITHER, observable by value on any architecture). The harness holds the call, takes the
// stop, then RELEASES the call while the stop is still held, and samples each word across the held window:
// SP-2 forbids any of them changing while the stop is held. Only after Resume do the writes land. The
// re-entry gate under test is `leaveBlocked`'s `poll()` — whose own comment names SP-2's second half; the
// control (documented in the PR, run by removing that poll) lands a write during the stop, the defect
// signature. The expected pass is near-certain because the mechanism was written against the clause; the
// control carries the evidentiary weight, so the sampling window is opened only AFTER the stop returns and
// closed only BEFORE Resume, so a word that changes could only be a write that landed during the held stop
// — never the harness racing its own release.
func TestSP2AParkedAgentTouchesNoGuestMemoryDuringTheStop(t *testing.T) {
	const (
		n         = 4
		base      = int32(128)
		stride    = int32(8)           // each word 4-aligned and distinct
		writeVal  = uint32(0x51150001) // non-zero and biased: 0 means "no write landed"
		deadline  = 10 * time.Second
		spawnWait = 30 * time.Second
	)

	// A shared memory both the guest agents and the host-side return-path write use.
	provider := hostLink(t, `(module (memory (export "mem") 1 1 shared))`, bin.Features{Threads: true}, nil)
	closeAtEnd(t, provider)
	mem, ok := provider.Export("mem")
	if !ok || mem.Kind != bin.ExternMemory {
		t.Fatal("provider exports no shared memory")
	}
	readWord := func(addr int32) uint32 {
		b, err := mem.mem.read(uint64(uint32(addr)), 0, 4)
		if err != nil {
			t.Fatalf("reading word at %d: %v", addr, err)
		}
		return binary.LittleEndian.Uint32(b)
	}

	release := make(chan struct{})
	var entered atomic.Int32
	parked := make(chan struct{}, n)
	wrote := make(chan struct{}, n) // a return-path write signals here, so the post-Resume read has a
	//                                happens-before to it and does not race the concurrent c.Write (-race)
	// The blocking host call: park in CanonCaller.Blocking; on the RETURN PATH, write the word. `Blocking`
	// is leaveGuest→enterBlocked→fn→enterGuest→leaveBlocked, so the write below runs only after leaveBlocked
	// — the re-entry boundary SP-2 turns on.
	lower := CanonLowerExtern(
		ft([]bin.ValType{bin.I32}, nil),
		func(c *CanonCaller, args []Value) ([]Value, error) {
			addr := uint32(args[0].Bits)
			berr := c.Blocking(func() error {
				entered.Add(1)
				parked <- struct{}{}
				select {
				case <-release:
					return nil
				case <-c.Context().Done():
					return c.Context().Err()
				}
			})
			if berr != nil {
				return nil, berr
			}
			werr := c.Write(uint64(addr), le32(writeVal)) // the return-path write to guest memory
			wrote <- struct{}{}                           // establishes happens-before for the post-Resume read
			return nil, werr
		},
		CanonOptions{Memory: &mem},
	)

	in := hostLink(t, `(module
		(import "h" "mem" (memory 1 1 shared))
		(import "h" "bw" (func $bw (param i32)))
		(func (export "run") (param $a i32) (call $bw (local.get $a))))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"mem": mem, "bw": lower}))
	closeAtEnd(t, in)

	entry := exportedFuncIndex(t, in, "run")
	addrs := make([]int32, n)
	for i := range addrs {
		addrs[i] = base + int32(i)*stride
	}
	// Pre-stop value is 0 (fresh shared memory); assert it, so a later 0 means "no write" and not "unread".
	for _, a := range addrs {
		if v := readWord(a); v != 0 {
			t.Fatalf("word at %d = %#x before any spawn, want 0 — the floor's pre-stop value", a, v)
		}
	}

	// Spawn N agents off a goroutine (bounded — a pool that blocks its spawner would wedge; #608's rule).
	type spawnFail struct {
		i   int
		err error
	}
	var returned atomic.Int32
	spawned := make(chan struct{})
	failed := make(chan spawnFail, 1)
	go func() {
		for i, a := range addrs {
			if _, err := in.Spawn(entry, a, 0); err != nil {
				failed <- spawnFail{i, err}
				return
			}
			returned.Add(1)
		}
		close(spawned)
	}()
	select {
	case <-spawned:
	case f := <-failed:
		t.Fatalf("Spawn of agent %d of %d: %v — premise, not assertion: nothing measured", f.i+1, n, f.err)
	case <-time.After(spawnWait):
		t.Fatalf("only %d of %d Spawn calls returned within %v", returned.Load(), n, spawnWait)
	}

	// Floor: every one of the N agents must be parked in the blocking call before the stop request.
	for range n {
		select {
		case <-parked:
		case <-time.After(spawnWait):
			t.Fatalf("only %d of %d agents parked in the blocking call within %v", entered.Load(), n, spawnWait)
		}
	}

	// Stop the world. A parked agent counts as at a safepoint (blocked == the SP-2 arrival half), so Stop
	// must return within the deadline with all N reached.
	if err := in.Stop(deadline); err != nil {
		t.Fatalf("Stop with %d agents parked in a blocking call: %v — SP-2's arrival half (a parked agent "+
			"counts as at a safepoint) failed", n, err)
	}

	// The stop is now held. Release the calls — WHILE stopped. Each agent leaves the block and re-enters
	// through leaveBlocked, whose poll must park it until Resume. Its return-path write must NOT land yet.
	close(release)

	// Sample every word across the held window: SP-2 forbids any changing while the stop is held. The
	// window is opened after Stop returned and closed before Resume, so a non-zero read here could only be
	// a write that landed during the held stop — the defect the control produces.
	const samples = 200
	for range samples {
		for _, a := range addrs {
			if v := readWord(a); v != 0 {
				t.Fatalf("word at %d = %#x while the stop is held — a parked agent's return-path write "+
					"landed during the stop; SP-2's re-entry gate (leaveBlocked's poll) did not hold it", a, v)
			}
		}
		time.Sleep(100 * time.Microsecond)
	}

	// Lift the stop. Now the re-entry gate opens and every write lands. Wait for all N writes to complete
	// (the `wrote` receive is the happens-before), THEN read — so the assertion does not race the writes.
	in.Resume()
	for range n {
		select {
		case <-wrote:
		case <-time.After(deadline):
			t.Fatalf("only %d of %d writes completed after Resume — the re-entry gate did not open", entered.Load(), n)
		}
	}
	for _, a := range addrs {
		if v := readWord(a); v != writeVal {
			t.Errorf("word at %d = %#x after Resume, want %#x — the write did not land once the stop was lifted", a, v, writeVal)
		}
	}
}
