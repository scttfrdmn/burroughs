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
)

// TestEveryOperationLoadsAPublishedImageAtMostOnce is the structural control decisions 0058 and
// [0065][0065] rest on, and it is a control rather than a comment because the rule is invisible at the
// point it is broken.
//
// **The rule.** One operation loads the published image once and does its bounds check *and* its
// access against that one slice. Two loads in one operation is the defect the mechanism exists to
// prevent: the check approves the first array and the access lands in the second, which after a
// relocating grow or a drop is a different array of a different length — so `bs[ea:ea+n]` can be out
// of range on a bound that was verified. The published descriptor makes each load internally
// consistent; it cannot make two loads agree with each other.
//
// **Why nothing else catches it.** It is not a compile error, not a vet finding, and not observable on
// any corpus vector, because the second load returns the same array in every single-threaded run. The
// board would stay green at 0 fail while the property was gone, which is the shape a control is for.
//
// **The domain is derived rather than listed** — every non-test file in this package, every function in
// it — because the failure this exists to catch is an arm added *later*: a new bulk operation, a SIMD
// path, a memory64 arm. A list of today's call sites would inherit today's blind spot.
//
// # Four subjects and two accessors, which is why this is not `…MemoryOperation…` any more
//
// It was `TestEveryMemoryOperationLoadsTheImageAtMostOnce` while `memory` was the only subject.
// Decision 0065 published `table`, `elemInstance` and `dataInstance` through images too, and named
// all three accessors `view` *for this control's sake*: the predicate matches on the selector, so a
// shared name puts three new subjects inside one existing control instead of asking for a fourth one.
// The rename is the whole cost of that, and the domain is untouched.
//
// **`size` counts as a load, and that is the second half of the widening.** Each of the four `size()`
// methods is `uint64(len(x.view()))`, so a function that bounds against `seg.size()` and then reads
// `seg.view()` has taken two loads while showing this scan one textual `view`. Counting both selectors
// is exact rather than approximate, and the premise is checked on the *declarations*:
// `grep -n '^func (.*) \(view\|size\)(' *.go` over the non-test files returns eight lines, two per
// subject, so no unrelated type in this package can join the population by name. (Checked on the
// declarations and not on the call sites for grave-adjacent reasons #627 paid for: a call-site grep
// counts prose inside doc comments — *a grep measures text*.)
//
// **Two sites load in mutually exclusive branches and were hoisted rather than exempted**:
// `memory.size`'s i32/i64 arms in `exec.go` and `table.size`'s in `truncsat.go` each called `size()`
// once per arm, which is one load on every execution and two to a scan. Both now take one `sz :=`
// above the branch. That is the shape the rule asks for anyway, and it is the reason the widening cost
// no exemption list — *an exemption inherits none of the trigger's lessons*.
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
// restoring `execTableInit`'s pre-0065 shape (`seg.size()` for the bound, `seg.view()` for the copy)
// fails naming `execTableInit` and the receiver `seg`, which is the arm the `size` widening bought;
// blinding the selector match to a name nothing calls fails the floor at `found 0`, which is the
// vacuity this floor exists for.
//
// [0065]: ../../docs/decisions/0065-the-table-and-segment-headers-move-inside-published-images-because-a-field-that-cannot-be-named-needs-no-enumeration-to-confine-it.md
func TestEveryOperationLoadsAPublishedImageAtMostOnce(t *testing.T) {
	// The exact number is pinned beside the floor because a floor alone catches a moved file and never
	// a silent partial loss — six sites disappearing looks like a pass to a `> 0` check. It rose from
	// ten to this when 0065 added three subjects and `size` joined the predicate; *a floor is not a
	// census*, so the pin is the count and the comparison below is the floor. **Counted by running the
	// scan, not forecast**: the first draft pinned 46 from a hand estimate and the control refused it
	// at 28, which is the pin now — a floor written from a guess is a floor that fires on the guess.
	const sitesWhenWritten = 28

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
			// The accessors themselves are excluded, and only they: `size` *is* a `view` caller, so
			// counting it inside its own declaration would report every subject's `size` as a
			// double load. The exclusion is on the declaration's own name, so it cannot spread.
			if fn.Name.Name == "view" || fn.Name.Name == "size" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || len(call.Args) != 0 {
					return true
				}
				if sel.Sel.Name != "view" && sel.Sel.Name != "size" {
					return true
				}
				var recv strings.Builder
				if perr := printer.Fprint(&recv, fset, sel.X); perr != nil {
					t.Fatalf("printing the receiver of a %s() call in %s: %v", sel.Sel.Name, key, perr)
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
		t.Fatalf("found %d `view()`/`size()` call sites across %d non-test files, and there were %d "+
			"when this control was written. Below the floor the parser has stopped seeing the calls "+
			"rather than the calls having gone away, and the assertion below would pass by asking "+
			"nothing", sites, files, sitesWhenWritten)
	}

	var offenders []string
	for key, byRecv := range perFunc {
		for recv, n := range byRecv {
			if n > 1 {
				offenders = append(offenders, fmt.Sprintf("%s loads %s's image %d times", key, recv, n))
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) != 0 {
		t.Errorf("%s\n"+
			"One operation loads the published image once and uses that slice for its bounds check "+
			"and its access (decisions 0058 and 0065). A second load may name a different array than "+
			"the one the check approved — after a relocating grow, a different length and a different "+
			"pointer; after a drop, no array at all — so an access verified in bounds lands out of "+
			"them. Bind the result to a local and pass the slice down.\n"+
			"`size()` counts as a load because it is one: it is `uint64(len(x.view()))`. Bound against "+
			"`len(local)` instead.\n"+
			"Two loads on *different* subjects in one function are legitimate and are counted "+
			"separately, which is why `execMemoryCopy` is not listed here.",
			strings.Join(offenders, "\n"))
	}
}

// TestAPublishedImageIsImmutableOnceStored is decision 0065's witness for its three new subjects, and
// like `TestGrowPublishesAFreshImageRatherThanMutatingTheHeldOne` one file over it is written against
// the regression the decision *creates* rather than the defect it fixes.
//
// **Why that is the honest target.** #622's defect — `t.slots = grown`, `s.refs = nil` — has no
// single-threaded witness, because a Go slice is a value: a reader that already copied the header holds
// a valid pointer and length whatever the owning struct does next. A test that took the slice, grew or
// dropped, and read it back would pass identically on both engines. That is an analytic zero. The
// memory-safety property is about a header observed *mid-write*, which needs two threads and the race
// detector — `TestARelocatingTableGrowDoesNotRaceAConcurrentReader` below.
//
// **The live regression is an arm that assigns through `img.Load()`** — `t.img.Load().slots = grown`,
// `s.img.Load().refs = nil` — which compiles, passes every conformance vector, and restores the exact
// hazard, because the three words a reader is dereferencing are being written underneath it again. So
// the property asserted is *immutability of a published descriptor*: the image a reader held before the
// operation names the same array at the same length afterwards, and a *new* descriptor is what the
// operation published.
//
// One function over three subjects because the assertion is identical and only the fixture differs;
// four tests for one property would be the shape #34's grave warns about from the other direction —
// separate controls are for failures with unrelated causes, and these three share one.
//
// Watched die, per subject: `t.img.Store(&tabImage{slots: grown})` → `t.img.Load().slots = grown` fails
// the table arm's pointer assertion; `s.img.Store(droppedElem)` → `s.img.Load().refs = nil` fails the
// element arm's length assertion; the same on `dataInstance.drop` fails the data arm's.
func TestAPublishedImageIsImmutableOnceStored(t *testing.T) {
	t.Run("table.grow", func(t *testing.T) {
		// The plain form, whose slots the decoder fills with its synthesized `ref.null func` — no GC
		// gate needed, and the explicit-initializer form would add one for nothing this arm asks.
		in := invoke1t(t, `(module (func $f) (table 2 funcref))`)
		tab := in.tables[0]
		held := tab.img.Load()
		heldLen := len(held.slots)
		based := &held.slots[0]

		if got := tab.grow(3, ref{Null: true}); got != 2 {
			t.Fatalf("grow(3) = %d, want the previous size 2", got)
		}
		if tab.img.Load() == held {
			t.Errorf("`grow` published no new descriptor: `img` still holds %p after a grow that "+
				"reported success, so the new slots were written into the descriptor a reader may "+
				"already be dereferencing", held)
		}
		if len(held.slots) != heldLen {
			t.Errorf("the descriptor held across the grow changed length, %d to %d.\n"+
				"A published `tabImage` is immutable — that is the whole of decision 0065 — because "+
				"a reader holding it is dereferencing these three words. `grow` must build a new "+
				"`tabImage` and `Store` it, never assign through `img.Load()`", heldLen, len(held.slots))
		}
		if &held.slots[0] != based {
			t.Errorf("the descriptor held across a relocating grow now names a different array.\n" +
				"The abandoned array must stay named by the old descriptor for as long as any reader " +
				"holds it — that is what keeps it alive and in bounds, and it is why relocation is " +
				"memory-safe under 0065 where it was a use-after-free before")
		}
		if got := tab.size(); got != 5 {
			t.Errorf("size() = %d after growing 2 by 3, so the table is not answering from the new "+
				"descriptor", got)
		}
	})

	// Both drops, and the fixture is built by hand rather than instantiated: an *active* segment is
	// dropped during instantiation, so a module's own segments are already empty by the time a test
	// can hold their pre-drop image. A passive segment survives, which is what makes it the fixture.
	t.Run("elem.drop", func(t *testing.T) {
		seg := newElemInstance([]ref{{Null: true}, {Null: true}, {Null: true}})
		held := seg.img.Load()
		heldLen := len(held.refs)
		if heldLen != 3 {
			t.Fatalf("the fixture holds %d refs, want 3: a zero-length segment would make the "+
				"length assertion below pass without a drop having to leave anything alone", heldLen)
		}
		seg.drop()
		if len(held.refs) != heldLen {
			t.Errorf("the `elemImage` held across the drop changed length, %d to %d.\n"+
				"`drop` must *publish* the empty image, not clear the one a reader may hold: "+
				"pairing the new nil pointer with the old length is how a reader indexes off "+
				"address 0, which is what #622 filed", heldLen, len(held.refs))
		}
		if seg.img.Load() == held {
			t.Errorf("`drop` published no new descriptor, so it wrote through the held one")
		}
		if got := seg.size(); got != 0 {
			t.Errorf("size() = %d after a drop, want 0 — `Elem.size` of a dropped segment is 0", got)
		}
		// Dropping twice is legal and does nothing (`bulk.wast:261`), which is only true because the
		// dropped state is a value. Asserted here because the shared `droppedElem` is what makes it
		// allocation-free, and a reader could reasonably wonder whether sharing costs the property.
		seg.drop()
		if got := seg.size(); got != 0 {
			t.Errorf("size() = %d after a second drop, want 0", got)
		}
	})

	t.Run("data.drop", func(t *testing.T) {
		seg := newDataInstance([]byte{1, 2, 3, 4})
		held := seg.img.Load()
		heldLen := len(held.bytes)
		if heldLen != 4 {
			t.Fatalf("the fixture holds %d bytes, want 4", heldLen)
		}
		seg.drop()
		if len(held.bytes) != heldLen {
			t.Errorf("the `dataImage` held across the drop changed length, %d to %d — `drop` must "+
				"publish the empty image rather than clear the held one (#622)", heldLen, len(held.bytes))
		}
		if seg.img.Load() == held {
			t.Errorf("`drop` published no new descriptor, so it wrote through the held one")
		}
		if got := seg.size(); got != 0 {
			t.Errorf("size() = %d after a drop, want 0", got)
		}
	})
}

// TestARelocatingTableGrowDoesNotRaceAConcurrentReader is #622's memory-safety half for the table, and
// **its oracle is the race detector rather than any assertion in it** —
// `TestARelocatingGrowDoesNotRaceAConcurrentReader`'s argument one subject over, and it is repeated
// rather than cross-referenced because a reader who lands here needs to know what a green means.
//
// Without `-race` this asserts only that nothing panicked and that the reads answered in bounds, which
// the pre-0065 engine also managed almost always: a torn header is a *window*, not a certainty. Under
// `-race` the verdict is exact, because `t.slots = grown` is a write to a shared three-word field with a
// concurrent reader on it and the detector reports that deterministically once both goroutines have run.
// `make race` and CI's `race` step are where this test has an oracle; `make check` runs it as a smoke
// test and that is all.
//
// **Only the grow is witnessed, not the drops**, and that is a property of the mechanism rather than an
// omission: a drop publishes an *empty* image, so there is no second array for a reader to be left
// holding and no relocation to race. The drops' regression is the immutability arm above.
//
// **The reader is a guest `table.get`, not a call to `tab.get`**, because the claim is about the path
// the interpreter takes — resolve the table, load the image, bounds-check, access — and it is the
// *pair* of loads inside one operation that 0065 forbids. `ref.is_null` is there so the result is an
// i32 the harness can carry; a null slot is the expected answer for every index this test reads.
func TestARelocatingTableGrowDoesNotRaceAConcurrentReader(t *testing.T) {
	// Every `table.grow` relocates — a table reserves no capacity, which is 0065's decision 6 — so no
	// fixture check is needed to reach the relocating arm the way memory's twin needs one.
	in := invoke1t(t, `(module
	  (table 1 funcref)
	  (func (export "get") (result i32) (ref.is_null (table.get (i32.const 0))))
	  (func (export "up") (result i32) (table.grow (ref.null func) (i32.const 1))))`)
	if len(in.tables) != 1 || in.tables[0] == nil {
		t.Fatalf("expected one table, got %d", len(in.tables))
	}

	const (
		grows = 24
		reads = 2000
	)
	done := make(chan error, 2)
	go func() {
		for range reads {
			// Index 0 is in bounds at every size this test reaches, so a trap here is the engine
			// answering from a descriptor that does not match the table it is in.
			out, err := in.Invoke("get")
			if err != nil {
				done <- fmt.Errorf("concurrent table.get: %w", err)
				return
			}
			if out[0].Int32() != 1 {
				done <- fmt.Errorf("concurrent table.get read a non-null slot, so the read landed "+
					"somewhere no `ref.null func` was written: %+v", out[0])
				return
			}
		}
		done <- nil
	}()
	go func() {
		for i := range grows {
			out, err := in.Invoke("up")
			if err != nil {
				done <- fmt.Errorf("concurrent table.grow %d: %w", i, err)
				return
			}
			if got := out[0].Int32(); got < 0 {
				done <- fmt.Errorf("grow %d refused with %d, so the relocating arm stopped being "+
					"reached and the rest of this test asserts nothing", i, got)
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

	// The grows are serial with each other, so the final size is exact — which is *not* a property of
	// concurrent `grow`s in general, and 0065's coherence residual says so.
	if got := in.tables[0].size(); got != 1+grows {
		t.Errorf("after %d serial grows the table is %d slots, want %d", grows, got, 1+grows)
	}
}
