// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
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

// TestAStopLandsInsideAnAdapterCopy is SP-1's adapter-copy witness (contract §3, ADR 0104): a `Stop` can park the
// caller's agent **inside** one of ADR 0103's adapter copies, which only a poll inside the copy can do. One
// subtest crosses a 24 MiB string, and one a 24 Mi-element `list<u8>`, the packed form.
//
// # Why it asserts a place and not a time
//
// This witness's first two drafts asserted a 2ms arrival bound, the second under a miss-count criterion. CI
// falsified that: on #1066's run, CI amd64 missed 11 of 40 trials **with the polls in place**. A wall-clock
// bound tight enough for a neuter to miss on a fast machine is inside a loaded runner's scheduling noise, and
// no count separates the two where the noise is that large.
//
// So the bound itself is carried by the janus readings on #515 (`BenchmarkSP1AdapterArrival`,
// `BenchmarkSP1AdapterListArrival`). This witness is the regression guard, and it asserts a fact noise cannot
// produce. Each `Stop` has a long deadline, so it always returns with the agent parked. After it, the parent's
// two result regions are read (testdata/adapter-copy-flip.wat). A region holding two fill bytes is a write
// stopped part-way, so the agent parked mid-copy. **The polled form parks mid-copy in some trials. The
// unpolled form never can**, because the adapter's other safepoints are frame entries, before or after a
// copy.
//
// The values are read against wasmtime 49.0.3 first (`run-sync("hello")` = 5, `run-list([1, 2, 3])` = 3), and
// every crossing in the loop checks its length.
//
// **Neuters, run:**
//   - **string:** `pollCaller` returning at once removes every adapter poll, and no trial parks mid-copy;
//   - **list<u8>:** `storeListData`'s packed arm writing the whole list in one `WriteBytes`, and no trial
//     parks mid-copy.
//
// The packed *lift*'s chunking is a read, which no memory state records. Its deterministic witness is canon's
// `TestAPackedListCrossesTheHeapInChunks`.
func TestAStopLandsInsideAnAdapterCopy(t *testing.T) {
	const n = 24 << 20
	b, err := os.ReadFile("testdata/adapter-copy-flip.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, c := range []struct {
		name, export string
		small        canon.Value
		smallLen     uint32
	}{
		{"string", "run-sync", canon.Str("hello"), 5},
		{"list<u8>", "run-list", adapterListArg(t, "run-list", 3), 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, err := Instantiate(b)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			cd, ok := in.export.exports[c.export]
			if !ok || cd.fn == nil || cd.fn.core.inst == nil || cd.fn.mem == nil {
				t.Fatalf("the fixture's %q export resolves to no core instance and memory", c.export)
			}
			// The oracle reading first, on a small value. It also moves the parent's bump allocator, so the
			// regions below are computed from where it now stands rather than from its initial value.
			if res, err := in.CallValues(c.export, c.small); err != nil {
				t.Fatalf("%s of %d: %v", c.export, c.smallLen, err)
			} else if v, ok := res[0].U32(); !ok || v != c.smallLen {
				t.Fatalf("%s of %d = %v, want u32 %d, wasmtime's reading", c.export, c.smallLen, res, c.smallLen)
			}
			core, name, mem := cd.fn.core.inst, cd.fn.core.name, cd.fn.mem
			// The crossings read their argument at `region`, which holds zero bytes to begin with. Zero bytes
			// are a valid string and a valid list<u8>, so nothing is placed.
			//
			// **Where the results land, which the check depends on.** The fixture's allocator starts the first
			// two n-byte results just past the oracle call's few bytes. From the third on, it wraps, and they
			// alternate exactly at `region` and `region+n`. So four crossings are made before any trial: until
			// then a region can hold two bytes left by an earlier, offset write rather than a stopped one.
			const region = 1024
			cross := func() error {
				res, err := core.Invoke(name, interp.I32(region), interp.I32(n))
				if err == nil && (len(res) != 1 || res[0].Int32() != n) {
					err = fmt.Errorf("%s read %v, want %d", c.export, res, n)
				}
				return err
			}
			for i := range 4 {
				if err := cross(); err != nil {
					t.Fatalf("priming crossing %d: %v", i, err)
				}
			}
			crossErr := make(chan error, 1)
			go func() {
				for {
					if err := cross(); err != nil {
						crossErr <- err
						return
					}
				}
			}()
			if err := core.Stop(30 * time.Second); err != nil {
				t.Fatalf("the first Stop: %v", err)
			}
			core.Resume()

			// mixed reports whether the region at off holds two distinct bytes. The adapter writes a result
			// forward, chunk by chunk, so a write stopped part-way has its new byte first and its old byte last,
			// and comparing the region's two ends detects it. The region is read only while the agent is parked,
			// so no write is in flight, and `Stop` returning is the edge that orders the agent's writes before
			// this read (SP-6).
			mixed := func(off uint64) bool {
				first, ferr := interp.ReadBoundaryMemory(mem, off, 1)
				last, lerr := interp.ReadBoundaryMemory(mem, off+n-1, 1)
				if ferr != nil || lerr != nil {
					t.Fatalf("reading the region at %d: %v %v", off, ferr, lerr)
				}
				return !bytes.Equal(first, last)
			}

			// 200 trials, because about 1 in 10 lands inside a copy. The rest land in the adapter's other
			// stretches, or in the callee's own fill, which the caller's stop waits for (#515's callee gap).
			const trials = 200
			rng := rand.New(rand.NewPCG(1, 6))
			inside := 0
			var slowest time.Duration
			for i := range trials {
				// The host's clock, spun: a point in the guest's run, not a wait for a signal.
				until := time.Now().Add(time.Duration(rng.Int64N(int64(40 * time.Millisecond))))
				for time.Now().Before(until) {
				}
				t0 := time.Now()
				if err := core.Stop(30 * time.Second); err != nil {
					t.Fatalf("trial %d: Stop: %v", i, err)
				}
				slowest = max(slowest, time.Since(t0))
				if mixed(region) || mixed(region+n) {
					inside++
				}
				core.Resume()
			}
			select {
			case err := <-crossErr:
				t.Fatalf("a crossing failed while the trials ran: %v", err)
			default:
			}
			t.Logf("%d of %d stops parked the agent inside an adapter copy; the slowest arrival was %s "+
				"(logged, not asserted: the bound is janus's reading on #515)", inside, trials, slowest)
			if inside == 0 {
				t.Fatalf("no %s stop of %d parked the agent inside the adapter's result copy: every region "+
					"held one fill byte. Only a poll inside the copy can park the agent there, so the copy ran "+
					"unpolled (SP-1, ADR 0104)", c.name, trials)
			}
		})
	}
}
