// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestMain prints decision 0076's three reservation counters after the whole package has run, which is the
// **board figure** `internal/interp/reserve.go:reservationUnavailable` says it is read as.
//
// **The figure is a claim about the host, not about this code, and that is why it is printed rather than
// asserted.** Several hundred live memories each reserving just under 4 GiB is address-space pressure, and
// a threshold here would be a bound on the runner rather than on the engine — it would go red on a
// container with an address-space rlimit and stay green on a host that had quietly stopped mapping
// anything. So this reports and does not gate. What *is* asserted lives in the two directions of the seam:
// `TestAMemoryReservesAddressSpaceRatherThanCommittingIt` on the mapping arm and
// `TestTheFallbackPathIsTheOneWithoutAMapping` on the other.
//
// **The unavailable count is not all involuntary**, and reading it as pressure without that correction
// would over-report: this package refuses on purpose once per test that names the relocating arm, so a
// floor of those deliberate refusals is inside every number this prints. The line names the floor beside
// the count for exactly that reason — *a floor is not a census*, and a count with a known deliberate
// component is not pressure until the component is subtracted.
//
// **The clause names `refuseReservation` and not a caller of it, because naming a caller is what broke it.**
// The line read *"deliberate refusals by withoutReservation"* until #671's arm F, which needed the same
// refusal from a `*testing.B` and therefore could not call that helper — so it assigned `reserveMapping` a
// second closure of its own, which counted nothing. The run printed `unavailable=7 (of which 0 are
// deliberate refusals)` on a package where **all seven were deliberate**: precisely the over-reporting the
// paragraph above exists to prevent, re-entered through the one direction its own wording could not see.
// *A comment's caller list is not the call graph.* The repair is that there is now exactly one closure, both
// seams install it, and `TestTheDeliberateRefusalSeamIsTheOnlyOne` holds that to the source rather than to
// this comment.
//
// On the `!unix` ports every memory is on the fallback and this line is the M-1 non-conformance figure
// `internal/interp/reserve_other.go` promises the counter would make readable.
func TestMain(m *testing.M) {
	code := m.Run()
	fmt.Printf("0076 reservations: unavailable=%d (of which %d are deliberate refusals by "+
		"refuseReservation), declined=%d, released=%d, release failures=%d\n",
		reservationUnavailable.Load(), deliberateRefusals.Load(), reservationDeclined.Load(),
		reservationReleased.Load(), reservationReleaseFailed.Load())
	os.Exit(code)
}

// deliberateRefusals counts this package's own refusals, so `TestMain`'s line can name the part of the
// unavailable count that the tests caused on purpose.
var deliberateRefusals atomic.Uint64

// refuseReservation is the **only** closure `reserveMapping` may be replaced by, and its being one function
// rather than one per caller is the whole of the repair described in `TestMain`'s third paragraph: a second
// closure that forgot the counter is not a visible defect, because the number it corrupts is a number
// nobody can independently derive.
func refuseReservation(int) ([]byte, bool) { deliberateRefusals.Add(1); return nil, false }

// withoutReservation makes `reserveMapping` refuse for one test and restores it.
//
// **Every test whose subject is the reallocating grow needs this, and that is decision 0076's shape rather
// than a testing inconvenience.** A memory reserved to its ceiling never relocates, so the arms ADR 0058's
// publication and ADR 0073's refusal are about have no population on the mapping path — and the tests that
// name those arms already guard against exactly this, by asserting `cap == len` before they grow. Those
// guards *fired* when the mapping landed, which is the strongest evidence the mechanism is live that this
// slice has: the fixtures said, unprompted, that the array they expected to move no longer moves.
//
// So the repair is to point them at the path the arm still lives on rather than to weaken them. The
// fallback is not a hypothetical: it is the whole of `windows`, `plan9` and the wasm ports, and any host
// whose overcommit policy or address-space rlimit refuses a 4 GiB mapping.
//
// The restore is a `t.Cleanup` rather than a `defer` in each caller, because a `t.Fatalf` between a manual
// set and a `defer` would leave the rest of the package running against the fallback — which fails nothing
// and quietly deletes the mapping path's coverage. Safe because no test in this package calls
// `t.Parallel()`, a premise `internal/interp/boundary.go` and `internal/interp/host.go` already rely on.
func withoutReservation(t *testing.T) {
	t.Helper()

	was := reserveMapping
	reserveMapping = refuseReservation
	t.Cleanup(func() { reserveMapping = was })
}

