# 0086 — The async Canonical ABI, behind `gate:async` — opens Phase 3

Date: 2026-09-11 · Status: **proposed** · [#735](https://github.com/scttfrdmn/burroughs/issues/735) · Opens the p3 async tier (contract §§5–6)
Ratio-Class: carried

**Draft — not stamped.** `Status: proposed` cites the decision issue #735, not an approval. Opening the
tier, the stamp (Status → accepted), and the creation of `gate:async` are Scott's; this ADR is the
decision-before-code record he stamps against, in the same form #688 → ADR 0084 opened Phase 2. Grounded in
recon [#734](https://github.com/scttfrdmn/burroughs/issues/734) and a byte-verified guest.

## Pinned spec commits

The design reads the WebAssembly component-model spec at a fixed point; the async text moves faster than the
value-type text, so the pin matters more, not less.

- **`WebAssembly/component-model` @ `2bed77e4228841c1d2721996d3ecc169ff96b158`** — the same pin as ADR 0084.
  The async artifacts, all under `design/mvp/`: `Concurrency.md` (the task/thread/waitable model),
  `CanonicalABI.md` (async `canon_lift`/`canon_lower`, `Task`/`Subtask`/`WaitableSet`, `stream`/`future`),
  `Explainer.md` (the 🔀 built-ins), `canonical-abi/definitions.py` (the executable model, the byte-exact
  oracle), and `test/async/*.wast` (the behavioral conformance suite).
- A later slice that needs newer async text re-pins with its own date; the difference is a recorded
  amendment, not a silent drift (0084's rule, inherited).

## Context

`gate:components` is on (ADR 0084 amendment, #720): Burroughs runs a **sync** `wasi:cli/run` component. WASI
0.3 moves async into the component model — `run` becomes an `async` functype, stdio becomes
`stream<u8>`/`future`, and `wasi:io` disappears. Recon #734 established, from a byte-verified guest (a Rust
`wasm32-wasip3` async-hello, sha1 `06f965f7…`, wasmtime 48.0.1 reading `Hello, world!\n`):

- A wasip3 async guest **builds today** (rustup nightly + `rustup target add wasm32-wasip3`, which ships a
  precompiled std + wasi-libc; no `-Zbuild-std`). #688's "no p3 guest" was a *Go* finding; the Rust path is open.
- **The guest's `run` export is a *sync-ABI lift of an async functype*, not a stackful async lift.** The one
  `canon lift` in the component carries no `async` canonopt (`opts.async_ = False`) though its functype is
  `func async` (`ft.async_ = True`) — so the host runs the core body to completion and calls `task.return_`
  itself (which is why the guest emits no `task.return`; `canon_lift` sync arm, def:2107–2118). What is async
  is the **import side**: 12 `[async-lower]` `canon lower`s (`opts.async_ = True`), whose subtasks the `run`
  task awaits by blocking on `waitable-set.wait` — so even this hello binds ~16 async built-ins centered on
  the waitable-set event loop, streams, and futures.

## Options and decision

Four decisions carry on #735; the guest's bytes fix the mechanism ones (decided-unless-Scott-objects), the
public entry and the contract text are Scott's.

1. **What the first slice lifts/lowers → the async *lower* path + the waitable-set event loop, on a sync
   lift.** The first guest does **not** exercise the async *lift* (neither stackless callback nor stackful) —
   it sync-lifts an async-typed export. So slice 1 is: (a) the sync lift of an async functype (close to the
   `gate:components` sync lift, but `ft.async_ = True` skips the sync-driver pump loop and lets the task stay
   pending, driven by the outer scheduler); (b) the **async `canon lower`** producing a subtask; (c) the
   **waitable-set / stream / future** built-ins the `run` task uses to await and to write `stream<u8>` stdout.
   **The async-lift ABI choice (stackless callback vs stackful) is deferred to the first guest that actually
   lifts an async export** — it is not forced by this guest and would be mechanism built ahead of a consumer.
2. **The suspension substrate → goroutine-per-task, reusing §5 `CanonCaller.Blocking`.** The `run` task
   blocking on `waitable-set.wait` suspends its goroutine; a futex-notify (R-1/R-2 readiness) wakes it. The
   model's `Thread` is that goroutine (ADR 0069). **This does not pull v2 §7 continuation stacks forward** —
   the Go runtime provides the growable stack and the switch the model's suspend (Concurrency.md:1399–1403)
   names, and the guest's suspension is a sync-lifted task awaiting subtasks, not a stackful lift.
3. **The 0.3 world split → implement 7 / stub-refuse-at-call 7 / refuse-at-bind on unmodeled kinds** (#735
   Decision 2). `wasi:io` is gone; no 0.2 import transfers. The `output-stream` *resource* path is replaced
   by a native `stream<u8>` with its own built-ins — the bulk of slice 1.
4. **The public async entry — left to Scott** (public API surface, an escalation subject): a mode on
   `ComponentConfig` vs a distinct `AsyncComponentConfig` vs consumer-triggered deferral (#735 Decision 4).
   Chair-side read on #735 recommends **no new type and no mode** — `Run` drives sync-or-async by the bytes,
   `IsComponent` already sniffs the artifact — with `gate:async` off refusing an async lift/lower by name at
   bind (exit 6, as `gate:components` did). Recorded as the leading option; the pick is Scott's.

## The capability line, at its two evidence levels

Following ADR 0084's amendment: the claim distinguishes model-verified from model-and-guest-verified, not
collapsed. **The real-guest level is a sync export with async imports — the async-export half is model-only
until a guest lifts one.**

- **Model + real guest:** *"a component's **sync-lifted `wasi:cli/run@0.3.0` export with async-lowered
  imports** runs on Burroughs behind `gate:async`, each blocking async import suspending only its task's
  goroutine while sibling work proceeds through the waitable-set loop, and its `stream<u8>` stdout written."*
  Byte-verified against wasmtime 48.0.1 and the `definitions.py` model.
- **Model only:** the **stackful (and stackless) async *lift*** of an async export, and the async value
  lowerings no first-guest path exercises (`future` payloads, `stream` reads, cancellation delivery) —
  verified against `definitions.py` alone, stated "differential-only" per row when the per-type table is
  written, never papered over with an invented reading.

## Two oracles

- **Byte-exact:** `definitions.py @ 2bed77e` driven through the model's own calls (`Store.invoke`/`lift`/
  `lower`/`tick`), model-produced state only (#728 standard). The async generator is a **scheduler-driver**
  (it drives the model's cooperative scheduler), materially heavier than the sync value-lowering generator.
- **Behavioral:** the async-hello guest's committed wasmtime 48.0.1 reading, plus the **2 validation**
  `test/async` cases (`validate-no-async-abi-for-sync-type`, `validate-no-stream-char`) runnable as
  decode/validate refusals. The other 36 `test/async` cases need built-ins beyond the first guest — the
  roadmap for later slices, not slice 1's differential.

## First guest

The Rust `wasm32-wasip3` async-hello (recon #734): a **sync-lifted** async-typed `wasi:cli/run@0.3.0` export
with async-lowered imports. Its bytes size slice 1 (the async lower + the waitable-set/stream/future event
loop + `stream<u8>` stdout). The three relocated borrow-lifetime assertions (#708) are the tier's first
pre-registered assertions, all reachable on the async path (#734 C10).

## Consequences

- **Gated off by default.** `gate:async` lands off; acceptance is the async suite's own green (contract §9).
  The flip is behaviour 4's own stamp-tier event, its forecast pre-registered on #735.
- **Contract text is a precondition for slice-1 *landing*, not for opening** (the H-2 sequence). §5's scope
  names "host calls"; `waitable-set.wait`/`{stream,future}.{read,write}` are a new **guest-called-suspend**
  category §5, §6 R-1/R-2, and §3's safepoint set do not name. The amendment is drafted on its own
  `type:contract` issue with the §5/§6/§3 wording as options and costs, and **stamped separately** — the tier
  opens, and no code lands past the point where the substrate's contract does not name what it is doing. Not
  written here.
- **The async-lift ABI (stackless vs stackful) is an open, deferred choice** — this guest forces neither, and
  a future reader should not conclude the tier commits to stackful. It is resolved by the first guest that
  lifts an async export.
- **Two gates behind us, this is the third.** Async is a Burroughs product line, not scaffolding.
- **#716 (mid-read cancellation) stops being optional** at this tier — streams are completion-based and a
  blocking `stream.read` is cancellable.
- **The pin ages:** the async text is younger than the value-type text; a slice needing newer text re-pins
  with a dated amendment.
- **`gate:async`'s creation and this ADR's stamp are Scott's.** No engine code lands on this ADR alone.
