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
// T-1 is written and lives in **#554**, a PR parked unmerged: `Spawn`, a shared-memory gate, an
// entry-signature check, and `runEntry` launching a goroutine that calls `runtime.LockOSThread` and
// never unlocks. Five tests for T-1 there, and it is deliberately red at one test — see below.
//
// It is **withheld** because `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` fires on it. That
// control watches for the first `go` statement in this package's non-test files, and it names what
// unparking has to answer: §4's boundary model has its mechanism and no litmus battery (**#10**),
// `memory.atomic.wait` cannot return 0 for woken (**#543**), `Spawn` shares the instance's globals so
// `global.set`'s plain writes are data races (**#573**), and the spawn walk's closure is smaller than
// the reachable set (**#575**). Deleting or re-pointing that control is a principal's call and not a
// test author's, so this paragraph is a parking notice rather than a to-do list.
//
// **The blocker has now changed twice without clearing, and this paragraph is where the first change
// was noticed too late** (grave **#561**; the second is grave **#576**, in the control's own name and
// message). It used to quote the control saying *"discharge #542"*, and to assert that all 67 atomics
// in `atomic.go` were plain read-then-write — true when written, falsified by [ADR 0051][0051], which
// made them sequentially-consistent word operations over the backing array. It then named **#557**,
// the tearing of memop.go's aligned plain accesses, and **#516**, §4's boundary edges; both are
// discharged, #557 by [ADR 0054][0054] and #516 by [ADR 0052][0052]. The watched *event* is unchanged
// through all of it, so `Spawn` is parked further along the same chain rather than for a new reason,
// and the way the first of these was settled is the part worth keeping: a `go` statement injected into
// a scratch non-test file and the resulting FAIL read back. **A claim about what an instrument will
// permit is a forecast about a machine sitting in the tree.**
//
// **Scott ruled that order, and the ruling is what this comment records rather than the question it
// used to pose.** Option 1: discharge #542 first — **#542 → #516 → #10** — reversing the in-session
// ordering that had put spawn ahead of §4's model. An override was refused on the grounds that *"once
// a second thread exists, plain Go operations on shared interpreter state are data races — undefined
// behaviour, not merely wrong values."* #542 is discharged. #554 carries the measurement that made
// that refusal concrete — two threads doing 2000 atomic adds each on one cell landing on 3392 rather
// than 4000, with `-race` naming `atomic.go`'s read and `memory.go`'s write — and the repaired engine
// returns 4000, which `TestAtomicRmwIsNotObservablyTornAcrossThreads` asserts and keeps asserting.
//
// **A PR and not a bare commit, deliberately.** A PR number resolves under `citecheck` and GitHub
// retains the diff and `refs/pull/N/head` independently of the branch; the commit SHA this comment
// used to cite could not be checked by anything in the tree, so it would have read as a valid
// citation forever while pointing at a pruned object.
//
// Two things deliberately not done, because both are the shape of arguing with the instrument: the
// file is not added to an exception list, and `Spawn` is not moved to a sibling package where the
// `go` statement would sit outside the control's domain. *An exemption inherits none of the
// trigger's lessons.*
//
// [0050]: ../../docs/decisions/0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md
// [0051]: ../../docs/decisions/0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md
// [0052]: ../../docs/decisions/0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md
// [0054]: ../../docs/decisions/0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md
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
