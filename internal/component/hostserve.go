// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// Serving `wasi:http/handler@0.3`'s `handle` over net/http (#1385, E2-in-b, ADR 0137).
//
// # A request is a host call
//
// Each request is minted as an own `request` in a host table of its own (newHostTable) and passed to the instance's
// `handle` export through callReturning (ADR 0136), which answers at the guest's `task.return` while its task runs on.
// That is contract §6 R-5's append on #1382: a host call of an export is a call, not a delivery of readiness.
//
// # An instance serves one task at a time
//
// The instances requests run on are a pool (HTTPServer), each serving one task at a time and taken back only once
// that task has exited and its response is finished, both cleanly. wasmtime counts an instance's concurrency to
// `task.return`; this counts it to the exit, so no task ever enters an instance whose earlier task has returned and
// not exited (ADR 0137's reading of the wit-bindgen panic, and the fork's one-task staging).
//
// # Goroutines
//
// One per request, the task's driver, started by ServeHTTP: the census's site (ADR 0137). ServeHTTP's own goroutine
// is net/http's. The request body's Read is ADR 0122's producer site, counted in the instance's HTTP registry. A
// request's context's AfterFunc, net/http's cancellation of a request whose client went away, runs only the body
// reader's abort, which takes asyncHandles.mu and returns.
//
// # Locks
//
// HTTPServer.mu and serveRequest.mu are leaves. httpIncoming.mu is a leaf: consume-body takes it under
// asyncHandles.mu to record its producer, and no engine lock is taken under it.

// serveIdleMax is how many idle instances a server keeps for the next request: wasmtime's per-instance concurrency
// default (16), reused as a count of warm instances. One returned with this many idle is closed (ADR 0137).
const serveIdleMax = 16

// servePage is the body every failure before an answer is given: wasmtime 49.0.3 serve's page, with its own name for
// wasmtime's (p3serve's err, err-internal and trap rows). No error is in it, as none is in wasmtime's.
const servePage = "<!doctype html>\n<html>\n<head>\n    <title>500 Internal Server Error</title>\n</head>\n<body>\n" +
	"    <center>\n        <h1>500 Internal Server Error</h1>\n        <hr>\n        burroughs\n    </center>\n" +
	"</body>\n</html>"

// writeServePage answers a request with the page: 500, text/html, a Content-Length.
func writeServePage(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Set("Content-Length", strconv.Itoa(len(servePage)))
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, servePage) //nolint:errcheck // a client gone is not this handler's to report
}

var errServeClosed = errors.New("component: the HTTP server is closed")

// HTTPServer serves a component's `wasi:http/handler@0.3` `handle` export as an http.Handler (ADR 0137). Its
// instances are made by spawn, each from the component's bytes and configuration with a host of its own.
type HTTPServer struct {
	spawn  func() (*Instantiated, error)
	export string // "" when the component has no handle export: every request is the page
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	idle   []*Instantiated
	live   map[*Instantiated]struct{} // every instance made and not closed, serving or idle
	reqs   map[*serveRequest]struct{} // the requests whose task has not ended
}

// NewHTTPServer serves the export named by export, a name HTTPHandlerExport returned, on instances spawn makes.
func NewHTTPServer(spawn func() (*Instantiated, error), export string) *HTTPServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &HTTPServer{
		spawn: spawn, export: export, ctx: ctx, cancel: cancel,
		live: map[*Instantiated]struct{}{}, reqs: map[*serveRequest]struct{}{},
	}
}

// HTTPHandlerExport is the name of this instance's `wasi:http/handler@0.3.x` `handle` export, as callReturning takes
// it, or "" for a component that exports none.
func (in *Instantiated) HTTPHandlerExport() string {
	names := make([]string, 0, len(in.export.exports))
	for name := range in.export.exports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasPrefix(name, "wasi:http/handler@0.3") {
			continue
		}
		if cd := in.export.exports[name]; cd.inst != nil {
			if rd, ok := cd.inst.export("handle"); ok && rd.fn != nil {
				return name + "#handle"
			}
		}
	}
	return ""
}

// take is an idle instance, the most recently returned, or a new one.
func (s *HTTPServer) take() (*Instantiated, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errServeClosed
	}
	if n := len(s.idle); n > 0 {
		in := s.idle[n-1]
		s.idle = s.idle[:n-1]
		s.mu.Unlock()
		return in, nil
	}
	s.mu.Unlock()
	in, err := s.spawn()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		in.Close()
		return nil, errServeClosed
	}
	s.live[in] = struct{}{}
	s.mu.Unlock()
	return in, nil
}

