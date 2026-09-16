// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

type streamWriteFixture struct {
	Name     string `json:"name"`
	Outcome  string `json:"outcome"`
	N        int    `json:"n"`
	M        *int   `json:"m"`
	WriteRet []int  `json:"write_ret"`
	Wi       int    `json:"wi"`
	Packed   int    `json:"packed"`
	Result   int    `json:"result"`
	Progress int    `json:"progress"`
	WiState  string `json:"wi_state"`
}

func loadStreamWrites(t *testing.T) []streamWriteFixture {
	t.Helper()
	var doc struct {
		StreamWrites []streamWriteFixture `json:"stream_writes"`
	}
	loadFixtures(t, &doc)
	if len(doc.StreamWrites) == 0 {
		t.Fatal("no stream_writes in fixtures.json")
	}
	return doc.StreamWrites
}

// TestStreamWriteDeliversTheOracleOutcomes pins stream.write against the committed oracle (stream_writes)
// for all three outcomes WITH the two things stream adds over future.read: progress packing and the
// IDLE-on-COMPLETED end state. The async write returns BLOCKED and registers the pending write; the host
// consumer takes some elements (COMPLETED, end IDLE — open for more) or the readable end drops (DROPPED,
// end DONE); the SAME waitable-set loop delivers (STREAM_WRITE, index, result|progress<<4). A codec that
// drops the progress field passes the full case and fails the partial one; a Go that inherits future's
// DONE-on-COMPLETED fails the end-state check.
func TestStreamWriteDeliversTheOracleOutcomes(t *testing.T) {
	for _, fx := range loadStreamWrites(t) {
		t.Run(fx.Name, func(t *testing.T) {
			h := newAsyncHandles()
			se := &writableStreamEnd{}
			se.index = h.addLocked(se)

			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			const ptr = 32
			writeRet, err := streamWrite(h)(cc, []interp.Value{interp.I32(int32(se.index)), interp.I32(ptr), interp.I32(int32(fx.N))})
			if err != nil {
				t.Fatalf("stream.write: %v", err)
			}
			if uint32(writeRet[0].Int32()) != uint32(fx.WriteRet[0]) {
				t.Errorf("stream.write = %#x, want %#x (BLOCKED)", uint32(writeRet[0].Int32()), uint32(fx.WriteRet[0]))
			}

			siVals, err := waitableSetNew(h)(cc, nil)
			if err != nil {
				t.Fatalf("waitable-set.new: %v", err)
			}
			si := siVals[0].Int32()
			if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(se.index)), interp.I32(si)}); jerr != nil {
				t.Fatalf("waitable.join: %v", jerr)
			}

			// The host consumer takes fx.Progress elements (COMPLETED) or the readable end drops (DROPPED).
			result := copyCompleted
			if fx.Outcome == "dropped" {
				result = copyDropped
			}
			h.mu.Lock()
			se.completeWriteLocked(uint32(fx.Progress), result)
			h.mu.Unlock()

			const evptr = 48
			codeVals, werr := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(evptr)})
			if werr != nil {
				t.Fatalf("waitable-set.wait: %v", werr)
			}
			if codeVals[0].Int32() != int32(eventStreamWrite) {
				t.Errorf("event code = %d, want %d (STREAM_WRITE)", codeVals[0].Int32(), eventStreamWrite)
			}
			buf, rerr := cc.Read(evptr, 8)
			if rerr != nil {
				t.Fatalf("reading event: %v", rerr)
			}
			p2 := binary.LittleEndian.Uint32(buf[4:8])
			// The packed payload carries progress in the high bits — the hazard the partial case exercises.
			wantPacked := uint32(fx.Result) | (uint32(fx.Progress) << 4)
			if p2 != wantPacked || int(p2) != fx.Packed {
				t.Errorf("event packed = %d (result %d, progress %d), want %d (fixture)", p2, p2&0xf, p2>>4, fx.Packed)
			}
			if got := se.endStateName(); got != fx.WiState {
				t.Errorf("end state = %s, want %s — a stream COMPLETED stays IDLE, only DROPPED is DONE (not future's rule)", got, fx.WiState)
			}
		})
	}
}

