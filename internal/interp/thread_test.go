// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	wbin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// spawnGated is `atomicGated`'s shape for a spawn vector, and it is a separate helper for one
// reason: these tests need the `*Instance` itself, to read the guest's memory back and to reach
// `spawn`, where `atomicGated` returns only an `Invoke`'s results.
//
// `Features{Threads: true}` because a `shared` memory does not decode without it — which is the same
// fact `Spawn`'s gate rests on, from the other side.
func spawnGated(t *testing.T, src string) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&wbin.Decoder{Features: wbin.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	return in
}

// awaitThread waits for a spawned thread to terminate, and fails rather than hanging.
//
// **A timeout and not a bare receive**, because the failure this is most likely to catch is a thread
// that never starts: a bare `<-t.done` would report that as a test binary hanging until the package
// timeout, with no line number and no other row's result. The duration is a *liveness* bound and
// never a performance claim — the guests below run a handful of instructions, so any value orders of
// magnitude above that is equally correct and nothing here reads the elapsed time.
func awaitThread(t *testing.T, th *thread) {
	t.Helper()
	select {
	case <-th.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never terminated: the goroutine either never ran or is stuck in the "+
			"interpreter, and no row after this one would mean anything", th)
	}
}

// TestEveryStackCreationSiteCarriesAThread is a **structural** control, and saying so is the point of
// this comment.
//
// Nothing on the interpreter's hot path *reads* `stack.t` in this slice — the first reader is #515's
// safepoint check — so a behavioural test of propagation is not available: deleting `t: &in.host` from
// a creation site changes no observable answer, and a test that passed either way would be an
// analytic zero wearing a control's clothes. What *is* checkable today is the invariant decision 0050
// actually rests on: **every stack the engine creates is given a thread**. So this parses the
// package's own non-test sources and asserts it of every `stack{…}` literal in them.
//
// **The domain is derived, not enumerated.** Listing today's four sites would inherit today's blind
// spot — the failure this exists to catch is a *fifth* site added later. That this control's own count
// went 3 → 4 when `runEntry` arrived, without the control being touched beyond its floor, is the
// derivation earning its keep. Test files are excluded because a bare `&stack{}` is the right thing
// there: several tests drive a single opcode arm and have no thread to speak of, which is also the
// second reason `stack.t`'s nil is legal.
//
// `os.ReadDir` plus `ParseFile` rather than `parser.ParseDir`, which is deprecated *and* wrong for the
// job in a way that matters: it does not consider build tags when grouping files into packages, so a
// tagged file could fall outside the domain. Walking the directory takes every `.go` file regardless
// of tag, which is the safe direction — a stack created behind a build tag is still a stack.
// `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` reaches the same conclusion for the same
// reason one file over, which is why this reads the same way rather than differently.
//
// Watched die, five ways: dropping `t:` at any one of the four sites fails naming that site, and
// blinding the type match fails the floor at `found 0`, which is the failure mode that would
// otherwise make the whole test vacuous.
func TestEveryStackCreationSiteCarriesAThread(t *testing.T) {
	// Four sites today — `constexpr.go`'s const-expr stack, `interp.go`'s start function and
	// `invokeIndex`, and `thread.go`'s `runEntry`. A count below this means the parser has stopped
	// seeing the literals rather than that the sites went away, and the assertion beneath it would
	// then pass by asking nothing. A floor rather than an equality on purpose: a *new* site is
	// exactly what this control should judge, not refuse to look at. The exact number is stated
	// because a floor alone catches a moved file and never a silent partial loss.
	const sitesWhenWritten = 4

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	var withThread, without []string
	files := 0
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files++
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// `&stack{…}` and `stack{…}` alike: the address-of is a separate node, so matching the
			// literal's own type name covers both spellings without knowing which is used.
			id, ok := lit.Type.(*ast.Ident)
			if !ok || id.Name != "stack" {
				return true
			}
			site := fmt.Sprintf("%s:%d", name, fset.Position(lit.Pos()).Line)
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "t" {
					withThread = append(withThread, site)
					return true
				}
			}
			without = append(without, site)
			return true
		})
	}
	sort.Strings(withThread)
	sort.Strings(without)

	if files == 0 {
		t.Fatalf("parsed 0 non-test .go files in internal/interp, so every assertion below is "+
			"vacuous: %d directory entries were considered", len(ents))
	}
	if total := len(withThread) + len(without); total < sitesWhenWritten {
		t.Fatalf("found %d `stack` literals across %d non-test files (%v), and there were %d when "+
			"this control was written. A count below the floor means this parser has stopped seeing "+
			"the sites, so the assertion below would pass by asking nothing",
			total, files, append(withThread, without...), sitesWhenWritten)
	}
	if len(without) != 0 {
		t.Errorf("these `stack` literals set no thread: %v\n"+
			"Every stack the engine creates carries the thread it runs on (decision 0050): three "+
			"host-side sites pass `&in.host` and `runEntry` passes the spawned thread. A stack with "+
			"no thread reaches #515's safepoint check as a nil dereference, and until #515 lands it "+
			"is silent — which is why this is checked structurally rather than by running "+
			"anything.\nSites that do carry one: %v",
			without, withThread)
	}
}

