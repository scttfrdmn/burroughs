# 0115 — The component host keys an import by its name and its semver compatibility class, so WASI 0.2 and 0.3 are never merged

Date: 2026-11-11 · Status: **accepted**, decided in the slice. It is an engine mechanism inside `internal/component`: no contract text, no public API, and no stamped ADR is reversed, so by the #647 review it is decided here and reported as a decision taken. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1229](https://github.com/scttfrdmn/burroughs/issues/1229).

## Context

**The defect.** `stripVersion`, in `internal/component/walk.go` until this ADR, cut the `@x.y.z` from every lowered import's identity before the host tables were consulted. That was written for a host that served one WASI track: a guest at `@0.2.6` (p3echo) and one at `@0.2.3` (p3hello) name the same interface, and the host serves the interface.

It stopped being true when the 0.3 guests arrived. **WASI 0.2 and WASI 0.3 share names and differ in signature.** `wasi:filesystem/types::[method]descriptor.stat` is synchronous in 0.2 and async in 0.3; `wasi:cli/stdout` has `get-stdout` in 0.2 and `write-via-stream` in 0.3. Stripping the version lets a 0.3 guest bind a 0.2 implementation by name, and the other way round. Today's 0.3 fixtures (p3async-hello, p3async-cancel) run partly *because* of the merge: they reach `exit`, `get-environment` and `get-arguments` from the table written for 0.2. Those three happen to have the same signature in both tracks, which is a fact about those three and not a property the lookup checked.

Every upcoming WASI 0.3 row (codec kinds, resources, clocks, sockets) needs the tracks kept apart, and ADR 0114 pins *"WASI 0.3.0 semantics, admitting 0.3.x by semver"*. A lookup that cannot tell 0.2 from 0.3 cannot admit 0.3.x without admitting 0.2.

## Options

1. **Key by the full version.** Exact match on `@x.y.z`.
   - *Costs:* p3echo (`@0.2.6`) and p3hello (`@0.2.3`) need separate entries for one implementation, and every patch release of WASI is a host change. It is stricter than the Component Model itself.
2. **Key by name plus the semver compatibility class**, the Component Model's own rule for which versions an import may be satisfied by.
   - *Keeps:* one entry serves every patch of a track, and a 0.2 implementation can never answer a 0.3 import.
   - *Costs:* each production entry must say which classes it serves, and an entry that serves both tracks must say so explicitly.
3. **Keep stripping, and special-case the colliding names.**
   - *Costs:* it guards only the collisions someone has noticed, and the next 0.3 row is where the unnoticed one lands.

## Choice

**Option 2.**

- **The class rule.** For a `0.x` version the class is `major.minor` (`0.2`, `0.3`), since in 0.x the minor version breaks compatibility; for `0.0.x` it is the whole version; for `>=1` it is the major version. An unversioned key has class `none`. A pre-release keeps its class (`0.3.0-rc-2026-02-09` is `0.3`). The version is parsed strictly as SemVer 2.0.0, and an unparseable one is its own class, which no entry serves, wildcards included.
- **Entries declare their classes in their keys.** A host table key is `interface@class::export` (`wasi:cli/stdout@0.2::get-stdout`), the class standing where an import's version does, written through one helper, `serve(m, classes, name, impl)`, which registers one implementation for each class it is given. So a track can later hold its own implementation of a name the other track also serves, which a table keyed by name alone could not. The production entries:
  - `0.2` only: the stdio getters, every `wasi:io/streams` and `wasi:io/error` entry, the four `[resource-drop]` keys, and `wasi:filesystem/preopens::get-directories` (whose 0.3 descriptor differs and is unverified).
  - `0.2` and `0.3`: `wasi:cli/exit::exit`, `wasi:cli/environment::get-arguments` and `get-environment`, whose signatures are the same in both.
  - `0.3` only: `wasi:cli/stdout::write-via-stream`.
- **An unversioned key is a wildcard** and serves every parseable class, `none` included. That is every test-only `syncImpls`/`asyncImpls` registration and every hand-built test table, which carry no version to disagree with. An unversioned *import* (class `none`) is served only by a wildcard, so it does not pick one of a name's tracks.
- **The lookup** (`internal/component/hosttrack.go:lookupHost`) has three outcomes:
  - an exact `(name, class)` hit or a wildcard hit binds as before;
  - a name the host serves only under another class binds a refusing extern which, when called, returns `ErrLinkRefused` naming both, e.g. *"import wasi:cli/stdout@0.3.0::get-stdout has only a WASI 0.2 implementation; it is not served for 0.3"*;
  - a plain miss is unchanged.
- **The refusal is at call, not at instantiate**, for the plain miss's reason: a guest can import a function it never calls, and refusing the import would refuse the guest.
- **One lookup for all four sites**, including the instantiate-time unmodeled-signature check, so that check sees the binding a call would get. The cross-track classes are gathered across the three tables, so `write-via-stream@0.2.6`, which misses the 0.3-only stream-consumer table and then plainly misses the sync table, is refused as cross-track and not as *"not provided"*.
- The map types and `walkComponent`'s signature are unchanged, so test seams that build a table by hand keep working, and their unversioned keys are wildcards.

## Consequences

- **Witness:** the class rule as a table test over `splitVersion`; the cross-track refusal in both directions (`get-stdout@0.3.0` and `output-stream.write@0.3.0` against 0.2-only entries, `write-via-stream@0.2.6` against a 0.3-only one), called, with the message naming both tracks; and `exit@0.3.2` and `get-arguments@0.3.0` still served. A control asserts that every production key carries a WASI class, so an entry written without `serve` fails rather than becoming a silent wildcard.
- **Neuter, run:** the lookup ignoring the class, as `stripVersion` did. The refusal tests must fail and the fixture tests must stay green. *Not* `splitVersion` reporting `none` for every key: since a class-scoped entry does not serve `none`, that neuter refuses every WASI import and fails the fixtures with the refusal tests, which shows the class is load-bearing but does not reproduce the old behaviour.
- **The 0.3 guests keep running**, on entries that now say they serve 0.3 rather than on a merge.
- **A WASI 0.3 row adds its entry under `0.3`**, beside a 0.2 one of the same name if there is one, and the two never meet.
- **No contract text changes.** The public API is unchanged; an embedder can observe only a 0.3 guest calling a 0.2-only import, which used to run against the wrong-track implementation and now refuses by name.

## Rollback

Making `lookupHost` ignore the class restores the merge exactly. The neuter is that rollback, run.
