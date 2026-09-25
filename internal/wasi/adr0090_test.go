// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// arm0090 is one row of [ADR 0090]'s registered witness. The four arms were registered **before** the
// repair existed, and two of them passed *before* it — which is what lets this test fail a repair that
// fixes the broken cases by breaking the working ones.
//
// [ADR 0090]: ../../docs/decisions/0090-proc-exit-is-instance-scoped-and-its-teardown-is-a-request-routed-into-the-shutdown-mechanism-that-already-exists.md
type arm0090 struct {
	name string
	file string
	// sel is handed to `mstart` by the harness's own `spawn` import rather than by the guest, so one
	// module serves both of the sibling-ends arms.
	sel int32
	// wantCode is the exit code `errors.As` must reach through the returned error, or -1 for "no
	// exitError in the chain".
	wantCode int
	// wantNilErr is true for the arm where `Invoke` must return the invoker's own normal result.
	wantNilErr bool
	// faultThenClose is arm 2's shape after [ADR 0090]'s amendment 4, and it is a different protocol
	// rather than a different expected value: a bare trap must NOT end the instance, so `Invoke` stays in
	// flight, the trap arrives through `Fault()` while it does, and the embedder's own `Close` is what
	// ends it. That makes the `Close` sentence TRUE, because this time somebody called `Close`.
	faultThenClose bool
	// mustNotMove marks the two arms that passed before the repair. Their failure means the repair
	// broke something that already worked, which is a different report from "the repair is incomplete".
	mustNotMove bool
	why         string
}

