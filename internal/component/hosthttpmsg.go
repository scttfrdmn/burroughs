// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The four functions that carry streams and futures (ADR 0135): `request.new` and `response.new`, which take a
// guest's body stream and trailers future and hand back a transmission future, and the two `consume-body`s, which
// hand a body back. Each is a stream consumer bound with both tables, its whole type declared (httpSignatures).
//
// # The host reads a guest future
//
// A trailers future given to new, and the `res` future given to consume-body, are guest futures the host reads.
// `readableFutureEnd.hostTake` is that read: a guest `future.write` that finds it runs it, lifting the value from the
// writer's memory, and the write completes COMPLETED (futureWriteLocked). A write parked before the host armed its
// read is taken at the arming (armHostTakeLocked). Either way the lift runs inside a guest call, under the run slot.

// liftFutureArgLocked is the lift of a host import's future argument, def:lift_async_value, liftStreamArgLocked's
// twin: handle i must be a readable future end of the parameter's payload `want`, IDLE and joined to no set, each a
// trap in the pin's order. **It is not removed from the guest's table**, as a stream argument is not: the existing
// divergence liftStreamArgLocked names. One already handed to the host traps as an index the guest no longer holds,
// def:Table.get's. The caller holds asyncHandles.mu.
func liftFutureArgLocked(h *asyncHandles, op string, i uint32, want *elemType) (*readableFutureEnd, error) {
	fe, ok := handleAt[*readableFutureEnd](h, i)
	if !ok {
		return nil, wrongKindTrap(op, i, "a readable future end", "def:lift_async_value")
	}
	// NEUTER, run: drop this check, and TestAFutureHandedToTheHostTwiceTraps' second lift of one end succeeds.
	// TestP3HTTPMatchesWasmtime passes under it: p3http hands each future to the host once.
	if fe.hostHeld {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: end %d was handed to the host already, so the guest does not "+
			"hold it (def:Table.get)", op, i)}
	}
	if !fe.elem.same(want) {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: end %d is a future of a different type: its payload is %s, "+
			"and the parameter's is %s (def:lift_async_value: trap_if(end.t != t))", op, i, fe.elem, want)}
	}
	if fe.resolved || fe.hasRead {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: end %d is not IDLE, so it cannot be passed "+
			"(def:lift_async_value: trap_if(end.state != End.State.IDLE))", op, i)}
	}
	if fe.set != nil {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: end %d is joined to a waitable set, so it cannot be passed "+
			"(def:lift_async_value: trap_if(end.in_waitable_set()))", op, i)}
	}
	fe.hostHeld = true
	return fe, nil
}

// armHostTakeLocked arms the host's read of a guest future it holds: a write already parked is taken now, from the
// writer's buffer, and completes COMPLETED; otherwise take runs when the guest's write arrives. The caller holds
// asyncHandles.mu and the run slot, since a parked write's value is read from guest memory.
//
// NEUTER, run: arm take without taking a parked write, and twelve of TestP3HTTPMatchesWasmtime's rows do not return
// within the hang guard (get, head, purge, dump, bigresp, early-drop, res-first, refused, dns, https, noscheme,
// timeout): each wrote its trailers future, or `res`, before the host armed its read, and wit-bindgen's block_on waits
// for that write, which nothing then takes. The rows that write after the send has started pass.
func armHostTakeLocked(fe *readableFutureEnd, take func(c *interp.CanonCaller, ptr uint32) error) error {
	if fe == nil {
		return nil // a message a test built without a trailers future
	}
	if w := fe.writer; w != nil && w.hasWrite && !w.resolved {
		if err := take(w.caller, w.srcPtr); err != nil {
			return err
		}
		w.completeWriteLocked(1, copyCompleted)
		fe.resolved, fe.delivered, fe.result = true, true, copyCompleted
		return nil
	}
	fe.hostTake = take
	return nil
}