// TestTheHostThreadTakesTheFirstIDAndIsNotSpecial is the behavioural half available in this slice: the
// thread exists, it is reachable from a running stack, and its id is 1.
//
// **Why an id assertion is worth anything here.** T-4's slot has no reader until #515, so the only
// property of the context that is observable today is its identity — and identity is where the
// main-thread special case T-2 forbids would show up first. A host thread with id 0 would be
// indistinguishable from an unset field; a host thread privileged in any other way would need a
// second field, and there is none.
//
// **The propagation half is observed through `runConst`, which is the one creation site that hands
// its stack back.** That matters: a test that built `&stack{t: &in.host}` and then checked `st.t ==
// &in.host` would be reading its own construction back — asserting a thing against itself, with the
// engine's code nowhere in the loop. `runConst` is propagation site 1 of 3 and it returns the stack it
// made, so this is the one place a *behavioural* check of the invariant is available at all. The other
// two sites are covered structurally above, and this test is why that coverage is not the whole story.
//
// The instance comes from the real constructor rather than a bare `&Instance{}` — grave #163's reason
// at a second site: a hand-assembled instance would assert against a struct the test filled in itself,
// and `host` is precisely the field `InstantiateLinked` sets.
func TestTheHostThreadTakesTheFirstIDAndIsNotSpecial(t *testing.T) {
	in, trap := instantiate1(t, `(module
		(global i32 (i32.const 7))
		(func (export "f") (result i32) (global.get 0)))`)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if in.host.id != 1 {
		t.Errorf("the host thread's id is %d, want 1: ids are monotonic from 1 so that 0 can mean "+
			"nothing but failure, and instantiation takes the first one", in.host.id)
	}
	if got := in.host.String(); got != "thread 1" {
		t.Errorf("the host thread renders as %q, want %q", got, "thread 1")
	}

	// The engine's own const-expression stack, from the engine's own code path.
	st, err := in.runConst(in.mod.Globals[0].Init, 1, 0, "this test's const expression")
	if err != nil {
		t.Fatalf("runConst: %v", err)
	}
	if st.t != &in.host {
		t.Errorf("a stack created by runConst carries %v, want the instance's own thread at %p: "+
			"propagation site 1 of 3 is not handing the thread over, and no board figure would "+
			"move if it never did", st.t, &in.host)
	}

	if got, err := in.Invoke("f"); err != nil || len(got) != 1 || got[0].Bits != 7 {
		t.Fatalf("invoking through the real path after reading the thread: %v, %v", got, err)
	}
}

// TestSpawnRunsAnEntryOnItsOwnThreadWithItsArgument is contract §2 T-1's core claim, asserted through
// the only channel a host has without #12's lifecycle: the shared memory the thread wrote.
//
// **The observation is ordered by `done`, not by a poll.** Reading the guest's memory while the
// spawned thread is still writing it is a genuine Go data race — the 67 atomics are plain reads and
// writes (**#542**), so nothing in the engine makes that safe, and `-race` would be right to flag it.
// Receiving on the closed channel first supplies the happens-before edge. That this test has to think
// about it at all is #516's subject arriving early: the moment a second thread exists, the boundary
// memory model stops being hypothetical.
//
// Four claims, each of which fails on its own:
//
//   - the entry ran at all (memory changed from its zero fill);
//   - its i32 argument arrived as local 0 (the value written is the one passed, and it is a value no
//     zero fill or off-by-one could produce);
//   - the thread got its own stack (the host's `Invoke` afterwards returns the *same* cell, so the
//     spawned run did not leave operands on a stack the host then reads);
//   - the tid is 2, not 1 and not 0 — the host's own thread took 1 at instantiation, which is what
//     "monotonic from 1, and 0 is never valid" means when there is something to count against.
func TestSpawnRunsAnEntryOnItsOwnThreadWithItsArgument(t *testing.T) {
	// 0x5eed is chosen so that neither byte is zero and the two halves differ: a one-byte write, a
	// byte-swap, or a partial store all produce something else. `(memory 1 1 shared)` needs the max
	// a shared memory requires.
	const want = 0x5eed
	in := spawnGated(t, `(module
		(memory 1 1 shared)
		(func $entry (param i32) (i32.store (i32.const 0) (local.get 0)))
		(func (export "read") (result i32) (i32.load (i32.const 0)))
		(export "entry" (func $entry))
	)`)

	if got := in.mems[0].bytes[0]; got != 0 {
		t.Fatalf("memory[0] is %#x before the spawn, so this test cannot tell a thread's write "+
			"from the instantiation's own fill", got)
	}

	th, err := in.spawn(0, want, 0)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if th.id != 2 {
		t.Errorf("tid is %d, want 2: the host's thread takes 1 at instantiation and ids are "+
			"monotonic from there, so a 1 here means the host has no thread of its own and a 0 "+
			"means an id was handed out that also means failure", th.id)
	}
	awaitThread(t, th)
	if th.err != nil {
		t.Fatalf("the entry function failed: %v", th.err)
	}

	if got := binary.LittleEndian.Uint32(in.mems[0].bytes[0:4]); got != want {
		t.Errorf("the spawned thread wrote %#x to memory[0:4], want %#x", got, want)
	}

	// The host reads the same cell through the engine's own load, on the host's thread. This is the
	// fourth claim: if the spawned run had shared the host's stack or corrupted it, this Invoke is
	// where it would show.
	res, err := in.Invoke("read")
	if err != nil {
		t.Fatalf("read from the host thread after the spawn: %v", err)
	}
	if len(res) != 1 || res[0].Bits != want {
		t.Errorf("the host reads %v from memory[0:4] after the spawn, want a single %#x", res, want)
	}
}

