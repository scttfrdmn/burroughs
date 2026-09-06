// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestATableReservesToItsDeclaredMaxUnderTheCeiling is [decision 0075][0075]'s reservation arm, and it is
// `TestSharedMemoryGrowthKeepsItsBackingArray`'s shape one subject over with the two places tables differ
// asserted rather than assumed.
//
// Five rows, and the middle three are each a floor under a claim `newTable`'s own comment makes:
//
//   - **A declared max inside the ceiling reserves all of it**, so every grow up to that max reslices and
//     the pointer is stationary. The contents are read back after the grow, because "same pointer" is
//     satisfiable by an engine that reallocated and got the same address from the allocator.
//   - **No declared max reserves nothing**, which is the engine reserving what the module declared and
//     never what the engine would guess (0075, decision 1). Asked in the direction that fails if the
//     reservation quietly covered everything — *an unasserted distance is the vacuum*.
//   - **A declared max above the ceiling reserves the ceiling**, not the declaration. This is the row that
//     makes the refusal in `TestARelocatingTableGrowRefusesWhileASiblingAgentCouldHoldTheImage` reachable
//     at all, so a fixture whose reservation covered its whole declaration would leave that test asserting
//     nothing.
//   - **A minimum above the ceiling is allocated in full**, because `reserve` never falls below `lim.Min`:
//     the ceiling caps what is reserved *ahead* of growth and can never make a table smaller than it
//     declared. Without this row, a `min(lim.Max, tableReserveSlots)` written without the outer `max`
//     would truncate the table itself and only a corpus vector with a 2000-slot minimum would catch it.
//   - **`min == max` reserves exactly the declaration**, the 279-table plurality of the corpus census in
//     0075, where the reservation costs nothing at all.
//
// **Capacity is read with `cap`, and that is a claim about the reservation rather than about `make`.** Go's
// `make([]T, n)` never widens `cap` past `n`, so a `cap` above the length here can only have come from the
// explicit three-argument `make` this decision added.
//
// Watched die: dropping the `if lim.HasMax` reservation from `newTable` fails rows 1, 3 and 5 on capacity;
// replacing `max(lim.Min, min(lim.Max, tableReserveSlots))` with the bare `min` fails row 4 by allocating a
// 1024-slot table for a module declaring 2000; raising the reservation to cover any declared max fails row 3.
//
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
func TestATableReservesToItsDeclaredMaxUnderTheCeiling(t *testing.T) {
	// `tableReserveSlots` is read rather than spelled, for the reason grave #621 paid for one file over: a
	// literal duplicating a package value is correct exactly once, and this test would then be pinning the
	// number the ceiling had when it was written rather than the rule.
	if tableReserveSlots < 2 {
		t.Fatalf("tableReserveSlots is %d, and every row below needs room to grow inside a reservation: "+
			"a ceiling of 0 or 1 would make rows 1 and 3 agree with row 2 by arithmetic", tableReserveSlots)
	}

	rows := []struct {
		name    string
		src     string
		wantLen uint64
		wantCap uint64
	}{
		{
			name:    "a declared max inside the ceiling reserves all of it",
			src:     `(module (table 1 32 funcref))`,
			wantLen: 1,
			wantCap: 32,
		},
		{
			name:    "no declared max reserves nothing",
			src:     `(module (table 1 funcref))`,
			wantLen: 1,
			wantCap: 1,
		},
		{
			name:    "a declared max above the ceiling reserves the ceiling",
			src:     `(module (table 1 4096 funcref))`,
			wantLen: 1,
			wantCap: tableReserveSlots,
		},
		{
			name:    "a minimum above the ceiling is allocated in full",
			src:     `(module (table 4096 4096 funcref))`,
			wantLen: 4096,
			wantCap: 4096,
		},
		{
			name:    "min == max reserves exactly the declaration",
			src:     `(module (table 4 4 funcref))`,
			wantLen: 4,
			wantCap: 4,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			in := invoke1t(t, row.src)
			if len(in.tables) != 1 || in.tables[0] == nil {
				t.Fatalf("expected one table, got %d", len(in.tables))
			}
			slots := in.tables[0].view()
			if got := uint64(len(slots)); got != row.wantLen {
				t.Errorf("the table is %d slots long, want %d: the declared minimum is the length and "+
					"the reservation is the capacity, and confusing the two would make every table "+
					"start at its max", got, row.wantLen)
			}
			if got := uint64(cap(slots)); got != row.wantCap {
				t.Errorf("the table reserved %d slots of capacity, want %d.\n"+
					"Decision 0075 reserves the declared max under the ceiling %d, so that a grow "+
					"reslices instead of abandoning an image a sibling agent may be holding (#662). "+
					"`make([]T, n)` never widens cap on its own, so this figure is the reservation's",
					got, row.wantCap, tableReserveSlots)
			}
		})
	}
}