// release gives an instance back: to the idle list when it served cleanly and there is room, otherwise closed. An
// instance Close has already taken is Close's.
//
// NEUTER, run: always close, and TestAServedInstanceIsReusedOnlyAfterItsTaskExits reads 0 instances after two
// sequential requests, where it wants 1. Release at the answer instead of here, wasmtime's count to task.return, and
// its hello sent while a held echo's task has returned and not exited enters that echo's instance and is answered
// with the 500 page, its task ending before an answer: the guest fails there on Burroughs too.
func (s *HTTPServer) release(in *Instantiated, reuse bool) {
	s.mu.Lock()
	if _, ok := s.live[in]; !ok {
		s.mu.Unlock()
		return
	}
	if reuse && !s.closed && len(s.idle) < serveIdleMax {
		s.idle = append(s.idle, in)
		s.mu.Unlock()
		return
	}
	delete(s.live, in)
	s.mu.Unlock()
	in.Close()
}

// instances is how many instances the server holds, serving or idle, for a witness.
func (s *HTTPServer) instances() (live, idle int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live), len(s.idle)
}

// Close cancels every task in flight, lets every body go, waits until deadline for each task to end, and then closes
// every instance, idle or not. It reports whether every task ended before the deadline. A request after it is
// answered with the page.
//
// **The wait is bounded, and a task past it can outlive Close**, Component.Close's posture: an instance's Close ends
// a task executing guest code, and does not wake one parked on something no host work will deliver, which then ends
// at its park's bound. Close then reports false, which Component.Close reports as ErrCloseIncomplete.
func (s *HTTPServer) Close(deadline time.Time) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return true
	}
	s.closed = true
	reqs := make([]*serveRequest, 0, len(s.reqs))
	for q := range s.reqs {
		reqs = append(reqs, q)
	}
	s.mu.Unlock()
	// NEUTER, run: skip the cancel, and TestCloseEndsEveryServedTaskAndItsGoroutine still passes, **vacuous there**:
	// its unanswered stall is ended by abortBodies below, which tells the body it is reading that the body ended. The
	// cancel is for a guest that waits on nothing a body can end, which no p3serve row is; Component.Close's order,
	// cancel and then tear down, is why it is here.
	s.cancel()
	// A task that has answered cannot be cancelled. One still writing its body is told the body's reader has gone, and
	// one still reading its request's body, or awaiting its trailers, is told the body ended, so it exits; a task parked
	// with nothing to wake it would otherwise wait out the park's bound inside Close. Each holds for a body Close finds
	// at any point of its life, recorded or not, Read or not (abortBodies).
	//
	// NEUTER, run: skip it, and TestCloseEndsEveryServedTaskAndItsGoroutine's Close reaches its bound and reports it:
	// its answered echo, reading a body the client holds open, and its forever, writing a body nobody reads, are parked
	// with nothing to end them. (Before Close's last wait was bounded, the test binary timed out inside Close.)
	for _, q := range reqs {
		q.abortBodies()
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	complete := waitEnded(reqs, timer.C)
	s.mu.Lock()
	ins := make([]*Instantiated, 0, len(s.live))
	for in := range s.live {
		ins = append(ins, in)
	}
	s.live, s.idle = map[*Instantiated]struct{}{}, nil
	s.mu.Unlock()
	for _, in := range ins {
		in.Close()
	}
	// A Close that reports true has seen every task end, so no serving goroutine is left running. One that reports false
	// has closed the instances of the tasks the deadline left, and does not wait for their goroutines again.
	return complete
}

// waitEnded waits for every task's end until expired fires, and reports whether all ended.
func waitEnded(reqs []*serveRequest, expired <-chan time.Time) bool {
	for _, q := range reqs {
		select {
		case <-q.ended:
		case <-expired:
			return false
		}
	}
	return true
}

// serveRequest is one request in flight: its instance and host table, the answer and the task's end, and the two
// parties whose finish releases the instance, ServeHTTP's response and the task.
type serveRequest struct {
	srv      *HTTPServer
	in       *Instantiated
	held     *hostResources
	incoming *httpIncoming // the request's body
	answer   chan struct{} // closed at task.return, once result is set
	ended    chan struct{} // closed when the task's call has returned

	mu        sync.Mutex
	result    []canon.Value // the lifted result, set at task.return
	body      *httpReqBody  // the response body's reader, once armed
	taskDone  bool
	taskErr   error // what the task's call returned, once taskDone
	abandoned bool  // ServeHTTP has returned without an answer: one that arrives is let go at once
	letGo     bool  // Close has let the bodies go: a response body recorded after it is let go at once
	parts     int   // finishes so far, of two
	clean     bool  // every finish so far was clean
}

