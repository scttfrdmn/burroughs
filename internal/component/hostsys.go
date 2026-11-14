// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's synchronous system interfaces (#1232, ADR 0119): `wasi:random`, the two clocks' `now` and
// `get-resolution`, and the rest of `wasi:cli` a `wasm32-wasip3` std guest reaches on startup. Every row is
// registered for 0.3 only (ADR 0115). The monotonic clock's async `wait-for` and `wait-until` are served here
// too, as the production async imports (asyncSource, #1233, ADR 0121), and stdin's `read-via-stream` is E6's.

// randomBytesCap bounds one `get-random-bytes` or `get-insecure-random-bytes` request (ADR 0119). The WIT
// lets a host return fewer than `max-len` bytes, and this is the figure wasmtime 49.0.3 returns for every
// request at or above it, measured: `max-len` of 2^26-1 gets 2^26-1 bytes, and 2^26, 2^26+1, 2^30 and
// u64::MAX each get 2^26. So a guest's `max-len` reads the same here as there. The host holds one
// canon.StringChunk of it at a time (canon.StoreListU8); the rest is the guest's own memory, allocated by its
// realloc.
const randomBytesCap = 1 << 26

// clockResolution is both clocks' `get-resolution`, in nanoseconds: preview 1's figure (clockResGet), and the
// unit Go's time API reads in. wasmtime 49.0.3 answers 1000 on darwin/arm64, which is the host's
// clock_getres; a pure-Go engine cannot ask that without cgo (ADR 0119).
const clockResolution = 1

// newInsecureSeed draws `get-insecure-seed`'s two words once per Host, since the WIT means the seed to be
// stable for an instance's lifetime.
//
// The error is discarded, and that is named rather than overlooked: since Go 1.24 crypto/rand.Read never
// returns one. It crashes the program instead, so no error value can reach this line.
func newInsecureSeed() [2]uint64 {
	var b [16]byte
	_, _ = crand.Read(b[:])
	return [2]uint64{binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])}
}

// randomBytes marshals `get-random-bytes` and `get-insecure-random-bytes: func(max-len: u64) -> list<u8>`,
// flattened to (max-len, ret). It returns min(max-len, randomBytesCap) bytes, so 0 asks for and gets none,
// and any other request gets at least one, as the WIT requires. Both read crypto/rand: the WIT permits the
// insecure source to be a secure one, and one source is simpler than two.
func (h *Host) randomBytes(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: get-random-bytes: got %d core args, want 2 (max-len, ret)", len(args))
	}
	// NEUTER THIS (n := uint64(0)), and TestP3SysMatchesWasmtime's length lines read 0 where wasmtime's read
	// 16 and 2^26.
	n := min(args[0].Bits, randomBytesCap)
	fill := func(b []byte) error {
		if _, err := crand.Read(b); err != nil {
			return fmt.Errorf("component: get-random-bytes: %w", err)
		}
		return nil
	}
	return nil, canon.StoreListU8(guestHeap{c}, int(n), fill, int(uint32(args[1].Bits)))
}

// randomU64 marshals `get-random-u64` and `get-insecure-random-u64: func() -> u64`, one flat i64.
func (h *Host) randomU64(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("component: get-random-u64: got %d core args, want 0", len(args))
	}
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("component: get-random-u64: %w", err)
	}
	return []interp.Value{interp.I64(int64(binary.LittleEndian.Uint64(b[:])))}, nil
}

// insecureSeed marshals `get-insecure-seed: func() -> tuple<u64, u64>`, two flat results past the one a
// lower returns directly, so the guest passes a return pointer (ret). The pair is the Host's, drawn in
// NewHost.
func (h *Host) insecureSeed(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: get-insecure-seed: got %d core args, want 1 (ret)", len(args))
	}
	u64 := canon.Type{Kind: canon.KindU64}
	v, err := canon.Record(canon.TupleType(u64, u64), map[string]canon.Value{
		"0": canon.U64(h.seed[0]), "1": canon.U64(h.seed[1]),
	})
	if err != nil {
		return nil, fmt.Errorf("component: get-insecure-seed: %w", err)
	}
	return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
}

// monotonicNow marshals `wasi:clocks/monotonic-clock.now: func() -> mark`, the nanoseconds since the Host was
// built. h.start carries Go's monotonic reading, so a wall-clock step does not move it. NewHost sets it:
// a zero start is preview 1's #815, where time.Since saturates and the clock stands still at 2^63-1.
func (h *Host) monotonicNow(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("component: monotonic-clock.now: got %d core args, want 0", len(args))
	}
	return []interp.Value{interp.I64(int64(h.mark()))}, nil
}

// mark is the monotonic clock's reading, a `mark`: the one clock `now` reads and the waits measure against.
func (h *Host) mark() uint64 { return uint64(time.Since(h.start).Nanoseconds()) }

// asyncSource is the production async imports (#1233, ADR 0121), WASI 0.3's two waits and tcp-socket's
// connect (ADR 0123), registered for 0.3 only. Each is a factory the walk calls with the importing instance's
// async and resource tables, which hold the work while it is pending, so that instance's Close stops it.
func (h *Host) asyncSource() map[string]asyncImplFor {
	m := map[string]asyncImplFor{}
	serve(m, wasiP3, "wasi:clocks/monotonic-clock::wait-for", h.waitFor)
	serve(m, wasiP3, "wasi:clocks/monotonic-clock::wait-until", h.waitUntil)
	// wasi:sockets' one async tcp method (ADR 0123, hostsock.go): a method, so its factory lends self from the
	// instance's resource table.
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.connect", h.tcpConnect)
	return m
}

