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

// Zero-page slots the clause-2 guest stores into. `[0,4096)` is what
// `cmd/link/internal/ld/data.go`'s `wasmMinDataAddr` documents as *"never accessed in correct
// execution"*, which is why the host may read it while the guest runs; `0x0F00..` is clear of the
// fork's trap marker (`0x10`), its `fd_write` byte count (`0x14`) and its gc phase mark (`0x18`).
const (
	c2AddrA     = 0x0F00 // sibling A's counter
	c2AddrB     = 0x0F04 // sibling B's counter — THE DISCRIMINATOR
	c2AddrPhase = 0x0F08 // 1 = about to block in fd_read, 2 = returned from it
	c2AddrSink  = 0x14   // the fork's fd_write byte count, asserted 0 so the overlap below is checked
)

// c2Hold is how long the host keeps the guest's `fd_read` in flight. The registered reading samples
// sibling B's counter "during the 400 ms hold", so the number is the registration's, not a tuning knob.
const c2Hold = 400 * time.Millisecond

// c2Reading is one arm's registered outcome, from phase4/clause2-recon.md's discriminating pair:
//
//	with the notification    sibling B advanced, 10 of 10
//	without it (stubbed)     0 -> 0, delta 0, every run, n=3
type c2Reading struct {
	name    string
	file    string
	n       int
	wantAdv bool
	why     string
}

// TestClause2BlockedGoroutinesPIsHandedOff is Phase 4 **clause 2**'s witness, reconstructed and committed.
//
// # Why this file exists at all
//
// Clause 2's original harness was a scratch test that was never committed, so the gate *"re-run clauses
// 1–3's witnesses"* could not be run: the witness was gone. That is the sibling of a lesson this campaign
// already paid for — **a repair in a disposable artifact dies with it, and so does a witness.** A witness
// that is not committed is not a witness.
//
// # What it shows
//
// A goroutine blocked in `fd_read` must not keep its P. Two **call-free** spinners race: with
// `GOMAXPROCS=2` the blocked goroutine holds one P and sibling A takes the other, so **sibling B can only
// run if the blocked goroutine's P is handed off**. Sibling A advances in both arms — that is observation
// 1, which never depended on the handoff — and **sibling B is the discriminator**.
//
// # The two arms are cross-binary, and that is stated rather than implied
//
// The negative arm is a *different binary*: the same guest source built against a fork whose
// `enterBlockingSyscall`/`exitBlockingSyscall` are stubbed to no-ops. It cannot be a build flag, because
// the mechanism is a build-tagged file pair keyed on `goexperiment.burroughsspawn` and dropping that
// experiment would remove the spawn door too — the arms would then differ in two variables.
//
// Verified **in the emitted artifact** rather than in the build command (standing property 13): the
// positive arm's `syscall.Read` body contains two calls to the mechanism and the negative arm's contains
// none. The provenance of both binaries is in `testdata/clause2/PROVENANCE`.
//
// # The resched pass is pinned OFF in both, and the reason is slice 5
//
// Slice 5 gave this target loop preemption, so a call-free spinner *can* yield its M. That would let one
// agent serve both siblings and the comparison would stop isolating the handoff. Both arms pin
// `-d=ssa/insert_resched_checks/off`, so the arms differ only by the mechanism.
func TestClause2BlockedGoroutinesPIsHandedOff(t *testing.T) {
	readings := []c2Reading{{
		name: "mechanism_present", file: "c2_present.wasm", n: 10, wantAdv: true,
		why: "the blocked goroutine's P is handed off, so sibling B gets one and advances",
	}, {
		name: "mechanism_absent", file: "c2_absent.wasm", n: 3, wantAdv: false,
		why: "no handoff, so there is no P for sibling B and its counter never moves",
	}}

	for _, r := range readings {
		t.Run(r.name, func(t *testing.T) {
			var advanced, ran int
			for i := range r.n {
				dA, dB, err := runClause2Arm(t, r.file)
				if err != nil {
					t.Fatalf("run %d: %v", i+1, err)
				}
				ran++
				// **Observation 1, asserted every run in BOTH arms.** If sibling A did not advance, the
				// guest never reached its own subject and sibling B's zero would be vacuous rather than
				// discriminating — the arm would "pass" the negative reading by not running at all.
				if dA == 0 {
					t.Errorf("run %d: sibling A did not advance; the blocking call did not overlap any "+
						"guest execution, so this run says nothing about the handoff", i+1)
				}
				if dB > 0 {
					advanced++
				}
				t.Logf("CLAUSE2 %s run=%d deltaA=%d deltaB=%d", r.name, i+1, dA, dB)
			}
			if ran != r.n {
				t.Fatalf("%d of %d runs executed", ran, r.n)
			}
			// The registered reading is a RATE, so it is asserted as one: all of them or none of them.
			switch {
			case r.wantAdv && advanced != r.n:
				t.Errorf("sibling B advanced in %d of %d runs, want %d of %d — %s",
					advanced, r.n, r.n, r.n, r.why)
			case !r.wantAdv && advanced != 0:
				t.Errorf("sibling B advanced in %d of %d runs, want 0 — %s", advanced, r.n, r.why)
			}
		})
	}
}

