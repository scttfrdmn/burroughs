# 0133 — `wasi:filesystem@0.3` serves each preopen through an `os.Root`, mints a descriptor per `get-directories` call, and resolves every async method inline

Date: 2026-11-24 · Status: **accepted**, 2026-11-24. The public half is Scott's: his two rulings on E1's public API, given in session and recorded on [#1374](https://github.com/scttfrdmn/burroughs/issues/1374) (*"`ComponentConfig.Preopens []Preopen` is approved as proposed … The zero value grants nothing, and there is no feature gate"*, and *"`burroughs run` accepts `--dir` (read-only) and `--scratch` (writable) for components, with wasip1's one-colon `HOST[:/GUEST]` syntax, mapped to `ComponentConfig.Preopens`"*). The stamp's limit is ADR 0097's form: this slice adds that one field and lets those two existing flags reach a component, and nothing else. The rest is an engine mechanism inside `internal/component`, which no contract text governs and which reverses no stamped ADR, so by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. The design is R1's, from its closing comment on [#1371](https://github.com/scttfrdmn/burroughs/issues/1371). The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1374](https://github.com/scttfrdmn/burroughs/issues/1374), E1a of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

`wasi:filesystem@0.3.0` has two interfaces. `preopens` has one function, `get-directories: func() -> list<tuple<descriptor, string>>`. `types` has one resource, `descriptor`, and its methods fall into three shapes:

- **Stream methods, not async:** `read-via-stream(offset) -> tuple<stream<u8>, future<result<_, error-code>>>`, `write-via-stream(data: stream<u8>, offset) -> future<result<_, error-code>>`, `append-via-stream` and `read-directory`.
- **Every other method is an `async func`**, among them `open-at`, `stat`, `stat-at`, `get-flags` and `get-type`.
- **`error-code` is a variant of 37 cases**, the last `other(option<string>)`.

**Confinement is in the WIT's own preamble:** *"If a `path` argument to any WASI function starts with `/`, or if any step of resolving a `path`, including `..` and symbolic link steps, reaches a directory outside of the base directory, or reaches a symlink to an absolute or rooted path in the underlying filesystem, the function fails with `error-code::not-permitted`."* `open-at`'s text adds the grant rule: a `write` or `mutate-directory` flag, or a `create` or `truncate` open flag, on a base without `mutate-directory` *"fails with `error-code::read-only`"*.

What Burroughs served before this slice: `get-directories` as an empty list from `Host.wasi`, and `descriptor` as a declared host resource that nothing minted and whose destructor was nil. `ComponentConfig` had no preopens, and `burroughs run` refused `--dir` and `--scratch` for a component. wasip1's `internal/wasi/fs.go` opens an `os.Root` only for a writable grant (ADR 0091), and its read path resolves with `filepath.EvalSymlinks` and a containment check, which is check-then-open.

**wasmtime 49.0.3 serves the interface**, so it is the oracle (R1's readings, on #1371). Its CLI has no read-only grant: `--dir` is always `read|mutate-directory`.

E1a is the first of R1's four slices: the grant, `get-directories`, the descriptor, `open-at`, `stat`, `stat-at`, `get-type`, `get-flags`, `read-via-stream`, `write-via-stream` and the error table. That is enough for Go's `os.ReadFile`, `os.WriteFile` and `os.Stat` once the fork binds them (F11a). `read-directory` is E1b's, since it needs a stream element carrying a string, and the mutations are E1c's.

## Options

1. **wasip1's split: an `os.Root` for a writable grant, `EvalSymlinks` and a containment check for a read-only one.** *For:* the code exists. *Costs:* the read path is check-then-open, so a symlink swapped in between the check and the open escapes the grant. It also answers wasip1's errnos, not the WIT's `not-permitted`.
2. **An `os.Root` for every grant, read-only included, with read-only enforced by the descriptor's flags.** *For:* `os.Root` confines with `openat` at every step, `..` and symlinks included, so there is no window. One code path serves both grants, and the WIT's `read-only` rule is a check on flags the descriptor already carries. *Costs:* Go's escape error is unexported (`errPathEscapes`, *"path escapes from parent"*), so mapping it to `not-permitted` matches its string, and a witness has to pin that string to the toolchain.
3. **Path arithmetic over the host path,** joining and cleaning the guest path under the grant. Ruled out: it is the thing `os.Root` exists to replace, and a symlink defeats it.

For the async methods:

- **(a) Inline:** the impl does the call on the guest's goroutine and resolves before it returns, so an async lower returns RETURNED, as wasmtime 49.0.3's does for `stat` and `open-at`.
- **(b) A goroutine per call,** as `tcp-socket.connect`'s dial has. *Costs:* a new goroutine site per method, for calls that return in microseconds on a local filesystem.

## Choice

**Option 2, with (a).**

### The grant

- **`Host.Preopens`, an internal `[]Preopen{Host, Guest, Writable}`,** is set from `ComponentConfig.Preopens`, which reuses `burroughs.Preopen` unchanged. A `Guest` left empty is the host path, as wasip1's is. The zero value grants nothing, and `get-directories` stays an empty list (`TestP3TcpMatchesWasmtime` relies on it).
- **Each preopen is checked at instantiate:** it must name an existing directory, or instantiate fails naming it, as wasip1's `initFDs` fails the run.
- **0.3's `get-directories` moves from `Host.wasi` to `Host.resourceSource`,** so it can mint owns, as `tcpCreate` does. 0.2's stays in `Host.wasi`, an empty list, since none of 0.2's descriptor methods is served. Each call mints one fresh `own<descriptor>` per preopen, each over an `os.Root` of its own (`os.OpenRoot` of the host path), and lowers the list through the caller's realloc. Two calls give two descriptors, each closed by its own drop, as wasmtime's two calls give two handles.
- **A preopen's flags:** `Writable` false is `read`, and true is `read|mutate-directory`.

### The descriptor

`fsDescriptor` holds its grant's index, its path relative to the grant (for E1c's `rename-at` and `link-at`), its flags, and either a nested `*os.Root` (a directory) or a reference to an open file (`fsFile`). **Its destructor closes the root, or releases the file's reference.**

**A file is reference-counted, `sockConn`'s shape.** The descriptor holds one reference, and each `read-via-stream` and `write-via-stream` takes one and releases it when its stream finishes, so a stream outlives a dropped descriptor, as the WIT's *"do not interfere with each other"* implies. The last release closes the file. Each instance's files are in a registry (`hostFS`, in `hostResources` beside `net`), so `Close` reaches one whose descriptor and streams the guest never finished.

### Paths

- **An absolute path is refused before any call**, with `not-permitted`. `os.Root` refuses one too, but with its unexported escape error, so the engine does not depend on that string for the case it can see for itself.
- **Every other path is handed to `os.Root` as written.** An escape, by `..` or by a symlink, absolute or relative, comes back as `errPathEscapes`, which maps to `not-permitted`. A witness pins the string against the toolchain in `go.mod`.
- **`path-flags.symlink-follow` absent, on a final component that is a symlink:** `open-at` answers `loop` (POSIX `O_NOFOLLOW`'s `ELOOP`, and wasmtime's reading), and `stat-at` reads the link itself (`Root.Lstat`). `Root.OpenFile` always follows a final symlink, so `open-at` asks `Root.Lstat` first. That is check-then-open, but only for *whether* to follow: confinement is still `os.Root`'s, so a swap in the window can make a symlink inside the grant be followed, and can never make one outside it reachable.

### `open-at`

- **The read-only rule is checked first,** on the base's flags, exactly as the WIT states it, and answers `read-only`.
- **Flags map to `os.OpenFile`'s:** `read|write` is `O_RDWR`, `write` alone `O_WRONLY`, anything else `O_RDONLY`; `create`, `exclusive` and `truncate` are `O_CREATE`, `O_EXCL` and `O_TRUNC`. A new file's mode is 0666 before the umask, `os.Create`'s.
- **A directory is opened as a nested root.** The path is opened, and if the result is a directory it is closed and reopened with `Root.OpenRoot`. `open-flags.directory` on anything else is `not-directory`.
- **The new descriptor's flags are the ones asked for,** so a child opened without `mutate-directory` cannot create beneath itself.

### The async methods resolve inline

`open-at`, `stat`, `stat-at`, `get-flags` and `get-type` are in `Host.asyncSource`. Each lends its `self` for the call (`lendAs`), does the call, and calls `onResolve` before it returns, so the async lower returns RETURNED. The sync lower of each is ADR 0126's blocking lower, which finds the subtask already resolved and does not wait. Every result is a fixed-size value or an own: `other` carries no string, and `descriptor-type`'s `other` is never answered. So no realloc runs inside a result store, including under `gate:cm-shared-memory`, whose off-agent store refuses one.

**`stat`'s record:** the type, the link count, the size, and three optional instants. The modification time is `FileInfo.ModTime`. The access and status-change times and the link count come from the platform's `stat` record where Go exposes one (darwin and linux); elsewhere both times are none and the count is 1. Each is `some` where it is known.

### The two stream methods

- **`read-via-stream` is ADR 0122's producer** over an `io.SectionReader` of the file from the offset, its goroutine counted in the file registry (`track`) and waited out by `Close`. The end of the file is `ok`, and a Read error maps through the error table. A descriptor without `read` gets a finished stream and `err(bad-descriptor)`, and a directory gets `err(is-directory)`. Neither is measured on wasmtime.
- **`write-via-stream` is `tcp-socket.send`'s shape** (ADR 0123): the guest's writes are copied into a bounded host buffer under `asyncHandles.mu`, and a writer goroutine writes them with `WriteAt` at an advancing offset with no lock held. The future resolves `ok` once the guest's writer has dropped and every byte is written. A descriptor without `write`, a directory included, resolves `err(bad-descriptor)` inline, and the stream is unlinked, so the guest's write completes DROPPED with 0 bytes. That is wasmtime 49.0.3's reading on a read-only fd.
- **The writer goroutine is a new site,** authorised by this ADR in the census (`TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`). It runs no guest code and touches no guest memory, so §4's boundary model and the table twin (#662) are not engaged by it: the guest's bytes are copied into the host buffer under the run slot, by the guest's own `stream.write` or by a delivery, and the goroutine writes only the host buffer to the file. Its deliveries follow ADR 0101's discipline, as the send writer's do, and it is counted in the file registry and waited out by `Close`. **At `Close` it writes what its buffer holds**, ADR 0127's reason: the guest was told those writes were complete.
- **The pending write is a waker for the park bound** (`asyncHandles.filePending`), as a socket's is (`sockPending`).

### The error table

`fsErrorLabel` maps a Go error to its `error-code` case, in `sockErrorLabel`'s style: the escape string first, then the `syscall.Errno` table, then the `os.Err*` sentinels, and `other` for anything else. `other` carries `none`, so the guest's realloc is never called for an error.

## Consequences

- **A component can read, write and stat files beneath a preopened directory,** through `ComponentConfig.Run`, `ComponentConfig.LoadComponent` and `burroughs run --dir`/`--scratch`. A read-only grant refuses a write with `read-only`.
- **Every other `descriptor` method still meets the refusing stub, by name,** among them `read-directory` (E1b), the mutations (E1c) and `append-via-stream` (E1d).
- **`wasi:filesystem@0.2` is unchanged:** E1a serves 0.3 only, and 0.2's `get-directories` keeps answering an empty list.
- **Named limits:**
  - A filesystem call runs on the guest's goroutine outside a safepoint, so one that stalls, on a network filesystem for instance, stalls a stop-the-world with it. Preview 1's path calls have the same limit.
  - A descriptor's relative path goes stale if a parent directory is renamed. Nothing in E1a reads it.
  - The escape mapping is tied to Go's error string, which the witness pins.
  - Opening a FIFO blocks in `open-at` until a writer appears, as `openat` does.

## The build, as measured

On darwin/arm64, in the worktree of #1374's branch. The readings were committed before the engine code (`fs-descriptor.reading`); `fs-run.reading` was taken after it and before the public paths that run it.

- **`TestFSDescriptorMatchesWasmtime`** (`internal/component/hostfs_test.go`, over `testdata/fs-descriptor.wat`): all twenty wasmtime rows and both read-only rows matched, with `gate:cm-threads` off and on, plain and under `-race`, and no file goroutine survived `Close`. The host's tree read as wasmtime's did after each row that writes.
- **The public paths:** `TestPreopensReachBothConfiguredPaths` (Run and LoadComponent) and `TestRunAComponentWithDirAndScratch` (`burroughs run`) matched `fs-run.reading`: 111 on `--dir`, 61 on `--scratch`, 3 on neither.
- **What the neuters showed** (each run, each reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **The read-only rule is the whole of read-only.** Dropped, a read-only grant opened hello.txt for write, created new.txt and truncated hello.txt on the host: the grant's `os.Root` confines, and does not refuse a write.
  - **`os.Root` is the confinement.** Replaced by path arithmetic, `../x` and `sub/../../x` reached outside the grant, and `../hello.txt` opened from `sub` reached the grant's file.
  - **The absolute-path refusal is invisible through the fixture**, since `os.Root` refuses `/etc` too; its witness is the function's table test. Dropping the escape mapping instead turned three of the four escapes into `other`, and left `/etc` `not-permitted`, which is that refusal holding.
  - **Inline resolution is what makes the lower return RETURNED.** Resolved from a goroutine, the async lowers of `stat`, `stat-at` and `get-type` returned STARTED with a subtask.

## Rollback

`Host.Preopens` ignored, with `get-directories` an empty list again: no descriptor can be minted, so every method is unreachable, and the tree is as it was before E1a.