// waitFor implements `wait-for: async func(how-long: duration)`: its deadline is now plus how-long on the
// monotonic clock, saturating at the largest mark.
func (h *Host) waitFor(async *asyncHandles, _ *hostResources) asyncLowerImpl {
	return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 1 {
			return nil, fmt.Errorf("component: wait-for: got %d core args, want 1 (how-long)", len(args))
		}
		now := h.mark()
		deadline := now + args[0].Bits
		if deadline < now {
			deadline = math.MaxUint64
		}
		return h.waitUntilMark(async, deadline, now, onResolve), nil
	}
}

// waitUntil implements `wait-until: async func(when: mark)`: `when` is a mark on the clock `now` reads.
func (h *Host) waitUntil(async *asyncHandles, _ *hostResources) asyncLowerImpl {
	return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 1 {
			return nil, fmt.Errorf("component: wait-until: got %d core args, want 1 (when)", len(args))
		}
		// NEUTER: read `when` against the wrong clock, as uint64(time.Now().UnixNano()) for h.mark(), and
		// TestP3SleepMatchesWasmtime's sleep_until line reads false: the deadline reads as long past.
		return h.waitUntilMark(async, args[0].Bits, h.mark(), onResolve), nil
	}
}

// waitUntilMark resolves at `deadline`, a mark, read against `now`. **A deadline at or before now resolves
// inline**, so the lower returns RETURNED (2) with no subtask registered, as wasmtime 49.0.3's does for
// `wait-for(0)`, `wait-until(0)` and `wait-until(now())` (wait-cancel.reading). Otherwise a host wait is
// started, and its onCancel is the subtask's.
//
// NEUTER: resolve inline whatever the deadline, and TestP3SleepMatchesWasmtime's sleep and sleep_until lines
// both read false.
func (h *Host) waitUntilMark(async *asyncHandles, deadline, now uint64, onResolve func(canon.Value)) func() {
	if deadline <= now {
		onResolve(canon.Value{})
		return nil
	}
	d := deadline - now
	if d > math.MaxInt64 {
		d = math.MaxInt64 // a timer's duration is an int64: 292 years, which no guest outlives
	}
	return async.startHostWait(time.Duration(d), onResolve)
}

// clockGetResolution marshals both clocks' `get-resolution: func() -> duration`, one flat i64.
func (h *Host) clockGetResolution(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("component: get-resolution: got %d core args, want 0", len(args))
	}
	return []interp.Value{interp.I64(clockResolution)}, nil
}

// systemInstantType is `wasi:clocks/system-clock`'s `instant`: `record { seconds: s64, nanoseconds: u32 }`.
func systemInstantType() canon.Type {
	return canon.RecordType(
		canon.Field{Name: "seconds", Type: canon.Type{Kind: canon.KindS64}},
		canon.Field{Name: "nanoseconds", Type: canon.Type{Kind: canon.KindU32}},
	)
}

// systemNow marshals `wasi:clocks/system-clock.now: func() -> instant`, a 16-byte record at the return
// pointer. time.Time's Unix and Nanosecond split an instant as the WIT does: seconds floored, and
// nanoseconds in [0, 1e9) even before the epoch.
func (h *Host) systemNow(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: system-clock.now: got %d core args, want 1 (ret)", len(args))
	}
	now := time.Now()
	v, err := canon.Record(systemInstantType(), map[string]canon.Value{
		"seconds":     canon.S64(now.Unix()),
		"nanoseconds": canon.U32(uint32(now.Nanosecond())),
	})
	if err != nil {
		return nil, fmt.Errorf("component: system-clock.now: %w", err)
	}
	return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
}

// noneOf marshals a `() -> option<T>` import whose answer is always none (`get-initial-cwd`, the three
// `get-terminal-*`), lowered through the codec at the return pointer, its sole argument. An option of a
// string or an own flattens to two core values, so it always returns through memory.
func noneOf(what string, elem canon.Type) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: %s: got %d core args, want 1 (ret)", what, len(args))
		}
		v, err := canon.None(canon.OptionType(elem))
		if err != nil {
			return nil, fmt.Errorf("component: %s: %w", what, err)
		}
		return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
	}
}

// exitWithCode marshals `wasi:cli/exit.exit-with-code: func(status-code: u8)`. It records the code the way
// `exit` records its status, and ends the run with it as `exit` does (exitError, ADR 0120). This returned
// nil until #1262, so the guest ran on past a call it treats as one that does not return.
func (h *Host) exitWithCode(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: exit-with-code: got %d core args, want 1 (status-code)", len(args))
	}
	h.Exited = true
	h.ExitCode = int32(uint8(args[0].Bits)) // lift_flat of a u8 keeps the low 8 bits
	// NEUTER: return nil, and TestP3StdMatchesWasmtime traps at the `unreachable` std puts after the call, and
	// TestP3SysMatchesWasmtime's run exits 0.
	return nil, exitError{code: h.ExitCode}
}