// TestAGrowWithinTheReservationKeepsTheArrayAndFillsTheNewSlots is the reslicing arm's two properties, and
// the second is the one that must **not** be transcribed from memory's twin.
//
// `memory`'s reslicing arm publishes `cur[:n]` and says nothing about the new bytes, because `make` zeroed
// them and zero is what the spec requires of fresh memory. A zeroed `ref` is `{Null: false, Addr: 0, Inst:
// nil}` — deliberately **not** `ref.null`, which is `ref`'s own documented design — so a table that reslices
// without filling hands the guest non-null references naming no function where it asked for `ref.null func`.
//
// **So the fill is witnessed through the guest, twice, and the second form is the one that scores.**
// `table.get` reading null is the direct assertion; `call_indirect` on slot 1 is the same fact where a guest
// can act on it, and it is the form a corpus vector would have used.
//
// **No vector does, and that is measured rather than assumed.** Deleting the fill loop and running
// `TestPhase1Files` leaves the corpus green, and a `panic` planted in the arm shows the corpus *does* reach it
// — at `table_grow.wast`, in the same run. So the arm is exercised upstream and its fill value is never
// observed, which is what makes this an accept-direction hole rather than an unreached branch. The package is
// green too with this one test skipped: it is the only oracle on the fill in the tree.
//
// The pointer is asserted stationary at the end because that is what says the *reslicing* arm ran: on the
// relocating arm the fill happens in `publish` instead, and a green here would be about the wrong code.
//
// Watched die: deleting the `for i := old; i < newSize; i++ { grown[i] = r }` loop from `grow`'s reslicing arm
// fails the `table.get` half four times over, then **kills the test binary** on the `call_indirect` half —
// `internal/interp/call.go:funcRefTarget` dereferences the zero `ref`'s nil `Inst` ([#669][669]), so the run
// dies with a nil-pointer panic repanicked out of `invokeIndex` rather than returning the wrong answer. Both
// halves score; the second scores louder than it was written to. Because a panic ends the run, that mutation's
// collateral was read from a second invocation with this test skipped — a panic hides the next test's death.
//
// [669]: https://github.com/scttfrdmn/burroughs/issues/669
func TestAGrowWithinTheReservationKeepsTheArrayAndFillsTheNewSlots(t *testing.T) {
	// Slot 0 holds `$f` so that a *correct* engine can still call through the table, which is what keeps
	// the `call_indirect` arm from passing because indirect calls are broken in general.
	in := invoke1t(t, `(module
	  (type $v (func))
	  (func $f)
	  (table 1 32 funcref)
	  (elem (i32.const 0) $f)
	  (func (export "up") (result i32) (table.grow (ref.null func) (i32.const 4)))
	  (func (export "null?") (param i32) (result i32) (ref.is_null (table.get (local.get 0))))
	  (func (export "call") (param i32) (call_indirect (type $v) (local.get 0))))`)
	if len(in.tables) != 1 || in.tables[0] == nil {
		t.Fatalf("expected one table, got %d", len(in.tables))
	}
	tab := in.tables[0]
	if uint64(cap(tab.view())) < 5 {
		t.Fatalf("the fixture reserved %d slots, so growing 1 by 4 cannot stay inside the reservation "+
			"and this test would be asserting the relocating arm's fill instead", cap(tab.view()))
	}
	base := &tab.view()[0]

	// Slot 0 is callable before the grow: the floor under the `call_indirect` arm below.
	if _, err := in.Invoke("call", Value{Type: binary.I32, Bits: 0}); err != nil {
		t.Fatalf("call_indirect through slot 0 before the grow: %v", err)
	}

	out, err := in.Invoke("up")
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	if got := out[0].Int32(); got != 1 {
		t.Fatalf("table.grow returned %d, want the previous size 1", got)
	}

	for _, i := range []int32{1, 2, 3, 4} {
		seen, gerr := in.Invoke("null?", Value{Type: binary.I32, Bits: uint64(uint32(i))})
		if gerr != nil {
			t.Fatalf("table.get %d after the grow: %v", i, gerr)
		}
		if got := seen[0].Int32(); got != 1 {
			t.Errorf("slot %d reads as non-null after a grow whose fill value was `ref.null func`.\n"+
				"The reslicing arm must write the fill value across the new slots: a zeroed `ref` is "+
				"{Null:false, Addr:0, Inst:nil}, which is not null and names no function, so publishing "+
				"`cur[:n]` without the fill hands the guest a reference nothing can resolve "+
				"(decision 0075)", i)
		}
	}

	// The same fact where the guest can act on it. `uninitialized element 1` is the spec's answer
	// (`eval.ml:126-129`). An unfilled slot does not reach this assertion at all: it holds the zero
	// `ref`, whose nil `Inst` panics inside `funcRefTarget` before any verdict is produced (#669).
	_, err = in.Invoke("call", Value{Type: binary.I32, Bits: 1})
	if err == nil {
		t.Fatalf("call_indirect through grown slot 1 succeeded.\n" +
			"A slot filled with `ref.null func` must trap `uninitialized element 1`. Succeeding means " +
			"the slot holds a resolvable reference where the guest asked for null — the accept-direction " +
			"wrong answer no rejection corpus can see (decision 0075)")
	}
	if want := "uninitialized element 1"; !strings.Contains(err.Error(), want) {
		t.Errorf("call_indirect through grown slot 1 reported %q, want %q", err, want)
	}

	if now := &tab.view()[0]; now != base {
		t.Errorf("the backing array moved from %p to %p, so this run took the relocating arm and the "+
			"fill it asserted was `publish`'s rather than the reslicing arm's. Rebuild the fixture so "+
			"the reservation covers the grow — do not drop this check", base, now)
	}
}