// TestSpawnStoresATerminatingTrapRatherThanDroppingIt pins thread.err's stated behaviour.
//
// **Both directions, because "stored" is only meaningful against "not stored on success".** A field
// that always held an error would satisfy the trap row alone, and a thread that swallowed its trap
// would be indistinguishable from one that returned — which is the wrong answer this claims not to
// be. Nothing exported reads `err`: surfacing it is exit semantics and therefore #12's, so this test
// is the only thing that will notice if a later change starts dropping it.
func TestSpawnStoresATerminatingTrapRatherThanDroppingIt(t *testing.T) {
	for _, row := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "trapping entry", body: "(unreachable)", wantErr: true},
		{name: "returning entry", body: "(drop (local.get 0))", wantErr: false},
	} {
		t.Run(row.name, func(t *testing.T) {
			in := spawnGated(t, fmt.Sprintf(`(module
				(memory 1 1 shared)
				(func (export "entry") (param i32) %s)
			)`, row.body))
			th, err := in.spawn(0, 1, 0)
			if err != nil {
				t.Fatalf("spawn: %v", err)
			}
			awaitThread(t, th)
			if row.wantErr && th.err == nil {
				t.Errorf("the entry trapped and thread.err is nil, so the trap was dropped: a "+
					"caller reading %s cannot tell this from a clean return", th)
			}
			if !row.wantErr && th.err != nil {
				t.Errorf("the entry returned normally and thread.err is %v, so err does not mean "+
					"what its name says", th.err)
			}
		})
	}
}

// TestSpawnRefusesWhatItCannotSupport sweeps both refusals in both directions.
//
// The accepting row is the load-bearing part: every refusal below is satisfied by a `Spawn` that
// refuses unconditionally, and one row that must succeed is what stops that.
func TestSpawnRefusesWhatItCannotSupport(t *testing.T) {
	for _, row := range []struct {
		name    string
		module  string
		entry   uint32
		wantErr error
	}{
		{
			name: "the shape T-1 names",
			module: `(module (memory 1 1 shared)
				(func (export "entry") (param i32)))`,
			wantErr: nil,
		},
		{
			// The gate, by construction: a `shared` flag only decodes with Threads on, so a
			// reachable shared memory *is* proof the gate was set. An unshared memory therefore
			// witnesses a module decoded under a feature set that has no threads in it.
			name: "an unshared memory",
			module: `(module (memory 1)
				(func (export "entry") (param i32)))`,
			wantErr: ErrNotShared,
		},
		{
			name:    "no memory at all",
			module:  `(module (func (export "entry") (param i32)))`,
			wantErr: ErrNotShared,
		},
		{
			name: "no parameter",
			module: `(module (memory 1 1 shared)
				(func (export "entry")))`,
			wantErr: ErrThreadEntry,
		},
		{
			name: "two parameters",
			module: `(module (memory 1 1 shared)
				(func (export "entry") (param i32) (param i32)))`,
			wantErr: ErrThreadEntry,
		},
		{
			// i64 rather than i32: the arity is right and only the type is wrong, which a check
			// written as `len(ft.Params) != 1` alone would accept.
			name: "the wrong parameter type",
			module: `(module (memory 1 1 shared)
				(func (export "entry") (param i64)))`,
			wantErr: ErrThreadEntry,
		},
		{
			// A result the entry leaves behind has nowhere to go: `runEntry`'s stack is discarded,
			// so accepting this would lose a value silently rather than refuse.
			name: "a result",
			module: `(module (memory 1 1 shared)
				(func (export "entry") (param i32) (result i32) (local.get 0)))`,
			wantErr: ErrThreadEntry,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			in := spawnGated(t, row.module)
			th, err := in.spawn(row.entry, 7, 0)
			switch {
			case row.wantErr == nil:
				if err != nil {
					t.Fatalf("spawn refused a conforming entry with %v; every refusing row in "+
						"this test is satisfied by a Spawn that refuses everything, and this "+
						"row is what rules that out", err)
				}
				awaitThread(t, th)
			case err == nil:
				t.Fatalf("spawn accepted this and returned %s, want %v", th, row.wantErr)
			case !errors.Is(err, row.wantErr):
				t.Errorf("spawn refused with %v, want an error wrapping %v: the sentinel is how a "+
					"caller tells a gate refusal from a bad entry index", err, row.wantErr)
			}
		})
	}
}

