// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The one-load rule's witnesses (ADR 0104; the chair's second ruling of 2026-10-19, item 1).
//
// A bulk instruction loads its image once and holds it across its polls. That is decision 0058's and 0065's
// rule, and #1061 and #1070 keep it for the pieced instructions. It is safe only because a grow refuses to
// relocate while another agent of the instance could hold the old image (`soleAgentLocked`, ADR 0073). These
// witnesses are that dependency's own test:
//   - agent A is stopped part-way through one large fill, holding its view;
//   - A is resumed, and agent B, a second call into the same instance, grows the memory or table at once;
//   - **B's grow must be refused, and A's fill must complete over the whole region.**
//
// NEUTER (run): the `soleAgentLocked` check removed from both relocating grows. Ten of ten runs failed. In eight,
// B's grow relocated, A finished writing into the old array, and the live image's tail was never filled: a
// memory byte left 0, or table slots left null. In the other two, A's fill had finished before B's grow, and
// the refusal check caught the relocation instead.
//
// **Those two were the flaw, read as a catch.** A fill that finishes before B's grow leaves nothing holding the
// old image, so a relocation is then CORRECT, with or without the neuter. The witness reported it as the defect,
// and on 2026-10-26 it did so on main (ubuntu-arm64, 98dccb7) against an engine that was right. The premise is now
// read, not assumed (`growAgainstAHeldFill`). Re-run after the repair: under the neuter, 10 of 10 fail (memory 5
// and table 3 on the harm, table 2 on a relocation with A still inside its fill). With a 200ms sleep after
// `Resume`, so that A always finishes first, both arms end on their instrument-fault floor and never on the
// defect.
//
// The existing refusal tests (`TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage` and its table
// twin) park the sibling in a host call. These park it at a poll mid-instruction, which is the case the
// pieced instructions created.

// stopMidFill stops the instance until a stop lands part-way through the fill (`mixed`, read while parked),
// and returns true with the agent parked there. It returns false if the fill finished first (`done` fired),
// so the caller can rerun the scenario on a fresh instance.
//
// **Not a fixed number of tries.** The first draft gave up after 2000 stops, and once failed: the stops took
// about 2ms, and all of them fell before the fill began or after its ~5ms ended.
func stopMidFill(t *testing.T, in *Instance, done <-chan error, mixed func() bool) bool {
	t.Helper()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			return false
		default:
		}
		if err := in.Stop(30 * time.Second); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if mixed() {
			return true
		}
		in.Resume()
	}
}

// attempts is how many fresh scenarios a witness runs before calling a fill that never stopped mid-way, or a
// reading that never came out conclusive, an instrument fault.
const attempts = 20

// growAgainstAHeldFill resumes A and has B grow at once. It returns the grow's result and whether A was **still
// inside its fill when the grow returned**, and it waits for the fill to finish before returning.
//
// **The premise is read rather than assumed, and that is a repair (grave #1118).** The first version took "A is still inside its
// fill" from the order of two calls: `Resume`, then B's grow. Nothing makes that order hold. On CI's ubuntu-arm64
// leg (main at 98dccb7, 2026-10-26) A's remaining fill finished first; nothing then held the old image, the grow
// relocated, which is correct, and the witness reported the defect it exists to catch. A 200ms sleep between the
// two calls reproduced it locally in 5 of 5 runs, with the same message and the same clean fill.
//
// So a relocation is the defect only when A was observably still filling after the grow returned, which means it
// was filling throughout. A relocation after A had returned is inconclusive, and the scenario is rerun. A fill
// that finishes in the instant between the grow's return and this read is also called inconclusive: that is a
// missed defect at worst, never a false one.
func growAgainstAHeldFill(t *testing.T, in *Instance, done <-chan error) (grown int32, held bool) {
	t.Helper()
	in.Resume()
	res, err := in.Invoke("up") // agent B
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	held = true
	var ferr error
	select {
	case ferr = <-done:
		held = false
	default:
		ferr = <-done
	}
	if ferr != nil {
		t.Fatalf("fill: %v", ferr)
	}
	return res[0].Int32(), held
}

