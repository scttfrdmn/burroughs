// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestSweepRow runs one fork-built Go test binary on this host and emits one structured row.
//
// # Why this is committed rather than scratch
//
// It is the harness the unaimed-program sweep's baselines are measured with, and **an earlier version of it
// was written, used to produce batch 2's rows, and then deleted** — hours after the fork's standing property
// 18 was written down, which says in as many words that a witness which is not committed is not a witness.
// The rows survived in a registration that could no longer be re-derived. So it lives here.
//
// **The guests do NOT live here**, and that is slice 1's precedent rather than an oversight: each is a
// fork-built Go test binary of 6–18 MB, built by a toolchain outside this repository. The skip below is
// licensed on exactly that ground — the harness is the part that was lost and the part worth keeping.
//
// # The four registered disciplines it implements
//
//  1. the BOUND comes from the caller, which takes it from the package's own observed finish time;
//  2. per-test OUTCOME SETS are readable, because the guest's whole output is dumped;
//  3. the row is structured, so tallies read named fields through `parsePoRow` rather than a regex;
//  4. the dump fires on BOTH terminal paths — the arm whose outcome set mattered most in the `archive/zip`
//     comparison was the one that hung, and a FINISHED-only dump had missed it.
func TestSweepRow(t *testing.T) {
	guest := os.Getenv("SWEEP_GUEST")
	if guest == "" {
		t.Skip("SWEEP_GUEST unset: the guests are fork-built binaries outside this repository")
	}
	img, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	feats := GuestFeatures()
	feats.Threads = true

	var out syncOut
	cfg := Config{
		Wasm: img, Args: append([]string{"sweep"}, strings.Fields(os.Getenv("SWEEP_ARGS"))...),
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out, Features: &feats,
	}
	if d := os.Getenv("SWEEP_DIR"); d != "" {
		cfg.Preopens = []Preopen{{Host: d, Guest: d}}
	}
	m, derr := decodeGuest(cfg)
	if derr != nil {
		t.Fatalf("decode: %v", derr)
	}
	h := newHost(cfg)
	if ferr := h.initFDs(cfg.Preopens); ferr != nil {
		t.Fatal(ferr)
	}

	var mstartIdx uint32
	for i := range m.Exports {
		if m.Exports[i].Name == "mstart" && m.Exports[i].Kind == bin.ExternFunc {
			mstartIdx = m.Exports[i].Index
		}
	}
	spawnFT, ok := clause2ImportType(m, "burroughs", "spawn")
	if !ok {
		t.Fatal("guest imports no `burroughs spawn`, so it carries no spawn door")
	}

	var in *interp.Instance
	var spawns, procExits atomic.Int32
	var peCode atomic.Int32
	var peTID atomic.Int64
	peCode.Store(-1)
	peTID.Store(-1)
	hostImports := h.imports()
	imp := func(mod, name string) (interp.Extern, bool) {
		if mod == "burroughs" && name == "spawn" {
			return interp.HostExtern(spawnFT, func(_ *interp.Caller, v []interp.Value) ([]interp.Value, error) {
				spawns.Add(1)
				tid, e := in.Spawn(mstartIdx, v[0].Int32(), 0)
				if len(spawnFT.Results) == 0 {
					return nil, e
				}
				return []interp.Value{interp.I32(int32(tid))}, e
			}), true
		}
		if mod == module && name == "proc_exit" {
			// Wraps rather than replaces: the real handler decides, and this only records WHICH agent asked,
			// which is the field ADR 0090's finding turned on.
			return interp.HostExtern(bin.FuncType{Params: []bin.ValType{bin.I32}},
				func(c *interp.Caller, v []interp.Value) ([]interp.Value, error) {
					procExits.Add(1)
					peCode.Store(int32(u32(v, 0)))
					peTID.Store(int64(c.Thread()))
					return h.procExit(c, v)
				}), true
		}
		return hostImports(mod, name)
	}

	var trap *interp.Trap
	in, trap, err = interp.InstantiateLinked(m, imp)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	defer func() { _ = in.Close() }()

	bound := 300 * time.Second
	if b := os.Getenv("SWEEP_BOUND"); b != "" {
		if d, e := time.ParseDuration(b); e == nil {
			bound = d
		}
	}

	verdicts := func() int {
		s := out.String()
		return strings.Count(s, "--- PASS") + strings.Count(s, "--- FAIL") + strings.Count(s, "--- SKIP")
	}
	var start time.Time
	emit := func(tag string) {
		// No value carries a space, which is the one constraint `parsePoRow` places on an emitter.
		fmt.Printf("PE-%s guest=%s t=%s bytes=%d verdicts=%d spawns=%d procExit=%d code=%d exitTID=%d\n",
			tag, sweepBase(guest), time.Since(start).Round(time.Second), out.Len(), verdicts(),
			spawns.Load(), procExits.Load(), peCode.Load(), peTID.Load())
		os.Stdout.Sync()
	}
	dump := func(tag string) {
		fmt.Printf("PE-GUESTOUT-BEGIN %s\n%s\nPE-GUESTOUT-END\n", tag, out.String())
		os.Stdout.Sync()
	}

	start = time.Now()
	done := make(chan struct{})
	go func() { _, _ = in.Invoke("_start"); close(done) }()
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	deadline := time.After(bound)
	for {
		select {
		case <-done:
			emit("FINISHED")
			dump("FINISHED")
			return
		case <-tk.C:
			emit("PROGRESS")
		case <-deadline:
			emit("HUNG")
			dump("HUNG")
			return
		}
	}
}

// syncOut is a mutex-guarded output sink, and it replaces a bare `strings.Builder` that was a DATA RACE.
//
// # The race, and why the arm it was found on is the interesting part
//
// The guest writes here from the `Invoke` goroutine, through the host's `fd_write` (`preview1.go`'s `sink`);
// the row emitter reads it from the test goroutine. On the FINISHED path those two are ordered by `done`
// closing, so that read was always safe. **On the PROGRESS and HUNG paths there is no such edge**, and
// `-race` reports the pair directly.
//
// It was found by a falsification aimed at the HUNG arm, and the registered hypothesis said in as many words
// that *"the FINISHED path cannot show it because `done` has closed before anything reads"*. **That sentence
// is false**: `emit("PROGRESS")` reads while the guest is still writing, and PROGRESS fires every 30 s — so
// the defect was live on every run longer than half a minute, which is every baseline row the unaimed-program
// sweep has taken. The falsification found something broader than its target, which is the argument for
// running one rather than reasoning about the arm.
//
// The rows already taken rest on the FINISHED read, which is the synchronised one; what was unsynchronised is
// the PROGRESS row's `bytes`/`verdicts`, and nothing consumed those.
type syncOut struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncOut) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncOut) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func (s *syncOut) Len() int                    { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Len() }

// sweepBase keeps a row's `guest` field space-free.
func sweepBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
