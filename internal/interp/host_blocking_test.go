// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// blockingCallerModule builds an instance whose sole export `call` reaches a canon-lower adapter whose
// impl parks in CanonCaller.Blocking until `release` is closed or the agent's context is cancelled
// (Close). onEnter fires once the impl is running and about to block. It is the §5 substrate the second
// guest's input-stream.blocking-read puts on the component path: a blocking excursion of the calling
// agent, parkable by Stop and terminable by Close.
func blockingCallerModule(t *testing.T, release <-chan struct{}, onEnter func()) *Instance {
	t.Helper()
	lower := CanonLowerExtern(
		ft(nil, []binary.ValType{binary.I32}),
		func(c *CanonCaller, _ []Value) ([]Value, error) {
			err := c.Blocking(func() error {
				if onEnter != nil {
					onEnter()
				}
				select {
				case <-release:
					return nil
				case <-c.Context().Done():
					return c.Context().Err()
				}
			})
			if err != nil {
				return nil, err
			}
			return []Value{I32(7)}, nil
		},
		CanonOptions{}, // no memory/realloc: the read's lower is exercised elsewhere; this isolates the block
	)
	return hostLink(t, `(module
		(import "h" "lower" (func $lower (result i32)))
		(func (export "call") (result i32) (call $lower)))`,
		binary.Features{}, hostImports(map[string]Extern{"lower": lower}))
}

// TestStopParksTheAgentInABlockingExcursion is §5 H-1 on the adapter: an agent parked in
// CanonCaller.Blocking is at a safepoint, so a Stop completes while it is blocked, and Resume + release
// lets the call finish. A blocking host call does not hold the world open against Stop.
func TestStopParksTheAgentInABlockingExcursion(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	caller := blockingCallerModule(t, release, func() { entered <- struct{}{} })

	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := caller.Invoke("call")
		if err != nil {
			errs <- err
			return
		}
		done <- out
	}()

	<-entered // the agent is inside Blocking
	if err := caller.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v — an agent parked in a blocking excursion must be at a safepoint (H-1)", err)
	}
	select {
	case <-done:
		t.Fatal("the call finished before release — the block did not hold")
	case err := <-errs:
		t.Fatalf("the call failed: %v", err)
	default:
	}
	caller.Resume()
	close(release)
	select {
	case out := <-done:
		if len(out) != 1 || out[0].Int32() != 7 {
			t.Errorf("after release the call returned %v, want [7]", out)
		}
	case err := <-errs:
		t.Fatalf("after release the call failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the call did not finish after Resume+release")
	}
}

// TestCloseTerminatesTheAgentInABlockingExcursion is §5 H-3: a Close while an agent is parked in
// CanonCaller.Blocking cancels its context, the impl returns through Done(), and the wake's poll ends
// the agent — the call reports ErrTerminated (T-5.4), the blocking read interrupted by shutdown.
func TestCloseTerminatesTheAgentInABlockingExcursion(t *testing.T) {
	release := make(chan struct{}) // never closed: only Close ends the block
	entered := make(chan struct{}, 1)
	caller := blockingCallerModule(t, release, func() { entered <- struct{}{} })

	errs := make(chan error, 1)
	go func() { _, err := caller.Invoke("call"); errs <- err }()

	<-entered
	if err := caller.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errs:
		if !errors.Is(err, ErrTerminated) {
			t.Fatalf("the call returned %v, want ErrTerminated — Close must interrupt a blocking excursion (H-3/T-5.4)", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call did not return after Close — the block was not interrupted")
	}
}