// hostDropFutureReaderLocked is the host dropping a guest future it holds, def:End.drop from the reader: its writer
// is notified DROPPED unless it is DONE or has an event pending (dropReaderOfWriterLocked). The caller holds
// asyncHandles.mu.
func hostDropFutureReaderLocked(fe *readableFutureEnd) {
	if fe == nil {
		return
	}
	fe.hostTake = nil
	if w := fe.writer; w != nil {
		dropReaderOfWriterLocked(w)
		fe.writer = nil
	}
}

// resolveTransmitLocked gives a transmission future its value, once: a pending read has it stored, and otherwise the
// read that arrives takes it (futureReadLocked's host-first arm). The caller holds asyncHandles.mu and the run slot.
func resolveTransmitLocked(fe *readableFutureEnd, v canon.Value) {
	if fe == nil || fe.resolved {
		return
	}
	fe.payload = &v
	if err := fe.completeLocked(copyCompleted); err != nil {
		fe.completeErr = err // surfaced at the future's drop, as write-via-stream's is
	}
}

// mintTransmitLocked mints new's transmission future, typed by its payload (#1282 item 1). The caller holds
// asyncHandles.mu.
func mintTransmitLocked(h *asyncHandles) *readableFutureEnd {
	fe := &readableFutureEnd{elem: elemOfCanon(transmitPayloadType())}
	fe.index = h.addLocked(fe)
	return fe
}

// liftGuestBody lifts new's contents and trailers: an `option<stream<u8>>` from (disc, stream) and a trailers future.
// The stream is held for the message in the instance's registry, so it cannot be handed to two.
func liftGuestBody(h *asyncHandles, reg *hostHTTP, op string, disc, stream, trailers interp.Value) (httpGuestBody, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b httpGuestBody
	switch d := uint32(disc.Bits); d {
	case 0:
	case 1:
		re, err := liftStreamArgLocked(h, op, uint32(stream.Bits))
		if err != nil {
			return b, err
		}
		if !reg.hold(re) {
			return b, &interp.Trap{Reason: fmt.Sprintf("%s: stream end %d was handed to the host already, so the guest "+
				"does not hold it (def:Table.get)", op, re.index)}
		}
		b.contents = re
	default:
		return b, &interp.Trap{Reason: fmt.Sprintf("%s: option case %d out of range", op, d)}
	}
	fe, err := liftFutureArgLocked(h, op, uint32(trailers.Bits), elemOfCanon(trailersPayloadType()))
	if err != nil {
		if b.contents != nil {
			reg.unhold(b.contents)
		}
		return b, err
	}
	b.trailers = fe
	return b, nil
}

// releaseLocked is the host letting go of a message's guest body unconsumed: the stream's reader is dropped, so its
// writes complete DROPPED (unlinkHostReadLocked), and the trailers future's reader is dropped. The caller holds
// asyncHandles.mu.
func (b *httpGuestBody) releaseLocked(reg *hostHTTP) {
	if b.contents != nil {
		unlinkHostReadLocked(b.contents)
		reg.unhold(b.contents)
		b.contents = nil
	}
	hostDropFutureReaderLocked(b.trailers)
	b.trailers = nil
}

// writePair writes two i32 handles at ret, a tuple's layout.
func writePair(c *interp.CanonCaller, op string, ret interp.Value, a, b int) error {
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[0:], uint32(a))
	binary.LittleEndian.PutUint32(buf[4:], uint32(b))
	if err := c.Write(uint64(uint32(ret.Bits)), buf[:]); err != nil {
		return fmt.Errorf("component: %s: writing the result's two handles: %w", op, err)
	}
	return nil
}

