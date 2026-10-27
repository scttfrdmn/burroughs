// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build hwlitmus

package interp

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// The hardware weak-memory reading (#1126; the chair's ruling of 2026-10-27, item 2, "settle it"). The design
// is on #1126 as decisions taken, and this file is its engine half: four litmus shapes, run on real hardware
// with persistent agents and a barrier before every trial, over the access kinds Burroughs makes SC and the
// ones it does not.
//
// **A reading, not a gate.** The build tag `hwlitmus` keeps it out of every normal, `-race` and `make strict`
// run, because what it measures is the machine. Run it as
//
//	go test -tags hwlitmus ./internal/interp/ -run TestHWLitmus -v                      # engine
//	go test -tags hwlitmus,burroughs_plainmem ./internal/interp/ -run TestHWLitmus -v   # engine neuter
//
// and read the table it logs. The engine build asserts its own promise (no SC-forbidden outcome, no torn or
// out-of-set value on the SC kinds) and every build asserts the wasm allowed set on the plain kinds, so a
// FAIL is a finding. The neuter build asserts only the allowed set: its forbidden count is the reading.
//
// # Why every trial gets fresh locations
//
// Trial i uses x[i] and y[i] and writes its reads to r_j[i]. Nothing is reset between trials, so the only
// synchronisation between them is the barrier, which is an `i32.atomic.rmw.add` and a spin on
// `i32.atomic.load`. Both reach memory through `atomicCell` (ADR 0051), which the neuter does not touch, so
// the neuter weakens the accesses under test and nothing that holds the harness together.

// hwValue is what every write stores: a byte pattern rather than 1, so that a read which tore shows it. A
// read of 0 is the old value, a read of hwValue the new one, a read whose every byte is 0x00 or 0x01 is torn,
// and any other byte is outside what any write put there.
const hwValue = 0x01010101

// hwKind is an access kind: how a shape's writes and reads reach memory.
type hwKind struct {
	name   string
	stride int  // bytes per location
	sc     bool // Burroughs promises sequential consistency for this kind (ADR 0054) or wasm does (atomics)
	store  func(addr string) string
	load   func(addr, dst string) string // reads addr and writes the result to dst
}

var hwKinds = []hwKind{
	{
		name: "i32 aligned (ADR 0054)", stride: 4, sc: true,
		store: func(a string) string { return fmt.Sprintf("(i32.store %s (i32.const %d))", a, hwValue) },
		load:  func(a, d string) string { return fmt.Sprintf("(i32.store %s (i32.load %s))", d, a) },
	},
	{
		name: "i32 atomic", stride: 4, sc: true,
		store: func(a string) string { return fmt.Sprintf("(i32.atomic.store %s (i32.const %d))", a, hwValue) },
		load:  func(a, d string) string { return fmt.Sprintf("(i32.store %s (i32.atomic.load %s))", d, a) },
	},
	{
		name: "i32 unaligned", stride: 8,
		store: func(a string) string {
			return fmt.Sprintf("(i32.store offset=1 align=1 %s (i32.const %d))", a, hwValue)
		},
		load: func(a, d string) string { return fmt.Sprintf("(i32.store %s (i32.load offset=1 align=1 %s))", d, a) },
	},
	{
		name: "v128 (ADR 0064)", stride: 16,
		store: func(a string) string {
			return fmt.Sprintf("(v128.store %s (v128.const i32x4 %d %d %d %d))", a, hwValue, hwValue, hwValue, hwValue)
		},
		load: func(a, d string) string { return fmt.Sprintf("(v128.store %s (v128.load %s))", d, a) },
	},
	{
		name: "bulk fill/copy", stride: 4,
		store: func(a string) string { return fmt.Sprintf("(memory.fill %s (i32.const 1) (i32.const 4))", a) },
		load:  func(a, d string) string { return fmt.Sprintf("(memory.copy %s %s (i32.const 4))", d, a) },
	},
}

// hwOp is one step of one agent: a write of location loc, or a read of loc into result slot reg.
type hwOp struct {
	write bool
	loc   int // 0 = x, 1 = y
	reg   int
}

// hwShape is a litmus shape: its agents' programs, its register count, and the one outcome sequential
// consistency forbids, as a predicate over the registers (each 0 for the old value, 1 for the new).
type hwShape struct {
	name      string
	agents    [][]hwOp
	regs      int
	forbidden func(r []int) bool
	forbids   string
}

func w(loc int) hwOp      { return hwOp{write: true, loc: loc} }
func r(loc, reg int) hwOp { return hwOp{loc: loc, reg: reg} }

