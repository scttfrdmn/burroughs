// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// bmm1HCRRounds is the round count. As in `b-mm-2`'s cases, the detector answers per round and deterministically,
// so the count buys schedule diversity rather than a chance of hitting a window.
const bmm1HCRRounds = 20

// bmm1HCRDelay is how long the consumer's host call waits before it returns. It is the ordering between publisher
// and consumer, and it is a delay on purpose (see the test's comment). It is generous because a publisher that
// writes *after* the return races the consumer's read in the unbroken engine too. The floor below reports that
// case as the instrument's fault.
const bmm1HCRDelay = 20 * time.Millisecond

// TestBMM1AHostWriteIsVisibleAfterAHostCallReturn is the litmus battery case
// `b-mm-1-a-host-write-is-visible-after-a-host-call-return` (contract §4 B-MM-1, the acquire half at the
// host-call-return crossing). It was built under item D of the §4 plan on #10, the chair's third ruling of
// 2026-10-24.
//
//	Every host→guest transition — host-call return, … — MUST constitute an acquire edge over the entire shared
//	address space for the resuming agent.
//
// **The reading it rests on**, recorded on #10 before it was built: the sentence names no writer, so a host
// publisher's writes ordered before the return are within what it promises the resuming agent. The clause's
// second sentence, the release edge at a guest→host transition, is not this case's subject, because a host
// publisher makes no such transition.
//
// # The witness, and why the signal is a delay
//
// The guest calls host `wait`. That host function starts a host goroutine, which writes a fresh word through
// `Caller.Write`; the write is plain (`memory.write` is a `copy`). `wait` then sleeps and returns, and the
// guest loads the word.
//
// **The publisher-to-consumer signal is a sleep, because every other signal decides the verdict itself.** The
// 2026-09-12 attempt at this crossing used a host flag. As an atomic, the flag's own happens-before ordered the
// write before the read, so `-race` was silent with the engine's edge removed. As a plain variable, the flag
// raced itself, so the unbroken case failed. A sleep neither synchronises nor races. The only happens-before
// edge left from the write to the read is the engine's: the write's `leaveGuest`, and the return's
// `enterGuest`, both `Add`s on ADR 0052's one package-level counter.
//
// **Allowed:** no `-race` report naming the located pair. **Forbidden:** a report whose two halves are the
// host's `memory.write` under `Caller.Write` and the guest's load in `memAccess`, at one address.
//
// **The floor:** every round reads the published value. A round that reads 0 means the publisher wrote after the
// return. In that case the read and the write are concurrent even in the unbroken engine, and any report is the
// instrument's. So a floor failure names the delay, not the engine.
//
// **The arbiter is `-race`, Go's memory model, on both CI architectures. It is not the hardware.** This
// certifies that Burroughs' Go implementation of the edge has no data race under the schedules explored. It
// does not certify the guest-visible ordering on weak-memory hardware, which is #795's stated limit.
//
// # Watched die
//
// NEUTER, run: `enterGuest` and `leaveGuest` emptied (`boundaryCrossings.Add(1)` removed), which is the missing
// edge B-MM-1 forbids. `-race` reported the pair: the write in `(*memory).write` under `(*Caller).Write`, and the
// read in `(*Instance).memAccess`. That held on darwin/arm64, and on linux/amd64 (janus, pueue task 90). The
// live engine was silent on both, with every round reading the published word (janus task 89).
func TestBMM1AHostWriteIsVisibleAfterAHostCallReturn(t *testing.T) {
	wait := HostExtern(ft([]bin.ValType{bin.I32}, nil), func(c *Caller, args []Value) ([]Value, error) {
		addr := uint64(uint32(args[0].Int32()))
		go func() { _ = c.Write(addr, []byte{1, 0, 0, 0}) }()
		time.Sleep(bmm1HCRDelay)
		return nil, nil
	})
	in := hostLink(t, `(module
		(import "h" "wait" (func $wait (param i32)))
		(memory 1 1 shared)
		(func (export "run") (param $a i32) (result i32)
			(call $wait (local.get $a))
			(i32.load (local.get $a))))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"wait": wait}))
	closeAtEnd(t, in)

	for i := range bmm1HCRRounds {
		// A fresh 16-byte-aligned word per round. With one reused word, round i's write would race round i-1's
		// read, and the report would be the harness's.
		a := int32(64 + 16*i)
		v, err := in.Invoke("run", I32(a))
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if got := v[0].Int32(); got != 1 {
			t.Fatalf("round %d read %d at %#x, not the published 1. The host publisher wrote after the call "+
				"returned, so the write and the read were concurrent in the unbroken engine too, and any race "+
				"report on this run is the instrument's, not the engine's. The delay (%s) did not order them",
				i, got, a, bmm1HCRDelay)
		}
	}
}
