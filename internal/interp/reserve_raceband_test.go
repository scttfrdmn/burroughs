// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race && (darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64) && !burroughs_noreserve

package interp

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// TestHeldReservationsDoNotStarveTheRaceHeap is decision 0113's witness, and it is deterministic where the defect
// it guards was rare (#1218). Under `-race` the runtime keeps its heap in [0x00c0_0000_0000, 0x00e0_0000_0000) and
// has no fallback. So a child process holds enough 4 GiB reservations to cover that band from darwin's starting
// address, then grows its heap, and must exit cleanly.
//
// **Its neuter runs beside it, every time.** The same child with the reservations made by `syscall.Mmap`, the
// kernel's placement and decision 0076's before 0113, must die on darwin. That is what was measured there
// (#1218). Without that arm a passing witness would not show that it reaches the failure at all.
//
// On linux the kernel places mappings top-down from far above the band, so the neuter is not expected to die
// there, and its outcome is logged rather than asserted. The placed arm is asserted on both.
func TestHeldReservationsDoNotStarveTheRaceHeap(t *testing.T) {
	if mode := os.Getenv("BURROUGHS_RACEBAND_CHILD"); mode != "" {
		holdThenGrow(mode)
		return
	}
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHeldReservationsDoNotStarveTheRaceHeap$", "-test.count=1")
		cmd.Env = append(os.Environ(), "BURROUGHS_RACEBAND_CHILD="+mode)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("placed")
	if err != nil || !strings.Contains(out, "raceband: survived") {
		t.Fatalf("with decision 0113's placement, a child holding the reservations did not survive heap growth "+
			"(err %v):\n%s", err, tail(out))
	}

	out, err = run("kernel")
	died := err != nil && strings.Contains(out, "fatal error:")
	switch {
	case runtime.GOOS == "darwin" && !died:
		t.Fatalf("the neuter (kernel placement) survived on darwin, where it was measured to die. The witness "+
			"no longer reaches the failure it guards, so its placed arm's pass says nothing (err %v):\n%s", err, tail(out))
	case runtime.GOOS != "darwin":
		t.Logf("neuter on %s: died=%v (not asserted: this kernel places mappings above the band)", runtime.GOOS, died)
	}
}

// holdThenGrow is the child: 160 reservations of 4 GiB, which from darwin's 0x70_0000_0000 run past the band's end
// at 0xe0_0000_0000, held live, then 2 GiB of heap, which needs arenas the band has to supply.
func holdThenGrow(mode string) {
	const n, count = 4 << 30, 160
	held := make([][]byte, 0, count)
	for range count {
		var bs []byte
		var ok bool
		if mode == "placed" {
			bs, ok = mapReservation(n)
		} else {
			var err error
			bs, err = syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
			ok = err == nil
		}
		if !ok {
			println("raceband: a reservation was refused, so the band was not covered")
			os.Exit(3)
		}
		held = append(held, bs)
	}
	heap := make([][]byte, 0, 64)
	for range 64 {
		heap = append(heap, make([]byte, 32<<20))
	}
	runtime.KeepAlive(held)
	runtime.KeepAlive(heap)
	println("raceband: survived")
	os.Exit(0)
}

// tail is the child's cause of death, then the end of its output. The fatal line comes first because a dump's
// tail is goroutine stacks, which are cut off long before its header is reached.
func tail(s string) string {
	cause := "no `fatal error:` line"
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "fatal error:") || strings.HasPrefix(line, "raceband:") {
			cause = line
			break
		}
	}
	if len(s) > 2000 {
		s = "…" + s[len(s)-2000:]
	}
	return "cause: " + cause + "\n" + s
}
