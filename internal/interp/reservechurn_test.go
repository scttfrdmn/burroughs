// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestMappedMemoriesSurviveChurn is #1089's churn probe of ADR 0076's mapping lifecycle. ADR 0076 is the cause of
// orion's darwin `-race` fatal (0 in 10,000 with its mappings off, against 7 in about 10,700 with them on), and a
// suspect for CI's linux/arm64 fatal.
//
// It does to mappings at scale what `internal/spec`'s tests do in passing. Goroutines instantiate a module whose
// memory is a mapped reservation, then the guest grows it, stores into its new pages, fills a span across a page
// boundary, and reads everything back. The instance is then dropped, so the mapping is released through
// `runtime.AddCleanup` (`memory.go`'s `newMemory`), while forced collections run. A wrong read-back fails the test,
// and so would a runtime fatal.
//
// **It is a probe, not a witness of a known defect**, so there is no neuter to run against it yet. What it asserts
// is that the guest's own view of its memory stays correct across the lifecycle. It also reports whether mappings
// were made at all (the reservation gauge), so a run on the Go-allocator path can't be read as one on mappings. It
// reports rather than fails, because the `burroughs_noreserve` arm runs it on purpose.
//
// Short by default, so every `go test` runs it: 4 goroutines, 64 cycles each. BURROUGHS_RESERVE_CHURN=<cycles>
// lengthens it for the stress workflow.
func TestMappedMemoriesSurviveChurn(t *testing.T) {
	cycles := 64
	if s := os.Getenv("BURROUGHS_RESERVE_CHURN"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			t.Fatalf("BURROUGHS_RESERVE_CHURN=%q is not a positive cycle count", s)
		}
		cycles = n
	}
	// One page at instantiation and room to grow to 64, so the memory is a reservation. The guest grows by 3,
	// stores the seed at the start of each new page and at an unaligned offset, fills 32 bytes across the
	// boundary between pages 2 and 3, and returns a checksum the host recomputes.
	const src = `(module
		(memory (export "mem") 1 64)
		(func (export "work") (param $seed i32) (result i32)
			(drop (memory.grow (i32.const 3)))
			(i32.store (i32.const 65536) (local.get $seed))
			(i32.store (i32.const 131072) (i32.add (local.get $seed) (i32.const 1)))
			(i32.store (i32.const 196609) (i32.add (local.get $seed) (i32.const 2)))
			(memory.fill (i32.const 196592) (i32.const 0x5a) (i32.const 16))
			(i32.add
				(i32.add (i32.load (i32.const 65536)) (i32.load (i32.const 131072)))
				(i32.add (i32.load (i32.const 196609)) (i32.load8_u (i32.const 196600))))))`

	before := reservationPeak.Load()
	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range cycles {
				seed := int32(w*1_000_000 + c)
				in := hostLink(t, src, binary.Features{}, nil)
				res, err := in.Invoke("work", I32(seed))
				if err != nil {
					errs <- "invoke: " + err.Error()
					return
				}
				want := seed + (seed + 1) + (seed + 2) + 0x5a
				if got := res[0].Int32(); got != want {
					errs <- "worker " + strconv.Itoa(w) + " cycle " + strconv.Itoa(c) + ": checksum " +
						strconv.Itoa(int(got)) + ", want " + strconv.Itoa(int(want))
					return
				}
				_ = in.Close()
				if c%8 == 7 {
					runtime.GC() // run the AddCleanup releases while other workers are mid-cycle
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	// A run on the Go-allocator path, under burroughs_noreserve or a port with no mappings, asks nothing of ADR
	// 0076. Say so rather than pass.
	if runtime.GOOS != "windows" && reservationPeak.Load() <= before && reservationLive.Load() == 0 {
		t.Logf("no mapping was made (reservation gauge unchanged), so this run exercised the Go-allocator path")
	}
}