// requestNew is `[static]request.new: func(headers, contents: option<stream<u8>>, trailers: future<…>, options:
// option<request-options>) -> tuple<request, future<result<_, error-code>>>`, (headers, cdisc, contents, trailers,
// odisc, options, ret). The arguments are lifted in order, each with the pin's traps: the headers' own, the stream,
// the future, the options' own, which is kept as an immutable copy.
func (h *Host) requestNew(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("request.new", args, 7, "headers, cdisc, contents, trailers, odisc, options, ret"); err != nil {
			return nil, err
		}
		hdrs, err := takeAs[*httpFields](res, httpFieldsResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		body, err := liftGuestBody(async, res.http, "request.new", args[1], args[2], args[3])
		if err != nil {
			return nil, err
		}
		var opts *httpOptions
		switch d := uint32(args[4].Bits); d {
		case 0:
		case 1:
			o, err := takeAs[*httpOptions](res, httpOptionsResource, args[5].Int32())
			if err != nil {
				return nil, err
			}
			opts = o.copyOf(true)
		default:
			return nil, &interp.Trap{Reason: fmt.Sprintf("request.new: option case %d out of range", d)}
		}
		r := &httpRequest{method: httpCase{label: "get"}, headers: hdrs, options: opts, h: async, reg: res.http, body: body}
		async.mu.Lock()
		r.sent = mintTransmitLocked(async)
		async.mu.Unlock()
		idx := res.mint(httpRequestResource, r)
		return nil, writePair(c, "request.new", args[6], int(idx), r.sent.index)
	}
}

// responseNew is `[static]response.new: func(headers, contents, trailers) -> tuple<response, future<…>>`, (headers,
// cdisc, contents, trailers, ret), with a default status of 200.
func (h *Host) responseNew(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("response.new", args, 5, "headers, cdisc, contents, trailers, ret"); err != nil {
			return nil, err
		}
		hdrs, err := takeAs[*httpFields](res, httpFieldsResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		body, err := liftGuestBody(async, res.http, "response.new", args[1], args[2], args[3])
		if err != nil {
			return nil, err
		}
		r := &httpResponse{status: 200, headers: hdrs, h: async, reg: res.http, body: body}
		async.mu.Lock()
		r.sent = mintTransmitLocked(async)
		async.mu.Unlock()
		idx := res.mint(httpResponseResource, r)
		return nil, writePair(c, "response.new", args[4], int(idx), r.sent.index)
	}
}

// passThroughLocked is consume-body of a guest-made message, as wasmtime 49.0.3's is: the guest's own body stream,
// or an empty host stream for contents none, and its own trailers future, whose read is then an intra-instance copy
// of a non-numeric payload, the trap both oracles raise (p3http's request-consume row). The value the guest writes to
// `res` becomes the message's transmission future's value. The caller holds asyncHandles.mu.
//
// NEUTER, run: answer a fresh host stream for a guest's contents, and TestP3HTTPMatchesWasmtime's response-consume,
// request-consume, consume-request-res and consume-response-res rows (and only they) do not return within the hang
// guard: the guest's own stream, which nobody now reads, takes its write_all for ever.
func passThroughLocked(h *asyncHandles, reg *hostHTTP, body *httpGuestBody, sent, resEnd *readableFutureEnd) (si, ti int, err error) {
	if body.contents != nil {
		si = body.contents.index
		reg.unhold(body.contents)
	} else {
		si = mintEmptyStreamLocked(h)
	}
	ti = body.trailers.index
	body.trailers.hostHeld = false
	*body = httpGuestBody{}
	// NEUTER, run: resolve ok instead of forwarding the value, and TestP3HTTPMatchesWasmtime's consume-request-res and
	// consume-response-res rows (and only they) read "sent: Ok(())" where wasmtime reads the HttpProtocolError the guest
	// wrote.
	err = armHostTakeLocked(resEnd, func(c *interp.CanonCaller, ptr uint32) error {
		v, lerr := liftTransmitValue(c, ptr)
		if lerr != nil {
			return lerr
		}
		resolveTransmitLocked(sent, v)
		return nil
	})
	return si, ti, err
}

