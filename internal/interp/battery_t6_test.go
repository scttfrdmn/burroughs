// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// A new contract §2 clause — a new T-6 ([ADR 0089]): a module-defined non-shared global is per-agent. These cases are the
// forecast pre-registered on #807, checked as a set.
//
// The occasion was Phase 4 slice 2: Go's wasm backend keeps its whole register bank in mutable globals
// (`regVars`: SP->0, CTXT->1, g->2, RET0..3->3-6, PAUSE->7), so a shared global space meant a second M
// erased the first's stack pointer. What follows is the engine-level property that fixes it, witnessed
// on small bytes rather than on a 1.8MB toolchain artifact.
//
// [ADR 0089]: ../../docs/decisions/0089-non-shared-globals-are-per-agent-because-a-guests-per-thread-state-is-its-whole-global-set-and-t-4-sized-it-at-one.md

// t6Module is the witness shape: the parent writes a global, spawns, waits on an atomic rendezvous in
// the SHARED MEMORY, then reads the global back. Memory is how the two agents agree they have both run;
// the global is the thing under test. A test that used the global itself to synchronise could not tell
// per-agent storage from a missed write.
const t6Module = `(module
	(import "h" "spawn" (func $spawn))
	(memory 1 1 shared)
	(global $g (mut i32) (i32.const 7))
	(func (export "child") (param i32)
		(global.set $g (i32.const 222))
		(i32.atomic.store (i32.const 0) (i32.const 1)))
	(func (export "parent") (result i32)
		(global.set $g (i32.const 111))
		(call $spawn)
		(loop $w
			(br_if $w (i32.ne (i32.atomic.load (i32.const 0)) (i32.const 1))))
		(global.get $g))
	(func (export "childReport") (param i32)
		(i32.atomic.store (i32.const 4) (global.get $g))
		(i32.atomic.store (i32.const 0) (i32.const 1)))
	(func (export "fresh") (result i32)
		(global.set $g (i32.const 99))
		(call $spawn)
		(loop $w
			(br_if $w (i32.ne (i32.atomic.load (i32.const 0)) (i32.const 1))))
		(i32.atomic.load (i32.const 4))))`

// spawnHarness links t6Module with a host `spawn` that starts `childName` as an agent, and returns a
// runner for an export. The host import is how a spawn happens at all from inside guest code — there is
// no guest-reachable spawn (D-1), which is why the witness lives in the engine's own harness.
func spawnHarness(t *testing.T, childName string) (*Instance, func(string) int32) {
	t.Helper()
	var spawned func()
	spawn := CanonLowerExtern(
		ft(nil, nil),
		func(_ *CanonCaller, _ []Value) ([]Value, error) { spawned(); return nil, nil },
		CanonOptions{},
	)
	in := hostLink(t, t6Module, bin.Features{Threads: true},
		hostImports(map[string]Extern{"spawn": spawn}))
	closeAtEnd(t, in)

	child := exportedFuncIndex(t, in, childName)
	spawned = func() {
		if _, err := in.Spawn(child, 0, 0); err != nil {
			t.Errorf("Spawn: %v", err)
		}
	}
	return in, func(export string) int32 {
		t.Helper()
		type res struct {
			v   []Value
			err error
		}
		ch := make(chan res, 1)
		go func() {
			v, err := in.Invoke(export)
			ch <- res{v, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s: %v", export, r.err)
			}
			return r.v[0].Int32()
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never completed — the rendezvous did not close, so no agent ran the child", export)
			return 0
		}
	}
}

// TestASpawnedAgentsWriteToADefinedGlobalIsNotObservableByItsSpawner is a new T-6's own case, and the
// forecast's item 1. Watched die: with `threadGlobals` returning `in.globals` (the pre-0089 sharing)
// it reports `parent read 222, want 111`. Before ADR 0089 this read **222** — the child's write, through the instance's
// shared `globals` slice, which is exactly how a second Go M erased the first's SP. It must read 111.
//
// **The 222 arm is not dropped, it is asserted as a distance**: the failure this guards against is a
// spawn that never ran the child, which would also leave 111 in place and pass a bare `== 111` check.
// So the child's own execution is witnessed through the memory rendezvous the parent waits on, and the
// value it wrote is asserted to be *readable by the child and absent from the parent*.
func TestASpawnedAgentsWriteToADefinedGlobalIsNotObservableByItsSpawner(t *testing.T) {
	_, run := spawnHarness(t, "child")
	if got := run("parent"); got != 111 {
		t.Errorf("parent read %d, want 111 — a spawned agent's write to a defined global reached its "+
			"spawner (222 is the pre-0089 reading, and the exact mechanism that corrupts a second M's SP)", got)
	}
}

