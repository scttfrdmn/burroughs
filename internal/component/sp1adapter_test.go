// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// adapterCopyArgLen is the string `run-sync` carries in SP-1's adapter-copy readings: 16 MiB, which each call
// copies four times in host code (testdata/adapter-copy.wat).
const adapterCopyArgLen = 16 << 20

// instantiateAdapterCopy loads the adapter-copy fixture and returns it with the core instance `run-sync`
// executes in. That instance's world is the one whose thread is inside the sync adapter during the copies,
// so it is the one a `Stop` must wait for.
func instantiateAdapterCopy(tb testing.TB) (*Instantiated, *interp.Instance) {
	tb.Helper()
	b, err := os.ReadFile("testdata/adapter-copy.wasm")
	if err != nil {
		tb.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		tb.Fatalf("instantiate: %v", err)
	}
	cd, ok := in.export.exports["run-sync"]
	if !ok || cd.fn == nil || cd.fn.core.inst == nil {
		tb.Fatal(`the fixture's "run-sync" export resolves to no core instance`)
	}
	return in, cd.fn.core.inst
}

// BenchmarkSP1AdapterArrival is SP-1's instrument for the adapter-copy class (ADR 0104, ruling 1), the
// component-level twin of interp's `BenchmarkSP1Arrival`, and like it a benchmark that asserts nothing. A
// guest calls `run-sync` with a 16 MiB string in a loop, and the host stops `run-sync`'s core instance
// `trials` times at arbitrary moments, reporting `arrival-p50-ns`, `arrival-p99-ns` and `arrival-max-ns`. Run
// it with `-benchtime=1x` on janus's `measured` group.
//
// A stop that lands while no call is inside the instance arrives at once, so the low percentiles mostly
// measure that. The adapter's stretch is in the tail, which is why p99 and max are what the forecast reads.
func BenchmarkSP1AdapterArrival(b *testing.B) {
	const trials = 400
	in, core := instantiateAdapterCopy(b)
	defer in.Close()
	arg := canon.Str(strings.Repeat("a", adapterCopyArgLen))
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := in.CallValues("run-sync", arg); err != nil {
				return
			}
		}
	}()

	var arrivals []time.Duration
	rng := rand.New(rand.NewPCG(1, 3))
	for b.Loop() {
		arrivals = arrivals[:0]
		for i := range trials {
			// The host's clock, spun: a point in the guest's run, not a wait for a signal.
			until := time.Now().Add(time.Duration(rng.Int64N(int64(20 * time.Millisecond))))
			for time.Now().Before(until) {
			}
			t0 := time.Now()
			if err := core.Stop(30 * time.Second); err != nil {
				b.Fatalf("trial %d: Stop: %v", i, err)
			}
			d := time.Since(t0)
			core.Resume()
			if i > 0 {
				arrivals = append(arrivals, d)
			}
		}
	}
	slices.Sort(arrivals)
	pct := func(p float64) time.Duration { return arrivals[int(p*float64(len(arrivals)-1))] }
	b.ReportMetric(float64(pct(0.50).Nanoseconds()), "arrival-p50-ns")
	b.ReportMetric(float64(pct(0.99).Nanoseconds()), "arrival-p99-ns")
	b.ReportMetric(float64(arrivals[len(arrivals)-1].Nanoseconds()), "arrival-max-ns")
}

// adapterListLen is the element count of SP-1's list readings: 512 Ki, the count whose lift reached 9ms with
// every poll in place (ADR 0104's as-built section).
const adapterListLen = 512 << 10

// placeAdapterList loads testdata/adapter-copy-list.wasm and calls `export` once with `arg`, so the list sits
// in the parent's memory at 1024, where the fixture's bump allocator starts (a list's backing is its first
// allocation). It returns the instance and a function that crosses that placed list through the adapter by
// invoking the export's core function directly. The core instance is what a `Stop` must wait for. An export
// call would lower its argument on the embedder's goroutine, outside any agent, which dilutes a reading.
func placeAdapterList(tb testing.TB, export string, arg canon.Value, n int) (*Instantiated, *interp.Instance, func() error) {
	tb.Helper()
	b, err := os.ReadFile("testdata/adapter-copy-list.wasm")
	if err != nil {
		tb.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		tb.Fatalf("instantiate: %v", err)
	}
	cd, ok := in.export.exports[export]
	if !ok || cd.fn == nil || cd.fn.core.inst == nil {
		tb.Fatalf("the fixture's %q export resolves to no core instance", export)
	}
	if res, err := in.CallValues(export, arg); err != nil {
		tb.Fatalf("placing the list: %v", err)
	} else if v, ok := res[0].U32(); !ok || v != uint32(n) {
		tb.Fatalf("placing the list: %s = %v, want u32 %d", export, res, n)
	}
	core, name := cd.fn.core.inst, cd.fn.core.name
	return in, core, func() error {
		res, err := core.Invoke(name, interp.I32(1024), interp.I32(int32(n)))
		if err == nil && (len(res) != 1 || res[0].Int32() != int32(n)) {
			err = fmt.Errorf("%s read %v, want %d", export, res, n)
		}
		return err
	}
}