// mintEmptyStreamLocked mints a readable stream<u8> end the host has already finished: a producer with no source and
// no future, whose first read takes DROPPED. The caller holds asyncHandles.mu.
func mintEmptyStreamLocked(h *asyncHandles) int {
	p := &streamProducer{h: h}
	p.final = &canon.Value{} // the source has ended; with no future, nothing reads the value
	p.writer = &writableStreamEnd{producer: p, elem: byteElem}
	p.reader = &readableStreamEnd{state: copyStateIdle, conn: p.writer, elem: byteElem}
	p.writer.conn = p.reader
	p.reader.index = h.addLocked(p.reader)
	return p.reader.index
}

// requestConsumeBody is `[static]request.consume-body: func(this: request, res: future<result<_, error-code>>) ->
// tuple<stream<u8>, future<result<option<trailers>, error-code>>>`, (this, res, ret). A request is always the
// guest's, so it is passThroughLocked's.
func (h *Host) requestConsumeBody(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("request.consume-body", args, 3, "this, res, ret"); err != nil {
			return nil, err
		}
		r, err := takeAs[*httpRequest](res, httpRequestResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		async.mu.Lock()
		resEnd, err := liftFutureArgLocked(async, "request.consume-body", uint32(args[1].Bits), elemOfCanon(transmitPayloadType()))
		if err != nil {
			r.body.releaseLocked(res.http)
			async.mu.Unlock()
			return nil, err
		}
		si, ti, err := passThroughLocked(async, res.http, &r.body, r.sent, resEnd)
		async.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, writePair(c, "request.consume-body", args[2], si, ti)
	}
}

// responseConsumeBody is `[static]response.consume-body`, request's shape: a guest's response is passThroughLocked's,
// and a send's is the host's body (consumeHostBodyLocked, hosthttpsend.go).
func (h *Host) responseConsumeBody(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("response.consume-body", args, 3, "this, res, ret"); err != nil {
			return nil, err
		}
		r, err := takeAs[*httpResponse](res, httpResponseResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		async.mu.Lock()
		defer async.mu.Unlock()
		resEnd, err := liftFutureArgLocked(async, "response.consume-body", uint32(args[1].Bits), elemOfCanon(transmitPayloadType()))
		if err != nil {
			if r.host != nil {
				r.host.dropBody()
			} else {
				r.body.releaseLocked(res.http)
			}
			return nil, err
		}
		var si, ti int
		if r.host != nil {
			si, ti, err = r.host.consumeHostBodyLocked(res, resEnd)
		} else {
			si, ti, err = passThroughLocked(async, res.http, &r.body, r.sent, resEnd)
		}
		if err != nil {
			return nil, err
		}
		return nil, writePair(c, "response.consume-body", args[2], si, ti)
	}
}

// destroyHTTPRequest is a request's destructor: dropped unsent and unconsumed, its body's ends are let go and its
// transmission future resolves `ok`, as wasmtime 49.0.3's does (p3http's request-drop row). At Close nothing is
// stored, since the instance is going.
func destroyHTTPRequest(obj any) error {
	r, ok := obj.(*httpRequest)
	if !ok {
		return fmt.Errorf("component: a request handle held a %T", obj)
	}
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	r.body.releaseLocked(r.reg)
	if !r.h.producersStopped {
		resolveTransmitLocked(r.sent, httpResultOK(nil, nil))
	}
	return nil
}

// destroyHTTPResponse is a response's destructor: a guest's as a request's, and a send's lets its body go, which
// abandons the connection (httpSend.dropBody).
func destroyHTTPResponse(obj any) error {
	r, ok := obj.(*httpResponse)
	if !ok {
		return fmt.Errorf("component: a response handle held a %T", obj)
	}
	if r.host != nil {
		r.host.dropBody()
		return nil
	}
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	r.body.releaseLocked(r.reg)
	if !r.h.producersStopped {
		resolveTransmitLocked(r.sent, httpResultOK(nil, nil))
	}
	return nil
}