// TestTheFallbackPathIsTheOneWithoutAMapping is the other half of *assert the arms differ*: it drives the
// same constructor with the primitive refusing and asserts the memory came out on the allocator's path,
// counted.
//
// **Without this the seam is unverified in both directions.** A `reserveMapping` that returned success
// while doing nothing useful, or a `withoutReservation` that failed to take effect, would leave every test
// in `internal/interp/reserve_unix_test.go` passing and every test that relies on the helper asserting the
// wrong arm. This one is untagged and never skips, so it is also the whole of what the `!unix` ports assert
// about decision 0076 — there the fallback is not an arm, it is the mechanism.
func TestTheFallbackPathIsTheOneWithoutAMapping(t *testing.T) {
	withoutReservation(t)

	before := reservationUnavailable.Load()
	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: 4096, HasMax: true}})
	if err != nil {
		t.Fatalf("newMemory: %v", err)
	}
	if got := reservationUnavailable.Load() - before; got != 1 {
		t.Fatalf("reservationUnavailable moved by %d, want 1: a memory that got no mapping is "+
			"exactly what this counter is for, and an uncounted degradation to O(size) growth "+
			"is indistinguishable from conformance from outside", got)
	}
	if img := mem.img.Load().bytes; cap(img) != len(img) {
		t.Errorf("the fallback gave a %d-byte memory %d bytes of capacity, want no reservation: "+
			"an unshared memory with no declaration to reserve against takes `make([]byte, n)`",
			len(img), cap(img))
	}
	if mem.noMove {
		t.Errorf("noMove is set on a memory that reserved nothing, which would put every " +
			"growth on the engine-limit refusal arm rather than on the relocating one")
	}
}

// TestAMemoryWithNoRoomToGrowIsAskedForNothing is decision 0076's rule 3, and it is here rather than in
// `internal/interp/reserve_unix_test.go` because it is the one arm whose answer is the same on every port.
//
// **What it certifies is the distinction between the two counters**, which is otherwise only a paragraph:
// `reservationDeclined` moves and `reservationUnavailable` does not, because the engine never asked. A
// single counter over both populations would read identically here and on a host that had refused, and
// those have opposite repairs — one is a rule working as written, the other is address-space pressure.
//
// The half of rule 3 that matters most is **not** tested here and cannot cheaply be: a memory64 declaring a
// minimum above 4 GiB is the case where declining costs something real (it keeps relocate-and-copy, so
// §8 M-1 is unmet for that module), and asserting it means committing 4 GiB. This is the affordable half —
// a max at or below the minimum, where declining costs nothing because `grow` refuses one check earlier.
func TestAMemoryWithNoRoomToGrowIsAskedForNothing(t *testing.T) {
	declined := reservationDeclined.Load()
	unavailable := reservationUnavailable.Load()

	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 2, Max: 2, HasMax: true}})
	if err != nil {
		t.Fatalf("newMemory: %v", err)
	}
	if got := reservationDeclined.Load() - declined; got != 1 {
		t.Errorf("reservationDeclined moved by %d, want 1: a memory whose max equals its minimum "+
			"has nothing to reserve into, and rule 3 is the arm that says so", got)
	}
	if got := reservationUnavailable.Load() - unavailable; got != 0 {
		t.Errorf("reservationUnavailable moved by %d, want 0: nothing was asked for here, and a "+
			"counter that cannot tell a declined reservation from a refused one reports host "+
			"address-space pressure that does not exist", got)
	}
	if img := mem.img.Load().bytes; cap(img) != len(img) {
		t.Errorf("a declined memory has %d bytes of capacity behind %d of length, want none: "+
			"rule 3 sends it to `make([]byte, n)`", cap(img), len(img))
	}
	if mem.noMove {
		t.Error("noMove is set on a memory that reserved nothing, which would turn its every " +
			"growth into the engine-limit refusal rather than leaving it the relocating path — " +
			"the narrowing rule 3 exists to prevent")
	}
	if got := mem.grow(1, nil); got != -1 {
		t.Errorf("grow returned %d, want -1: the premise that declining costs this population "+
			"nothing is that `grow` refuses past `limits.Max` before a reservation could matter", got)
	}
}

