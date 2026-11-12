# 0117 — A host resource is an entry in the holding instance's own handle table, named by its WASI identity, and its drop is real

Date: 2026-11-12 · Status: **accepted**, decided in the slice. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes), and no stamped ADR is reversed, so by the #647 review it is decided here and reported as a decision taken. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1231](https://github.com/scttfrdmn/burroughs/issues/1231) (E3) and [#1137](https://github.com/scttfrdmn/burroughs/issues/1137) (contract §6 R-4's witness).

## Context

WASI 0.3's host-defined resources — `wasi:sockets`' `tcp-socket` first, `wasi:filesystem`'s `descriptor` later — need a guest to hold `own` and `borrow` handles to objects the host implements. What the tree had at 7321e3d:

- **Handles on the Host, not the instance.** `Host` kept `writers map[int32]io.Writer` and `errors map[int32]error`, each with a counter from 1, and removed nothing. `InstantiateWithHost` passes one Host's tables to the root walk and to every nested walk, so all the instances one Host served shared one numbering. A handle index minted in one instance named the same stream in another, where the reference's rule (def:Table.get) is that an index means something only in the table that minted it. That is the gap #1137 records as R-4.
- **Untyped handles.** The `own<error>` the host lowered carried `errorRT = 0`, and `get-directories`' `own<descriptor>` element was `canon.OwnType(0)` too. `get-stdin` minted an *output-stream* over `io.Discard`, and `blocking-read` ignored its `self` and read the Host's stdin. Nothing checked a handle's type, so nothing could tell.
- **No drop.** Every `canon resource.drop` bound `dropNoop`, whatever its type operand named. The four `[resource-drop]` keys in `wasi()` mapped to a no-op too, and were unreachable: a key there is a `canon lower`'s identity, and a resource drop is a `canon resource.drop`, which the walk binds itself.
- **A table that only the tests used.** `canon/codec.go` had `resourceTable` — slot 0 reserved, a LIFO free list, `add`/`get`/`remove` — and only the model heap of the differential used it. Its `numLends` was never incremented, and there was no borrow lift.
- **Local resource identity.** An imported `(export "output-stream" (type (sub resource)))` decodes to a placeholder `TDResource` with no name, and `own`/`borrow` carry a local type-space index (`canontype.go` maps it to `canon.OwnType(int(vt.Ref))`). So an RT was a local ordinal, not an identity.

## Options

1. **Keep per-Host maps and make them per-instance by key**, prefixing each handle with an instance number.
   - *Costs:* a second table discipline beside the codec's, with its own reuse rule that matches neither the reference nor wasmtime. Each new resource type needs another map.
2. **One typed per-instance table, the codec's own, exported**, with host resource types declared by their WASI identity.
   - *Keeps:* production uses the table the reference fixtures test, slot reuse included. A handle's type and lend count are checked where the reference checks them.
   - *Costs:* the stream and error handlers change shape, since they need the instance's table, and the tests that read the Host's maps change with them.
3. **Unify with `asyncHandles` now**, into the reference's single per-instance table of resources and waitables.
   - *Costs:* `asyncHandles` nils a freed slot and does not reuse it, with a stated reason; changing that moves every async fixture's handle numbers, and a resources slice is the wrong place to pay for that.

## Choice

**Option 2.**

