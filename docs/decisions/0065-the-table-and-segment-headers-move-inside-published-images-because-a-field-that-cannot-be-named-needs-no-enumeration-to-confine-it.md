# 0065 — the table and segment headers move inside published images, because a field that cannot be named needs no enumeration to confine it

Date: 2026-09-04 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, and it changes no gate's default. What Scott did order is
that this slice happen next and that its mechanism is already built — recorded on
[#622](https://github.com/scttfrdmn/burroughs/issues/622#issuecomment-5547553498) before any code, because
a transcript is not an artifact. Recorded by the actor it was given to, so *durability is not independence*
and every commit in the slice stays `Ratio-Class: carried`.

Filed against **[#622](https://github.com/scttfrdmn/burroughs/issues/622)**, split out of
[#573](https://github.com/scttfrdmn/burroughs/issues/573) at the mechanism seam: #573's remaining classes
want a multi-word *element* write mechanism and these three want the one
[ADR 0058](0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md)
already built.

## Context

Three sites publish a three-word slice header to an object that is already reachable:

    internal/interp/table.go:table.grow            t.slots = grown
    internal/interp/segment.go:elemInstance.drop   s.refs  = nil
    internal/interp/segment.go:dataInstance.drop   s.bytes = nil

`unsafe.Sizeof([]ref(nil))` is 24 bytes — pointer, len, cap — measured on #622 rather than read off the
declaration. This is the **memory-unsafe** class of #573's domain: every other race there produces a value
neither thread wrote, and this one produces a *wrong memory*, because a reader that pairs a `nil` pointer
with a stale non-zero length indexes off address 0.

0058's mechanism transfers with the subject swapped, and its title is the part that generalises —
*reachability is not a spawn-time property* says nothing about linear memory specifically. `Spawn` (#554)
runs the entry function in the same `*Instance`, so a table and a segment are reachable from a second thread
for the identical reason a memory is. Writing that transfer down is still a decision; it is a **successor**
citing 0058 rather than a fresh mechanism argument, and 0058's own two paragraphs — immutability once
published, and a struct rather than an `atomic.Pointer[[]ref]` so the type states the immutability — are
quoted in #622's body rather than re-argued here.

### The one thing that is new, and it is why this ADR exists rather than a commit message

[ADR 0064](0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md)
had to buy the extent of its plain region with an enumeration and a control over it, because there was no
way to make a plain access unwriteable. Here there is. Moving `slots`, `refs` and `bytes` **inside** the
image structs does not merely make the racy publication avoidable — it makes `t.slots` a compile error, so
the claim *"every access to this header goes through the published image"* holds by construction and needs
no scan to assert it. That is the same dissolving move 0058 itself took on
[#575](https://github.com/scttfrdmn/burroughs/issues/575) — `m.bytes` does not exist to be written, which is
why no control has to watch for it — and it is Scott's own reading of this slice, recorded on the #637
review: *"moving `slots`/`refs`/`bytes` inside
the image structs so a direct field access stops compiling makes the enumeration hold by construction rather
than by a scan."*

The alternative shapes all keep the field nameable — a mutex around the header, an
`atomic.Pointer[[]ref]`, a `Store`-only convention with a comment — and each of those would owe 0064's
enumeration-plus-control for a region three files wide. **Choosing the shape that removes the obligation is
cheaper than discharging it**, and that, rather than any performance argument, is what decides between
mechanisms whose safety properties are identical.

[0063]: 0063-a-numeric-globals-single-word-goes-atomic-and-a-v128s-pair-goes-under-the-globals-own-mutex.md

## Decision

1. **`tabImage`, `elemImage` and `dataImage`**, each a struct holding the one slice, each published through
   an `atomic.Pointer` on the owning object. The slice fields move inside; `table`, `elemInstance` and
   `dataInstance` keep no header field, so a direct access does not compile.

2. **All three accessors are named `view()`**, matching `(*memory).view`. This is the load-once control's
   predicate, so covering four subjects is a **rename of the existing control** rather than a fourth one:
   `TestEveryMemoryOperationLoadsTheImageAtMostOnce` becomes
   `TestEveryOperationLoadsAPublishedImageAtMostOnce`, its domain unchanged (every function in every
   non-test file of this package) and its floor re-pinned. Scott's words on the #637 review: *"a rename
   rather than a fourth control, which is better than either building one or widening a domain by hand."*

3. **`size()` counts as a load, and the two branching call sites are restructured so it can.** Each of the
   four `size()` methods is `uint64(len(x.view()))`, so a function calling `seg.size()` and then
   `seg.view()` loads twice while showing the control one textual `view()`. Widening the predicate to
   `{view, size}` is exact rather than approximate — `grep -n '^func (.*) size('` over the non-test files
   returns four lines and they are these four subjects, so no unrelated type can join the population by
   name. Two sites then over-report because they load in mutually exclusive branches (`memory.size`'s
   i32/i64 arms in `exec.go`, `table.size`'s in `truncsat.go`); both are hoisted to one load before the
   branch, which is the shape the rule asks for anyway.

4. **`table.grow` serialises on its own `growMu`, with `t.limits.Min` inside the critical section**, which
   is [ADR 0061](0061-grow-serialises-on-its-own-mutex-rather-than-a-compare-and-swap-over-the-descriptor-because-the-length-lives-in-two-places-and-only-one-is-in-the-descriptor.md)
   transferred with its *reason* and not only its shape. 0061's title is the reason: the length lives in two
   places and only one is in the descriptor. `table` has the identical two places — the slice's length and
   `t.limits.Min`, which `type_of` reads at import-match time — so a compare-and-swap over the image would
   relocate the defect to the copy it cannot reach.

5. **A drop publishes a shared empty image rather than a fresh one.** `elem.drop` on an already-dropped
   segment is legal and does nothing (`bulk.wast:261` drops twice), so a guest can execute it in a loop; a
   `&elemImage{}` per execution would be a guest-triggered allocation per instruction. One package-level
   empty image per kind is sound for the reason every published image is: it is never written after
   publication, so sharing it between segments is unobservable — nothing in the semantics distinguishes
   *dropped* from *declared empty*, which is exactly why the dropped state is a value rather than a flag.

6. **No reslice arm and no `noMove` mark for tables**, and this is a *difference* from memory rather than an
   omission. Memory has both because
   [decision 0056](0056-the-no-move-mark-is-set-where-the-reservation-happens-and-grow-refuses-on-the-mark-because-spawn-can-establish-it-while-one-thread-exists.md)
   reserves capacity for a memory whose array must never move, and that reservation exists because ADR 0051's
   atomics hold a raw pointer into the array across an access. Tables reserve nothing, no atomic
   instruction addresses a table, and the threads proposal at this pin has no shared tables, so every
   `table.grow` relocates. Copying memory's two arms across would have been *check a ruling's premises, not
   just its conclusion*: the shape is visible and the reservation is the load-bearing part.

## Options considered

- **(A) The chosen shape.** Fields inside the images, `view()` on all three, one renamed control. Costs one
  indirection per table access and per segment read — the same cost 0058 pre-registered and measured for
  memory on `internal/interp/membench`, in a place with a *smaller* access rate than linear memory's, since
  a table is read once per `call_indirect` and a segment once per bulk operation.
- **(B) A mutex per table and per segment.** Correct, and strictly worse on both axes that matter here: it
  serialises readers that 0058's mechanism lets run concurrently, and it leaves `t.slots` nameable, so the
  no-direct-access claim reverts to testimony plus a scan — 0064's bill, for a property (A) gets free.
- **(C) `atomic.Pointer[[]ref]` with no image struct.** Same safety, and refused for 0058's own stated
  reason rather than a new one: a reader of that type cannot tell from the type whether the header it is
  about to dereference is still being written.
- **(D) Leave the drops alone and fix only `table.grow`.** Refused because the drops are the *cheaper* half
  of the same defect and the mechanism is shared; splitting them would leave two of #622's three sites open
  behind a decision that had already been written for them.
- **(E) Fold `t.limits.Min` into `tabImage` so the size lives once.** Not refused — **deferred, and it is
  memory's open question too**, filed as the coherence residual below. It would make the size a single
  published fact, which is 0061's *other* option, and taking it here for tables while memory keeps two
  copies would leave two sibling subjects on different mechanisms for no reason either ADR gives.

## Consequences

- **#622's three sites are memory-safe against a concurrent reader**, and the abandoned array stays alive
  and in bounds for as long as any reader holds the descriptor naming it — 0058's property, unchanged.
- **The load-once rule now covers four subjects and two accessors**, and its failures name the receiver
  expression as before. The approximation 0058's control already stated — two spellings of one object in one
  function pass the check — is unchanged and still stated.
- **Two witnesses per new subject would be four more tests, and there are two instead.** The relocating-grow
  race test is written once for tables, because `elem.drop` and `data.drop` publish an *empty* image and
  have no relocating arm to race; the published-descriptor-immutability test covers all three subjects in one
  function, since the assertion is identical and only the fixture differs.
- **Nothing here is reachable on `main`.** `Spawn` (#554) is not on `main`, so this is blocker work for #554
  rather than a live defect, exactly as #622 says.

### The coherence residual, which this does not repair

A reader holding a pre-grow `tabImage` reads a **wholly stale table** — no tearing at all, because the
header it holds is internally consistent and names the array `grow` abandoned. A `table.set` into that array
is lost. This is the table's twin of **[#586](https://github.com/scttfrdmn/burroughs/issues/586)**, it is
0058's residual rather than a new one, and it needs [#10](https://github.com/scttfrdmn/burroughs/issues/10)'s
allowed-outcome tables to say what is permitted before any code can be right about it. Stated here because
the mechanism above makes the *unsafe* half go away and a reader who stops at that would conclude the whole
question is closed.

**No measurement is taken in this slice and none is claimed.** The one-indirection cost is inherited from
0058's measured result on a different subject; if a table-access benchmark is ever wanted, that is its own
work with its own pre-registration, and the rollback for this mechanism is the same one 0058 states — the
mechanism is removable by moving the fields back out, which the compiler then reports at every call site.
