<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0070 — An embedder panic is repaired inside the `defer`s that already exist, because a second one is a measured cliff and a converted panic is a public promise

Date: 2026-09-05 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, it changes no gate's default and no public signature, and
it reverses no stamped ADR — it is
[0067](0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)'s
constraint honoured rather than lifted. Option B below *would* have touched the public surface, which is
one of the three subjects that must be escalated, and declining it is why this document is not a question.

Filed against **[#650](https://github.com/scttfrdmn/burroughs/issues/650)**. It settles what an embedder
panic does to the engine's safepoint bookkeeping. It does **not** settle
[#12](https://github.com/scttfrdmn/burroughs/issues/12) (T-5 exit/join/detach), which is where a `recover`
above a spawned thread first appears and therefore where this ADR's third site stops being unreachable;
and it does not settle what §5 *says* about a panicking host function, which remains nothing.

## Context

`callHost` establishes four things around the embedder's function and defers exactly one of them. #650's
body has the anatomy; what this section adds is what the state actually measures, because two sentences in
that body are wrong in the direction that would have produced a test failing on correct code.

**The measured state on unrepaired `main`.** One host function that panics, recovered by the test above
`Invoke`, on `3dab4a9`:

| | crossing delta | `callers` | `blocked` | `Stop(2s)` | `Close` |
|---|---|---|---|---|---|
| unrepaired | **3** (odd) | 1 | 1 | `nil` | returns |
| `callHost` repaired only (**option A**) | 4 | **1** | 0 | **deadline expiry: "0 of 1 arrived within 2s"** | returns |
| all three sites repaired (**option E**) | 4 | 0 | 0 | `nil` | returns |

Two corrections follow from the first and second rows, and both change what the acceptance test can be.

**The marks leak in pairs, so the predicate's error cancels — and #650's headline is true without being the
harm.** `Stop` asks `blocked == callers`; a panic out of `h.fn` adds one to each, so the *difference* is
preserved and the leaked thread reads exactly as the clean one does. Measured: `Stop` returns `nil` on the
leaked state and `nil` on the repaired state. A clean idle thread is *also* arrived, and correctly — any
re-entry reaches `enterFrame`, whose first statement is `st.t.poll()` (`tailcall.go`), so an `Invoke` during
a stop parks before one guest instruction runs. So the acceptance test #650 filed — *"a `Stop` that must
not report arrival"* — **cannot be written as filed**: it fails on correct code. What is broken on main is
the row's first column, the **crossing count**, measured at 3 where the mechanism predicts 4:
`invokeIndex`'s pair plus `callHost`'s excursion, minus the `enterGuest` the panic skipped.
`TestEveryBoundaryCrossingIsPaired` can see that only for an odd number of leaks inside its own delta,
which is why the number is pinned directly by this slice's own test instead.

**The pairing is a coincidence of which frames the panic crosses, not a property — and option A breaks
it.** Repairing `callHost` alone is measured in row two: `callers=1, blocked=0`, and `Stop` waits out its
**whole deadline** to report `0 of 1 arrived` for a thread executing nothing. That is the same defect made
loud instead of silent, and it is the reason the repair cannot stop at the site where the panic originates.

## Options

- **A. `defer` the pair in `callHost` only.** Measured above: it repairs the crossing count and converts
  the silent breach into a deadline expiry. Rejected on its own measurement rather than on its price.
- **B. `recover` at the host-call boundary and convert the panic to `ErrHostTrap`.** Pairs every count on
  one path and needs no flag. Rejected for two reasons that point the same way: it changes what an embedder
  observes, which is a **public API surface** question and one of the three subjects that must be escalated
  rather than decided in a slice; and a `recover` here swallows the panic value and the traceback of a bug
  that belongs to the embedder, replacing a stack that names their function with an error that names ours.
  Available later as a surface decision; not available as a bookkeeping repair.
- **C. Accept, and make the guard able to say so.** Rejected because the guard cannot say so. Its predicate
  is `blocked > callers`, and the leak is symmetric — measured: no panic from the guard on row one. Making
  it diagnostic means a new per-thread field recording that a mark was abandoned, which is more state than
  the repair itself and leaves the breach in place.
- **D. Declare it out of contract.** Available when defining the state is expensive. It costs no `defer`
  here, so the argument has no premise.
- **E. Fold the repair into the `defer` each site already has, gated on a flag. Chosen.** No function gains
  a second `defer`, so 0067's cliff is not approached; the straight-line pairs stay straight-line and keep
  0067's tighter drop point; nothing is recovered, so an embedder's panic still arrives at the embedder
  with its own stack.

## The mechanism

Three sites, one shape. The flag is true across **exactly** the call that can carry an embedder panic, and
false everywhere else, which is what keeps the repair off the early-return paths where no mark is set and
an unconditional uncount would drive a counter negative:

```go
embedderRunning := false
defer func() {
	if embedderRunning {
		t.unmarkBlocked()
		enterGuest()
	}
	w.endHostCall()          // the job this defer already had
}()
...
leaveGuest()
t.enterBlocked()
embedderRunning = true
results, callErr := h.fn(c, args)
embedderRunning = false
t.leaveBlocked()
enterGuest()
```

- **`callHost`** folds into `defer w.endHostCall()`, across `h.fn`.
- **`invokeIndex`** folds into `defer leaveGuest()`, across `in.run` — the site that uncounts `callers`.
- **`runEntry`** folds into its `defer leaveGuest()`, across `in.invoke`, for the spawned-thread copy.

**`unmarkBlocked` and not `leaveBlocked`, because the unwinding path must not park.** `leaveBlocked` is now
the composition of a clear and a poll, and the panic path takes only the clear. The poll serves SP-2's
second half — a thread leaving a wait must not touch guest memory before observing a stop — and a thread
carrying a panic outward touches none: it runs `defer`s and returns frames. What it must not do is park,
because a `Stop` in flight would then hold a panicking goroutine inside its round and the stop's completion
would depend on an embedder's `recover` running, which is a dependency SP-4 cannot state. The omission is
safe rather than merely convenient because the next entry into the guest polls anyway, at `enterFrame`.

**`enterGuest()` on the unwind, which is bookkeeping and not a claim about guest state.** The crossing
counter's invariant is the parity of nested brackets: `callHost` opened an excursion out of the guest that
must be closed before `invokeIndex`'s outer `defer leaveGuest()` runs above it. Leaving it open is what
made the measured delta odd.

## Pre-registration of the fixed-cost forecast

Written **before the A/B ran** and left standing whichever way it came out, because *only the ordering
distinguishes a pre-registration from an amended threshold*. Codegen is evidence that the cliff is not
approached; it is not a price, and 0067 is the ADR that established that this boundary's fixed cost gets
measured rather than argued.

- **Instrument:** `internal/interp/invokebench` on `janus.local`'s `measured` group, `scripts/ab.sh` at 12
  rounds with `--null --graft`, base `main`, head this branch. The same protocol 0067 used, so its landed
  figure is comparable to this one.
- **The rows, and the reason there are two new ones.** `Empty` prices `invokeIndex`'s fold alone.
  **`HostCall` is new in this slice** and prices `callHost`'s fold — no row in this package crossed back out
  to an embedder, so `callHost`'s half would otherwise have rested on codegen with no number at all. Each
  has its own byte-identical null twin; `EmptyNull`'s resolution does not transfer to a row of a different
  magnitude.
- **Bar:** `TwoUncontendedLockUnlock`, as in 0067. It is a *generous* ceiling here and is said to be:
  this slice adds no lock to either path, so a delta approaching one would mean the closure did something
  structural rather than storing a bool.
- **Forecast:** both deltas at or under **0.25× the bar**, i.e. two stores and a capture, not a lock.
- **Vacuity check, in the direction that matters:** each null delta must be **narrower than the bar**, or
  the board does not adjudicate the forecast at all and the figure is reported without being weighed.
- **If the forecast fails**, the fold is narrowed to `callHost` — the only site with a *measured* defect
  (row one's odd crossing) — and `invokeIndex`/`runEntry` fall back to option C owing a per-thread field.
  Narrowing happens before landing, not after: a failed forecast narrows, it does not license.

### The result, and the one thing on the board that was not forecast

`janus.local` `measured` task 16, 0 concurrent at submit, linux/amd64, i9-9960X, base `1d16643`'s parent
(`main`), 12 rounds, `--null --graft`:

```
                            │    base     │                head                │                null                │
Empty-32                      172.3n ± 1%   171.9n ± 1%       ~ (p=0.486 n=12)   172.5n ± 1%       ~ (p=0.943 n=12)
EmptyNull-32                  172.0n ± 0%   172.4n ± 1%       ~ (p=0.943 n=12)   173.0n ± 1%       ~ (p=0.339 n=12)
TwoUncontendedLockUnlock-32   20.99n ± 0%   19.41n ± 0%  -7.53% (p=0.000 n=12)   20.98n ± 0%       ~ (p=0.199 n=12)
HostCall-32                   278.6n ± 0%   281.8n ± 1%  +1.17% (p=0.000 n=12)   278.2n ± 0%       ~ (p=0.504 n=12)
HostCallNull-32               278.6n ± 0%   281.0n ± 1%  +0.86% (p=0.001 n=12)   279.1n ± 1%       ~ (p=0.898 n=12)
geomean                       137.0n        135.4n       -1.16%                  137.2n       +0.12%
```

- **The forecast holds on both rows it was made about.** `invokeIndex`'s fold is not detectable at this
  resolution (`Empty` −0.4 ns, p=0.486); `callHost`'s costs **+3.2 ns**, which is **0.15× the bar** against
  the criterion's 0.25×. The two host rows are byte-identical twins and move together (+3.2 ns, +2.4 ns),
  which is the effect measured twice rather than once — a host call's fixed cost rose from 278.6 ns to
  ~281 ns, of which the *boundary* share is what this ADR bought.
- **The vacuity check passes in the direction it was pointed.** Every null delta is inside ±1 ns against a
  20.99 ns bar, so the board has the resolution to adjudicate a 5.25 ns criterion.
- **The bar row moved, and it is the row that cannot have been changed.** −7.53%, p=0.000, ±0% spreads,
  while base and null agree at 20.98–20.99 ns. `BenchmarkTwoUncontendedLockUnlock` is a function-local
  `sync.Mutex` calling no engine code, and `--graft` gave all three arms a byte-identical copy of it, so
  the residual explanation is code layout in head's binary. That makes head an arm carrying an
  **offset**, which is bias rather than jitter and is normally the thing no comparison inside a table can
  witness — here one row happens to witness it, being the only row required to be invariant. **The
  phenomenon was already filed**: [#580](https://github.com/scttfrdmn/burroughs/issues/580) records a
  semantically inert diff moving unrelated rows 6–9%, and its number is four lines above the bar row in
  `invokebench`'s own package comment, which is where it should have been read before a second issue was
  opened. What is new is the denominator, filed as
  [#653](https://github.com/scttfrdmn/burroughs/issues/653) with four candidate repairs. **Not repaired
  here**, because the perturbation is row-specific (bar −7.53%, `Empty` ~0%, `HostCall` +1.17%): no
  single-number correction exists, and a per-arm normaliser would import the bar's own layout luck into
  every other row.
- **What that limit does and does not do to this decision.** The criterion is met against the bar value
  base and null agree on, and the alternative reading — that some of `HostCall`'s +3.2 ns is layout rather
  than the two stores — moves the figure only *downward*. Either way the fold is bounded well under a
  lock, which is the quantity option E was chosen for. It is stated rather than smoothed because the
  honest version of *"forecast met"* here includes that one of the instrument's own assumptions was
  measured false on the same board.

## Consequences

- **0067's cliff is measured, not assumed absent.** `go build -gcflags=-S` reports **no
  `runtime.deferprocStack` anywhere in `internal/interp`**, before and after; `-gcflags=-m` reports *"func
  literal does not escape"* at all three sites and moves no flag to the heap. This is the reading 0067's own
  text told the next person to take, and it is the whole reason the choice is a fold rather than a `defer`.
- **An embedder's panic still reaches the embedder**, with its value and its stack. Asserted, so that a
  later `recover` cannot be added quietly: the acceptance test fails if nothing panics out of `Invoke`.
- **What is still unrepaired, named rather than implied.** A panic from *engine* code — not an embedder's —
  recovered above `Invoke` still leaks `callers` alone, which is row two's state. 0067 already says that
  case is an engine bug that has left the instance undefined, and this ADR does not change that: what it
  removes is the case where the panicking party is the embedder and the panic is theirs to recover.
- **The third site's repair is unreachable today**, since nothing on a non-test path recovers a panic out of
  a spawned thread — a panic there ends the process. It is witnessed by a test that supplies the `recover`
  #12's join will supply by construction, rather than asserted as a protection nothing can observe.
- **Rollback:** revert the three folds and `unmarkBlocked`. The option that survives a revert is C, and it
  would arrive owing a new per-thread field.