// adapterListArg builds the `list<u8>` or `list<string>` argument of `n` elements.
func adapterListArg(tb testing.TB, export string, n int) canon.Value {
	tb.Helper()
	elems := make([]canon.Value, n)
	elem := canon.Type{Kind: canon.KindU8}
	for i := range elems {
		if export == "run-strs" {
			elems[i] = canon.Str(string(rune('a' + i%26)))
		} else {
			elems[i] = canon.U8(uint8(i))
		}
	}
	if export == "run-strs" {
		elem = canon.Type{Kind: canon.KindString}
	}
	l, err := canon.List(elem, elems...)
	if err != nil {
		tb.Fatalf("building the list: %v", err)
	}
	return l
}

// BenchmarkSP1AdapterListArrival is SP-1's instrument for a list crossing ADR 0103's adapter (the chair's
// third ruling of 2026-10-18, item 2): `BenchmarkSP1AdapterArrival`'s protocol over a 512 Ki-element list,
// one sub-benchmark per element class. `list<u8>` is the scalar class, which may cross as bytes, and
// `list<string>` the non-scalar one. It asserts nothing. Run it with `-benchtime=1x` on janus's `measured`
// group.
func BenchmarkSP1AdapterListArrival(b *testing.B) {
	for _, class := range []struct {
		name, export string
		trials       int
	}{{"list-u8", "run-list", 400}, {"list-string", "run-strs", 200}} {
		b.Run("class="+class.name, func(b *testing.B) {
			in, core, cross := placeAdapterList(b, class.export, adapterListArg(b, class.export, adapterListLen), adapterListLen)
			defer in.Close()
			done := make(chan struct{})
			defer close(done)
			go func() {
				for {
					select {
					case <-done:
						return
					default:
					}
					if cross() != nil {
						return
					}
				}
			}()
			var arrivals []time.Duration
			rng := rand.New(rand.NewPCG(1, 5))
			for b.Loop() {
				arrivals = arrivals[:0]
				for i := range class.trials {
					// The host's clock, spun: a point in the guest's run, not a wait for a signal.
					until := time.Now().Add(time.Duration(rng.Int64N(int64(20 * time.Millisecond))))
					for time.Now().Before(until) {
					}
					t0 := time.Now()
					if err := core.Stop(30 * time.Second); err != nil {
						b.Fatalf("trial %d: Stop: %v", i, err)
					}
					d := time.Since(t0)
					core.Resume()
					if i > 0 {
						arrivals = append(arrivals, d)
					}
				}
			}
			slices.Sort(arrivals)
			pct := func(p float64) time.Duration { return arrivals[int(p*float64(len(arrivals)-1))] }
			b.ReportMetric(float64(pct(0.50).Nanoseconds()), "arrival-p50-ns")
			b.ReportMetric(float64(pct(0.99).Nanoseconds()), "arrival-p99-ns")
			b.ReportMetric(float64(arrivals[len(arrivals)-1].Nanoseconds()), "arrival-max-ns")
		})
	}
}

// TestAnAdapterCopyCrossesItsString is the fixture's own witness, so the arrival readings time calls known to
// be right: `run-sync` returns the echoed string's length, which wasmtime 49.0.3 reads as len(s).
func TestAnAdapterCopyCrossesItsString(t *testing.T) {
	in, _ := instantiateAdapterCopy(t)
	defer in.Close()
	for _, n := range []int{12, adapterCopyArgLen} {
		t0 := time.Now()
		res, err := in.CallValues("run-sync", canon.Str(strings.Repeat("a", n)))
		if err != nil {
			t.Fatalf("run-sync of %d bytes: %v", n, err)
		}
		if v, ok := res[0].U32(); len(res) != 1 || !ok || v != uint32(n) {
			t.Errorf("run-sync of %d bytes = %v, want u32 %d", n, res, n)
		}
		t.Logf("run-sync of %d bytes: %s", n, time.Since(t0))
	}
}

