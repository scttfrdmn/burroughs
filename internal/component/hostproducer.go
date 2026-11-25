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
// **A socket's listen stream is one too** (ADR 0125): `stream<own<tcp-socket>>`, with no future, its source a
// listener and its elements accepted sockets, each minted as an own when it is delivered (copyOwnsLocked). The rules
// below hold for it with an Accept for a Read and a queue of sockets for the buffer of bytes, except at the end: it
// has no future to resolve, a transient Accept error is skipped, and Close closes the listener and waits it out.
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
	// atEnd, when set, is the future's value at the end of input in place of ok, computed then, under h.mu and the run
	// slot: an HTTP response body's trailers, which arrive after its last byte (ADR 0135). ok stays the value a reader's
	// drop resolves with.
	atEnd func() canon.Value

	// accept, when set, makes this listen's stream<own<tcp-socket>> (ADR 0125): its source is a listener, its
	// elements accepted sockets, and it has **no future**. src, srcMu, buf and mapErr are unused, and socks holds
	// what buf holds for bytes: sockets accepted, adopted into the registry, and not yet delivered.
	accept *sockAccept
	socks  []*tcpSocket

	// dir, when set, makes this read-directory's stream<directory-entry> (ADR 0134): its source is a directory, its
	// elements records carrying a string, and it has **no goroutine**. Each read is served by the reader's own
	// stream.read, which reads a batch and lowers it through the reader's realloc with no engine lock held
	// (dirReadLocked). src, srcMu, buf, mapErr, track, accept and socks are unused. serving is set while a read is
	// served with the lock released, so a cancel of it traps (streamCancelReadForm).
	dir     *fsDirSource
	serving bool
}

// queuedLocked reports something read and not yet delivered: bytes, or accepted sockets. The caller holds p.h.mu.
func (p *streamProducer) queuedLocked() bool { return len(p.buf) > 0 || len(p.socks) > 0 }

