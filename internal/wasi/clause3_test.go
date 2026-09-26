// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// Zero-page slots clause 3's guest stores its verdict into. Same region and same rationale as clause 2's,
// one block up, and disjoint from it.
const (
	c3AddrVerdict   = 0x0F20 // 0 = not reached, 1 = VERIFIED, 2 = verification FAILED
	c3AddrCollected = 0x0F24 // collections completed
	c3AddrFinalized = 0x0F28 // finalizers that ran on a REACHABLE node — must be 0
	c3AddrMutations = 0x0F2C // rewrites performed
	c3AddrBadNode   = 0x0F30 // id of the first node that failed verification, or 0
	c3AddrCount     = 0x0F34 // nodes seen by the verifying walk
)

// The registered figures, from harness-protocol.md's clause 3 positive arm: 8 collections, 120,000
// mutations, 0 finalizers, 4000 nodes verified.
const (
	c3WantCollected = 8
	c3WantMutations = 120_000
	c3WantNodes     = 4000
)

type c3Result struct {
	verdict, collected, finalized, mutations, badNode, count uint32
}

// TestClause3GCAcrossTwoAgents is Phase 4 **clause 3**'s witness, reconstructed and committed.
//
// A Go program compiled by the fork completes garbage collections while running on two host agents, and
// **the heap is verified afterwards**. Not `gctrace` output, not "it didn't crash".
//
// # The two things a broken collection cannot fake
//
//  1. **A checksummed live structure.** A ring of 4000 nodes, each `sum` derived from its own fields *and
//     its successor's id*. One goroutine rewrites payloads and re-derives checksums continuously while
//     another forces collections. A collector that marks while links are being rewritten can free a node
//     reachable only through a pointer in flight and hand its memory to a later allocation; the walk then
//     reads a wrong `sum`, a garbage successor, or the wrong count. **None of those requires a crash** —
//     the failure this is for is silent.
//  2. **Finalizers on nodes the structure still holds.** A finalizer running on a reachable object is
//     unambiguous evidence the collector freed something live. It needs no memory-model argument, and the
//     count must be exactly 0.
//
// # The negative arm is the load-bearing half, and it is cross-binary
//
// `c3_notstopped.wasm` is the **same guest source** built against a fork whose `stopTheWorldWithSema` has
// its wait neutered, so the world is not actually stopped. A witness that passes on a not-stopped world
// proves nothing about a stopped one — and a single-agent arm is *not* a substitute, because one agent
// cannot produce the interleaving at all.
//
// Two edits were needed rather than one, and the second is the interesting one: neutering the wait alone
// leaves the following `sched.stopwait != 0` double-check, which **throws**. A throw is a crash, and this
// witness is for the silent failure — so the check is disabled too, or the negative arm would prove that
// Go's own assertion works instead of that the heap corrupts.
//
// Provenance for both binaries, including the exact edits, is in `testdata/clause3/PROVENANCE`.
func TestClause3GCAcrossTwoAgents(t *testing.T) {
	// **The REPETITION COUNT is reduced by default, and the figures are not.** One run of this guest costs
	// ~77s under the interpreter, so the registered n (10 positive / 3 negative) is ~17 minutes — paid by
	// every `make check` and every CI run. What each run asserts is unchanged and exact: 8 collections,
	// 120,000 mutations, 0 finalizers, 4000 nodes, bad=0. **Only the RATE is weakened**, and the
	// registered rate was reproduced at full n on 2026-09-25 with the numbers recorded in
	// `testdata/clause3/PROVENANCE`.
	//
	// This is not a skip: both arms always run, and both always assert their figures. `BURROUGHS_WITNESS_FULL=1`
	// restores the registered n for anyone re-establishing the rate rather than guarding the regression.
	full := os.Getenv("BURROUGHS_WITNESS_FULL") != ""
	nPos, nNeg := 1, 1
	if full {
		nPos, nNeg = 10, 3
	}
	for _, arm := range []struct {
		name     string
		file     string
		n        int
		wantPass bool
		why      string
	}{{
		name: "world_stopped", file: "c3_stopped.wasm", n: nPos, wantPass: true,
		why: "the collector did not act on a heap the mutator was still changing",
	}, {
		name: "world_not_stopped", file: "c3_notstopped.wasm", n: nNeg, wantPass: false,
		why: "with the STW wait neutered the collector marks a heap under mutation, and the walk must catch it",
	}} {
		t.Run(arm.name, func(t *testing.T) {
			var verified, ran, died int
			for i := range arm.n {
				r, err := runClause3Arm(t, arm.file)
				// **A run that DIED is a third outcome, and it is counted rather than fataled.** The
				// registration named two — verified, or verification failed — and the neutered world
				// produces a third: the Go runtime throws inside its own mark workers before the guest
				// reaches its walk. That is a real discrimination (the positive arm never does it) but it
				// is NOT the registered reading, so it is recorded under its own name instead of being
				// folded into "failed".
				if err != nil {
					if errors.Is(err, errNoVerdict) && !arm.wantPass {
						died++
						ran++
						t.Logf("CLAUSE3 %s run=%d DIED-BEFORE-VERIFYING collected=%d mutations=%d: %v",
							arm.name, i+1, r.collected, r.mutations, err)
						// Even a died run must show the collector ran, or the arm is vacuous.
						if r.collected == 0 {
							t.Errorf("run %d: died with 0 collections, so nothing about a not-stopped "+
								"world was exercised", i+1)
						}
						continue
					}
					t.Fatalf("run %d: %v", i+1, err)
				}
				ran++
				t.Logf("CLAUSE3 %s run=%d verdict=%d collected=%d finalized=%d mutations=%d nodes=%d bad=%d",
					arm.name, i+1, r.verdict, r.collected, r.finalized, r.mutations, r.count, r.badNode)
				if r.verdict == 1 {
					verified++
				}
				// **Asserted on EVERY run of BOTH arms**: the guest must have reached its own subject. A
				// negative arm that "fails" because no collection ran, or because the mutator never
				// started, would pass this test while witnessing nothing.
				if r.collected != c3WantCollected {
					t.Errorf("run %d: %d collections, want %d — the arm did not reach its own subject",
						i+1, r.collected, c3WantCollected)
				}
				if r.mutations != c3WantMutations {
					t.Errorf("run %d: %d mutations, want %d — the mutator did not finish, so the "+
						"collector never raced it", i+1, r.mutations, c3WantMutations)
				}
			}
			if ran != arm.n {
				t.Fatalf("%d of %d runs executed", ran, arm.n)
			}
			if died > 0 {
				t.Logf("CLAUSE3 %s: %d of %d runs died before verifying — a discriminating outcome, but "+
					"NOT harness-protocol.md's registered one (\"checksum mismatch, wrong count, or a "+
					"finalizer firing\"). Recorded as a deviation rather than counted as the reading.",
					arm.name, died, arm.n)
			}
			switch {
			case arm.wantPass && verified != arm.n:
				t.Errorf("verified %d of %d runs, want all — %s", verified, arm.n, arm.why)
			case !arm.wantPass && verified != 0:
				t.Errorf("verified %d of %d runs, want NONE — %s. A negative arm that passes means the "+
					"witness cannot tell a stopped world from a running one, so the positive arm's green "+
					"asserts nothing", verified, arm.n, arm.why)
			}
		})
	}
}

