// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// # [Decision 0076][0076]'s five arms, and why they are one benchmark that reports no ns/op
//
// Contract §8 M-1 asks for growth *"amortized O(pages touched)"*, and 0076's claim to have delivered it
// rests on five numbers that were registered in [#672](https://github.com/scttfrdmn/burroughs/issues/672)
// before any of them existed. This is that pre-registration executed, one arm per printed table, with the
// bars restated in the headers so a reader comparing them does not have to hold the ADR open:
//
//   - **Arm A — reservation cost**, bar worst-of-five under 1 ms at every rung including 65535 pages. The
//     bar is ADR 0051's, taken over rather than re-argued, because the whole reason this decision exists is
//     that 0051's mechanism missed it by three orders of magnitude.
//   - **Arm B — one-page growth at increasing current size**, bar flat within 2× across the rungs. This is
//     the arm that decides whether the sentence "amortized O(pages touched)" may be written down at all: if
//     a one-page grow gets slower as the memory gets bigger, the win narrows to allocation time and the
//     claim narrows with it.
//   - **Arm C — RSS with only the minimum touched**, bar under 1 MiB per 4 GiB reserved. Rule 2 reserves an
//     address-type ceiling for a memory with no declared max, and that rule is only sound if reserving is
//     not committing.
//   - **Arm D — mark time with ten reservations live**, expected flat, because a mapping is off-heap and a
//     `[]byte` is pointer-free even when it is not. ADR 0075's arm B measured a **10×** rise on the table
//     twin, whose slot holds three pointers; if this arm rises too, the analysis that let memories go first
//     is wrong.
//   - **Arm E — the control**, `make([]byte, n, reserve)` at the same rungs. It re-takes 0051's column *on
//     this host* rather than comparing the mapping against a remembered number, which is the only form in
//     which the two are the same measurement.
//
// **Worst is the column that decides, so the statistic is not a mean and this is not `benchstat` work.**
// *Match the statistic on both sides*: the bars are worst cases because an instantiation pays whichever
// span it lands on, and averaging that spread away deletes the signal. Grave #612's split says the same
// from the other end — `bench` is one arm, `ab` is the comparison, and this is neither.
//
// # The two instrument guards, and which one is asserted
//
// #672 registered both. They are not symmetric, and the reason is worth stating rather than discovering:
//
//  1. **Assert the reservation happened** — from the mapped capacity and the counter, not from the absence
//     of an error. This one is a `b.Fatalf`. A run whose mapping path had quietly fallen back to `make`
//     would print five plausible tables and a reader would take them for M-1's numbers.
//  2. **Assert the arms differ** — and here the honest form is *printed, not asserted*. The obvious
//     assertion is that arm E's RSS tracks its reservation while arm A's does not, and it would be a wrong
//     assertion: a `make` that lands on a **fresh** span skips its clear, which is exactly 0051's 4.288 ms
//     best-case column, and its RSS would then look like the mapping's. Asserting it would make this
//     harness fail for the reason 0051 was right about. So arm E prints its own RSS delta beside arm A's
//     and the difference is the reader's to draw, while guard 1 carries the weight — it distinguishes the
//     arms structurally, which is what guard 2 was reaching for through a proxy.
//
// # Two skip guards, because this harness is dangerous to run by accident
//
// `make bench` runs `-bench . -count=10` over `./internal/interp/...` at the default 1 s benchtime, and a
// `b.N`-ignoring body under that would either be re-run ten times at 4 GiB or be looped up by the framework
// hunting for a duration it will never reach. So it declines unless `BURROUGHS_MEM_LADDER` is set, and
// declines unless `b.N == 1`, naming `-benchtime=1x`. Both skips say they are not passes.
//
// # Provenance is the operator's, and the recipe is in the skip message
//
// Nothing that gets measured runs outside the queue: `janus.local`, group `measured`, through
// `scripts/labrun`. Arm C needs `/proc/self/status`, so `janus` is also the only host in the fleet that can
// take it — a figure with no host is not a result, and this one additionally has no meaning without a
// Linux one.
//
// [0076]: ../../docs/decisions/0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md
func BenchmarkMemoryReservationLadder(b *testing.B) {
	if os.Getenv("BURROUGHS_MEM_LADDER") == "" {
		b.Skip("skipped, and this is not a pass: decision 0076's reservation ladder reserves up to 4 GiB " +
			"of address space per rung and its result is five printed tables rather than an ns/op. " +
			"Run it on the queue:\n" +
			"  scripts/labrun janus.local measured -- env BURROUGHS_MEM_LADDER=1 go test " +
			"./internal/interp/ -run XXX -bench MemoryReservationLadder -benchtime=1x -v")
	}
	if b.N != 1 {
		b.Skip("skipped: this harness measures its own repetitions, so the framework must not iterate it. " +
			"Pass -benchtime=1x.")
	}
	b.ReportMetric(0, "ns/op")

	// The pre-registered rungs. 65535 is the top because it is `maxPages32` — rule 2's reservation for a
	// memory with no declared max, and the rung 0051's bar was missed at.
	rungs := []uint64{1, 16, 256, 4096, 16384, 32768, 65535}
	const reps = 5

	fmt.Printf("\n%s\n", strings.Repeat("=", 78))
	fmt.Printf("decision 0076 reservation ladder — host %s, %d reps per rung\n", hostLabel(), reps)
	if rss, ok := residentBytes(); ok {
		fmt.Printf("RSS at start: %d bytes\n", rss)
	} else {
		fmt.Printf("RSS: unavailable on this host, so arm C reports nothing (it needs /proc/self/status)\n")
	}

	armA(b, rungs, reps)
	armB(b, reps)
	armC(b)
	armD(b, rungs, reps)
	armE(b, rungs, reps)
}

