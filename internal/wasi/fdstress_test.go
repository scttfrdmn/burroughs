// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestScratchWritePathIsSafeUnderConcurrentHostCalls is batch 4's lever 3 at the host level: the **write**
// machinery ADR 0091 added, under contention.
//
// # What it adds over the lock witness that already exists
//
// [TestFDTableIsSafeUnderConcurrentHostCalls] covers the table's lock, and states its own gap honestly. It
// drives `addFD`/`dropFD`/`fdClose` over entries holding `writer: io.Discard`, so it never touches anything
// this slice built. Three properties are new and none of them is the lock:
//
//  1. **a `*os.Root` shared by every call through one grant** — Go documents `os.Root` as safe for concurrent
//     use, and this measures it rather than citing it;
//  2. **read-back verification** — a payload written and read through the grant must come back byte-identical,
//     which is the only channel that can see a write landing in the wrong file. A count of successful calls
//     cannot;
//  3. **the fd table returns to its starting size** — a leak or a double-close breaks it, and neither shows up
//     as a failed call.
//
// # Why the handlers are not driven end to end here
//
// `fd_pwrite` reads its iovecs from guest memory, so it needs a `Caller` and therefore an instance. The
// guest-level arm ([TestFdStressGuestVerifiesEveryPayload]) is where the real handler path runs; this one
// drives the machinery beneath it — the root, the nested root, the table — from many host goroutines at once,
// which is the arrangement a single-agent guest cannot produce.
//
// It is only a full witness under `-race`; without it a data race on a map or a root is silently
// sometimes-fine. `make ci` runs the suite with the detector on.
func TestScratchWritePathIsSafeUnderConcurrentHostCalls(t *testing.T) {
	scratch := t.TempDir()
	h, preopenFD := hostFor(t, Preopen{Host: scratch, Guest: "/s", Writable: true})

	startSize := h.fdCountForTest()
	if startSize == 0 {
		t.Fatal("the fd table is empty before the run; stdio and the preopen should be in it, and the " +
			"size assertion at the end would be comparing nothing to nothing")
	}

	root, e := h.dirRootAt(preopenFD)
	if e != errSuccess {
		t.Fatalf("the scratch grant yielded no root (errno %d)", e)
	}

	const workers, each = 8, 40
	var ops, mismatches, ioErrors atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for it := range each {
				// Unique per worker AND iteration, so a crossed handle yields another worker's bytes rather
				// than a value this one could plausibly have written.
				payload := fmt.Sprintf("w%03d-i%03d-payload", w, it)
				name := fmt.Sprintf("f-%d-%d.bin", w, it)

				if err := root.WriteFile(name, []byte(payload), 0o600); err != nil {
					ioErrors.Add(1)
					t.Errorf("w=%d it=%d WriteFile through the shared root: %v", w, it, err)
					continue
				}
				got, err := root.ReadFile(name)
				if err != nil {
					ioErrors.Add(1)
					t.Errorf("w=%d it=%d ReadFile: %v", w, it, err)
					continue
				}
				if string(got) != payload {
					mismatches.Add(1)
					t.Errorf("w=%d it=%d read back %q, wrote %q — a write landed in the wrong file, or two "+
						"handles addressed one", w, it, got, payload)
				}
				// The table, exercised with entries that carry the new fields, concurrently with the root
				// traffic above. A file fd is added and closed, so `addFD`/`dropFD` run under contention
				// with `fd` reads on a stable descriptor.
				f, oerr := root.OpenFile(name, os.O_RDWR, 0)
				if oerr != nil {
					ioErrors.Add(1)
					t.Errorf("w=%d it=%d OpenFile: %v", w, it, oerr)
					continue
				}
				fd := h.addFD(&fdEntry{file: f, hostPath: filepath.Join(scratch, name), writable: true})
				if got := h.fd(1); got == nil {
					t.Error("fd 1 (stdout) vanished from the table under concurrent mutation")
				}
				if _, cerr := h.fdClose(nil, []interp.Value{interp.I32(int32(fd))}); cerr != nil {
					t.Errorf("w=%d it=%d fdClose: %v", w, it, cerr)
				}
				if err := root.Remove(name); err != nil {
					ioErrors.Add(1)
					t.Errorf("w=%d it=%d Remove: %v", w, it, err)
				}
				ops.Add(1)
			}
		}(w)
	}
	wg.Wait()

	// **Non-vacuity, asserted rather than assumed.** Every failure path above `continue`s, so a run in which
	// nothing worked reports zero mismatches — the same reading as a run in which everything worked.
	if want := int64(workers * each); ops.Load() != want {
		t.Errorf("completed %d of %d operations: a witness that skipped its work reports zero mismatches "+
			"for the same reason a clean one does", ops.Load(), want)
	}
	if endSize := h.fdCountForTest(); endSize != startSize {
		t.Errorf("the fd table went from %d entries to %d: every descriptor this test opened was closed, so "+
			"a difference is a leak or a double-close, and neither appears as a failed call",
			startSize, endSize)
	}
	t.Logf("FDSTRESS-HOST ops=%d mismatches=%d ioerrors=%d table=%d->%d",
		ops.Load(), mismatches.Load(), ioErrors.Load(), startSize, h.fdCountForTest())
}

