// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestGrowPublishesAFreshImageRatherThanMutatingTheHeldOne is decision 0058's own witness, and it is
// written against the regression that decision makes available rather than against the defect it fixes.
//
// **The defect 0058 fixes has no single-threaded witness, and pretending otherwise would be the
// vacuity.** Before 0058 `grow` assigned `m.bytes = grown`; a reader that had already copied the slice
// header into a local still held a valid pointer and length, because a Go slice is a *value*. So a
// single-threaded test that took `bs := m.view()`, grew the memory, and read `bs` back would pass
// identically on both engines — an analytic zero, an assertion that could not have come out any other
// way. The memory-safety property is about a header observed *mid-write*, which needs two threads and
// the race detector: that is the arm below.
//
// **What this asserts instead is the one regression 0058 creates**, which is a live shape rather than a
// hypothetical: an arm added later that writes `m.img.Load().bytes = grown` — mutating the descriptor a
// reader may already hold instead of publishing a new one. That compiles, passes every conformance
// vector, and silently restores the exact hazard, because the three words a reader is dereferencing are
// being written underneath it again. So the property here is *immutability of a published descriptor*:
// the `*memImage` a reader held before the grow names the same array at the same length afterwards.
//
// Both arms of `grow` are asked, because they publish for different reasons — the reslice arm because
// the pointer is unchanged and only the length rises, the reallocating arm because the array itself is
// replaced — and an in-place mutation of either is the same defect.
//
// Watched die: replacing the reslice arm's `m.img.Store(&memImage{bytes: cur[:n]})` with
// `m.img.Load().bytes = cur[:n]` fails at the length assertion for the reserved memory; the same
// mutation on the reallocating arm fails at the pointer assertion for the unshared one. Reverting the
// whole mechanism to `m.bytes = …` does not compile, which is a weaker but real signal.
func TestGrowPublishesAFreshImageRatherThanMutatingTheHeldOne(t *testing.T) {
	// **The relocating arm lives on decision 0076's fallback path, so this test runs there.** A memory
	// reserved through an anonymous mapping is reserved to its ceiling and never relocates, which is
	// what M-1 buys; the arm this test is about is still real on the `!unix` ports and on any host that
	// refuses a mapping, and it is still the arm ADR 0058's publication exists for. The `cap == len`
	// guard below is what said so, unprompted, when the mapping landed.
	withoutReservation(t)

	build := func(lim binary.Limits) *memory {
		m, err := newMemory(binary.Memory{Limits: lim})
		if err != nil {
			t.Fatalf("newMemory(%+v): %v", lim, err)
		}
		return m
	}

	// The reserved memory takes the reslice arm: same array, greater length.
	reserved := build(binary.Limits{Min: 1, Max: 4, HasMax: true, Shared: true})
	held := reserved.img.Load()
	heldLen := len(held.bytes)
	if got := reserved.grow(2, nil); got != 1 {
		t.Fatalf("reserved grow(2) = %d, want the previous size 1", got)
	}
	if reserved.img.Load() == held {
		t.Errorf("the reslice arm published no new descriptor: `img` still holds %p after a grow "+
			"that reported success, so either the grow did nothing or the length was written into "+
			"the descriptor a reader may already be dereferencing", held)
	}
	if len(held.bytes) != heldLen {
		t.Errorf("the descriptor held across the grow changed length, %d to %d.\n"+
			"A published `memImage` is immutable: that is the whole of decision 0058, because a "+
			"reader holding this descriptor is dereferencing these three words, and rewriting them "+
			"is the torn header the atomic pointer exists to prevent. The arm must build a new "+
			"`memImage` and `Store` it, never assign through `img.Load()`",
			heldLen, len(held.bytes))
	}
	if reserved.size() != 3 {
		t.Errorf("size() = %d after growing 1 to 3 pages, so the new descriptor is not the one the "+
			"memory answers from", reserved.size())
	}

	// The unshared memory at exactly its capacity takes the reallocating arm.
	unshared := build(binary.Limits{Min: 1})
	heldU := unshared.img.Load()
	// **A hard failure rather than a skip, because this is the fixture losing its discriminating
	// power rather than the engine misbehaving.** `allocate` reserves nothing for an unshared memory,
	// and a one-page `make([]byte, 65536)` is a large object the allocator serves in exact pages, so
	// `cap == len` and the grow below must relocate. If that ever stops holding, this arm silently
	// tests the reslice path twice — and *a skip is not a verdict*: a green earned by declining to ask
	// is what this message exists to prevent.
	if cap(heldU.bytes) != len(heldU.bytes) {
		t.Fatalf("the allocator gave a %d-byte memory %d bytes of capacity, so grow(1) below will "+
			"reslice instead of relocating and this arm asserts nothing about relocation. Rebuild "+
			"the fixture so the relocating arm is reached — do not delete the arm",
			len(heldU.bytes), cap(heldU.bytes))
	}
	basedU := &heldU.bytes[0]
	if got := unshared.grow(1, nil); got != 1 {
		t.Fatalf("unshared grow(1) = %d, want 1", got)
	}
	if &heldU.bytes[0] != basedU {
		t.Errorf("the descriptor held across a relocating grow now names a different array.\n" +
			"The abandoned array must stay named by the old descriptor for as long as any reader " +
			"holds it — that is what keeps it alive and in bounds, and it is why relocation is " +
			"memory-safe under decision 0058 where it was a use-after-free before")
	}
	if len(heldU.bytes) != pageSize {
		t.Errorf("the held descriptor's length is %d, want the pre-grow %d: a reader still on the "+
			"old array must see the old bounds, not the new memory's", len(heldU.bytes), pageSize)
	}
	if unshared.img.Load() == heldU {
		t.Errorf("the reallocating arm published no new descriptor")
	}
}

