// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"sync"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The store lockdown (#1040; ADR 0105, superseding ADR 0100's no-poison section).
//
// At the pin, a trap locks the whole store before it is raised (def:trap:"current_instance().store.lock_down()"),
// and the embedding API refuses every later call (def:Store.invoke:"assert(not self.lockdown)"). Nothing else in
// the store runs: the only scheduler entry makes the same assertion (def:Store.tick:"assert(not self.lockdown)").
// wasmtime 49.0.3 agrees, refusing a second call with "cannot enter component instance". Burroughs used to run it.
//
// **The store is one `Instantiated`**: the root walker and every instance it composes share one lock, installed
// by `shareLock` after the walk. The first trap anywhere in it sets the lock. From then on:
//   - every embedder entry refuses with `ErrInstanceLocked`, wrapping the trap;
//   - a call already in flight ends with the same error, at its next canon built-in or suspension point, or
//     when it returns.
//
// The pin leaves those in-flight calls unresolved: they neither return nor trap. A host goroutine waiting on
// one would wait forever, so Burroughs returns them, with the error that says why. That is the reference's
// outcome made observable, not a different one.
//
// **Core wasm is untouched.** An `interp.Instance` keeps the core spec's semantics, where an instance stays
// callable after a trap; the spec suite's `assert_trap` followed by further use depends on it.

// ErrInstanceLocked reports a call refused, or ended, because an earlier trap locked the component instance. The
// error carrying it also wraps that trap, so errors.As reaches it.
var ErrInstanceLocked = errors.New("component instance is locked: an earlier trap ended it, and the reference " +
	"refuses every later call")

// lockedError is ErrInstanceLocked joined with the trap that set the lock.
type lockedError struct{ trap error }

func (e *lockedError) Error() string   { return ErrInstanceLocked.Error() + ": " + e.trap.Error() }
func (e *lockedError) Unwrap() []error { return []error{ErrInstanceLocked, e.trap} }

// storeLock is one `Instantiated`'s lockdown state. The zero value is unlocked; `done` is made by newStoreLock.
type storeLock struct {
	mu   sync.Mutex
	err  error         // the trap that locked the store, or nil
	done chan struct{} // closed once, when err is set
}

func newStoreLock() *storeLock { return &storeLock{done: make(chan struct{})} }

// set locks the store with trap, if it is not locked already. The first trap wins.
//
// **It ends nothing itself.** In-flight work ends where it next suspends or calls a built-in, through its own
// unwind paths: the waits select on `wait()`, and the built-ins check `locked()`. A first draft also killed every
// parked Thread from here, on its own goroutine. That raced the owner routing, because a kill could reach a waiting
// owner before routeFault handed it its trap, and nothing needed it once the waits select on the lock. Threads
// parked with no call to end them are reclaimed by Close, as before.
func (l *storeLock) set(trap error) {
	if l == nil || trap == nil {
		return
	}
	l.mu.Lock()
	if l.err != nil {
		l.mu.Unlock()
		return
	}
	l.err = trap
	l.mu.Unlock()
	// **After the unlock** (contract §4 B-MM-3): the close is a resume of every wait selecting on it, and an
	// engine lock must not be held across one. Exactly one caller reaches here, because err was set under the lock.
	close(l.done)
}

// locked returns the error a refused or ended call reports, or nil while the store is unlocked.
func (l *storeLock) locked() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		return nil
	}
	return &lockedError{trap: l.err}
}

// trap returns the trap that locked the store, or nil.
func (l *storeLock) trap() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// wait returns the channel closed when the store locks; nil (blocking forever in a select) when there is no lock.
func (l *storeLock) wait() <-chan struct{} {
	if l == nil {
		return nil
	}
	return l.done
}

// isTrap reports whether err is a trap of the guest's, which locks the store: a core trap, a canon trap_if, or
// a host function that failed (a trap at the pin). Host-side refusals that ran no guest code are not: an unknown
// export, an unsupported form, an argument that does not lower, or a cancelled context.
func isTrap(err error) bool {
	var t *interp.Trap
	return errors.As(err, &t) || errors.Is(err, interp.ErrHostTrap)
}

// finish maps the outcome of an embedder call. A call's own trap sets the lock. A call that was in flight when
// the store locked (it started unlocked and ended locked) and FAILED reports ErrInstanceLocked, unless the trap
// that locked it is its own.
//
// **A call that started locked is passed through unmapped.** Refusing it is the entry check's job alone, so that
// removing the check is visible: mapping it here would relabel a call that had already run guest code as
// refused, and the entry check's neuter passed until this distinction was restored.
//
// **A call that succeeded is passed through too.** Its guest code finished, and it released the run slot before
// anything that locked the store could run. That is the reference's order: an orphan Thread readied by a
// `resume-later` runs on a later tick, after the readying call has resolved. The first draft also mapped a
// success, and on arm64 CI `TestATrapAfterItsOwnerReturnedIsKept` failed: `poke` resolved with 0, and its Thread
// trapped in the gap before `finish` ran. A call the lock actually ends has already failed, at a built-in or a
// wait, with the lock's error.
func (l *storeLock) finish(startedLocked bool, err error) error {
	if l == nil || startedLocked || err == nil {
		return err
	}
	if isTrap(err) {
		l.set(err)
	}
	t := l.trap()
	if t == nil {
		return err
	}
	if errors.Is(err, t) {
		return err // this call's own trap locked the store, and it reports that trap itself
	}
	return &lockedError{trap: t}
}

// shareLock installs l on w and every walker it composes, and on their Thread tables. Read at call time, so the
// guards bound during the walk see it.
func (w *walker) shareLock(l *storeLock) {
	w.lock = l
	if w.threads != nil {
		w.threads.lock = l
	}
	if w.async != nil {
		w.async.lock = l
	}
	for _, c := range w.children {
		c.shareLock(l)
	}
}

// lockGuard ends a canon built-in or lowered import with ErrInstanceLocked when the store is locked, before it
// runs and again after it returns. The second check is what ends a host call that was already in flight when
// another call trapped.
func (w *walker) lockGuard(fn interp.CanonFunc) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := w.lock.locked(); err != nil {
			return nil, err
		}
		res, err := fn(c, args)
		// **Only a success is replaced.** An error already ending the call passes through untouched, because
		// the unwind keys on it: replacing a killed wait's errThreadKilled sent the waiter down the wrong unwind,
		// and a run slot was released twice (`TestATrapWhileASiblingWaitsEndsTheCallAndTheWaiter` hung).
		if err == nil {
			if lerr := w.lock.locked(); lerr != nil {
				return nil, lerr
			}
		}
		return res, err
	}
}

// lowerExtern is interp.CanonLowerExtern with the lock guard applied, for every canon built-in and lowered
// import the walker binds.
func (w *walker) lowerExtern(ft bin.FuncType, fn interp.CanonFunc, opts interp.CanonOptions) interp.Extern {
	return interp.CanonLowerExtern(ft, w.lockGuard(fn), opts)
}
