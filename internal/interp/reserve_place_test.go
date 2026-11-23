// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build (darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64) && !burroughs_noreserve

package interp

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// # The controls' premise is a free range at the cursor, and they establish it (#1284)
//
// Both controls below ask the kernel for the cursor's hint and assert that it was honoured. That premise has two
// halves. The first is a kernel that honours a free hint, which darwin and linux do (decision 0113). The second
// is a hint that is **free**, and that is not this package's to promise in a shared process.
//
// Go 1.26 randomizes the heap's base address by default (`runtime/malloc.go:mallocinit`, `randomizeHeapBase`), so
// a non-race heap starts anywhere in the low 64 TiB on amd64 and the low 128 TiB on arm64. Measured with go1.26.9
// over 4,000 processes each, 0.8% on linux/amd64 and 0.35% on darwin/arm64 put the heap within 512 GiB above
// reservationBase, which is about how far this package's run walks the cursor. When the cursor reaches the heap,
// its hint is refused and the reservation is a stray. Only an honoured hint moves the cursor (grave #1254), so
// every later reservation in the process is a stray too. That is the shape of all four of #1284's CI failures:
// every hint ignored, with the strays top-down on linux and just above the heap on darwin (#1292).
//
// The engine is within decision 0113 there, because a stray is counted and kept. The race band 0113 protects is
// untouched, because `randomizeHeapBase` is off under `-race`, the only mode that has the band. So the repair is
// the controls' premise, not the mechanism: each control first establishes a free range at the cursor, with
// requireAFreeRangeAtTheCursor, and then asserts on it exactly as before.

// cursorStep is how far requireAFreeRangeAtTheCursor moves the cursor past an occupied range per probe. It is a
// reservation's own largest size, so a step never jumps a gap that one reservation could have used.
const cursorStep = maxPages32 * pageSize

// cursorProbes bounds the walk. 64 steps is about 256 GiB of address space, far more than the few GiB a test
// process's heap occupies.
const cursorProbes = 64

// requireAFreeRangeAtTheCursor makes the next `size` bytes at the cursor free. If it cannot, it fails, saying the
// kernel did not honour a free hint, which is the half of the premise decision 0113 measured.
//
// It probes with probeAt, an address-only mapping, and releases the probe. If the range is free, nothing
// changes. If something this package did not place holds it (#1284: Go's randomized heap), the cursor moves up by
// cursorStep until a probe is honoured. **That move is one the engine's own rule permits**: placeNext only moves
// up, and every address it skips was unavailable to the engine anyway. The move and the occupant are logged, so a
// run that needed it says so.
//
// The probe and the control's reservations cannot be one critical section, because the reservations take placeMu
// themselves. Nothing in this package maps at the cursor in between, and no test here runs in parallel. The one
// gap left is the heap growing into the probed range in that window.
// probeAt maps n bytes at hint with no access and no commit, so it asks only whether that address range is free.
//
// **It is not `mmapAt`** (#1348). The probe used the engine's own mapping, which is `PROT_READ|PROT_WRITE` and
// committed, so a 16 GiB probe was subject to the kernel's overcommit accounting. A runner with little memory
// refused it with ENOMEM before the address was ever considered (PR #1347's run 45126427018, ubuntu-24.04), and
// the control failed for a reason unrelated to its subject. `PROT_NONE` with `MAP_NORESERVE` commits nothing, so
// a refusal here is about the address space, which is what the probe means to read.
//
// NEUTER: probe with `mmapAt` again, and TestTheCursorProbeAsksOnlyAboutTheAddress fails on its ENOMEM stand-in.
func probeAt(hint, n uintptr) (uintptr, syscall.Errno) {
	addr, _, errno := syscall.Syscall6(syscall.SYS_MMAP, hint, n,
		syscall.PROT_NONE, syscall.MAP_ANON|syscall.MAP_PRIVATE|syscall.MAP_NORESERVE, ^uintptr(0), 0)
	return addr, errno
}

func requireAFreeRangeAtTheCursor(t *testing.T, size uintptr) {
	t.Helper()
	placeMu.Lock()
	defer placeMu.Unlock()
	from := placeNext
	var occupied []string
	for range cursorProbes {
		hint := placeNext
		addr, errno := probeAt(hint, size)
		if errno != 0 {
			t.Fatalf("an address-only probe of %#x bytes at the cursor %#x was refused outright: %v", size, hint, errno)
		}
		p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
		if err := unmapPlaced(unsafe.Slice((*byte)(p), size)); err != nil {
			t.Fatalf("releasing the cursor probe at %#x: %v", addr, err)
		}
		if addr == hint {
			if hint != from {
				t.Logf("the cursor's range at %#x was held by a mapping this package did not place, so the "+
					"control moved the cursor up to %#x, the first free range (#1284). Occupants: %s",
					from, hint, strings.Join(occupied, " | "))
			}
			return
		}
		occupied = append(occupied, mappingsIn(hint, hint+size))
		placeNext = hint + cursorStep
	}
	t.Fatalf("the kernel ignored %d hints of %#x bytes from %#x upward, %#x apart, so it does not honour a free "+
		"hint here, which is the premise decision 0113's controls state. Mappings at each hint: %s",
		cursorProbes, size, from, cursorStep, strings.Join(occupied, " | "))
}