type streamHostFirstFixture struct {
	Name     string `json:"name"`
	ReadN    int    `json:"read_n"`
	WriteN   int    `json:"write_n"`
	ReadRet  []int  `json:"read_ret"`
	WriteRet []int  `json:"write_ret"`
	Packed   int    `json:"packed"`
	Result   int    `json:"result"`
	Progress int    `json:"progress"`
	WiState  string `json:"wi_state"`
	RiState  string `json:"ri_state"`
	ReadGot  []int  `json:"read_got"`
}

// TestStreamHostFirstCompletesInline pins the host-arrives-first ordering — the real p3async-hello flow,
// where the host reads the readable end BEFORE the guest issues its stream.write on the writable end. Here
// the guest's write is the second arriver: it drives the copy and completes INLINE, returning the packed
// payload result|(progress<<4) DIRECTLY rather than BLOCKED, and arming no waitable event for the write.
// This is the mirror image of TestStreamWriteDeliversTheOracleOutcomes (guest-first, write→BLOCKED→event).
// The under-read (host 2 / guest 4) and over-read (host 4 / guest 2) rows together prove progress is
// min(read_n, write_n) — neither "the pending side's remaining" nor "the arriving side's". The pending read
// end stays COPYING (its event armed but not consumed); the write end goes IDLE (its event consumed inline).
func TestStreamHostFirstCompletesInline(t *testing.T) {
	var doc struct {
		StreamHostFirst []streamHostFirstFixture `json:"stream_hostfirst"`
	}
	loadFixtures(t, &doc)
	if len(doc.StreamHostFirst) == 0 {
		t.Fatal("no stream_hostfirst in fixtures.json")
	}
	for _, fx := range doc.StreamHostFirst {
		t.Run(fx.Name, func(t *testing.T) {
			h := newAsyncHandles()
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}

			// stream.new mints a connected (ri, wi) pair over one shared stream.
			newRet, err := streamNew(h)(cc, nil)
			if err != nil {
				t.Fatalf("stream.new: %v", err)
			}
			packed := uint64(newRet[0].Int64())
			ri, wi := uint32(packed&0xffffffff), uint32(packed>>32)
			re, ok := handleAt[*readableStreamEnd](h, ri)
			if !ok {
				t.Fatalf("stream.new did not mint a readable end at %d", ri)
			}

			// Seed the guest's write buffer with "AB…" at dstPtr+read_n, disjoint from the read buffer.
			const dstPtr = 32
			srcPtr := uint32(dstPtr + fx.ReadN + 8)
			src := make([]byte, fx.WriteN)
			for k := range src {
				src[k] = byte(65 + k)
			}
			if werr := cc.Write(uint64(srcPtr), src); werr != nil {
				t.Fatalf("seeding src: %v", werr)
			}

			// Host reads FIRST — it parks (no writer yet). completed must be false: the host-first case.
			h.mu.Lock()
			completed, rerr := re.hostReadLocked(cc, dstPtr, uint32(fx.ReadN))
			h.mu.Unlock()
			if rerr != nil {
				t.Fatalf("host read: %v", rerr)
			}
			if completed {
				t.Fatal("host read completed inline, want parked (host-first: no writer yet)")
			}

			// Guest writes SECOND — it drives the copy and completes INLINE, returning the packed payload.
			writeRet, err := streamWrite(h)(cc, []interp.Value{interp.I32(int32(wi)), interp.I32(int32(srcPtr)), interp.I32(int32(fx.WriteN))})
			if err != nil {
				t.Fatalf("stream.write: %v", err)
			}
			gotPacked := uint32(writeRet[0].Int32())
			if gotPacked == asyncBlocked {
				t.Fatalf("stream.write returned BLOCKED, want inline payload %#x — host-first must complete inline", fx.Packed)
			}
			wantPacked := uint32(fx.Result) | (uint32(fx.Progress) << 4)
			if gotPacked != wantPacked || int(gotPacked) != fx.Packed {
				t.Errorf("inline write payload = %d (result %d, progress %d), want %d (fixture)", gotPacked, gotPacked&0xf, gotPacked>>4, fx.Packed)
			}
			if int(gotPacked>>4) != fx.Progress {
				t.Errorf("progress = %d, want %d — min(read_n=%d, write_n=%d)", gotPacked>>4, fx.Progress, fx.ReadN, fx.WriteN)
			}

			// End states: the write end consumed its event inline (IDLE); the read end's event is armed but
			// not yet consumed (stays COPYING).
			we, _ := handleAt[*writableStreamEnd](h, wi)
			if got := we.endStateName(); got != fx.WiState {
				t.Errorf("wi state = %s, want %s (COMPLETED write goes IDLE, consumed inline)", got, fx.WiState)
			}
			if got := re.endStateName(); got != fx.RiState {
				t.Errorf("ri state = %s, want %s (armed event untaken stays COPYING)", got, fx.RiState)
			}
			// The read end's event must be ARMED (the host consumer that issued the read learns the copy
			// completed) — with the SAME progress — even though it stays COPYING here (unconsumed). Without
			// this, the write completes but the reader is never notified: the host never gets its bytes.
			if !re.resolved {
				t.Error("read end not armed after the write drove the copy — the host reader would never learn it completed")
			}
			if re.progress != uint32(fx.Progress) {
				t.Errorf("read end progress = %d, want %d (the shared copy count)", re.progress, fx.Progress)
			}

			// The bytes that landed in the host's read buffer — progress elements, the rest untouched.
			landed, lerr := cc.Read(dstPtr, uint64(fx.ReadN))
			if lerr != nil {
				t.Fatalf("reading landed bytes: %v", lerr)
			}
			for k, want := range fx.ReadGot {
				if int(landed[k]) != want {
					t.Errorf("read buffer[%d] = %d, want %d", k, landed[k], want)
				}
			}
		})
	}
}