var hwShapes = []hwShape{
	{
		name: "MP", agents: [][]hwOp{{w(0), w(1)}, {r(1, 0), r(0, 1)}}, regs: 2,
		forbidden: func(v []int) bool { return v[0] == 1 && v[1] == 0 }, forbids: "r1=1 r2=0",
	},
	{
		name: "SB", agents: [][]hwOp{{w(0), r(1, 0)}, {w(1), r(0, 1)}}, regs: 2,
		forbidden: func(v []int) bool { return v[0] == 0 && v[1] == 0 }, forbids: "r1=0 r2=0",
	},
	{
		name: "LB", agents: [][]hwOp{{r(0, 0), w(1)}, {r(1, 1), w(0)}}, regs: 2,
		forbidden: func(v []int) bool { return v[0] == 1 && v[1] == 1 }, forbids: "r1=1 r2=1",
	},
	{
		name: "IRIW", agents: [][]hwOp{{w(0)}, {w(1)}, {r(0, 0), r(1, 1)}, {r(1, 2), r(0, 3)}}, regs: 4,
		forbidden: func(v []int) bool { return v[0] == 1 && v[1] == 0 && v[2] == 1 && v[3] == 0 },
		forbids:   "r1=1 r2=0 r3=1 r4=0",
	},
}

// hwLayout places the regions: the barrier word at 0, then x, y and one result array per register, each
// batch*stride bytes and page-aligned.
type hwLayout struct {
	batch, stride  int
	x, y           int
	rbase, rstride int
	pages          int
}

func layoutFor(s hwShape, k hwKind, batch int) hwLayout {
	region := batch * k.stride
	region = (region + 0xffff) &^ 0xffff
	l := hwLayout{batch: batch, stride: k.stride, x: 0x10000}
	l.y = l.x + region
	l.rbase = l.y + region
	l.rstride = region
	l.pages = (l.rbase + s.regs*region) / 0x10000
	return l
}

func addrOf(base, stride int) string {
	return fmt.Sprintf("(i32.add (i32.const %d) (i32.mul (local.get $i) (i32.const %d)))", base, stride)
}

