// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"math"
	"time"
)

// holdHostAwake arms a timer that never fires, and returns it for the caller to stop when its wait ends (#1303,
// ADR 0128).
//
// # Why a guest wait holds a timer
//
// A guest may wait on something nothing can ever deliver: a stackful `waitable-set.wait` on an empty set, or a
// `memory.atomic.wait32` with no timeout and no other agent. The pin leaves such a wait blocked, and wasmtime
// 49.0.3 hangs on it. Go does not leave a process blocked when every goroutine is blocked on a channel or a
// lock: the runtime's `checkdead` ends it with *"all goroutines are asleep - deadlock!"* and exit 2. That is what
// Burroughs did, through `burroughs run`, `ComponentConfig.Run` and `Component.Call` alike. A guest's choice
// ended the host.
//
// `checkdead` reports nothing while any P's timer heap is non-empty, because it cannot see past a timer. So a
// wait that holds a timer can never be reported. That is the whole mechanism. The wait itself is unchanged: it
// blocks until it is woken, or for ever, as the pin's does.
//
// # Why this timer
//
//   - **A function timer, not a channel timer.** Since Go 1.23 a channel timer is in a heap only while a
//     goroutine is blocked on its channel. An `AfterFunc` timer is in its P's heap from the moment it is armed,
//     whatever the waiter selects on.
//   - **The longest duration there is.** `math.MaxInt64` nanoseconds is about 292 years, so the timer never
//     fires and its function never runs. It starts no goroutine, so the goroutine census and the goroutine
//     checks (`requireNoSocketGoroutines` and its siblings) have nothing to see.
//   - **One per wait, held by the waiting goroutine.** The caller stops it when its wait ends, however the wait
//     ends, including by `Close`. No state is shared between waits, so there is nothing to count or lock.
//
// # What a stopped timer leaves behind
//
// Stopping a timer does not take it out of its heap at once. The runtime removes it when the P whose heap holds
// it next checks its timers, and until then `checkdead` still counts it. A waiter woken on another P leaves the
// stopped timer on the P that armed it, and a P that schedules nothing more never removes it. Measured with a
// probe at GOMAXPROCS=4, 3 of 3: a timer armed like this one and stopped from a second goroutine kept a process
// whose every goroutine then blocked alive past a 5 s bound. At GOMAXPROCS=1 the same process died at once.
//
// So after a wait ends, a process that later blocks every goroutine *outside* any guest wait (an embedder's own
// deadlock, or an engine defect) can hang where it would have died with the fatal. Go's crypto/rand already opens
// this window, for a minute, on its first read in a process, which is what delayed the fatal to 60 s on #1303.
// The window here has no end. ADR 0128 records the bounded shape that was not taken, and its costs.
//
// # Where it is held
//
// In [CanonCaller.Blocking], which every canon function's wait runs inside (contract §5's H-1, checked by
// `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion`), and in the infinite arm of [memory.wait]. ADR 0128
// lists the sites behind the first.
//
// NEUTER: return a stopped timer here, and every hang arm of TestAnUnwakeableWaitHangsThroughBurroughsRun and of
// TestAnUnwakeableWaitHangsThroughTheLibrary ends the host with Go's fatal as soon as it reaches the wait: exit 2,
// stderr "fatal error: all goroutines are asleep - deadlock!". The children run with GOMAXPROCS=1, so the fatal
// is not deferred.
func holdHostAwake() *time.Timer {
	return time.AfterFunc(math.MaxInt64, func() {})
}
