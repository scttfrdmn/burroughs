<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0071 — T-5 is live-only membership, a bounded status record, a fault in two channels, and a sentinel panic for the terminal unwind, because the poll may not learn a second reason to stop

Date: 2026-09-05 · Status: **accepted**, **stamped by relay** — Scott, on the report that carried #12's
option set, recorded verbatim as [#12 comment
5555709678](https://github.com/scttfrdmn/burroughs/issues/12#issuecomment-5555709678): *"**Q1-b taken**,
with one addition … **Q2-a taken**, with a bound … **Q3 — I disagree with b′ on its second half.**"* On
0069's terms, and this is the stamp's own subject rather than a review of the code that followed: the
answers below are his, and the contract amendment this ADR accompanies is normative text, which is one of
the three subjects an actor here may not decide.

**What this citation is, and what it is not.** It resolves to a principal's decision on a stated option
set. It does **not** resolve to a GitHub review artifact: the ruling was a session turn and the recording
was made by the actor it was given to, so *durability is not independence* and every commit resting on it
is `Ratio-Class: carried`. Stated because a forged provenance about the project's own governance is worse
than a wrong option.

**What the stamp did not decide is everything below "Mechanism".** Scott ruled on semantics — detached by
default, join over a bounded record, a trap in two channels, a shutdown that ends every thread and waits.
How the terminal unwind travels, and what the fast path pays for it, is this document's own choice, taken
under *decide and proceed*. The one place the two meet is the public surface: `Join` and `Fault` exist
because the ruling put them there in words (*"the retained trap must also be retrievable by the embedder
independently of any host entry"*), and their names and signatures are the narrowest rendering of those
words that T-1's entry shape can produce.

Filed against **[#12](https://github.com/scttfrdmn/burroughs/issues/12)**, contract §10.3. It settles T-5
and nothing adjacent: not [#586](https://github.com/scttfrdmn/burroughs/issues/586) (ADR 0058's coherence
residual), not §10.4's guest-visible cancel primitive, not the `gate:threads` default, and **not
[#656](https://github.com/scttfrdmn/burroughs/issues/656)** — the arrival protocol's identity defect,
which is measured, filed, and deliberately not repaired here.

## Context

Four facts about `main`, measured rather than argued, are what the ruling was taken against. They are
reproduced here because the choice is only legible against them.

| | measured on `main` |
|---|---|
| 1 | A trap that ends a spawned thread is stored in `internal/interp/thread.go:thread.err` and read by nothing outside the package. An embedder cannot see it at all. |
| 2 | `Instance.Join` does not exist. `thread.done` is unexported; a spawned trap is unobservable by construction. |
| 3 | `internal/interp/safepoint.go:world.members` only grows: **51 members after 50 completed spawns**, and every completed thread is walked by every subsequent `Stop`. |
| 4 | `Instance.Close` returned in **39µs** while a spawned counter ran on to 23008 — a terminal operation returning while guest code executes. |

Fact 3 is the leak the `members` comment already names, citing T-5 and §10.3 as its reason for existing.
Fact 4 is the hazard the ruling calls *"the hazard `Close` exists to remove"*.

## The stamped answers

- **Detached by default** (T-5.1). No join obligation; no guest-visible join primitive. A guest that
  wants one builds it over T-3's futex, which is what the threads proposal and wasi-threads both assume.
- **Join is a host API over a bounded record** (T-5.2). Live-only membership plus a `tid`→status record,
  *"consumed by a join, or dropped at `Close`"*.
- **A trap is reported in two channels** (T-5.3). At the next host entry **and** through a retrieval that
  needs no host entry, because *"a guest that spawns, traps, then blocks forever in a futex never makes a
  host entry."*
- **Shutdown ends every thread and waits** (T-5.4), with the un-endable case *named* and reported as
  shutdown's error.
- **No `tid` reuse** (T-5.5).

## Mechanism

### The terminal unwind is a sentinel panic, not an error return through the poll

A thread executing guest code is ended by making its next safepoint terminal. The safepoint is
`internal/interp/safepoint.go:thread.poll`, reached from `internal/interp/tailcall.go:enterFrame` and from
fourteen back-edge sites in `internal/interp/exec.go:Instance.run`. Two ways to get a termination out of
there:

- **A. Give `poll` and `jumpTo` an error result.** This is exactly the return that
  [ADR 0059](0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md)
  withdrew, on the ground that *"§3 has no clause a poll can fail … the error would have been **always
  nil**"*. T-5.4 supplies the clause, so the ground is gone — but the cost is not. It is a new error check
  on fourteen back-edge arms and on every frame entry, in the hottest loop in the engine, to carry a
  condition that fires once per thread per lifetime.
- **B. Panic an unexported sentinel from `parkAtSafepoint`, recover it in the two `defer`s that already
  exist.** The fast path is untouched: `poll` still loads exactly one atomic (`thread.stopReq`) and calls
  nothing else on the not-taken branch. `thread.exitReq` is read only *inside* `parkAtSafepoint`, after
  the release, which is by construction the slow path — a thread only gets there because `stopReq` was
  already set.

**B, and the ADR 0067 constraint is why it costs nothing structurally.** No function in `internal/interp`
may gain a second `defer` (the first stops being open-coded; 25–29 ns/call measured). B adds none: it
folds a `recover()` into `internal/interp/thread.go:Instance.runEntry`'s existing defer and
`internal/interp/interp.go:Instance.invokeIndex`'s existing defer — which is precisely the third fold
[ADR 0070](0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md) called *"unreachable
today"*, saying *"#12's exit/join is a `recover` above this frame by construction"*. That forecast is
discharged by this ADR rather than restated.

**Pre-registered, and checkable by disassembly rather than by a benchmark:** the emitted code for
`internal/interp.(*thread).poll` is byte-identical between `main` and this branch, and the inlining
decision at all fifteen call sites is unchanged. `go build -gcflags='-S -m'` answers both. A benchmark
could not distinguish *unchanged* from *changed by less than its noise floor*; codegen can.

**The recover must re-panic anything that is not the sentinel**, or B silently converts ADR 0070's subject
— an embedder panic — into an error return, which is the public promise 0070 declined to make. This is the
one line in the mechanism whose absence is invisible to every test that does not panic on purpose, so it
gets its own test.

**A waiter takes the same sentinel, and the draft of this paragraph said it needed none of it.** It read
*"`internal/interp/futex.go:Instance.memoryWait` already returns an error, so … a third `select` case … plus
a dequeue"* — two errors in one sentence, and the second is the load-bearing one. There is no
`Instance.memoryWait`: the site is `internal/interp/futex.go:memory.wait`, and it returns `int32`, the
instruction's result. So there is no error channel to put a shutdown in, and an added one would be observed
by nothing — `Close` sets `exitReq` *and* `stopReq`, so the deferred safepoint on the way out panics the
sentinel before any value reaches `atomicWait`. An always-nil-in-effect error is the shape `unparam` is
enabled for. The third `select` arm therefore dequeues and calls `thread.terminate`, and the signature does
not move. What the paragraph got right is the reason: the three defined results (0/1/2) are fixed by the
proposal, so a shutdown cannot be spelled as one of them without telling the guest that one of *woken*,
*not-equal* or *timed-out* happened when none did — and `notify`'s wake count is guest-visible, so *woken*
in particular is a number another thread may already have read.

**The unwind does not poll, and the reason this ADR gave for it was false — measured, in this slice,
before landing.** The sentence standing here said that `memory.wait`'s `defer t.leaveBlocked()` polls, that
a poll on a thread carrying the sentinel *"panics during a panic — a double panic, which takes the process
down"*, and that this is what compels the `recover`. Go does not behave that way: a panic raised inside a
deferred function while another is active replaces it and is recovered normally above, so the bare
`t.leaveBlocked()` re-panics the *same* sentinel and reaches the *same* `recover` with the same value. The
injection that restores the bare form **survives the whole package suite**, which is the finding rather
than a footnote to it: the `recover` form buys nothing for the sentinel, and the paragraph was arguing for
the right line from a crash that does not occur.

**What the `recover` is actually for is the panic that is not the sentinel** — [ADR
0070](0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md)'s subject, unwinding
through a thread inside a wait. The bare form polls it, and `parkAtSafepoint` does one of two things by
mark: with `exitReq` it terminates, converting a live panic value into the sentinel; with only `stopReq` it
**parks** an unwinding thread on `<-release` and counts its arrival. So the choice is `callHost`'s panic
path from 0070 with *"panic"* read as *"any panic"*, and it is **undiscriminated by every test here**:
nothing in `internal/interp` panics through `memory.wait` except `terminate`, so the case is unreachable in
this tree and the line is named rather than claimed as covered — 0070's own *"unreachable today"* standard
for this same fold. The flag the draft proposed (*"switch on an `unwinding` flag and call `unmarkBlocked`"*)
stays rejected against both: it would have to be **cleared** at the three ordinary exits, and a missed one
silently skips the boundary poll SP-2 asks for, where the `recover`'s two cases *are* panicking and not and
so have no missable state. Either way the next guest entry polls at `enterFrame`, so nothing SP-2 asks for
is lost.

**A second line in row 5 has no witness either, and is recorded for the same reason.** Dropping the
`t.terminate()` call from the cancellation arm — keeping the dequeue — also survives the suite: the arm
falls through to `resolveExpiry`, the deferred cleanup polls a thread whose `exitReq` is set, and the
sentinel is panicked one frame later. It is kept for *where* the termination lands (before a result is
chosen, so no reading of `resolveExpiry` can be mistaken for an answer the wait was entitled to give), not
for a behaviour difference. Two named undiscriminated lines is what an injection battery is for; the
alternative was two lines a later reader would trust for reasons that do not hold.

**And the same argument reaches one site the draft did not name: a host call that returns a trap.**
`callHost` unmarks without polling on that path too, for the reason above plus one that is only visible from
§5: H-3 promises a call interrupted by shutdown *"must not return success"*, and it is the trap wrapping
`context.Canceled` that says so. Run the poll first and the terminal unwind discards that value and reports
`ErrTerminated` instead — a shutdown overwriting shutdown's own answer. Skipping the poll **defers** the
termination rather than declining it: `exitReq` and `stopReq` stay set, so the thread ends at its next
safepoint, and in this engine a trap is terminal for the invocation anyway (exception handling is
validate-only — there is no `try_table` in `internal/interp`). Two landed tests are what compelled it, and
they are named in the consequences below.

### The five rows

| # | Change | Site |
|---|---|---|
| 1 | `world.live` replaces `world.members`; `world.exited` holds `tid`→terminal status; `world.retire` moves a thread from one to the other | `internal/interp/safepoint.go:world` |
| 2 | `Instance.Join(tid)` blocks on the thread's completion, answers and **consumes** the record, `ErrUnknownThread` otherwise | `internal/interp/thread.go:Instance.Join` |
| 3 | A sticky `world.fault` set by `retire`, reported once through `Invoke` as `ErrThreadFault` and always readable by `Instance.Fault()` | `internal/interp/interp.go:Instance.Invoke` |
| 4 | `thread.exitReq`, read after the release in `parkAtSafepoint`; the sentinel panic; `Close` sets it on every live thread and releases them | `internal/interp/safepoint.go:thread.parkAtSafepoint` |
| 5 | Trap-out-of-wait on the cancelled context: dequeue, then the sentinel; the blocked mark is cleared by a `recover` in the defer that already existed | `internal/interp/futex.go:memory.wait` |

Plus what T-5.4's *"and waits"* compels: `Close` waits for quiescence — no live thread other than the
instantiation thread, every live thread at `callers == 0`, `hostCalls == 0` — on a channel released from
`endHostCall`, `leaveCall`, and `retire` under the claim-then-close-outside-the-lock pattern §4 B-MM-3
requires.

**A join answers `error`, not a value**, because T-1 fixes the entry shape at one `i32` and no results —
checked exactly, in both directions, at `internal/interp/thread.go:Instance.spawn`. The contract clause
says so inline, so that a later reader does not supply the missing value themselves.

**The two records are bounded differently, and the asymmetry is deliberate.** The per-`tid` status record
is bounded by *consumption* (a join, or `Close`), because there is one per thread and fact 3 is what an
unbounded per-thread record measures. The fault slot is bounded by *being one slot*: first fault wins, it
is never consumed, and `Fault()` stays answerable after a join has taken the per-thread copy. If it were
consumed by the `Invoke` channel, T-5.3's two channels would be one channel with a race over which caller
gets the news.

**A shutdown-induced termination is not a fault.** `retire` records `ErrTerminated` in the status record
but never in the fault slot: an embedder that calls `Close` and then reads `Fault()` must not be told its
guest trapped. This is the one place where the two channels' contents differ, and it is the difference
between reporting a guest defect and reporting one's own shutdown.

### What provably cannot be ended, named as T-5.4 requires

1. **A host function that ignores its cancelled context.** `Close` cancels every thread's context (§5
   H-3); an embedder function that never reads `Caller.Context` never returns, and no mechanism in pure Go
   can take its goroutine away. Pre-existing, and documented on `Caller.Context` rather than only here.
2. **`Close` called from inside a host function of the same instance.** That thread's own
   `callers`/`hostCalls` marks are what quiescence waits for, so the wait is on the caller itself.
   Detecting it needs goroutine identity, which pure Go does not offer, so it is **nameable but not
   distinguishable** from the legitimate case of a sibling thread in a host call that is about to return.

Both are reported the same way, and that is what the bounded wait is for: `Close` waits a fixed
`closeQuiesceInterval` and, on expiry, returns an error naming the threads still live rather than
returning silently. The interval is a documented engine-internal value rather than a parameter because
`Close() error`'s signature is already public and widening it is an escalation subject; a configurable form
is a later decision if an embedder asks for one. It is an unexported `var` and not a `const`, which is not a
widening — nothing outside `internal/interp` can reach it — but is what lets the expiry arm be witnessed on
the real path: at ten seconds a test of that arm costs ten seconds, and the alternative is calling
`unquiesced` directly, which is *a control can test the helper, not the path* stated as a design. **The expiry error does not claim the case was
un-endable** — a 956ms non-polling `memory.fill` tail is measured on this engine, so slow and impossible
are not distinguishable from outside, and the error says which threads rather than why.

## Consequences

- `world.members` goes, and with it the leak fact 3 measured. Every `Stop` walks live threads only.
- **`internal/interp` gains its first non-test `recover`.** The comment in `interp.go` asserting there is
  none becomes false in this commit and is repaired in it.
- **Two consequences the verdict compelled, both about which of two right-looking errors a caller is
  owed**, and both are recorded here rather than only in the code because each one *chooses between* clauses
  of two different sections:
  1. **A host call that returns a trap unmarks without polling** (`internal/interp/host.go:Instance.callHost`).
     Compelled by `TestAHostCallIsCountedAndWaitedForByTheCallersWorld` and
     `TestCloseCancelsTheHostCallsContextAndWaitsForItToReturn`, which both went red on `ErrTerminated` where
     §5 H-3 promises the trap wrapping `context.Canceled`. Argued above.
  2. **A closed instance refuses at `internal/interp/interp.go:Instance.invokeIndex`, with `ErrClosed`,
     before `enterGuest`.** `beginHostCall` was the only site that knew, which was correct for a module with
     a host import and silent for every module without one. Row 4 is what makes the gap visible rather than
     merely latent: `Close` marks `in.host` terminal like any other thread, so the same call is now ended at
     `enterFrame`'s poll and reported as `ErrTerminated` — a *termination* claimed for a call that never
     started. `TestAClosedInstanceBeginsNoHostCallAndSpawnsNoThread` asks for `ErrClosed`, and
     `TestAClosedInstanceInvokesNothing` is the same refusal for the module that has no host import at all.
     At the top of `invokeIndex` rather than in `Invoke` so a re-export chain's delegating hop is covered
     too, and the predicate (`world.isClosed`) is deliberately advisory: a `Close` landing just after it
     reads false finds the thread through the ordinary terminal marks, so the loser of that race gets the
     other right answer rather than a wrong one.
- **`Stop` after `Close` returns an error instead of a verdict.** A stop round over a world whose threads
  are all terminating can only report a meaningless expiry or a meaningless nil, and `admit` already
  treats `closed` as terminal for `Spawn`.
- Six sites citing §10.3 or #12 as an open question are falsified by the amendment and repaired here:
  `internal/interp/thread.go` (three), `internal/interp/safepoint.go` (one), `internal/interp/spawn_test.go`
  (two). ADRs 0050/0068/0069 and `CHANGELOG.md` keep theirs: those are testimony about what was true when
  written.
- **#656 is untouched on purpose.** Its two witnesses — a false expiry when an awaited thread exits mid
  round, a false nil when a woken waiter's arrival satisfies a runner's slot — are defects in arrival
  *counting*, which row 1 neither creates nor worsens: a retired thread that never sends leaves `Stop`
  behaving exactly as it does today. Folding a repair in would make this PR's subject two subjects, and the
  criterion is whether the verdict compelled it. It did not. It does block the `gate:threads` flip, and is
  sequenced there.
- `Join` and `Fault` are the first engine surface an embedder can use to observe a thread at all, so they
  are also the first place a later `Thread` handle type would land instead. Not proposed: a method pair
  answering the stamped words is the narrower commitment.