// answered is onReturn: the answer recorded for ServeHTTP, or, for a request whose client went away before it, let go
// at once. Recorded and judged under q.mu, so it and abandon never both miss it, and signalled after the unlock (§4
// B-MM-3). It runs inside the guest's task.return, holding the run slot, so the response it lets go is destroyed
// there.
func (q *serveRequest) answered(r []canon.Value) {
	q.mu.Lock()
	gone := q.abandoned
	if !gone {
		q.result = r
	}
	q.mu.Unlock()
	if gone {
		q.dropAnswer(r, true)
		return
	}
	close(q.answer)
}

// takeResult is the answer once q.answer is closed.
func (q *serveRequest) takeResult() []canon.Value {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.result
}

// abandon is ServeHTTP's return without an answer: an answer already recorded is let go now, and one given later by
// answered.
func (q *serveRequest) abandon() {
	q.mu.Lock()
	q.abandoned = true
	r := q.result
	q.result = nil
	q.mu.Unlock()
	if r != nil {
		q.dropAnswer(r, false)
	}
}

// dropAnswer destroys the response an answer carries, so its body's reader is dropped and a guest writing that body
// is told DROPPED, which lets its task run on to its exit (p3serve's stall and sleep rows, whose handlers answer a
// client already gone and then write a body nobody reads). A task.return holds the run slot already; anywhere else it
// is taken for the destructor's stores.
//
// NEUTER, run: let the answer lie in the host table instead, and TestServingAnAbandonedRequestLetsItsTaskExit's task
// ends with "async-lift park expired with no event", the park's bound, where it wants nil: its body's write was
// parked with nothing to read it.
func (q *serveRequest) dropAnswer(r []canon.Value, inSlot bool) {
	if len(r) != 1 {
		return
	}
	c, payload, ok := r[0].Case()
	if !ok || c != "ok" || payload == nil {
		return
	}
	idx, ok := payload.OwnHandle()
	if !ok {
		return
	}
	resp, err := takeAs[*httpResponse](q.held, httpResponseResource, int32(idx))
	if err != nil {
		return
	}
	if inSlot {
		_ = destroyHTTPResponse(resp) //nolint:errcheck // a *httpResponse's destructor returns no error
		return
	}
	underRunSlot(resp.h, func() {
		_ = destroyHTTPResponse(resp) //nolint:errcheck // a *httpResponse's destructor returns no error
	})
}

// abortBodies is Close's: a response body ServeHTTP is still copying is let go here, its reader dropped by a delivery
// so a guest writing it is told DROPPED even while ServeHTTP is held in a Write to a client that reads nothing, and a
// request body is ended (httpIncoming.abort), so a guest reading it, or awaiting its trailers, is told its end. Either
// way the task can exit before the instance closes, while the deliveries that tell it still run. ServeHTTP's own
// closeServedBody then finds it closed.
//
// **Neither half rests on what is in flight at its instant** (ADR 0137's postscript). Close comes at any moment, and
// there is a moment between a response's head leaving and its body being recorded, and another between one Read of a
// request body and the next, or after the last a guest makes. So a response body recorded after this is let go at
// once (setBody), and the request body's stream is ended by the host itself rather than through a Read, with any Read
// after it refused. A let-go that reached only what was in flight left the body it missed held for ever, and Close
// reached its bound: forever's body, recorded after Close had let the bodies go, on #1401's CI.
func (q *serveRequest) abortBodies() {
	q.mu.Lock()
	q.letGo = true
	b := q.body
	q.mu.Unlock()
	if b != nil {
		closeServedBody(b)
	}
	q.incoming.abort()
}

// run is the task's driver, the one goroutine per request ADR 0137 authorises: the call of `handle`, which returns
// when the task exits. Its context is the server's, so a client going away cancels nothing (p3serve's stall and sleep
// rows), and Close cancels every task.
func (q *serveRequest) run(arg canon.Value) {
	err := q.in.callReturning(q.srv.ctx, q.srv.export, q.held, q.answered, arg)
	q.mu.Lock()
	q.taskDone, q.taskErr = true, err
	b := q.body
	q.mu.Unlock()
	if b != nil {
		b.taskEnded()
	}
	q.finish(err == nil)
	q.srv.mu.Lock()
	delete(q.srv.reqs, q)
	q.srv.mu.Unlock()
	close(q.ended)
}