// TestFdStressGuestVerifiesEveryPayload is lever 3's guest-level arm: the real handler path, driven by a guest
// whose goroutines each check what they wrote.
//
// # Its reach, and the part it cannot have
//
// A **stock** `wasip1` guest runs at `GOMAXPROCS=1`, so its goroutines multiplex onto one agent and every host
// call arrives on one thread. **So this arm does not contend the fd table**; the host arm above does that. What
// this arm has instead is the only thing that can see a *wrong payload*: the guest knows what it wrote.
//
// The multi-agent version needs `GOEXPERIMENT=burroughsspawn` and a `burroughs spawn` import this package
// cannot supply — the same limitation [TestFDTableIsSafeUnderConcurrentHostCalls] records — so it runs in the
// sweep, out of CI, against a fork-built guest. Batch 4's registration carries that row.
//
// # `WriteAt`/`ReadAt`, not `Write` then `Seek`
//
// `fd_seek` is among the 21 functions still refused (ADR 0091 amendment 1). A seek-based round trip would fail
// on every iteration, every iteration would `continue`, and the guest would report **zero mismatches out of
// zero operations** — a witness passing by asking nothing. The offset pair is also the better subject, since
// `fd_pwrite` is what this slice added and this is the only thing exercising it under concurrency.
func TestFdStressGuestVerifiesEveryPayload(t *testing.T) {
	wasm := buildGuest(t, "fdstress")
	scratch := t.TempDir()

	var out, errBuf strings.Builder
	cfg := Config{
		Wasm: wasm, Args: []string{"fdstress", "/tmp", "8", "16"},
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errBuf,
		Preopens: []Preopen{{Host: scratch, Guest: "/tmp", Writable: true}},
	}
	code, err := Run(cfg)
	if err != nil {
		t.Fatalf("the guest did not run: %v\nstdout: %s\nstderr: %s", err, out.String(), errBuf.String())
	}
	body := out.String()
	if !strings.Contains(body, "FDSTRESS-END") {
		t.Fatalf("the guest did not reach its end marker, so its counts are a prefix rather than a result:\n%s",
			body)
	}
	if code != 0 {
		t.Errorf("the guest exited %d: it exits non-zero only on a payload mismatch\n%s", code, body)
	}

	var ops, mismatches, ioErrors, reopened int
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "FDSTRESS-FINAL ") {
			fmt.Sscanf(ln, "FDSTRESS-FINAL ops=%d mismatches=%d ioerrors=%d reopened=%d",
				&ops, &mismatches, &ioErrors, &reopened)
		}
	}
	// **Non-vacuity first**, because every failure path in the guest continues: a run where nothing worked
	// reports zero mismatches, which is what a clean run reports.
	if ops != 8*16 {
		t.Errorf("the guest completed %d of %d operations — a witness that skipped its work reports zero "+
			"mismatches for the same reason a clean one does\n%s", ops, 8*16, body)
	}
	if mismatches != 0 {
		t.Errorf("%d payload mismatch(es): a write landed in the wrong file, or one fd was handed to two "+
			"goroutines\n%s", mismatches, body)
	}
	if ioErrors != 0 {
		t.Errorf("%d I/O error(s) under a scratch grant, where every call should be permitted\n%s",
			ioErrors, body)
	}
	if reopened == 0 {
		t.Errorf("the guest could reopen no files after the run, which is what an exhausted fd table looks "+
			"like from inside\n%s", body)
	}
	t.Logf("FDSTRESS-GUEST ops=%d mismatches=%d ioerrors=%d reopened=%d", ops, mismatches, ioErrors, reopened)
}
