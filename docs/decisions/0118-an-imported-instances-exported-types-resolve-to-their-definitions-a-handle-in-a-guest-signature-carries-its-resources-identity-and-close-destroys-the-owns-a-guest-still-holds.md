# 0118 — An imported instance's exported types resolve to their definitions, a handle in a guest signature carries its resource's identity, and Close destroys the owns a guest still holds

Date: 2026-11-12 · Status: **accepted**, 2026-11-12. Scott, in session, approving the plan for v0.14.0's fourth slice (#1248 with #1252). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes), and no stamped ADR is reversed. It decides the question ADR 0117 left to *"that slice"*, destructors at `Close`, because #1252 asks for it. Filed against [#1248](https://github.com/scttfrdmn/burroughs/issues/1248) and [#1252](https://github.com/scttfrdmn/burroughs/issues/1252).

## Context

E2 and E3 left three gaps, each recorded in the ADR that left it.

- **A `use`d type decodes as a placeholder** (ADR 0116, #1248). WASI types such as `wasi:cli/types`' `error-code` reach a guest through an imported instance's type export, and the guest's own signature could not name them. `write-via-stream` decoded as `future<ref 3>`, so #1246's repair took the payload type from the host's declaration (`host.go:stdioWriteResultOK`). E4, E6 and E7 all take `use`d records, enums and variants, so without this each would restate every WASI type on the host side.
- **Own and borrow in a guest signature carry a local type index** (ADR 0117, #1252.1). `canontype.go` mapped them to `canon.OwnType(int(vt.Ref))`, and a guest's declared resource types were never checked against the host's.
- **Owns still held at `Close` are never destroyed** (ADR 0117, #1252.2). `walker.close` never touched the instance's table of host resources. E7's sockets need this.

Reading the decoder at bf4910b, #1248 had **three causes, which must land together**:

1. `exportDecl`'s `0x03` arm and `typeBound` dropped the bound — eq or sub, the eq target, and the export name — and appended a resource placeholder for every type export. `InstanceType` kept only its func exports.
2. A top-level `alias export $inst "x" (type)` was never resolved. `typeSpaceToTypes` returns -1 for an alias ordinal, so `sectionTypeAt` failed, and an `alias outer` naming one became `VUnresolvedAlias` (`instanceAlias`).
3. `resolveVal` had no `future`/`stream` arm, and `valHasUnresolvedAlias` had neither those nor a `record` arm.

Fixing only the first turns p3hello's `[method]descriptor.stat` into a signature carrying `VUnresolvedAlias`, because its `datetime` reaches it through cause 2, and the walk refuses such a signature at instantiate (the #753 refusal).

## Options

For #1248:

1. **Resolve in the decoder**: keep each type export's definition, and resolve a top-level alias of an imported instance's type export to it. The `alias outer` resolution #756 built then inlines it.
   - *Keeps:* one resolver, the decoder's, which every signature consumer already reads.
   - *Costs:* aliases out of instances the component instantiates itself stay unresolved (their exports' types are the nested component's).
2. **Resolve in the walk**, where the instance space holds the real instances.
   - *Costs:* the signatures the binding checks are read from the decoded types, not the walk, so a second resolver would have to agree with the first.
3. **Keep restating each WASI type on the host side.**
   - *Costs:* every later row restates every type, and nothing checks a restatement against the guest.

For #1252.1:

1. **Carry the identity on the handle**: `ValType.Resource`, filled in by `resolveVal`, in the form `typeStep` already builds (`interface@version::resource`), and check a host-served import's signature against the host's resource types at instantiate.
2. **Keep the local index and look it up at each use.**
   - *Costs:* the index is an ordinal of the scope the type was declared in, and a resolved type is copied across scopes, so the lookup has the wrong-type-that-exists hazard `typeAt` was written to prevent.

For #1252.2:

1. **Destroy the remaining owns when the instance closes**, as wasmtime drops a store's host resources with the store.
2. **Leave them to the Go collector**, ADR 0117's position. It runs no destructor, so a socket a guest never dropped stays open.

## Choice

**Option 1 in each case.**

1. **The bound is kept.** `typeBound` returns `(eq, idx)`, and `externType` reports `sub resource` alongside the index.
   - Inside an instance type, `(export "x" (type (eq N)))` introduces a copy of `local[N]`. `(sub resource)` introduces a `TDResource` whose identity is `selfResourceID(x)`, `"::x"`. An instance type does not know which import it types, and one instance type can type several.
   - `InstanceType.Types` records each type export by name, resolved against the instance's own space before anything copies it out (`InstTypeExport`).
   - A top-level type import keeps its bound the same way. An eq import is its target, and a `sub resource` import is a resource whose identity is the import's name (`Import.SubResource`).
2. **Top-level type aliases resolve** (`Component.typeOrdinal`). The type-space ordinal of an `alias export $inst "x" (type)` maps through the instance space to the instance import, then to the import's `InstanceType`, then to its type export `x`. That export's unbound identities are completed with the import's name (`qualifyResourceID`). `sectionTypeAt` answers through it, so `alias outer` inlines the definition. **Scope: imported instances only.** An alias out of a guest-instantiated instance stays unresolved and is still refused by name where a value of it would be marshaled.
3. **Futures and streams.** `resolveVal` and `valHasUnresolvedAlias` follow a future's or stream's payload, and `valHasUnresolvedAlias` gains the `record` arm. `unmodeledValKind` is unchanged: it refuses a future or stream as itself, so no payload could change its verdict.
4. **Resource identity.**
   - `ValType.Resource` is set on an own or borrow whose index names a `TDResource` with an identity. Once set it is kept: a type copied into another scope still has its old scope's ordinal in `Ref`.
   - `lowerSignature` completes the import's own resources with its name, so `[method]output-stream.write`'s self is `borrow<wasi:io/streams@0.2.3::output-stream>`, and its `own<error>`, reached through an outer alias, is `wasi:io/error@0.2.3::error`.
   - `canonTypeWalk` takes a resolver (`canonTypeIn`): an identity the host implements carries that resource's RT, and every other handle keeps its local index. A guest-defined resource has no identity until #1251.
   - **The cross-check at instantiate**, for every import some host table serves (`checkHostResources`). Every own and borrow in its signature must name a host resource type. A `[method]R.m`'s self must be a borrow of `R` as the import's interface names it. A mismatch is refused with `ErrLinkRefused`, naming the import and the resources.
5. **The consumer for #1248.** A stream consumer carries its declared future payload type (`streamConsumer.payload`). `write-via-stream`'s is `stdioWriteResultType`. At instantiate, the guest's resolved payload must be `canon.TypeEqual` to it, or the lower is refused (`checkConsumerPayload`). #1246's workaround becomes a checked agreement. The completion is still lowered at the host's type, and the check is what makes that the guest's type too.
6. **Destructors at `Close`.**
   - `canon.HandleTable.Each` is the reference's `def:Table.__iter__`.
   - `hostResources.closeAll` removes every remaining own under the lock and runs each destructor once, outside it, as `drop` does. The table records each minted own's type, so it can find the destructor.
   - `walker.close` calls it across the tree of nested instances, **after** the core instances close, because `interp.Instance.Close` waits for the host calls in flight. So no handler is still using an object when its destructor runs. A second `Close` finds an empty table.
   - **An own still lent to a call is not destroyed.** That can happen only when the close's wait for host calls expired. The object leaks, which is better than destroying it under the call.
   - The reference has no instance teardown to copy. wasmtime drops a store's host resources with the store. A destructor's error at teardown has no reader, since `Close` returns no error, so it is dropped, with the suppression named at the site.

### Out of scope, recorded

- **Guest-defined resources** (#1251). Their handles keep a local index and no identity, and the cross-check refuses one in a host-served import's signature, since the host's table cannot hold it.
- **Aliases out of guest-instantiated instances.** They stay unresolved. If a guest reaches one, that is a follow-up issue.
- **The bare world-level import path** of `lowerSignature` still maps its type through `typeSpaceToTypes` and is unchanged.
- **A nested component's lowers are not cross-checked**, and this is a gap `lowerSignature` already had. A nested lower's identity is the *outer* import's name (`test:res/things@0.1.0::make`), and `lowerSignature` looks that name up among the *nested* component's imports (`things`). It finds nothing and returns nil, so the unmodeled-kind refusal, the #753 refusal and this ADR's checks all have no signature to read. `host-resource-nested.wasm` is a specimen. Closing it means resolving a nested lower's signature through the instance argument, which is its own change.

## Consequences

- **A repair the slice compelled.** `resolveVal`'s `seen` map was meant to be the current chain, but nothing removed an index from it on the way back out. So a second sibling reference to the same type read as a cycle. p3hello's `descriptor-stat` names `option<datetime>` three times, and once the type exports resolved, the second and third became `VUnresolvedAlias`, and p3hello was refused at instantiate. Nothing had reached this while a `use`d type was a resource placeholder, because a placeholder is not followed. The index now leaves `seen` when its chain returns.
- **Witnesses** (`resolveimported_test.go`, on real fixtures where one exists):
  - `TestAUsedTypeDecodesAsItsDefinition`: p3async-hello's `write-via-stream` decodes as `future<result<_, enum{io, illegal-byte-sequence, pipe}>>`.
  - `TestAUsedCompoundTypeIsResolvedInItsOwnScope` (`use-compound-type.wasm`): a `use`d record whose field is a local reference keeps the meaning of that reference.
  - `TestUsedTypesResolveInP3HelloAndEveryImportStillBinds`: each of `descriptor-stat`'s three timestamps is `option<datetime>` with its record. Every p3hello and p3echo import resolves with no unresolved alias, and both guests instantiate against the real host.
  - `TestAWorldLevelTypeImportResolvesThroughItsBound` (`world-type-imports.wasm`): an eq-bound and a sub-resource type import.
  - `TestAnUnresolvedAliasIsSeenInsideAFutureAndARecord`.
  - `TestAGuestHandleCarriesItsHostResourcesIdentity`: p3hello's `output-stream.write` self and its `own<error>` carry their identities, and the self's canon type carries the host output-stream's RT.
  - `TestAResolvedHandleKeepsItsIdentityInAnotherScope`.
  - `TestHostMethodWhoseSelfIsAnotherResourceIsRefused` (`host-resource-self-mismatch.wasm`) and `TestHostImportOverAResourceTheHostDoesNotImplementIsRefused` (`host-resource-unknown.wasm`).
  - `TestWriteViaStreamPayloadMustAgreeWithTheHosts`: `write-via-stream-error-code.wasm` instantiates, and `write-via-stream-other-error-code.wasm`, which differs in one label, is refused.
  - `TestCloseDestroysTheOwnsTheGuestStillHolds`, `TestCloseReachesTheOwnsANestedInstanceHolds` (`host-resource-nested.wasm`) and `TestCloseLeavesAnOwnLentToACallInProgress`.
- **Neuters, run**, each named at its site:
  - The eq arm returning the placeholder: the payload's error reads as a reference.
  - The type-export resolution skipped: the record's field resolves in the component's scope, to the record itself.
  - The alias-export arm unresolved: all three timestamps are unresolved, and p3hello is refused at instantiate, by the stub host naming `[method]descriptor.stat` and by the real host's resource check naming `get-stderr`.
  - The `seen` delete removed: the second and third timestamps are unresolved.
  - The type-import arm answering nothing: the world-level self has no identity, and its result stays a reference.
  - The future arm removed: the payload stays a reference.
  - The `valHasUnresolvedAlias` arms removed: all three specimens read clean.
  - The identity fill removed: an empty identity. The keep rule removed: the second scope's resource. The bridge's resolver ignored: RT 0, the local index, rather than the host output-stream's.
  - The method rule, the identity rule and the payload check each returning nil: each refused guest instantiates.
  - `walker.close` without the teardown: the destructor count is 1 of 3. The teardown not recursing into nested instances: 0 for the nested own. The lend test removed: the destructor runs inside the call.
- **The fixtures** are `.wat` assembled with wasm-tools 1.258.0, and every one validates. `host-resource-self-mismatch` reaches its mismatched self through an eq bound, because wasm-tools refuses the direct spelling (*"function does not match expected resource name"*).
- **Tests changed:**
  - `TestBindingAdmitsImplementedFilesystemErrorCodeOnRealGuest` walks with the production resource types, where it had an empty source. Its implemented `filesystem-error-code` takes `borrow<error>`, which must now name a host resource, and an empty source implements none, which no real host is.
  - `typeresolve_test.go`'s alias row keeps its assertion. Its comment now says the alias it names has no record behind it, since an alias export of an imported instance does resolve.
  - `TestTheBridgeAndTheUnmodeledPredicateAgree`'s declared `VRef` divergence stands. A resolved signature still holds a `VRef` only for a reference to a resource, func or instance type, and the bridge still cannot follow one.
- **What a guest sees.** A guest whose host-served import declares a handle the host does not implement, a method whose self is another resource, or a `write-via-stream` payload other than the host's, is refused at instantiate with `ErrLinkRefused`. Before, it was bound and trapped at call or read the wrong bytes. An own a guest still holds when its instance closes has its destructor run once. p3hello, p3echo and p3async-hello run as before.
- **No contract text changes, and no public API.**

## Rollback

Each half reverts on its own. Restoring the placeholder in `exportedTypeDef` and the unresolved alias arm in `typeOrdinal` restores the decode, and with it #1246's substitution. The payload check then has nothing to compare, so it must be reverted with them. Removing the `checkHostResources` call restores binding without the cross-check. Removing the `closeAllHostResources` call restores ADR 0117's teardown. Each of these is a neuter above, run.
