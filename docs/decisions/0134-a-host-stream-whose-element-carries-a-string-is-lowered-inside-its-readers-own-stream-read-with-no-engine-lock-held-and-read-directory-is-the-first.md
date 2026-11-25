# 0134 — a host stream whose element carries a string is lowered inside its reader's own `stream.read`, with no engine lock held, and `read-directory` is the first

Date: 2026-11-24 · Status: **accepted**, 2026-11-24. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes, no exported symbol and no CLI flag; `ComponentConfig.Preopens` and `burroughs run --dir`/`--scratch`, stamped on [#1374](https://github.com/scttfrdmn/burroughs/issues/1374) for ADR 0133, already grant the directory, and listing one is inside that grant), and no stamped ADR is reversed. So by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. The design is R1's, from its closing comment on [#1371](https://github.com/scttfrdmn/burroughs/issues/1371) (its "Gaps" item 1 and its E1b slice). The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1374](https://github.com/scttfrdmn/burroughs/issues/1374), E1b of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047), and every citation into the reference names the pin it read, 2e7895c.

## Context

`wasi:filesystem/types@0.3.0`'s `descriptor` has one listing method:

```wit
record directory-entry { %type: descriptor-type, name: string }
read-directory: func() -> tuple<stream<directory-entry>, future<result<_, error-code>>>;
```

The WIT: *"This always returns a new stream which starts at the beginning of the directory. Multiple streams may be active on the same directory, and they do not interfere with each other."* The `.` and `..` entries are omitted. A `directory-entry` is 24 bytes aligned to 4: the `descriptor-type` variant at 0 (its case byte, and `other`'s `option<string>` at 4), and the name's pointer and length at 16 and 20.

**It is the one new Canonical-ABI mechanism E1 needs** (R1's gap 1), because every host stream Burroughs produced carried bytes (stdin, a socket's receive, a file's `read-via-stream`) or owns (a listen's accepted sockets, ADR 0125), and neither calls the guest. A `directory-entry` carries a string, and lowering a string calls the reader's `cabi_realloc`, which is guest code. What R1 found, and this ADR's author re-read in the code:

- **The decoder dropped `stream.read`'s realloc.** `parseCanon`'s `0x0f` arm kept the memory and the `async` canonopt, and the walk bound every async built-in with `CanonOptions{Memory: d.lowerMem}`, so the read's `CanonCaller` had no realloc to call.
- **A host copy had two arms:** `copyIntoLocked`'s bytes and `copyOwnsLocked`'s owns. Both run under `asyncHandles.mu`, and a realloc there is guest code under an engine lock, which ADR 0101's result-store amendment and `async_lower.go`'s three-step store exist to prevent (`TestAResultStoresReallocRunsWithNoEngineLockHeld`).
- **A host copy runs wherever its delivery lands**: on a producer's goroutine, through `acquireRun`, or on the slot holder's as it leaves host code. Under `gate:cm-shared-memory` a store applied off its agent's goroutine refuses a realloc by name (`offAgentHeap`, ADR 0130), since `cabi_realloc` runs on the lowering agent's stack.
- **The link checked a producer's stream as `stream<u8>`** (`checkConsumerPayload`'s producer arm), so `read-directory`'s `tuple<stream<directory-entry>, …>` needed a check of its own, as `tcp-socket.listen`'s did (`checkListenSignature`).

The pin's reallocation sets `may_leave` false around the call (def@2e7895c:LiftLowerContext.reallocate:"self.inst.may_leave = False"), so a realloc cannot call a built-in or an import. And its string store allocates at alignment 1 for the string's byte length, once per string (def@2e7895c:store_string_copy:"ptr = cx.allocate(dst_alignment, dst_byte_length)").

### Both oracles, read before this ADR

`testdata/fs-readdir.wat` has 17 rows, every one using the async `stream.read`, since the sync form is refused (#1272). Sixteen were read on wasmtime 49.0.3 (10 of 10 per row), and on the pin through `refread.py`, with its host written as Burroughs' producer is stated below (16 of 16 seeds per row). The seventeenth is the read-only grant's, which wasmtime's CLI cannot grant (ADR 0133). The two oracles agree on every row. What they read:

- **A read that finds entries returns BLOCKED, and its event carries them.** A read of 8 on a three-entry directory is BLOCKED, then `0x31`: DROPPED with progress 3, in the one event. On one entry it is `0x11`, and on five, `0x51`.
- **A read that finds nothing left takes DROPPED inline:** `0x01` on an empty directory, and on the read after the last entry.
- **A read of 1 at a time is never told the end early:** three reads BLOCKED with `0x10` each, COMPLETED with 1, then a fourth DROPPED inline.
- **A read of 0 completes inline with progress 0,** and touches nothing.
- **A name is lowered through one realloc call per name,** `(0, 0, 1, len)`: four names of 1, 6 (`é.txt`, two bytes for the `é`), 40 and 100 bytes read four calls, alignment 1, 147 bytes, and their contents in the heap the realloc handed out.
- **A file descriptor's stream is finished at once:** its read takes DROPPED inline, and its future is `err(not-directory)`. A directory opened with no descriptor flags is listed as any other. A reader that drops its stream before the end has its future resolve `ok`.
- **The entry's type is its link's, not its target's:** a symlink reads `symbolic-link`.

## Options

**Where the realloc runs.**

1. **In a delivery, as the byte arm copies:** a producer goroutine reads the directory, and the store lowers each entry under the run slot. *Costs:* the store holds `asyncHandles.mu` today, so it would have to become three steps; it runs off the reader's goroutine whenever the slot's holder is not the reader, so under `gate:cm-shared-memory` every string would meet `offAgentHeap`'s refusal; and a goroutine site the census must authorise, for I/O that returns in microseconds on a local disk.
2. **Inside the reader's own `stream.read`**, on the reader's goroutine: the read reads a batch of the directory and lowers it with no engine lock held (R1's design). *For:* the realloc runs on the stack it belongs to, under every gate, so `offAgentHeap` is never this arm's heap; no goroutine and no delivery exist to order; the lock discipline is `async_lower.go`'s three steps, once. *Costs:* the read does host I/O on the guest's goroutine outside a safepoint, ADR 0133's named limit for every filesystem call.

**DROPPED with progress.** wasmtime's `0x31` is the event of a read that parked, and the pin admits it there: the event's result is computed when it is taken, from whether the writer is still there (def@2e7895c:StreamEnd.notify.stream_event:"if self.other is None:"), so a host that writes into a parked read and then drops its writer gives that read DROPPED with the copy's progress. **The pin does not admit it inline.** A read that arrives second copies and takes its event in the same step, with the writer present (def@2e7895c:copy:"code,index,payload = end.get_pending_event()"), so an inline return is COMPLETED with its progress. The pin's `writer-first` row reads exactly that: COMPLETED with 1 inline, then DROPPED inline on the next read.

- **(a) Burroughs' byte producers' rule** (ADR 0122): deliver COMPLETED, and finish on the next read. *For:* pin-legal, and one rule for every producer. *Costs:* a listing takes one read more than wasmtime's, and five of the sixteen wasmtime rows read otherwise: one, one-drain, many, batch and no-read-flag. (This sentence named the two one-at-a-time rows in place of no-read-flag until the build measured option (a) as a neuter: a read of 1 is never short, so it never finishes early under either option.)
- **(b) wasmtime's reading:** a read that finds entries parks with its copy's event already pending, and a batch that ends the listing finishes the stream behind it, so the event is DROPPED with the batch's progress. *For:* pin-legal, as above, and every row reads as both oracles do. *Costs:* a read returns BLOCKED with its event already pending, so the guest waits for an event that is there, which is the precedent stdin's end set (ADR 0122's read-park).

## Choice

**Option 2, with (b).**

### The decoder and the walk

- **`stream.read`'s realloc survives the decode** (`parseCanon`'s `0x0f` arm) and is bound into the built-in's caller, resolved and type-checked as a lower's is (`walker.lowerRealloc`), so a mistyped one is `ErrCanonOptionInvalid` at instantiate.
- **Only `stream.read`'s is kept.** `future.read`'s, which the pin also uses for a payload carrying a string, is still dropped: no host future of this engine resolves with a string (ADR 0133's `other` carries none), so nothing reads it.

### The element and the signature

- **`dirEntryElem` is the host stream's element**, `record { type: descriptor-type, name: string }` through the codec, stamped on both ends at the mint. The guest's `stream.read` is checked against it as every stream built-in is (`checkStreamEndType`, ADR 0124), and its buffer strides by its 24 bytes.
- **The link's producer arm checks the producer's own element** (`streamConsumer.elem`, nil for `u8`): read-directory's guest must declare `tuple<stream<directory-entry>, future<result<_, error-code>>>`, the entry structurally the host's (`canonTypeIn`, `canon.TypeEqual`), or the import is `ErrLinkRefused`. The guest reads the two handles at its own declaration's offsets and the entries at its own element's layout, so a guest declaring another shape is refused at instantiate rather than handed bytes it reads as something else.

### The read is three steps, and only the middle one runs guest code

`streamRead`, finding a directory producer's writer, takes the read itself:

1. **Under `asyncHandles.mu`:** the pin's traps, in def:copy's order, as for every read; the end to COPYING; the destination captured; the producer marked serving. A finished listing takes DROPPED inline here, and a read of 0 COMPLETED with 0 inline, def:End.copy's last arm.
2. **No lock:** `File.ReadDir` for the read's count, and each entry lowered at `dst + 24·i` through `canon.StoreVia` over the reader's own `guestHeap`, so each name's realloc is the reader's `cabi_realloc`, called on the reader's goroutine.
3. **Under `asyncHandles.mu`:** the copy armed COMPLETED with its progress, and, if the batch ended the listing, the stream finished behind it (the future resolved, then the writer dropped, ADR 0122's order), which makes the pending event DROPPED at its take. The read returns BLOCKED with that event pending. A read that lowered nothing and found the end takes DROPPED inline.

**A Close in between** finds the producers stopped at step 3, and the read returns BLOCKED with nothing armed: the instance is closing. `Close` closes the listing's file, so a `ReadDir` it interrupts returns an error that nothing reads.

### A built-in on an end being served

While step 2 runs, the end is COPYING, so a second `stream.read` traps (not IDLE) and `stream.drop-readable` traps (mid-copy), as for any read in flight. **`stream.cancel-read` of an end being served traps by name**, after the pin's own checks: the copy is under way and cannot be cancelled into, and the pin traps a cancel of an end a read is waiting in, def@2e7895c:cancel_copy:"trap_if(end.has_sync_waiter)". It is reachable only from a second agent of a shared-memory instance (`gate:cm-shared-memory`): otherwise the reader holds the run slot for the call, and its realloc cannot leave (`may_leave` is false), so no other code of the instance runs while it is served.

### The off-agent refusal is not reached

Under `gate:cm-shared-memory` the lowering still runs on the reader's goroutine, with the reader's `CanonCaller`, so `offAgentHeap` is never its heap. **That is structural rather than checked**, and the one place it could break is refused by name: the copy under `asyncHandles.mu` (`copyIntoLocked`), which a delivery or a second arriver runs, refuses an element that is neither a byte nor an own. Only a directory producer's stream carries one, and its reads never reach that copy.

### read-directory's producer

- **Each call opens the directory afresh** (`Root.Open(".")` on the descriptor's root), so each stream starts at the beginning, and two do not share an offset. The file joins the instance's registry (`hostFS.adopt`); the stream's finish or its reader's drop releases it, and `Close` closes one nobody finished.
- **A batch is `File.ReadDir(n)`, not `os.ReadDir`**, which reads the whole directory and sorts it. The read asks again while it is short of n, so **a short batch is the end of the listing** (`io.EOF`), and a full one is not probed past, which is why a read of 1 is not told the end early. Entries come in the host's order.
- **An entry's type is `DirEntry.Type()`**, the link's own type and never its target's, mapped as `stat`'s is (`fsTypeLabel`); `other` carries none. **A name that is not UTF-8** ends the stream with `err(illegal-byte-sequence)` after the entries before it, since a component string must be valid. Linux admits such a name; this machine's filesystem does not, so it is not measured on wasmtime.
- **A read error** ends the stream with the error's code (`fsErrorLabel`), after the entries the batch returned.
- **A file descriptor's stream is finished at its first read,** DROPPED inline with the future `err(not-directory)`, wasmtime's reading. A descriptor's flags are not consulted, as wasmtime does not.
- **A reader that drops early** stops the producer and resolves the future `ok` (`readerDroppedLocked`), wasmtime's reading.

## Consequences

- **A component can list a directory it is granted**, through `ComponentConfig.Run`, `ComponentConfig.LoadComponent` and `burroughs run --dir`/`--scratch`. A read-only grant lists, since listing writes nothing.
- **A host stream can carry an element with a string**, and the next one costs a producer of its own and nothing in the copy: the arm is the read's three steps, not an element kind.
- **Named limits:**
  - `ReadDir` runs on the guest's goroutine outside a safepoint, so one that stalls, on a network filesystem for instance, stalls a stop-the-world with it (ADR 0133's limit).
  - A stream.read of a string-carrying element with no `realloc` canonopt is not refused at instantiate, where wasm-tools 1.258.0 refuses it (*"canonical option `realloc` is required"*). It fails at its first name, with `CanonCaller.Realloc`'s error. No canon in this engine enforces that rule yet.
  - `future.read`'s realloc is still dropped at decode (above).
  - Entries come in host order, so a guest that wants an order sorts.

## The build, as measured

On darwin/arm64, in the worktree of #1374's E1b branch. The readings were committed before the engine code (`fs-readdir.reading`, `fs-readdir-run.reading`, and `refread.reading`'s `fs-readdir` lines).

- **`TestFSReadDirectoryMatchesBothOracles`** (`internal/component/hostfs_readdir_test.go`, over `testdata/fs-readdir.wat`): all sixteen wasmtime rows, and the read-only row, matched with `gate:cm-threads` off and on, plain and under `-race`. When each row returned, the instance's registry held only the file descriptors it had left open.
- **`TestADirectoryReadIsLoweredWithNoEngineLockHeld`** stands inside each of the names row's four realloc calls and finds `asyncHandles.mu`, the registry's lock and the resource table's lock all free. From inside the first, a cancel of the end traps by name, a drop traps mid-copy and a second read traps not IDLE, and the row still reads wasmtime's value.
- **The public paths:** `TestAComponentListsTheDirectoryItIsGranted` (`ComponentConfig.Run`) and `TestRunAComponentThatListsItsDir` (`burroughs run`) matched `fs-readdir-run.reading`: 134 on `--dir` and `--scratch`, 3 on neither.
- **What the neuters showed** (each run, each reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **The realloc is the whole mechanism's precondition.** With it dropped at the decode, or not bound into the built-in, the fifteen rows that lower an entry trap at their first name.
  - **The three steps are what keep the realloc lock-free.** With `asyncHandles.mu` held across the lowering, all four probes find it held.
  - **DROPPED with progress is a property of the parked read.** Returning the event inline fails six rows, each with its payload inline: one, one-at-a-time-imm, batch, read-only-one, no-read-flag and early-drop. Option (a), no finish behind the batch, fails six too: one, one-drain, many, batch, read-only-one and no-read-flag.
  - **The cancel guard is load-bearing:** without it the cancel from inside the realloc returns CANCELLED with the read still being lowered, the drop after it removes the handle, and the guest's wait for its own read traps.
  - **The copy's refusal of a string element is reached only by asking it:** without it, a directory stream's copy under the lock returns progress 0 and no error.

## Rollback

`read-directory` unserved again: its import meets the refusing stub by name, as before this slice, and the decoder's and the walk's realloc for `stream.read` binds a realloc no host stream calls.
