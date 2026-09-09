# 0083 — A read-only WASI preview-1 filesystem: a per-run fd table, capability preopens, and `path_open` scoped to reading

Date: 2026-09-08 · Status: **proposed** · [#690](https://github.com/scttfrdmn/burroughs/issues/690) · Scope and the four requirements: Scott
Ratio-Class: carried

## Context

The third WASI guest reads a filename from `argv` and opens the file (`os.ReadFile`). Measured, it
imports **19** `wasi_snapshot_preview1` functions — the stdin+sleep guest's 16 plus **`path_open`**,
**`path_filestat_get`**, and **`fd_filestat_get`**. That is a subsystem, not three stubs: opening a
file needs a per-run **fd table** the runtime does not have, and reaching the filesystem at all needs
the **preopen** stubs (`fd_prestat_get`/`fd_prestat_dir_name`) made real.

**Scoped read-only (Scott), to exactly what `os.ReadFile` drives:** `path_open` for reading, `fd_read`
from a file fd, `fd_close`, `fd_filestat_get` + `path_filestat_get`, and the two prestat calls made
real. Writes, `fd_seek`, and directory enumeration are deferred — no guest here exercises them, and
guest-driven has been right every slice; unconsumed design in this tree has repeatedly been built
wrong.

## Decision

**A per-run fd table, capability-based preopens, and a read-only `path_open`, built on the preview-1
host module (ADR 0080).**

1. **A per-run fd table** on `host`, mapping a guest fd (`u32`) to an entry: stdio (0/1/2), a
   **preopen directory** (a host dir path), or an **open file** (a host `*os.File`). File fds are
   allocated above the preopens.

   **Read-only is a property of the *surface*, not the *structure* (requirement 1).** The entry can
   represent an open host file whatever mode it was opened in; what is read-only is what `path_open`
   *accepts* — read `oflags` and rights only. A later write slice widens `path_open`'s accepted rights
   and opens `O_RDWR`; it does not reshape the table. So the table carries an `*os.File` and a
   read-only marker, not a read-only-only representation.

2. **Preopens are explicit and empty by default — a security commitment, and the ADR says why
   (requirement 2).** `WASIP1Config` gains `Preopens []Preopen{Guest, Host string}`. Each becomes a
   dir fd (3, 4, …); `fd_prestat_get` reports its name length and `fd_prestat_dir_name` writes the
   name. **With no preopen, the guest can open nothing** — `path_open` has no dir fd to resolve
   against, and `fd_prestat_get` reports `EBADF` past the last preopen, ending the runtime's discovery
   loop. There is **no default preopen of `.`**: WASI's model is capability-based, and a default `.`
   would be the runtime granting a program ambient authority over its working directory that nobody
   asked for — the guest reaching a file it was never handed. A program reaches only what a preopen
   granted, and **path resolution may not escape the preopen root**, because a capability that leaks
   is not a capability.

   **The check is on the *resolved* path against the preopen root, not the textual path (Scott's design
   commitment, and this is why it is in the ADR rather than the PR).** A textual `..` filter is the
   classic wrong implementation: it passes a `..` test while a symlink pointing outside, an absolute
   path, or a platform-specific spelling walks straight through. So the refusal (`ENOTCAPABLE`) is
   decided by resolving the request to a real host path — symlinks followed — and asserting that path
   lies within the preopen's resolved root; the textual form is never the thing checked. An
   implementation that filters the textual path satisfies the letter of "no escape" and leaves the
   hole open.

3. **`path_open`, read-only.** Resolve `path` against the dir fd's host path, refuse a path escaping
   the preopen, open the host file `O_RDONLY`, allocate a new fd, and write it to the `opened_fd`
   pointer. **A request for write access is refused, not silently downgraded:** write bits in
   `fs_rights_base`, or `oflags` with create/truncate, return `ENOTCAPABLE` — the read-only surface is
   visible to the guest as a missing capability, not a lie about having granted one.

4. **`fd_read`, `fd_close`, and the two `filestat` calls consult the table.** `fd_read` already serves
   stdin; it now serves a file fd from the table. `fd_close` closes the host file and frees the fd.
   `fd_filestat_get` (an open fd) and `path_filestat_get` (a path under a dir fd) write the 64-byte
   `filestat` — filetype and size are what `os.ReadFile` reads.

5. **The errno mapping names its authority (requirement 3).** The values are the **WASI preview-1
   `errno` enum from `typenames.witx`** (the `wasi_snapshot_preview1` witx), not invented: `success`
   (0), `badf` (8), `noent` (44), `acces` (2), `isdir` (31), `notdir` (54), `inval` (28), `notcapable`
   (76). Go's `os` errors map onto them — `os.IsNotExist` → `noent`, `os.IsPermission` → `acces`, a
   directory opened as a file → `isdir`. A wrong errno makes a guest take a different branch silently,
   the least noticeable failure in the subsystem, so the mapping cites the witx and is asserted
   against a guest that observes the branch (`os.ReadFile` of a missing file must see `noent`).

6. **The CLI's two pre-decided items, recorded as taken (Scott, 2026-09-08):**
   - **argv passes after `--`:** `burroughs run prog.wasm -- args…`. The `--` separates runtime
     grammar from guest grammar, so a guest argument can never be read as a function name.
   - **`--dir HOST[:GUEST]` maps a host directory into the guest** as a preopen; **no directory is
     visible unless named.** This is decision 2's capability model at the command line.

## Consequences

- **Capability line:** a program compiled by a third-party toolchain reads a file the user granted it
  — `burroughs run cat.wasm --dir . -- input.txt`.
- **The fd table is the reusable structure**; the write slice, `fd_seek`, and directory enumeration
  widen the surface over it when a guest needs them (guest-driven).
- **The two security controls are negative tests, and must be watched dying with the escape *actually
  succeeding* (Scott's requirement).** A negative test on a capability boundary passes for three wrong
  reasons this project has already found: the mechanism is absent, the path never reaches the check, or
  the guest fails earlier for an unrelated reason. So the must-fail fixture is not "the check errors
  differently" — it is a build **with the check removed** in which the guest opens a file *outside* the
  preopen and **reports its contents**, proving the escape is otherwise reachable and the guest observes
  it. If the injected build merely errors, the control was exercised, not witnessed — a control is not
  born until it is watched die. This holds for both controls: the no-`--dir` guest that must open
  nothing, and the `..`/symlink/absolute path that must be refused.
- **`fd_prestat_get`/`fd_prestat_dir_name`/`fd_read`/`fd_close` stop being stubs**; `poll_oneoff`'s fd
  arm and everything write-side stay deferred.