// TestASpawnedAgentsDefinedGlobalIsFreshlyInitializedNotInherited is the forecast's item 3, and it is
// what makes "per-agent" mean something narrower than "a copy": the child's global holds its
// INITIALIZER's value (7), not whatever the spawner last wrote (99). Copy-on-spawn and fresh-init are
// both "per-agent" and no shape argument separates them; this does. Watched die twice — under the
// pre-0089 sharing AND under an injected copy-on-spawn that stores the spawner's current value into the
// fresh cell — and the second is the one that matters, since only it distinguishes the two per-agent
// designs from each other.
func TestASpawnedAgentsDefinedGlobalIsFreshlyInitializedNotInherited(t *testing.T) {
	_, run := spawnHarness(t, "childReport")
	if got := run("fresh"); got != 7 {
		t.Errorf("the child read %d from its own copy, want 7 (the initializer). 99 would mean the "+
			"spawner's value was inherited, which is copy-on-spawn rather than fresh initialization", got)
	}
}

// TestAnImportedGlobalStaysSharedAcrossAgents is the forecast's item 2 — a new T-6's exclusion, witnessed
// rather than stated.
//
// **Its first injection was a no-op, and that near-miss is why the injection is recorded here rather
// than reported as "watched die".** Setting `off := 0` in `threadGlobals` — "treat imports as
// definitions" — left this test PASSING, and not because the test is strong: this module imports its
// global and defines none, so `in.mod.Globals` is empty, the loop body never runs, and `off` cannot
// matter. An injection that cannot reach the code under test says nothing about the test, and it reads
// exactly like a control that survived. The injection that DOES reach it copies the imported slots
// explicitly, and under that one this test fails with `parent read 111 ... want 222`. An import names a cell the EXPORTING instance owns, so a per-agent copy of it
// would answer a different question than the module asked, and a child's write to it MUST reach the
// parent. This is the one arm where the pre-0089 answer is also the correct answer, which is why it
// needs its own case: a mechanism that copied every slot would pass every other test here.
func TestAnImportedGlobalStaysSharedAcrossAgents(t *testing.T) {
	owner := hostLink(t, `(module (global (export "g") (mut i32) (i32.const 0)))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{}))
	closeAtEnd(t, owner)
	gExt, ok := owner.Export("g")
	if !ok {
		t.Fatal("owner does not export g")
	}

	var spawned func()
	spawn := CanonLowerExtern(ft(nil, nil),
		func(_ *CanonCaller, _ []Value) ([]Value, error) { spawned(); return nil, nil },
		CanonOptions{})
	in := hostLink(t, `(module
		(import "h" "spawn" (func $spawn))
		(import "h" "g" (global $g (mut i32)))
		(memory 1 1 shared)
		(func (export "child") (param i32)
			(global.set $g (i32.const 222))
			(i32.atomic.store (i32.const 0) (i32.const 1)))
		(func (export "parent") (result i32)
			(global.set $g (i32.const 111))
			(call $spawn)
			(loop $w
				(br_if $w (i32.ne (i32.atomic.load (i32.const 0)) (i32.const 1))))
			(global.get $g)))`,
		bin.Features{Threads: true},
		hostImports(map[string]Extern{"spawn": spawn, "g": gExt}))
	closeAtEnd(t, in)

	child := exportedFuncIndex(t, in, "child")
	spawned = func() {
		if _, err := in.Spawn(child, 0, 0); err != nil {
			t.Errorf("Spawn: %v", err)
		}
	}
	type res struct {
		v   []Value
		err error
	}
	ch := make(chan res, 1)
	go func() { v, err := in.Invoke("parent"); ch <- res{v, err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("parent: %v", r.err)
		}
		if got := r.v[0].Int32(); got != 222 {
			t.Errorf("parent read %d from an IMPORTED global, want 222 — a new T-6 excludes imports, so the "+
				"child's write must reach the spawner; a per-agent copy of an import would answer a "+
				"different question than the module asked", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("parent never completed")
	}
}

// TestTheHostThreadsGlobalsAreTheInstancesOwn is the mechanism's load-bearing aliasing, asserted through
// behaviour the public boundary can see rather than through the field. If the host thread held a COPY,
// instantiation would initialize one slice and every host `global.get` would read another — so this
// asserts that a host-side write is visible to the public `Global` accessor and vice versa.
//
// It is also why the board forecast for #807 was "unchanged" rather than hopeful: every single-threaded
// path reads one backing array, as before.
func TestTheHostThreadsGlobalsAreTheInstancesOwn(t *testing.T) {
	in := hostLink(t, `(module
		(global $g (export "g") (mut i32) (i32.const 7))
		(func (export "bump") (global.set $g (i32.const 42))))`,
		bin.Features{}, hostImports(map[string]Extern{}))
	closeAtEnd(t, in)

	v, err := in.Global("g")
	if err != nil {
		t.Fatalf("Global before: %v", err)
	}
	if v.Int32() != 7 {
		t.Fatalf("Global(g) = %d before any write, want 7 (the initializer)", v.Int32())
	}
	if _, ierr := in.Invoke("bump"); ierr != nil {
		t.Fatalf("bump: %v", ierr)
	}
	v, err = in.Global("g")
	if err != nil {
		t.Fatalf("Global after: %v", err)
	}
	if v.Int32() != 42 {
		t.Errorf("Global(g) = %d after a guest write on the host thread, want 42. A divergence here "+
			"means the host thread holds a COPY rather than the instance's own slice, which would split "+
			"instantiation's view from the accessor's", v.Int32())
	}
}

// TestASharedGlobalIsRefusedByName is the forecast's item 4, and it is what makes a new T-6's scope
// CHECKABLE rather than asserted. A new T-6 is written over the *non-shared* category so it stays correct
// when the shared-everything-threads encoding is accepted; the claim that the category is TOTAL today
// rests on the decoder refusing that encoding, and a scope claim resting on an unwitnessed refusal is
// the #732 shape one level up.
//
// The encoding: a globaltype's mutability byte is a bitfield in that proposal, with bit 1 = shared, so
// `0x02` (shared, immutable) and `0x03` (shared, mutable) are the bytes to refuse. `decodeMutability`
// admits only 0x00 and 0x01.
func TestASharedGlobalIsRefusedByName(t *testing.T) {
	for _, mut := range []byte{0x02, 0x03} {
		// A minimal module with one global section entry: i32 (0x7f), the mutability byte, `i32.const 0`
		// `end`, hand-built so the refusal fires on bytes rather than on a constructed struct.
		img := []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
			0x06, 0x06, 0x01, 0x7f, mut, 0x41, 0x00, 0x0b, // global section: 1 entry
		}
		_, err := (&bin.Decoder{Features: bin.Features{Threads: true}}).DecodeModule(img)
		if err == nil {
			t.Fatalf("mutability byte %#02x: a SHARED global decoded. A new T-6's \"today that category is "+
				"total\" would be false, and a shared global reaching per-agent storage would be "+
				"copied when the format says it is shared", mut)
		}
		if !errors.Is(err, bin.ErrMalformedMutability) {
			t.Errorf("mutability byte %#02x: refused by %v, want ErrMalformedMutability — refuse by "+
				"name, so a reader learns which byte was wrong", mut, err)
		}
	}
	// Non-vacuity: the identical module with an ADMITTED mutability byte must decode, or the loop above
	// would pass for any reason at all (a malformed header, a wrong section id).
	for _, mut := range []byte{0x00, 0x01} {
		img := []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x06, 0x06, 0x01, 0x7f, mut, 0x41, 0x00, 0x0b,
		}
		if _, err := (&bin.Decoder{Features: bin.Features{Threads: true}}).DecodeModule(img); err != nil {
			t.Fatalf("mutability byte %#02x must decode, or the refusals above are vacuous: %v", mut, err)
		}
	}
}
