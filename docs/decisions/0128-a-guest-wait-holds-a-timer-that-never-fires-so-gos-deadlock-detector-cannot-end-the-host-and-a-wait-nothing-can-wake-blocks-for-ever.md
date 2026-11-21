# 0128 — A guest wait holds a timer that never fires, so Go's deadlock detector cannot end the host, and a wait nothing can wake blocks for ever

Date: 2026-11-21 · Status: **accepted**, 2026-11-21. Scott, in session: *"The wait hangs, as the pin and wasmtime 49.0.3 do"*, recorded on [#1303](https://github.com/scttfrdmn/burroughs/issues/1303#issuecomment-7070131698). The ruling was given in session, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/interp`: no contract text, no public API (no exported symbol, and the root package changes only by a test), and no stamped ADR is reversed. [ADR 0121](0121-wait-for-and-wait-until-are-async-host-imports-on-the-hosts-monotonic-clock-cancelled-inline-and-stopped-by-close-and-a-sync-lifted-function-blocks-or-traps-by-its-type.md)'s park bound for the stackless park is untouched. Filed against [#1303](https://github.com/scttfrdmn/burroughs/issues/1303), found by F5 ([#1240](https://github.com/scttfrdmn/burroughs/issues/1240)).

## Context

**The defect.** A guest that waits on something nothing can ever wake ended the host process with Go's runtime fatal, *"all goroutines are asleep - deadlock!"*, and exit 2. A host process ending because of a guest's choice is an engine defect.

Measured on main at d8076a1, darwin/arm64:

- **The stackful `waitable-set.wait`** (`waitableSetWait`, in a plainly lifted, async-typed `wasi:cli/run`), on an empty set: the fatal, exit 2, 60 s after the guest reached the wait, through `burroughs run`, `ComponentConfig.Run` and `Component.Call` alike.
  - A context deadline on `Component.Call` did not save it. The deadline requests a cancellation, and a stackful wait does not observe one, which is what the pin does (def:WaitableSet.wait waits on `has_pending_event` alone). Once the deadline's timer had fired, nothing was left pending, and the fatal followed.
- **An infinite `memory.atomic.wait32`** on a shared memory, with no other agent (`internal/interp/futex.go`): the same fatal, at once.
- **The callback-lifted shape** of the first already exits 1 after 30 s with `ErrLiftParkExpired`, ADR 0121's park bound.
- **The sync-typed shape** traps *"cannot block a synchronous task before returning"* on both engines, as the pin does.

**Why 60 s and not at once.** Go's crypto/rand arms a one-minute warning timer on its first read in a process and stops it when the read returns (`crypto/internal/sysrand.Read`, Go 1.27.4). A stopped timer stays in the heap of the P that armed it until that P next checks its timers, and the detector (`runtime.checkdead`) reports nothing while any P's heap is non-empty. A component host reads crypto/rand when it is made (`newInsecureSeed`). Measured: a Go program that does one `crypto/rand.Read` and then blocks every goroutine dies after 60.17 s at GOMAXPROCS=4, and after 0.17 s without the read. The stackful witness died after 60.01 s at GOMAXPROCS=2 and 0.00 s at GOMAXPROCS=1, where the P that goes idle is the one holding the stopped timer, and it removes the timer before it idles. A core module reads no crypto/rand, and died at once.

### What the pin does

- **A sync-typed function traps.** def:canon_lift:"trap_if(not candidates)" sits inside `if not ft.async_:`, and the loop takes its candidates from the instance's threads.
- **An async-typed waiter is left blocked.** def:Store.tick takes `candidates = { thread for thread in self.waiting if thread.ready() }`, and resumes one only `if candidates:`. With nothing ready it does nothing, and the waiter stays in `store.waiting`.
- **Its own driver loops for as long as anything waits.** `run_tests.py`'s `lift_and_run`, at the pin, is `while inst.store.waiting: inst.store.tick()`.
- **Teardown is the host's policy.** `Concurrency.md` § Component Instance Lifetime, at the pin: *"If the component instance is conservatively kept alive (until a hard timeout), this may end up wasting resources for periodic background activities that run in an infinite (waiting) loop."*

The threads proposal leaves an infinite `memory.atomic.wait` with no notifier suspended for ever.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, each row 10 of 10 under a 2 s `timeout(1)`. Committed as `internal/component/testdata/unwakeable-wait.reading` and `internal/interp/testdata/wait-forever.reading`:

- `unwakeable-wait.wasm`, the stackful empty-set wait: **hangs**, killed at the bound (exit 124), nothing on stdout or stderr, 0.00 s of user and system time.
- `wait-forever.wasm`'s `forever` and `forever64`, `-W threads=y,shared-memory=y`: **hang** the same way.
- The controls: `unwakeable-wait-woken.wasm`, the same stackful wait on a set holding a `wait-for(10ms)`, exits 0. `brief`, a `wait32` with a 10 ms timeout, prints 2.
- The callback-lifted shape, read on #1303 with a scratch fixture: hangs, idle and silent.

## Options

These are the ruling's, and #1303's comment states each with its cost.

1. **The wait hangs, as the pin and wasmtime do.** The host stays blocked until the wait is woken, or for ever, and nothing is reported. *Costs:* `burroughs run` on such a guest runs until a signal ends it, and a library caller's call does not return, the way a guest in an infinite loop already does not.
2. **A named bound like ADR 0121's 30 s park bound.** *Costs:* it diverges from wasmtime, and a waker the bound's check cannot see (another Thread, another agent, a later call from the embedder) would read as expired.
3. **An immediate trap when nothing can wake the wait.** *Costs:* it diverges from the pin, which traps only a sync-typed function, and through `Component.Call` it is wrong by construction, since a later call can wake the parked task.

## Choice

**Option 1, by Scott's ruling.** A guest wait with no bound blocks until it is woken, or for ever. While it blocks it holds a Go timer, so Go's deadlock detector can never end the host. Nothing is reported, and nothing fires on a wait that can still be woken.

### The mechanism

`holdHostAwake` (`internal/interp/hostawake.go`) arms `time.AfterFunc(math.MaxInt64, func() {})` and returns it, and the waiter stops it when its wait ends. `checkdead` reports nothing while any P's timer heap is non-empty, so a held timer makes the fatal unreachable. The wait itself is unchanged.

- **A function timer.** Since Go 1.23 a channel timer is in a heap only while a goroutine is blocked on its channel, so whether a channel timer counted would depend on what the wait selects on. An `AfterFunc` timer is in its P's heap from the moment it is armed.
- **The longest duration there is,** about 292 years. It never fires, so its function never runs, and it starts no goroutine: the goroutine census (`TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`) and the goroutine checks (`requireNoSocketGoroutines` and its siblings) have nothing to see.
- **One per wait, held by the waiting goroutine,** and stopped by a `defer` however the wait ends, including a `Close` that ends it and the sentinel panic of a termination. Nothing of it runs after the wait ends.

**Not taken: a refcounted keepalive** shared by every parked waiter, whose timer exists only while the count is above zero. It needs a mutex and a count on every wait, and each transition between zero and one parked waiter still arms or stops a timer, so it costs more and buys nothing a per-wait timer does not.

**Not taken: a finite timer re-armed by its own function** while the wait is parked. It would bound the stopped-timer window below (to its span), and it costs a firing, and a runtime goroutine, per span on every parked wait, and a firing that can race the wait's end. The ruling's *"nothing fires"* is the reason it is not the default.

### Where the timer is held

**In `interp.CanonCaller.Blocking`, for every canon function's wait**, rather than at each wait. Contract §5's H-1 already requires every canon function's wait to run inside `Blocking`, and `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion` checks that it does, so a wait added later holds the timer without anyone having to remember it. The waits behind it, on main at d8076a1:

| Site | Wait | Bounded before this ADR? |
|---|---|---|
| `async_waitset.go` `waitableSetWait` | the stackful `waitable-set.wait` | **no** (#1303's subject) |
| `async_synclower.go` `awaitSyncLower` | a sync lower of an async host import, until it resolves (ADR 0126) | no |
| `cmsched.go` `giveBack` | a component-model Thread's park in `thread.suspend` and the switching forms, behind `gate:cm-threads` (ADR 0100) | no |
| `cross_component.go` `liftAsAsyncImpl` | a cross-component async callee's first segment (ADR 0095) | no, but it ends at the callee's own wait, which holds its own timer |
| `async_subtask.go`, the two sync `subtask.cancel` waits | a cancel's resolution | yes, `subtaskCancelBound` (30 s) |
| `host.go` `blockingRead` | WASI 0.2's blocking read | no; a read of an `os.File` is a system call, which the detector does not count as asleep, but an embedder's `io.Reader` can block on a channel |
| `lowerargs.go` `underCallerMark` | not a wait: a sync cross-component callee's code runs inside it, and that callee's waits are their own `Blocking` | — |

**In `memory.wait`'s infinite arm**, for `memory.atomic.wait32` and `wait64` with a negative timeout. A finite wait needs nothing, since its own timer is in a heap while the `select` is blocked on it.

**Not covered, and why:**

- **The stackless park** (`awaitEvent`) holds its own timer, ADR 0121's park bound, which this ADR does not touch.
- **The callback YIELD arm** (`cmsched.go` `yieldWait`) waits for the scheduler's reply, and the scheduler is runnable. A Thread it runs that parks for ever does so in `giveBack`, which holds a timer.
- **A host function** (`callHost`) is not a guest wait. An embedder's host function that blocks for ever on a channel is the embedder's code, and Go's fatal there is the embedder's to meet.
- **An engine defect** that blocks every goroutine outside any guest wait still ends with the fatal, and should.

## Consequences

- **`burroughs run` hangs until a signal, as wasmtime does.** `burroughs run` installs no signal handler (no `os/signal` in the tree), so Go's default handling applies: SIGINT and SIGTERM end the process. Measured with the fixed build, at the default GOMAXPROCS: alive and idle 75 s after reaching `unwakeable-wait`'s wait and `forever`'s (0.00 s user and system time), past the 60 s at which the stackful wait used to die, and ended by SIGINT with exit 130 and by SIGTERM with exit 143, nothing on stderr.
- **A library caller's `ComponentConfig.Run` or `Instance.Call` blocks**, the way a guest stuck in an infinite loop already blocks it. `ComponentConfig.Run` takes no context, and a stackful wait does not observe `Component.Call`'s, which is the pin's behaviour. `Close` ends a parked `Component.Call`. Measured with `unwakeable-wait.wasm`: the cancellation `Close` requests first is not observed either, so `Close` returns `ErrCloseIncomplete` at its 5 s bound (`componentCloseBound`), and the call returns `ErrTerminated`.
- **The stopped-timer window.** A stopped timer leaves its heap when the P that holds it next checks its timers. A waiter woken on another P leaves its stopped timer on the P that armed it, and a P that schedules nothing more never removes it. So after a wait ends, a process that later blocks every goroutine *outside* any guest wait (an embedder's own deadlock) can hang where it would have died with the fatal. Measured with a probe, 3 of 3 at GOMAXPROCS=4: a timer armed this way and stopped from a second goroutine kept such a process alive past a 5 s bound, and at GOMAXPROCS=1 it died at once. Go's crypto/rand opens the same window for a minute on its first read, and every component host already opens that one. This window has no end, and the re-armed timer above is the shape that would bound it.
- **Cost:** one runtime timer armed and stopped per `Blocking` excursion and per infinite futex wait, and one allocation. No figure is recorded: no measured run was taken on lab hardware, and no claim rests on one.
- **Witnesses**, each in a child process, because the fatal would end the test binary (`internal/testenv/hang.go`'s `RunHangChild`). The child is the test binary with `-test.timeout=0`, so `go test`'s alarm timer cannot mask the fatal, and with `GOMAXPROCS=1`, so the crypto/rand timer cannot defer it. A child alive 2 s after it starts the guest is sent SIGQUIT, and its goroutine dump must show the guest parked in its wait (`component.waitableSetWait`, `interp.(*memory).wait`), so "alive" is not read from a child that never reached the wait. On Windows, which has no SIGQUIT, the child is killed and only its being alive is read.
  - `TestAnUnwakeableWaitHangsThroughBurroughsRun` (`cmd/burroughs`), through `dispatch`, and `TestAnUnwakeableWaitHangsThroughTheLibrary` (root package), through `ComponentConfig.Run` and `Instance.Call`. Five arms each, judged against the committed readings: `unwakeable-wait`, `forever` and `forever64` hang, `unwakeable-wait-woken` exits 0, and `brief` returns 2. Both pass, also under `-race`.
  - **The period, 2 s:** with the timer removed, every hang arm ended 3 to 5 ms after starting the guest, with and without `-race`, so 2 s is a margin of more than two orders of magnitude. At the default GOMAXPROCS a period under 60 s would not discriminate the stackful arm, which is why the children run at GOMAXPROCS=1.
  - **The controls:** `unwakeable-wait-woken` (a host timer wakes the stackful wait) and `brief` (a timer ends the futex wait) end by themselves within milliseconds, and the process does not outlive them. Wakes by another Thread and by a host producer are the existing suites': the futex notify tests in `internal/interp`, `async-waitset-async-typed`, the p3 stream suites, and the liftpark, runslot and cmthreads suites, all passing unchanged.
- **Neuters, run** (darwin/arm64), each named at its site:
  - `holdHostAwake` returning a stopped timer: every hang arm, in both tests, fails with *"the host ended 4ms after starting the guest, exit 2: fatal error: all goroutines are asleep - deadlock!"* (3 to 5 ms, with and without `-race`). The controls pass.
  - the timer dropped from `CanonCaller.Blocking` only: `unwakeable-wait` fails that way, and `forever` and `forever64` pass.
  - the timer dropped from `memory.wait` only: `forever` and `forever64` fail that way, and `unwakeable-wait` passes.

## Rollback

Dropping the timer from either site restores #1303: a guest's unwakeable wait ends the host with Go's deadlock fatal, at once or after the crypto/rand minute.