// setBody records the response body's reader, and tells it at once if the task has already ended, and lets it go at
// once if Close has let the bodies go already (abortBodies). Recorded and judged under q.mu, so it and abortBodies
// never both miss it. Letting it go before respond's setup delivery is safe in either order of the two: a setup after
// the unlink arms nothing, the body being closed (armLocked), and an unlink after the setup tells the guest's writer
// DROPPED, a write parked on it included.
//
// NEUTER, run: drop the letGo arm, and TestAResponseBodyRecordedAfterCloseIsLetGoAtOnce reads the body open and its
// guest writer not told, where it wants both. That is the place #1401's CI run read in
// TestCloseEndsEveryServedTaskAndItsGoroutine, by chance, with forever parked in its write until Close's bound.
func (q *serveRequest) setBody(b *httpReqBody) {
	q.mu.Lock()
	q.body = b
	done, letGo := q.taskDone, q.letGo
	q.mu.Unlock()
	if done {
		b.taskEnded()
	}
	if letGo {
		closeServedBody(b)
	}
}

// finish is one party's end: the second releases the instance, reused only if both were clean and the store is not
// locked. The host table is closed first, so an answer nobody took, or a request the call never moved, is destroyed
// while no task runs on the instance.
func (q *serveRequest) finish(clean bool) {
	q.mu.Lock()
	if q.parts == 0 {
		q.clean = true
	}
	q.parts++
	q.clean = q.clean && clean
	last, ok := q.parts == 2, q.clean
	q.mu.Unlock()
	if !last {
		return
	}
	underRunSlot(q.in.w.async, q.held.closeAll)
	q.srv.release(q.in, ok && q.in.w.lock.trap() == nil)
}

// underRunSlot runs f holding the instance's run slot, deliverHTTP's discipline without asyncHandles.mu, for a store a
// destructor makes, which takes that lock itself. Close's stop abandons the wait, and then f does not run.
func underRunSlot(h *asyncHandles, f func()) {
	if h.agentsRunFree {
		f()
		return
	}
	ch := &runChain{}
	if !h.acquireRun(ch, nil, h.producerStop) {
		return
	}
	defer h.releaseRunFor(ch)
	f()
}

// ServeHTTP serves one request: it mints the request, starts the call, and answers at the guest's task.return with
// the response's head, then its body, flushed as it goes, then its trailers (ADR 0137).
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.export == "" {
		writeServePage(w)
		return
	}
	in, err := s.take()
	if err != nil {
		writeServePage(w)
		return
	}
	rc := http.NewResponseController(w)
	// **Full duplex** (ADR 0137): Go's HTTP/1 server otherwise makes the request body unreadable once the response's
	// head has gone, and a guest that answers first and reads after (p3serve's echo and concurrent rows) needs it. An
	// HTTP/2 request is full duplex already, and answers ErrNotSupported.
	//
	// NEUTER, run: skip it, and TestP3ServeMatchesWasmtime's concurrent row fails at its place: the first held
	// request's response head never arrives within the guard, its handler having answered while its body is open.
	_ = rc.EnableFullDuplex() //nolint:errcheck // HTTP/2's ErrNotSupported: full duplex there already
	body := &httpIncoming{body: r.Body, trailer: r.Trailer, rc: rc}
	res := in.w.hostRes
	res.http.addIncoming(body)
	defer res.http.dropIncoming(body)
	defer body.finish()

	q := &serveRequest{
		srv: s, in: in, held: newHostTable(), incoming: body, answer: make(chan struct{}), ended: make(chan struct{}),
	}
	req := incomingRequest(in.w.async, res.http, r, body)
	idx := q.held.mint(httpRequestResource, req)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		q.held.closeAll()
		s.release(in, false)
		writeServePage(w)
		return
	}
	s.reqs[q] = struct{}{}
	s.mu.Unlock()
	go q.run(canon.Own(httpRequestResource.rt, uint32(idx)))

	select {
	case <-q.answer:
	case <-q.ended:
		// The task ended without answering: it trapped, exited with no task.return, or Close ended it.
		select {
		case <-q.answer: // answered, and then ended before this select saw the answer
		default:
			writeServePage(w)
			q.finish(true)
			return
		}
	case <-r.Context().Done():
		// The client went away before the answer (p3serve's stall and sleep rows): the task runs on, and its answer is
		// let go when it arrives.
		q.abandon()
		q.finish(false)
		return
	}
	whole, abort := s.respond(w, r, rc, q, q.takeResult())
	q.finish(whole)
	if abort {
		// Trailers resolved as an error: the connection closes before the body ends (p3serve's trailers-err row),
		// which the abort sentinel makes net/http do.
		//
		// NEUTER, run: return without the panic, and TestP3ServeMatchesWasmtime's trailers-err row reads "the response
		// ended" where wasmtime closes the connection first.
		panic(http.ErrAbortHandler)
	}
}