// armA times `newMemory` at each rung against ADR 0051's 1 ms bar, and is where guard 1 lives.
func armA(b *testing.B, rungs []uint64, reps int) {
	b.Helper()
	fmt.Printf("\narm A — newMemory at the rung, %d reps, bar: worst under 1ms\n", reps)
	fmt.Printf("| rung (pages) | reserved bytes | best | worst | clears 1ms |\n| --- | --- | --- | --- | --- |\n")

	for _, rung := range rungs {
		lim := binary.Limits{Min: 1, Max: rung, HasMax: true}
		if rung == 1 {
			// A max equal to the minimum is rule 3 and reserves nothing, which would make this row
			// a measurement of the fallback wearing arm A's heading. Two pages of headroom keeps the
			// row on the mapping path without moving what it measures.
			lim.Max = 2
		}
		var best, worst time.Duration
		// Held to the end of the rung: a reservation the collector has already reclaimed — and whose
		// cleanup has already unmapped it — is not the state an instantiation leaves behind.
		live := make([]*memory, 0, reps)
		for i := range reps {
			before := reservationUnavailable.Load()
			start := time.Now()
			mem, err := newMemory(binary.Memory{Limits: lim})
			d := time.Since(start)
			if err != nil {
				b.Fatalf("newMemory at rung %d: %v", rung, err)
			}

			// **Guard 1, at every rep rather than once.** A reservation that failed at the top rung
			// only — the interesting case, since it is where the address space runs out — would
			// otherwise be averaged into a plausible row.
			if got := reservationUnavailable.Load() - before; got != 0 {
				b.Fatalf("rung %d rep %d took the allocator's fallback (reservationUnavailable "+
					"moved by %d), so every number this harness prints below is about "+
					"`make` and not about a mapping", rung, i, got)
			}
			if img := mem.img.Load().bytes; uint64(cap(img)) != lim.Max*pageSize {
				b.Fatalf("rung %d rep %d has %d bytes of capacity, want the reservation's %d: "+
					"the counter says a mapping was made and the capacity says it was not, "+
					"which is a worse state than either", rung, i, cap(img), lim.Max*pageSize)
			}

			live = append(live, mem)
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
		fmt.Printf("| %d | %d | %v | %v | %s |\n", rung, lim.Max*pageSize, best, worst, clears)
		runtime.KeepAlive(live)
	}
}

// armB times a single-page `grow` at increasing current size, which is the arm M-1's own sentence rests on.
//
// **Each rep times a batch of grows and divides, and the first local run is why.** Timing one reslice
// against `time.Since` measured `0s` at six of seven rungs and a first-row `9.75µs` at the seventh — a
// clock-resolution floor and one warm-up outlier, from which the flatness ratio came out as `9750x` with
// zero in the denominator. That figure said nothing about growth and everything about the instrument: *an
// unasserted distance is the vacuum*, and a bar of "flat within 2x" cannot be read off a column that is
// mostly zero. Batching lifts the per-grow cost above the clock's granularity, which is the only way the
// pre-registered bar has an observable subject at all.
func armB(b *testing.B, reps int) {
	b.Helper()
	// Distinct rungs from the other arms: each row grows `batch*reps` pages past its rung, so the top one
	// stays under `maxPages32` rather than hitting the declared max mid-measurement and timing a refusal.
	rungs := []uint64{1, 16, 256, 4096, 16384, 32768, 65000}
	const batch = 64

	fmt.Printf("\narm B — one-page grow at a current size of N pages, %d reps of %d grows, bar: flat within 2x\n",
		reps, batch)
	fmt.Printf("| current pages | best per grow | worst per grow |\n| --- | --- | --- |\n")
	// The column is `rung+1` and not `rung`, because the untimed warm-up below has already grown once
	// by the time the batch starts. Printing the rung would be a one-page lie in the independent
	// variable, which is a small error in a table whose whole subject is whether size matters.

	var minWorst, maxWorst, minBest, maxBest time.Duration
	var worstAt uint64
	for i, rung := range rungs {
		mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: maxPages32, HasMax: true}})
		if err != nil {
			b.Fatalf("newMemory: %v", err)
		}
		// **The setup grow is untimed, is one call, and happens at every rung including the first.**
		// It is what makes the rows comparable rather than just cheap: the batched run above still
		// showed a 271ns worst at the bottom rung against 16ns everywhere else, because that was the
		// one row whose `rung > 1` guard skipped the setup and so paid the first-ever grow on that
		// memory inside a timed batch. An unmatched row is not a data point about size — *assert the
		// arms differ* read from the other end, where the arms were differing for a reason that was
		// not the independent variable. So every row grows once untimed and every timed batch starts
		// one page above its rung.
		if got := mem.grow(rung, nil); got != 1 {
			b.Fatalf("setup grow past %d pages returned %d, want 1", rung, got)
		}
		var best, worst time.Duration
		for j := range reps {
			start := time.Now()
			for range batch {
				if got := mem.grow(1, nil); got < 0 {
					b.Fatalf("grow was refused (%d) at rung %d, so this row measures a "+
						"refusal rather than a growth", got, rung)
				}
			}
			per := time.Since(start) / batch
			if j == 0 || per < best {
				best = per
			}
			if per > worst {
				worst = per
			}
		}
		fmt.Printf("| %d | %v | %v |\n", rung+1, best, worst)
		if i == 0 || worst < minWorst {
			minWorst = worst
		}
		if i == 0 || worst > maxWorst {
			maxWorst = worst
			worstAt = rung + 1
		}
		if i == 0 || best < minBest {
			minBest = best
		}
		if i == 0 || best > maxBest {
			maxBest = best
		}
		runtime.KeepAlive(mem)
	}
	// Worst against worst and best against best, because *match the statistic on both sides* — and both,
	// because at this magnitude they can disagree about whether the bar was cleared and the disagreement is
	// the finding. Printed rather than asserted for guard 2's reason: a `b.Fatalf` here would leave the
	// harness unable to *report* the number that falsifies the claim, which is the one number worth
	// reporting.
	if minWorst <= 0 || minBest <= 0 {
		fmt.Printf("\nflatness: not measurable — a column reached zero even batched, so this run does "+
			"not adjudicate the 2x bar (best floor %v, worst floor %v)\n", minBest, minWorst)
		return
	}
	fmt.Printf("\nflatness: best per grow %v to %v — %.2fx; worst per grow %v to %v — %.2fx (bar: under 2x)\n",
		minBest, maxBest, float64(maxBest)/float64(minBest),
		minWorst, maxWorst, float64(maxWorst)/float64(minWorst))

	// **Where the maximum sits is what says whether the worst column is about size at all**, and it is
	// printed because *compare the floor to the bar*: a per-grow cost in the tens of nanoseconds against a
	// scheduler that can steal hundreds means the worst column's spread may be wider than the 2x bar for
	// reasons that have nothing to do with the independent variable. The shape distinguishes them. A worst
	// column whose maximum is at the **largest** rung is the shape that falsifies "amortized O(pages
	// touched)"; a maximum in the middle of the ladder is noise, and reading it as a verdict would retire a
	// correct mechanism on the strength of one descheduled batch.
	fmt.Printf("worst column's maximum sits at %d pages, of a ladder topping out at %d — "+
		"a maximum at the top is the M-1-falsifying shape, a maximum in the middle is noise\n",
		worstAt, rungs[len(rungs)-1]+1)
}