1. **The codec's table is exported** as `canon.HandleTable` with `canon.ResourceHandle` entries: `NewHandleTable`, `Add(rt, rep, own)`, `Get`, `Remove`, and the reference's three handle rules on top of them — `LiftOwn` (def:lift_own), `LiftBorrow` (def:lift_borrow: the handle stays and its lend count rises) and `Drop` (def:canon_resource_drop's table half, which refuses a handle with a lend outstanding). `ResourceHandle.EndLend` is def:Subtask.deliver_resolve's decrement. The model heap's own lift and lower go through `Add` and `LiftOwn`, so the fixtures exercise the production methods.
2. **Resource identity is the host's WASI name, in ADR 0115's keying.** A host resource type is declared as `interface@class::resource`, through `serve`: `wasi:io/streams@0.2::output-stream`, `…::input-stream`, `wasi:io/error@0.2::error`, `wasi:filesystem/types@0.2::descriptor`. Its RT is a number minted once per declaration, so two declarations never share one — the reference compares `ResourceType`s by identity (def:canon_resource_drop:"trap_if(h.rt is not rt)").
   - At walk time the component **type** index space is recorded as it grows (`typeStep`). An entry that is an `alias export $i "name" (type)` of a host-filled instance gets the identity `import-name::name`, so `(alias export $streams "output-stream" (type))` over `wasi:io/streams@0.2.3` is `wasi:io/streams@0.2.3::output-stream`. A `canon resource.drop` resolves its type operand through that record and `lookupHost`. A nested instance reaches the same identity, because its instance argument is the parent's host-filled instance.
   - A host import's handles carry identity by the host's declaration: each handler is registered against the resource type it takes or returns. This is `stdioWriteResultOK`'s reasoning in ADR 0116: the host implements the interface, and the interface fixes the type.
   - **What the stop rule left:** the guest's *signature* still carries the local index. `canontype.go` maps `own`/`borrow` to `canon.OwnType(int(vt.Ref))`, and a host import's guest-side signature is not cross-checked against the host's declaration. Closing that needs the instance-type decoder to name its exported resources and `resolveVal` to carry the name through `own`/`borrow`. Nothing in this slice marshals a guest-signature `own`/`borrow` through the codec: the host handlers read handles as core i32s.
3. **A per-instance table**, `hostResources`, created in `walkComponent` beside `async`, so every nested instance gets its own. The host functions bound for an instance capture its table by closure, the existing pattern (`r.consumer(w.async)`). It holds the codec's table and the Go object behind each rep, under one mutex. A handler runs outside the mutex; what keeps a borrowed object alive across a handler is the lend, which a concurrent drop refuses.
4. **Host resource methods are a fourth host source**, `resourceMethod func(*hostResources) interp.CanonFunc`, the shape of `streamConsumer`, registered with `serve` and bound in `resolveHost` after the stream consumers and before the sync table. Typed adapters build them:
   - `method`, generic over the object type `T` and given the resource type and a handler, lends `self: borrow<T>` with `LiftBorrow`, checking its type, hands the handler the object as a `T`, and releases the lend at return, whatever the handler returns;
   - `takeAs[T]` transfers an `own<T>` argument with `LiftOwn`; the object passes to the host;
   - `mints(type, build)` and `hostResources.mint` lower a returned `own<T>` with `Add`.
5. **A real `resource.drop` for a host resource type.** An own is removed and its type's destructor runs once, outside the mutex: a per-type `dtor` func, or `Close` if the object is an `io.Closer`, or nothing. The stdio stream objects are deliberately not Closers, because the process's streams are not the guest's to close. An own with a lend outstanding traps; a borrow handle is removed and nothing more. A type the host does not implement — a guest-defined resource, or an unresolved type operand — keeps `dropNoop`, stated at the site: no guest can mint one while `resource.new` binds the refusing stub. The `[resource-drop]` keys are deleted rather than made real, since no guest could reach them.
6. **The 0.2 `wasi:io` handles move onto the per-instance table** with distinct types, which ends the RT-0 clash: `get-stdout`/`get-stderr` mint output-streams, `get-stdin` an input-stream, a failed write an error; `blocking-read` reads the stream its `self` names. An unknown `self` used to be refused as `ErrLinkRefused` (a write) or answered with the placeholder `"stream error"` (`to-debug-string`); both now trap, as def:lift_borrow does. `p3hello` and `p3echo` still match wasmtime.
7. **`canonbuiltins_test.go`'s `resources` status is split.** `resource.drop` is `resource-drop-host-types`, real for a host type only; `resource.new` and `resource.rep` are `resource-refused-at-call`. One status glossed *"modeled since ADR 0084"* had overstated all three.

### Out of scope, recorded

- **Guest-defined resources** (`resource.new`, `resource.rep`, `lower_borrow`'s borrow handles and their `borrow_scope`). They keep the refusing stub, and their drop keeps `dropNoop`.
- **One table with the waitables.** The reference keeps resources and waitables in one per-instance table (def:ComponentInstance); this engine keeps `asyncHandles` separate. **The divergence is stated:** a guest's resource handle numbers come from their own index space, so a resource handle and a waitable can share a number here where the reference would give them distinct ones. A guest can observe that only by relying on two handles of different kinds never coinciding, which the ABI does not promise.
- **Destructors at `Close`.** An own still held when the instance closes is released by the Go collector, and its destructor does not run. The reference has no instance teardown to copy; wasmtime drops a store's host resources with the store. A socket will need this, and it is that slice's to decide.

## Consequences

- **A repair the slice compelled.** `liftSignature` indexed `c.Types` by the lift's type ordinal directly, while its comment said the lookup was the section one. Ordinal and section position agree until a type alias precedes the lift's type, and no fixture before this one had one; the first did, and every export got the next export's type. It now goes through `sectionTypeAt`.
- **Witnesses:**
  - `TestHandleTableLifecycleFollowsTheReference` (canon): add, get, drop, slot reuse (the order read from a probe of the pinned `Table`), a wrong-type drop, a lend, a lent own refused as an own, a lent own refused as a drop, and a drop accepted once the lend ends.
  - `TestHostResourceHandleIsRefusedInAnotherInstance`: #1137's R-4 on real instances of `host-resource-synth.wasm`, served by one Host. An index minted in instance a traps in instance b, and a third instance's own handle at the same index reads its own object.
  - `TestHostResourceDropRunsTheDestructorOnce`: a counting destructor runs exactly once per guest drop; the freed slot is reused; a second drop traps and does not run it again.
  - `TestHostResourceBorrowIsLentForTheCallAndReleasedAtReturn`: the lend count is 1 inside the method, and a drop attempted then traps and runs no destructor.
  - `TestHostResourceOwnArgumentTransfersToTheHost`, and `TestHostResourceTypesAreDistinct`, the RT clash's control.
  - `TestHostResourceSlotReuseMatchesWasmtime`, the differential: `host-resource-reuse.wasm` prints the three indices `get-stdout` returns around a drop. wasmtime 49.0.3 reads `1 1 2`, 10 of 10; Burroughs at 7321e3d read `1 2 3`; this build reads `1 1 2`.
  - `TestStreamErrArmLowersAnOwnErrorHandle` now also asserts the guest's drop of its `own<error>` freed it.
- **Neuters, run:** `Drop` without its lend check fails the lifecycle's lent-own row and the borrow test. One table shared by every walker fails R-4's witness (`b.poke(7) = 101`), the differential (`8 8 9`), and two drop rows. `dropNoop` bound for every type fails the destructor count (0), the differential (`1 2 3`) and the stream-error test's drop assertion. `liftSignature` back on the direct index fails the four `host-resource-synth` tests and no other.
- **No contract text changes, and no public API.** An embedder can observe the drop's effects only through a guest: handle numbers reused as wasmtime reuses them, and a stale or foreign handle trapping where it used to be answered.

## Rollback

Binding `dropNoop` for every type and sharing one `hostResources` across walkers restores the old numbering and the no-op drop; the two neuters are that rollback, run. They do not restore the old answers to a stale or wrongly typed `self`, which trap now through the typed adapters; reverting those means reverting the handlers' shape.