// respond writes an answer: the page for err(code) or a response this host cannot send, and otherwise the response.
// It reports whether the response was sent whole, and whether its trailers failed, so the connection must be cut.
func (s *HTTPServer) respond(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, q *serveRequest,
	result []canon.Value,
) (whole, abort bool) {
	if len(result) != 1 {
		writeServePage(w)
		return false, false
	}
	c, payload, ok := result[0].Case()
	if !ok || c != "ok" || payload == nil {
		// err(error-code): wasmtime logs the code on its stderr and sends only the page; a handler has no log.
		writeServePage(w)
		return true, false
	}
	idx, ok := payload.OwnHandle()
	if !ok {
		writeServePage(w)
		return false, false
	}
	resp, err := takeAs[*httpResponse](q.held, httpResponseResource, int32(idx))
	if err != nil {
		writeServePage(w)
		return false, false
	}
	h := resp.h
	h.mu.Lock()
	gb := resp.body
	resp.body = httpGuestBody{}
	produced := resp.host != nil || (gb.contents != nil && gb.contents.conn != nil && gb.contents.conn.producer != nil)
	h.mu.Unlock()
	if produced {
		// A body the host produces, a send's response or an incoming request's body handed back: a host-to-host copy,
		// which is E2-in-c's (#1386). Nothing of it is sent.
		destroyServed(resp, gb)
		writeServePage(w)
		return false, false
	}

	resp.mu.Lock()
	status, hdrs := resp.status, resp.headers
	resp.mu.Unlock()
	head := w.Header()
	for _, g := range hdrs.entries() {
		vs := make([]string, len(g.values))
		for i, v := range g.values {
			vs[i] = string(v)
		}
		head[http.CanonicalHeaderKey(g.name)] = vs
	}
	w.WriteHeader(int(status))
	// The head leaves at the answer, whatever body follows (p3serve's late and concurrent rows). A client gone is seen
	// at the body's first write, so the flush's error is not read here.
	_ = rc.Flush()

	b := &httpReqBody{h: h, reg: resp.reg, reader: gb.contents, trailers: gb.trailers}
	res := q.in.w.hostRes
	h.mu.Lock()
	if h.hostIO == nil {
		h.hostIO = sync.NewCond(&h.mu)
	}
	// A body being read is a waker for the park bound, for as long as the host reads it: a guest parked on its body's
	// write is woken by the host's next read, however long a slow client takes.
	h.httpPending += 2 // one for the read, one for the delivery below, which lowers its own
	h.mu.Unlock()
	q.setBody(b)
	var setupErr error
	deliverHTTP(h, func() {
		if b.reader == nil {
			b.closing = true // contents none: an empty body
		} else if err := b.setupLocked(resp.caller); err != nil {
			setupErr = err
			b.stopping = true
		}
		take := func(tc *interp.CanonCaller, ptr uint32) error {
			label, f, lerr := liftTrailersValue(tc, res, ptr)
			if lerr != nil {
				return lerr
			}
			b.tSet, b.tErr, b.tFields = true, label, f
			h.hostIO.Broadcast()
			return nil
		}
		if err := armHostTakeLocked(b.trailers, take); err != nil {
			setupErr = err
			b.stopping = true
		}
		h.hostIO.Broadcast()
	})
	whole, label := s.copyBody(w, r, rc, b)
	h.mu.Lock()
	h.httpPending--
	h.mu.Unlock()
	closeServedBody(b)
	// The transmission future (response.new's): ok for a response sent whole, the trailers' own code for one they
	// failed, connection-terminated otherwise. Neither code was read on wasmtime: p3serve's guest drops its future.
	sent := resp.sent
	h.mu.Lock()
	h.httpPending++
	h.mu.Unlock()
	deliverHTTP(h, func() {
		switch {
		case whole:
			resolveTransmitLocked(sent, httpResultOK(nil, nil))
		case label != "":
			resolveTransmitLocked(sent, httpResultErr(nil, label))
		default:
			resolveTransmitLocked(sent, httpResultErr(nil, "connection-terminated"))
		}
	})
	return whole && setupErr == nil, label != ""
}

