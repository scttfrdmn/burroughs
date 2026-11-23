// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// E3's witnesses (#1337, ADR 0131): a component starts its own agents through the component spawn door,
// `burroughs:threads/spawn@0.1.0#spawn` (contract §2 T-1), and the door is refused by name where T-1 does not
// admit it. The fixtures are `testdata/spawn-door*.wat`; their memories declare max = min, so they are Go-heap
// backed and `-race` sees guest memory.

// spawnDoorChain is the guest's `chain(k)`, recomputed here so the expected output does not come from the guest.
func spawnDoorChain(k uint64) uint64 {
	x := k*0x9E3779B97F4A7C15 + 1
	for i := range uint64(100000) {
		x ^= x >> 33
		x *= 0xFF51AFD7ED558CCD
		x ^= x >> 29
		x += i
	}
	return x
}

// spawnDoorWant is spawn-door.wasm's stdout: each agent's chain, the global it started with (its own T-6 global's
// initializer, 0), the global it read after its work (its argument), and whether it saw run's release (1); and the
// global run read after joining them (its own, 0x77).
func spawnDoorWant() string {
	var b strings.Builder
	for k := uint64(1); k <= 2; k++ {
		fmt.Fprintf(&b, "agent %d chain=%016x entry-global=%08x global=%08x released=%08x\n", k, spawnDoorChain(k), 0, k, 1)
	}
	fmt.Fprintf(&b, "run global=%08x\n", 0x77)
	return b.String()
}

// engineGoroutines is every goroutine an engine file started (its `created by` frame is in internal/interp or
// internal/component, and not in a test file), by its stack.
func engineGoroutines() []string {
	var out []string
	for _, g := range strings.Split(allGoroutineStacks(), "\n\n") {
		_, created, ok := strings.Cut(g, "\ncreated by ")
		if !ok {
			continue
		}
		lines := strings.SplitN(created, "\n", 3)
		if len(lines) < 2 {
			continue
		}
		site := lines[1]
		if (strings.Contains(site, "/internal/interp/") || strings.Contains(site, "/internal/component/")) &&
			!strings.Contains(site, "_test.go") {
			out = append(out, g)
		}
	}
	return out
}

// TestAComponentStartsItsOwnAgentsThroughTheSpawnDoor is E3's main witness (#1337, ADR 0131). spawn-door's run
// calls `burroughs:threads/spawn#spawn` twice, and each call starts an agent at the importing core instance's
// `mstart` as a thread of run's task (ADR 0130). It asserts:
//   - **the output**, byte for byte against spawnDoorWant, whose chains are recomputed here: each agent's work, its
//     own T-6 global (it starts at the initializer 0, not run's 0x77, and reads back its argument; run reads back
//     0x77), and `released=00000001`, which an agent prints only if it saw run's release, issued after run met both
//     agents while itself executing;
//   - **no engine goroutine surviving Close**: neither an agent's nor any socket's or producer's.
//
// **Three agents executing at once is asserted by a place, not by a sample.** `released=00000001` is that place: an
// agent prints it only from its spin, and run releases only after meeting both agents while itself in guest code, so
// all three were executing guest code at that moment. The engine's own count, `interp.Instance.ExecutingAgents`, is
// sampled every 200µs across run's 50ms hold and **logged, not asserted**. #1359's macOS run read a peak of 2 with
// the output byte-identical: on a 3-vCPU runner shared with every other test package, the sampler's thread can miss a
// 50ms window, so the sample measured the runner. 20 of 20 local runs read 3 at -cpu 8, 3 and 2 alike, because the
// count is a mark and a descheduled agent keeps it. The count under a place is E2's witness,
// TestAgentsOfASharedMemoryInstanceExecuteAtOnce, whose agents spin until its probe has read 3.
//
// **NEUTER, run:** with the door invoking `mstart` on the caller's own goroutine (`core.Invoke` in `spawnDoor.call`
// in place of `spawnAgent`), the peak reads 1, the agents print `released=00000000`, and they share run's globals:
// the output assertion fails on its own.
// **NEUTER, run:** with `finishSpawnDoors`' free check dropped, TestTheSpawnDoorIsRefusedByName's unshared row
// instantiates.
func TestAComponentStartsItsOwnAgentsThroughTheSpawnDoor(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	baseline := len(engineGoroutines())
	var out, errOut bytes.Buffer
	in := instantiateFixtureWithHost(t, "spawn-door.wasm", NewHost(&out, &errOut, nil))
	closed := false
	defer func() {
		if !closed {
			in.Close()
		}
	}()
	if !in.w.async.agentsRunFree {
		t.Fatal("spawn-door's canonical memory is shared and the gate is on, but the instance is not free")
	}
	core := coreInstanceWithExport(in, "mstart")
	if core == nil {
		t.Fatal("no core instance exports mstart")
	}
	stop := make(chan struct{})
	sampled := make(chan int, 1)
	go func() {
		m := 0
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				sampled <- m
				return
			case <-tick.C:
				m = max(m, core.ExecutingAgents())
			}
		}
	}()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := in.CallRun()
		done <- result{code, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(60 * time.Second):
		close(stop)
		t.Fatalf("run did not return within 60s; the engine's goroutines:\n%s", allGoroutineStacks())
	}
	close(stop)
	peak := <-sampled
	if r.err != nil || r.code != 0 {
		t.Fatalf("run: exit %d, %v (stderr %q)", r.code, r.err, errOut.String())
	}
	t.Logf("peak executing agents, sampled (logged, not asserted): %d", peak)
	if got, want := out.String(), spawnDoorWant(); got != want {
		t.Errorf("stdout\n%s\nwant\n%s", got, want)
	}
	in.Close()
	closed = true
	deadline := time.Now().Add(5 * time.Second)
	for len(engineGoroutines()) > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if left := engineGoroutines(); len(left) > baseline {
		t.Errorf("%d engine goroutines survived Close (%d before the fixture):\n%s", len(left), baseline,
			strings.Join(left, "\n\n"))
	}
}