// mappingsIn names what is mapped in [lo, hi), from `/proc/self/maps` on linux. darwin has no such file, and
// `vmmap` is a process to spawn rather than a file to read, so there it names nothing and says so.
func mappingsIn(lo, hi uintptr) string {
	if runtime.GOOS != "linux" {
		return fmt.Sprintf("[%#x, %#x): not named on %s", lo, hi, runtime.GOOS)
	}
	b, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return fmt.Sprintf("[%#x, %#x): /proc/self/maps unreadable: %v", lo, hi, err)
	}
	var in []string
	for line := range strings.Lines(string(b)) {
		span, _, _ := strings.Cut(line, " ")
		s, e, ok := strings.Cut(span, "-")
		if !ok {
			continue
		}
		start, err1 := strconv.ParseUint(s, 16, 64)
		end, err2 := strconv.ParseUint(e, 16, 64)
		if err1 == nil && err2 == nil && uintptr(end) > lo && uintptr(start) < hi {
			in = append(in, strings.TrimSpace(line))
		}
	}
	if len(in) == 0 {
		return fmt.Sprintf("[%#x, %#x): nothing in /proc/self/maps", lo, hi)
	}
	return fmt.Sprintf("[%#x, %#x): %s", lo, hi, strings.Join(in, "; "))
}

// TestAReservationIsPlacedAtOrAboveTheBase is decision 0113's placement control. Three 4 GiB reservations land at
// or above reservationBase, each at the end of the one before, and none is counted below the base.
//
// **What it guards:** a refactor back to `syscall.Mmap` would put reservations wherever the kernel chooses, which
// on darwin is upward from 0x70_0000_0000 and into the race heap's band. Nothing else would notice until a rare
// `-race` fatal came back. This fails at once instead.
func TestAReservationIsPlacedAtOrAboveTheBase(t *testing.T) {
	const n = 4 << 30
	// NEUTER (#1284): delete this call, and TestThePlacementControlsHoldWhenSomethingElseHoldsTheCursor fails.
	requireAFreeRangeAtTheCursor(t, 3*n)
	below := reservationOffHint.Load()
	var held [][]byte
	t.Cleanup(func() {
		for _, bs := range held {
			if err := unmapReservation(bs); err != nil {
				t.Errorf("releasing a placed reservation: %v", err)
			}
		}
	})
	var prev uintptr
	for i := range 3 {
		bs, ok := mapReservation(n)
		if !ok {
			t.Fatalf("reservation %d of %d bytes was refused", i, n)
		}
		held = append(held, bs)
		base := uintptr(unsafe.Pointer(&bs[0]))
		if base < reservationBase {
			t.Fatalf("reservation %d landed at %#x, below the base %#x: decision 0113's placement is not in effect",
				i, base, reservationBase)
		}
		if i > 0 && base != prev+n {
			t.Errorf("reservation %d landed at %#x, not at the end of the one before (%#x)", i, base, prev+n)
		}
		prev = base
	}
	if got := reservationOffHint.Load() - below; got != 0 {
		t.Errorf("%d reservation(s) counted off-hint on a kernel measured to honour the hint", got)
	}
}

