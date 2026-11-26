# 0138 — the rest of `descriptor` resolves inline, a two-descriptor call resolves through one grant's root or answers `cross-device`, and each departure from wasmtime is named

Date: 2026-11-26 · Status: **accepted**, 2026-11-26. It is an engine mechanism inside `internal/component`: no contract text, no public API (no exported symbol, no root-package file and no CLI flag changes; `ComponentConfig.Preopens` and `burroughs run --dir`/`--scratch`, stamped on [#1374](https://github.com/scttfrdmn/burroughs/issues/1374) for ADR 0133, already grant the directory, and every method here acts inside that grant), and no stamped ADR is reversed. So by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. ADR 0133 is the design authority for E1, and this ADR records only what 0133 does not decide. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1374](https://github.com/scttfrdmn/burroughs/issues/1374), E1c and E1d of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)); the slices are R1's, from its closing comment on [#1371](https://github.com/scttfrdmn/burroughs/issues/1371).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

After E1a (ADR 0133) and E1b (ADR 0134), `wasi:filesystem/types@0.3.0`'s `descriptor` had seventeen methods left at the refusing stub. E1c is the four mutations: `create-directory-at`, `unlink-file-at`, `remove-directory-at` and `rename-at`. E1d is the other thirteen: `readlink-at`, `symlink-at`, `link-at`, `set-size`, `set-times`, `set-times-at`, `sync`, `sync-data`, `advise`, `metadata-hash`, `metadata-hash-at`, `is-same-object`, and `append-via-stream`, the one stream method among them. The WIT at wasip3 0.8.0 declares no other method, so after this ADR nothing of `descriptor` is refused by name.

**ADR 0133 already decides most of what they need.** Every grant is an `os.Root`, a path is handed to it as written, an absolute path is refused first, and errors map through `fsErrorLabel`. A descriptor's flags are the ones it was opened with, and every `async func` resolves inline. **Five things it does not decide:**

- what a call over **two descriptors** resolves through (`rename-at`, `link-at`);
- the **read-only rule** for anything but `open-at`;
- **readlink-at's string result**, which is the first filesystem result that calls the guest's realloc;
- **times**: what precision, what range, and a symlink's own times;
- the methods the WIT leaves to the host: `metadata-hash`'s function, `advise`, `sync` and `append-via-stream`.

**Both fixtures were read on wasmtime 49.0.3 before this ADR.** `fs-mutate.wat` has 24 rows and `fs-rest.wat` has 41, every row 10 of 10 identical. They show five places where a faithful Burroughs cannot read as wasmtime does:

- **wasmtime renames and links across two preopens.** `rename-at` from one grant into another is ok. wasmtime resolves with `renameat` over two directory fds, and both of the fixture's trees are on one filesystem.
- **wasmtime follows a stale descriptor.** A directory opened, then renamed by its parent, still takes a `rename-at` into it, since an fd survives the rename.
- **wasmtime does not consult a child's flags.** `sub`, opened with `read` and no `mutate-directory`, takes `create-directory-at`, `unlink-file-at`, `rename-at`, `symlink-at`, `link-at`, `set-times-at` and `set-times`. The WIT's `mutate-directory` text says each must be `read-only`, and ADR 0133's child carries the flags it was opened with.
- **wasmtime sets a symlink's own times** through `set-times-at` without `symlink-follow`.
- **wasmtime's file `set-times` is nanosecond-precise** on darwin. Go's `syscall.Futimes` is microsecond-precise: measured, a `usec` of 1 is stored as 1000 ns.

## Options

**A call over two descriptors.** `os.Root.Rename` and `os.Root.Link` act inside one root. Darwin's `syscall` has no `renameat` or `linkat`, so a rename between two directory fds is not reachable from the standard library there.

1. **Path arithmetic over the two host paths.** Ruled out by ADR 0133: a symlink swapped into either path escapes the grant.
2. **Refuse any two distinct descriptors with `cross-device`.** *For:* one line. *Costs:* every guest that renames between the grant and a directory it opened fails where wasmtime succeeds, and so do two descriptors from two `get-directories` calls. That is two of the fixture's common rows.
3. **One root that reaches both.** Within one grant, the grant's own root reaches every descriptor opened beneath it. Each descriptor records its path from the grant (ADR 0133's `fsDescriptor.path`, kept for exactly this). *Costs:* a recorded path can stop naming its directory: after a parent's rename, or where a symlink and `..` made the lexical path differ from the walked one. And a resolution through the grant's root is not confined to either descriptor.

**A symlink's own times.** `os.Root` has no `Lchtimes`, and `Root.Chtimes` always follows a final symlink.

- (a) **Follow it anyway.** That sets the wrong object's times.
- (b) **ADR 0091's race-free route:** open the target `O_NOFOLLOW` and set the times on the fd. *Costs:* it opens the target, which needs read permission a times call does not, and opening a FIFO or a device has effects a times call does not. On a symlink it still cannot set the link's times; it only fails cleanly.
- (c) **Refuse by name with `unsupported`,** after asking `Root.Lstat` whether the final component is a symlink.

## Choice

**Option 3 for two descriptors, (c) for a symlink's own times,** and the rest as follows.

### A call over two descriptors

`rename-at` and `link-at` (`fsTwoDescriptors`):

- **The same descriptor:** its own root, `Root.Rename` or `Root.Link`. This includes the same handle passed twice, and is the common case, since a libc resolves both paths against one preopen.
- **Two grants: `cross-device`.** A root cannot span two grants, which is ADR 0091's condition 1 for preview 1: *"A single `*os.Root` cannot span two grants."* **This departs from wasmtime's reading.**
- **Two descriptors of one grant:** the grant's root, opened for the call from its preopen's host path and closed after it. Three checks come first:
  - **each path is resolved on its own descriptor's root** (`Root.Lstat`), so an escape from either descriptor is `not-permitted` there, as the WIT's base directory is the descriptor and not the grant;
  - **each descriptor's recorded path must still name its directory**, judged by `os.SameFile` between the grant root's `Stat` of the path and the descriptor's own `Stat(".")`;
  - **a path that does not name its directory is `cross-device`:** the two are not reachable through one root. This departs from wasmtime, which follows the fd.

  Then the two paths are joined onto the recorded ones without cleaning, so a `..` or a symlink in them is walked as the descriptor's root would walk it, and the call is the grant root's `Rename` or `Link`.
- **The window:** between the checks and the call, a component of a recorded path can be swapped. The grant's root still confines, so the swap can make the call act elsewhere inside the grant, never outside it. That is ADR 0133's bound for `open-at`'s symlink check, here over a longer window.

### The read-only rule

- **Every method that would create, rename, delete or change an object checks `mutate-directory` first**, after the one check that comes before it everywhere: a base that is a file is `not-directory`, wasmtime's reading. Those methods are `create-directory-at`, `unlink-file-at`, `remove-directory-at`, `rename-at`, `symlink-at`, `link-at`, `set-times-at`, and `set-times` on a directory. **`rename-at` and `link-at` check both descriptors.** The WIT says *"shall fail with `error-code::read-only` if they would otherwise succeed"*, and whether a call would succeed is known only by making it, so the refusal comes before anything is resolved, as `open-at`'s does (ADR 0133). A read-only child answers `read-only` for `../x` and for a name that exists.
- **A file carries the provenance of the directory it was opened from** (`fsDescriptor.baseMutates`), ADR 0091's fd-provenance precedent. `mutate-directory` *"may only be set on directories"*, so a file's own flags cannot carry it. `set-times` on a file is `read-only` unless the base it was opened from had `mutate-directory`. That keeps a read-only grant's files' times unchanged, and on a writable grant it reads as wasmtime does, a file opened `read` included. `set-size` and `append-via-stream` already need `write`, which a read-only grant refuses at `open-at`.
- **A child opened without `mutate-directory` answers `read-only`** for every mutation, which is ADR 0133's child flags read through the WIT's text. **This departs from wasmtime,** which mutates through it.

### The mutations

- **`create-directory-at`:** `Root.Mkdir` at 0777 before the umask, `os.Mkdir`'s.
- **`unlink-file-at` asks `Root.Lstat` first.** A directory is `not-permitted` on every platform. That is POSIX's own errno for unlinking a directory, and darwin's and wasmtime's reading, and the WIT allows it (*"`access` or `is-directory` may be returned instead of the POSIX-specified `not-permitted`"*). The check is needed, not chosen: `Root.Remove` removes an empty directory too. A symlink is removed as itself.
- **`remove-directory-at` asks the same.** Anything but a directory, a symlink to one included, is `not-directory`, rmdir's `ENOTDIR`. A directory is then `Root.Remove`d, which answers `not-empty` and `invalid` (for `.`) as the platform does.
- **Each check is a check before an act,** and the act is still `os.Root`'s, so a swap in the window acts on something else inside the grant, never outside it.

### `readlink-at`, `symlink-at`, `link-at`

- **`readlink-at`:** `Root.Readlink`. Content that is an absolute path is `not-permitted`, as the WIT says. Relative content is returned even when it climbs out of the grant, wasmtime's reading, since following it is what `os.Root` refuses.
- **`readlink-at`'s string is lowered through the caller's realloc, on the caller's goroutine, with no engine lock held.** The impl resolves inline, while its agent's in-host window is open, so the store is queued in that window. `leaveHost` applies it on the same goroutine with `asyncHandles.mu` released, through `guestHeap`, under every gate, `gate:cm-shared-memory` included. A sync lower stores it after the wait through the same heap (ADR 0126). **ADR 0133's sentence that "no realloc runs inside a result store" held for E1a's methods, and does not hold for this one.** Nothing new is built: this is the async lower's three-step store (`asyncLowerFuncWith`).
- **`symlink-at`:** an absolute target is `not-permitted` (`fsAbsolute`), as the WIT says. A relative target that climbs out is created, wasmtime's reading, since `Root.Symlink` *"does not validate oldname"* and following it is refused.
- **`link-at`:**
  - `symlink-follow` is `invalid`, wasmtime's reading;
  - an old path that is a directory is `not-permitted`, the WIT's *"`not-permitted` if the old path is not a file"*, asked of `Root.Lstat` first;
  - an unfollowed symlink is linked as itself, `Root.Link`'s documented behaviour and wasmtime's reading.

### Sizes and times

- **`set-size`:**
  - a directory is `bad-descriptor`;
  - a descriptor without `write` is `invalid`, which is ftruncate's errno for an fd not open for writing and wasmtime's reading. It is checked first, so every platform answers alike;
  - a size past 2^63 − 1 is `invalid`.
- **An instant is normalised and clamped** (`fsInstantTime`):
  - nanoseconds of 10^9 or more carry into the seconds, darwin's reading;
  - the result is clamped to Go's int64-nanosecond range, 1677 to 2262. That is darwin's own clamp: measured, an instant of 2^62 seconds is stored as 9223372036.854775807;
  - `now` is one `time.Now()` reading for the call, and `no-change` leaves the time unchanged.
- **`set-times` on a directory is `Root.Chtimes(".")`.**
- **`set-times` on a file is the fd's** (`fsFileTimes`), at nanosecond precision where the platform allows it:
  - darwin: `utimensat` on `/dev/fd/N`;
  - linux: `utimensat` on `/proc/self/fd/N`;
  - Windows: `SetFileTime`, with nil for `no-change`;
  - elsewhere: `unsupported`.

  `syscall.Futimes` is not taken, because it is microsecond-precise.
- **`set-times-at`, following, is `Root.Chtimes`.** It is nanosecond-precise, with `os.Root`'s documented Unix race: a target swapped to a symlink mid-call has the link's times set, never anything outside the grant.
- **`set-times-at`, not following:** a final component that is a symlink is `unsupported` (option (c)), and anything else is `Root.Chtimes`. The check is ADR 0133's `open-at` shape, so a swap in the window can make an in-grant symlink be followed, and can never reach outside the grant. **This departs from wasmtime,** which sets the link's own times.

### What the WIT leaves to the host

- **`sync` and `sync-data`:**
  - a file without `write` is `ok` with no effect, as the WIT says;
  - a file with `write` is `File.Sync`. `sync-data` is the same call, since an fsync is at least an fdatasync;
  - a directory is an fsync of `.` on darwin and linux, and no effect on Windows, whose flush needs write access to the handle.
- **`advise`:**
  - on a file it is `ok` and changes nothing, at any offset and length, as wasmtime's reading past the end shows. The advice is advisory, and darwin has no `posix_fadvise`;
  - a directory is `bad-descriptor`, wasmtime's reading;
  - an advice of 6 or more is the guest's trap at the lift, the Canonical ABI's discriminant check (wasmtime: *"unexpected discriminant: 6"*).
- **`metadata-hash` and `metadata-hash-at`** (`fsMetadataHash`) hash the device and inode numbers, wasmtime's inputs. Measured, wasmtime's hash is unchanged by a write, a `set-times` and a `set-size`, so it hashes identity and not content, and a preview-1-style inode drawn from it stays put. The function is SHA-256 of the two as little-endian u64s, with `lower` and `upper` its first two little-endian words. It is fixed rather than keyed, so a value is stable across runs. **Where Go exposes no device and inode (Windows), it hashes the type, size and modification time,** the WIT's own baseline (*"a hash of the last-modification timestamp and file size"*). There a write changes the hash, and two files alike in all three collide.
- **`is-same-object`** is `os.SameFile` of the two descriptors' stats, which is exact on every platform. A stat that fails answers false, since the result has no error case.
- **`append-via-stream` is `write-via-stream`'s writer** (ADR 0133). The difference is the offset: each write takes the file's size as its offset, under a lock on the file (`fsFile.appendMu`), so this instance's appends do not interleave. It is not `O_APPEND`'s atomicity, because Go's `WriteAt` refuses a file opened `O_APPEND`, and the descriptor's file is shared with offset reads and writes. Another process's append between the size and the write can be overwritten. A descriptor without `write`, or a directory, is `bad-descriptor` with the stream unlinked, so the guest's write completes DROPPED with 0 bytes, wasmtime's reading. **The writer goroutine is the same site** as write-via-stream's, `(*Host).fsWriteViaStream`, now made for either. The census entry names this ADR.

## Consequences

- **Every method of `wasi:filesystem/types@0.3.0`'s `descriptor` is served.** None meets the refusing stub any more. ADR 0133's consequence *"Every other `descriptor` method still meets the refusing stub, by name"* is overtaken, not wrong: it was true of E1a.
- **Four departures from wasmtime 49.0.3, each in fixture rows of its own,** with wasmtime's reading recorded beside the value Burroughs asserts:
  1. a rename or link across two grants is `cross-device`;
  2. a descriptor whose recorded path no longer names its directory is `cross-device` as the second descriptor of a rename or link;
  3. a child without `mutate-directory` is `read-only`;
  4. a symlink's own times are `unsupported`.

  **The read-only grant's rows are not departures:** wasmtime's CLI grants no read-only directory (ADR 0133), so they come from the WIT's text, a file's times included.
- **Named limits:**
  - **Every filesystem call runs on the guest's goroutine outside a safepoint** (ADR 0133's limit).
  - **The checks before an act leave windows.** Two-descriptor calls, `unlink-file-at`, `remove-directory-at`, `link-at`'s directory check and `set-times-at` each check before they act. Every window is bounded by `os.Root` to the grant.
  - **`append-via-stream` is not atomic against another process's append.**
  - **On Windows, `metadata-hash` is not an identity,** a file opened without `write` cannot have its times set (`SetFileTime` needs write access), and a directory's `sync` does nothing.
  - **On linux, a file's `set-times` needs `/proc` mounted.**

## Rollback

The seventeen methods unregistered from `Host.asyncSource` and `Host.streamConsumers`: each meets the refusing stub by name again, as before this ADR, and the rest of ADR 0133's and ADR 0134's descriptor is unchanged.
