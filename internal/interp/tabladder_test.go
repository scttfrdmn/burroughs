// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// # [Decision 0075][0075]'s reservation ladder, and why it is a benchmark that reports no ns/op
//
// The ceiling in `internal/interp/table.go:tableReserveSlots` limits which programs run — a table whose
// declared max exceeds it takes the relocating arm and can be refused — so the pre-registration derives it
// from a measurement instead of picking it. Two arms, both registered before any number existed:
//
//   - **Arm A — `newTable` at the rung**, against a bar of **worst of five under 1 ms**. The bar is ADR
//     0051's, taken over rather than re-argued.
//   - **Arm B — mark cost with reservations live.** Ten tables reserved at the rung, `runtime.GC()` timed.
//     This is the arm memory's bar cannot see: a memory's reservation is `[]byte` and never scanned, a
//     table's is `[]ref` — three pointers in every 40-byte slot — and is walked every mark cycle.
//
// **Worst is the column that decides, so the statistic is not a mean and this is not `benchstat` work.** A
// fresh arena span is handed out already zeroed while a recycled one is cleared first, and an instantiation
// pays whichever it lands on; averaging that spread away deletes the signal. *Match the statistic on both
// sides* — the bar is a worst case, so the measurement reports a worst case. Grave #612's split says the same
// from the other end: `bench` is one arm, `ab` is the comparison, and this is neither.
//
// # Two guards, because this harness is dangerous to run by accident
//
// `make bench` runs `-bench . -count=10` over `./internal/interp/...` at the default 1 s benchtime. A
// `b.N`-ignoring body under that would either be re-run ten times at 2^20 slots or be looped up by the
// framework hunting for a duration it will never reach. So:
//
//  1. It skips unless `BURROUGHS_TABLE_LADDER` is set. The skip is not a verdict and says so.
//  2. It skips unless `b.N == 1`, naming `-benchtime=1x`. The body measures its own five repetitions with
//     `time.Since` and reports **no** ns/op, because the framework's statistic is the wrong one.
//
// # Provenance is the operator's, and the recipe is in the skip message
//
// Nothing that gets measured runs outside the queue: `janus.local`, group `measured`, through
// `scripts/labrun`. The printed table goes into 0075 with the host, the group, the task id and the
// concurrent-task count beside it — a figure with no host is not a result.
//
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
func BenchmarkTableReservationLadder(b *testing.B) {
	if os.Getenv("BURROUGHS_TABLE_LADDER") == "" {
		b.Skip("skipped, and this is not a pass: decision 0075's reservation ladder allocates up to 42 MiB " +
			"per rung and its result is a printed table rather than an ns/op. Run it on the queue:\n" +
			"  scripts/labrun janus.local measured -- env BURROUGHS_TABLE_LADDER=1 go test " +
			"./internal/interp/ -run XXX -bench TableReservationLadder -benchtime=1x -v")
	}
	if b.N != 1 {
		b.Skip("skipped: this harness measures its own repetitions, so the framework must not iterate it. " +
			"Pass -benchtime=1x.")
	}
	b.ReportMetric(0, "ns/op")

	// The rungs are the pre-registered ones: 320 (the corpus's largest reservable declaration), 1024, and
	// then powers of four and two up to 2^20 — 12.8 KiB to 42 MiB of `ref`.
	rungs := []uint64{320, 1024, 4096, 16384, 65536, 1 << 18, 1 << 19, 1 << 20}

	// One module, decoded and instantiated once, so neither arm times a decode. The declared max is the
	// i32 element cap, which makes `reserve` exactly `tableReserveSlots` at every rung — the ladder moves
	// the engine's ceiling rather than the module's declaration, because a per-rung module would also
	// move `lim.Max` and no longer measure the reservation path a real ceiling takes.
	in, tbl := ladderFixture(b)

	// Restored because `tableReserveSlots` is a package var and every other test in this binary reads it.
	defer func(orig uint64) { tableReserveSlots = orig }(tableReserveSlots)

	const reps = 5
	fmt.Printf("\ndecision 0075 arm A — newTable at the rung, %d reps, bar: worst under 1ms\n", reps)
	fmt.Printf("| rung | bytes | best | worst | clears 1ms |\n| --- | --- | --- | --- | --- |\n")
	for _, rung := range rungs {
		tableReserveSlots = rung
		var best, worst time.Duration
		// Held to the end of the rung so the allocations are live while the next one is taken: a
		// reservation the collector has already reclaimed is not the state an instantiation leaves behind.
		live := make([]*table, 0, reps)
		for i := range reps {
			start := time.Now()
			tab, err := in.newTable(tbl)
			d := time.Since(start)
			if err != nil {
				b.Fatalf("newTable at rung %d: %v", rung, err)
			}
			live = append(live, tab)
			if i == 0 || d < best {
				best = d
			}
			if d > worst {
				worst = d
			}
		}
		clears := "yes"
		if worst >= time.Millisecond {
			clears = "**no**"
		}
		fmt.Printf("| %d | %d | %v | %v | %s |\n", rung, rung*refSize, best, worst, clears)
		runtime.KeepAlive(live)
	}

	const tables = 10
	fmt.Printf("\ndecision 0075 arm B — runtime.GC() with %d reservations live, %d reps\n", tables, reps)
	fmt.Printf("| rung | bytes live | best | worst |\n| --- | --- | --- | --- |\n")
	for _, rung := range rungs {
		tableReserveSlots = rung
		live := make([]*table, 0, tables)
		for range tables {
			tab, err := in.newTable(tbl)
			if err != nil {
				b.Fatalf("newTable at rung %d: %v", rung, err)
			}
			live = append(live, tab)
		}
		// One untimed collection first, so the timed ones measure marking this heap rather than
		// reclaiming the previous rung's.
		runtime.GC()
		var best, worst time.Duration
		for i := range reps {
			start := time.Now()
			runtime.GC()
			d := time.Since(start)
			if i == 0 || d < best {
				best = d
			}
			if d > worst {
				worst = d
			}
		}
		fmt.Printf("| %d | %d | %v | %v |\n", rung, uint64(tables)*rung*refSize, best, worst)
		runtime.KeepAlive(live)
	}
}

// ladderFixture builds the one instance both arms allocate through, and returns the `binary.Table` whose
// constructor is the subject. `*testing.B` rather than `*testing.T`, which is why it does not reuse
// `instantiate1`.
func ladderFixture(b *testing.B) (*Instance, binary.Table) {
	b.Helper()
	img, err := text.EncodeModule([]byte(`(module (table 1 4294967295 funcref))`))
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		b.Fatalf("instantiate: %v", trap)
	}
	if len(m.Tables) != 1 {
		b.Fatalf("the fixture declares %d tables, want 1: both arms time the one constructor", len(m.Tables))
	}
	return in, m.Tables[0]
}
