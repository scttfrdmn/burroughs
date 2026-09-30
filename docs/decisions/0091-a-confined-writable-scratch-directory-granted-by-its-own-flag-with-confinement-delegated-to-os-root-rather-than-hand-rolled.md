# 0091 — A confined writable scratch directory, granted by its own flag, with confinement delegated to `os.Root` rather than hand-rolled

Date: 2026-09-26 · Status: **accepted** (ratified on the #832 review with five conditions; **amendment 1 below revises the scope from eight functions to four** and is part of the ratification) · [#831](https://github.com/scttfrdmn/burroughs/issues/831) (the recon) · Extends [ADR 0083](0083-a-read-only-wasi-preview1-filesystem-a-per-run-fd-table-capability-preopens-and-path-open-scoped-to-reading.md)
Ratio-Class: ordered https://github.com/scttfrdmn/burroughs/issues/831

**`Status` was held open until an approval existed to cite, and now one does.** The #829 review ordered *"a recon plus an ADR 0091 recommendation"* and said *"Recommendation only. No implementation until it's ratified."* Ratification came from chat-Claude on the #832 review, with five conditions, on the finding in §4 below that confinement **can** be enforced without granting more than the named directory — the condition the review set for keeping this a technical ADR rather than escalating it to Scott. **Read the body with amendment 1**: it withdraws five of the eight functions §Decision names and adds one the body lists as staying refused, because the body's demand set came from a synthetic guest rather than from the consumers.

Recorded by the actor the order reached, so no independent provenance; commits resting on it stay `Ratio-Class: carried` unless they carry their own citation.

## Context — the write slice moved from evidence to blocker

ADR 0083 made the preview-1 filesystem read-only and deferred *"the write slice, `fd_seek`, and directory enumeration"* until a guest needed them. Two of the three have since landed ([#816](https://github.com/scttfrdmn/burroughs/issues/816)). The write slice's trigger has now fired, and it fired as a measurement rather than as a request.

The unaimed-program sweep's batch 2 set out to run four Go standard-library packages under the Phase 4 fork and look for single-M faces. Three ran. **`os` produced 227 verdicts and every one was a FAIL**, with one uniform cause:

```
TempDir: mkdir /tmp/TestX...: Not implemented on wasip1
```

That is `path_create_directory`'s ADR 0083 refusal, working exactly as designed. But `t.TempDir()` runs in **test setup**, so the package fails before any test body executes. The consequence is not that `os` scores badly — it is that **the one package in the batch that exercises the fd table under load cannot run at all**, and the sweep's reach statement has had to say so: *evidence about scheduling and exit, not about I/O.*

So this is no longer a deferral waiting on a consumer. The consumer arrived, and it is the instrument.

## The recon

### 1. The demand set is measured on an engine that completes the cycle, not inferred

A guest performing exactly what a Go test's `t.TempDir()` plus a write–read–cleanup cycle performs was run on **both** engines. Run on Burroughs first, and that run is **shadowed and therefore not the answer**:

| step | Burroughs |
|---|---|
| `MkdirTemp` | `ENOSYS` — *Not implemented on wasip1* |
| `WriteFile` | **`ENOTCAPABLE`** — *Capabilities insufficient* |
| `ReadFile` | `ENOENT` (the file was never created) |
| `Write` / `Seek` / `Sync` / `Close` | **never reached** — the open failed |
| `Truncate` | `ENOENT` — **never reached** |
| `Rename`, `Chtimes`, `Remove` | `ENOSYS` |
| `ReadDir` | ok |

Five refusals fired. **That count is a floor, not the demand set**, because an early failure hides every call downstream of it — `fd_write`, `fd_seek`, `fd_sync` and `fd_filestat_set_size` are absent from it only because nothing got far enough to call them. Reporting those five as "what is needed" would have under-specified the slice by half.

So the same guest bytes were run on **wasmtime 49.0.1**, where every step succeeds, with `WASMTIME_LOG=wasmtime_wasi=trace`. Every step `ok`; 168 trace lines; **19 distinct preview-1 functions**. That is the unshadowed demand set.

### 2. The demand set against Burroughs' current host

| function | Burroughs today | needed |
|---|---|---|
| `fd_close`, `fd_fdstat_get`, `fd_fdstat_set_flags`, `fd_filestat_get`, `fd_prestat_dir_name`, `fd_prestat_get`, `fd_read`, `fd_readdir`, `fd_write`, `path_filestat_get`, `path_open` | **implemented** (11) | already there |
| `fd_filestat_set_size`, `fd_seek`, `fd_sync`, `path_create_directory`, `path_filestat_set_times`, `path_remove_directory`, `path_rename`, `path_unlink_file` | **refuses `ENOSYS`** (8) | must change |

**Eight functions change from refusing to implementing.** None is absent, so the slice adds no new import names and ADR 0080's *supplied whole* property is untouched.

### 3. `path_open` is a policy change, not an implementation change — and it is the only one

`path_open` is implemented and **denies write intent by policy**, on the open mode rather than on `fs_rights_base` (ADR 0083's 2026-09-09 append, which corrected requirement 1 after measuring that Go's `os.Open` requests write rights speculatively even for a read):

```go
if oflags := uint16(u32(args, 4)); oflags&(oflagCreat|oflagExcl|oflagTrunc) != 0 {
    return ret(errNotcapable), nil
}
```

That `ENOTCAPABLE` is what `WriteFile` hit above, and it is **the correct errno and the correct place**. The write slice's change here is to make the branch conditional on the preopen's own writability rather than unconditional — so a read-only grant keeps returning `ENOTCAPABLE` verbatim, and only a scratch grant proceeds. **`ENOSYS` versus `ENOTCAPABLE` stays load-bearing**: after this slice, `ENOTCAPABLE` means *this grant is read-only* and `ENOSYS` still means *this engine does not implement this*, which is what keeps the remaining 16 refusals honest rather than dressed as policy.

### 4. Confinement — the review's escalation condition, and it is NOT met, so this stays technical

The review said: *"If the recon finds the confinement can't be enforced without granting more than the named directory, it goes to Scott."*

**It can be enforced, and not by hand.** The obstacle first, because it is the reason a hand-rolled version would be wrong: ADR 0083's `resolveUnder` enforces containment on the **resolved** path via `filepath.EvalSymlinks`, and its own comment states the limit — *"`EvalSymlinks` requires the target to exist, which is exactly right for a read."* **A write creates a path that does not exist yet**, so `resolveUnder` cannot resolve it: it returns `ENOENT` for the very file being created. Extending it would mean resolving the parent, checking containment, then joining the final component unresolved — and that reintroduces a TOCTOU window between the check and the `openat`, which is the class of defect ADR 0083 deliberately avoided by checking the resolved path.

**Go's standard library already holds the answer.** `os.Root` (`os.OpenRoot`) is a directory handle whose every operation is confined beneath it, implemented with `openat`-family calls, symlink-aware, and documented safe for concurrent use. Pure Go, no cgo, no new dependency.

Measured rather than read off the doc comment — every escape shape a preview-1 guest can express, and the in-grant operations alongside so the negative arm is not passing by collapse:

| escape attempt | result |
|---|---|
| `Create ../pwned` | refused — *openat ../pwned: path escapes from parent* |
| `OpenFile ../../pwned O_CREATE` | refused |
| `Mkdir ../pwneddir` | refused |
| `ReadFile esc/secret.txt` (symlink out of the grant) | refused |
| `Create esc/pwned` (create **through** a symlink) | refused |
| `ReadFile abs/passwd` (absolute symlink to `/etc`) | refused |
| `Rename mv.txt ../escaped.txt` | refused |
| `Remove ../outside/secret.txt` | refused |
| `Chtimes ../outside/secret.txt` | refused |
| `Symlink /etc` then read through it | refused |

All ten refused; all eight in-grant operations (`Mkdir`, `Create`, `WriteFile`, `Rename`, `Chtimes`, `Truncate`+`Sync`, `Remove`, `RemoveAll`) permitted; and a direct check of the filesystem afterwards found **nothing created outside the grant and the outside file still present**. The positive arm working is what makes the ten refusals a confinement result rather than a broken handle.

**Residual risks, named rather than omitted**, all from `os.Root`'s own documentation:

- On Unix, `Root.Chtimes` is vulnerable to a symlink race — if the target changes from a regular file to a symlink mid-operation, the link may be touched instead of its target. This reaches `path_filestat_set_times`, one of the eight. It is a *timestamp* on a path already inside the grant; recorded as accepted, not fixed.
- `os.Root` does not prohibit traversal of filesystem boundaries, bind mounts, `/proc`, or Unix device files. A grant of a directory containing such a thing exposes it — which is a property of what the operator names, exactly as `--dir` already is.
- `GOOS=js` and `plan9` weaken or lose the guarantee. Burroughs runs its host on unix; `internal/interp/reserve_other.go`'s precedent says a non-unix port states its own conformance, so this belongs in that ledger rather than here.

## Options

**A. Make the existing `--dir` grants writable.** Rejected. It silently converts every existing read-only grant into a write grant, which is the opposite of ADR 0083's model and would change what an existing invocation permits with no change to the invocation.

**B. A second flag granting a writable directory — recommended.** `--scratch HOST[:/GUEST]`, repeatable, mirroring `--dir`'s grammar including #828's absolute-guest-path refusal. A preopen carries a `writable bool`; nothing is writable unless granted through this flag; `--dir` is untouched and stays read-only. Writes are confined by an `*os.Root` opened once per scratch preopen at `initFDs` time and closed with the instance.

**C. A `--dir` modifier, e.g. `--dir HOST:/GUEST:rw`.** Rejected on grammar: `--dir`'s separator has already cost two measurements and one refusal slice (#828), and a third colon-delimited field on the same flag is the same trap with more surface. A distinct flag name is self-describing in a way a third field is not.

**D. An API-only capability with no CLI flag.** Rejected by the same argument that earned `--dir` its flag: a capability the CLI cannot grant is a capability the CLI's own tests cannot exercise, and the sweep that needs this runs through the library *and* the CLI.

## Decision (recommended, pending ratification)

1. **`Preopen` gains a writability bit**, and the library default is unwritable. Read-only stays the default at every layer with no flag set.
2. **`--scratch HOST[:/GUEST]`** grants a writable preopen. Never implied by `--dir`, never on by default, and it inherits #828's guest-path refusals rather than restating them.
3. **Confinement is delegated to `os.Root`**, opened per scratch preopen. No new path-resolution code: the eight operations map onto `Root.Mkdir`, `Root.Remove` (twice — file and directory), `Root.Rename`, `Root.Chtimes`, `Root.OpenFile`, and on the returned `*os.File`, `Seek`, `Sync` and `Truncate`. `resolveUnder` keeps the read path exactly as ADR 0083 left it.
4. **`path_open`'s write refusal becomes conditional on the grant**, and returns `ENOTCAPABLE` unchanged for a read-only one.
5. **The 16 remaining refusals stay refusals**: `fd_advise`, `fd_allocate`, `fd_datasync`, `fd_fdstat_set_rights`, `fd_filestat_set_times`, `fd_pwrite`, `fd_renumber`, `fd_tell`, `path_link`, `path_readlink`, `path_symlink`, `proc_raise`, `sock_accept`, `sock_recv`, `sock_send`, `sock_shutdown`. The recon's guest reached **none** of them, so each keeps `ENOSYS` and keeps its deferral visible. Note that `fd_filestat_set_times` stays refused while `path_filestat_set_times` becomes implemented — the fd-level form was not in the demand set, and widening on symmetry rather than on a consumer is what ADR 0083's deferral rule exists to prevent.

## The witness

**The confinement probe in §4 becomes the committed witness**, not a paraphrase of it. It already has the shape the corpus asks for and it has been watched discriminate: ten escape attempts that must be refused, eight in-grant operations that must be permitted, and a post-hoc filesystem check for anything that landed outside. Its positive arm is what stops the negative arm from passing by collapse — a broken `*os.Root` would refuse all eighteen and, without the in-grant arm, read as perfect confinement.

Three further witnesses the slice owes:

- **A read-only grant stays read-only.** `--dir` and `--scratch` over the same host directory in one run, with every write refused through the first and permitted through the second. This is the arm that fails if the writability bit is ever defaulted on, and it is the reason the bit is on the preopen rather than on the host.
- **The refusal surface is asserted as a set, not as examples.** `TestSuppliedSurfaceMatchesTheSpecification` already derives the supplied names from the committed witx; the companion assertion is that exactly the 16 names above still refuse and exactly the 8 no longer do, derived from the imports table rather than listed by hand — so a ninth function quietly implemented fails the witness.
- **`os` runs.** The end-to-end verdict is the sweep's own: `encoding/json` finished 5/5 with 6 verdicts and one outcome set, and `os` should produce a comparable row instead of 227 identical setup failures. That is the artifact this slice is *for*, and it is what says the capability was the blocker rather than a guess about the blocker.

## Consequences

- **The sweep's reach statement changes**, and that is the point: batch 3 can include packages that exercise the fd table, which batch 2 could not. The review's ordering — batch 3 waits on this ruling — follows from that and not from convenience.
- **The engine's most conservative default is preserved.** A run with no `--scratch` has exactly today's filesystem behaviour, byte for byte, including its errnos. That is the property that makes this a technical ADR: it grants nothing that was not asked for by name.
- **ADR 0083's deferral list is discharged in full** by this slice plus #816, so the sentence *"the write slice, `fd_seek`, and directory enumeration"* stops being a live deferral. It should be amended there rather than left reading as outstanding.
- **A `--scratch` grant is a real capability and should read like one.** It is the first Burroughs flag that lets a guest change the host's filesystem, and the help text carries that plainly rather than by implication.

---

# Amendment 1 — 2026-09-30: the scope is the measured set, and the recon measured the wrong guest

Status: **accepted** · ratified by chat-Claude on the #832 review, with Scott's non-objection on #830 recorded separately · [#831](https://github.com/scttfrdmn/burroughs/issues/831)

**The ratification stands; this amendment revises what it ratified.** The body above recommends **eight**
functions moving from refusing to implemented. That list is wrong, and the reason it is wrong is a
principle this project already holds.

## Why the list changed

The eight came from a **synthetic recon guest** — a program I wrote to perform "what a Go test's
`t.TempDir()` plus a write–read–cleanup cycle performs". It performed what I thought that was. Five of
its steps (`Seek`, `Sync`, `Truncate`, `Rename`, `Chtimes`) were there because I put them there, and
none is reached by any consumer the ADR names.

Measuring from the **consumers** instead — §Decision's own acceptance criteria — gives three different
sets, and the ADR's eight matches none of them:

| consumer | wasmtime verdict | needs changing |
|---|---|---|
| `t.TempDir()` + write + read | passes | `path_create_directory`, `path_unlink_file`, `path_remove_directory` |
| `os.TestWriteAtConcurrent` | **passes** | `path_unlink_file`, **`fd_pwrite`** |
| the whole `os` package | **0 PASS / 17 FAIL** | ~12, and see below |
| my synthetic recon guest | passes | the eight above |

**This is *derive the domain from the space, never from the registry* applied to a capability instead of
a witness** (Scott's framing, #832 review). A synthetic guest is a registry: it contains what its author
enumerated. The consumer is the space. The same error in a witness would have been caught by the
surface control that derives its domain from the witx; there was no such control for a *capability*,
which is why the recon's own doc comment could correctly report five refusals as a floor and still hand
back a list nobody asked for.

## The measured scope

**Four functions change from refusing to implemented:**

| function | asked for by |
|---|---|
| `path_create_directory` | `t.TempDir()` |
| `path_unlink_file` | both acceptance consumers |
| `path_remove_directory` | `t.TempDir()`'s registered cleanup |
| `fd_pwrite` | `os.TestWriteAtConcurrent` |

**And `path_open`'s write-mode policy change is listed here explicitly**, because a reader counting what
moved should see it. It is not a new function — §3 of the body describes it — but it is the change that
makes every write above reachable, so omitting it from the count would understate the slice by its most
load-bearing edit.

**Five of the body's eight are withdrawn**: `fd_seek`, `fd_sync`, `fd_filestat_set_size`, `path_rename`,
`path_filestat_set_times`. Each stays refused with `ENOSYS`, on ADR 0083's own on-demand rule. The
remaining-refusal count goes from the body's 16 to **21**.

## `fd_pwrite` moves off the stays-refused list, scoped to its ORIGIN rather than to a path

The body lists `fd_pwrite` among the 16 that stay refused, on the ground that the recon's guest never
reached it. The measured consumer does: `os.TestWriteAtConcurrent` is the fd-table-under-load test that
batch 2 could not run at all, which is the artifact this slice exists for.

**It needs no path confinement, and that is the interesting part.** `fd_pwrite` acts on an fd the guest
already holds, so there is no path to resolve and no root to confine against — `os.Root` is irrelevant to
it. The only question is **where that fd came from**:

- an fd opened for writing under a `--scratch` preopen — permitted;
- **any other fd, including one from a read-only `--dir` preopen — refused with `ENOTCAPABLE`.**

`ENOTCAPABLE` and not `ENOSYS`, because after this slice the function *is* implemented and the refusal is
the capability model speaking, which is the distinction ADR 0083 set and §3 of the body preserves. **That
refusal gets its own witness arm.** Without one, the permit path working is the only evidence, and a
`fd_pwrite` that ignored its fd's origin would pass that.

This is the first capability in the engine carried by an **fd's provenance** rather than by a path, so
the fd table gains the bit that records it.

## Condition 2 is settled: `path_filestat_set_times` stays refused entirely

The review's condition 2 permitted its **no-follow** form while refusing the symlink-following one,
citing `Root.Chtimes`'s documented Unix race. Three facts close it:

1. **`os.Root` has no `Lchtimes`.** It has `Lchown` and `Lstat`, so the omission is deliberate upstream
   rather than an oversight to route around. The form the condition permits is not implementable through
   the mechanism this ADR chose.
2. **No acceptance consumer asks for it.** It appears in the synthetic guest's set and in the
   whole-`os` set, and the latter is dropped below.
3. A `Root.Lstat`-then-`Chtimes` sequence would be a check followed by a use, i.e. the same race with an
   extra step — and worse, one this document would have described as mitigated.

**What retires this:** a consumer that needs it, plus the `Root.OpenFile(..., O_NOFOLLOW)` +
`syscall.Futimes(fd)` route, which is a genuine race-free no-follow implementation using only the
standard library. It is recorded here and not taken because `syscall.Futimes` is **platform-conditional**
and `internal/wasi` has no build-tagged paths today; adding the first one is its own decision, not a
detail of this slice.

## Condition 1 is discharged by scope, and leaves a tripwire

The review's condition 1 requires a cross-grant `path_rename`/`path_link` to be refused by name, with its
own witness arm. **With the measured scope, neither function is implemented** — `path_rename` is
withdrawn above and `path_link` was never in the set — so no multi-preopen operation exists and the arm
would have nothing to exercise. Building it anyway would be a witness whose subject is absent, which this
corpus already prices: *a control that tests a helper nothing calls*.

Their refusal is covered by condition 3's default-unchanged errno witness, which enumerates the whole
refusal surface.

**What retires this:** implementing `path_rename` or `path_link` requires the cross-grant refusal **and
its witness arm in the same PR**. A single `*os.Root` cannot span two grants, so the behaviour would
otherwise be whatever the implementation happened to do — which is the thing condition 1 exists to
prevent, and the reason it is recorded as owed rather than as satisfied.

## Condition 5 is restated as a two-part acceptance

**Part A.** A guest calling `t.TempDir()` then writing and reading a file succeeds under
`--scratch HOST:/tmp`. No environment variable is needed: Go's `os.TempDir` on `wasip1` falls back to
`/tmp`, so this does not wait on [#830](https://github.com/scttfrdmn/burroughs/issues/830). Measured on
wasmtime as the reference reading.

**Part B.** `os.TestWriteAtConcurrent` goes from **FAIL to PASS** in batch 3's frozen-names row. It is
the fd-table-under-load test, it passes on wasmtime with nothing but a writable `/tmp`, and it is the
single row expected to move.

**The whole-`os` row is dropped.** With its source mapped read-only and a writable scratch, `os` scores
**0 PASS / 17 FAIL on wasmtime**, and the causes are environmental rather than engine-level: `/dev/null`,
the GOROOT layout, and its own source tree at paths no sandbox grants. **A row that fails on both engines
adjudicates nothing** — *identical boards are the finding*, and here the finding would be about the host
filesystem rather than about Burroughs. Batch 3 measures the frozen names.

## Conditions 3 and 4 stand unchanged

The default-unchanged errno witness (condition 3) and the committed confinement tests (condition 4) are
unaffected by the narrowing. Condition 3's domain **grows**: the refusal surface it enumerates is now 21
names rather than 16, and it derives that domain from the witx rather than from a list, so the growth
costs it nothing.

## Two witnesses survived their injections, and what that says about escape-only confinement tests

Recorded because both gaps are the kind a reader of the test would not see.

**1. An escape-only confinement test measures *confined to a root*, not *confined to THE root*.** §4's probe
and its committed form both refused all ten escape shapes and permitted all eight in-grant operations.
Injecting `os.OpenRoot(filepath.Dir(root))` — confining the grant to the **parent** of the granted directory
— left every one of those eighteen arms green. The escapes still escape the parent, so they are still
refused; the in-grant operations still succeed, in the wrong directory. **Only the host filesystem can tell
the two apart**, so the witness now writes a file through the grant and asserts it appears inside the granted
directory and *not* one level above it.

**2. Nothing connected the flag name to the capability.** Injecting `Writable: true` into `preopenFlag`'s
accessor — every `--dir` silently becoming a write grant — left the whole tree green.
`TestRunGrantsAndDeniesFilesystemAccess` exercises reads only, and the library-level two-grant witness builds
`Preopen` values directly rather than through the flags. The engine was witnessed and **the CLI's mapping onto
it was not**, which is the gap a reader checking "is `--dir` read-only?" would have believed was covered.
`TestDirIsReadOnlyAndOnlyScratchGrantsWrites` closes it by running one guest under each flag.

The general shape, for the next capability: **a refusal-only witness cannot distinguish a capability aimed
at the wrong target from one aimed correctly**, because both refuse the same things. Assert where the
permitted operation *lands*, not only what the refused one returns.

## Amendment 2 — 2026-09-30: a directory fd is a confinement boundary, and the implementation opened one before anything tested it

Status: **accepted** · ordered on the #833 review

Making `t.TempDir()`'s cleanup work required `path_*` to resolve against **any** directory fd rather than only
a preopen's, because Go's `RemoveAll` descends by opening each directory and operating through the resulting
fd. **That widened the confinement surface for both kinds of grant, and every arm in §4's probe starts from
the preopen**, so none of them reached it. The shape that matters starts one level down: open a subdirectory
inside the grant, then climb or follow a symlink relative to *that* fd.

### The threat model, which decides what layer the arms sit at

**No stock Go guest can express this attack.** Go's `wasip1` runtime cleans a path textually before it reaches
`path_open` (`appendCleanPath`), and its `os.Root` refuses escaping and absolute symlinks *in the guest* before
any host call happens. A module hand-written in wasm can send whatever path bytes it likes with whatever dirfd
it holds, and that is what is being defended against. So the arms drive the resolution primitives directly —
`resolveUnder` for reads, the nested `os.Root` for writes — against a subdirectory fd built through the same
`attachDirRoot` the engine uses. **What they do not cover is the wasm-level marshalling**, whose happy path is
`TestAScratchGrantIsRequiredForEveryWrite` and part A's cleanup.

### What holds, measured for both grant kinds

Six read escapes and six write escapes, refused in both: climbing out of the grant, climbing past the base,
an absolute path, a symlink inside the subdirectory pointing out, an absolute symlink, a symlink aimed straight
at a file outside, plus the write forms (create, mkdir, create-through-symlink, write-through-symlink, remove
outside, rename out). A legitimate read and a legitimate write are permitted through the same fd, and the
write is asserted to **land beneath the subdirectory** — the landing check §4 learned it needed.

`--scratch` reaches this by a **nested** `os.Root` derived from the grant's own root, so a child fd cannot
reach where its parent could not. `--dir` reaches it through `resolveUnder` against the subdirectory's resolved
host path, with containment on the resolved result — and a read-only grant's subdirectory fd is asserted to
have **no write capability at all**, because "`--dir` is read-only" has to hold at every level and not only at
the preopen.

**One observation recorded rather than required:** resolution through a subdirectory fd is
*subdirectory-scoped*, so a sibling inside the grant but outside the subdirectory is refused. That is
**stricter** than the grant, and safe in the direction that matters — anything contained by a directory inside
the grant is contained by the grant. A guest wanting the sibling addresses it through the preopen. The test
logs which reading holds rather than asserting one, so a future change to grant-scoped resolution reports
itself instead of failing.

### Watched die four ways

1. the subdirectory fd resolving against `/` — four read escapes permitted, including `/etc/passwd`;
2. the nested root taken from the grant's **parent** — a write escape permitted and the landing check red;
3. containment checked **without** resolving symlinks (`Clean` instead of `EvalSymlinks`) — the three symlink
   shapes permitted, which is the arm a textual `..` filter would have left uncovered;
4. a read-only grant's subdirectory fd handed a write root — the `--dir` capability arm red.
