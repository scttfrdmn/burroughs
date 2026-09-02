// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"sort"
	"strings"
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
	if got := reserved.grow(2); got != 1 {
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
	if got := unshared.grow(1); got != 1 {
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

// TestARelocatingGrowDoesNotRaceAConcurrentReader is the memory-safety half, and **its oracle is the
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
// The `go` statement lives here rather than in engine code, which is the same placement
// `TestAtomicRmwIsNotObservablyTornAcrossThreads` argues for: the tripwire scans non-test files, T-1's
// `Spawn` is parked, and two goroutines calling `Invoke` on one instance need none of it — they get
// their own frames and stacks and share `in.mems[0]`, which is exactly the sharing §4 is about.
//
// **The reader is a guest `i32.load`, not a call to `m.read`**, because the claim is about the path the
// interpreter takes: `memAccess` resolves the memory, loads the image, bounds-checks and accesses,
// and it is the *pair* of loads inside one operation that decision 0058 forbids. A direct call to
// `m.read` would exercise one function instead of the path.
func TestARelocatingGrowDoesNotRaceAConcurrentReader(t *testing.T) {
	// An unshared memory with no maximum: `allocate` reserves nothing, so every grow past the
	// current capacity takes the relocating arm — the arm that used to write three words.
	in, trap := instantiate1(t, `(module
	  (memory 1)
	  (func (export "load") (param i32) (result i32) (i32.load (local.get 0)))
	  (func (export "up") (result i32) (memory.grow (i32.const 1))))`)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if len(in.mems) != 1 || in.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(in.mems))
	}

	const (
		grows = 24
		reads = 2000
	)
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
				done <- fmt.Errorf("grow %d refused with %d, so the relocating arm stopped "+
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
}

// TestEveryMemoryOperationLoadsTheImageAtMostOnce is the structural control decision 0058 rests on, and
// it is a control rather than a comment because the rule is invisible at the point it is broken.
//
// **The rule.** One operation loads the published image once and does its bounds check *and* its access
// against that one slice. Two loads in one operation is the defect the mechanism exists to prevent: the
// check approves the first array and the access lands in the second, which after a relocating grow is a
// different array of a different length — so `bs[ea:ea+n]` can be out of range on a bound that was
// verified. The published descriptor makes each load internally consistent; it cannot make two loads
// agree with each other.
//
// **Why nothing else catches it.** It is not a compile error, not a vet finding, and not observable on
// any corpus vector, because the second load returns the same array in every single-threaded run. The
// board would stay green at 0 fail while the property was gone, which is the shape a control is for.
//
// **The domain is derived rather than listed** — every non-test file in this package, every function in
// it — because the failure this exists to catch is an arm added *later*: a new bulk operation, a SIMD
// path, a memory64 arm. A list of today's ten call sites would inherit today's blind spot.
//
// **Receivers are compared by their printed expression, which is an approximation, and it is the
// permissive direction.** `execMemoryCopy` legitimately loads two images because it holds two memories
// (`dstMem` and `srcMem`), so the count is per receiver expression rather than per function. Two
// spellings of one memory — `m.view()` and `in.mems[0].view()` in one function — would therefore pass
// this check while breaking the rule. That is stated rather than hidden: the check catches the shape the
// rule is broken in, which is the same receiver written the same way twice, and a reader who needs the
// other shape now has a sentence telling them the control does not see it.
//
// Watched die: adding a second `bs2 := m.view()` to `read` fails naming `read` and the receiver `m`;
// blinding the selector match to a name nothing calls fails the floor at `found 0`, which is the
// vacuity this floor exists for.
func TestEveryMemoryOperationLoadsTheImageAtMostOnce(t *testing.T) {
	// Ten sites when written: `size`, `read`, `write`, `writeNum` and `grow` in memory.go, `cell` in
	// atomic.go, and one each in `execMemoryFill` and `execMemoryInit` plus two in `execMemoryCopy`.
	// The exact number is pinned beside the floor because a floor alone catches a moved file and never
	// a silent partial loss — six sites disappearing looks like a pass to a `> 0` check.
	const sitesWhenWritten = 10

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	// Keyed "file:function", each holding a count per printed receiver expression.
	perFunc := map[string]map[string]int{}
	sites, files := 0, 0
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
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := name + ":" + fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "view" || len(call.Args) != 0 {
					return true
				}
				var recv strings.Builder
				if perr := printer.Fprint(&recv, fset, sel.X); perr != nil {
					t.Fatalf("printing the receiver of a view() call in %s: %v", key, perr)
				}
				if perFunc[key] == nil {
					perFunc[key] = map[string]int{}
				}
				perFunc[key][recv.String()]++
				sites++
				return true
			})
		}
	}

	if files == 0 {
		t.Fatalf("parsed 0 non-test .go files in internal/interp, so every assertion below is "+
			"vacuous: %d directory entries were considered", len(ents))
	}
	if sites < sitesWhenWritten {
		t.Fatalf("found %d `view()` call sites across %d non-test files, and there were %d when this "+
			"control was written. Below the floor the parser has stopped seeing the calls rather "+
			"than the calls having gone away, and the assertion below would pass by asking nothing",
			sites, files, sitesWhenWritten)
	}

	var offenders []string
	for key, byRecv := range perFunc {
		for recv, n := range byRecv {
			if n > 1 {
				offenders = append(offenders, fmt.Sprintf("%s loads %s.view() %d times", key, recv, n))
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) != 0 {
		t.Errorf("%s\n"+
			"One operation loads the published image once and uses that slice for its bounds check "+
			"and its access (decision 0058). A second load may name a different array than the one "+
			"the check approved — after a relocating grow, a different length and a different "+
			"pointer — so an access verified in bounds lands out of them. Bind the result to a local "+
			"and pass the slice down.\n"+
			"Two loads on *different* memories in one function are legitimate and are counted "+
			"separately, which is why `execMemoryCopy` is not listed here.",
			strings.Join(offenders, "\n"))
	}
}
