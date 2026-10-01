// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_scanlane && !burroughs_endtable

package interp

import (
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestLazyTableIsPublishedSafely is ruling 3's witness: **a lazily built pairing table is shared mutable state
// across agents, and must be published rather than written in place.**
//
// # Why this exists at all
//
// Lanes C and D fill their tables on first entry. This is the threaded tier and several agents run the same
// function at once, so two agents entering a cold body race to build its table — and a table written into
// shared memory while another agent reads it is a data race whichever agent wins. The project's own recorded
// lesson is the frame: *synchronising shared state does not ask whether it should be shared.* Here it must be
// shared, because a table built per agent would defeat the mechanism, so it must be **published**.
//
// # What it does NOT witness, stated because the gap is the interesting part
//
// The real subject is **two guest agents entering one cold body at once**, and no committable guest can produce
// that: it needs `GOEXPERIMENT=burroughsspawn`, a `burroughs spawn` import this package cannot supply, and a
// multi-megabyte test binary. This is the same limitation `TestFDTableIsSafeUnderConcurrentHostCalls` records
// for the fd table, and the same resolution: drive the mechanism from several **host** goroutines, which
// performs the same memory operations in the same order without the guest. The guest-level version belongs to
// the fork's sweep.
//
// # It is only a witness under `-race`
//
// Without the detector an unsynchronised map write is silently sometimes-fine, and with enough luck even a
// plain Go map survives a short run. `make ci` runs the suite with the detector on, and the injection that
// births this test is run under it explicitly.
func TestLazyTableIsPublishedSafely(t *testing.T) {
	// A body with real structure, so the table has something to hold and a partially published one would
	// differ from a complete one.
	body := make([]binary.Instr, 0, 256)
	for range 32 {
		body = append(body,
			binary.Instr{Op: opBlock},
			binary.Instr{Op: opLoop},
			binary.Instr{Op: opEnd},
			binary.Instr{Op: opEnd},
		)
	}
	// **The function must live in a MODULE now, and that is the mechanism change showing up in its witness.**
	// The earlier store was keyed by the body's data pointer and needed no module; the slot store indexes
	// `mod.Funcs` by the function's position, so a bare `&binary.Func{...}` has no slot and the witness would
	// exercise nothing. Handing the instance a real module is what keeps this arm on the real path — and
	// building it the other way is how the nil-module guard in `funcSlot` was found.
	mod := &binary.Module{Funcs: []binary.Func{{Body: body}}}
	fn := &mod.Funcs[0]
	in := &Instance{mod: mod}

	// **Every goroutine asks for the SAME cold body at once**, which is the arrangement the ruling names.
	// Agents are started together rather than in sequence so the first-entry window is contended rather
	// than merely visited.
	const agents = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	results := make([][]int32, agents)
	for a := range agents {
		done.Add(1)
		go func(a int) {
			defer done.Done()
			start.Wait()
			tbl := in.frameEnds(fn)
			// Reading every slot is what turns a half-published table into an observable difference rather
			// than a latent one: a reader that only took the header would miss a tail that was still zero.
			for pc := range body {
				if body[pc].Op != opBlock && body[pc].Op != opLoop {
					continue
				}
				end, err := endOf(body, tbl, pc)
				if err != nil {
					t.Errorf("agent %d: endOf(%d): %v", a, pc, err)
					return
				}
				results[a] = append(results[a], int32(end))
			}
		}(a)
	}
	start.Done()
	done.Wait()

	// **Every agent must have seen the same pairing.** A table published half-built would give one agent a
	// different answer from another, which no single-agent assertion can see.
	if len(results[0]) == 0 {
		t.Fatal("no agent resolved any header, so every comparison below is between empty slices")
	}
	for a := 1; a < agents; a++ {
		if len(results[a]) != len(results[0]) {
			t.Fatalf("agent %d resolved %d headers, agent 0 resolved %d", a, len(results[a]), len(results[0]))
		}
		for i := range results[a] {
			if results[a][i] != results[0][i] {
				t.Errorf("agent %d and agent 0 disagree about header %d: %d vs %d — a table was observed "+
					"while it was still being filled", a, i, results[a][i], results[0][i])
			}
		}
	}

	// And the pairing is correct, not merely agreed: sixteen agents agreeing on a wrong answer is still wrong,
	// and the scan is the authority both lanes fall back to.
	for i, pc := range structuralPCs(body) {
		want, err := matchEnd(body, pc)
		if err != nil {
			t.Fatalf("matchEnd(%d): %v", pc, err)
		}
		if int(results[0][i]) != want {
			t.Errorf("header %d resolved to %d, the scan says %d: the agents agree on a wrong pairing",
				pc, results[0][i], want)
		}
	}
	t.Logf("PUBLICATION %d agents, %d header(s) each, all agreeing and all matching the scan",
		agents, len(results[0]))
}

func structuralPCs(body []binary.Instr) []int {
	var out []int
	for pc := range body {
		if body[pc].Op == opBlock || body[pc].Op == opLoop {
			out = append(out, pc)
		}
	}
	return out
}
