// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package growbench measures what [decision 0061][0061] costs `memory.grow`: one `sync.Mutex`
// acquisition per grow, so that the length change is the single atomic read-modify-write the threads
// proposal's model calls for (`relaxed.rst:246`) instead of three steps.
//
// **It exists because the forecast could not otherwise be non-analytic.** `internal/interp/membench`
// never grows — the string `grow` does not appear anywhere under that directory — so a null result
// there is a zero that could not have come out otherwise, and *an analytic zero is not a measurement*.
// That absence was checked while writing 0061's pre-registration rather than after it, which is the
// only reason a forecast about these rows was possible at all.
//
// It drives the real front end through `Invoke`, for `membench`'s reason (grave #125): the subject is a
// code path, so a hand-built module or a locally re-implemented `grow` would measure the copy.
//
// # The three arms, and what each one can and cannot falsify
//
//   - `Reslice` — a **shared** memory, whose reservation (`sharedReservePages`) covers its whole
//     declared range, so every grow takes the reslicing arm: four validation comparisons, one
//     `memImage` allocation, one atomic `Store`. **This is the sensitive arm.** The lock is the largest
//     fraction of the operation here, so this is the row that can embarrass 0061, and it is the row the
//     pre-registered bar is about.
//   - `Reallocate` — an **unshared** memory, `cap == len`, so every grow is a `make` plus a `copy` of
//     the whole memory. The lock is a rounding error here **by construction**, which makes this arm a
//     within-instrument control rather than evidence: a measurable cost on it falsifies something other
//     than the lock, and the right response would be to find out what.
//   - `ResliceNull` — the same source as `Reslice`, so the pair gives a within-run floor.
//     [#580](https://github.com/scttfrdmn/burroughs/issues/580) is why this is not optional: a
//     semantically inert diff moved unrelated rows 6–9% on amd64 with `unsafe.Sizeof` held equal, so a
//     null result at an unmeasured resolution cannot distinguish *no effect* from *an effect under the
//     floor*. This arm gives the floor a number on the same run rather than an assumption.
//
// `BenchmarkUncontendedLockUnlock` is the **bar**, and it is measured here rather than quoted: 0061
// registers the reslice arm's acceptable cost as *no more than one uncontended Lock/Unlock pair*, and a
// bar recalled from memory is not a bar. It is plain Go, deliberately — the claim it supports is about
// `sync.Mutex`, not about this engine.
//
// # Two conventions a reader has to know before comparing rows
//
// **`Reslice` grows by zero pages.** `memory.grow 0` is legal, returns the current size, and takes the
// reslicing arm on any memory — `n == len(cur)`, so `n <= cap(cur)` holds — publishing a fresh
// descriptor for the same array. Every step this arm is about (the lock, the four comparisons, the
// allocation, the `Store`) is identical to a one-page reslicing grow; what a zero delta buys is that the
// row is **repeatable**, since a benchmark that actually grows runs out of declared maximum in
// microseconds and would spend the rest of its iterations measuring the refusal path instead.
//
// **`grows` grows per Invoke, so ns/op is per `grows` grows, not per grow.** Divide to compare against
// the Lock/Unlock bar. The count exists for `membench`'s reason — `Invoke`'s own fixed cost has to be a
// small share of the row — and is stated here because a reader who reads these rows as per-grow figures
// would find the lock a hundredfold more expensive than it is.
//
// [0061]: ../../../docs/decisions/0061-grow-serialises-on-its-own-mutex-rather-than-a-compare-and-swap-over-the-descriptor-because-the-length-lives-in-two-places-and-only-one-is-in-the-descriptor.md
package growbench

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// grows is how many `memory.grow` instructions one Invoke of the reslice arms performs.
//
// Matched to `membench`'s `accesses` so a reader comparing across bench packages is comparing the same
// trip count.
const grows = 1000

// reallocPages is the page count the reallocating arm climbs to from its declared minimum of one.
//
// Sized so that the `copy` dominates the row and instantiation does not: 63 reallocations copying an
// average of two megabytes is milliseconds of `memcpy` against a single-page instantiation. Both halves
// of that matter — a smaller number would let instantiation dilute the arm, and a larger one would push
// the row into hundreds of milliseconds and leave `b.N` too small for `benchstat` to say anything.
const reallocPages = 64

