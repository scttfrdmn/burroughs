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

## Why this is a record and not a tripwire test

A test asserting these blockers *persist* would fail the moment #870 lands, which is the next slice. The
measurements are reproducible from the probe above, and each blocker's own slice carries the witness that
supersedes it. **What a tripwire would protect against — the limitation being forgotten — is instead held by
three registered issues with acceptance tests.**
