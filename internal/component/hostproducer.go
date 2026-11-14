// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// A stream producer is the host writing a `stream<u8>` the guest reads (#1234, ADR 0122): WASI 0.3's stdin,
// `read-via-stream: func() -> tuple<stream<u8>, future<result<_, error-code>>>`, and E7's socket receive next.
// The guest holds the readable end and the future; the writable end is the HOST's, held here and never put in
// the guest's table (`writableStreamEnd.producer`).
//
// # Reads happen on demand
//
// A `Read` of the source starts only when a guest read is pending and the producer's buffer is empty, sized
// `min(readN, 64 KiB)`, and at most one is in flight per stream. So the host never consumes a byte of stdin no
// guest asked for: what the embedder's reader holds after the guest stops reading is what the guest left. The
// one exception is a read of 0 with the buffer empty, which can only be answered by a byte, so it starts a
// `Read` of the cap; what that returns stays in the buffer.
//
// **Nothing read is lost.** A `Read` returns at most what the pending read asked for, but the read can be
// cancelled while it is in flight, or replaced by a shorter one, so the copy takes `min(readN, len(buf))` and
// the rest stays in the buffer for the next read (`driveStreamCopyLocked`'s host arm).
//
// # The goroutine, and where its result lands
//
// A `Read` blocks and cannot be interrupted, so it runs on a goroutine of its own, the one site this ADR
// authorises (`startReadLocked`). Its result is guest-memory work, so it reaches the guest by ADR 0101's
// result-store discipline, the path an async lower's `onResolve` takes: queued in `hostStores` while the run
// slot's holder is in host code, and otherwise applied under the slot (`acquireRun`), on a chain of its own.
// Under the slot and `asyncHandles.mu` it appends to the buffer, copies into a pending read and arms it
// COMPLETED, and finishes the stream if the source has ended.
//
// # The end of the stream
//
// A `Read` that returns `io.EOF` makes the future's value `ok`, and any other error makes it `err(io)`. The
// stream FINISHES once its buffer is empty: the future resolves, then the writer drops (`dropWriterLocked`), so
// the reader sees DROPPED and wasi-libc's future read finds the value already there. Bytes that came with the
// end are delivered first, and the finish waits for the next read, so a COMPLETED the guest has not yet taken
// is not turned into DROPPED under it. A guest that drops the readable end stops the producer, and the future
// resolves `ok`, as `wasi:cli/stdin` states.
//
// # Close abandons a goroutine blocked in Read
//
// `walker.close` stops the producers (`stopProducers`): a flag under `asyncHandles.mu`, which every delivery
// checks under the lock it stores under, and a channel that aborts a delivery waiting for the run slot.
// **Nothing waits for a goroutine in `Read`.** It is abandoned, and whatever it reads after the stop is
// discarded. That is `CanonCaller.Blocking`'s H-3 limit, stated for streams: a source that never returns holds
// one goroutine, and no longer holds `Close`. The reader is never closed: the stdio objects are the process's.

// producerReadCap bounds one Read of a producer's source, as `blockingRead`'s cap bounds WASI 0.2's.
const producerReadCap = 1 << 16

// streamProducer is one host-produced stream: its source, the host-held writer, the guest's two ends, and the
// bytes read and not yet delivered. Every field but src and srcMu is guarded by h.mu, and the three hooks are
// set before the producer is shared and never written after.
type streamProducer struct {
	h      *asyncHandles
	src    io.Reader   // nil: an immediate end of input, with no goroutine
	srcMu  *sync.Mutex // the Host's lock over src, so two producers never Read it concurrently; nil for none
	writer *writableStreamEnd
	reader *readableStreamEnd
	future *readableFutureEnd
	ok     canon.Value // `result::ok` at the future's payload type
	buf    []byte      // read from src and not yet delivered
	// reading reports a Read in flight. final is set once src has ended: the future's value. done is set once
	// the stream has finished or its reader dropped, after which nothing is read or delivered.
	reading bool
	final   *canon.Value
	done    bool

	// The three hooks a socket's receive sets (ADR 0123). stdin sets none, and reads as it did under ADR 0122.
	//
	// mapErr is the future's value for a Read that failed other than at the end of input; nil is stdin's
	// `err(io)`. track is asked before each goroutine starts, and returns the call that marks it ended, or false
	// once the source's owner has stopped; nil starts it untracked. onDone runs once, under h.mu, when the
	// producer is done: its stream finished, or its reader dropped (readerDropped).
	mapErr func(error) canon.Value
	track  func() (ended func(), ok bool)
	onDone func(readerDropped bool)
}