// closeServedBody lets the guest's body go once the host has stopped reading it: httpReqBody.Close for a stream, and
// for contents none only the trailers future's reader, if its value was never taken.
func closeServedBody(b *httpReqBody) {
	if b.reader != nil {
		_ = b.Close() // httpReqBody.Close returns nil
		return
	}
	h := b.h
	h.mu.Lock()
	if b.closed {
		h.mu.Unlock()
		return
	}
	b.closed = true
	if h.hostIO != nil {
		h.hostIO.Broadcast()
	}
	taken := b.tSet
	if !taken {
		h.httpPending++
	}
	h.mu.Unlock()
	if !taken {
		deliverHTTP(h, func() { hostDropFutureReaderLocked(b.trailers) })
	}
}

// copyBody copies the guest's body to the writer, flushing after every Read, and then writes its trailers, only for
// a request whose TE names trailers (hyper's rule, p3serve's trailers and trailers-te rows). It reports whether the
// body was sent whole, and the code of trailers resolved as an error.
func (s *HTTPServer) copyBody(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, b *httpReqBody) (whole bool, label string) {
	// A client gone while the guest writes nothing is told by the request's context, which aborts the Read.
	stop := context.AfterFunc(r.Context(), b.abort)
	defer stop()
	buf := make([]byte, httpBodyBufCap)
	for {
		n, err := b.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return false, "" // the client went away: the reader is dropped by closeServedBody (forever)
			}
			_ = rc.Flush() // a failed flush is the next write's
		}
		if err == nil {
			continue
		}
		var ce *httpCodeError
		switch {
		case errors.Is(err, io.EOF):
			// NEUTER, run: write the trailers whatever the TE, and TestP3ServeMatchesWasmtime's trailers row reads
			// x-echo=tail and x-sum=10 where wasmtime, with no TE: trailers, sent neither.
			if acceptsTrailers(r) {
				writeTrailers(w, b)
			}
			return true, ""
		case errors.As(err, &ce):
			return false, ce.label
		default:
			return false, ""
		}
	}
}

// acceptsTrailers reports whether the request's TE names trailers.
func acceptsTrailers(r *http.Request) bool {
	for _, v := range r.Header.Values("Te") {
		for _, t := range strings.Split(v, ",") {
			if name, _, _ := strings.Cut(strings.TrimSpace(t), ";"); strings.EqualFold(strings.TrimSpace(name), "trailers") {
				return true
			}
		}
	}
	return false
}

// writeTrailers sets the guest's trailers on the writer, after the body, under net/http's TrailerPrefix, so each is
// sent whether or not the guest declared it in a trailer header.
func writeTrailers(w http.ResponseWriter, b *httpReqBody) {
	b.h.mu.Lock()
	f := b.tFields
	b.h.mu.Unlock()
	if f == nil {
		return
	}
	head := w.Header()
	for _, g := range f.entries() {
		vs := make([]string, len(g.values))
		for i, v := range g.values {
			vs[i] = string(v)
		}
		head[http.TrailerPrefix+g.name] = vs
	}
}

// destroyServed lets go of a response this host will not send: its body's ends, as its destructor would, under the
// run slot, since the guest's task may still be running.
func destroyServed(resp *httpResponse, gb httpGuestBody) {
	resp.h.mu.Lock()
	resp.body = gb
	resp.h.mu.Unlock()
	underRunSlot(resp.h, func() {
		_ = destroyHTTPResponse(resp) //nolint:errcheck // a *httpResponse's destructor returns no error
	})
}

// incomingRequest is a request being served as a `request` object (ADR 0137): its method, its target's path and query,
// a scheme and an authority only for an absolute-form target, its headers lowercased, sorted, immutable and without
// the names `fields` forbids, and the host's body.
func incomingRequest(h *asyncHandles, reg *hostHTTP, r *http.Request, body *httpIncoming) *httpRequest {
	method := httpCase{label: "other", other: r.Method}
	if l, ok := httpStandardMethods[r.Method]; ok {
		method = httpCase{label: l}
	}
	pq := r.URL.RequestURI()
	req := &httpRequest{method: method, path: &pq, headers: incomingFields(r), h: h, reg: reg, incoming: body}
	if r.URL.Scheme != "" {
		switch strings.ToLower(r.URL.Scheme) {
		case "http":
			req.scheme = &httpCase{label: "HTTP"}
		case "https":
			req.scheme = &httpCase{label: "HTTPS"}
		default:
			req.scheme = &httpCase{label: "other", other: r.URL.Scheme}
		}
	}
	// NEUTER, run: drop the authority, and TestP3ServeMatchesWasmtime's target-absolute row reads authority None where
	// wasmtime reads Some("p3serve.test"). The target row passes under it: an origin-form target carries none.
	if r.URL.Host != "" {
		a := r.URL.Host
		req.authority = &a
	}
	return req
}

