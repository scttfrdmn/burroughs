# 0086 — The async Canonical ABI, stackful first, behind `gate:async` — opens Phase 3

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
0.3 moves async into the component model — `run` becomes `async func`, stdio becomes `stream<u8>`/`future`,
and `wasi:io` disappears. Recon #734 established, from a byte-verified guest (a Rust `wasm32-wasip3`
async-hello, sha1 `06f965f7…`, wasmtime 48.0.1 reading `Hello, world!\n`):

- A wasip3 async guest **builds today** (rustup nightly + `rustup target add wasm32-wasip3`, which ships a
  precompiled std + wasi-libc; no `-Zbuild-std`). #688's "no p3 guest" was a *Go* finding; the Rust path is open.
- The guest is **stackful** (0 `callback` in the component) and binds **~16 async built-ins** even for a
  hello — the async-lowered 0.3 stdio imports drag in the waitable-set event loop, streams, and futures. The
  export side is nearly trivial; the import side is the mechanism.

## Options and decision

Four decisions carry on #735; three are fixed by the guest's bytes (decided-unless-Scott-objects), the
fourth is public surface and is Scott's.

1. **Stackful vs stackless first → stackful.** The guest is stackful; a goroutine-per-agent engine runs it
   on the existing §5 `CanonCaller.Blocking` substrate (ADR 0069) — the goroutine *is* the model's `Thread`,
   so a blocking canon call parks it and a futex-notify wakes it. **This does not pull v2 §7 continuation
   stacks forward**: the Go runtime provides the growable stack and the switch that the model's stackful
   suspend (Concurrency.md:1399–1403) names. Stackless would add a callback trampoline the guest never uses.
2. **The 0.3 world split → implement 7 / stub-refuse-at-call 7 / refuse-at-bind on unmodeled kinds** (#735
   Decision 2). `wasi:io` is gone; no 0.2 import transfers. The `output-stream` *resource* path is replaced
   by a native `stream<u8>` with its own built-ins — the bulk of slice 1.
3. **Task-per-goroutine → yes**, consistent with H-1/H-3 and the H-2 amendment. **Caveat (contract text,
   Scott's):** `waitable-set.wait`/`{stream,future}.{read,write}` are a *new category* — guest-called
   engine-internal suspend points that §5 (scoped to "host calls") and §6 R-1/R-2 do not name. The tier needs
   §5's scope and §6's framing amended; this ADR names the need and does not write the text.
4. **The public async entry — left to Scott** (public API surface, an escalation subject): a mode on
   `ComponentConfig` vs a distinct `AsyncComponentConfig` vs consumer-triggered deferral (#735 Decision 4).

## The capability line, at its two evidence levels

Following ADR 0084's amendment: the claim distinguishes model-verified from model-and-guest-verified, not
collapsed.

*"An async-typed `wasi:cli/run@0.3.0` component runs on Burroughs behind `gate:async`, with each async import
that blocks suspending only its own task's goroutine — control returns to the caller and sibling tasks keep
running — while a non-async callee that would block traps, and backpressure/exclusive-lock admission is
honored."*

- **Model + real guest:** the async-hello's `run` lift, its `stream<u8>` stdout write, and its bound
  event-loop built-ins, byte-verified against wasmtime 48.0.1 and the `definitions.py` model.
- **Model only:** the async value lowerings a guest on the first path does not exercise (`future` payloads,
  `stream` reads, cancellation delivery), verified against `definitions.py` alone — stated "differential-only"
  per row when the per-type table is written, not papered over.

## Two oracles

- **Byte-exact:** `definitions.py @ 2bed77e` driven through the model's own calls (`Store.invoke`/`lift`/
  `lower`/`tick`), model-produced state only (#728 standard). The async generator is a **scheduler-driver**
  (it drives the model's cooperative scheduler), materially heavier than the sync value-lowering generator.
- **Behavioral:** the async-hello guest's committed wasmtime 48.0.1 reading, plus the **2 validation**
  `test/async` cases (`validate-no-async-abi-for-sync-type`, `validate-no-stream-char`) runnable as
  decode/validate refusals. The other 36 `test/async` cases need built-ins beyond the first guest — the
  roadmap for later slices, not slice 1's differential.

## First guest

The Rust `wasm32-wasip3` async-hello (recon #734), stackful, exports `wasi:cli/run@0.3.0`. Its bytes size
slice 1: the stackful lift, the `stream<u8>` stdout write, and the waitable-set/stream/future built-ins the
async-lowered stdio imports bind. The three relocated borrow-lifetime assertions (#708) are the tier's first
pre-registered assertions, all reachable on the async path (#734 C10).

## Consequences

- **Gated off by default.** `gate:async` lands off; acceptance is the async suite's own green (contract §9).
  The flip is behaviour 4's own stamp-tier event, its forecast pre-registered on #735 (the three borrow
  assertions, the refused-by-name set with a firing witness per entry, the inverted gate-off test, the named
  inputs) — the per-type value table deferred to when the async codec work is scoped.
- **Two gates behind us, this is the third:** `gate:components` (on), and this. Async is a Burroughs product
  line, not scaffolding.
- **Contract text is owed, not written here:** §5 scope, §6 R-1/R-2 framing, and §3's safepoint set must
  name the new guest-called suspend points — flagged for Scott, `type:contract`.
- **#716 (mid-read cancellation) stops being optional** at this tier — streams are completion-based and a
  blocking `stream.read` is cancellable.
- **The pin ages:** the async text is younger than the value-type text; a slice needing newer text re-pins
  with a dated amendment.
- **`gate:async`'s creation and this ADR's stamp are Scott's.** No engine code lands on this ADR alone.