// readViaStream is `wasi:cli/stdin@0.3::read-via-stream`'s bind (ADR 0122): sync-lowered with a return pointer,
// it mints the readable end and then the future, in the reference's lowering order
// (def:lower_async_value:"end.index = cx.inst.handles.add(end)"), and writes the two i32 handles, the stream's at
// +0 and the future's at +4, which is where the tuple's layout puts them and where wasi-libc reads them.
func (h *Host) readViaStream(async *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: read-via-stream: got %d core args, want 1 (ret)", len(args))
		}
		okv, err := stdioWriteResultOK()
		if err != nil {
			return nil, err
		}
		p := &streamProducer{h: async, src: h.Stdin, srcMu: &h.stdinReads, ok: okv}
		if h.Stdin == nil {
			p.final = &p.ok // no stdin is an immediate end of input
		}
		async.mu.Lock()
		ri, fi := p.mintLocked()
		async.mu.Unlock()
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[0:], uint32(ri))
		binary.LittleEndian.PutUint32(buf[4:], uint32(fi))
		if werr := c.Write(uint64(uint32(args[0].Bits)), buf[:]); werr != nil {
			return nil, fmt.Errorf("component: read-via-stream: writing the stream and future handles: %w", werr)
		}
		return nil, nil
	}
}

// mintLocked makes the stream's ends and its future, and adds the guest's two to the table: the readable end,
// then the future. The caller holds p.h.mu. Both ends carry byteElem (ADR 0124): the host writes bytes, and the
// guest's declared stream<u8> was checked at instantiate (checkConsumerPayload), so its stream.read names that type.
func (p *streamProducer) mintLocked() (ri, fi int) {
	p.writer = &writableStreamEnd{producer: p, elem: byteElem}
	p.reader = &readableStreamEnd{state: copyStateIdle, conn: p.writer, elem: byteElem}
	p.writer.conn = p.reader
	p.future = &readableFutureEnd{payload: &p.ok}
	p.reader.index = p.h.addLocked(p.reader)
	p.future.index = p.h.addLocked(p.future)
	return p.reader.index, p.future.index
}

// readLocked is a guest `stream.read` of this stream, called by streamRead with the end already COPYING and its
// buffer recorded. It is served from the buffer inline, or parks and starts a Read, or, with the source ended
// and the buffer empty, parks and finishes the stream. The caller holds p.h.mu.
func (p *streamProducer) readLocked(e *readableStreamEnd) ([]interp.Value, error) {
	switch {
	case len(p.buf) > 0:
		// The writer's buffer is there, so the read is the second arriver and copies inline (def:End.copy:"elif
		// buffer.remain() > 0 and self.other.buffer.remain() > 0:"). A read of 0 copies nothing and completes
		// itself with progress 0 (def:End.copy:"self.notify(progress = 0)").
		progress, err := driveStreamCopyLocked(e, p.writer)
		if err != nil {
			return nil, err
		}
		e.armLocked(progress, copyCompleted)
		ev, _ := e.pendingEventLocked()
		return []interp.Value{interp.I32(int32(ev.p2))}, nil
	case p.final != nil:
		// **The source has ended: the read parks, and the stream finishes behind it**, so the read returns
		// BLOCKED with its DROPPED already pending. That is the host's drop ordered after the read, which the
		// pin admits (the read parks at a writer with no buffer, def:End.copy:"self.buffer = buffer", and the
		// host's drop then notifies it), and it is wasmtime 49.0.3's reading on an empty stdin
		// (read-park.reading's `run empty`). Finishing first and returning DROPPED inline read differently:
		// read-park's callback-lifted guest, which waits for the event, waited for one that had been taken.
		//
		// NEUTER: finish first and take the event inline, and TestReadParkMatchesWasmtime's `none` row hangs.
		e.hasRead = true
		p.finishLocked()
		return blockedResult(), nil
	default:
		e.hasRead = true
		p.startReadLocked(e.readN)
		return blockedResult(), nil
	}
}