// TestARelocatingTableGrowRefusesWhileASiblingAgentCouldHoldTheImage is [#662][662]'s witness — [decision
// 0075][0075]'s decision 3 — and it is `TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage`'s
// three-part shape with a fourth part that exists only for tables.
//
// # The defect
//
// Decision 0065 publishes a table's slots through one `atomic.Pointer[tabImage]`, so an agent that loaded
// the old descriptor keeps a pointer and a length that agree with each other and name an array nothing has
// freed. Every read through it is in bounds. Every `table.set` through it lands in an array the engine has
// stopped answering from, and is therefore lost — not torn, not unsafe, gone. The agent cannot read its own
// store back at its next instruction, which is outside every memory model rather than a permitted
// relaxation, so no §4 clause is needed to say the state is forbidden: ADR 0073 found that answer set empty
// for memories and the reading is about the shape, not the subject.
//
// # Four parts, and parts 2 and 4 are the floors
//
//  1. **The refusal.** A sibling agent is parked inside a host function, so the world's caller count is 2 —
//     the parked `Invoke` and the growing one — and `world.soleAgentLocked` refuses on identity plus
//     `self.callers <= 1`. The grow reports the spec's `-1` and `tableGrowthRefusedWithASiblingAgent` moves
//     by exactly one, because `table.grow` shares `-1` with four spec refusals and the engine's own record
//     is the whole of what makes this limit distinguishable.
//  2. **The floor on relocation.** With the sibling released, the *same* call must relocate and succeed.
//     Without this, `relocate` returning `false` unconditionally would satisfy parts 1 and 3 both — no lost
//     write, because no relocation, ever.
//  3. **The lost write itself.** Between the refusal and the release, the test writes a non-null reference
//     through the image it captured *before* the refused grow and reads that slot back through a guest
//     `table.get`. This is the assertion that fails on the unfixed engine: the relocation would have
//     happened, the engine would answer from the new array, and the slot would read null.
//  4. **The floor on the reservation, which memory's twin has no equivalent of.** #662's stated price was
//     that *"a threaded program's table would stop growing at its initial capacity"*, and the reservation is
//     how that price is paid down rather than accepted. So the same sibling is parked again and a grow
//     *within* the reservation must **succeed**: the array does not move, so there is nobody to strand, and
//     an engine that refused every grow with a sibling live would pass parts 1–3 and fail here. This is the
//     part that says the fix is a reservation and not a prohibition.
//
// **The sibling is a parked host call and not a second guest loop**, which is what makes every assertion
// exact on every run rather than a race the test usually wins — the shape #608 was filed for. There is no
// `-race`-only verdict here.
//
// **`cap == len` is asserted before anything else** for parts 1–3, because they are about the relocating arm
// and a table with reserved capacity would reslice instead — a green earned by never reaching the subject.
// A hard failure rather than a skip: *a skip is not a verdict*.
//
// Watched die four ways. `relocate` returning `true` unconditionally fails part 1 on both channels and part 3
// on the lost slot — the unfixed engine, exactly. Dropping the `soleAgentLocked` call does the same. Making
// `soleAgentLocked` return `false` unconditionally fails part 2. Deleting the reservation from `newTable`
// fails part 4, which is the arm that prices the refusal.
//
// [662]: https://github.com/scttfrdmn/burroughs/issues/662
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
func TestARelocatingTableGrowRefusesWhileASiblingAgentCouldHoldTheImage(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	// Two tables in one module: the no-max one reserves nothing and is the relocating subject, and the
	// second declares a max inside the ceiling so part 4 has a reslicing grow to ask about. One instance
	// for both, because the sibling agent is a property of the *world* and parking twice would prove
	// nothing extra.
	in := hostLink(t, `(module
	  (import "h" "park" (func $park))
	  (table $moving 1 funcref)
	  (table $reserved 1 32 funcref)
	  (func (export "park") (call $park))
	  (func (export "null?") (param i32) (result i32)
	    (ref.is_null (table.get $moving (local.get 0))))
	  (func (export "up") (result i32) (table.grow $moving (ref.null func) (i32.const 1)))
	  (func (export "up-reserved") (result i32)
	    (table.grow $reserved (ref.null func) (i32.const 1))))`,
		binary.Features{}, hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
				close(entered)
				<-release
				return nil, nil
			}),
		}))
	if len(in.tables) != 2 || in.tables[0] == nil || in.tables[1] == nil {
		t.Fatalf("expected two tables, got %d", len(in.tables))
	}
	moving, reserved := in.tables[0], in.tables[1]
	held := moving.img.Load().slots
	if cap(held) != len(held) {
		t.Fatalf("the no-max table was given %d slots of capacity for a length of %d, so the grows "+
			"below reslice instead of relocating and parts 1-3 assert nothing about the arm they name. "+
			"Rebuild the fixture so the relocating arm is reached — do not delete the test",
			cap(held), len(held))
	}
	if cap(reserved.view()) < 2 {
		t.Fatalf("the reserved table has %d slots of capacity, so part 4's grow cannot stay inside a "+
			"reservation", cap(reserved.view()))
	}

	// The sibling agent: an `Invoke` that has entered a host function and stays there. Its caller is
	// counted from `invokeIndex`, so the world's count is 1 before each grow below adds its own.
	parked := make(chan error, 1)
	go func() {
		_, err := in.Invoke("park")
		parked <- err
	}()
	<-entered

	// Part 1: the refusal, in both channels.
	before := tableGrowthRefusedWithASiblingAgent.Load()
	out, err := in.Invoke("up")
	if err != nil {
		t.Fatalf("grow with a sibling agent parked: %v", err)
	}
	if got := out[0].Int32(); got != -1 {
		t.Errorf("table.grow returned %d with a sibling agent inside `Invoke`, want -1.\n"+
			"A relocation here abandons the array that agent is holding, and every `table.set` it "+
			"makes through the old image afterwards is lost — the agent cannot read its own store "+
			"back, which no memory model permits. The conforming answer is the spec's -1 (#662)", got)
	}
	if moved := tableGrowthRefusedWithASiblingAgent.Load() - before; moved != 1 {
		t.Errorf("tableGrowthRefusedWithASiblingAgent moved by %d, want 1.\n"+
			"`table.grow` reports every failure as -1 and shares that answer with four spec "+
			"refusals, so this counter is the whole of the record that says which engine limit "+
			"refused", moved)
	}
	if got := moving.size(); got != 1 {
		t.Errorf("the refused grow changed the size to %d slots, want 1 unchanged", got)
	}

	// Part 4: the reservation, with the same sibling still parked. This is the price #662 named being
	// paid down rather than accepted, so it runs *before* the release — a grow that only worked once the
	// world went idle would prove nothing about the reservation.
	before = tableGrowthRefusedWithASiblingAgent.Load()
	rbase := &reserved.view()[0]
	out, err = in.Invoke("up-reserved")
	if err != nil {
		t.Fatalf("grow within the reservation with a sibling agent parked: %v", err)
	}
	if got := out[0].Int32(); got != 1 {
		t.Errorf("table.grow within the reservation returned %d with a sibling agent live, want the "+
			"previous size 1.\n"+
			"The reservation is what makes this legal: the array does not move, so an older "+
			"descriptor names the same slots at a smaller length and no write can be stranded. An "+
			"engine that refused here would be paying #662's stated price — a threaded program's "+
			"table stuck at its initial capacity — instead of paying it down (decision 0075)", got)
	}
	if moved := tableGrowthRefusedWithASiblingAgent.Load() - before; moved != 0 {
		t.Errorf("tableGrowthRefusedWithASiblingAgent moved by %d on a grow inside the reservation, "+
			"want 0: the refusal is the relocating arm's and nothing else's", moved)
	}
	if now := &reserved.view()[0]; now != rbase {
		t.Errorf("the reserved table's array moved from %p to %p, so part 4 asked about the "+
			"relocating arm and its success says nothing about the reservation", rbase, now)
	}

	// Part 3: the write the defect loses. `held` is the image captured before the refused grow, which is
	// what a sibling agent would still be holding; on the fixed engine it is also the live image. A
	// funcref rather than a value: this table holds references, and `Inst` is what a `table.get` reader
	// distinguishes from null.
	held[0] = ref{Addr: 0, Inst: in}
	seen, err := in.Invoke("null?", Value{Type: binary.I32, Bits: 0})
	if err != nil {
		t.Fatalf("table.get after writing through the held image: %v", err)
	}
	if got := seen[0].Int32(); got != 0 {
		t.Errorf("a reference written through the held image reads back as null.\n" +
			"This is #662 itself: the grow relocated, the engine now answers from the new array, " +
			"and the `table.set` went into the one it abandoned. Memory-safe, in bounds, and lost")
	}

	// Part 2: the floor. The sibling is gone, so the identical call must relocate and succeed.
	close(release)
	if perr := <-parked; perr != nil {
		t.Fatalf("the parked invoke: %v", perr)
	}
	before = tableGrowthRefusedWithASiblingAgent.Load()
	out, err = in.Invoke("up")
	if err != nil {
		t.Fatalf("grow as the sole agent: %v", err)
	}
	if got := out[0].Int32(); got != 1 {
		t.Fatalf("table.grow returned %d as the sole agent, want the previous size 1.\n"+
			"A fix that refuses every relocation satisfies \"no lost write\" by never relocating, "+
			"which is the vacuity this arm exists to catch: with no sibling agent there is nobody "+
			"to strand and the growth must happen", got)
	}
	if moved := tableGrowthRefusedWithASiblingAgent.Load() - before; moved != 0 {
		t.Errorf("tableGrowthRefusedWithASiblingAgent moved by %d on a successful grow, want 0", moved)
	}
	if got := moving.size(); got != 2 {
		t.Errorf("size() = %d after the sole-agent grow, want 2", got)
	}
	if now := &moving.view()[0]; now == &held[0] {
		t.Errorf("the sole-agent grow reported success without moving the array (%p), so it took the "+
			"reslicing arm and part 2 proves nothing about relocation", now)
	}
	// The blit carries the write made while the refusal stood, which is the other half of "no write is
	// lost": the refusal defers the relocation, it does not discard what happened during it.
	seen, err = in.Invoke("null?", Value{Type: binary.I32, Bits: 0})
	if err != nil {
		t.Fatalf("table.get after the sole-agent grow: %v", err)
	}
	if got := seen[0].Int32(); got != 0 {
		t.Errorf("the reference written before the relocation reads back as null: `publish` must copy " +
			"the old image into the new array")
	}
}