// dropQueueLocked discards what was read and not delivered. An accepted socket's connection reference is released,
// so the connection closes (asyncHandles.mu → tcpSocket.mu → hostNet.mu, ADR 0125's order). The caller holds p.h.mu.
func (p *streamProducer) dropQueueLocked() {
	p.buf = nil
	for _, s := range p.socks {
		_ = destroyTCPSocket(s) //nolint:errcheck // a *tcpSocket's destructor returns no error
	}
	p.socks = nil
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
// A directory's carry dirEntryElem, checked the same way (ADR 0134).
func (p *streamProducer) mintLocked() (ri, fi int) {
	el := byteElem
	if p.dir != nil {
		el = dirEntryElem
	}
	p.writer = &writableStreamEnd{producer: p, elem: el}
	p.reader = &readableStreamEnd{state: copyStateIdle, conn: p.writer, elem: el}
	p.writer.conn = p.reader
	p.future = &readableFutureEnd{payload: &p.ok, elem: elemOfPayload(&p.ok)} // typed by its payload (#1282 item 1)
	p.reader.index = p.h.addLocked(p.reader)
	p.future.index = p.h.addLocked(p.future)
	return p.reader.index, p.future.index
}

// mintAcceptLocked makes a listen stream's ends, with no future, and adds the readable end to the table (ADR 0125).
// Both carry acceptElem, `own<tcp-socket>`; the guest's declared listen result was checked at instantiate
// (checkListenSignature), so its stream.read names that type. The caller holds p.h.mu.
func (p *streamProducer) mintAcceptLocked() int {
	p.writer = &writableStreamEnd{producer: p, elem: acceptElem}
	p.reader = &readableStreamEnd{state: copyStateIdle, conn: p.writer, elem: acceptElem}
	p.writer.conn = p.reader
	p.reader.index = p.h.addLocked(p.reader)
	return p.reader.index
}

// readLocked is a guest `stream.read` of this stream, called by streamRead with the end already COPYING and its
// buffer recorded. It is served from the buffer inline, or parks and starts a Read, or, with the source ended
// and the buffer empty, parks and finishes the stream. The caller holds p.h.mu.
func (p *streamProducer) readLocked(e *readableStreamEnd) ([]interp.Value, error) {
	switch {
	case p.queuedLocked():
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
//
// **An element that is neither a byte nor an own is refused by name** (ADR 0134). A directory's entries carry a
// string, which lowers through the reader's `cabi_realloc`, guest code that may not run under this lock nor off the
// reader's goroutine (`offAgentHeap`). Its reads are served by the reader's own stream.read, which never reaches this
// copy; this is the arm a delivery or a second arriver would reach if one did.
//
// NEUTER, run: drop this refusal, and TestAStringElementIsNeverCopiedUnderTheLock's copy of a directory stream returns
// progress 0 with no error, its entry neither lowered nor refused.
func (p *streamProducer) copyIntoLocked(r *readableStreamEnd) (uint32, error) {
	if el := p.writer.elem; el != byteElem && el.own == nil {
		return 0, fmt.Errorf("%w: a host stream of %s is lowered only inside its reader's own stream.read, never by a "+
			"copy under the handle table's lock (ADR 0134)", ErrUnsupportedForm, el)
	}
	if own := p.writer.elem.own; own != nil {
		return p.copyOwnsLocked(r, own)
	}
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

// copyOwnsLocked is the copy's `own` arm (ADR 0125, the element descriptor's own, ADR 0124): `min(readN, queued)`
// accepted sockets are delivered, each minted as a fresh own of `own` in the reading instance's table
// (def:lower_own, through hostResources.mint) and its i32 index stored at `dstPtr + 4*i`, which is
// def:WritableGuestBuffer.write's store of an own element. The rest stay queued. The mint takes
// `hostResources.mu` under `asyncHandles.mu`, the order ADR 0125 states, acyclic because that lock is a leaf. The
// reader's buffer was checked as def:GuestBuffer checks it when its stream.read was called. The caller holds p.h.mu
// and the run slot.
//
// NEUTER: store each socket's queue position rather than its minted index, and TestP3TcpServeMatchesWasmtime's
// accept reads a handle the guest's table does not hold, and its receive traps.
func (p *streamProducer) copyOwnsLocked(r *readableStreamEnd, own *hostResource) (uint32, error) {
	n := uint32(len(p.socks))
	if r.readN < n {
		n = r.readN
	}
	if n == 0 {
		return 0, nil
	}
	if r.hostSink != nil { // unreachable: a host consumer is refused a produced stream before it reads
		return 0, fmt.Errorf("component: stream copy: a host-produced stream read by a host consumer")
	}
	if p.accept == nil { // unreachable: only listen's stream carries an own element
		return 0, fmt.Errorf("component: stream copy: a host stream of %s has no source of owns", p.writer.elem)
	}
	out := make([]byte, 4*n)
	for i := range n {
		idx := p.accept.res.mint(own, p.socks[i])
		binary.LittleEndian.PutUint32(out[4*i:], uint32(idx))
	}
	p.socks = p.socks[n:]
	if len(p.socks) == 0 {
		p.socks = nil
	}
	if err := r.caller.Write(uint64(r.dstPtr), out); err != nil {
		return 0, fmt.Errorf("component: stream copy: writing %d handles to reader at %#x: %w", n, r.dstPtr, err)
	}
	return n, nil
}

// startReadLocked starts one Read of up to n bytes, capped, unless one is in flight or Close has stopped the
// table. A read of 0 asks for the cap. The caller holds p.h.mu.
//
// It is the producer's one goroutine site, authorised by ADR 0122 in the census
// (TestEveryEngineGoroutineIsAtASiteADecisionAuthorises), by ADR 0123 for a socket's source, and by ADR 0125 for a
// listener's: the same goroutine runs one Accept rather than one Read, n unused. The goroutine ends when its Read
// or Accept returns and its delivery is applied or discarded. A Read that never returns leaves it blocked,
// abandoned by Close; a socket's Close closes its source, connection or listener, so its Read or Accept returns and
// it is waited out.
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
	var buf []byte
	if p.accept == nil {
		buf = make([]byte, n)
	}
	go func() {
		defer ended()
		if p.accept != nil {
			p.acceptOne()
			return
		}
		p.read(buf)
	}()
}

// acceptOne is a listen stream's goroutine body (ADR 0125): one Accept, then its delivery, as read's is.
func (p *streamProducer) acceptOne() {
	s, err := p.accept.accept()
	p.deliverStore(func() { p.applyAccept(s, err) }, func() {
		if s != nil {
			_ = destroyTCPSocket(s) //nolint:errcheck // a *tcpSocket's destructor returns no error
		}
	})
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
	p.deliverStore(func() { p.apply(data, rerr) }, nil)
}

// deliverStore is deliver over any store: a Read's apply, or an Accept's (ADR 0125). discard, when set, runs with
// no lock held if the store is given up, so what the store carried is released: an accepted socket's connection.
func (p *streamProducer) deliverStore(store, discard func()) {
	h := p.h
	discarded := func() {
		if discard != nil {
			discard()
		}
	}
	h.mu.Lock()
	if h.producersStopped {
		h.producerReads--
		h.mu.Unlock()
		discarded()
		return
	}
	if h.window.inHost > 0 {
		h.window.hostStores = append(h.window.hostStores, store)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	// **A free instance's delivery takes no slot** (#1336, ADR 0130's §3), for `onResolve`'s reasons. A delivery
	// calls no guest code, so it is addressed to no agent's window, and the instance's above is kept as before.
	if h.agentsRunFree {
		store()
		return
	}
	ch := &runChain{}
	if !h.acquireRun(ch, nil, h.producerStop) {
		h.mu.Lock()
		h.producerReads--
		h.mu.Unlock()
		discarded()
		return
	}
	defer h.releaseRunFor(ch)
	store()
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
		// NEUTER, run: skip atEnd, and TestP3HTTPMatchesWasmtime's five posts that reach a response (and only they) read
		// "trailers: none" where wasmtime reads the peer's x-sum.
		if p.atEnd != nil && errors.Is(rerr, io.EOF) {
			v := p.atEnd()
			p.final = &v
		}
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
	p.settleLocked()
}

// settleLocked serves what a delivery left: a pending read is copied into from the queue, or finishes the stream
// once its source has ended, or starts the next Read or Accept; with no read pending, an ended source and an empty
// queue finish the stream. The caller holds p.h.mu and the run slot.
func (p *streamProducer) settleLocked() {
	e := p.reader
	pending := e.conn == p.writer && e.hasRead && !e.resolved
	switch {
	case pending && p.queuedLocked():
		progress, err := driveStreamCopyLocked(e, p.writer)
		if err != nil { // unreachable: the read's range was checked when it was issued
			p.final = &p.ok
			p.dropQueueLocked()
			p.finishLocked()
			return
		}
		e.armLocked(progress, copyCompleted)
	case pending && p.final != nil:
		p.finishLocked()
	case pending:
		p.startReadLocked(e.readN) // a Read of 0 bytes with no error, or a skipped Accept: read again
	case !p.queuedLocked() && p.final != nil:
		p.finishLocked() // no read pending: the idle reader is notified DROPPED
	}
}

// applyAccept is an Accept's store (ADR 0125), apply's shape: under the run slot and h.mu, an accepted socket is
// queued, a transient error is skipped, and a fatal one ends the stream; then settleLocked serves a pending read.
// A socket that arrives after Close's stop or the reader's drop is released, its connection closed.
func (p *streamProducer) applyAccept(s *tcpSocket, aerr error) {
	h := p.h
	h.mu.Lock()
	defer h.mu.Unlock()
	h.producerReads--
	p.reading = false
	if h.producersStopped || p.done {
		if s != nil {
			_ = destroyTCPSocket(s) //nolint:errcheck // a *tcpSocket's destructor returns no error
		}
		return
	}
	switch {
	case aerr == nil:
		p.socks = append(p.socks, s)
	case acceptTransient(aerr):
		// Skipped: the stream is perpetual (sockets.wit's listen), and a pending read accepts again.
	case p.final == nil:
		p.final = &p.ok // fatal: the stream finishes, its writer dropping; it has no future to carry a code
	}
	p.settleLocked()
}

// finishLocked ends the stream: the future resolves to the final value, then the host-held writer drops, which
// notifies the reader with DROPPED (dropWriterLocked). The caller holds p.h.mu.
//
// **A listen stream has no future** (ADR 0125), so only its writer drops.
//
// NEUTER: resolve the future unconditionally, and TestATransientAcceptErrorDoesNotEndTheStream's fatal row panics on
// the nil future.
func (p *streamProducer) finishLocked() {
	if p.done {
		return
	}
	p.done = true
	if p.future != nil {
		p.future.payload = p.final
		if err := p.future.completeLocked(copyCompleted); err != nil {
			p.future.completeErr = err // surfaced at the future's drop, as write-via-stream's is
		}
	}
	dropWriterLocked(p.writer)
	if p.onDone != nil {
		p.onDone(false)
	}
}

// readerDroppedLocked is the guest's drop of the readable end: the producer stops and the future resolves
// `ok`, as `wasi:cli/stdin` states (*"If the stream's readable end is dropped the future will resolve to
// success"*). A Read in flight is discarded when it returns. The caller holds p.h.mu.
//
// **A listen stream has no future, and its queue holds sockets** (ADR 0125): each queued socket's connection is
// released, and onDone releases the stream's reference to the listener.
//
// NEUTER: resolve the future unconditionally, and TestP3TcpServeMatchesWasmtime's run panics at the listener's
// drop on the nil future.
func (p *streamProducer) readerDroppedLocked() {
	if p.done {
		return
	}
	p.done = true
	p.dropQueueLocked()
	if p.future != nil {
		p.future.payload = &p.ok
		if err := p.future.completeLocked(copyCompleted); err != nil {
			p.future.completeErr = err
		}
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