// armC reads RSS around a top-rung reservation with only the minimum touched, which is rule 2's premise.
func armC(b *testing.B) {
	b.Helper()
	fmt.Printf("\narm C — RSS delta for a %d-page reservation, minimum touched, bar: under 1MiB per 4GiB\n",
		maxPages32)

	// The availability probe discards its value on purpose: the number that matters is taken after the
	// collection below, so reading one here and overwriting it would be an ineffectual assignment — and
	// `ineffassign` said so, which is the linter noticing that this function asks the question twice.
	if _, ok := residentBytes(); !ok {
		fmt.Printf("| unavailable | — | — |\n\nThis host has no /proc/self/status, so **arm C was not " +
			"taken**. That is a gap in the run and not a null result: rule 2 reserves an address-type " +
			"ceiling and its soundness is exactly this number.\n")
		return
	}
	// One collection first, so the delta is not the previous arm's garbage being reclaimed under it.
	runtime.GC()
	before, _ := residentBytes()

	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: maxPages32, HasMax: true}})
	if err != nil {
		b.Fatalf("newMemory: %v", err)
	}
	mem.view()[0] = 1 // the minimum, touched — one page, so the reservation is committed nowhere else

	after, _ := residentBytes()
	delta := int64(after) - int64(before)
	reserved := uint64(maxPages32) * pageSize
	bar := int64(reserved / (4 << 30) * (1 << 20))
	if bar < 1<<20 {
		bar = 1 << 20
	}
	verdict := "yes"
	if delta > bar {
		verdict = "**no**"
	}
	fmt.Printf("| reserved bytes | RSS delta | bar | clears |\n| --- | --- | --- | --- |\n")
	fmt.Printf("| %d | %d | %d | %s |\n", reserved, delta, bar, verdict)
	runtime.KeepAlive(mem)
}

