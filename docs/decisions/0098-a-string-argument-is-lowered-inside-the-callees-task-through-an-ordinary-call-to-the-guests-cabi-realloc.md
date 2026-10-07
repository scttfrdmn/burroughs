# 0098 — A string argument is lowered inside the callee's task, through an ordinary call to the guest's `cabi_realloc`

Date: 2026-10-07 · Status: **accepted** — the chair's direction on the #914 review, given to settle three mechanism choices before implementation · [#902](https://github.com/scttfrdmn/burroughs/issues/902) · Implements the argument half of [ADR 0097](0097-the-public-compound-value-surface-componenttype-plus-string-list-and-record-stamped-on-a-four-point-summary-with-three-changes.md)
Ratio-Class: carried

Recorded by the actor the direction was given to, so no independent provenance — `carried` rather than `ordered`, because this is mechanism and not a principal's stamp on a stated summary. ADR 0097's stamp covers the public names; nothing here adds one.

## Context

ADR 0097 landed a `string` **result** crossing the public boundary and refused a `string` **argument** by name, because lowering one means putting bytes in the guest's linear memory. Recon turned up that this is a layer deeper than "call the guest's realloc":

- **`CanonCaller.Realloc` cannot be used.** It works off `c.realloc.owner.resolveCall`, `c.st` and `c.t` — a live call's stack and thread, which exist only *inside* a guest call. Argument lowering happens before one.
- **`interp.Instance` exported no memory accessor at all** — `Invoke`, `Global`, `Module`, `Deferred` and nothing else. So writing the bytes had no API either.

And it lands on a constraint already decided: [ADR 0073](0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md) — *grow refuses to relocate when a sibling agent could hold the old image, and the boundary accessors take the growth lock.* A host-side write is exactly such an accessor.

## Decision

### 1. The lowering happens inside the callee's task, on the entry the callee runs on

The model lowers a lift's parameters inside `canon_lift`'s thread body: `enter_implicit_thread()`, then `task.start()`, then `lower_flat_values(...)`, and only then the core call (`definitions.py:2097-2107`, read at the pin). The lowering is inside the callee's exclusive context, not before it.

Burroughs' analogue of that context is the instance's **entry slot** (`asyncHandles.entrySem`), so the allocation, the write and the first callee call share **one** acquisition. Doing the lowering outside it would let a sibling call into the instance between the allocation and the call that uses it — and a guest is entitled to reuse anything it likes on an entry it owns.

**Consequence, and the reason this shapes the code rather than just the comments**: a `string` argument cannot be lowered in `CallValuesCtx`, where the scalars are flattened, because nothing holds the entry there. So the flat argument list is built with **placeholder slots** and a `pendingLower` record, and the work runs in `runLiftTask`. `lowerPending` deliberately does **not** acquire the entry itself — acquiring it there would mean releasing it between the allocation and the call, which is the thing the entry exists to prevent.

**A pending lowering on a sync lift is refused by name.** The sync path has no task and no entry slot, so there is nowhere to satisfy this ordering.

### 2. The realloc is called as an ordinary guest call

Through `inst.Invoke`, the same path an embedder's call takes: it creates the thread and stack, runs the safepoint poll, and is **visible to a stop-the-world**.

**The alternative — synthesizing a `CanonCaller` to borrow the adapter's path — is refused.** It would be a second route into guest code that the safepoint and lock controls have never been checked against, and this engine has already paid twice for a guest-entering path that one control could not see (§5 H-1's missing excursion, grave #892's sibling). A new one bought for convenience is not worth the review surface.

### 3. The write goes through one locked boundary accessor, and the image resolves inside the lock

**`Caller.Write` already was that accessor** — it resolves the memory, takes `growMu.RLock()` across the image load and the copy, and its own comment says this is the direction ADR 0073's decision 6 was built for. So the decision here is **not** new locking logic; it is to avoid writing a second copy of that contract.

The three locked lines are factored into `writeUnderGrowthLock`, and `interp.WriteBoundaryMemory(mem *Extern, offset, buf)` is the entry point for a host with no `Caller`. A **free function rather than a `Caller` constructor**, because a `Caller` carries a thread identity and a context this path has neither of, and fabricating a zero `ThreadID` to fill a field nothing reads would be a value that looks like an answer. It runs no guest code, so §5 **H-2** is untouched: the guest is not re-entered, only its bytes are written.

**Resolving the image inside the lock is load-bearing here and not merely tidy**: `cabi_realloc` can **grow the memory on its way to returning the pointer**, so a base and length captured before the call would describe an image that no longer exists.

### 4. Ownership follows the model: the callee owns it, and the host never frees

`lower_flat_values` has no matching free, and there is no "unlower" anywhere in the model. A lowered argument belongs to the callee once the call is made; the callee's `post-return` (sync) or its own bookkeeping (async) reclaims it.

So this engine allocates, writes, and **does not touch the allocation again** — no free, no re-read, no retained pointer. Recorded because the opposite instinct is strong: a host that allocated something usually cleans it up, and doing so here would free memory the guest still owns.

## Consequences

- **The fixture is `string-arg-realloc-synth.wasm`**, whose realloc **grows** and returns a pointer into the page it just added, and whose `echo` returns the **sum of the bytes it read** — so the assertion is on content at the right address, not on the call completing. Three lifts, because three failure modes are unrelated: the working path, a realloc whose body is `unreachable`, and a `peek` that reports the byte at the allocation so the ownership rule is observable.
- **No real toolchain produces this guest.** `wit-bindgen` allocates into a pre-grown heap, so a write against a stale image would land correctly anyway and the hazard would be invisible.
- **A lift declaring a `string` parameter and no `(realloc)` cannot be built**: `wasm-tools` refuses it at validation. The engine must still not dereference a zero `coreDef`, so that arm is witnessed against a hand-built `compFunc` — *a negative claim buys a branch an exemption only if something checks the exemption.*
- **The locking half is not witnessed by this slice, and the limit is stated in the test.** Excluding a *concurrent* relocating `grow` needs a second agent growing while the write runs. The `RLock` is the one `Caller.Write` has always taken, and [#586](https://github.com/scttfrdmn/burroughs/issues/586)'s litmus work is where that exclusion is measured.
- **A null pointer for a non-empty request is refused**, as is a negative one: a realloc answering 0 has allocated nothing, and writing at 0 would corrupt whatever the guest keeps at the bottom of its memory. An empty request legitimately gets any pointer and writes nothing.
- **The release still waits.** ADR 0097's ruling is unchanged: merge only, no version cut until lists and records work too.