// TestAStopDuringAnAdapterCopyArrivesWithinTheBound is SP-1's adapter-copy witness (contract §3, ADR 0104):
// an agent inside ADR 0103's sync adapter, copying a 24 MiB string, reaches a safepoint within `bound` of a
// `Stop`, not after the copy.
//
// **The guest's loop is the adapter and almost nothing else.** It invokes `run-sync`'s core function directly,
// over a string placed once in the parent's memory, rather than calling the export. An export call lowers its
// argument on the embedder's goroutine, outside any agent, and a stop landing there arrives at once, which would
// let the neuter pass by luck. The string is placed at 1024, where the fixture's bump allocator starts. Each
// echo is the same bytes, so the allocator's wrap over that region rewrites what is already there.
//
// # Why the criterion is a miss count and not "no miss"
//
// **The bound is 2ms, and the neuter sets it.** On darwin/arm64 one unpolled 24 MiB copy is about 4ms, so the
// 10ms registered on #515 passed under the neuter (run: 3 of 3). The polled arrivals' slowest was about 0.07ms.
//
// **But 2ms is inside a loaded machine's scheduling noise.** The first draft failed on a single miss, and it
// missed once under `make ci`'s parallel test load with its polls in place. A descheduled agent misses at
// random, while an unpolled copy misses whenever a stop lands with more than 2ms of it left, so the two
// separate by rate. Measured locally, alongside a whole-tree test run:
//   - in 20 trials, the polled form missed 0 to 2 times in about 30 runs;
//   - in 40 trials, the polled form missed 0 times in 20 runs, and the neuter missed 15 to 24 times in 20 runs.
//
// So the witness fails at `maxMisses` (6) or more of 40, and a miss is logged either way.
//
// A larger string would separate the two by length instead, but 24 MiB is what the fixture's 64 MiB memories
// hold. A `list<u8>` crosses element by element and is far slower, but it was tried and refused: its lift is
// one zeroed Go allocation per list, unpolled and proportional to the count. That stretch reached 9ms for
// 512 Ki elements with every poll in place, and it is named in ADR 0104's as-built section.
//
// **Neuter, run:** `pollCaller` returning at once, which removes every poll the adapter's heaps make. A stop
// then waits for the copy in progress to end, and misses whenever it lands with more than the bound left. It
// failed 20 of 20 runs. `Close` ends the guest at its next frame entry.
func TestAStopDuringAnAdapterCopyArrivesWithinTheBound(t *testing.T) {
	const n = 24 << 20
	in, core := instantiateAdapterCopy(t)
	defer in.Close()
	if res, err := in.CallValues("run-sync", canon.Str(strings.Repeat("a", n))); err != nil {
		t.Fatalf("placing the string: %v", err)
	} else if v, ok := res[0].U32(); !ok || v != n {
		t.Fatalf("placing the string: run-sync = %v, want u32 %d", res, n)
	}
	name := in.export.exports["run-sync"].fn.core.name
	go func() {
		for {
			if _, err := core.Invoke(name, interp.I32(1024), interp.I32(n)); err != nil {
				return
			}
		}
	}()
	if err := core.Stop(30 * time.Second); err != nil {
		t.Fatalf("the first Stop: %v", err)
	}
	core.Resume()

	const (
		bound     = 2 * time.Millisecond * raceSlowdown
		trials    = 40
		maxMisses = 6
	)
	rng := rand.New(rand.NewPCG(1, 4))
	var worst time.Duration
	var misses []int
	for i := range trials {
		// The host's clock, spun: a point in the guest's run, not a wait for a signal.
		until := time.Now().Add(time.Duration(rng.Int64N(int64(40 * time.Millisecond))))
		for time.Now().Before(until) {
		}
		t0 := time.Now()
		err := core.Stop(bound)
		d := time.Since(t0)
		core.Resume()
		if err != nil {
			misses = append(misses, i)
			t.Logf("trial %d: Stop(%s) missed after %s: %v", i, bound, d, err)
		}
		worst = max(worst, d)
	}
	t.Logf("the slowest of %d arrivals: %s; %d missed", trials, worst, len(misses))
	if len(misses) >= maxMisses {
		t.Fatalf("Stop(%s) on an agent inside a sync adapter copy missed its deadline in %d of %d trials %v, "+
			"at or past %d; the adapter's heaps poll between %d-byte chunks, so misses at that rate mean a copy "+
			"ran unpolled", bound, len(misses), trials, misses, maxMisses, canon.StringChunk)
	}
}
