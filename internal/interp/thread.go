// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import "fmt"

// ThreadID names a wasm thread. Contract §2's T-1 `tid`.
//
// Monotonic from 1, and 0 is deliberately not a valid id: a zero `ThreadID` is what a caller gets
// back alongside an error, and the one thing it must not be able to mean is "the first thread".
type ThreadID uint64

// thread is one wasm thread's execution context — decision [0050]'s chosen representation, and the
// object contract §§3–5's per-thread state will hang off.
//
// **Why this exists as its own type rather than as fields on `stack`.** `stack` is threaded through
// every function on the hot path already (`run`, `runFrame`, `enterFrame`, `invoke`), so putting the
// slot straight on it would have cost zero new parameters and three propagation sites — measurably
// the cheapest option, and the one 0050 rejects. `stack` is per-*invocation*; T-4 asks for
// per-*thread*, and §7's growable continuations will make those different by creating stacks inside
// one thread. A slot living on `stack` would then be per-continuation, and a fresh stack that forgot
// to copy it would hold a **zero value that looks like a legitimate first thread** — a plausible
// wrong answer rather than a crash. A `*thread` copies idempotently and a forgotten copy is nil.
//
// The larger reason is that §§3–5 need more per-thread state than a slot: SP-1's stop/epoch flag
// checked at back-edges and call sites, SP-2's "in a host call, therefore at a safepoint" bit, T-3's
// futex park token. Every one is per-thread and every one would be per-continuation on `stack`.
//
// # T-1's spawn is not here, and a control in this package is why
//
// This slice was written with T-1 attached: `Spawn`, a shared-memory gate, an entry-signature check,
// and `runEntry` launching a goroutine that calls `runtime.LockOSThread` and never unlocks. It works
// and it is measured — commit `3b0129f` on this branch holds it, with seven tests and twelve
// falsifications.
//
// It is **withheld** because `TestAtomicsArePlainWhileTheInterpreterIsSingleThreaded` fires on it.
// That control watches for the first `go` statement in this package's non-test files and says what to
// do about it: *"the atomics now need a memory model (contract §4) … Do not exempt this file;
// discharge #542."* All 67 atomics in `atomic.go` are plain read-then-write, correct only while
// nothing runs concurrently with a function body, and **#542's own body prices its discharge as
// §4's litmus battery** (#516, #10) rather than as a change to that file. So T-1 sits behind work
// that this slice was sequenced ahead of, which is a **ruling about the phase's order** and not a
// choice available to the code. Flagged for Scott rather than decided here.
//
// Two things deliberately not done, because both are the shape of arguing with the instrument: the
// file is not added to an exception list, and `Spawn` is not moved to a sibling package where the
// `go` statement would sit outside the control's domain. *An exemption inherits none of the
// trigger's lessons.*
//
// [0050]: ../../docs/decisions/0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md
type thread struct {
	// id is T-1's tid, assigned once at creation and never written again.
	id ThreadID

	// slot is T-4: the per-thread slot, the `g` register analog. Read as `st.t.slot` — two
	// dereferences from a pointer the hot path already holds in a register.
	//
	// **Nothing reads it yet, and 0050 declines to benchmark the read for that reason.** The first
	// reader is #515's safepoint check. Comparing `st.t.slot` against `st.slot` today would compare
	// two fields neither of which is read, which is an analytic zero: it could not have come out any
	// other way. What 0050 pre-registers instead is the cost of *carrying* the pointer, which is
	// falsifiable on layout and allocation alone — and which passed, at a worst row of +0.94%.
	//
	// No `slotOf`/`setSlot` accessors: they would be two functions nothing calls, which `deadcode`
	// refuses and which would in any case guess at the shape #515's reader wants. The guest-visible
	// surface for T-4 — the host function a module calls to read its own slot — is public API and
	// does not ride a representation PR.
	//
	// **The suppression is weaker than the one it copies, and says so.** `stack.refs` carried this
	// exact directive under 0002 and was at least *allocated*; this field is neither read nor
	// written, so the pin is purely about where the slot lives. It is still the right pin: 0050
	// exists to decide that, and landing the object without the slot would hand the placement to
	// #515's PR — *"moving all of them later, in the PR that can least afford a representation
	// change"*, which is option C's own argument against option B. Deleted, not kept, when the
	// reader arrives: a directive must not outlive its subject.
	//
	//nolint:unused // pinned by 0050 before its first consumer; retired by #515's safepoint check
	slot uint64
}

// String names a thread in an error or a test failure, so a message about one says which. The only
// method on the type.
func (t *thread) String() string { return fmt.Sprintf("thread %d", t.id) }