type streamDropsFixture struct {
	TrapMatrix struct {
		StreamReadable         map[string]string `json:"stream_readable"`
		StreamWritable         map[string]string `json:"stream_writable"`
		FutureWritableContrast map[string]string `json:"future_writable_contrast"`
	} `json:"trap_matrix"`
	Notify struct {
		ReadRet             []int  `json:"read_ret"`
		DropWiRet           []int  `json:"drop_wi_ret"`
		EventCode           int    `json:"event_code"`
		Result              int    `json:"result"`
		Progress            int    `json:"progress"`
		RiStateAfterConsume string `json:"ri_state_after_consume"`
	} `json:"notify"`
}

// dropOutcome runs a drop CanonFunc on a handle already in the table and reports "trap" or "ok" — the
// end-state audit's verdict, compared to the oracle's trap matrix.
func dropOutcome(t *testing.T, fn interp.CanonFunc, cc *interp.CanonCaller, idx int) string {
	t.Helper()
	_, err := fn(cc, []interp.Value{interp.I32(int32(idx))})
	var tr *interp.Trap
	if errors.As(err, &tr) {
		return "trap"
	}
	if err != nil {
		t.Fatalf("drop returned a non-trap error: %v", err)
	}
	return "ok"
}

// TestStreamDropsAuditEndStates pins drop-readable (0x13) and drop-writable (0x14) against the committed
// oracle (stream_drops). Two things are audited. (1) The trap guard: a stream end drops from IDLE or DONE
// and TRAPS mid-copy (COPYING) — you cannot drop an end with an in-flight or armed-but-unconsumed copy, so
// a drop before the host consumer takes the read event traps rather than silently discarding a completed
// copy. (2) The anti-hang notification: dropping the idle writable end while a read is pending wakes the
// reader with DROPPED (progress 0), transitioning it to DONE — not leaving it waiting forever, which is the
// failure the host-first inline flow could otherwise produce.
func TestStreamDropsAuditEndStates(t *testing.T) {
	var doc struct {
		StreamDrops streamDropsFixture `json:"stream_drops"`
	}
	loadFixtures(t, &doc)
	fx := doc.StreamDrops
	if len(fx.TrapMatrix.StreamReadable) == 0 {
		t.Fatal("no stream_drops trap matrix in fixtures.json")
	}

	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}

	// (1) Trap guard, readable end: construct an end in each state, drop it, compare to the oracle.
	readableStates := map[string]copyState{"IDLE": copyStateIdle, "COPYING": copyStateCopying, "DONE": copyStateDone}
	for state, want := range fx.TrapMatrix.StreamReadable {
		h := newAsyncHandles()
		re := &readableStreamEnd{state: readableStates[state]}
		re.index = h.addLocked(re)
		if got := dropOutcome(t, streamDropReadable(h), cc, re.index); got != want {
			t.Errorf("drop-readable in %s = %s, want %s (oracle trap matrix)", state, got, want)
		}
	}

	// (1) Trap guard, writable end: the model's COPYING is a pending unresolved write; IDLE is fresh or a
	// resolved-open write; DONE is a dropped end. The oracle says IDLE/DONE ok, COPYING traps.
	writableStates := map[string]*writableStreamEnd{
		"IDLE":    {},
		"COPYING": {hasWrite: true},
		"DONE":    {done: true},
	}
	for state, want := range fx.TrapMatrix.StreamWritable {
		h := newAsyncHandles()
		we := writableStates[state]
		we.index = h.addLocked(we)
		if got := dropOutcome(t, streamDropWritable(h), cc, we.index); got != want {
			t.Errorf("drop-writable in %s = %s, want %s (oracle trap matrix)", state, got, want)
		}
	}

	// (2) Anti-hang notification: stream.new -> join ri -> host read (pends) -> drop wi -> the reader's
	// STREAM_READ event fires DROPPED (progress 0) and the end goes DONE.
	h := newAsyncHandles()
	newRet, err := streamNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("stream.new: %v", err)
	}
	packed := uint64(newRet[0].Int64())
	ri, wi := uint32(packed&0xffffffff), uint32(packed>>32)
	re, _ := handleAt[*readableStreamEnd](h, ri)

	siVals, err := waitableSetNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("waitable-set.new: %v", err)
	}
	si := siVals[0].Int32()
	if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(ri)), interp.I32(si)}); jerr != nil {
		t.Fatalf("waitable.join: %v", jerr)
	}

	h.mu.Lock()
	completed, rerr := re.hostReadLocked(cc, 32, 4) // host reads first -> parks
	h.mu.Unlock()
	if rerr != nil || completed {
		t.Fatalf("host read: err=%v completed=%v, want parked", rerr, completed)
	}

	dropRet, derr := streamDropWritable(h)(cc, []interp.Value{interp.I32(int32(wi))})
	if derr != nil {
		t.Fatalf("drop-writable of an idle writer trapped, want ok: %v", derr)
	}
	if len(dropRet) != len(fx.Notify.DropWiRet) {
		t.Errorf("drop-writable return arity = %d, want %d", len(dropRet), len(fx.Notify.DropWiRet))
	}

	const evptr = 48
	codeVals, werr := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(evptr)})
	if werr != nil {
		t.Fatalf("waitable-set.wait: %v", werr)
	}
	if int(codeVals[0].Int32()) != fx.Notify.EventCode {
		t.Errorf("event code = %d, want %d (STREAM_READ)", codeVals[0].Int32(), fx.Notify.EventCode)
	}
	buf, brerr := cc.Read(evptr, 8)
	if brerr != nil {
		t.Fatalf("reading event: %v", brerr)
	}
	p2 := binary.LittleEndian.Uint32(buf[4:8])
	if int(p2&0xf) != fx.Notify.Result || int(p2>>4) != fx.Notify.Progress {
		t.Errorf("notified event = result %d progress %d, want result %d progress %d (DROPPED, nothing copied)", p2&0xf, p2>>4, fx.Notify.Result, fx.Notify.Progress)
	}
	if got := re.endStateName(); got != fx.Notify.RiStateAfterConsume {
		t.Errorf("reader end state after consuming DROPPED = %s, want %s (a dropped read is terminal)", got, fx.Notify.RiStateAfterConsume)
	}
}