// TestTheSpawnDoorIsRefusedByName is contract §2 T-1's refusals for the component door (#1337, ADR 0131), each by
// name and each before any guest code runs: the door offered to an instance that is not free (its canonical memory
// unshared, with the gate on or off, or the gate off over a shared one), a door with no `mstart` or one of the wrong
// type, a door imported at a type other than `func(arg: u32)` or `(i32) -> ()`, and an importer that uses no
// canonical memory.
func TestTheSpawnDoorIsRefusedByName(t *testing.T) {
	for _, c := range []struct {
		fixture string
		gate    string
		is      error
		want    string
	}{
		{"spawn-door.wasm", "", ErrUnsupportedForm, "offered only with gate:cm-shared-memory on"},
		{"spawn-door-unshared.wasm", "1", ErrUnsupportedForm, "this one names an unshared canonical memory"},
		{"spawn-door-unshared.wasm", "", ErrUnsupportedForm, "offered only with gate:cm-shared-memory on"},
		{"spawn-door-no-mstart.wasm", "1", ErrLinkRefused, `exports no function "mstart"`},
		{"spawn-door-mstart-type.wasm", "1", ErrLinkRefused, `"mstart" export must have type (i32) -> ()`},
		{"spawn-door-wrong-type.wasm", "1", ErrLinkRefused, "with type func(arg: u64); the door's type is func(arg: u32)"},
		{"spawn-door-core-type.wasm", "1", ErrLinkRefused, "the door lowers to (i32) -> ()"},
		{"spawn-door-no-canon-memory.wasm", "1", ErrLinkRefused, "uses none of the component's canonical memories"},
	} {
		t.Run(c.fixture+"/gate="+c.gate, func(t *testing.T) {
			t.Setenv(cmSharedMemoryGateEnv, c.gate)
			wasm, rerr := os.ReadFile("testdata/" + c.fixture)
			if rerr != nil {
				t.Fatalf("the committed fixture is missing, so this row has no subject: %v", rerr)
			}
			in, err := InstantiateWithHost(wasm, NewHost(io.Discard, io.Discard, nil))
			if err == nil {
				in.Close()
				t.Fatalf("%s instantiated, want a refusal containing %q", c.fixture, c.want)
			}
			if !errors.Is(err, c.is) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%s: %v, want %v containing %q", c.fixture, err, c.is, c.want)
			}
			if !strings.Contains(err.Error(), "burroughs:threads/spawn@0.1.0") {
				t.Errorf("the refusal does not name the import: %v", err)
			}
		})
	}
}