// TestProcExitIsInstanceScoped is [ADR 0090]'s witness: a guest agent that exits or traps ends the
// instance, and `Invoke` returns the recorded first cause rather than never returning.
//
// **Go-free by construction.** Each guest is hand-written WAT committed beside this file with its
// assembled bytes, so nothing here depends on a Go toolchain, a Go runtime, a scheduler, or a futex
// lock layer — which is what makes a failure here a statement about Burroughs rather than about the
// Phase 4 fork that found the defect.
//
// **The bytes are committed rather than assembled at run time**, which is the wabt precedent: a test
// that shells `wat2wasm` skips when the tool is absent, and a skip in CI is a pass that never asked the
// question. There is therefore no `t.Skip` in this file at all.
//
// # The four arms, and why three of them must not move
//
//	proc_exit        a SPAWNED agent calls proc_exit -> instance ENDS, Invoke returns code 7
//	trap             a SPAWNED agent traps           -> instance SURVIVES; Fault reports it; Close ends it
//	invoker_exits    the INVOKING agent exits while a sibling parks -> unchanged
//	sibling_returns  a SPAWNED agent RETURNS cleanly  -> instance CARRIES ON, unchanged
//
// **This list said arm 2 ended the instance until amendment 4, and the sentence is replaced rather than
// annotated.** That was the wasi-threads reading, and building it showed the clause reverses stamped
// [ADR 0071]: a bare trap is a *fault*, and 0071 already answers a fault by recording it, reporting it on
// both of T-5.3's channels, and leaving the instance usable. `proc_exit` is different because it is the
// guest **declaring** the process is over — which is why the engine asks for a declaration
// ([interp.InstanceEnder]) instead of inferring intent from "it was a trap".
//
// **Only arm 1 is supposed to end the instance, and that is what gives the other three power.** Arm 4
// alone catches a hook placed at *"agent ended"* rather than *"agent trapped"* (§2 T-5's ordinary
// per-thread exit); arm 2 alone catches a hook that fires on any trap and so reverses 0071. A repair
// witnessed on arm 1 by itself would pass with either mistake in place.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
//
// [ADR 0090]: ../../docs/decisions/0090-proc-exit-is-instance-scoped-and-its-teardown-is-a-request-routed-into-the-shutdown-mechanism-that-already-exists.md
func TestProcExitIsInstanceScoped(t *testing.T) {
	arms := []arm0090{{
		name: "proc_exit", file: "sibling_ends.wasm", sel: 0, wantCode: 7,
		why: "a spawned agent's proc_exit ends the instance, and the code reaches the runner",
	}, {
		// **Amendment 4 changed this arm's meaning, and it now PINS stamped ADR 0071.** It read "a
		// spawned agent's trap ends the instance too" — the wasi-threads reading — until building it
		// showed that clause reverses 0071, whose witness requires a host entry after a spawned trap to
		// still run. A bare trap is a fault, not a declaration, so the instance survives it.
		name: "trap", file: "sibling_ends.wasm", sel: 1, wantCode: -1,
		faultThenClose: true, mustNotMove: true,
		why: "a bare trap is a FAULT: ADR 0071 keeps the instance usable, and only `Close` ends it",
	}, {
		name: "invoker_exits", file: "invoker_exits.wasm", sel: 0, wantCode: 7, mustNotMove: true,
		why: "the exit path that already worked must keep working",
	}, {
		name: "sibling_returns", file: "sibling_returns.wasm", sel: 0, wantCode: -1, wantNilErr: true,
		mustNotMove: true,
		why:         "T-5's ordinary per-thread exit: a clean return must NOT end the instance",
	}}

	// **The bound is stated, and it is far below the pre-repair symptom rather than near it.** Before the
	// repair arms 1 and 2 did not return at all, so any finite bound discriminates; 30s is chosen to be
	// unmistakably a hang if it is ever reached, not to be a tight timing assertion.
	const bound = 30 * time.Second

	var ran int
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			err := runArm0090(t, arm, bound)
			switch {
			case err == nil:
			case arm.mustNotMove:
				// **A different report, deliberately.** This arm passed before the repair existed, so its
				// failure does not mean the repair is incomplete — it means the repair broke something
				// that already worked, which is the one outcome the other arms cannot distinguish.
				t.Errorf("AN ARM THAT MUST NOT MOVE HAS MOVED: %v\n\tThis arm passed before ADR 0090's "+
					"repair, so this is a regression in working behaviour rather than an unfinished "+
					"repair — check the hook's predicate before checking its mechanism.", err)
			default:
				t.Error(err)
			}
			ran++
		})
	}

	// **The population check, and it is here because this file's own history needs it.** During the
	// investigation a tally of these runs was taken through a pipeline that had filtered every line
	// away, and it printed a count for a pattern that had matched nothing — an instrument reporting on
	// output it never received. So the witness asserts that every arm actually executed rather than
	// trusting that no failure line means no failure.
	if ran != len(arms) {
		t.Errorf("%d of %d arms ran; a witness that silently skips an arm is not a witness, and the "+
			"arms that must not move are exactly the ones a skip would hide", ran, len(arms))
	}
}