type streamCancelReadFixture struct {
	ReadRet      []int  `json:"read_ret"`
	CancelRet    []int  `json:"cancel_ret"`
	Result       int    `json:"result"`
	Progress     int    `json:"progress"`
	RiStateAfter string `json:"ri_state_after"`
}

// TestStreamCancelReadProducesCancelled pins stream.cancel-read (0x11) against the committed oracle, and is
// the FIRST assertion that CANCELLED is produced by a RUNNING path. CANCELLED has been pinned in the codec
// since the future oracle (#752), but only ever injected synthetically — the future test hand-calls
// completeLocked(copyCancelled) because future.cancel-read is refused by name. Here a pending read is
// cancelled through the actual cancel op: it delivers result=CANCELLED, progress 0, INLINE (not BLOCKED),
// and the end returns to IDLE (a cancelled read is open, not DONE — only DROPPED is DONE). The explicit
// cross-check is Scott's: the value this running path delivers must equal the encoding #752 asserted (the
// future_reads CANCELLED payload) and the codec's own copyCancelled constant — not merely pass its own row.
func TestStreamCancelReadProducesCancelled(t *testing.T) {
	var doc struct {
		StreamCancelRead streamCancelReadFixture `json:"stream_cancel_read"`
		FutureReads      []struct {
			Name    string `json:"name"`
			Payload *int   `json:"payload"`
		} `json:"future_reads"`
	}
	loadFixtures(t, &doc)
	fx := doc.StreamCancelRead
	if len(fx.CancelRet) == 0 {
		t.Fatal("no stream_cancel_read in fixtures.json")
	}

	h := newAsyncHandles()
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	newRet, err := streamNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("stream.new: %v", err)
	}
	ri := uint32(uint64(newRet[0].Int64()) & 0xffffffff)
	re, _ := handleAt[*readableStreamEnd](h, ri)

	h.mu.Lock()
	completed, rerr := re.hostReadLocked(cc, 32, 4) // host reads first -> parks (COPYING)
	h.mu.Unlock()
	if rerr != nil || completed {
		t.Fatalf("host read: err=%v completed=%v, want parked", rerr, completed)
	}

	cancelRet, cerr := streamCancelRead(h)(cc, []interp.Value{interp.I32(int32(ri))})
	if cerr != nil {
		t.Fatalf("stream.cancel-read: %v", cerr)
	}
	got := uint32(cancelRet[0].Int32())
	if got == asyncBlocked {
		t.Fatal("stream.cancel-read returned BLOCKED, want the CANCELLED payload inline")
	}
	if int(got) != fx.CancelRet[0] {
		t.Errorf("cancel-read payload = %d, want %d (fixture)", got, fx.CancelRet[0])
	}
	if int(got&0xf) != fx.Result || int(got>>4) != fx.Progress {
		t.Errorf("cancel-read = result %d progress %d, want result %d progress %d (CANCELLED, nothing copied)", got&0xf, got>>4, fx.Result, fx.Progress)
	}
	if got := re.endStateName(); got != fx.RiStateAfter {
		t.Errorf("end state after cancel = %s, want %s (CANCELLED leaves the end IDLE, not DONE)", got, fx.RiStateAfter)
	}

	// Scott's explicit cross-check: this RUNNING production of CANCELLED equals the codec constant AND the
	// encoding #752 pinned synthetically (the future_reads CANCELLED payload) — the running path delivers
	// what the fixture asserted, not a value only this test happens to agree with.
	if got&0xf != uint32(copyCancelled) {
		t.Errorf("running CANCELLED = %d, want copyCancelled constant %d", got&0xf, copyCancelled)
	}
	var futurePin *int
	for _, r := range doc.FutureReads {
		if r.Name == "future-read-u32-cancelled" {
			futurePin = r.Payload
		}
	}
	if futurePin == nil {
		t.Fatal("future_reads CANCELLED row (#752 pin) not found — the cross-check has no reference")
	}
	if int(got&0xf) != *futurePin {
		t.Errorf("running CANCELLED encoding = %d, but #752 pinned %d synthetically — the running path diverged from the fixture", got&0xf, *futurePin)
	}
}