// incomingFields is a request's headers as wasmtime 49.0.3 hands them to a guest (p3serve's dump rows): lowercased
// and immutable, with the eight forbidden names dropped and a `trailer` header, which net/http moves into
// Request.Trailer, put back from the declared keys.
func incomingFields(r *http.Request) *httpFields {
	hdr := make(map[string][]string, len(r.Header)+1)
	for k, vs := range r.Header {
		// NEUTER, run: keep the forbidden names, and TestP3ServeMatchesWasmtime's dump row reads connection,
		// keep-alive and proxy-authorization among the guest's headers, which wasmtime drops.
		if httpForbiddenFields[strings.ToLower(k)] {
			continue
		}
		hdr[k] = vs
	}
	if len(r.Trailer) > 0 {
		keys := make([]string, 0, len(r.Trailer))
		for k := range r.Trailer {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		hdr["Trailer"] = []string{strings.Join(keys, ", ")}
	}
	return fieldsOfHeader(hdr)
}

// httpIncoming is the body of a request being served: the request's Body and Trailer, read by the producer's
// goroutine, and the controller whose read deadline ends a Read in flight. Every field but the three read-only ones
// is guarded by mu.
type httpIncoming struct {
	body    io.Reader
	trailer http.Header // filled by net/http before Read returns the end
	rc      *http.ResponseController

	mu       sync.Mutex
	closed   bool            // ServeHTTP is returning: no Read touches the body after it
	aborted  bool            // Close has let the body go: a Read in flight was ended, and none starts after it
	reading  bool            // a Read is in flight
	idle     chan struct{}   // closed when the Read in flight returns
	producer *streamProducer // the body's producer, once consume-body has made it
}

var errIncomingClosed = errors.New("component: wasi:http: the request being served has been answered")

// Read is the producer's read of the request body (ADR 0122's site). After Close's abort it reads nothing and fails, as
// a Read the abort ended in flight fails, so a guest is told the same end whichever it was: HTTP-protocol-error, the
// producer's mapping of any failed Read.
//
// NEUTER, run: drop the aborted arm, and TestARequestBodyReadAfterCloseIsRefused reads the body and returns io.EOF,
// where it wants no read and errServeClosed: a held body would have blocked that Read until its client went away. The
// stream's own end (endIncoming) is a delivery, so a read the guest issues before it lands would otherwise start a
// Read that only ServeHTTP's return ends, and a ServeHTTP held in a Write does not return.
func (b *httpIncoming) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errIncomingClosed
	}
	if b.aborted {
		b.mu.Unlock()
		return 0, errServeClosed
	}
	b.reading, b.idle = true, make(chan struct{})
	b.mu.Unlock()
	n, err := b.body.Read(p)
	b.mu.Lock()
	b.reading = false
	idle := b.idle
	b.mu.Unlock()
	close(idle) // after the unlock (§4 B-MM-3): finish waits on it only for a Read it saw in flight
	return n, err
}

// abort is Close's let-go of the request body (abortBodies): it ends a Read in flight by a read deadline in the past,
// refuses every Read after it, and ends the body's stream itself (endIncoming), so the guest is told the body's end
// whatever it is doing, reading or not. The first two are under mu, so it never touches the controller after finish,
// and so a Read either is in flight when it runs or starts after it and finds it.
func (b *httpIncoming) abort() {
	b.mu.Lock()
	b.aborted = true
	if !b.closed && b.reading {
		_ = b.rc.SetReadDeadline(time.Unix(1, 0)) //nolint:errcheck // a controller that cannot is HTTP/2's, whose body closes at the return
	}
	p := b.producer
	b.mu.Unlock()
	endIncoming(p)
}

// endIncoming ends a request body's stream at Close's let-go, by a delivery under ADR 0101's discipline, since the end
// may complete a guest's read of the trailers future, which writes its memory. **The let-go cannot rest on a Read**: a
// producer Reads only while its guest has a read pending (ADR 0122), so a guest that has stopped reading its body and
// awaits that body's trailers, as p3serve's echo does once its response's write is told DROPPED, has no Read for the
// deadline to end, and would wait for a future only this host can resolve. nil, a body not consumed yet, has nothing
// to end here: consumeLocked ends it when it is made.
//
// NEUTER, run: skip this call in abort, and TestCloseEndsATaskAwaitingTheTrailersOfABodyItStoppedReading's Close
// reaches its bound with the echo parked in awaitEvent, its write told DROPPED and its trailers never resolved, and its
// ServeHTTP held in a Write.
func endIncoming(p *streamProducer) {
	if p == nil {
		return
	}
	h := p.h
	h.mu.Lock()
	if p.done {
		h.mu.Unlock()
		return
	}
	h.httpPending++
	h.mu.Unlock()
	deliverHTTP(h, func() { endIncomingLocked(p) })
}

