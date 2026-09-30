// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The eighth guest (batch 4, lever 3): an AIMED fd-table stress, because the standard library cannot supply
// one on this target.
//
// # Why it had to be written
//
// The sweep's five clean packages were all selected the same way — rank-1 on "tests that spawn goroutines" —
// and the tests that would stress the fd table hardest are precisely the ones Go declines on `wasip1`: `os`'s
// three hardest are pipe tests, and `wasip1` has no pipes. So the fd table has been exercised by exactly one
// concurrent-writer test (`TestWriteAtConcurrent`) across two batches. A clean board from one lever is not a
// clean engine, and this guest is the second lever aimed straight at the table.
//
// # What it asserts, and why each part is there
//
// N goroutines, each in a loop: create a file under the scratch grant, write a payload unique to that
// goroutine and iteration, seek back, read it, and **compare**. Then close.
//
//   - **The payload is read back and compared, not counted.** A torn write, a write landing in another
//     goroutine's file, or an fd handed to two goroutines at once produces wrong *bytes*; a count of
//     successful calls sees none of that. The guest is the only party that knows what it wrote.
//   - **Every goroutine's payload is unique**, so a crossed fd shows up as another goroutine's bytes rather
//     than as a plausible value.
//   - **The fd table must return to its starting size.** Reported as a count of files the guest could still
//     open at the end; a leak or a double-close breaks it. The host asserts the table directly, which is the
//     stronger channel — this is the guest's own half.
//
// # Reporting, not exiting
//
// Every failure is printed and counted, and the guest exits non-zero only at the end. A guest that exited on
// the first mismatch would hide how many there were and which goroutines saw them, which is the difference
// between "there is a race" and "here is its shape".
//
// Compiled to GOOS=wasip1 by the harness; lives under testdata so the toolchain does not build it with the
// package.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

func main() {
	base := "/tmp"
	workers, iters := 8, 24
	if len(os.Args) > 1 {
		base = os.Args[1]
	}
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil && n > 0 {
			workers = n
		}
	}
	if len(os.Args) > 3 {
		if n, err := strconv.Atoi(os.Args[3]); err == nil && n > 0 {
			iters = n
		}
	}
	fmt.Printf("FDSTRESS-BEGIN base=%s workers=%d iters=%d gomaxprocs=%d\n",
		base, workers, iters, runtimeGOMAXPROCS())

	var mismatches, ioErrors, ops atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for it := range iters {
				// Unique per goroutine AND iteration, so a crossed fd yields another worker's bytes
				// rather than a value that could have been this one's.
				payload := fmt.Sprintf("w%04d-i%04d-%s", w, it, "0123456789abcdef")
				name := filepath.Join(base, fmt.Sprintf("fdstress-%d-%d.bin", w, it))

				f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
				if err != nil {
					ioErrors.Add(1)
					fmt.Printf("FDSTRESS-ERR w=%d it=%d open: %v\n", w, it, err)
					continue
				}
				// **`WriteAt`/`ReadAt`, not `Write` then `Seek`.** `fd_seek` is among the 21 functions
				// still refused (ADR 0091 amendment 1), so a seek-based round trip would fail on every
				// iteration, every iteration would `continue`, and the guest would report **zero
				// mismatches out of zero operations** — a witness passing by asking nothing. Caught before
				// the first run by checking the implemented set rather than by seeing a suspicious zero.
				//
				// The offset pair is also the better subject: `fd_pwrite` is the capability this slice just
				// added, and this is the only thing exercising it under real concurrency.
				if _, err := f.WriteAt([]byte(payload), 0); err != nil {
					ioErrors.Add(1)
					fmt.Printf("FDSTRESS-ERR w=%d it=%d writeat: %v\n", w, it, err)
					_ = f.Close()
					continue
				}
				// **A yield between the write and the read, and it is load-bearing.** Without it this
				// guest could not observe a crossed handle at all: at `GOMAXPROCS=1` there is no
				// preemption point between two host calls, so the write/read pair is effectively atomic
				// and another worker cannot interleave. An injection that pointed every `path_open` at one
				// shared file produced **zero mismatches out of 128 operations** — the witness reported
				// perfect agreement about a file every worker was overwriting.
				//
				// `Gosched` opens the window the property needs. It does not manufacture a defect: if the
				// engine keeps each worker's fd distinct, another worker running here changes nothing.
				runtime.Gosched()
				buf := make([]byte, len(payload))
				n, err := f.ReadAt(buf, 0)
				if err != nil {
					ioErrors.Add(1)
					fmt.Printf("FDSTRESS-ERR w=%d it=%d readat: %v\n", w, it, err)
					_ = f.Close()
					continue
				}
				if got := string(buf[:n]); got != payload {
					mismatches.Add(1)
					fmt.Printf("FDSTRESS-MISMATCH w=%d it=%d wrote=%q read=%q\n", w, it, payload, got)
				}
				if err := f.Close(); err != nil {
					ioErrors.Add(1)
					fmt.Printf("FDSTRESS-ERR w=%d it=%d close: %v\n", w, it, err)
				}
				ops.Add(1)
			}
		}(w)
	}
	wg.Wait()

	// The guest's half of the table check: how many fds it can still open. A leak shows up as this number
	// falling; the host asserts the table's size directly, which is the authority.
	var held []*os.File
	for i := range 32 {
		f, err := os.OpenFile(filepath.Join(base, fmt.Sprintf("fdstress-final-%d.bin", i)),
			os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			break
		}
		held = append(held, f)
	}
	fmt.Printf("FDSTRESS-FINAL ops=%d mismatches=%d ioerrors=%d reopened=%d\n",
		ops.Load(), mismatches.Load(), ioErrors.Load(), len(held))
	for _, f := range held {
		_ = f.Close()
	}
	fmt.Println("FDSTRESS-END")
	if mismatches.Load() > 0 {
		os.Exit(1)
	}
}