// TestNoExportedMethodReturnsASliceAliasingAMemory is the property the cleanup's safety rests on, as a
// control rather than as a sentence in a comment.
//
// **The failure it exists to prevent is a use-after-free with the GC's help.** `runtime.AddCleanup` fires
// when the `*memory` is unreachable. An embedder holding a subslice of the backing array keeps the *bytes*
// reachable and the `*memory` not, so the cleanup unmaps memory the embedder is still reading — and reading
// unmapped address space is a `SIGSEGV` the Go runtime does not turn into a panic. Today
// `internal/interp/host.go:Caller.Read` copies, which is what makes the whole scheme sound; this fires if
// a future edit makes it return a window instead, whatever the motivation.
//
// **The domain is derived, not listed.** The methods that could hand bytes out come from reflecting over
// the boundary types rather than from a list in this test, because *a comment's caller list is not the call
// graph*: a new exported `[]byte`-returning method would otherwise be born uncovered, and the one that
// mattered would be the one somebody added for performance. Every such method must have a probe here, and
// the absence of a probe is a failure rather than a gap.
func TestNoExportedMethodReturnsASliceAliasingAMemory(t *testing.T) {
	var got []byte
	in := hostLink(t, `(module
	  (import "h" "peek" (func $peek))
	  (memory 1 64)
	  (func (export "go") (call $peek)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"peek": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				bs, err := c.Read(0, pageSize)
				got = bs
				return nil, err
			}),
		}))

	img := in.mems[0].img.Load().bytes
	base := uintptr(unsafe.Pointer(&img[0]))
	end := base + uintptr(cap(img))

	// probes covers, by method name, every exported way out. `Caller.Read` is the only one today.
	probes := map[string]func() []byte{
		"Read": func() []byte {
			if _, err := in.Invoke("go"); err != nil {
				t.Fatalf("invoke: %v", err)
			}
			return got
		},
	}

	// The derived domain: exported methods on the boundary types whose results include a []byte.
	byteSlice := reflect.TypeOf([]byte(nil))
	for _, rt := range []reflect.Type{reflect.TypeOf(&Caller{}), reflect.TypeOf(&Instance{})} {
		for i := range rt.NumMethod() {
			m := rt.Method(i)
			hands := false
			for j := range m.Type.NumOut() {
				if m.Type.Out(j) == byteSlice {
					hands = true
				}
			}
			if !hands {
				continue
			}
			if _, ok := probes[m.Name]; !ok {
				t.Errorf("%s.%s returns a []byte and no probe here reads it.\n"+
					"Every exported method that can hand an embedder bytes out of a "+
					"memory is in this control's domain, because decision 0076's "+
					"cleanup is safe only while none of them aliases the backing "+
					"array. Add a probe — do not narrow the domain",
					rt.Elem().Name(), m.Name)
			}
		}
	}
	if len(probes) == 0 {
		t.Fatal("no probe reached a memory's bytes, so this control asserts nothing")
	}

	for name, probe := range probes {
		bs := probe()
		if len(bs) != pageSize {
			t.Errorf("%s returned %d bytes, want %d: the probe is not reading the whole page "+
				"and an aliasing window might sit outside what it looked at", name, len(bs), pageSize)
		}
		if len(bs) == 0 {
			continue
		}
		if p := uintptr(unsafe.Pointer(&bs[0])); p >= base && p < end {
			t.Errorf("%s returned a slice aliasing the memory's backing array.\n"+
				"That is a use-after-free once `runtime.AddCleanup` unmaps the reservation: "+
				"the bytes stay reachable, the `*memory` does not, and the embedder reads "+
				"unmapped address space — a SIGSEGV the Go runtime does not turn into a "+
				"panic. The boundary must copy: see decision 0076's lifetime section", name)
		}
	}
}

// TestTheDeliberateRefusalSeamIsTheOnlyOne asserts that every assignment to `reserveMapping` in this
// package's tests installs `refuseReservation` or restores a saved value, and that the two balance.
//
// **It reads the source because the defect it names is invisible to every other kind of check.** A private
// refusal closure works perfectly: the arm it serves measures the fallback, its guards pass, and the only
// casualty is `TestMain`'s exculpatory count — a figure that goes *down* silently while the count it
// qualifies stays the same. Nothing runs red. A reader then subtracts the wrong floor from a number whose
// whole purpose is to be a floor-corrected one, and concludes the host is under address-space pressure it is
// not under. That happened once, in the same run that produced #671's measured extent, so this control's
// population is one and it is not hypothetical.
//
// **It parses rather than greps, and the first draft's failure is why.** Written as a line scan for
// `reserveMapping = `, it reported four defects and none of them were real: two were restores whose
// single-line closure put `}()` inside the captured right-hand side, and **two were its own error message and
// its own `strings.Cut` argument** — the control matching the token in its own source. *Measure with the
// instrument, not a regex*, and a string literal is exactly what a parser cannot mistake for code. The
// heuristic repair available at that point — trim the trailing punctuation, require an identifier — would
// have left a scanner that was right about today's four lines by adjustment.
//
// It scans the test files because `reserveMapping`'s only non-test assignment is its declaration, and a scan
// that had to skip that would be a scan with an exemption — *an exemption inherits none of the trigger's
// lessons*.
func TestTheDeliberateRefusalSeamIsTheOnlyOne(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("globbing this package's test files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no *_test.go matched in this package, so this control scanned nothing and " +
			"agreed with itself: an empty population is not a clean one")
	}

	fset := token.NewFileSet()
	var installs, restores int
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); !ok || id.Name != "reserveMapping" {
					continue
				}
				if i >= len(assign.Rhs) {
					continue
				}
				where := fset.Position(assign.Pos())
				rhs, ok := assign.Rhs[i].(*ast.Ident)
				switch {
				case ok && rhs.Name == "refuseReservation":
					installs++
				case ok && rhs.Name == "was":
					restores++
				default:
					t.Errorf("%s assigns reserveMapping a %T, want the shared "+
						"`refuseReservation` (or `was`, restoring).\n"+
						"A refusal closure written here counts nothing, so "+
						"`TestMain`'s \"of which N are deliberate refusals\" "+
						"under-reports by however many times this seam fires — "+
						"and the unavailable count it qualifies then reads as "+
						"host pressure rather than as this package's own doing.",
						where, assign.Rhs[i])
				}
			}
			return true
		})
	}

	// Not a count floor: **every install must have a restore**, because an install that leaks runs the
	// rest of the package on the fallback, which fails nothing and silently deletes the mapping arm's
	// coverage. The equality is the assertion; that both are non-zero is the vacuity guard under it.
	if installs == 0 || restores == 0 {
		t.Errorf("found %d install(s) and %d restore(s) of reserveMapping across %d test file(s), "+
			"want both seams present (`withoutReservation` and the ladder's fallback arm): a "+
			"scan that finds nothing to check passes by asking nothing",
			installs, restores, len(files))
	}
	if installs != restores {
		t.Errorf("reserveMapping is installed %d time(s) and restored %d time(s).\n"+
			"An unrestored refusal is not a local defect: every later test in this package "+
			"then measures the allocator's fallback while its name claims the mapping, and "+
			"the arms that assert `cap == len` before growing are exactly the ones that "+
			"would keep passing", installs, restores)
	}
	t.Logf("parsed %d test file(s): %d install(s), %d restore(s), all through the counted seam",
		len(files), installs, restores)
}