// TestSpawnRefusesAnEntryIndexThatResolvesToNothing keeps the out-of-range case separate from the
// signature sweep above, because it fails through `resolveCall` rather than through either sentinel —
// folding it in would have let a row pass by matching the wrong error.
func TestSpawnRefusesAnEntryIndexThatResolvesToNothing(t *testing.T) {
	in := spawnGated(t, `(module (memory 1 1 shared)
		(func (export "entry") (param i32)))`)
	if th, err := in.spawn(99, 0, 0); err == nil {
		t.Errorf("spawn accepted function index 99 in a module with one function and returned %s", th)
	}
}

// TestSpawnDropsTheHandleAndReturnsOnlyAnID pins the boundary half of the `spawn`/`Spawn` split: the
// exported entry point hands back an id and nothing else, because a handle *is* #12's lifecycle API.
//
// It is a real assertion rather than a restatement of the signature — a `Spawn` that returned a
// non-zero id on a refusal would leave a caller believing it had a thread, which is the one thing a
// tid must not be able to mean.
func TestSpawnDropsTheHandleAndReturnsOnlyAnID(t *testing.T) {
	in := spawnGated(t, `(module (memory 1 1 shared)
		(func (export "entry") (param i32)))`)
	tid, err := in.Spawn(0, 3, 0)
	if err != nil || tid == 0 {
		t.Fatalf("Spawn returned (%d, %v), want a non-zero tid and no error", tid, err)
	}

	noMem := spawnGated(t, `(module (func (export "entry") (param i32)))`)
	if tid, err := noMem.Spawn(0, 3, 0); err == nil || tid != 0 {
		t.Errorf("Spawn returned (%d, %v) on a refusal, want (0, an error): a non-zero tid "+
			"alongside an error is a handle to a thread that does not exist", tid, err)
	}
}

// spawnGatedLinked is `spawnGated` with an import resolver, needed by the one claim below that cannot
// be made inside a single instance: a thread whose entry is an *imported* function runs in the
// instance that defined the body, so the memories it can reach are not the spawner's.
//
// It fails on a trap or a shortfall for `supplier`'s reason — an importer's assertions are ambiguous
// about which module was at fault otherwise.
func spawnGatedLinked(t *testing.T, src string, imp Imports) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&wbin.Decoder{Features: wbin.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap, err := InstantiateLinked(m, imp)
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	return in
}