// TestAMemoryFillHeldAcrossAParkSurvivesASiblingGrow is the memory arm.
func TestAMemoryFillHeldAcrossAParkSurvivesASiblingGrow(t *testing.T) {
	// The relocating arm lives on decision 0076's fallback path; a reserved memory never relocates.
	withoutReservation(t)
	const pages = 1024 // 64 MiB, filled whole in 1024 bulkChunks
	src := `(module (memory ` + itoa(pages) + `)
		(func (export "fill") (memory.fill (i32.const 0) (i32.const 7) (i32.const ` + itoa(pages*65536) + `)))
		(func (export "up") (result i32) (memory.grow (i32.const 1))))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	size := pages * 65536
	for scenario := 0; ; scenario++ {
		if scenario == attempts {
			t.Fatalf("in %d scenarios every grow relocated after A's fill had returned, so none was a reading "+
				"of a grow against a held fill: instrument fault", attempts)
		}
		var in *Instance
		var done chan error
		for attempt := 0; ; attempt++ {
			if attempt == attempts {
				t.Fatalf("in %d fresh runs no stop landed part-way through the fill: instrument fault", attempts)
			}
			var trap *Trap // the concrete type: a nil *Trap in an `error` variable is a non-nil error
			in, trap = mustInst(t, m)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			if v := in.mems[0].view(); cap(v) != len(v) {
				t.Fatalf("the memory has %d bytes of capacity for a length of %d, so a grow would reslice "+
					"rather than relocate, and this test would assert nothing about the arm it names", cap(v), len(v))
			}
			done = make(chan error, 1)
			go func(in *Instance, done chan error) {
				_, ferr := in.Invoke("fill")
				done <- ferr
			}(in, done)
			if stopMidFill(t, in, done, func() bool { return stopWord(t, in, 0) != stopWord(t, in, size-4) }) {
				break
			}
			_ = in.Close()
		}

		grown, held := growAgainstAHeldFill(t, in, done)
		// The fill's own result first: that is the harm, and it is checked on every reading, conclusive or not.
		// The grow's -1 is checked after, as the refusal's channel.
		view := in.mems[0].view()
		for off := 0; off < size; off += bulkChunk {
			for _, b := range []int{off, off + bulkChunk - 1} { // each piece's first and last byte
				if view[b] != 7 {
					t.Fatalf("byte %d of the live memory is %d after the fill completed, want 7: the fill's "+
						"remaining pieces went to an image the memory no longer uses (the grow returned %d)", b,
						view[b], grown)
				}
			}
		}
		_ = in.Close()
		if grown == -1 {
			return
		}
		if held {
			t.Fatalf("memory.grow returned %d while another agent was still inside a fill holding the old "+
				"image; it must refuse to relocate (-1), because that agent's remaining writes would land in the "+
				"abandoned array (ADR 0073)", grown)
		}
		t.Logf("scenario %d: the grow relocated after A's fill had returned, which is correct; rerunning", scenario)
	}
}

// TestATableFillHeldAcrossAParkSurvivesASiblingGrow is the table arm: a no-max table, so its grow relocates.
func TestATableFillHeldAcrossAParkSurvivesASiblingGrow(t *testing.T) {
	const slots = 2 << 20
	src := `(module (table $t ` + itoa(slots) + ` funcref) (func $f) (elem declare func $f)
		(func (export "fill") (table.fill $t (i32.const 0) (ref.func $f) (i32.const ` + itoa(slots) + `)))
		(func (export "up") (result i32) (table.grow $t (ref.null func) (i32.const 1))))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for scenario := 0; ; scenario++ {
		if scenario == attempts {
			t.Fatalf("in %d scenarios every grow relocated after A's fill had returned, so none was a reading "+
				"of a grow against a held fill: instrument fault", attempts)
		}
		var in *Instance
		var done chan error
		for attempt := 0; ; attempt++ {
			if attempt == attempts {
				t.Fatalf("in %d fresh runs no stop landed part-way through the fill: instrument fault", attempts)
			}
			var trap *Trap
			in, trap = mustInst(t, m)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			if v := in.tables[0].view(); cap(v) != len(v) {
				t.Fatalf("the table has %d slots of capacity for a length of %d, so a grow would reslice rather "+
					"than relocate", cap(v), len(v))
			}
			done = make(chan error, 1)
			go func(in *Instance, done chan error) {
				_, ferr := in.Invoke("fill")
				done <- ferr
			}(in, done)
			if stopMidFill(t, in, done, func() bool {
				v := in.tables[0].view()
				return v[0].Null != v[slots-1].Null
			}) {
				break
			}
			_ = in.Close()
		}

		grown, held := growAgainstAHeldFill(t, in, done)
		view := in.tables[0].view()
		for k := 0; k < slots; k += elemChunk {
			if view[k].Null {
				t.Fatalf("slot %d of the live table is null after the fill completed: the fill's remaining "+
					"pieces went to an image the table no longer uses (the grow returned %d)", k, grown)
			}
		}
		_ = in.Close()
		if grown == -1 {
			return
		}
		if held {
			t.Fatalf("table.grow returned %d while another agent was still inside a fill holding the old "+
				"image; it must refuse to relocate (-1)", grown)
		}
		t.Logf("scenario %d: the grow relocated after A's fill had returned, which is correct; rerunning", scenario)
	}
}
