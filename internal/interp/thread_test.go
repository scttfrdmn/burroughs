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
