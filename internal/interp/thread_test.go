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
	"path/filepath"
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

// TestEveryStackCreationSiteCarriesAThread is a **structural** control, and saying so is the point of
// this comment.
//
// Nothing on the interpreter's hot path *reads* `stack.t` in this slice — the first reader is #515's
// safepoint check — so a behavioural test of propagation is not available: deleting `t: in.host` from
// a creation site changes no observable answer, and a test that passed either way would be an
// analytic zero wearing a control's clothes. What *is* checkable today is the invariant decision 0050
// actually rests on: **every stack the engine creates is given a thread**. So this parses the
// package's own non-test sources and asserts it of every `&stack{...}` literal in them.
//
// **The domain is derived, not enumerated.** Listing today's four sites would inherit today's blind
// spot — the failure this exists to catch is a *fifth* site added later — so it reads the directory
// and scopes to `package interp`, non-test files. Test files are excluded because a bare `&stack{}`
// is the right thing there: several tests drive a single opcode arm and have no thread to speak of,
// which is also the second reason `stack.t`'s nil is legal.
//
// Watched die, four ways: dropping `t:` at any one of the three `in.host` sites fails naming that
// site; dropping `t: t` in `runEntry` fails naming it; and a floor on the site count fails if the
// literal is spelled some way this parser cannot see, which is the failure mode that would otherwise
// make the whole test vacuous.
func TestEveryStackCreationSiteCarriesAThread(t *testing.T) {
	const dir = "."
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	pkg, ok := pkgs["interp"]
	if !ok {
		t.Fatalf("no `package interp` in %s, so this control has no subject: %v",
			dir, mapKeys(pkgs))
	}

	var withThread, without []string
	for name, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// `&stack{...}` and `stack{...}` alike: the address-of is a separate node, so matching
			// the literal's own type name covers both spellings without knowing which is used.
			id, ok := lit.Type.(*ast.Ident)
			if !ok || id.Name != "stack" {
				return true
			}
			site := fmt.Sprintf("%s:%d", filepath.Base(name), fset.Position(lit.Pos()).Line)
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

	// The vacuity floor. Four sites exist today — `constexpr.go`'s const-expr stack, `interp.go`'s
	// start function and `invokeIndex`, and `thread.go`'s `runEntry` — and a count below that means
	// the parser stopped seeing the literals rather than that the sites went away. Pinned as a floor
	// and not an equality on purpose: a *new* site is exactly what this control should judge, not
	// refuse to look at. Grave-adjacent, and the grave is that a floor catches a moved file and
	// never a silent partial loss, so the number is the exact one, stated.
	if total := len(withThread) + len(without); total < 4 {
		t.Fatalf("found %d `stack` literals in package interp's non-test files (%v), and there "+
			"were 4 when this control was written. A count below the floor means this parser has "+
			"stopped seeing the sites, so the assertion below would pass by asking nothing",
			total, append(withThread, without...))
	}
	if len(without) != 0 {
		t.Errorf("these `stack` literals set no thread: %v\n"+
			"Every stack the engine creates carries the thread it runs on (decision 0050): the "+
			"three host-side sites pass `in.host` and `runEntry` passes the spawned thread. A "+
			"stack with no thread reaches #515's safepoint check as a nil dereference, and until "+
			"#515 lands it is silent — which is why this is checked structurally rather than by "+
			"running anything.\nSites that do carry one: %v", without, withThread)
	}
}

// mapKeys names what a lookup found instead, so a failure above reports the packages that *are*
// there rather than only the one that is not.
func mapKeys(m map[string]*ast.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
