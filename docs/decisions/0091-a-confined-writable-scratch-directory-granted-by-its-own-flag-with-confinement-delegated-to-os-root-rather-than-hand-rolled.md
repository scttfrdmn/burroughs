# 0091 — A confined writable scratch directory, granted by its own flag, with confinement delegated to `os.Root` rather than hand-rolled

Date: 2026-09-26 · Status: **proposed — recommendation only, no implementation** · [#831](https://github.com/scttfrdmn/burroughs/issues/831) (the recon) · Extends [ADR 0083](0083-a-read-only-wasi-preview1-filesystem-a-per-run-fd-table-capability-preopens-and-path-open-scoped-to-reading.md)
Ratio-Class: ordered https://github.com/scttfrdmn/burroughs/issues/831

**`Status` is held open deliberately.** The #829 review ordered *"a recon plus an ADR 0091 recommendation"* and said *"Recommendation only. No implementation until it's ratified."* A `Status: accepted` here would cite an approval that does not exist, which this project treats as worse than a wrong option. The ruling that would accept it is chat-Claude's, on the finding in §4 below that confinement **can** be enforced without granting more than the named directory — which is the condition the review set for keeping this a technical ADR rather than escalating it to Scott.

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