// runClause3Arm runs one guest once and reads its verdict block out of the zero page.
func runClause3Arm(t *testing.T, file string) (c3Result, error) {
	t.Helper()
	var zero c3Result
	img, rerr := os.ReadFile(filepath.Join("testdata", "clause3", file))
	if rerr != nil {
		return zero, fmt.Errorf("read guest: %w", rerr)
	}
	feats := GuestFeatures()
	feats.Threads = true

	var out strings.Builder
	cfg := Config{
		Wasm: img, Args: []string{"clause3"}, Stdin: strings.NewReader(""),
		Stdout: &out, Stderr: &out, Features: &feats,
	}
	m, derr := decodeGuest(cfg)
	if derr != nil {
		return zero, fmt.Errorf("decode: %w", derr)
	}
	h := newHost(cfg)
	if ferr := h.initFDs(nil); ferr != nil {
		return zero, fmt.Errorf("initFDs: %w", ferr)
	}

	var mstartIdx uint32
	var found bool
	for i := range m.Exports {
		if m.Exports[i].Name == "mstart" && m.Exports[i].Kind == bin.ExternFunc {
			mstartIdx, found = m.Exports[i].Index, true
		}
	}
	if !found {
		return zero, errNoMstart
	}
	spawnFT, ok := clause2ImportType(m, "burroughs", "spawn")
	if !ok {
		return zero, errNoSpawnImport
	}

	var in *interp.Instance
	var spawns atomic.Int32
	// The verdict block is read through a RETAINED Caller rather than inside a host call, because this
	// guest need not make one at the moment the verdict is written. `Caller.Read` documents itself as
	// usable from a goroutine in no world at all, which is exactly this use.
	var keep atomic.Pointer[interp.Caller]
	hostImports := h.imports()
	imp := func(mod, name string) (interp.Extern, bool) {
		if mod == "burroughs" && name == "spawn" {
			return interp.HostExtern(spawnFT,
				func(c *interp.Caller, v []interp.Value) ([]interp.Value, error) {
					spawns.Add(1)
					keep.CompareAndSwap(nil, c)
					tid, e := in.Spawn(mstartIdx, v[0].Int32(), 0)
					if len(spawnFT.Results) == 0 {
						return nil, e
					}
					return []interp.Value{interp.I32(int32(tid))}, e
				}), true
		}
		return hostImports(mod, name)
	}

	var trap *interp.Trap
	var err error
	in, trap, err = interp.InstantiateLinked(m, imp)
	if err != nil {
		return zero, fmt.Errorf("link: %w", err)
	}
	if trap != nil {
		return zero, fmt.Errorf("instantiate trapped: %w", trap)
	}
	defer func() { _ = in.Close() }()

	// **The Invoke error is CAPTURED, not discarded.** The first version of this dropped it, and when the
	// negative arm produced no verdict the harness could say only that — an exit with no located cause.
	done := make(chan struct{})
	var ierr error
	go func() { _, ierr = in.Invoke("_start"); close(done) }()
	select {
	case <-done:
	case <-time.After(300 * time.Second):
		return zero, errArmHung
	}

	// **Two agents is a precondition, not an outcome.** A run on one agent would pass the positive arm for
	// the wrong reason — one agent cannot produce the interleaving the witness is about.
	if spawns.Load() < 1 {
		return zero, errNoSecondAgent
	}
	c := keep.Load()
	if c == nil {
		return zero, errNoCaller
	}
	ld := func(a uint64) uint32 {
		b, e := c.Read(a, 4)
		if e != nil || len(b) < 4 {
			return 0
		}
		return binary.LittleEndian.Uint32(b)
	}
	r := c3Result{
		verdict:   ld(c3AddrVerdict),
		collected: ld(c3AddrCollected),
		finalized: ld(c3AddrFinalized),
		mutations: ld(c3AddrMutations),
		badNode:   ld(c3AddrBadNode),
		count:     ld(c3AddrCount),
	}
	if r.verdict == 0 {
		// Report the mechanism alongside the verdict channel: "no verdict" is a *state*, and the reason it
		// happened lives in the Invoke error and the guest's own output.
		// Both errors are wrapped, so `errors.Is` reaches either the sentinel or the engine's own trap —
		// the mechanism channel and the verdict channel stay separable.
		if ierr != nil {
			return r, fmt.Errorf("%w (guest output: %q): %w", errNoVerdict, tail(out.String(), 400), ierr)
		}
		return r, fmt.Errorf("%w (guest output: %q)", errNoVerdict, tail(out.String(), 400))
	}
	return r, nil
}

var (
	errNoSecondAgent = errors.New("no agent was spawned, so the run was single-agent and cannot witness " +
		"a collection across two of them")
	errNoCaller  = errors.New("no Caller was retained, so the verdict block could not be read")
	errNoVerdict = errors.New("the guest wrote no verdict: it did not reach its verification, so neither " +
		"a pass nor a failure is being reported")
)

// tail keeps the last n bytes of a guest's output, so a long trace does not bury its own last line.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
