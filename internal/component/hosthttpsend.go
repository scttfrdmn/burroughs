// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// `wasi:http/client@0.3`'s `send` (ADR 0135): an async import over the instance's own `net/http.Transport`, behind
// `Host.AllowHTTP`.
//
// # One goroutine per send
//
// The round trip runs on a goroutine of its own, the one site ADR 0135 authorises in the census. It runs no guest code
// and touches no guest memory: its outcome reaches the guest through `onResolve`, ADR 0101's discipline, and the
// transmission future's through `deliverHTTP`, the same discipline. One claim decides who resolves: the round trip's
// return, the guest's cancel, or Close.
//
// # The request body is read by net/http
//
// The guest's body stream is read into a bounded host buffer by write-via-stream's consumer shape (tcp-socket.send's,
// ADR 0123), and the Transport's own goroutine Reads the buffer (`httpReqBody.Read`). That goroutine is net/http's,
// not an engine site. It touches no guest memory: the room a Read frees is delivered under ADR 0101's discipline,
// which is where a parked guest write's copy runs.
//
// # The response body is ADR 0122's producer
//
// consume-body of a send's response is a stream producer over the body, its future the trailers, its Read the
// producer's goroutine site, counted in the instance's HTTP registry.
//
// # Close
//
// The registry (`hostHTTP`) holds the Transport, every send in flight and every response body still open. Close cancels
// every one's context, which ends a round trip and a body Read, closes the idle connections, and waits every send
// goroutine and body Read out.

// httpBodyBufCap bounds the bytes a request body holds that the guest has written and the Transport has not yet Read.
const httpBodyBufCap = 64 << 10

// The causes a send's context is cancelled with, so the error a round trip or a body Read returns maps to its code.
var (
	errHTTPFirstByte    = errors.New("component: wasi:http: the request's first-byte-timeout expired")
	errHTTPBetweenBytes = errors.New("component: wasi:http: the request's between-bytes-timeout expired")
	errHTTPCancelled    = errors.New("component: wasi:http: the send was cancelled")
	errHTTPBodyDropped  = errors.New("component: wasi:http: the response body was dropped")
	errHTTPStopped      = errors.New("component: wasi:http: the instance closed")
	errHTTPBodyAborted  = errors.New("component: wasi:http: the request body was abandoned")
)

// httpCodeError is an error that already is its code: a request body whose trailers future resolved an error, which
// fails the round trip with that code, as wasmtime 49.0.3's does (p3http's post-dropbody row).
type httpCodeError struct{ label string }

func (e *httpCodeError) Error() string {
	return "component: wasi:http: the guest's trailers were " + e.label
}

// httpConnectTimeoutError is a dial that its connect-timeout ended.
type httpConnectTimeoutError struct{ err error }

func (e *httpConnectTimeoutError) Error() string {
	return "component: wasi:http: the request's connect-timeout expired: " + e.err.Error()
}
func (e *httpConnectTimeoutError) Unwrap() error { return e.err }

// httpConnectTimeoutKey carries a send's connect-timeout to the Transport's dial.
type httpConnectTimeoutKey struct{}

// hostHTTP is one component instance's HTTP: its Transport, its sends in flight and their bodies, and the request body
// streams messages hold. It lives in the instance's hostResources, so a nested instance has its own. Its lock is a
// leaf, taken under asyncHandles.mu and never the reverse.
type hostHTTP struct {
	mu        sync.Mutex
	stopped   bool
	transport *http.Transport
	pending   map[*httpSend]struct{} // round trips not yet resolved: the claim
	live      map[*httpSend]struct{} // sends whose context Close cancels: until the response body is done
	held      map[*readableStreamEnd]struct{}
	// incoming are the bodies of requests this instance is serving (hostserve.go, ADR 0137), whose Read in flight
	// Close ends.
	incoming map[*httpIncoming]struct{}
	wg       sync.WaitGroup
}

func newHostHTTP() *hostHTTP {
	return &hostHTTP{
		pending: map[*httpSend]struct{}{}, live: map[*httpSend]struct{}{}, held: map[*readableStreamEnd]struct{}{},
		incoming: map[*httpIncoming]struct{}{},
	}
}

