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

## Amendment, 2026-09-09 — requirement 1's write refusal keys on `oflags`, not `fs_rights_base`

Appended on implementing #690, on the rule that records are append-corrected. Requirement 1 said a
path_open "requesting write access — write bits in `fs_rights_base`, or `oflags` with create/truncate —
returns `ENOTCAPABLE`." The `fs_rights_base` half was falsified by the guest: **Go's `os.Open` sends
`oflags=0x0` but `fs_rights_base=0xff7febe`, which includes `fd_write`** — the runtime requests broad
rights speculatively even for a read, expecting the host to grant the intersection, not to refuse.
Refusing on those bits refused *every* read.

So read-only is enforced two ways, neither of them the `fs_rights_base` set: a **create/truncate/excl
`oflags`** open (the shape `os.Create` sends) is refused `ENOTCAPABLE` at path_open, and an actual
**`fd_write` to an opened file fd fails** (a file entry carries no writer, so `fd_write` answers
`EBADF`). A plain open is granted a read-only fd. The guarantee requirement 1 wanted — no write reaches
the host — holds; the mechanism is the open mode and the write call, not the requested rights, because
the requested rights are not a reliable signal of intent from this runtime. Measured, not assumed
(`typenames.witx` names the bits; the guest named the value).

## Append, 2026-09-23 — ONE ARRIVAL FROM THE DEFERRED SET, NOT THE CATEGORY'S ARRIVAL (#816)

This ADR deferred *"the write slice, `fd_seek`, and directory enumeration"* to *"a later write slice …
when a guest needs them (guest-driven)"*. **A guest needs two of them. This append records that, and
deliberately does not record more than that.**

**Read this as one arrival, not as the write slice happening.** The category above is still open: of what
its wording names, the write surface has **no consumer** and stays refused. A reader who takes this append
as "the deferred surface is now implemented" would be wrong, which is why the scope is stated before the
table rather than after it. (Scott's disposal instruction, on ruling the scope.)

### The consumer, and what it obliged

Phase 4 slice 6 runs Go **test binaries** — programs nobody wrote for this engine. They import nine
preview-1 functions this host did not supply. ADR 0080's rule is that the set must be **supplied whole
because link refuses a gap**; it says nothing about bodies. So supplying and implementing are different
sets, and the obligation was measured **per FAILURE**, not per call, on released-**go1.27.1** binaries with
no fork code in them:

| package | of the nine: imported | called | scope of the measurement |
|---|---|---|---|
| `sync` | 8 | **0** | full package |
| `context` | 7 | **0** | full package |
| `archive/zip` | 9 | **3** | full package |
| `runtime` | 9 | **0** | **the four named tests of Phase 4 amendment 2**, not the package — 1032 tests is not a bounded run on an interpreter |

**The table mixes scopes and says so**: three rows are whole packages, `runtime`'s is a named subset. A
`runtime` row at full scope is not available in bounded time, so its zero is a floor, not a census.

`archive/zip`'s three calls, and what refusing each one did:

| function | measured | disposition |
|---|---|---|
| `fd_pread` | called ×38; refusing it **fails** `TestFSModTime` (`read testdata/subdir.zip: Not implemented on wasip1`) | **implemented** |
| `fd_readdir` | called ×1; refusing it **fails** `FuzzReader` (`readdirent testdata: Not implemented on wasip1`) | **implemented** |
| `path_create_directory` | called ×1; **refusal absorbed — no test failed** | refused, and the refusal counted |
| `fd_seek`, `fd_filestat_set_size`, `path_readlink`, `path_remove_directory`, `path_symlink`, `path_unlink_file` | imported, never called | refused by name, refusal counted |

### AN IMPORT IS A POSSIBILITY, A SYSCALL IS A CONSUMER

`path_create_directory` is the trap this distinction catches, and it would have caught me: it is in the
import set, it is in this ADR's write category **by name**, and a guest **calls it** — so an import list,
or even a call list, would have justified implementing it. The syscall trace refuted that: `testing`
absorbs the refusal and no test fails. **Scott's phrasing, kept because it is the sharpest form of this
project's deferral law so far** (*a deferral's trigger is a hypothesis about its consumer*), here applied
one level up — to which deferral **owns** a function, and to whether a function named in a category has a
consumer at all.

### THE READ-ONLY DECISION SURVIVES THIS APPEND INTACT

**Both implemented functions are reads.** `fd_pread` reads a descriptor the capability model already
granted, at an offset; `fd_readdir` lists a directory already granted. Neither accepts a write request,
neither widens `path_open`'s accepted rights, and requirement 1's refusal is untouched. The deferral's own
wording predicted a write slice; the consumer that arrived needs no writes.

Also unchanged, and stated because the names are adjacent: **`fd_write` was already implemented** (stdio),
and **`fd_pwrite` is imported by none of the four packages**, so it has no consumer here and is not part of
this arrival.

### What the implementation owes, and one thing it deliberately refused to do

- **`fd_readdir` is stateless.** A resumable enumeration wants a cursor in the `fdEntry`; that would make
  entries mutable and silently invalidate the fd table's map-level lock, whose sufficiency rests on *"the
  table is mutable and its entries are not"* (#813). That note named this slice as its precondition, so
  `fd_readdir` re-reads the directory by path per call and slices by the cookie — O(n) per call, paid
  deliberately, entries still immutable.
- **Refusals are counted, not merely returned.** A function refused by name is a claim that no guest needs
  it, and that claim is falsifiable only if the host can say whether it was reached. Without the counter,
  "never called" and "called and quietly refused" are the same observation from outside.
- **`ENOSYS`, not `ENOTCAPABLE`.** This ADR set that distinction and it is load-bearing: `ENOTCAPABLE` says
  *the capability model refuses you* — a write request, an escape past a preopen — and a guest may
  reasonably degrade on it. `ENOSYS` says *this engine does not implement this*, which keeps a deferral
  visible instead of dressing it as policy.
