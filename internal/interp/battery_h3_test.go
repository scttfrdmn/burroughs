// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestH3ShutdownInterruptsParkedAgents is the litmus battery case `h3-shutdown-interrupts-a-parked-agent`
// (contract §5 H-3, the MUST half): a thread parked in a blocking host call MUST be interruptible by engine
// shutdown, and a cancelled call MUST report a cancellation error — never success (a success would report a
// completed operation that never completed, worse than a hang because nothing downstream can tell).
//
// N=4 agents park in a blocking host call (blocked on ctx.Done — only Close ends them). `Close` must return
// within the deadline (it cancels every member's context and waits on the in-flight calls), and each of the
// N calls must return a cancellation error (ErrTerminated), none returning success. The right-shape witness
// this grows from is `TestCloseTerminatesTheAgentInABlockingExcursion` (T-5.4, N=1) — a blocking excursion,
// not the async candidate `TestCloseTerminatesAgentParkedInWaitableSetWait`, which parks in a guest-called
// built-in (H-4/SP-5's shape, not H-3's host call). Tag verified by shape, per the tagging finding on #742.
//
// Control watched dying (documented in the PR): making `cancelCtx` a no-op leaves the parked calls
// uninterrupted, so a bounded `Close` times out — the forbidden "a call failing to return." Close is run
// with a bounded wait so the control fails cleanly (a named timeout) rather than hanging, which would name
// nothing. Arbiter: neither. Run under -race per the case discipline.
func TestH3ShutdownInterruptsParkedAgents(t *testing.T) {
	const (
		n        = 4
		deadline = 10 * time.Second
	)

	var entered atomic.Int32
	parked := make(chan struct{}, n)
	lower := CanonLowerExtern(
		ft(nil, []bin.ValType{bin.I32}),
		func(c *CanonCaller, _ []Value) ([]Value, error) {
			err := c.Blocking(func() error {
				entered.Add(1)
				parked <- struct{}{}
				<-c.Context().Done() // only engine shutdown (Close) ends this park
				return c.Context().Err()
			})
			return []Value{I32(7)}, err
		},
		CanonOptions{},
	)
	in := hostLink(t, `(module
		(import "h" "call" (func $call (result i32)))
		(func (export "call") (result i32) (call $call)))`,
		bin.Features{}, hostImports(map[string]Extern{"call": lower}))

	// N concurrent agents, each parking in the blocking host call.
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := in.Invoke("call")
			errs <- err
		}()
	}

	// Floor: all N parked host-side before shutdown is requested.
	for range n {
		select {
		case <-parked:
		case <-time.After(deadline):
			t.Fatalf("only %d of %d agents parked in the blocking call within %v", entered.Load(), n, deadline)
		}
	}

	// Shutdown. Close is run with a bounded wait: it must return within the deadline (it interrupts the
	// parked calls and waits on them). A control that leaves them uninterrupted makes this time out — a
	// named failure, not a hang.
	closeDone := make(chan error, 1)
	go func() { closeDone <- in.Close() }()
	select {
	case cerr := <-closeDone:
		if cerr != nil {
			t.Fatalf("Close reported %v — shutdown did not quiesce the parked agents", cerr)
		}
	case <-time.After(deadline):
		t.Fatalf("Close did not return within %v — shutdown failed to interrupt %d parked host calls "+
			"(H-3 forbidden: a call failing to return)", deadline, n)
	}

	// Every cancelled call must report a cancellation error, never success.
	for range n {
		select {
		case err := <-errs:
			if err == nil {
				t.Errorf("a parked call returned SUCCESS under shutdown — H-3's worst forbidden: a completed " +
					"operation that never completed, which nothing downstream can tell from a real success")
			} else if !errors.Is(err, ErrTerminated) {
				t.Errorf("a parked call returned %v under shutdown, want ErrTerminated (a cancellation error)", err)
			}
		case <-time.After(deadline):
			t.Fatalf("a parked call did not return after Close within %v", deadline)
		}
	}
}
