// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"encoding/binary"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSP6AResumedAgentSeesWritesMadeDuringTheStop is the litmus battery case
// `sp6-a-resumed-agent-sees-writes-made-during-the-stop` (contract §3 SP-6): writes performed while the
// world is stopped — by the stopping agent or the host between Stop completing and Resume being called —
// are visible to every resumed agent without further synchronization, across the whole shared address
// space, ordered before any post-Resume memory operation. This is the STW->mark/move->resume publication
// edge the threaded tier's GC directly depends on (recon #742, F2); SP-2 witnesses the inverse (no write
// DURING the stop), and together they bracket the stop window from both sides.
//
// SP-6 is a STRUCTURAL guarantee, witnessed with a stronger pair of evidence than H-1's. The positive
// (below) passes under `-race`: the stop-time host writes are ordered before the resumed guest loads, so the
// detector reports nothing on the located spread-word pairs. The SECOND witness makes the structural claim
// concrete — run, not argued: a control REMOVING the named acquire edge (Resume's `close(release)` /
// `<-release` channel pair) leaves the guarantee intact under `-race`, because the edge is carried
// redundantly by the atomic `stopReq` store→load a resuming agent must execute to un-park at all. So the
// registered `-race` control does NOT die — the acquire edge is inherent to how resume works, not a deletable
// line. This corrected SP-6's own drafting, which named the channel as the sole edge; safepoint.go's comment
// is corrected in the same change. The only falsifying instrument is a resume that learns of stop-lift WITHOUT
// synchronization — a different engine.
//
// Unlike H-1 (structural by the goroutine-per-agent execution model, witnessed by an adversary failing to
// starve), SP-6 is structural by the resume path's OWN synchronization, witnessed twice: the positive under
// `-race`, AND the named edge's removal leaving it intact — a second witness H-1 does not have.
//
// Shape (#742 tagging finding, condition 1): the clause is "the WHOLE shared address space", so the reader
// loads SPREAD words — low/mid/high across the page — not one location. That samples the address space; no
// finite test proves every address, so this is sampled coverage, stated as such, not full.
//
// Arbiter: `-race`, both arches. Run under -race per the case discipline (also its arbiter here).
func TestSP6AResumedAgentSeesWritesMadeDuringTheStop(t *testing.T) {
	const (
		goAddr   = int32(16)
		resBase  = int32(0x2000)
		deadline = 10 * time.Second
	)
	// Spread across the 64KiB page — low, mid, high — sampling the whole address space, not one word.
	type word struct {
		addr int32
		val  uint32
	}
	words := []word{
		{0x100, 0x5150_0001},
		{0x4000, 0x5150_0002},
		{0x8000, 0x5150_0003},
		{0xF000, 0x5150_0004},
	}

	done := make(chan struct{}, 1)
	doneFn := HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
		done <- struct{}{} // A finished its reads: a boundary crossing, so results are readable race-free after
		return nil, nil
	})
	// The reader spins on `go` (plain load, back-edge — Stop can park it here), then loads the four spread
	// words with straight-line guest typed loads and stores them to the result region, then signals done.
	in := hostLink(t, `(module
		(import "h" "done" (func $done))
		(memory (export "mem") 1 1 shared)
		(func (export "reader") (param $go i32)
			(loop $wait (br_if $wait (i32.eqz (i32.load (local.get $go)))))
			(i32.store (i32.const 0x2000) (i32.load (i32.const 0x100)))
			(i32.store (i32.const 0x2004) (i32.load (i32.const 0x4000)))
			(i32.store (i32.const 0x2008) (i32.load (i32.const 0x8000)))
			(i32.store (i32.const 0x200C) (i32.load (i32.const 0xF000)))
			(call $done)))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"done": doneFn}))
	closeAtEnd(t, in)

	mem, ok := in.Export("mem")
	if !ok || mem.Kind != bin.ExternMemory {
		t.Fatal("module exports no shared memory")
	}
	hostWrite := func(addr int32, v uint32) {
		if err := mem.mem.write(uint64(uint32(addr)), 0, le32(v)); err != nil {
			t.Fatalf("host write at %#x: %v", addr, err)
		}
	}
	readWord := func(addr int32) uint32 {
		b, err := mem.mem.read(uint64(uint32(addr)), 0, 4)
		if err != nil {
			t.Fatalf("read at %#x: %v", addr, err)
		}
		return binary.LittleEndian.Uint32(b)
	}

	entry := exportedFuncIndex(t, in, "reader")
	if _, err := in.Spawn(entry, goAddr, 0); err != nil { // arg0 = go word address; results are at 0x2000
		t.Fatalf("Spawn reader: %v", err)
	}

	// Stop the world. A is spinning on `go`; Stop parks it at a back-edge. Stop returning is the floor: A is
	// parked before any stop-time write.
	if err := in.Stop(deadline); err != nil {
		t.Fatalf("Stop while the reader spins: %v", err)
	}

	// Stop-time writes: the host publishes the four spread words, then sets `go`, all while the world is held
	// — the GC's moves, modeled host-side. Ordered before the resumed reads only by Resume's acquire edge.
	for _, w := range words {
		hostWrite(w.addr, w.val)
	}
	hostWrite(goAddr, 1)

	in.Resume()

	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatalf("the resumed reader did not finish within %v", deadline)
	}

	// Race-free (after the done boundary crossing): each spread word the reader stored must be the published
	// value — the resumed agent saw the stop-time writes across the address space.
	for i, w := range words {
		if got := readWord(resBase + int32(i)*4); got != w.val {
			t.Errorf("result[%d] (addr %#x) = %#x, want %#x — the resumed agent did not see the stop-time write", i, w.addr, got, w.val)
		}
	}
}