// TestSpawnMarksEveryMemoryTheNewThreadCanReach is decision 0056's walk asserted *dynamically*, which
// is the half `TestEveryGoStatementInEngineCodeIsPrecededByTheWalk` in `internal/testenv` says it
// cannot do: that control pairs a `go` statement with a call to `reserveForASecondThread`
// syntactically, and a call inside a branch that never runs or a loop over an empty sequence would
// satisfy it while marking nothing.
//
// Six claims, and the two that pin the *domain* of the walk were added after the first four were
// watched die and two mutations survived. `in.reachableMemories()` in place of `target`'s, and
// `target.mems` in place of the closure, both left the import row green: the importer reaches the
// supplier through the very slot the entry resolves along, so all three expressions happen to name
// the supplier's memory in a two-module fixture. **A row that cannot separate two implementations is
// the corpus declining to choose**, so rows five and six are hand-built to be the discriminating
// pair, and the mutation battery pins which row each kills:
//
//	mutation                             kills
//	target.reachableMemories → in.…      "only the spawner reaches"    (over-marks)
//	target.reachableMemories → .mems     "two imports away"            (under-marks)
//	target.reachableMemories → in.mems   all three domain rows
//	the copy dropped                     "in the spawning instance"
//	the early return dropped             "a second spawn reallocates nothing"
//	the walk hoisted above the shape check  "a refused spawn marks nothing"
//	reservation → the current size       "in the spawning instance"
//
// The hoist is *above the entry-shape check and below `resolveCall`* on purpose: `target` does not
// exist any earlier, so a walk placed at the top of `spawn` changes the domain as well as the
// position and kills two rows for two reasons. The first attempt at that mutation did exactly that,
// which is how the data-flow half of the placement argument (now on `spawn`) got noticed.
//
//   - **An unshared memory in the spawning instance is marked**, and marked only by the spawn: the
//     assertion runs against a memory read as unmarked one line earlier, so the row cannot pass on a
//     fixture that arrived already marked. The consequence is checked rather than the flag alone —
//     `grow` reslices into the reservation afterwards and the backing array's first byte keeps its
//     address, which is the property #556 is about. The contents survive, which a relocation that
//     forgot its `copy` would fail and a flag-only assertion would not notice.
//   - **A memory reachable *only* through an imported entry is marked.** The supplier's unshared
//     memory is asserted absent from the spawner's own index space first, so a bare `in.mems` walk
//     marks nothing here and fails. It does *not* separate `in`'s closure from `target`'s, or the
//     closure from `target.mems` — rows five and six are for those, and this row's job is the plain
//     case that `resolveCall`'s `target`-not-`in` distinction exists for.
//   - **A memory two import hops out is marked** — the entry's defining instance calls into a third,
//     whose memory the thread can therefore reach. The middle instance declares no memory at all, so
//     a walk over `target.mems` marks nothing and fails while a closure walk marks the leaf's.
//   - **A memory only the *spawner* reaches is left unmarked.** Marking narrows growth permanently, so
//     the walk covering more than the new thread can touch is a real cost and not a safe default:
//     §0's partisanship says an instance's own single-threaded memories keep their cheap
//     allocate-and-blit. This is the negative half of the domain, and without it the code comment's
//     claim that the walk "marks what a second thread can touch, not everything in sight" is an
//     unasserted sentence.
//   - **A refused spawn marks nothing.** Marking is not free to undo — a marked memory can never grow
//     past `sharedReservePages` again — so a `Spawn` that fails its entry-shape check must leave the
//     instance as it found it. This row fails on a walk placed before the refusals rather than after.
//   - **A second spawn reallocates nothing.** `reserveForASecondThread` returns early on a marked
//     memory, so the array's address and capacity are unchanged across the second spawn. Without the
//     early return the second walk would replace an array a *running* thread may hold — the exact
//     use-after-free `grow`'s refusal arm exists to prevent, arriving through the repair for it.
//
// Every row awaits its thread before reading guest state. Reading `m.bytes` or `m.noMove` while a
// spawned thread runs is a genuine Go data race and `-race` would be right to flag it; the closed
// `done` channel is the happens-before edge (`TestSpawnRunsAnEntryOnItsOwnThreadWithItsArgument`
// argues this at length).
func TestSpawnMarksEveryMemoryTheNewThreadCanReach(t *testing.T) {
	// Two memories, the first shared because `Spawn` refuses an instance that reaches none, the
	// second unshared and declaring no maximum — which is the case `reservation`'s no-max arm exists
	// for and which no *shared* memory can reach (`ErrSharedMemoryNoMax`).
	const twoMemories = `(module
		(memory 1 1 shared)
		(memory 1)
		(func (export "entry") (param i32))
		(func (export "bad") (param i32) (result i32) (local.get 0))
	)`

	t.Run("an unshared memory in the spawning instance", func(t *testing.T) {
		in := spawnGated(t, twoMemories)
		shared, unshared := in.mems[0], in.mems[1]
		if !shared.noMove {
			t.Fatalf("memory 0 is declared shared and is not marked, so `allocate` is not doing " +
				"its half and this row cannot attribute a mark to the walk")
		}
		if unshared.noMove {
			t.Fatalf("memory 1 is already marked before any spawn, so nothing below could tell " +
				"the walk's mark from the fixture's")
		}
		if cap(unshared.bytes) != len(unshared.bytes) {
			t.Fatalf("memory 1 arrives with %d bytes of spare capacity, so the reservation this "+
				"row attributes to the walk is partly the allocator's",
				cap(unshared.bytes)-len(unshared.bytes))
		}
		// A pattern no zero fill produces, at both ends of the page, so a `copy` with the wrong
		// length fails as loudly as a missing one.
		unshared.bytes[0], unshared.bytes[pageSize-1] = 0xa5, 0x5a

		th, err := in.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the entry failed: %v", th.err)
		}

		if !unshared.noMove {
			t.Errorf("memory 1 is unmarked after a spawn that can reach it: `grow` gates its " +
				"refusal on the mark, so this memory's backing array can still be replaced while " +
				"the spawned thread reads it (#556, decision 0056)")
		}
		if got, want := unshared.bytes[0], byte(0xa5); got != want {
			t.Errorf("memory 1 byte 0 is %#x after the walk, want %#x: the relocation lost the "+
				"contents", got, want)
		}
		if got, want := unshared.bytes[pageSize-1], byte(0x5a); got != want {
			t.Errorf("memory 1 byte %d is %#x after the walk, want %#x: the relocation copied "+
				"less than the memory holds", pageSize-1, got, want)
		}
		if got := uint64(cap(unshared.bytes)); got != sharedReservePages*pageSize {
			t.Errorf("memory 1 has capacity %d after the walk, want %d — the cap in pages times "+
				"the page size, which is what `reservation` gives a memory declaring no maximum",
				got, sharedReservePages*pageSize)
		}

		// The consequence, which is what the mark is *for*: growth now reslices, so the address a
		// concurrent reader holds stays valid. Compared as `*byte` rather than through `unsafe`,
		// which Go's pointer equality already answers.
		before := &unshared.bytes[0]
		if got := unshared.grow(1); got != 1 {
			t.Fatalf("growing memory 1 by a page returned %d, want its old size 1", got)
		}
		if after := &unshared.bytes[0]; after != before {
			t.Errorf("growing memory 1 moved its backing array (%p -> %p) after the walk marked "+
				"it: the mark is set and the reservation is not being resliced into, which is the "+
				"stale-pointer window #556 names", before, after)
		}
	})

	t.Run("an unshared memory reached only through an imported entry", func(t *testing.T) {
		// The supplier's memory is unshared and the supplier declares no shared memory at all, so
		// nothing about *it* would permit a spawn. The importer's shared memory is what satisfies
		// the gate; the thread runs in the supplier.
		s := spawnGated(t, `(module
			(memory 1)
			(func (export "entry") (param i32))
		)`)
		ext, ok := s.Export("entry")
		if !ok {
			t.Fatal("the supplier does not export entry")
		}
		// The import precedes the memory definition because the text format requires it, and the
		// spawn is asked for entry index 0 — the imported function, which is what puts the thread
		// in the supplier.
		in := spawnGatedLinked(t, `(module
			(import "s" "entry" (func $e (param i32)))
			(memory 1 1 shared)
			(export "entry" (func $e))
		)`, func(module, name string) (Extern, bool) {
			if module == "s" && name == "entry" {
				return ext, true
			}
			return Extern{}, false
		})

		if s.mems[0].noMove {
			t.Fatalf("the supplier's memory is marked before any spawn, so this row cannot " +
				"attribute a mark to the walk")
		}
		// The discriminating premise: a walk over the *spawner's* index space cannot reach this
		// memory, so a row that passed under such a walk would not be testing the closure.
		for i, m := range in.mems {
			if m == s.mems[0] {
				t.Fatalf("the supplier's memory occupies the spawner's memory index %d, so this "+
					"row no longer distinguishes a closure walk from an `in.mems` one — the "+
					"fixture must import the function and not the memory", i)
			}
		}

		th, err := in.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn through an imported entry: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the imported entry failed: %v", th.err)
		}

		if !s.mems[0].noMove {
			t.Errorf("the supplier's memory is unmarked after a spawn whose entry runs in the " +
				"supplier: the thread executes `target`'s body against `target`'s memories, so a " +
				"walk over the spawner's index space marks the wrong set (`reachableMemories`)")
		}
	})

	t.Run("a memory two imports away", func(t *testing.T) {
		// The leaf holds the memory. The middle instance declares none at all, which is what makes
		// this row separate a closure walk from `target.mems`: the thread's entry is *defined* in
		// the middle instance, so `target` is that one and its own index space is empty.
		leaf := spawnGated(t, `(module
			(memory 1)
			(func (export "leaf") (param i32))
		)`)
		leafFn, ok := leaf.Export("leaf")
		if !ok {
			t.Fatal("the leaf does not export leaf")
		}
		middle := spawnGatedLinked(t, `(module
			(import "u" "leaf" (func $l (param i32)))
			(func (export "entry") (param i32) (call $l (local.get 0)))
		)`, func(module, name string) (Extern, bool) {
			if module == "u" && name == "leaf" {
				return leafFn, true
			}
			return Extern{}, false
		})
		if len(middle.mems) != 0 {
			t.Fatalf("the middle instance holds %d memory slot(s), so this row no longer "+
				"separates a closure walk from one over `target.mems`", len(middle.mems))
		}
		entryFn, ok := middle.Export("entry")
		if !ok {
			t.Fatal("the middle instance does not export entry")
		}
		in := spawnGatedLinked(t, `(module
			(import "s" "entry" (func $e (param i32)))
			(memory 1 1 shared)
			(export "entry" (func $e))
		)`, func(module, name string) (Extern, bool) {
			if module == "s" && name == "entry" {
				return entryFn, true
			}
			return Extern{}, false
		})

		if leaf.mems[0].noMove {
			t.Fatal("the leaf's memory is marked before any spawn, so this row asserts nothing")
		}
		th, err := in.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn through two import hops: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the entry failed two hops out: %v", th.err)
		}
		if !leaf.mems[0].noMove {
			t.Errorf("the leaf's memory is unmarked after a spawn whose entry can call into it: " +
				"the walk stops at the entry's own instance, so a memory the new thread reaches " +
				"through a further import keeps a backing array `grow` may replace (#556)")
		}
	})

	t.Run("a memory only the spawner reaches", func(t *testing.T) {
		// The mirror of the row above: the supplier's memory *is* reachable and must be marked, and
		// the importer's own unshared memory is not and must not be. Both assertions in one row,
		// because a walk that marked everything in sight would satisfy either alone.
		s := spawnGated(t, `(module
			(memory 1)
			(func (export "entry") (param i32))
		)`)
		ext, ok := s.Export("entry")
		if !ok {
			t.Fatal("the supplier does not export entry")
		}
		in := spawnGatedLinked(t, `(module
			(import "s" "entry" (func $e (param i32)))
			(memory 1 1 shared)
			(memory 1)
			(export "entry" (func $e))
		)`, func(module, name string) (Extern, bool) {
			if module == "s" && name == "entry" {
				return ext, true
			}
			return Extern{}, false
		})
		spawnerOnly := in.mems[1]
		if spawnerOnly.noMove || s.mems[0].noMove {
			t.Fatal("a fixture memory is marked before the spawn, so this row asserts nothing")
		}

		th, err := in.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the imported entry failed: %v", th.err)
		}

		if !s.mems[0].noMove {
			t.Errorf("the supplier's memory is unmarked, so this row's negative half below cannot " +
				"be read as a narrowing — it would be consistent with a walk that marks nothing")
		}
		if spawnerOnly.noMove {
			t.Errorf("the spawner's own unshared memory is marked, and the new thread runs in the " +
				"supplier and cannot reach it: marking it narrows its growth to " +
				"`sharedReservePages` for nothing, which is the cost §0 says to pay only where a " +
				"second thread can actually observe the array move")
		}
		if got := cap(spawnerOnly.bytes); got != len(spawnerOnly.bytes) {
			t.Errorf("the spawner's own unshared memory gained %d bytes of capacity, so the walk "+
				"reserved for it and only the mark was withheld",
				got-len(spawnerOnly.bytes))
		}
	})

	t.Run("a refused spawn marks nothing", func(t *testing.T) {
		in := spawnGated(t, twoMemories)
		unshared := in.mems[1]
		if unshared.noMove {
			t.Fatal("memory 1 is marked before the refused spawn, so this row asserts nothing")
		}
		// Entry 1 returns an i32, which T-1's shape refuses — a refusal that fires *after*
		// `resolveCall`, so a walk placed before the checks would already have run.
		if _, err := in.spawn(1, 1, 0); !errors.Is(err, ErrThreadEntry) {
			t.Fatalf("spawning the wrong-shaped entry returned %v, want ErrThreadEntry — this row "+
				"needs the refusal it names", err)
		}
		if unshared.noMove {
			t.Errorf("memory 1 is marked after a *refused* spawn: marking narrows what a memory " +
				"can do permanently — it can never grow past `sharedReservePages` again — so a " +
				"failed Spawn must leave the instance as it found it, which puts the walk after " +
				"every refusal and not before")
		}
		if cap(unshared.bytes) != len(unshared.bytes) {
			t.Errorf("memory 1 gained %d bytes of capacity from a refused spawn, so the walk ran "+
				"and only the mark was withheld", cap(unshared.bytes)-len(unshared.bytes))
		}
	})

	t.Run("a second spawn reallocates nothing", func(t *testing.T) {
		in := spawnGated(t, twoMemories)
		unshared := in.mems[1]
		first, err := in.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("first spawn: %v", err)
		}
		awaitThread(t, first)
		base, capacity := &unshared.bytes[0], cap(unshared.bytes)

		second, err := in.spawn(0, 2, 0)
		if err != nil {
			t.Fatalf("second spawn: %v", err)
		}
		awaitThread(t, second)
		if got := &unshared.bytes[0]; got != base {
			t.Errorf("the second spawn moved memory 1's backing array (%p -> %p): "+
				"`reserveForASecondThread` is not returning early on a marked memory, so a second "+
				"spawn replaces an array the first thread may still hold — the use-after-free the "+
				"mark exists to prevent, arriving through its own repair", base, got)
		}
		if got := cap(unshared.bytes); got != capacity {
			t.Errorf("the second spawn changed memory 1's capacity from %d to %d, so it "+
				"re-reserved a memory that was already marked", capacity, got)
		}
	})

	// **This row fails, and its failure is the finding: [#575].** It is the same claim as the five
	// above — a memory the new thread can reach is marked — reached along an edge the walk does not
	// follow, so it belongs in this test rather than in one named for the defect.
	//
	// `reachableMemories`' first draft argued the closure was complete because a `funcref` could not
	// name another instance's function. Grave #163 had already widened `ref` to a pair and
	// `funcRefTarget` resolves through `r.Inst`, so a table slot may hold a foreign funcref and
	// `call_indirect` leaves the instance that owns the table. The sentence came from `link.go`, where
	// it is accurate as *history*; reading it in the present tense is what let the argument stand.
	//
	// It does not become passable by following tables too — see the row after it.
	//
	// [#575]: https://github.com/scttfrdmn/burroughs/issues/575
	t.Run("a memory reached through a foreign funcref in a table", func(t *testing.T) {
		spawner, foreign, _ := crossInstanceTableFixture(t, false)
		th, err := spawner.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the entry failed on the new thread: %v", th.err)
		}
		// Reachability first, so the row cannot pass by the thread never getting there: 7 is what
		// the foreign function stores, and it stores it into the foreign instance's own memory.
		if got := foreign.mems[0].bytes[0]; got != 7 {
			t.Fatalf("the foreign instance's memory holds %d at byte 0, want 7 — the indirect call "+
				"did not reach it, so nothing below is a claim about the walk", got)
		}
		if !foreign.mems[0].noMove {
			t.Errorf("the new thread wrote into a memory the walk left unmarked, and the memory is "+
				"unreserved too (len %d, cap %d), so `grow` takes the allocate-and-blit arm and "+
				"replaces the array while that thread is running in it.\n"+
				"This is #556 on a path decision 0056 does not cover, and it is memory-unsafe rather "+
				"than a lost update: a slice header is three words, and a reader that sees the new "+
				"length with the old pointer is out of bounds of the array it is indexing.\n"+
				"**Do not repair this by widening the walk** — the next row shows the reachable set "+
				"is not a spawn-time property at all. #575 holds the option space; the ADR comes "+
				"before the code.",
				len(foreign.mems[0].bytes), cap(foreign.mems[0].bytes))
		}
	})

	// The row that closes off the cheap repair, and it passes: what it asserts is the *timing*, which
	// is checkable, and not the mark, which the row above already reports.
	t.Run("a foreign instance created after the spawn is reachable too", func(t *testing.T) {
		spawner, foreign, th := crossInstanceTableFixture(t, true)
		if foreign.mems[0].bytes[0] != 0 {
			t.Fatal("the fixture ran the entry before the release, so the ordering below means nothing")
		}
		if _, err := spawner.Invoke("release"); err != nil {
			t.Fatalf("release: %v", err)
		}
		awaitThread(t, th)
		if th.err != nil {
			t.Fatalf("the entry failed on the new thread: %v", th.err)
		}
		if got := foreign.mems[0].bytes[0]; got != 7 {
			t.Errorf("the foreign instance's memory holds %d at byte 0, want 7.\n"+
				"That instance did not exist when `Spawn` ran its walk — it was linked into the "+
				"spawner's exported table while the thread was already spinning — so **no "+
				"enumeration performed at spawn time could have included its memory**, however many "+
				"kinds of edge it followed. A table slot also takes a foreign funcref from "+
				"`table.set`, `table.copy` and `table.init` after the fact. #575's remedy therefore "+
				"has to be a rule about relocation rather than a bigger walk", got)
		}
	})
}