// copyIntoLocked copies `min(readN, len(buf))` buffered bytes into the reader's buffer and keeps the rest:
// `driveStreamCopyLocked`'s host arm. The caller holds p.h.mu.
//
// NEUTER: drop the rest after a copy, and TestAStreamReadCancelledMidReadServesTheBufferedBytesNext's read after
// the 4-byte one parks BLOCKED, "ef" lost.
func (p *streamProducer) copyIntoLocked(r *readableStreamEnd) (uint32, error) {
	n := uint32(len(p.buf))
	if r.readN < n {
		n = r.readN
	}
	if n == 0 {
		return 0, nil
	}
	if r.hostSink != nil { // write-via-stream refuses a produced stream before it reads, so a host never reads here
		return 0, fmt.Errorf("component: stream copy: a host-produced stream read by a host consumer")
	}
	if err := r.caller.Write(uint64(r.dstPtr), p.buf[:n]); err != nil {
		return 0, fmt.Errorf("component: stream copy: writing %d bytes to reader at %#x: %w", n, r.dstPtr, err)
	}
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return n, nil
}

// startReadLocked starts one Read of up to n bytes, capped, unless one is in flight or Close has stopped the
// table. A read of 0 asks for the cap. The caller holds p.h.mu.
//
// It is the producer's one goroutine site, authorised by ADR 0122 in the census
// (TestEveryEngineGoroutineIsAtASiteADecisionAuthorises), and by ADR 0123 for a socket's source. The goroutine
// ends when its Read returns and its delivery is applied or discarded. A Read that never returns leaves it
// blocked, abandoned by Close; a socket's Close closes its source, so its Read returns and it is waited out.
func (p *streamProducer) startReadLocked(n uint32) {
	if p.reading || p.done || p.h.producersStopped {
		return
	}
	if n == 0 || n > producerReadCap {
		n = producerReadCap
	}
	// A tracked source's goroutine is counted by its owner before it starts, so the owner's Close can wait it
	// out (a socket's, ADR 0123). An owner that has stopped starts none: its Close has closed the source.
	ended := func() {}
	if p.track != nil {
		e, ok := p.track()
		if !ok {
			return
		}
		ended = e
	}
	p.reading = true
	p.h.producerReads++
	buf := make([]byte, n)
	go func() {
		defer ended()
		p.read(buf)
	}()
}

// read is the goroutine's body: one Read, then its delivery.
func (p *streamProducer) read(buf []byte) {
	if p.srcMu != nil {
		p.srcMu.Lock()
	}
	n, err := p.src.Read(buf)
	if p.srcMu != nil {
		p.srcMu.Unlock()
	}
	p.deliver(buf[:n], err)
}