// runClause2Arm runs one guest once and returns sibling A's and sibling B's counter deltas across the
// host's hold of `fd_read`.
func runClause2Arm(t *testing.T, file string) (deltaA, deltaB uint32, err error) {
	t.Helper()
	img, rerr := os.ReadFile(filepath.Join("testdata", "clause2", file))
	if rerr != nil {
		return 0, 0, fmt.Errorf("read guest: %w", rerr)
	}
	feats := GuestFeatures()
	// ADR 0088's occasion, one path over: a `burroughsspawn` guest has a shared memory and atomics, so
	// `gate:threads` IS load-bearing for it and `GuestFeatures()`'s default would refuse to decode it.
	feats.Threads = true

	var out strings.Builder
	cfg := Config{
		Wasm: img, Args: []string{"clause2"}, Stdin: strings.NewReader("x"),
		Stdout: &out, Stderr: &out, Features: &feats,
	}
	m, derr := decodeGuest(cfg)
	if derr != nil {
		return 0, 0, fmt.Errorf("decode: %w", derr)
	}
	h := newHost(cfg)
	if ferr := h.initFDs(nil); ferr != nil {
		return 0, 0, fmt.Errorf("initFDs: %w", ferr)
	}

	var mstartIdx uint32
	var found bool
	for i := range m.Exports {
		if m.Exports[i].Name == "mstart" && m.Exports[i].Kind == bin.ExternFunc {
			mstartIdx, found = m.Exports[i].Index, true
		}
	}
	if !found {
		return 0, 0, errNoMstart
	}
	// **The spawn import's type is DERIVED from the module, not written here.** Writing it is how this
	// session already lost time twice: a guessed arity links against nothing and the linker's refusal is
	// the only thing that says so. The guest declares `(i32) -> ()`; a future guest that declares
	// something else will still link.
	spawnFT, ok := clause2ImportType(m, "burroughs", "spawn")
	if !ok {
		return 0, 0, errNoSpawnImport
	}

	var in *interp.Instance
	var reads atomic.Int32
	var gotA, gotB atomic.Uint32
	var sinkBytes atomic.Uint32
	hostImports := h.imports()

	imp := func(mod, name string) (interp.Extern, bool) {
		if mod == "burroughs" && name == "spawn" {
			return interp.HostExtern(spawnFT,
				func(_ *interp.Caller, v []interp.Value) ([]interp.Value, error) {
					tid, e := in.Spawn(mstartIdx, v[0].Int32(), 0)
					if len(spawnFT.Results) == 0 {
						return nil, e
					}
					return []interp.Value{interp.I32(int32(tid))}, e
				}), true
		}
		if mod == module && name == "fd_read" {
			// **The hold and the measurement are in the SAME host function, on the blocking goroutine.**
			// `Caller.Read` is documented as usable from a goroutine in no world at all, so sampling here
			// needs no retained caller and no second thread — and it samples exactly the interval the
			// registration names.
			return interp.HostExtern(
				bin.FuncType{
					Params:  []bin.ValType{bin.I32, bin.I32, bin.I32, bin.I32},
					Results: []bin.ValType{bin.I32},
				},
				func(c *interp.Caller, v []interp.Value) ([]interp.Value, error) {
					if reads.Add(1) != 1 {
						// Later reads report EOF: one hold per run keeps the interval unambiguous.
						return clause2SetNread(c, u32(v, 3), 0)
					}
					a0, b0 := clause2Load(c, c2AddrA), clause2Load(c, c2AddrB)
					time.Sleep(c2Hold)
					a1, b1 := clause2Load(c, c2AddrA), clause2Load(c, c2AddrB)
					gotA.Store(a1 - a0)
					gotB.Store(b1 - b0)
					sinkBytes.Store(clause2Load(c, c2AddrSink))
					return clause2SetNread(c, u32(v, 3), 0)
				}), true
		}
		return hostImports(mod, name)
	}

	var trap *interp.Trap
	in, trap, err = interp.InstantiateLinked(m, imp)
	if err != nil {
		return 0, 0, fmt.Errorf("link: %w", err)
	}
	if trap != nil {
		return 0, 0, fmt.Errorf("instantiate trapped: %w", trap)
	}
	defer func() { _ = in.Close() }()

	done := make(chan struct{})
	go func() { _, _ = in.Invoke("_start"); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second * raceSlowdown):
		// The guest's spin bound is finite, so a hang here is an engine fault rather than the guest
		// running long — and the negative arm must terminate with a verdict (standing property 4).
		return 0, 0, errArmHung
	}

	if reads.Load() == 0 {
		return 0, 0, errNoRead
	}
	// **The zero-page overlap is CHECKED, not assumed.** `0x0F00..` sits inside the fork's `fd_write`
	// diagnostic sink (`0x20..0x0FFF`); it is safe only while that sink is unused, and `0x14` is the sink's
	// own byte count. A non-zero here means the guest wrote diagnostics and the counters may be its bytes.
	if n := sinkBytes.Load(); n != 0 {
		return 0, 0, fmt.Errorf("fd_write sink recorded %d bytes, so the counter slots may hold "+
			"diagnostic bytes rather than counters", n)
	}
	return gotA.Load(), gotB.Load(), nil
}