// crossInstanceTableFixture builds grave #163's shape with a memory hung off it: a spawner whose entry
// calls indirectly through slot 0 of a table it exports and never fills, and a second instance that
// imports that table, writes its own function into the slot, and holds the unshared memory the call
// lands in. Nothing links the spawner to it — the assertion below is that the import closure the walk
// follows does not reach it, which is the premise both rows rest on.
//
// With `spinFirst`, the entry waits on byte 0 of the shared memory before the indirect call and the
// spawn happens *before* the second instance is linked, so the caller can order instantiation after
// the walk. The wait is bounded: a fixture that hangs would take the package timeout down with it, and
// falling out of the loop reaches the same indirect call, which is a trap on a null slot and reported
// as one. The thread is returned rather than reached for afterwards, because `spinFirst` needs the
// spawn to happen inside this function — and a field on `Instance` holding the last thread it started
// would be engine state existing for a test's convenience. It is nil without `spinFirst`.
func crossInstanceTableFixture(t *testing.T, spinFirst bool) (spawner, foreign *Instance, th *thread) {
	t.Helper()
	const entry = `(func (export "entry") (param i32) (call_indirect (type $v) (i32.const 0)))`
	const spinning = `(func (export "entry") (param i32)
			(local $i i32)
			(block $done
				(loop $spin
					(br_if $done (i32.atomic.load (i32.const 0)))
					(local.set $i (i32.add (local.get $i) (i32.const 1)))
					(br_if $spin (i32.lt_u (local.get $i) (i32.const 100000000)))
				)
			)
			(call_indirect (type $v) (i32.const 0))
		)
		(func (export "release") (i32.atomic.store (i32.const 0) (i32.const 1)))`
	body := entry
	if spinFirst {
		body = spinning
	}
	spawner = spawnGated(t, `(module
		(type $v (func))
		(memory 1 1 shared)
		(table (export "t") 1 funcref)
		`+body+`
	)`)
	tab, ok := spawner.Export("t")
	if !ok {
		t.Fatal("the spawner does not export its table")
	}
	if spinFirst {
		started, err := spawner.spawn(0, 1, 0)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		th = started
	}
	foreign = spawnGatedLinked(t, `(module
		(import "m" "t" (table 1 funcref))
		(memory 1)
		(func $w (i32.store (i32.const 0) (i32.const 7)))
		(elem (i32.const 0) $w)
	)`, func(module, name string) (Extern, bool) {
		if module == "m" && name == "t" {
			return tab, true
		}
		return Extern{}, false
	})

	for i, x := range spawner.funcs {
		if x != nil && x.owner == foreign {
			t.Fatalf("the spawner imports function %d from the foreign instance, so its import "+
				"closure reaches that instance's memory anyway and neither row is about the "+
				"cross-instance table edge", i)
		}
	}
	for _, m := range spawner.reachableMemories() {
		if m == foreign.mems[0] {
			t.Fatal("the foreign memory is in the spawner's import closure, so the walk covers it " +
				"and these rows assert nothing")
		}
	}
	return spawner, foreign, th
}