// TestAPublishingGrowDoesNotRaceAConcurrentReader is the memory-safety half, and **its oracle is the
// race detector rather than any assertion in it**.
//
// Saying that plainly is the point: under `go test` without `-race` this test asserts only that nothing
// panicked and that the loads returned in-bounds answers, which the old engine also managed almost
// always — a torn header is a *window*, not a certainty, and a test that reported green on the broken
// engine nine runs in ten would be worse than no test. Under `-race` the verdict is exact, because the
// old engine's `m.bytes = grown` is a write to a shared three-word field with a concurrent reader on it
// and the detector reports that deterministically once both goroutines have run. `make race` and CI's
// `race` step are where this test has an oracle; `make check` runs it as a smoke test and that is all.
//
// # It rides the reslicing arm now, and [ADR 0073][0073] is why
//
// It was `TestARelocatingGrowDoesNotRaceAConcurrentReader` and it grew an *unshared* memory under a
// concurrent guest reader, on the premise that relocation is the arm a torn header shows up on. [#586][586]
// closed that arm to exactly this fixture: a relocation is refused while any agent other than the grower
// is inside `Invoke`, and a second goroutine calling `Invoke` on one instance is that agent. Left as it
// was, the test would have failed nondeterministically on its own *"the relocating arm stopped being
// reached"* guard — the guard doing its job, on a fixture that no longer reaches what it names.
//
// So the control is **re-pointed rather than retired**, because the risk it names is not the arm it used
// to ride: *a published descriptor must never be mutated under a reader*, which is decision 0058's whole
// subject and is a property of both arms. The arm still concurrently reachable is the reslicing one — a
// reserved memory never relocates, so a shared memory declaring a max inside `sharedReservePages` grows
// under a concurrent reader as often as this loop asks, and the mutation the detector is here to catch
// (`m.img.Load().bytes = cur[:n]` in place of the `Store`) lives on that arm unchanged. The relocating
// arm's own concurrent-reader case is not left uncovered so much as made *unreachable*, and
// `TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage` is the witness for that.
//
// **The base pointer is asserted stationary at the end, and that is the fixture's vacuity check.** It is
// what says the reslicing arm was reached: a fixture that started relocating would be refused by #586's
// predicate and the grows would report `-1`, so the two failures name different causes and neither is
// silent.
//
// The `go` statement lives here rather than in engine code, which is the same placement
// `TestAtomicRmwIsNotObservablyTornAcrossThreads` argues for: the tripwire scans non-test files, and two
// goroutines calling `Invoke` on one instance need no `Spawn` at all — they get their own frames and
// stacks and share `in.mems[0]`, which is exactly the sharing §4 is about.
//
// **The reader is a guest `i32.load`, not a call to `m.read`**, because the claim is about the path the
// interpreter takes: `memAccess` resolves the memory, loads the image, bounds-checks and accesses,
// and it is the *pair* of loads inside one operation that decision 0058 forbids. A direct call to
// `m.read` would exercise one function instead of the path.
//
// [586]: https://github.com/scttfrdmn/burroughs/issues/586
// [0073]: ../../docs/decisions/0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md
func TestAPublishingGrowDoesNotRaceAConcurrentReader(t *testing.T) {
	// A shared memory declaring a max inside the reservation cap: `allocate` reserves all 32 pages, so
	// every grow below reslices into that capacity and republishes the same array at a greater length.
	in := instantiateThreads1(t, `(module
	  (memory 1 32 shared)
	  (func (export "load") (param i32) (result i32) (i32.load (local.get 0)))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`)
	if len(in.mems) != 1 || in.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(in.mems))
	}

	const (
		grows = 24
		reads = 2000
	)
	// Read before the goroutines start and compared after they join: the whole run must stay on the
	// reslicing arm, and this is the pointer that says so.
	base := &in.mems[0].view()[0]
	done := make(chan error, 2)
	go func() {
		for range reads {
			// Address 0 is in bounds at every size this test reaches, so a trap here is the
			// engine answering from a descriptor that does not match the memory it is in.
			if _, err := in.Invoke("load", Value{Type: binary.I32, Bits: 0}); err != nil {
				done <- fmt.Errorf("concurrent load: %w", err)
				return
			}
		}
		done <- nil
	}()
	go func() {
		for i := range grows {
			out, err := in.Invoke("up")
			if err != nil {
				done <- fmt.Errorf("concurrent grow %d: %w", i, err)
				return
			}
			if got := out[0].Int32(); got < 0 {
				done <- fmt.Errorf("grow %d refused with %d, so the reslicing arm stopped "+
					"being reached and the rest of this test asserts nothing", i, got)
				return
			}
		}
		done <- nil
	}()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	// The grows are serial with each other, so the final size is exact — which is *not* a property
	// of concurrent `grow`s in general, and decision 0058's residual says so.
	if got := in.mems[0].size(); got != 1+grows {
		t.Errorf("after %d serial grows the memory is %d pages, want %d", grows, got, 1+grows)
	}
	if now := &in.mems[0].view()[0]; now != base {
		t.Errorf("the backing array moved from %p to %p, so this run left the reslicing arm and "+
			"nothing above asserted what it claims to.\n"+
			"A reserved memory must reslice: the reservation is what makes the pointer stationary, "+
			"and after #586 the relocating arm would have *refused* under the concurrent reader "+
			"rather than moving. Rebuild the fixture so the reservation covers every grow — do not "+
			"drop this check", base, now)
	}
}

// TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage is [#586][586]'s witness — [ADR 0073][0073]
// decision 1 — and it comes in three parts because a refusal is trivially satisfiable.
//
// # The defect, and why it is not a memory-safety one
//
// Decision 0058 publishes a memory's bytes through one `atomic.Pointer[memImage]`, so a thread that
// loaded the old descriptor keeps a pointer and a length that agree with each other and name an array
// nothing has freed. Every read through it is in bounds. Every *write* through it lands in an array the
// engine has stopped answering from, and is therefore lost — not torn, not unsafe, gone. The agent that
// made it cannot read its own store back at its next instruction, which is outside every memory model
// rather than a permitted relaxation, so the engine makes the state unreachable instead of describing it.
//
// # Three parts, and the second is the one that keeps the first honest
//
//  1. **The refusal.** A sibling agent is parked inside a host function, so `in.host.callers` is 2 — the
//     parked `Invoke` and the growing one — and `soleAgentLocked` refuses on identity plus
//     `self.callers <= 1`. The grow reports the spec's `-1` and `growthRefusedWithASiblingAgent` moves by
//     exactly one, which is the two channels of `growthRefusedPastReservation`'s own argument: a guest
//     that sees `-1` cannot tell which engine limit it hit, so the record has to be the engine's.
//  2. **The floor.** With the sibling released, the *same* call must relocate and succeed. Without this,
//     `relocate` returning `false` unconditionally would satisfy part 1 and part 3 both — no lost write,
//     because no relocation, ever. *An unmeasured stability claim is not a protection*: the over-refusal
//     has a level, and this is where it is pinned.
//  3. **The lost write itself.** Between the refusal and the release, the test writes through the image it
//     captured *before* the refused grow and reads that byte back through a guest `i32.load8_u`. This is
//     the defect's own shape at unit scale, and it is the assertion that fails on the unfixed engine: the
//     relocation would have happened, the guest would read from the new array, and the byte would be zero.
//     The read is a guest load rather than `mem.view()` for `TestAPublishingGrowDoesNotRaceAConcurrentReader`'s
//     reason — the claim is about the path the interpreter takes to reach memory.
//
// **The sibling is a parked host call and not a second guest loop, which is what makes this deterministic.**
// A second goroutine spinning in guest code would have `callers > 0` only while it happened to be inside
// `Invoke`, so the refusal would be a race the test wins most of the time — the shape #608 was filed for.
// A host function that signals and then blocks on a channel holds its caller count for exactly as long as
// the test wants it held. There is no `-race`-only verdict here and no window: every assertion below is
// exact on every run.
//
// **`cap == len` is asserted before anything else**, because the whole test is about the relocating arm and
// a memory with allocator slack would reslice instead — a green earned by never reaching the subject. A
// hard failure rather than a skip: *a skip is not a verdict*.
//
// Watched die three ways. `relocate` returning `true` unconditionally fails part 1 on the return value and
// the counter, *and* part 3 on the lost byte — the unfixed engine, exactly. Dropping the
// `soleAgentLocked` call alone does the same. Making `soleAgentLocked` return `false` unconditionally fails
// part 2, naming the over-refusal.
//
// [586]: https://github.com/scttfrdmn/burroughs/issues/586
// [0073]: ../../docs/decisions/0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md
func TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage(t *testing.T) {
	// **The relocating arm lives on decision 0076's fallback path, so this test runs there.** A memory
	// reserved through an anonymous mapping is reserved to its ceiling and never relocates, which is
	// what M-1 buys; the arm this test is about is still real on the `!unix` ports and on any host that
	// refuses a mapping, and it is still the arm ADR 0058's publication exists for. The `cap == len`
	// guard below is what said so, unprompted, when the mapping landed.
	withoutReservation(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	in := hostLink(t, `(module
	  (import "h" "park" (func $park))
	  (memory 1)
	  (func (export "park") (call $park))
	  (func (export "peek") (result i32) (i32.load8_u (i32.const 0)))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`,
		binary.Features{}, hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
				close(entered)
				<-release
				return nil, nil
			}),
		}))
	if len(in.mems) != 1 || in.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(in.mems))
	}
	mem := in.mems[0]
	held := mem.img.Load().bytes
	if cap(held) != len(held) {
		t.Fatalf("the allocator gave a %d-byte memory %d bytes of capacity, so the grows below "+
			"reslice instead of relocating and this test asserts nothing about the arm it names. "+
			"Rebuild the fixture so the relocating arm is reached — do not delete the test",
			len(held), cap(held))
	}

	// The sibling agent: an `Invoke` that has entered a host function and stays there. Its caller is
	// counted from `invokeIndex`, so `in.host.callers` is 1 before the grow below adds its own.
	parked := make(chan error, 1)
	go func() {
		_, err := in.Invoke("park")
		parked <- err
	}()
	<-entered

	// Part 1: the refusal, in both channels.
	before := growthRefusedWithASiblingAgent.Load()
	out, err := in.Invoke("up")
	if err != nil {
		t.Fatalf("grow with a sibling agent parked: %v", err)
	}
	if got := out[0].Int32(); got != -1 {
		t.Errorf("memory.grow returned %d with a sibling agent inside `Invoke`, want -1.\n"+
			"A relocation here abandons the array that agent is holding, and every write it "+
			"makes through the old image afterwards is lost — the agent cannot read its own "+
			"store back, which no memory model permits. The conforming answer is the spec's "+
			"-1 (ADR 0073)", got)
	}
	if moved := growthRefusedWithASiblingAgent.Load() - before; moved != 1 {
		t.Errorf("growthRefusedWithASiblingAgent moved by %d, want 1.\n"+
			"`memory.grow` reports every failure as -1, so the counter is the whole of the "+
			"record that says *which* engine limit refused — the same argument "+
			"`growthRefusedPastReservation` exists for", moved)
	}
	if got := mem.size(); got != 1 {
		t.Errorf("the refused grow changed the size to %d pages, want 1 unchanged", got)
	}

	// Part 3: the write the defect loses. `held` is the image captured before the refused grow, which
	// is what a sibling agent would still be holding; on the fixed engine it is also the live image.
	held[0] = 0x5a
	seen, err := in.Invoke("peek")
	if err != nil {
		t.Fatalf("peek after writing through the held image: %v", err)
	}
	if got := seen[0].Int32(); got != 0x5a {
		t.Errorf("a byte written through the held image reads back as %#x, want 0x5a.\n"+
			"This is #586 itself: the grow relocated, the engine now answers from the new "+
			"array, and the write went into the one it abandoned. Memory-safe, in bounds, "+
			"and lost", got)
	}

	// Part 2: the floor. The sibling is gone, so the identical call must relocate and succeed.
	close(release)
	if perr := <-parked; perr != nil {
		t.Fatalf("the parked invoke: %v", perr)
	}
	before = growthRefusedWithASiblingAgent.Load()
	out, err = in.Invoke("up")
	if err != nil {
		t.Fatalf("grow as the sole agent: %v", err)
	}
	if got := out[0].Int32(); got != 1 {
		t.Fatalf("memory.grow returned %d as the sole agent, want the previous size 1.\n"+
			"A fix that refuses every relocation satisfies \"no lost write\" by never "+
			"relocating, which is the vacuity this arm exists to catch: with no sibling agent "+
			"there is nobody to strand and the growth must happen", got)
	}
	if moved := growthRefusedWithASiblingAgent.Load() - before; moved != 0 {
		t.Errorf("growthRefusedWithASiblingAgent moved by %d on a successful grow, want 0", moved)
	}
	if got := mem.size(); got != 2 {
		t.Errorf("size() = %d after the sole-agent grow, want 2", got)
	}
	if now := &mem.view()[0]; now == &held[0] {
		t.Errorf("the sole-agent grow reported success without moving the array (%p), so it took "+
			"the reslicing arm and the floor above proves nothing about relocation", now)
	}
	// The blit carries the write made while the refusal stood, which is the other half of "no write is
	// lost": the refusal defers the relocation, it does not discard what happened during it.
	seen, err = in.Invoke("peek")
	if err != nil {
		t.Fatalf("peek after the sole-agent grow: %v", err)
	}
	if got := seen[0].Int32(); got != 0x5a {
		t.Errorf("the byte written before the relocation reads back as %#x, want 0x5a: the blit "+
			"must copy the old image into the new array", got)
	}
}