// hwModule renders one shape over one access kind as a module whose export "t<k>" is agent k's program,
// taking the trial count. Agents meet at the barrier word before each trial: each adds 1 and spins until the
// word reaches (i+1)*agents.
func hwModule(s hwShape, k hwKind, l hwLayout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(module (memory (export \"mem\") %d %d shared)\n", l.pages, l.pages)
	hwAgents(&b, s, k, l, func(ai int) string { return fmt.Sprintf("(export \"t%d\")", ai) })
	b.WriteString(")")
	return b.String()
}

// hwAgents writes one function per agent, each named $t<k> and carrying whatever head(k) returns (an export
// for the engine's module, nothing for wasmtime's, which dispatches to them).
func hwAgents(b *strings.Builder, s hwShape, k hwKind, l hwLayout, head func(int) string) {
	n := len(s.agents)
	for ai, ops := range s.agents {
		fmt.Fprintf(b, "(func $t%d %s (param $n i32) (local $i i32)\n", ai, head(ai))
		b.WriteString("(block $done (loop $trial\n")
		b.WriteString("(br_if $done (i32.ge_u (local.get $i) (local.get $n)))\n")
		b.WriteString("(drop (i32.atomic.rmw.add (i32.const 0) (i32.const 1)))\n")
		fmt.Fprintf(b, "(loop $spin (br_if $spin (i32.lt_u (i32.atomic.load (i32.const 0)) "+
			"(i32.mul (i32.add (local.get $i) (i32.const 1)) (i32.const %d)))))\n", n)
		for _, op := range ops {
			loc := addrOf(l.x, l.stride)
			if op.loc == 1 {
				loc = addrOf(l.y, l.stride)
			}
			if op.write {
				b.WriteString(k.store(loc) + "\n")
			} else {
				b.WriteString(k.load(loc, addrOf(l.rbase+op.reg*l.rstride, l.stride)) + "\n")
			}
		}
		b.WriteString("(local.set $i (i32.add (local.get $i) (i32.const 1)))\n(br $trial)))\n)\n")
	}
}

// hwWasmtimeModule is the same shape as a wasi-threads command for wasmtime's core threads (#1126, arm 5).
// `_start` spawns each agent through `wasi:thread-spawn`, waits on a done counter at byte 4, and writes the
// result arrays to stdout, where wasmtimeBatch scores them with the engine's own tally. The memory is imported,
// which wasi-threads requires, so every thread's instance shares it.
func hwWasmtimeModule(s hwShape, k hwKind, l hwLayout) string {
	var b strings.Builder
	n := len(s.agents)
	b.WriteString("(module\n")
	b.WriteString("(import \"wasi\" \"thread-spawn\" (func $spawn (param i32) (result i32)))\n")
	b.WriteString("(import \"wasi_snapshot_preview1\" \"fd_write\" " +
		"(func $fd_write (param i32 i32 i32 i32) (result i32)))\n")
	fmt.Fprintf(&b, "(import \"env\" \"memory\" (memory %d %d shared))\n", l.pages, l.pages)
	b.WriteString("(export \"memory\" (memory 0))\n") // WASI's fd_write reads through the export
	hwAgents(&b, s, k, l, func(int) string { return "" })
	b.WriteString("(func (export \"wasi_thread_start\") (param $tid i32) (param $arg i32)\n")
	for ai := 0; ai < n; ai++ {
		fmt.Fprintf(&b, "(if (i32.eq (local.get $arg) (i32.const %d)) (then (call $t%d (i32.const %d))))\n",
			ai, ai, l.batch)
	}
	b.WriteString("(drop (i32.atomic.rmw.add (i32.const 4) (i32.const 1))))\n")
	b.WriteString("(func (export \"_start\") (local $p i32) (local $left i32)\n")
	for ai := 0; ai < n; ai++ {
		fmt.Fprintf(&b, "(if (i32.lt_s (call $spawn (i32.const %d)) (i32.const 0)) (then unreachable))\n", ai)
	}
	fmt.Fprintf(&b, "(loop $w (br_if $w (i32.lt_u (i32.atomic.load (i32.const 4)) (i32.const %d))))\n", n)
	// Write [rbase, rbase+regs*rstride) to fd 1, looping on short writes. The iovec is at 8, the count at 16.
	fmt.Fprintf(&b, "(local.set $p (i32.const %d)) (local.set $left (i32.const %d))\n", l.rbase, s.regs*l.rstride)
	b.WriteString("(block $out (loop $more\n")
	b.WriteString("(br_if $out (i32.eqz (local.get $left)))\n")
	b.WriteString("(i32.store (i32.const 8) (local.get $p)) (i32.store (i32.const 12) (local.get $left))\n")
	b.WriteString("(if (call $fd_write (i32.const 1) (i32.const 8) (i32.const 1) (i32.const 16)) (then unreachable))\n")
	b.WriteString("(local.set $p (i32.add (local.get $p) (i32.load (i32.const 16))))\n")
	b.WriteString("(local.set $left (i32.sub (local.get $left) (i32.load (i32.const 16))))\n")
	b.WriteString("(br $more))))\n")
	b.WriteString(")")
	return b.String()
}

// hwTally is one cell of the table.
type hwTally struct {
	trials, forbidden, torn, outOfSet int
	outcomes                          map[string]int
}

func (h *hwTally) add(o hwTally) {
	h.trials += o.trials
	h.forbidden += o.forbidden
	h.torn += o.torn
	h.outOfSet += o.outOfSet
	if h.outcomes == nil {
		h.outcomes = map[string]int{}
	}
	for k, v := range o.outcomes {
		h.outcomes[k] += v
	}
}

// classify reads one location-sized result: 0 old, 1 new, 2 torn, 3 out of set.
func classify(bs []byte) int {
	allZero, allNew, inSet := true, true, true
	for _, c := range bs {
		switch c {
		case 0x00:
			allNew = false
		case 0x01:
			allZero = false
		default:
			inSet = false
		}
	}
	switch {
	case !inSet:
		return 3
	case allZero:
		return 0
	case allNew:
		return 1
	default:
		return 2
	}
}

// tallyRegs scores a batch from its result arrays. A torn or out-of-set register makes the trial's ordering
// unreadable, so such a trial counts there and not in the outcome histogram.
func tallyRegs(s hwShape, l hwLayout, width int, regs [][]byte) hwTally {
	t := hwTally{trials: l.batch, outcomes: map[string]int{}}
	v := make([]int, s.regs)
	for i := 0; i < l.batch; i++ {
		readable := true
		for j := range v {
			c := classify(regs[j][i*l.stride : i*l.stride+width])
			switch c {
			case 2:
				t.torn++
				readable = false
			case 3:
				t.outOfSet++
				readable = false
			}
			v[j] = c
		}
		if !readable {
			continue
		}
		key := fmt.Sprint(v)
		t.outcomes[key]++
		if s.forbidden(v) {
			t.forbidden++
		}
	}
	return t
}

func hwTrials(t *testing.T) int {
	if s := os.Getenv("BURROUGHS_HWLITMUS_TRIALS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			t.Fatalf("BURROUGHS_HWLITMUS_TRIALS=%q is not a positive count", s)
		}
		return n
	}
	return 1 << 20
}

// hwWidth is the bytes a kind's read writes to its result slot.
func hwWidth(k hwKind) int {
	if k.stride == 16 {
		return 16
	}
	return 4
}

// runEngineBatch runs one batch of one shape over one kind on a fresh instance, so every location starts 0.
func runEngineBatch(t *testing.T, s hwShape, k hwKind, l hwLayout) hwTally {
	in := hostLink(t, hwModule(s, k, l), bin.Features{Threads: true, SIMD: true}, hostImports(nil))
	defer func() { _ = in.Close() }()
	tids := make([]ThreadID, len(s.agents))
	for ai := range s.agents {
		tid, err := in.Spawn(exportedFuncIndex(t, in, fmt.Sprintf("t%d", ai)), int32(l.batch), 0)
		if err != nil {
			t.Fatalf("%s/%s: Spawn of agent %d: %v", s.name, k.name, ai, err)
		}
		tids[ai] = tid
	}
	for ai, tid := range tids {
		if err := in.Join(tid); err != nil {
			t.Fatalf("%s/%s: Join of agent %d: %v", s.name, k.name, ai, err)
		}
	}
	mem, ok := in.Export("mem")
	if !ok {
		t.Fatal("the shape module exports no memory")
	}
	regs := make([][]byte, s.regs)
	for j := range regs {
		bs, err := ReadBoundaryMemory(&mem, uint64(l.rbase+j*l.rstride), uint64(l.batch*l.stride))
		if err != nil {
			t.Fatalf("%s/%s: reading register %d: %v", s.name, k.name, j, err)
		}
		regs[j] = bs
	}
	return tallyRegs(s, l, hwWidth(k), regs)
}

func hwBatch(k hwKind) int { return 65536 * 4 / k.stride }

// TestHWLitmus is the engine arm, or the engine-neuter arm under `burroughs_plainmem` (hwArm says which).
func TestHWLitmus(t *testing.T) {
	trials := hwTrials(t)
	t.Logf("arm %q · %s/%s · GOMAXPROCS %d · %d trials per cell", hwArm, runtime.GOOS, runtime.GOARCH,
		runtime.GOMAXPROCS(0), trials)
	for _, k := range hwKinds {
		for _, s := range hwShapes {
			batch := hwBatch(k)
			var tot hwTally
			for tot.trials < trials {
				tot.add(runEngineBatch(t, s, k, layoutFor(s, k, batch)))
			}
			t.Logf("| %s | %-4s | %-22s | forbidden (%s) %d / %d | torn %d | out-of-set %d | %v |",
				hwArm, s.name, k.name, s.forbids, tot.forbidden, tot.trials, tot.torn, tot.outOfSet,
				tot.outcomes)
			if tot.outOfSet != 0 {
				t.Errorf("%s/%s: %d reads outside the wasm allowed set: a byte no write stored", s.name,
					k.name, tot.outOfSet)
			}
			if k.sc && hwPromiseHolds {
				if tot.forbidden != 0 {
					t.Errorf("%s/%s: %d SC-forbidden outcomes (%s) on an SC kind", s.name, k.name,
						tot.forbidden, s.forbids)
				}
				if tot.torn != 0 {
					t.Errorf("%s/%s: %d torn reads on an access ADR 0053 makes one word", s.name, k.name,
						tot.torn)
				}
			}
		}
	}
}

// TestHWLitmusNativeControl is the native plain-Go control: the same shapes over plain int32 slices with the
// same barrier, compiler and hardware together. It asks whether this harness on this machine can see a weak
// outcome at all, and it asserts nothing: a silent control makes a silent engine arm mean nothing here.
func TestHWLitmusNativeControl(t *testing.T) {
	trials := hwTrials(t)
	t.Logf("arm %q · %s/%s · GOMAXPROCS %d · %d trials per cell", "native plain Go", runtime.GOOS,
		runtime.GOARCH, runtime.GOMAXPROCS(0), trials)
	for _, s := range hwShapes {
		const batch = 65536
		var tot hwTally
		for tot.trials < trials {
			tot.add(nativeBatch(s, batch))
		}
		t.Logf("| native | %-4s | plain int32            | forbidden (%s) %d / %d | %v |", s.name, s.forbids,
			tot.forbidden, tot.trials, tot.outcomes)
	}
}

// nativeBatch is runEngineBatch's twin in Go. The plain accesses race by design; this file never builds
// under `-race` in any gate.
func nativeBatch(s hwShape, batch int) hwTally {
	locs := [2][]int32{make([]int32, batch), make([]int32, batch)}
	regs := make([][]int32, s.regs)
	for j := range regs {
		regs[j] = make([]int32, batch)
	}
	var counter atomic.Int32
	n := int32(len(s.agents))
	var wg sync.WaitGroup
	for _, ops := range s.agents {
		wg.Add(1)
		go func(ops []hwOp) {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			for i := 0; i < batch; i++ {
				counter.Add(1)
				for counter.Load() < int32(i+1)*n {
				}
				for _, op := range ops {
					if op.write {
						nativeStore(&locs[op.loc][i])
					} else {
						regs[op.reg][i] = nativeLoad(&locs[op.loc][i])
					}
				}
			}
		}(ops)
	}
	wg.Wait()
	as := make([][]byte, s.regs)
	for j := range as {
		as[j] = make([]byte, batch*4)
		for i, v := range regs[j] {
			binary.LittleEndian.PutUint32(as[j][i*4:], uint32(v))
		}
	}
	return tallyRegs(s, hwLayout{batch: batch, stride: 4}, 4, as)
}

//go:noinline
func nativeStore(p *int32) { *p = hwValue }

//go:noinline
func nativeLoad(p *int32) int32 { return *p }

// TestHWLitmusWasmtime is arm 5: the SC kinds' shapes on wasmtime's core threads (wasi-threads), as
// registration corroboration and never as the oracle (#1126). A forbidden outcome with atomic accesses would
// mean a mis-registered shape or a wasmtime defect; with plain aligned accesses wasm allows it, and a non-zero
// count there says the shape's question is live on this hardware. It asserts only the allowed set.
//
// **The binary is $BURROUGHS_WASMTIME, and it cannot be the oracle pin.** wasmtime removed wasi-threads in
// bytecodealliance/wasmtime#13558 (merged 2026-06-05, first absent from v46.0.0): 49.0.3 still lists
// `-S threads` in its help and refuses it with "the `-Sthreads` flag is no longer supported". #1126's recon
// assumed 49.0.3 could run core threads, and it cannot. The reading uses the v36 LTS line, which keeps them
// (v36.0.19, fetched by its release digest; the table records the version and digest per machine).
func TestHWLitmusWasmtime(t *testing.T) {
	bin := os.Getenv("BURROUGHS_WASMTIME")
	if bin == "" {
		t.Fatal("set BURROUGHS_WASMTIME to a wasmtime that still has wasi-threads (v45 or earlier; the reading " +
			"uses v36.0.19 LTS). The oracle pin, 49.0.3, refuses `-S threads`")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("no wasmtime at %s: %v", bin, err)
	}
	ver, _ := exec.Command(bin, "--version").Output()
	trials := hwTrials(t)
	t.Logf("arm %q · %s · %s/%s · %d trials per cell", "wasmtime core threads", strings.TrimSpace(string(ver)),
		runtime.GOOS, runtime.GOARCH, trials)
	dir := t.TempDir()
	for _, k := range hwKinds[:2] {
		for _, s := range hwShapes {
			l := layoutFor(s, k, hwBatch(k))
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.wat", s.name, k.stride))
			if err := os.WriteFile(path, []byte(hwWasmtimeModule(s, k, l)), 0o600); err != nil {
				t.Fatal(err)
			}
			var tot hwTally
			for tot.trials < trials {
				cmd := exec.Command(bin, "run", "-W", "threads=y", "-S", "threads=y", path)
				var stderr strings.Builder
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("%s/%s: wasmtime: %v\n%s", s.name, k.name, err, stderr.String())
				}
				if len(out) != s.regs*l.rstride {
					t.Fatalf("%s/%s: wasmtime wrote %d bytes, want %d", s.name, k.name, len(out), s.regs*l.rstride)
				}
				regs := make([][]byte, s.regs)
				for j := range regs {
					regs[j] = out[j*l.rstride : j*l.rstride+l.batch*l.stride]
				}
				tot.add(tallyRegs(s, l, hwWidth(k), regs))
			}
			t.Logf("| wasmtime | %-4s | %-22s | forbidden (%s) %d / %d | torn %d | out-of-set %d | %v |",
				s.name, k.name, s.forbids, tot.forbidden, tot.trials, tot.torn, tot.outOfSet, tot.outcomes)
			if tot.outOfSet != 0 {
				t.Errorf("%s/%s: %d reads outside the wasm allowed set on wasmtime", s.name, k.name, tot.outOfSet)
			}
		}
	}
}