// TestOneIgnoredHintDoesNotMoveTheCursor is grave #1254's witness. A kernel that ignores one hint and places the
// mapping high, as Linux's top-down default does, must not move the cursor: the next reservation still asks for,
// and gets, the address the cursor named before the stray.
//
// The stray is stood in by the `mmapAt` seam, because no kernel this was measured on ignores a hint on demand.
// It must land **above** the cursor, as Linux's top-down placement did on CI's x86 runner: a stray below the
// cursor was already counted, and only one above it moved the cursor. So the stand-in maps for real 32 TiB above
// the hint it was given, which is a real mapping released like any other.
func TestOneIgnoredHintDoesNotMoveTheCursor(t *testing.T) {
	const n = 4 << 30
	kernel := mmapAt
	t.Cleanup(func() { mmapAt = kernel })

	var held [][]byte
	t.Cleanup(func() {
		for _, bs := range held {
			if err := unmapReservation(bs); err != nil {
				t.Errorf("releasing a reservation: %v", err)
			}
		}
	})

	// NEUTER (#1284): delete this call, and TestThePlacementControlsHoldWhenSomethingElseHoldsTheCursor fails.
	requireAFreeRangeAtTheCursor(t, n)
	placeMu.Lock()
	want := placeNext
	placeMu.Unlock()
	strays := reservationOffHint.Load()

	ignored := false
	mmapAt = func(hint, size uintptr) (uintptr, syscall.Errno) {
		if !ignored {
			ignored = true
			return kernel(hint+1<<45, size) // placed elsewhere, above the cursor, not at the hint
		}
		return kernel(hint, size)
	}

	stray, ok := mapReservation(n)
	if !ok {
		t.Fatal("the stray reservation was refused")
	}
	held = append(held, stray)
	if got := reservationOffHint.Load() - strays; got != 1 {
		t.Fatalf("the ignored hint counted %d stray(s), want 1", got)
	}

	next, ok := mapReservation(n)
	if !ok {
		t.Fatal("the reservation after the stray was refused")
	}
	held = append(held, next)
	// **Which cause, read rather than assumed** (#1292). The message used to name the cursor moving as the only
	// cause, but a refused hint looks the same from the address alone. A second stray in the count says the kernel
	// refused the cursor's own hint, and its absence says the reservation was honoured at a cursor that had moved.
	if got := uintptr(unsafe.Pointer(&next[0])); got != want {
		if reservationOffHint.Load()-strays == 2 {
			t.Errorf("after one ignored hint the next reservation landed at %#x: the kernel refused the cursor's "+
				"hint %#x as well. Mappings there: %s", got, want, mappingsIn(want, want+n))
		} else {
			t.Errorf("after one ignored hint the next reservation landed at %#x, not at the cursor's hint %#x: the "+
				"stray moved the cursor (grave #1254)", got, want)
		}
	}
}

// TestThePlacementControlsHoldWhenSomethingElseHoldsTheCursor is #1284's witness. A mapping this package did not
// place sits inside the range each control is about to use, as Go's randomized heap did on CI, and each control
// must still pass. It passes by moving to the first free range above, so the cursor must end above the occupant.
//
// The occupant is stood in by one 64 MiB mapping, a Go heap arena's size, placed 1 GiB above the cursor. That is
// inside both controls' ranges, and it is not at the cursor itself, because CI's heap had no reason to start
// exactly there.
func TestThePlacementControlsHoldWhenSomethingElseHoldsTheCursor(t *testing.T) {
	const arena = 64 << 20
	for name, control := range map[string]func(*testing.T){
		"TestAReservationIsPlacedAtOrAboveTheBase": TestAReservationIsPlacedAtOrAboveTheBase,
		"TestOneIgnoredHintDoesNotMoveTheCursor":   TestOneIgnoredHintDoesNotMoveTheCursor,
	} {
		requireAFreeRangeAtTheCursor(t, 16<<30)
		placeMu.Lock()
		at := placeNext + 1<<30
		addr, errno := mmapAt(at, arena)
		placeMu.Unlock()
		if errno != 0 || addr != at {
			t.Fatalf("placing the stand-in occupant at %#x: got %#x, %v", at, addr, errno)
		}
		p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
		occupant := unsafe.Slice((*byte)(p), arena)
		ok := t.Run(name, control)
		if err := unmapPlaced(occupant); err != nil {
			t.Fatalf("releasing the stand-in occupant: %v", err)
		}
		if !ok {
			t.Errorf("%s failed with a mapping it did not place inside its range at %#x", name, at)
		}
		placeMu.Lock()
		after := placeNext
		placeMu.Unlock()
		if after < at+arena {
			t.Errorf("%s left the cursor at %#x, not above the occupant at [%#x, %#x)", name, after, at, at+arena)
		}
	}
}

// TestTheCursorProbeAsksOnlyAboutTheAddress is #1348's witness. The engine's mapping seam is replaced by a kernel
// that refuses, with ENOMEM, any mapping over 1 GiB, as a runner short of memory refused #1284's 16 GiB probe on
// CI. The probe must still read the cursor's range, because it asks about the address and commits nothing. So
// requireAFreeRangeAtTheCursor returns rather than failing.
//
// It runs the helper in a subtest, so a Fatalf there is a failed subtest, not this test's own exit.
func TestTheCursorProbeAsksOnlyAboutTheAddress(t *testing.T) {
	kernel := mmapAt
	t.Cleanup(func() { mmapAt = kernel })
	mmapAt = func(hint, size uintptr) (uintptr, syscall.Errno) {
		if size > 1<<30 {
			return 0, syscall.ENOMEM
		}
		return kernel(hint, size)
	}
	if !t.Run("probe", func(t *testing.T) { requireAFreeRangeAtTheCursor(t, 16<<30) }) {
		t.Errorf("the cursor probe failed when committed mappings over 1 GiB are refused: it read the commit " +
			"limit, not the address range (#1348)")
	}
}