// endIncomingLocked finishes the stream now, as a failed Read would: its future err(HTTP-protocol-error), the code a
// Read the abort ended in flight gives, unless the source had already ended with nothing left undelivered; what was
// read and not delivered dropped; and the writer dropped, so a pending read is told DROPPED. A Read still in flight is
// discarded at its delivery, which finds the producer done. The caller holds p.h.mu and the run slot.
func endIncomingLocked(p *streamProducer) {
	if p.done {
		return
	}
	if p.final == nil || p.queuedLocked() {
		v := p.mapErr(errServeClosed)
		p.final = &v
	}
	p.dropQueueLocked()
	p.finishLocked()
}

// finish is ServeHTTP's last act on the body: net/http forbids a Read after the handler returns, so none starts after
// this, and one in flight is ended by a read deadline in the past and waited out.
func (b *httpIncoming) finish() {
	b.mu.Lock()
	b.closed = true
	reading, idle := b.reading, b.idle
	ended := reading && b.rc.SetReadDeadline(time.Unix(1, 0)) == nil
	b.mu.Unlock()
	if ended {
		<-idle
	}
}

// consumeLocked is request.consume-body of a request being served: ADR 0122's producer over its body, whose future is
// the trailers, `ok(some(trailers))` at the end, the trailers minted as an immutable own at that delivery, or
// `ok(none)`; `err(HTTP-protocol-error)` for a Read that fails, the one code p3serve's stall row reads; and `ok(none)` at
// the reader's drop. `res` is read at once and its value dropped, as for a send's response. The caller holds
// asyncHandles.mu.
func (b *httpIncoming) consumeLocked(h *asyncHandles, res *hostResources, resEnd *readableFutureEnd) (si, ti int, err error) {
	opt := canon.OptionType(fieldsOwnT())
	none := optionValue(fieldsOwnT(), nil)
	p := &streamProducer{
		h:     h,
		src:   b,
		ok:    httpResultOK(&opt, &none),
		track: res.http.track,
		// NEUTER, run: answer internal-error, and TestP3ServeMatchesWasmtime's stall row reads its trailers "err
		// ErrorCode::InternalError" where wasmtime reads HttpProtocolError.
		mapErr: func(error) canon.Value { return httpResultErr(&opt, "HTTP-protocol-error") },
		atEnd:  func() canon.Value { return b.trailersValue(res) },
	}
	si, ti = p.mintLocked()
	// Recorded for Close's let-go (endIncoming), or, consumed after it, ended now: the ends were minted just now, so no
	// read of them is pending and the end writes no guest memory. mu is a leaf, so it is taken here under
	// asyncHandles.mu, and abort either finds the producer or has set aborted before this reads it.
	//
	// NEUTER, run: skip the end, and TestARequestBodyConsumedAfterCloseIsEndedAtOnce reads the stream not ended and no
	// trailers value, where it wants error(HTTP-protocol-error). No p3serve row reaches this arm.
	b.mu.Lock()
	b.producer = p
	aborted := b.aborted
	b.mu.Unlock()
	if aborted {
		endIncomingLocked(p)
	}
	err = armHostTakeLocked(resEnd, func(c *interp.CanonCaller, ptr uint32) error {
		_, lerr := liftTransmitValue(c, ptr)
		return lerr
	})
	return si, ti, err
}

// trailersValue is the trailers future's value at the end of the body: the request's trailers as an immutable own
// minted now, or none. Run under asyncHandles.mu and the run slot, by the delivery that saw the end.
func (b *httpIncoming) trailersValue(res *hostResources) canon.Value {
	opt := canon.OptionType(fieldsOwnT())
	sent := http.Header{}
	for k, vs := range b.trailer {
		if len(vs) > 0 {
			sent[k] = vs
		}
	}
	if len(sent) == 0 {
		none := optionValue(fieldsOwnT(), nil)
		return httpResultOK(&opt, &none)
	}
	handle := canon.Own(httpFieldsResource.rt, uint32(res.mint(httpFieldsResource, fieldsOfHeader(sent))))
	some := optionValue(fieldsOwnT(), &handle)
	return httpResultOK(&opt, &some)
}