type streamCancelWriteFixture struct {
	WriteRet     []int  `json:"write_ret"`
	CancelRet    []int  `json:"cancel_ret"`
	Result       int    `json:"result"`
	Progress     int    `json:"progress"`
	WiStateAfter string `json:"wi_state_after"`
}

// TestStreamCancelWriteProducesCancelled pins stream.cancel-write (0x12) — cancel-read's symmetric twin on
// the writable end — against the committed oracle. A guest-first write parks (BLOCKED, COPYING); cancelling
// it delivers CANCELLED with progress 0 INLINE (not BLOCKED), and the end returns to IDLE (only DROPPED is
// DONE). Same CANCELLED encoding as cancel-read, on a STREAM_WRITE-coded event.
func TestStreamCancelWriteProducesCancelled(t *testing.T) {
	var doc struct {
		StreamCancelWrite streamCancelWriteFixture `json:"stream_cancel_write"`
	}
	loadFixtures(t, &doc)
	fx := doc.StreamCancelWrite
	if len(fx.CancelRet) == 0 {
		t.Fatal("no stream_cancel_write in fixtures.json")
	}

	h := newAsyncHandles()
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	newRet, err := streamNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("stream.new: %v", err)
	}
	wi := uint32(uint64(newRet[0].Int64()) >> 32)

	// A guest-first write parks (no reader yet) -> COPYING.
	const srcPtr = 32
	writeRet, werr := streamWrite(h)(cc, []interp.Value{interp.I32(int32(wi)), interp.I32(srcPtr), interp.I32(4)})
	if werr != nil {
		t.Fatalf("stream.write: %v", werr)
	}
	if uint32(writeRet[0].Int32()) != uint32(fx.WriteRet[0]) {
		t.Errorf("write = %#x, want %#x (BLOCKED)", uint32(writeRet[0].Int32()), uint32(fx.WriteRet[0]))
	}

	cancelRet, cerr := streamCancelWrite(h)(cc, []interp.Value{interp.I32(int32(wi))})
	if cerr != nil {
		t.Fatalf("stream.cancel-write: %v", cerr)
	}
	got := uint32(cancelRet[0].Int32())
	if got == asyncBlocked {
		t.Fatal("stream.cancel-write returned BLOCKED, want the CANCELLED payload inline")
	}
	if int(got) != fx.CancelRet[0] || int(got&0xf) != fx.Result || int(got>>4) != fx.Progress {
		t.Errorf("cancel-write = %d (result %d progress %d), want %d (result %d progress %d)", got, got&0xf, got>>4, fx.CancelRet[0], fx.Result, fx.Progress)
	}
	if got&0xf != uint32(copyCancelled) {
		t.Errorf("cancel-write result = %d, want copyCancelled constant %d", got&0xf, copyCancelled)
	}
	we, _ := handleAt[*writableStreamEnd](h, wi)
	if s := we.endStateName(); s != fx.WiStateAfter {
		t.Errorf("end state after cancel = %s, want %s (CANCELLED leaves the end IDLE)", s, fx.WiStateAfter)
	}
}