// ---- Lifting a future's value.

func alignUp(n, a int) int { return (n + a - 1) / a * a }

// liftTrailersValue lifts a `result<option<trailers>, error-code>` the guest wrote at ptr: an ok's own is taken from
// the guest's table (def:lift_own) and frozen, since trailers are immutable once sent, and an error is its case's
// label. An error's payload is not kept.
func liftTrailersValue(c *interp.CanonCaller, res *hostResources, ptr uint32) (label string, f *httpFields, err error) {
	t := trailersPayloadType()
	heap := guestHeap{c}
	b, err := heap.ReadBytes(int(ptr), canon.ElemSize(t))
	if err != nil {
		return "", nil, &interp.Trap{Reason: fmt.Sprintf("trailers future: reading the value at %d: %v", ptr, err)}
	}
	opt, ec := canon.OptionType(fieldsOwnT()), httpErrorCodeType()
	off := alignUp(1, max(canon.Alignment(opt), canon.Alignment(ec)))
	switch b[0] {
	case 0:
		switch b[off] {
		case 0:
			return "", nil, nil
		case 1:
			idx := binary.LittleEndian.Uint32(b[off+alignUp(1, 4):])
			got, terr := takeAs[*httpFields](res, httpFieldsResource, int32(idx))
			if terr != nil {
				return "", nil, terr
			}
			got.mu.Lock()
			got.immutable = true
			got.mu.Unlock()
			return "", got, nil
		default:
			return "", nil, &interp.Trap{Reason: fmt.Sprintf("trailers future: option case %d out of range", b[off])}
		}
	case 1:
		v, lerr := canon.LoadVia(heap, ec, int(ptr)+off)
		if lerr != nil {
			return "", nil, &interp.Trap{Reason: fmt.Sprintf("trailers future: lifting the error-code: %v", lerr)}
		}
		name, _, _ := v.Case()
		return name, nil, nil
	default:
		return "", nil, &interp.Trap{Reason: fmt.Sprintf("trailers future: result case %d out of range", b[0])}
	}
}

// liftTransmitValue lifts a `result<_, error-code>` the guest wrote at ptr, with its strings made none: the value is
// stored again into a host future under asyncHandles.mu, where a string's realloc would run guest code under an engine
// lock (ADR 0135's named divergence).
func liftTransmitValue(c *interp.CanonCaller, ptr uint32) (canon.Value, error) {
	v, err := canon.LoadVia(guestHeap{c}, transmitPayloadType(), int(ptr))
	if err != nil {
		return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf("res future: lifting the value at %d: %v", ptr, err)}
	}
	return withoutStrings(v)
}

// withoutStrings is v with every `some(string)` made none.
func withoutStrings(v canon.Value) (canon.Value, error) {
	switch v.Type.Kind {
	case canon.KindOption:
		p, some, _ := v.Option()
		if !some {
			return v, nil
		}
		if p.Type.Kind == canon.KindString {
			return canon.None(v.Type)
		}
		sp, err := withoutStrings(p)
		if err != nil {
			return canon.Value{}, err
		}
		return canon.Some(v.Type, sp)
	case canon.KindVariant:
		name, p, _ := v.Case()
		if p == nil {
			return v, nil
		}
		sp, err := withoutStrings(*p)
		if err != nil {
			return canon.Value{}, err
		}
		return canon.Variant(v.Type, name, &sp)
	case canon.KindRecord:
		fs, _ := v.Record()
		m := make(map[string]canon.Value, len(fs))
		for i, f := range fs {
			sf, err := withoutStrings(f)
			if err != nil {
				return canon.Value{}, err
			}
			m[v.Type.Fields[i].Name] = sf
		}
		return canon.Record(v.Type, m)
	default:
		return v, nil
	}
}