// runArm0090 runs one arm and returns why it failed, or nil.
func runArm0090(t *testing.T, arm arm0090, bound time.Duration) error {
	t.Helper()
	img, err := os.ReadFile(filepath.Join("testdata", "adr0090", arm.file))
	if err != nil {
		return fmt.Errorf("read guest: %w", err)
	}
	feats := GuestFeatures()
	feats.Threads = true // the guests use a shared memory and atomics

	var out strings.Builder
	cfg := Config{
		Wasm: img, Args: []string{"adr0090"}, Stdin: strings.NewReader(""),
		Stdout: &out, Stderr: &out, Features: &feats,
	}
	m, derr := decodeGuest(cfg)
	if derr != nil {
		return fmt.Errorf("decode: %w", derr)
	}
	h := newHost(cfg)
	if ferr := h.initFDs(nil); ferr != nil {
		return fmt.Errorf("initFDs: %w", ferr)
	}

	var mstartIdx uint32
	var foundMstart bool
	for i := range m.Exports {
		if m.Exports[i].Name == "mstart" && m.Exports[i].Kind == bin.ExternFunc {
			mstartIdx, foundMstart = m.Exports[i].Index, true
		}
	}
	if !foundMstart {
		// A guest with no `mstart` would spawn nothing, and every arm would pass by never reaching its
		// own subject — the vacuity this check exists to refuse.
		return errors.New("guest exports no `mstart`, so the arm cannot spawn and proves nothing")
	}

	var in *interp.Instance
	var spawns atomic.Int32
	hostImports := h.imports()
	imp := func(mod, name string) (interp.Extern, bool) {
		if mod == "burroughs" && name == "spawn" {
			return interp.HostExtern(
				bin.FuncType{Params: []bin.ValType{bin.I32}, Results: []bin.ValType{bin.I32}},
				func(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
					spawns.Add(1)
					// The arm selector is supplied HERE rather than by the guest, so `sibling_ends.wasm`
					// serves both the exit and the trap arm with one set of committed bytes.
					tid, e := in.Spawn(mstartIdx, arm.sel, 0)
					return []interp.Value{interp.I32(int32(tid))}, e
				}), true
		}
		return hostImports(mod, name)
	}

	var trap *interp.Trap
	in, trap, err = interp.InstantiateLinked(m, imp)
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if trap != nil {
		return fmt.Errorf("instantiate trapped: %w", trap)
	}
	// **Not deferred before the wait.** `runModule` defers `Close` until after `Invoke` returns, which
	// is exactly what never happened before the repair — so a `Close` that ran while this arm was still
	// waiting would repair the symptom inside the instrument and every arm would pass.
	defer func() { _ = in.Close() }()

	done := make(chan struct{})
	var ierr error
	go func() { _, ierr = in.Invoke("_start"); close(done) }()

	if arm.faultThenClose {
		return trapArm0090(t, in, done, &ierr, bound, arm)
	}

	select {
	case <-done:
	case <-time.After(bound):
		return fmt.Errorf("Invoke did not return within %v — %s", bound, arm.why)
	}

	if spawns.Load() != 1 {
		return fmt.Errorf("spawned %d agents, want 1: the arm never reached its own subject",
			spawns.Load())
	}

	if arm.wantNilErr {
		if ierr != nil {
			return fmt.Errorf("Invoke err = %w, want nil — %s", ierr, arm.why)
		}
		return nil
	}
	if ierr == nil {
		return fmt.Errorf("Invoke err = nil, want a cause — %s", arm.why)
	}

	var ee exitError
	switch got, want := errors.As(ierr, &ee), arm.wantCode >= 0; {
	case want && !got:
		// The load-bearing assertion for the runner: without this, `runModule` returns 0 for a guest
		// that asked for 7 and the repair is cosmetic.
		return fmt.Errorf("errors.As found no exitError in %v; the recorded cause must carry it so the "+
			"runner needs no special case — %s", ierr, arm.why)
	case want && ee.code != arm.wantCode:
		return fmt.Errorf("exit code = %d, want %d — %s", ee.code, arm.wantCode, arm.why)
	case !want && got:
		return fmt.Errorf("errors.As found exitError(%d) in %w, want none — %s", ee.code, ierr, arm.why)
	}

	// **The false-sentence check, which is amendment 2's own subject.** A guest-requested teardown must
	// not be reported as `Close`'s, because no embedder called `Close` — and this is the error EVERY
	// embedder sees, not just this package's runner.
	if strings.Contains(ierr.Error(), "after `Close`") {
		return fmt.Errorf("Invoke reported a `Close` that never happened: %w", ierr)
	}
	return nil
}

