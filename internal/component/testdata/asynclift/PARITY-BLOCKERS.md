# The parity witness: three blockers, measured 2026-10-03

2a-iii's recon record. The witness registered on
[#857](https://github.com/scttfrdmn/burroughs/issues/857) — drive the committed `suspending` guest's `run`
concurrently and sequentially through an internal embedding, against `concurrent.reading` and
`sequential.reading` — **cannot run at all**. This is the "before" state, recorded as an artifact rather than
left in a comment.

**It is not one blocker.** Three bite, in this order, and the first is earlier than anything the registration
anticipated.

## 1. The guest's `tick` import has no host path

The suspending guest declares a **bare world-level import**:

```
  (import "tick" (func $tick (;0;) (type 0)))
```

`walkComponent`'s `host` parameter is what resolves such an import, and it is **`stubHost` at both top-level
call sites** (`link_component.go:594`, `:620`), which *"fills every import with a permissive refusing
instance"*. The only non-stub `host` in the tree is `argHost` (`:284`), for a **nested** component's
instantiation arguments.

**Probed, not inferred.** Supplying `tick` through `h.asyncImpls` under three spellings — `tick`,
`$root::tick`, `actual::0` — leaves the call failing and the impl entered **zero** times:

```
run -> [] err=host function trapped: component: link refused:
       import actual::0 is not provided (stub host)   (tick entered 0 time(s))
```

`actual::0` is wit-component's indirection: it inserts a `shim` module and an `actual` instance, so the
guest's real imports arrive **by index** rather than by WIT name. Mapping that index back to `tick` is part
of [#870](https://github.com/scttfrdmn/burroughs/issues/870).

**This corrects a conclusion recorded on [#863](https://github.com/scttfrdmn/burroughs/issues/863):** that
the host-import mechanism already exists. It does, for imports shaped as `interface::export` and reached
through `wasi()` / `asyncImpls` — and **not** for a bare world-level import, which goes through `host`
instead. The distinction was collapsed there.

## 2. A guest that suspends cannot complete

The lift loop handles exactly one dispatch code:

```go
case callbackExit:   // the task resolved via task.return
default:
    // WAIT / YIELD: the callback re-entry and the park — step 2, not the EXIT-only skeleton.
    return fmt.Errorf("%w: async-lift dispatch code %d (park/yield) is step 2, not yet built", ...)
```

The `cb` core func is **bound and never invoked**. The suspending guest awaits `tick`, so its lift returns
WAIT — which is refused by name. [#871](https://github.com/scttfrdmn/burroughs/issues/871).

## 3. One lift task per component instance

`asyncHandles` carries a single `lift *liftTask` and is built at **one** call site, `walkComponent` — so its
scope is per **instance**. The loop traps a second entry.

Two concurrent `run` calls therefore cannot both be in flight, which is precisely the property
`concurrent.reading` measures. [#869](https://github.com/scttfrdmn/burroughs/issues/869).

**The more dangerous half is not that assertion.** `link_component.go:103` reads `f.h.lift.resolved` *after*
the callee returns. With one task that is correct; with several it reads **another task's** resolution — a
silent wrong answer rather than a trap.

## What this record corrects about its own forecast

An earlier report said the **sequential arm should reproduce** while only the concurrent arm would fail. That
was an expectation stated before it was run, and it was wrong: blocker 1 stops the guest being entered at
all, so **neither arm runs**. The sequential reading is #871's acceptance target, not a prediction.

## And a prose defect, fixed in the slice that found it

The at-most-one trap and `liftTask`'s comment both said *"per agent"*, while the field lives on
**instance-wide** handles with a single call site. **The prose claimed something narrower than the code
enforced**, so a reader would believe two agents could each hold a lift. Both now say "instance", which is
the scope actually enforced — and #869 is where it becomes per-agent, which is what the comment always
claimed.

## Addendum (2026-10-03) — blocker 1 is resolved, and removing it brought blocker 2 into view

**Appended, not edited.** The text above is the measurement as it stood, and the value of a "before"
record is that it still reads as one.

### Blocker 1 is gone (#870)

The defect was the **kind** of thing the resolver returned, not the absence of a resolver: `stubHost`
answered every import with a stub *instance*, so a bare world-level function import produced a `compDef`
whose `.fn` was nil — and `walk.go`'s impl lookup is guarded on `fn != nil`, so **it never ran, for
provided and unprovided imports alike**.

Fixed where the sort is known: a function import now gets a function-shaped stub whose `stubName` is the
import's **own name**, so the existing lookup does all the work, keyed `tick`, with no second dispatch
path. A second gap came with it — `lowerSignature` returned nil for any name without `::`, so the lowering
had no signature to marshal against; it now resolves a bare name through the `ExternFunc` import's own
`TypeIndex`.

### The refusal naming: a claim written here before it was measured, and falsified by its own arm

**This section said the repair above also made an unprovided bare import refuse as `tick` rather than
`actual::0`. That was wrong when written.** It was inferred from the kind fix — the stub now carries
`stubName: "tick"` — and never run. The refusal arm then asserted `tick` and got:

```
host function trapped: component: link refused: import actual::0 is not provided (stub host)
```

The inference missed a layer. `compFunc.stubName` is refused at `link_component.go`, but an
**unimplemented canon lower** never reaches it: the lower is appended with `lowerName: "tick"`, no impl
is found in `asyncWasiHost`, and the CORE-level resolver falls through to `refuse(mod, name)` — whose
`mod`/`name` are the core module's import strings. So the message was built from coordinates one layer
below the name anyone could have supplied.

Repaired at that site: `refuse` now takes the lower's component-level identity and names it as the
subject, keeping the core coordinates as a trailing diagnostic (`import tick is not provided (stub host;
lowered at core actual::0)`). This improves every unimplemented lower's refusal, not only a bare one — a
missing WASI method now names the interface and method rather than wit-component's indirection.

**The lesson is the ordering, not the layer.** A claim about a message is one `go test` away from being
measured, and this one was published in the same breath as the fix it was inferred from. The arm existed,
which is the only reason the correction is here rather than in a later session's grave.

### Both probe outputs, which is how the chain was confirmed rather than inferred

Inline-resolving `tick` — the guest runs to completion:

```
PROBE run -> [u32 7] err=<nil>   (tick entered 1)
```

Deferring `tick` — the guest parks, and the run stops exactly where the record above predicted:

```
DEFERRED run -> [] err=component: gate:async is on but the async tier's execution is not yet
                 implemented: async-lift dispatch code 2 (park/yield) is step 2, not yet built
```

So **removing blocker 1 exposes blocker 2 at the next step, naming the dispatch code.** That is the
registered chain measured rather than reasoned, and it makes #871's subject reachable instead of
hypothetical.

### A correction this addendum carries

`actual` is **never resolved through `host`.** The original record left that as an open hypothesis — "the
real `actual` cannot be built, so something downstream falls back" — and reading the component settled it
the other way: `actual` is an inline-export **core instance the component builds itself**, whose export
`"0"` *is* the canon lower of `tick`, indexed because wit-component's fixup module patches a shim table by
index. The hypothesis was wrong, and it was registered as a hypothesis precisely so it could be.

### The inline result has a reference now

`run -> 7` above is Burroughs' own output, and when first obtained it had **nothing to check against**:
every committed reading here uses the rendezvous `tick`, which defers. So `inline.reading` was captured
from wasmtime on the same arm — `tick` resolving at once with `id + 100` — and the parity witness asserts
against **that committed value**, not a literal:

```
OUTCOME   inline: run(1)=Ok((101,)) run(2)=Ok((102,))
```

Two calls, because one cannot tell a passthrough or a constant from a real `id + 100`. **Burroughs' own
number is not evidence until the reference agrees** — the same discipline as the `compute` readings.

### The witness watched die, and the two halves fail differently

Each half of the repair was neutered in turn, because an arm that has only been watched pass is a
forecast. The result is worth recording, since the halves are not interchangeable:

| neutered | provided arm | refusal arm | deferring arm |
|---|---|---|---|
| the kind fix (`funcStep`) | FAIL — `link refused: actual::0` | FAIL | FAIL |
| `lowerSignature`'s bare branch | **FAIL — `run(1) = 0`, no error** | pass | pass |

**The second row is the dangerous one.** With no signature the lower binds with `hasResult` false, so the
host's resolution is discarded and `run` returns **0 with no error at all** — a silently wrong value, not
a refusal. The host impl is still entered and still receives the right argument, so every check short of
the returned value passes. That is precisely the defect class a reading with no reference cannot catch:
asserting "the import was entered" would have passed, and so would asserting "no error". Only comparing
the value against wasmtime's committed reading fails.

## Why this is a record and not a tripwire test

A test asserting these blockers *persist* would fail the moment #870 lands, which is the next slice. The
measurements are reproducible from the probe above, and each blocker's own slice carries the witness that
supersedes it. **What a tripwire would protect against — the limitation being forgotten — is instead held by
three registered issues with acceptance tests.**