// TestANewBoundaryAccessorMustDecideOnTheTableGrowthLock is the tripwire [decision 0075][0075] takes in place
// of a sentence, and it is a tripwire rather than a control because **its population is empty today**.
//
// ADR 0073's decision 6 gave `Caller.Read` and `Caller.Write` a read-shared acquisition of
// `memory.growMu`, because a retained `Caller` is an agent no `world` count can see: it holds no `callers`,
// marks no `hostCalls`, and may be used from a goroutine in no world at all, so `grow`'s sibling-agent
// predicate cannot exclude it and a relocation would strand it. **A table has no such accessor**, so 0075
// needs no twin of that decision — and that is a fact about today's boundary, not a property of the design.
// The moment a `Caller.TableGet`, `Caller.Funcref` or `Caller.Elem` exists, the same argument applies to
// `table.growMu` and *nothing in the tree would say so*: the new method would compile, pass every vector, and
// silently be an unexcludable agent.
//
// **Named after the rule and not the state.** It would have been tempting to call this
// `…HasNoTableAccessor`, and that name is falsified by whichever slice adds one — the shape this very slice
// renamed a control out of, one file over: it was `TestARelocatingTableGrowDoesNotRaceAConcurrentReader`,
// named for the arm it rode, and the arm stopped being reachable. A name that states today's arrangement
// has to be rewritten by the work that changes the arrangement; a name that states the rule does not. The
// rule here is that a new boundary accessor **decides** the question, and the decision is recorded by
// editing the set below.
//
// **`table.growMu` is a plain `sync.Mutex` where `memory.growMu` is an `RWMutex`**, which is the other half
// of what a new accessor has to decide: a boundary table accessor would need the read-shared form, or it
// would serialise every host call against every other one.
//
// Watched die: adding `func (c *Caller) Table() *table { return nil }` to host.go fails this test naming
// `Table` as unpinned.
//
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
func TestANewBoundaryAccessorMustDecideOnTheTableGrowthLock(t *testing.T) {
	// The pinned set, with what each one reaches. `Context` and `Thread` reach no guest storage at all;
	// `Read` and `Write` reach a memory and take `memory.growMu.RLock` (ADR 0073's decision 6). Nothing
	// here reaches a table.
	pinned := map[string]string{
		"Context": "no guest storage",
		"Thread":  "no guest storage",
		"Read":    "guest memory, under memory.growMu.RLock",
		"Write":   "guest memory, under memory.growMu.RLock",
	}

	typ := reflect.TypeOf(&Caller{})
	var have []string
	for i := range typ.NumMethod() {
		have = append(have, typ.Method(i).Name)
	}
	sort.Strings(have)
	if len(have) == 0 {
		t.Fatal("reflect found no exported methods on *Caller, so the comparison below is vacuous: " +
			"the boundary has at least `Context`")
	}

	for _, name := range have {
		if _, ok := pinned[name]; !ok {
			t.Errorf("`Caller` exports a method this control has not seen: %s.\n"+
				"A boundary accessor is an agent no `world` caller count can see, so if %s reaches a "+
				"**table** it must take `table.growMu` for the duration of the access — otherwise a "+
				"concurrent `table.grow` relocates the array underneath it and the write is lost "+
				"(#662, decision 0075). `memory.growMu` is an `RWMutex` for exactly this and "+
				"`table.growMu` is not yet, so a table accessor needs that widened first.\n"+
				"If %s reaches no table, say so by adding it to `pinned` above. The decision is the "+
				"point; the edit is how it is recorded.", name, name, name)
		}
	}
	for name := range pinned {
		if _, ok := reflect.TypeOf(&Caller{}).MethodByName(name); !ok {
			t.Errorf("this control pins `Caller.%s` and no such method exists. A removed boundary "+
				"accessor is fine — delete its row — but a pin naming nothing is a citation that "+
				"resolves to nowhere while still reading as coverage", name)
		}
	}
}