// armD times `runtime.GC()` with ten reservations live per rung — ADR 0075 arm B's question, asked of the
// twin that is supposed to answer it differently.
func armD(b *testing.B, rungs []uint64, reps int) {
	b.Helper()
	const mems = 10
	fmt.Printf("\narm D — runtime.GC() with %d reservations live, %d reps, expected flat\n", mems, reps)
	fmt.Printf("| rung (pages) | reserved bytes live | best | worst |\n| --- | --- | --- | --- |\n")

	for _, rung := range rungs {
		lim := binary.Limits{Min: 1, Max: max(rung, 2), HasMax: true}
		live := make([]*memory, 0, mems)
		for range mems {
			mem, err := newMemory(binary.Memory{Limits: lim})
			if err != nil {
				b.Fatalf("newMemory at rung %d: %v", rung, err)
			}
			live = append(live, mem)
		}
		// One untimed collection, so the timed ones measure marking this heap rather than reclaiming
		// the previous rung's.
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
		fmt.Printf("| %d | %d | %v | %v |\n", rung, mems*lim.Max*pageSize, best, worst)
		runtime.KeepAlive(live)
	}
}

// armE is the control: 0051's `make([]byte, n, reserve)`, at the same rungs, on this host.
//
// It calls `make` directly rather than driving `allocate` with the seam refusing, and the difference
// matters: the fallback caps its reservation at `sharedReservePages`, so `allocate` cannot be made to take
// 0051's measurement at all. What this arm re-takes is the *expression* 0051 measured, which is the only
// thing that makes the two columns comparable.
func armE(b *testing.B, rungs []uint64, reps int) {
	b.Helper()
	fmt.Printf("\narm E — the control: make([]byte, pageSize, rung*pageSize) at the same rungs, %d reps\n", reps)
	fmt.Printf("| rung (pages) | reserved bytes | best | worst | RSS delta |\n| --- | --- | --- | --- | --- |\n")

	for _, rung := range rungs {
		reserve := int(max(rung, 2) * pageSize)
		var best, worst time.Duration
		live := make([][]byte, 0, reps)
		runtime.GC()
		before, haveRSS := residentBytes()
		for i := range reps {
			start := time.Now()
			bs := make([]byte, pageSize, reserve)
			d := time.Since(start)
			bs[0] = 1 // touched exactly as far as arm A's memory is, so the RSS columns are comparable
			live = append(live, bs)
			if i == 0 || d < best {
				best = d
			}
			if d > worst {
				worst = d
			}
		}
		rss := "unavailable"
		if haveRSS {
			if after, ok := residentBytes(); ok {
				rss = strconv.FormatInt(int64(after)-int64(before), 10)
			}
		}
		fmt.Printf("| %d | %d | %v | %v | %s |\n", rung, reserve, best, worst, rss)
		runtime.KeepAlive(live)
	}
	fmt.Printf("\n%s\n", strings.Repeat("=", 78))
}

// residentBytes reads this process's resident set size, and reports `false` where it cannot.
//
// **`/proc/self/status` and no fallback, because a wrong RSS is worse than none here.** Arm C's whole job
// is to say that reserving is not committing, so a number this function guessed at would be the one figure
// in the run that could manufacture the conclusion. `darwin` has no `/proc` and no pure-Go way to ask, and
// no cgo is available to it, so on the dev box this returns false and arm C says it was not taken.
func residentBytes() (uint64, bool) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "VmRSS:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 || fields[1] != "kB" {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb << 10, true
	}
	return 0, false
}

// hostLabel names the runner in the printed table, so the figures carry their provenance into the ADR
// rather than relying on the operator to remember which box took them.
func hostLabel() string {
	h, err := os.Hostname()
	if err != nil {
		h = "unknown"
	}
	return fmt.Sprintf("%s %s/%s", h, runtime.GOOS, runtime.GOARCH)
}