// trapArm0090 is arm 2 after [ADR 0090]'s amendment 4: a spawned agent **traps** while the invoking agent
// sits in an infinite `memory.atomic.wait32`. The instance must SURVIVE it, which is stamped
// [ADR 0071]'s answer for a fault, and this arm is what pins that answer in the threaded setting where
// 0071's own witness does not reach — there, nothing is parked in a futex.
//
// The protocol, in the order that makes it an assertion rather than three separate facts:
//
//  1. `Invoke` is STILL IN FLIGHT. A teardown here would be the reversal, so its absence is the claim.
//  2. `Fault()` reports the trap while it is in flight — T-5.3's *"retrieval that requires no host entry
//     at all"*, and the only channel available to an embedder whose one live agent is parked forever.
//  3. The embedder calls `Close`, and only then does `Invoke` return. The `Close` sentence it carries is
//     TRUE this time, which is the distinction amendment 2 drew: false when the guest exited, true here.
//  4. `errors.As` finds NO `exitError` — a trap is not an exit, and conflating them would make a crashed
//     guest report a clean status.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
// [ADR 0090]: ../../docs/decisions/0090-proc-exit-is-instance-scoped-and-its-teardown-is-a-request-routed-into-the-shutdown-mechanism-that-already-exists.md
func trapArm0090(t *testing.T, in *interp.Instance, done <-chan struct{}, ierr *error,
	bound time.Duration, arm arm0090,
) error {
	t.Helper()

	// Step 2 first in wall-clock terms: poll `Fault` until the spawned agent's trap is retained. This is
	// bounded, and reaching the bound is the failure — a trap that never arrives means the thread did not
	// trap at all and every assertion below would be vacuous.
	var ferr error
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if ferr = in.Fault(); ferr != nil {
			break
		}
		select {
		case <-done:
			// **Step 1's assertion, and it fires here rather than later.** If `Invoke` returned before the
			// fault was even observable, the instance was torn down by the trap — the reversal this arm
			// exists to catch.
			return fmt.Errorf("Invoke returned (%v) before the trap was retained: a bare trap tore the "+
				"instance down, which reverses ADR 0071 — %s", *ierr, arm.why)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if ferr == nil {
		return fmt.Errorf("Fault() stayed nil for %v: the spawned agent never trapped, so this arm "+
			"asserted nothing", bound)
	}
	if !errors.Is(ferr, interp.ErrThreadFault) {
		return fmt.Errorf("Fault() = %w, want ErrThreadFault — T-5.3's retrieval channel is the only one "+
			"an embedder whose live agent is parked forever can use", ferr)
	}

	// Step 1, stated positively now that the fault is in hand: the instance is still running.
	select {
	case <-done:
		return fmt.Errorf("Invoke returned (%v) on its own after a bare trap; ADR 0071 keeps the "+
			"instance usable and only `Close` may end it — %s", *ierr, arm.why)
	case <-time.After(100 * time.Millisecond):
	}

	// Step 3: the embedder's own teardown, which is what may end it.
	if cerr := in.Close(); cerr != nil {
		return fmt.Errorf("Close: %w", cerr)
	}
	select {
	case <-done:
	case <-time.After(bound):
		return fmt.Errorf("Invoke did not return within %v of `Close` — T-5.4 requires shutdown to end "+
			"an agent parked in `memory.atomic.wait`", bound)
	}

	// Step 4, and the mirror of amendment 2: here the `Close` sentence is TRUE, because `Close` was
	// called. Asserted rather than merely tolerated, so that a later change cannot quietly make this
	// path report a guest exit.
	if *ierr == nil {
		return errors.New("Invoke returned nil after `Close` ended it mid-flight, want the terminal error")
	}
	var ee exitError
	if errors.As(*ierr, &ee) {
		return fmt.Errorf("errors.As found exitError(%d) after a bare TRAP: a fault must not be "+
			"reported as a clean exit", ee.code)
	}
	if !strings.Contains((*ierr).Error(), "after `Close`") {
		return fmt.Errorf("Invoke returned %w, want the `Close` sentence — the embedder did call "+
			"`Close` here, so that sentence is true and is the right report", *ierr)
	}
	return nil
}