// build takes wat through encode, decode and instantiate with the threads gate on.
//
// The gate is on because `(memory … shared)` does not decode without it, and it is on for both arms
// rather than only the shared one so that the two rows differ by the memory's declaration and nothing
// else — including nothing about which features the decoder was told to accept.
func build(src string) (*interp.Instance, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, fmt.Errorf("instantiate: %w", trap)
	}
	if derr := in.Deferred(); derr != nil {
		return nil, fmt.Errorf("instantiate fell short: %w", derr)
	}
	return in, nil
}

// resliceModule is `grows` zero-delta grows, straight-line.
//
// Straight-line rather than looped for `membench`'s reason: the figure should be the grow and not a
// guest loop's bookkeeping. The result is dropped inside the guest, so nothing here depends on what Go's
// compiler can see through — the work happens inside the interpreter's dispatch loop over data the
// compiler has no view of.
func resliceModule(decl string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(module %s\n\t(func (export \"grow\")\n", decl)
	for range grows {
		b.WriteString("\t\t(drop (memory.grow (i32.const 0)))\n")
	}
	b.WriteString("\t\t))")
	return b.String()
}

// reallocModule climbs from one page to `reallocPages`, one page at a time.
//
// Every grow past the first reallocates, because an unshared memory gets no reserved capacity: `cap ==
// len`, so `n <= cap(cur)` is false and the arm is `make` plus `copy`.
func reallocModule() string {
	var b strings.Builder
	fmt.Fprintf(&b, "(module (memory 1 %d)\n\t(func (export \"grow\")\n", reallocPages)
	for range reallocPages - 1 {
		b.WriteString("\t\t(drop (memory.grow (i32.const 1)))\n")
	}
	b.WriteString("\t\t))")
	return b.String()
}

// reslice is the timed body for both reslicing rows: one instance, reused, since a zero-delta grow
// leaves the memory exactly as it found it.
func reslice(b *testing.B) {
	b.Helper()
	in, err := build(resliceModule(fmt.Sprintf("(memory 1 %d shared)", reallocPages)))
	if err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	// One call outside the loop, so a failure is a failure rather than folded into the first
	// iteration's time.
	if _, err := in.Invoke("grow"); err != nil {
		b.Fatalf("invoke grow: %v", err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke("grow"); err != nil {
			b.Fatalf("invoke grow: %v", err)
		}
	}
}

// BenchmarkReslice is the sensitive arm: `grows` reslicing grows, where the lock is the largest share
// of the operation.
func BenchmarkReslice(b *testing.B) { reslice(b) }

// BenchmarkResliceNull is byte-identical in source to BenchmarkReslice and is the within-run floor.
//
// *Comparisons need a vacuity check*, and #580's measurement is why this one is a separate row rather
// than a sentence: the pair's spread on a single run is the resolution at which the arm above can be
// read, and without it a null on that arm is unfalsifiable.
func BenchmarkResliceNull(b *testing.B) { reslice(b) }

// BenchmarkReallocate climbs one page at a time from a fresh instance per iteration.
//
// The instance is rebuilt inside the timed region because there is no way to shrink a memory: after one
// iteration it sits at its declared maximum and every further grow would measure the refusal path.
// `Instantiate` of a one-page module is microseconds against this row's milliseconds of `memcpy`, and
// `reallocPages` is chosen to keep that ratio — which is a claim the `Reslice` rows can check, since
// they pay the same instantiation once and report it in their own fixed cost.
func BenchmarkReallocate(b *testing.B) {
	src := reallocModule()
	if _, err := build(src); err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	b.ResetTimer()
	for range b.N {
		in, err := build(src)
		if err != nil {
			b.Fatalf("building the bench module: %v", err)
		}
		if _, err := in.Invoke("grow"); err != nil {
			b.Fatalf("invoke grow: %v", err)
		}
	}
}

// BenchmarkUncontendedLockUnlock is decision 0061's acceptance bar, measured on the same run as the
// rows it bounds.
//
// It is plain Go and touches no engine code, which is the point: the bar is a claim about what
// `sync.Mutex` costs when nobody contends it, and reading it off this machine on this run is what makes
// *"the reslice arm may cost one Lock/Unlock pair and no more"* a checkable sentence instead of a
// remembered number. `grows` iterations per op so the row is directly comparable to the reslice rows
// without rescaling.
func BenchmarkUncontendedLockUnlock(b *testing.B) {
	var mu sync.Mutex
	for range b.N {
		for range grows {
			mu.Lock()
			mu.Unlock() //nolint:staticcheck // SA2001 is about empty critical sections; an empty one is exactly the subject here.
		}
	}
}