// TestAMemoryInTwoIndexSpacesRelocatesOnlyWhenEveryWorldIsIdle is [ADR 0073][0073]'s multi-world arm: a
// memory two instances hold answers the sole-agent question once per world, so it relocates when both are
// idle and refuses when an agent is inside `Invoke` on **either** of them.
//
// # The test this replaces asserted the defect as the rule
//
// It was named `TestAMemoryInTwoIndexSpacesRefusesToRelocate`, and it pinned an unconditional refusal — the
// mechanism's first draft answered for one `world` and gave up as soon as a second appeared, on the
// argument that two `world.mu`s need a lock order nothing establishes. The lock argument was sound and the
// *cost* was never measured: `memory_grow.wast` exports two memories from one module and grows them from a
// second, so the suite's own fixture for growing a memory is the two-index-space case, and refusing it turned
// **30 default-lane passes into fails**. The deadlock is closed by `relocMu` instead. What that test was
// doing — asserting, in its own failure messages, that the refusal was correct — is *the defect stated as
// the rule*: a reviewer reading it would have confirmed the bug.
//
// # Three questions, and each of the first two is a floor under the third
//
//  1. **One world, no sibling: relocate.** Runs before the import, so a fixture that simply never grows
//     cannot be mistaken for a working mechanism.
//  2. **Two worlds, both idle: relocate.** This is the assertion the replaced test had inverted, and it is
//     the one the board cares about. Asked from the importer *and* from the supplier, because
//     `attachWorld` records worlds on the memory rather than on either instance and the supplier is the
//     side that looks single-threaded and is not.
//  3. **Two worlds, an agent parked in the other one: refuse — in both directions.** Without this the
//     mechanism could ignore every world but `self`'s and still pass 1 and 2, and the predicate would be
//     decorative. The agent is a parked host call in one instance while the *other* grows, so the caller
//     that refuses the grow is one no single-world predicate could see.
//
// # Part 3 runs both directions because one direction passed by coincidence of attach order
//
// It parked only in the supplier at first, and the injection battery priced that: the mutation *"consult
// only `m.ws[0]`"* was pre-registered to fail this test and **passed**. `ws` is append-ordered and the
// supplier's `build` attaches first, so `m.ws[0]` *is* the world holding the parked agent — a predicate
// that asks exactly one world asks the right one here, for a reason having nothing to do with being right.
// *The shape of what survives names the bug*: what the fixture could not see was the predicate's breadth,
// so the second row parks in the **importer**, where `m.ws[0]` is idle and a single-world answer strands a
// live agent. Two host functions rather than one for the same reason a `close` cannot be repeated — each
// instance needs its own `entered`/`release` pair.
//
// `cap == len` is asserted before each grow that must move, for
// `TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage`'s reason: allocator slack would take the
// reslicing arm and earn a green without reaching the subject.
//
// Watched die: locking and checking only `m.ws[0]` fails part 3's second row on both channels — the
// supplier grows, its own world is `ws[0]` and idle, so it relocates and strands the parked importer agent
// — and passes the first row, which is the measurement above. Refusing whenever `len(m.ws) > 1` — the
// replaced mechanism — fails part 2 in both directions. Dropping `soleAgentLocked` fails part 3.
//
// [0073]: ../../docs/decisions/0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md
func TestAMemoryInTwoIndexSpacesRelocatesOnlyWhenEveryWorldIsIdle(t *testing.T) {
	// **The relocating arm lives on decision 0076's fallback path, so this test runs there.** A memory
	// reserved through an anonymous mapping is reserved to its ceiling and never relocates, which is
	// what M-1 buys; the arm this test is about is still real on the `!unix` ports and on any host that
	// refuses a mapping, and it is still the arm ADR 0058's publication exists for. The `cap == len`
	// guard below is what said so, unprompted, when the mapping landed.
	withoutReservation(t)

	// One parkable host function per instance. `entered` is closed by the host call and `release` closes
	// it back, so a pair is single-use and part 3's two directions cannot share one.
	type parkable struct {
		entered chan struct{}
		release chan struct{}
	}
	parkHost := func() (*parkable, Imports) {
		p := &parkable{entered: make(chan struct{}), release: make(chan struct{})}
		return p, hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
				close(p.entered)
				<-p.release
				return nil, nil
			}),
		})
	}

	supPark, supHost := parkHost()
	sup := hostLink(t, `(module
	  (import "h" "park" (func $park))
	  (memory (export "m") 1)
	  (func (export "park") (call $park))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`,
		binary.Features{}, supHost)
	if len(sup.mems) != 1 || sup.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(sup.mems))
	}
	mem := sup.mems[0]

	// relocating asserts the next grow of `mem` will move the array rather than reslice into allocator
	// slack, which is the arm every part of this test is about.
	relocating := func(where string) {
		t.Helper()
		if img := mem.img.Load().bytes; cap(img) != len(img) {
			t.Fatalf("%s: the allocator gave a %d-byte memory %d bytes of capacity, so the grow "+
				"reslices instead of relocating and this test asserts nothing about the arm it "+
				"names. Rebuild the fixture — do not delete the test", where, len(img), cap(img))
		}
	}

	// Part 1: one world, no sibling. Runs first so the floor is not an artifact of the import below.
	relocating("before the import")
	before := growthRefusedWithASiblingAgent.Load()
	out, err := sup.Invoke("up")
	if err != nil {
		t.Fatalf("the supplier's grow before the import: %v", err)
	}
	if got := out[0].Int32(); got != 1 {
		t.Fatalf("the supplier's grow returned %d before anything imported its memory, want the "+
			"previous size 1. A memory in one index space with one agent has nobody to strand", got)
	}
	if moved := growthRefusedWithASiblingAgent.Load() - before; moved != 0 {
		t.Fatalf("growthRefusedWithASiblingAgent moved by %d on the sole-world grow, want 0", moved)
	}

	// The second index space. `build` calls `attachWorld` over the importer's fully populated `mems`,
	// which is where the memory learns it is in a world other than the one that allocated it.
	impPark, impHost := parkHost()
	imp := hostLink(t, `(module
	  (import "s" "m" (memory 1))
	  (import "h" "park" (func $park))
	  (func (export "park") (call $park))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`,
		binary.Features{}, func(mod, name string) (Extern, bool) {
			if ext, ok := impHost(mod, name); ok {
				return ext, true
			}
			return sup.Export(name)
		})
	if len(imp.mems) != 1 || imp.mems[0] != mem {
		t.Fatalf("the importer's memory 0 is not the supplier's object, so this test is not about " +
			"a shared memory at all")
	}
	if len(mem.ws) != 2 {
		t.Fatalf("the memory is in %d worlds after two instances installed it, want 2: with one "+
			"entry every assertion below would be a single-world one wearing a two-instance "+
			"fixture", len(mem.ws))
	}
	// Attach order is what part 3's two directions turn on, so it is asserted rather than assumed: the
	// supplier's `build` ran first, so a predicate consulting `m.ws[0]` alone consults the supplier's
	// world. Were this to flip, part 3's rows would swap which one is the coincidence and which one is
	// the witness, and both would still run — but the doc comment above would be wrong.
	if mem.ws[0] != &sup.world {
		t.Fatalf("m.ws[0] is not the supplier's world, so the attach order this test's part 3 " +
			"reasons about has changed. The rows still run; the doc comment naming which " +
			"direction a single-world predicate passes by coincidence needs re-reading")
	}

	// Part 2: two worlds, both idle. Either side may grow.
	for _, c := range []struct {
		name string
		in   *Instance
	}{
		{"the importer", imp},
		{"the supplier, which now looks single-threaded and is not", sup},
	} {
		t.Run(c.name, func(t *testing.T) {
			relocating(c.name)
			size := mem.size()
			refused := growthRefusedWithASiblingAgent.Load()
			got2, gerr := c.in.Invoke("up")
			if gerr != nil {
				t.Fatalf("grow: %v", gerr)
			}
			if got := got2[0].Int32(); got != int32(size) {
				t.Errorf("memory.grow returned %d for a memory in two idle index spaces, want the "+
					"previous size %d.\nTwo worlds is the ordinary case, not the exotic one — "+
					"`memory_grow.wast` is this exact shape — and a mechanism that refuses it "+
					"refuses the suite's own fixture for growing a memory", got, size)
			}
			if moved := growthRefusedWithASiblingAgent.Load() - refused; moved != 0 {
				t.Errorf("growthRefusedWithASiblingAgent moved by %d on a grow no agent could "+
					"be stranded by, want 0", moved)
			}
			if got := mem.size(); got != size+1 {
				t.Errorf("size() = %d after the grow, want %d", got, size+1)
			}
		})
	}

	// Part 3: two worlds, an agent parked in the one that is not the grower's — both directions, because
	// `m.ws[0]` is the supplier's and a one-world predicate therefore answers the first row correctly for
	// the wrong reason. See the doc comment.
	for _, c := range []struct {
		name   string
		park   *parkable
		agent  *Instance // the instance an agent is parked inside
		grower *Instance // the instance that then tries to grow
	}{
		{"an agent parked in the supplier, the importer grows", supPark, sup, imp},
		{"an agent parked in the importer, the supplier grows", impPark, imp, sup},
	} {
		t.Run(c.name, func(t *testing.T) {
			parked := make(chan error, 1)
			go func() {
				_, perr := c.agent.Invoke("park")
				parked <- perr
			}()
			<-c.park.entered
			// The release runs however this subtest leaves, so a failing assertion cannot leave a
			// goroutine wedged inside a host call holding a caller count the next row would read.
			defer func() {
				close(c.park.release)
				if perr := <-parked; perr != nil {
					t.Errorf("the parked invoke: %v", perr)
				}
			}()

			relocating(c.name)
			size := mem.size()
			refused := growthRefusedWithASiblingAgent.Load()
			got3, gerr := c.grower.Invoke("up")
			if gerr != nil {
				t.Fatalf("the grow with %s: %v", c.name, gerr)
			}
			if got := got3[0].Int32(); got != -1 {
				t.Errorf("memory.grow returned %d with %s, want -1.\nThat agent can reach these "+
					"bytes through its own index space, so relocating abandons an array it may "+
					"hold. A predicate consulting only the grower's world — or only `m.ws[0]` — "+
					"would answer exactly this way in one of these two rows", got, c.name)
			}
			if moved := growthRefusedWithASiblingAgent.Load() - refused; moved != 1 {
				t.Errorf("growthRefusedWithASiblingAgent moved by %d, want 1", moved)
			}
			if got := mem.size(); got != size {
				t.Errorf("the refused grow changed the size from %d to %d pages", size, got)
			}
		})
	}
}
