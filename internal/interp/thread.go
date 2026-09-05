// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"sync/atomic"
)

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
// `memory.atomic.wait` cannot return 0 for woken (**#543**), `Spawn` shares the instance's globals and a
// **reference** global's `global.set` is still a plain write (**#573**), and the spawn walk's closure is
// smaller than the reachable set (**#575**). Deleting or re-pointing that control is a principal's call and not a
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
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
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
	// **That retirement condition has now been falsified by the work it named, and the directive
	// stays.** This field's forecast was that *"the first reader is #515's safepoint check"*. #515's
	// check landed ([ADR 0059]) and reads `stopReq` below, not `slot` — a stop request is engine
	// state and T-4's slot is guest-visible state, and nothing about polling one requires reading the
	// other. So the suppression's subject is unchanged and deleting it here would be a directive
	// removed on a coincidence of issue numbers. What *is* corrected is the sentence: `slot`'s first
	// reader is the host function a module calls to read its own slot, which is public API and still
	// unwritten. A retirement condition that names a *slice* rather than a *reader* is the kind of
	// citation that reads as satisfied the moment that slice lands, which is why the condition below
	// now names the reader.
	//
	//nolint:unused // pinned by 0050 before its first consumer; retired by T-4's guest-visible slot accessor
	slot uint64

	// stopReq is contract §3 SP-1's epoch/stop flag: set by `Stop` on another goroutine, read by
	// `poll` at every back-edge and call site. [ADR 0059]'s mechanism.
	//
	// **Atomic because of who writes it, not because of contention.** The write happens once per stop
	// round and the read happens per back-edge, so there is no contention to speak of; what makes a
	// plain `bool` wrong is that `Stop` runs on a different goroutine, and a non-atomic read of a word
	// another goroutine writes is a data race — undefined behaviour, not a slightly-stale answer.
	stopReq atomic.Bool

	// blocked is contract §3 SP-2's mark: this thread is suspended and therefore *at a safepoint* —
	// decision 0060's third choice.
	//
	// **The mark names no reason, and the omission is the design.** SP-2 is one clause over two
	// consumers — *"a thread blocked in a host call **or** in `memory.atomic.wait`"* — and
	// `enterBlocked`/`leaveBlocked` (safepoint.go) mention neither: they take a `*thread`, move this
	// count under `world.mu`, and park if a stop is in flight. `memory.atomic.wait` is merely the
	// consumer that exists, not the thing being counted. This sentence said *"suspended in
	// `memory.atomic.wait`"* until #602's scoping — a true description of a population of size one
	// wearing the grammar of a constraint on it, which is **grave #645**. The cost is a reader who
	// wants a blocking host call concluding they must build a second mechanism instead of reusing
	// this one, and that reading happened once before the repair. A blocking host call wraps its call
	// in the same pair and inherits SP-2 and SP-4 whole.
	//
	// **Guarded by `world.mu`, and deliberately not an atomic.** Both writers (`enterBlocked`,
	// `leaveBlocked`) and the only reader (`Stop`) hold that mutex, and the whole point is that the
	// transition and the count cannot interleave: an atomic read would let `Stop` observe "not
	// blocked" from a thread that is one instruction from blocking, and then wait for an arrival that
	// will never come. `stopReq` above is atomic for the opposite reason — its reader is the hot path
	// and must not take a lock.
	//
	// **A count and not a flag, because a `thread` is per *instance* and a caller is per *call*.**
	// `link.go` registers exactly one thread per instance, and an embedder may drive N concurrent
	// `Invoke` calls through it — the engine's own `TestAtomicRmwIsNotObservablyTornAcrossThreads`
	// does, with N=2. A flag would be cleared by the first of those to leave a wait while the others
	// were still in one, which is the mark saying "running" about a thread that is not. The count is
	// exact for that shape; what it cannot fix alone is a *mixed* one, where one caller is suspended
	// and another is executing guest code on the same `thread` — #592, and `callers` below is the
	// other half of that repair.
	blocked int

	// callers is how many callers are currently executing on this thread, moved by
	// `enterCall`/`leaveCall` (safepoint.go) around each `Invoke`'s guest execution. Decision
	// [0067]'s mechanism, and the second half of the mark above.
	//
	// **It exists because `blocked` is a count of callers and `Stop` was reading it as a fact about
	// the thread.** The predicate was `blocked > 0`, which is *"some caller here is suspended"* asked
	// in place of *"no caller here is running"*. Those coincide only while a thread has at most one
	// caller, and one `thread` per instance against an ungated exported `Invoke` means it does not.
	// With A suspended in a wait and B in a loop, `blocked` is 1, the old predicate answered "at a
	// safepoint", and `Stop` returned `nil` while B ran — contract §3 SP-2 failing on its own terms,
	// since a thread reported at a safepoint *"cannot touch guest memory until it re-enters through a
	// boundary that observes the stop."* `Stop` now asks `blocked == callers`.
	//
	// **`blocked <= callers` holds by construction, and the invariant is load-bearing rather than
	// incidental**, which is why `Stop` asserts it rather than trusting it: `enterBlocked` is reached
	// only from `futex.go`'s `wait`, which is reached only from guest code, which runs only inside a
	// call that has already incremented this field. A `blocked` above `callers` would make an equality
	// predicate unsatisfiable and hang every `Stop` — a worse failure than the one being fixed, and
	// silent.
	//
	// **Guarded by `world.mu` for `blocked`'s reason and not for a weaker version of it.** An atomic
	// count would put this field outside the critical section that makes 0060's three-way race a
	// two-way one, and would need its own ordering argument against a `Stop` that reads both fields.
	// 0067 records that as option B′, rejected: the failure it re-opens is the one 0060 names as the
	// outcome that must not exist.
	//
	// **Instantiate-time guest execution is not wrapped, and is not a gap.** `build`'s start function
	// and `runConst` run before `InstantiateLinked` returns, so no external reference to the instance
	// exists and no `Stop` can be in flight against it. That is the only guest code outside `Invoke`.
	//
	// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
	callers int

	// reported records that this thread's arrival has been sent for the round `world.resume` names,
	// so that N callers sharing one thread produce one arrival and not N.
	//
	// **This is a bug fix to #591's arrival protocol and not a new requirement.** `parkAtSafepoint`
	// argued that its send could not block because *"the buffer is `len(w.members)` and each thread
	// sends once per round"* — and each *thread* does, while each *caller* also sends, so three
	// concurrent `Invoke`s and one `Stop` filled a one-slot buffer and left the third caller blocked
	// on a send forever, with `Resume` unable to free it. See `parkAtSafepoint`.
	reported bool

	// w is the stop-the-world state this thread participates in, set by `world.register` at creation.
	//
	// Nil is legal and means "no world", which is any `thread` this package's own tests build by
	// literal. `poll` reads `stopReq` before it ever reaches this field, so a nil `w` costs the hot
	// path nothing: an unregistered thread can never have `stopReq` set, because the only writer is
	// the `Stop` that walks a world's members.
	w *world
}

// String names a thread in an error or a test failure, so a message about one says which. The only
// method on the type.
func (t *thread) String() string { return fmt.Sprintf("thread %d", t.id) }