// hold records a stream end as a message's body, and reports false if one already is.
func (n *hostHTTP) hold(re *readableStreamEnd) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.held[re]; ok {
		return false
	}
	n.held[re] = struct{}{}
	return true
}

func (n *hostHTTP) unhold(re *readableStreamEnd) {
	n.mu.Lock()
	delete(n.held, re)
	n.mu.Unlock()
}

// track counts a response body's Read about to start (a producer's track hook). After stop none starts.
func (n *hostHTTP) track() (ended func(), ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return nil, false
	}
	n.wg.Add(1)
	return n.wg.Done, true
}

// start registers a send and counts its goroutine.
func (n *hostHTTP) start(s *httpSend) (ended func(), ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return nil, false
	}
	n.pending[s] = struct{}{}
	n.live[s] = struct{}{}
	n.wg.Add(1)
	return n.wg.Done, true
}

// claim removes s from the pending round trips and reports whether this caller removed it.
func (n *hostHTTP) claim(s *httpSend) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.pending[s]; !ok {
		return false
	}
	delete(n.pending, s)
	return true
}

// release forgets a send whose response body is done, or that failed.
func (n *hostHTTP) release(s *httpSend) {
	n.mu.Lock()
	delete(n.live, s)
	n.mu.Unlock()
}

// roundTripper is the instance's Transport, made at its first send. ForceAttemptHTTP2 is DefaultTransport's, and
// DisableCompression keeps net/http from adding an accept-encoding wasmtime does not send (p3http's dump row).
//
// NEUTER, run: leave compression on, and TestP3HTTPMatchesWasmtime's dump row (and only it) has the peer read
// Accept-Encoding=gzip.
func (n *hostHTTP) roundTripper() *http.Transport {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.transport == nil {
		n.transport = &http.Transport{
			DialContext:           httpDial,
			ForceAttemptHTTP2:     true,
			DisableCompression:    true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	return n.transport
}

// httpDial is the Transport's dial, bounded by the send's connect-timeout when it has one.
func httpDial(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	t, ok := ctx.Value(httpConnectTimeoutKey{}).(time.Duration)
	if !ok {
		return d.DialContext(ctx, network, addr)
	}
	dctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	c, err := d.DialContext(dctx, network, addr)
	if err != nil && errors.Is(dctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, &httpConnectTimeoutError{err: err}
	}
	return c, err
}

// stop is Close's: no send starts after it, every pending round trip is claimed so it resolves nothing, every live
// send's context is cancelled, which ends its round trip and its body's Read, and the idle connections are closed.
//
// NEUTER, run: skip the cancels, and TestCloseEndsASendInFlightAndItsGoroutine's Close does not return within the hang
// guard: it waits in waitAllHTTP for the round trip stalled at the peer.
func (n *hostHTTP) stop() {
	n.mu.Lock()
	n.stopped = true
	pending := n.pending
	n.pending = map[*httpSend]struct{}{}
	live := make([]*httpSend, 0, len(n.live))
	for s := range n.live {
		live = append(live, s)
	}
	n.live = map[*httpSend]struct{}{}
	tr := n.transport
	incoming := make([]*httpIncoming, 0, len(n.incoming))
	for b := range n.incoming {
		incoming = append(incoming, b)
	}
	n.mu.Unlock()
	for s := range pending {
		s.h.mu.Lock()
		s.h.httpPending--
		s.h.mu.Unlock()
	}
	for _, s := range live {
		s.cancel(errHTTPStopped)
	}
	// An incoming request's body Read in flight is ended by a read deadline in the past, so waitAllHTTP does not wait on
	// a client that never sends more (ADR 0137).
	for _, b := range incoming {
		b.abort(nil) // inside Close, after its stop, which already ends the delivery's wait for the slot
	}
	if tr != nil {
		tr.CloseIdleConnections()
	}
}

// addIncoming and dropIncoming register an incoming request's body for stop, over the life of its ServeHTTP.
func (n *hostHTTP) addIncoming(b *httpIncoming) {
	n.mu.Lock()
	n.incoming[b] = struct{}{}
	n.mu.Unlock()
}

func (n *hostHTTP) dropIncoming(b *httpIncoming) {
	n.mu.Lock()
	delete(n.incoming, b)
	n.mu.Unlock()
}

// wait returns once every send goroutine and response body Read has ended.
func (n *hostHTTP) wait() { n.wg.Wait() }

// httpSend is one send: its context, its request body, and once it has one, its response.
type httpSend struct {
	reg         *hostHTTP
	h           *asyncHandles
	res         *hostResources
	ctx         context.Context
	cancelCause context.CancelCauseFunc
	body        *httpReqBody // nil for a request with no contents
	sent        *readableFutureEnd
	resolve     func(canon.Value)
	between     time.Duration // the between-bytes-timeout, 0 for none
	resp        *http.Response
}

// cancel ends the send's context with a cause, and wakes a request body Read waiting for the guest.
func (s *httpSend) cancel(cause error) {
	s.cancelCause(cause)
	if s.body != nil {
		s.body.abort()
	}
}

// dropBody is a send's response let go unread, or its body dropped: the connection is abandoned. It takes no lock but
// the registry's, so it may run under asyncHandles.mu.
func (s *httpSend) dropBody() {
	s.cancelCause(errHTTPBodyDropped)
	s.reg.release(s)
}

// httpSend is `send: async func(request) -> result<response, error-code>`, the request's own its one flat parameter.
//
// **Ungranted, it is `err(HTTP-request-denied)` at the call** (Scott's ruling on #1375), resolved inline, so the lower
// returns RETURNED, and the request's transmission future resolves the same. wasmtime 49.0.3 instead refuses to link
// the component without `-S http`: the named divergence (ADR 0135).
func (h *Host) httpSend(async *asyncHandles, res *hostResources) asyncLowerImpl {
	return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 1 {
			return nil, fmt.Errorf("component: wasi:http send: got %d flat parameters, want 1 (request)", len(args))
		}
		r, err := takeAs[*httpRequest](res, httpRequestResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		ownResp := canon.OwnType(httpResponseResource.rt)
		fail := func(label string) (func(), error) {
			async.mu.Lock()
			r.body.releaseLocked(res.http)
			resolveTransmitLocked(r.sent, httpResultErr(nil, label))
			async.mu.Unlock()
			onResolve(httpResultErr(&ownResp, label))
			return nil, nil
		}
		// NEUTER, run: drop the grant check, and TestASendIsDeniedAtTheCallWithoutTheGrant's guest reaches the peer and
		// prints its "status: 418" where the denial is HttpRequestDenied, in both gate states, and
		// TestAllowHTTPReachesBothConfiguredPaths' two denied rows fail the same way.
		if !h.AllowHTTP {
			return fail("HTTP-request-denied")
		}
		hreq, timeouts, label := r.goRequest()
		if label != "" {
			return fail(label)
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		if t := timeouts[0]; t != nil {
			ctx = context.WithValue(ctx, httpConnectTimeoutKey{}, time.Duration(durationOf(*t)))
		}
		hreq = hreq.WithContext(ctx)
		s := &httpSend{reg: res.http, h: async, res: res, ctx: ctx, cancelCause: cancel, sent: r.sent, resolve: onResolve}
		if t := timeouts[2]; t != nil {
			s.between = time.Duration(durationOf(*t))
		}

		async.mu.Lock()
		body := r.body
		r.body = httpGuestBody{}
		if re := body.contents; re != nil && re.conn != nil && re.conn.producer != nil {
			body.releaseLocked(res.http)
			async.mu.Unlock()
			cancel(nil)
			return nil, fmt.Errorf("%w: wasi:http send of a body stream the host produces; a host-to-host copy is not "+
				"built (ADR 0122)", ErrUnsupportedForm)
		}
		if body.contents != nil {
			s.body = &httpReqBody{h: async, reg: res.http, reader: body.contents, trailers: body.trailers, declared: hreq.Trailer}
			hreq.Body = s.body
			if err := s.body.setupLocked(c); err != nil {
				body.releaseLocked(res.http)
				async.mu.Unlock()
				cancel(nil)
				return nil, err
			}
		} else {
			hreq.ContentLength = 0
		}
		// The trailers future is read at the send's start, and its value is the body's end's (httpReqBody.Read). A
		// request with no contents has its trailers read and dropped.
		take := func(tc *interp.CanonCaller, ptr uint32) error {
			label, f, lerr := liftTrailersValue(tc, res, ptr)
			if lerr != nil {
				return lerr
			}
			if s.body != nil {
				s.body.tSet, s.body.tErr, s.body.tFields = true, label, f
				async.hostIO.Broadcast()
			}
			return nil
		}
		if err := armHostTakeLocked(body.trailers, take); err != nil {
			async.mu.Unlock()
			cancel(nil)
			return nil, err
		}
		async.mu.Unlock()

		ended, ok := res.http.start(s)
		if !ok {
			// Close has stopped the instance's HTTP between the call and here: the send resolves inline, as a dial refused
			// by a stopped registry does, with internal-error, a code no reading fixes.
			cancel(errHTTPStopped)
			async.mu.Lock()
			body.releaseLocked(res.http)
			async.mu.Unlock()
			return fail("internal-error")
		}
		async.mu.Lock()
		async.httpPending++ // a waker for the park bound until the round trip's resolution is stored
		async.mu.Unlock()
		var firstByte *time.Timer
		if t := timeouts[1]; t != nil {
			// NEUTER, run: never arm the timer, and TestP3HTTPMatchesWasmtime's timeout row (and only it) does not return
			// within the hang guard, waiting on the stalled peer, where wasmtime reads ConnectionReadTimeout.
			firstByte = time.AfterFunc(time.Duration(durationOf(*t)), func() { s.cancel(errHTTPFirstByte) })
		}
		tr := res.http.roundTripper()
		go func() {
			defer ended()
			resp, rerr := tr.RoundTrip(hreq)
			if firstByte != nil {
				firstByte.Stop()
			}
			s.finish(resp, rerr)
		}()
		return s.cancelSubtask(), nil
	}
}

// goRequest is the request as net/http's, with its timeouts, or the code a request that cannot be sent is answered
// with inline. The order is wasmtime's as far as it was read: a missing authority or path-with-query is the URI's, and
// then a scheme other than HTTP or HTTPS is a protocol error. A scheme of none is https (p3http's noscheme row).
func (r *httpRequest) goRequest() (hreq *http.Request, timeouts [3]*uint64, label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.options != nil {
		timeouts = r.options.copyOf(true).timeouts
	}
	if r.authority == nil || r.path == nil {
		return nil, timeouts, "HTTP-request-URI-invalid"
	}
	scheme := "https"
	if r.scheme != nil {
		switch r.scheme.label {
		case "HTTP":
			scheme = "http"
		case "HTTPS":
		default:
			return nil, timeouts, "HTTP-protocol-error"
		}
	}
	method := strings.ToUpper(r.method.label)
	if r.method.label == "other" {
		method = r.method.other
	}
	host := *r.authority
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	path, query, hasQuery := strings.Cut(*r.path, "?")
	u := &url.URL{Scheme: scheme, Host: host, Opaque: path, RawQuery: query, ForceQuery: hasQuery && query == ""}
	hreq = &http.Request{Method: method, URL: u, Host: host, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{}}
	// No user-agent unless the guest sets one: a key with no value suppresses net/http's default.
	//
	// NEUTER, run: drop this line, and TestP3HTTPMatchesWasmtime's dump row (and only it) has the peer read
	// User-Agent=Go-http-client/1.1, which wasmtime does not send.
	hreq.Header["User-Agent"] = nil
	for _, g := range r.headers.copyOf(true).groups {
		vs := make([]string, len(g.values))
		for i, v := range g.values {
			vs[i] = string(v)
		}
		switch g.name {
		case "user-agent":
			hreq.Header["User-Agent"] = vs
		case "content-length":
			n, err := strconv.ParseInt(vs[0], 10, 64)
			if err != nil {
				return nil, timeouts, "HTTP-request-length-required"
			}
			hreq.ContentLength = n
		case "trailer":
			// Only the trailers a trailer header declares are sent, hyper's rule as wasmtime reads it (p3http's post and
			// post-declared rows): declared before the send, filled at the body's end.
			//
			// NEUTER, run, both halves: ignore the trailer header, and TestP3HTTPMatchesWasmtime's post-declared row (and only
			// it) reads trailer="" where wasmtime's peer reads "tail". Declare every trailer the guest wrote instead, and its
			// post, big and post-empty rows read trailer="tail" where wasmtime's read "". post-cl passes under both: a
			// fixed-length body carries no trailers.
			for _, v := range vs {
				for _, k := range strings.Split(v, ",") {
					if k = strings.TrimSpace(k); k != "" {
						if hreq.Trailer == nil {
							hreq.Trailer = http.Header{}
						}
						hreq.Trailer[http.CanonicalHeaderKey(k)] = nil
					}
				}
			}
		default:
			hreq.Header[g.name] = vs
		}
	}
	return hreq, timeouts, ""
}

// finish is the round trip's return. Unclaimed, the guest's cancel or Close resolved it already, and a response that
// arrived anyway is let go. Claimed, the subtask resolves with the response, minted here as an own, or the error's
// code, and the transmission future resolves after it.
func (s *httpSend) finish(resp *http.Response, rerr error) {
	if !s.reg.claim(s) {
		if resp != nil {
			_ = resp.Body.Close() // nobody holds it
		}
		s.cancelCause(errHTTPCancelled)
		return
	}
	ownResp := canon.OwnType(httpResponseResource.rt)
	if rerr != nil {
		label := httpErrorLabel(rerr, context.Cause(s.ctx))
		s.cancel(rerr)
		s.reg.release(s)
		s.resolve(httpResultErr(&ownResp, label))
		deliverHTTP(s.h, func() { resolveTransmitLocked(s.sent, httpResultErr(nil, label)) })
		return
	}
	s.resp = resp
	hdr := resp.Header.Clone()
	if len(resp.Trailer) > 0 {
		// net/http moves the trailer header into Response.Trailer; it is put back from the declared keys.
		//
		// NEUTER, run: leave it out, and TestP3HTTPMatchesWasmtime's five posts that reach a response (and only they) read
		// "header trailer: []" where wasmtime reads ["X-Sum"].
		keys := make([]string, 0, len(resp.Trailer))
		for k := range resp.Trailer {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		hdr["Trailer"] = []string{strings.Join(keys, ", ")}
	}
	r := &httpResponse{status: uint16(resp.StatusCode), headers: fieldsOfHeader(hdr), h: s.h, reg: s.reg, host: s}
	handle := canon.Own(httpResponseResource.rt, uint32(s.res.mint(httpResponseResource, r)))
	s.resolve(httpResultOK(&ownResp, &handle))
	deliverHTTP(s.h, func() { resolveTransmitLocked(s.sent, httpResultOK(nil, nil)) })
}

// cancelSubtask is the send subtask's onCancel, run as host code on the canceller's goroutine: a round trip that
// returned first has resolved it, and the cancel does nothing. The transmission future is internal-error, a code no
// reading fixes.
//
// NEUTER, run: return without claiming or resolving, and TestACancelledSendResolvesCancelledAndItsGoroutineEnds' send
// never resolves within its hang guard: the round trip stays stalled at the peer, and nothing ends it.
func (s *httpSend) cancelSubtask() func() {
	return func() {
		if !s.reg.claim(s) {
			return
		}
		s.cancel(errHTTPCancelled)
		s.reg.release(s)
		s.resolve(canon.NoResolution())
		s.h.mu.Lock()
		s.h.httpPending--
		resolveTransmitLocked(s.sent, httpResultErr(nil, "internal-error"))
		s.h.mu.Unlock()
	}
}

// deliverHTTP runs a store under ADR 0101's discipline, sockSend.deliver's: queued for the run slot's holder while it
// is in host code, otherwise under the slot on a chain of its own, and dropped if Close has stopped the table. f runs
// under h.mu; the caller raised h.httpPending, which this lowers whether f runs or not.
func deliverHTTP(h *asyncHandles, f func()) { deliverHTTPUntil(h, f, nil) }

// deliverHTTPUntil is deliverHTTP whose wait for the run slot also ends at `until`, nil for never, and the store is
// then dropped, as at Close's stop. HTTPServer.Close passes its deadline for its let-go (ADR 0139, #1398): the let-go
// runs on Close's own goroutine before its bounded wait, so a guest that answered and then computes without yielding
// held Close here, and `producerStop` closes only at the instance's Close, after that wait.
func deliverHTTPUntil(h *asyncHandles, f func(), until <-chan struct{}) {
	apply := func() {
		h.mu.Lock()
		h.httpPending--
		if !h.producersStopped {
			f()
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	if h.producersStopped {
		h.httpPending--
		h.mu.Unlock()
		return
	}
	if h.window.inHost > 0 {
		h.window.hostStores = append(h.window.hostStores, apply)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	if h.agentsRunFree {
		apply()
		return
	}
	ch := &runChain{}
	if !h.takeRunSlot(ch, nil, until, h.producerStop, nil) {
		h.mu.Lock()
		h.httpPending--
		h.mu.Unlock()
		return
	}
	defer h.releaseRunFor(ch)
	apply()
}

// consumeHostBodyLocked is consume-body of a send's response: ADR 0122's producer over the body, its future the
// trailers. At the end of the body it is `ok(some(trailers))`, the trailers minted as an immutable own at that
// delivery, or `ok(none)` when the peer sent none; at the reader's drop `ok(none)`, as wasmtime 49.0.3 reads it
// (p3http's early-drop row); a Read error its code. `res` is read at once and its value dropped, as wasmtime reads it
// (p3http's res-first row). The caller holds asyncHandles.mu.
func (s *httpSend) consumeHostBodyLocked(res *hostResources, resEnd *readableFutureEnd) (si, ti int, err error) {
	opt := canon.OptionType(fieldsOwnT())
	none := optionValue(fieldsOwnT(), nil)
	p := &streamProducer{
		h:     s.h,
		src:   &httpRespBody{r: s.resp.Body, send: s},
		ok:    httpResultOK(&opt, &none),
		track: s.reg.track,
		mapErr: func(rerr error) canon.Value {
			return httpResultErr(&opt, httpErrorLabel(rerr, context.Cause(s.ctx)))
		},
		atEnd: func() canon.Value { return s.trailersValue(res) },
		// A body done or dropped lets the connection go: at the end it is back in the pool already, and at a drop it is
		// abandoned. Neither takes a lock but the registry's, so it may run under asyncHandles.mu.
		onDone: func(readerDropped bool) {
			if readerDropped {
				s.dropBody()
				return
			}
			s.reg.release(s)
		},
	}
	si, ti = p.mintLocked()
	err = armHostTakeLocked(resEnd, func(c *interp.CanonCaller, ptr uint32) error {
		_, lerr := liftTransmitValue(c, ptr)
		return lerr
	})
	return si, ti, err
}

// trailersValue is the trailers future's value at the end of the body: the trailers the peer sent, as an immutable own
// minted now, or none. Run under asyncHandles.mu and the run slot, by the delivery that saw the end; the body's Read
// filled Response.Trailer before it returned the end.
func (s *httpSend) trailersValue(res *hostResources) canon.Value {
	opt := canon.OptionType(fieldsOwnT())
	sent := http.Header{}
	for k, vs := range s.resp.Trailer {
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

// httpRespBody is a response body as the producer Reads it, each Read bounded by the between-bytes-timeout when the
// request has one.
type httpRespBody struct {
	r    io.Reader
	send *httpSend
}

func (b *httpRespBody) Read(p []byte) (int, error) {
	if b.send.between <= 0 {
		return b.r.Read(p)
	}
	t := time.AfterFunc(b.send.between, func() { b.send.cancelCause(errHTTPBetweenBytes) })
	n, err := b.r.Read(p)
	t.Stop()
	return n, err
}

// httpReqBody is a request's body as net/http Reads it: the guest's stream, read by the host into a bounded buffer,
// and the trailers future's value at its end. Every field but declared is guarded by h.mu; declared is the request's
// Trailer, written only by Read, on the Transport's writing goroutine, which writes it after the body.
type httpReqBody struct {
	h        *asyncHandles
	reg      *hostHTTP
	reader   *readableStreamEnd
	trailers *readableFutureEnd
	declared http.Header
	buf      []byte
	// closing is the guest's writer dropped; stopping a cancel or Close, closed net/http's Close; arming a re-arm queued;
	// eof the end returned.
	closing, stopping, closed, arming, eof bool
	// The trailers future's value once taken: set, an error's code ("" for ok), and the fields.
	tSet    bool
	tErr    string
	tFields *httpFields
	// taskGone is the writer's task ended, for a response body the host is serving (ADR 0137): a body that has not
	// ended by then ends with what was written and no trailers, wasmtime 49.0.3's reading of a trap after the answer.
	taskGone bool
}

// taskEnded is the serving host's word that the task writing this body has exited or trapped: Read ends the body once
// what it holds is read (ADR 0137).
func (b *httpReqBody) taskEnded() {
	b.h.mu.Lock()
	b.taskGone = true
	if b.h.hostIO != nil {
		b.h.hostIO.Broadcast()
	}
	b.h.mu.Unlock()
}

func (b *httpReqBody) free() int { return httpBodyBufCap - len(b.buf) }

// setupLocked registers the host's read of the guest's stream, tcp-socket.send's: the copy appends to the buffer, a
// COMPLETED copy re-arms at the room left, and the writer's drop is the body's end. The caller holds h.mu and the run
// slot, since a write already parked is copied now.
func (b *httpReqBody) setupLocked(c *interp.CanonCaller) error {
	h, re := b.h, b.reader
	if h.hostIO == nil {
		h.hostIO = sync.NewCond(&h.mu)
	}
	if re.writerDropped {
		b.closing = true
		return nil
	}
	re.hostSink = func(p []byte) error {
		b.buf = append(b.buf, p...)
		h.hostIO.Broadcast()
		return nil
	}
	re.onHostComplete = func(r copyResult) {
		if r == copyCompleted {
			re.resolved, re.delivered, re.progress = false, false, 0
			if room := b.free(); room > 0 {
				re.readN = uint32(room)
			} else {
				re.hasRead = false
			}
			return
		}
		b.closing = true // DROPPED: the guest's writer is gone
		h.hostIO.Broadcast()
	}
	re.caller, re.state = c, copyStateCopying
	return b.armLocked()
}

// armLocked re-arms the host's read at the room the buffer has, sockSend.armLocked's: with a guest write parked, its
// copy runs now, reading guest memory, so the caller holds the run slot as well as h.mu.
func (b *httpReqBody) armLocked() error {
	re := b.reader
	if b.closing || b.stopping || b.closed {
		return nil
	}
	re.resolved, re.delivered, re.progress = false, false, 0
	room := b.free()
	if room <= 0 {
		re.hasRead = false
		return nil
	}
	re.readN, re.hasRead = uint32(room), true
	if w := re.conn; w != nil && w.hasWrite && !w.resolved {
		progress, err := driveStreamCopyLocked(re, w)
		if err != nil {
			return err
		}
		w.completeWriteLocked(progress, copyCompleted)
		re.armLocked(progress, copyCompleted)
	}
	return nil
}

// Read is net/http's read of the body. It waits on the instance's host-I/O condition for bytes, the writer's drop, or
// a stop. The room a Read frees is re-armed by a delivery, under ADR 0101's discipline, since a parked guest write's
// copy reads guest memory. At the end it waits for the trailers future: an error fails the request with its code, and
// otherwise the declared trailers are filled.
//
// NEUTER, run: return io.EOF at the writer's drop without waiting for the trailers, and
// TestARequestBodysEndWaitsForItsTrailers' Read returns the end before the trailers arrive. TestP3HTTPMatchesWasmtime
// passes under it: its guest writes the trailers before net/http's Read reaches the end, so the race is never lost.
func (b *httpReqBody) Read(p []byte) (int, error) {
	h := b.h
	h.mu.Lock()
	for {
		if b.stopping || b.closed || h.producersStopped {
			h.mu.Unlock()
			return 0, errHTTPBodyAborted
		}
		if len(b.buf) > 0 {
			n := copy(p, b.buf)
			b.buf = b.buf[n:]
			if len(b.buf) == 0 {
				b.buf = nil
			}
			if !b.closing && !b.arming {
				b.arming = true
				h.httpPending++
				h.mu.Unlock()
				deliverHTTP(h, func() {
					b.arming = false
					if err := b.armLocked(); err != nil {
						b.stopping = true // the copy failed: the request fails, and the guest is told by its write
					}
					if b.taskGone {
						h.hostIO.Broadcast() // a Read waiting on the re-arm to judge the end (below)
					}
				})
				return n, nil
			}
			h.mu.Unlock()
			return n, nil
		}
		if b.taskGone && !b.arming && (!b.closing || !b.tSet) {
			// **The writer's task has ended and the body has not** (ADR 0137): what it wrote has been read, and no
			// re-arm is queued that could copy more, so the body ends here, cleanly and with no trailers, as wasmtime
			// 49.0.3 ends one whose guest trapped after its answer (p3serve's trap-after row).
			//
			// NEUTER, run: wait on instead, and TestP3ServeMatchesWasmtime's trap-after row reads "partial" and then
			// "body error: the guard expired" where wasmtime's body ends: the trapped guest will never drop its writer.
			//
			// eof is left unset, so Close unlinks the stream the guest never finished.
			b.tFields = nil
			h.mu.Unlock()
			return 0, io.EOF
		}
		if b.closing && b.tSet {
			if b.tErr != "" {
				h.mu.Unlock()
				return 0, &httpCodeError{label: b.tErr}
			}
			if b.declared != nil && b.tFields != nil {
				for k := range b.declared {
					b.tFields.mu.Lock()
					if i := b.tFields.indexLocked(strings.ToLower(k)); i >= 0 {
						vs := make([]string, len(b.tFields.groups[i].values))
						for j, v := range b.tFields.groups[i].values {
							vs[j] = string(v)
						}
						b.declared[k] = vs
					}
					b.tFields.mu.Unlock()
				}
			}
			b.eof = true
			h.mu.Unlock()
			return 0, io.EOF
		}
		h.hostIO.Wait()
	}
}

// abort is a cancel's: a Read waiting for the guest returns.
func (b *httpReqBody) abort() {
	b.h.mu.Lock()
	b.stopping = true
	if b.h.hostIO != nil {
		b.h.hostIO.Broadcast()
	}
	b.h.mu.Unlock()
}

// Close is net/http's, which it calls once the body is no longer needed, error or not. A body it did not read to the
// end lets the guest's ends go, by a delivery: the stream's writes complete DROPPED, and the trailers future's reader
// is dropped.
func (b *httpReqBody) Close() error {
	b.closeUntil(nil)
	return nil
}

// closeUntil is Close whose delivery's wait for the run slot ends at `until` (deliverHTTPUntil): HTTPServer.Close's
// deadline, for its let-go (ADR 0139), or nil for never.
func (b *httpReqBody) closeUntil(until <-chan struct{}) {
	h := b.h
	h.mu.Lock()
	if b.closed {
		h.mu.Unlock()
		return
	}
	b.closed = true
	h.hostIO.Broadcast()
	done := b.eof
	if !done {
		h.httpPending++
	}
	h.mu.Unlock()
	b.reg.unhold(b.reader)
	if !done {
		deliverHTTPUntil(h, func() {
			unlinkHostReadLocked(b.reader)
			if !b.tSet {
				hostDropFutureReaderLocked(b.trailers)
			}
		}, until)
	}
}

// httpErrorLabel maps a round trip's or a body Read's error to its error-code case (ADR 0135's table). cause is the
// send's context's, which names a timeout the host set.
func httpErrorLabel(err, cause error) string {
	switch {
	case errors.Is(cause, errHTTPFirstByte), errors.Is(cause, errHTTPBetweenBytes):
		// NEUTER, run: drop this arm, and TestP3HTTPMatchesWasmtime's timeout row (and only it) reads InternalError, its
		// send and its transmission future both, where wasmtime reads ConnectionReadTimeout.
		return "connection-read-timeout"
	}
	var ce *httpCodeError
	if errors.As(err, &ce) {
		return ce.label
	}
	var ct *httpConnectTimeoutError
	if errors.As(err, &ct) {
		return "connection-timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.IsTimeout {
			return "DNS-timeout"
		}
		return "DNS-error"
	}
	var rh tls.RecordHeaderError
	var alert tls.AlertError
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	switch {
	case errors.As(err, &verify), errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &hostname):
		return "TLS-certificate-error"
	case errors.As(err, &alert):
		return "TLS-alert-received"
	case errors.As(err, &rh):
		return "TLS-protocol-error"
	}
	switch sockErrorLabel(err) {
	case "connection-refused":
		return "connection-refused"
	case "connection-reset", "connection-aborted", "connection-broken":
		return "connection-terminated"
	case "timeout":
		return "connection-timeout"
	case "remote-unreachable":
		return "destination-unavailable"
	}
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "HTTP-response-incomplete"
	case errors.Is(err, io.EOF):
		return "connection-terminated"
	case strings.Contains(err.Error(), "malformed HTTP"):
		return "HTTP-protocol-error"
	}
	return "internal-error"
}
