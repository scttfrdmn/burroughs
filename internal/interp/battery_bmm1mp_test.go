// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// bmm1MPRounds is the trial count. The detector answers per round, so the count buys schedule diversity, and
// it is large enough for the inconclusive bound below to mean something: 10% of 40 is 4 rounds.
const bmm1MPRounds = 40

// bmm1MPDelay is how long the consumer's host call waits before it returns. It is a timing assumption and not
// an edge (the chair's fourth ruling of 2026-10-24), and the test counts the rounds where it did not hold.
const bmm1MPDelay = 20 * time.Millisecond

// bmm1MPMaxInconclusive is the ruled bound: the case fails if more than 10% of rounds are inconclusive.
const bmm1MPMaxInconclusive = bmm1MPRounds / 10

// TestBMM1AMessagePassesAcrossAHostCallReturnBetweenTwoGuests is the litmus battery case
// `b-mm-1-message-passing-across-a-host-call-return` (contract §4 B-MM-1, both halves composed at the
// host-call-return crossing). It was re-registered and built under the chair's fourth ruling of 2026-10-24.
//
// Guest A writes a word with `memory.fill` and then makes a host call: a guest→host transition, B-MM-1's
// release edge. Guest B, in a **second instance** that imports the same shared memory, makes a host call that
// sleeps, and after the return (a host→guest transition, the acquire edge) it loads the word. Both the
// publisher and the consumer are guest agents, which was Scott's 2026-09-14 condition for reopening this hole.
//
// # Why two instances
//
// Inside one instance the host-call path's own world bookkeeping (`beginHostCall`/`endHostCall` and the
// blocked marks) also orders A's crossing before B's. So a one-instance case stays silent with the boundary
// edge removed and cannot be watched dying; that was measured, and it is structural-by-redundancy, like
// SP-6's. Two instances have two worlds, and the only edge between them is ADR 0052's package-level counter,
// which is the configuration that decision made the counter package-level for.
//
// # The signal is a delay, and a delay is an assumption
//
// A sleep neither synchronises nor races. The 2026-09-12 attempt used a host flag, and the flag decided the
// verdict itself: as an atomic it ordered the write, and as a plain variable it raced itself. **But a sleep is
// a timing assumption, not an edge.** A round in which B reads before A's fill has happened has witnessed
// nothing. B reads 0, and if the detector reports, the two accesses were concurrent in the unbroken engine
// too. Such a round is counted **inconclusive**, never as a pass, and more than 10% of them fail the case.
//
// **Allowed:** no `-race` report naming the located pair. **Forbidden:** a report whose halves are A's write in
// `execMemoryFill` and B's post-return load in `memAccess`, at one address.
//
// **The arbiter is `-race`, Go's memory model, on both CI architectures. It is not the hardware** (#742, #795).
//
// # Watched die
//
// NEUTER, run: `enterGuest` and `leaveGuest` emptied (`boundaryCrossings.Add(1)` removed). `-race` reported
// the located pair, `(*Instance).execMemoryFill` against `(*Instance).memAccess`, on darwin/arm64 and on
// linux/amd64 (janus, pueue task 90). The live engine was silent on both, and every round was conclusive
// (janus task 89). Re-run on the committed form, with the same located pair: on darwin/arm64, and on
// linux/amd64 (janus task 102, neutered; task 101, live, 3 of 3 runs with 40 of 40 rounds conclusive).
//
// THE BOUND, run: with the delay set to 0, every round read before A's fill, giving 40 of 40 inconclusive and a
// failure naming the ruled bound, in 3 of 3 runs. So the count can see the assumption fail.
func TestBMM1AMessagePassesAcrossAHostCallReturnBetweenTwoGuests(t *testing.T) {
	nop := HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) { return nil, nil })
	sleep := HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) {
		time.Sleep(bmm1MPDelay)
		return nil, nil
	})
	pub := hostLink(t, `(module
		(import "h" "pub" (func $pub))
		(memory (export "mem") 1 1 shared)
		(func (export "publish") (param $a i32)
			(memory.fill (local.get $a) (i32.const 1) (i32.const 4))
			(call $pub)))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"pub": nop}))
	closeAtEnd(t, pub)
	mem, ok := pub.Export("mem")
	if !ok {
		t.Fatal("the publisher exports no memory")
	}
	con := hostLink(t, `(module
		(import "h" "sleep" (func $sleep))
		(import "m" "mem" (memory 1 1 shared))
		(func (export "consume") (param $a i32) (result i32)
			(call $sleep)
			(i32.load (local.get $a))))`,
		bin.Features{Threads: true}, func(module, name string) (Extern, bool) {
			if module == "m" {
				return mem, name == "mem"
			}
			return sleep, module == "h" && name == "sleep"
		})
	closeAtEnd(t, con)
	if len(pub.mems) != 1 || len(con.mems) != 1 || pub.mems[0] != con.mems[0] {
		t.Fatal("the two instances do not share one memory object, so their accesses could not race at all")
	}

	// The publishers' errors are collected only after every round, because waiting on one before B's read
	// would be a happens-before edge from A to B that is not the engine's.
	done := make(chan error, bmm1MPRounds)
	inconclusive := 0
	for i := range bmm1MPRounds {
		// A fresh 16-byte-aligned word per round, so round i's write cannot race round i-1's read.
		a := int32(64 + 16*i)
		go func() { _, err := pub.Invoke("publish", I32(a)); done <- err }()
		v, err := con.Invoke("consume", I32(a))
		if err != nil {
			t.Fatalf("round %d: consume: %v", i, err)
		}
		if v[0].Int32() != 0x01010101 {
			inconclusive++
		}
	}
	for range bmm1MPRounds {
		if err := <-done; err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	t.Logf("%d of %d rounds conclusive, %d inconclusive (B read before A's fill)",
		bmm1MPRounds-inconclusive, bmm1MPRounds, inconclusive)
	if inconclusive > bmm1MPMaxInconclusive {
		t.Fatalf("%d of %d rounds were inconclusive, over the ruled bound of %d: the delay (%s) did not order the "+
			"publisher's write before the consumer's read often enough for the detector's silence to mean "+
			"anything. Any race report on this run may be the instrument's, not the engine's",
			inconclusive, bmm1MPRounds, bmm1MPMaxInconclusive, bmm1MPDelay)
	}
}