// deliver brings a Read's result to the guest under ADR 0101's result-store discipline, as `onResolve` does:
// queued for the run slot's holder while it is in host code, and otherwise applied under the slot, taken on a
// chain of its own and given up if Close stops the table meanwhile.
func (p *streamProducer) deliver(data []byte, rerr error) {
	h := p.h
	h.mu.Lock()
	if h.producersStopped {
		h.producerReads--
		h.mu.Unlock()
		return
	}
	if h.inHost > 0 {
		h.hostStores = append(h.hostStores, func() { p.apply(data, rerr) })
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	ch := &runChain{}
	if !h.acquireRun(ch, nil, h.producerStop) {
		h.mu.Lock()
		h.producerReads--
		h.mu.Unlock()
		return
	}
	defer h.releaseRunFor(ch)
	p.apply(data, rerr)
}

// apply is the delivery's store, run under the run slot or on its holder's goroutine as it leaves host code. It
// takes h.mu for the whole store, so a Close's stop, which takes the same lock, either precedes it and the
// store is discarded, or follows it and finds the store complete.
//
// NEUTER: drop this stop check, deliver's, and deliver's abort channel, and TestCloseAbandonsAProducerBlockedInRead's
// late bytes are stored into the closed instance: its reader armed, "late" in its buffer.
func (p *streamProducer) apply(data []byte, rerr error) {
	h := p.h
	h.mu.Lock()
	defer h.mu.Unlock()
	h.producerReads--
	p.reading = false
	if h.producersStopped || p.done {
		return // stopped by Close, or the reader dropped: the bytes are discarded
	}
	p.buf = append(p.buf, data...)
	// The end of input is `ok`, and any other failure `err(io)`.
	//
	// NEUTER: map io.EOF to err(io) too, and TestP3CatMatchesWasmtime's std guest prints "reading stdin failed: I/O
	// error" and exits 1. Its no-stdin rows never Read, so they are unaffected.
	if rerr != nil && p.final == nil {
		p.final = &p.ok
		if !errors.Is(rerr, io.EOF) {
			var v canon.Value
			if p.mapErr != nil {
				v = p.mapErr(rerr) // a socket's error-code (ADR 0123)
			} else {
				var err error
				if v, err = ioErrorResult(); err != nil {
					v = p.ok // unreachable: the constant label is declared
				}
			}
			p.final = &v
		}
	}
	e := p.reader
	pending := e.conn == p.writer && e.hasRead && !e.resolved
	switch {
	case pending && len(p.buf) > 0:
		progress, err := driveStreamCopyLocked(e, p.writer)
		if err != nil { // unreachable: the read's range was checked when it was issued
			p.final = &p.ok
			p.buf = nil
			p.finishLocked()
			return
		}
		e.armLocked(progress, copyCompleted)
	case pending && p.final != nil:
		p.finishLocked()
	case pending:
		p.startReadLocked(e.readN) // a Read of 0 bytes with no error: read again
	case len(p.buf) == 0 && p.final != nil:
		p.finishLocked() // no read pending: the idle reader is notified DROPPED
	}
}

// finishLocked ends the stream: the future resolves to the final value, then the host-held writer drops, which
// notifies the reader with DROPPED (dropWriterLocked). The caller holds p.h.mu.
func (p *streamProducer) finishLocked() {
	if p.done {
		return
	}
	p.done = true
	p.future.payload = p.final
	if err := p.future.completeLocked(copyCompleted); err != nil {
		p.future.completeErr = err // surfaced at the future's drop, as write-via-stream's is
	}
	dropWriterLocked(p.writer)
	if p.onDone != nil {
		p.onDone(false)
	}
}

// readerDroppedLocked is the guest's drop of the readable end: the producer stops and the future resolves
// `ok`, as `wasi:cli/stdin` states (*"If the stream's readable end is dropped the future will resolve to
// success"*). A Read in flight is discarded when it returns. The caller holds p.h.mu.
func (p *streamProducer) readerDroppedLocked() {
	if p.done {
		return
	}
	p.done = true
	p.buf = nil
	p.future.payload = &p.ok
	if err := p.future.completeLocked(copyCompleted); err != nil {
		p.future.completeErr = err
	}
	if p.onDone != nil {
		p.onDone(true)
	}
}

// ioErrorResult is `result::err(io)` at the stdio future's payload type: a Read that failed for a reason other
// than the end of input.
func ioErrorResult() (canon.Value, error) {
	t, err := stdioWriteResultType()
	if err != nil {
		return canon.Value{}, err
	}
	ec, err := canon.EnumType(stdioErrorCodeLabels...)
	if err != nil {
		return canon.Value{}, err
	}
	code, err := canon.Enum(ec, "io")
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(t, "error", &code)
}

// stopProducers is Close's stop for this table's producers: no Read starts and no delivery stores after it, and
// a delivery waiting for the run slot gives up. A goroutine blocked in Read is abandoned, never waited on.
//
// The flag is set under the lock and the channel closed after it (§4 B-MM-3: no engine lock across a channel
// operation). Only the call that set the flag closes it, so a second Close does not close it twice.
func (h *asyncHandles) stopProducers() {
	h.mu.Lock()
	first := !h.producersStopped
	h.producersStopped = true
	// A send's writer waiting for bytes sees the stop (ADR 0123). A condition, not a channel: no channel
	// operation under an engine lock.
	if h.hostIO != nil {
		h.hostIO.Broadcast()
	}
	h.mu.Unlock()
	if first {
		close(h.producerStop)
	}
}