// clause2Load reads a little-endian u32 from guest memory, or 0 if it cannot be read — a failure to read
// is reported as no progress, which is the conservative direction for the positive arm and cannot manufacture
// the negative one.
func clause2Load(c *interp.Caller, addr uint64) uint32 {
	b, err := c.Read(addr, 4)
	if err != nil || len(b) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

// clause2SetNread writes `n` to the guest's `nread` out-param and answers ESUCCESS.
func clause2SetNread(c *interp.Caller, ptr, n uint32) ([]interp.Value, error) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	// A failed write becomes an ERRNO, not a Go error: a Go error from a host function is a trap the guest
	// cannot catch, and a short write is something preview 1 spells in its own return value.
	if werr := c.Write(uint64(ptr), b[:]); werr != nil {
		//nolint:nilerr // the errno IS the channel here; returning werr would trap the guest instead
		return ret(errIO), nil
	}
	return ret(errSuccess), nil
}

// Sentinel errors, so a run that proves nothing is distinguishable from one that proves the wrong thing.
var (
	errNoMstart = errors.New("guest exports no `mstart`, so it cannot spawn and the arm proves nothing")
	// **Not attributed to the engine**, which is what the first version of this sentence did. The guest's
	// own spin bound can hang the POSITIVE arm: with the resched pass off, a handed-off P leaves both Ps
	// held by unpreemptible spinners and `main` cannot return from `exitsyscall`. So a hang here is first a
	// question about the guest's bound and only then about the engine.
	errArmHung = errors.New("no return from Invoke within the bound — check the guest's spin bound before " +
		"the engine: an unpreemptible spinner can hold the P `main` needs back")
	errNoRead = errors.New("the guest never called fd_read, so no hold happened and both counters are vacuous")
)

// clause2ImportType answers a named function import's declared type, so a host stub's signature comes from
// the guest rather than from the harness author's memory.
func clause2ImportType(m *bin.Module, mod, name string) (bin.FuncType, bool) {
	for i := range m.Imports {
		im := &m.Imports[i]
		if im.Module == mod && im.Name == name && im.Kind == bin.ExternFunc {
			if int(im.Index) < len(m.Types) && m.Types[im.Index].Kind == bin.CompFunc {
				return m.Types[im.Index].Func, true
			}
		}
	}
	return bin.FuncType{}, false
}

var errNoSpawnImport = errors.New(`guest does not import "burroughs" "spawn", so the door is absent`)
