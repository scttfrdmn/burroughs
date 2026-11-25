// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// httpPeerMux is p3http's peer for Burroughs' rows, and testdata/p3http/peer's twin, which serves the oracle's: the
// two must answer alike, so a route changed in one is changed in the other. /dump writes the Host it read with the
// port replaced by PORT, so no port reaches a reading. hello, when not nil, replaces /hello's handler.
func httpPeerMux(port string, hello http.HandlerFunc) *http.ServeMux {
	m := http.NewServeMux()
	if hello == nil {
		hello = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Peer", "hello")
			fmt.Fprintf(w, "hello, %s %s", r.Method, r.URL.Path)
		}
	}
	m.HandleFunc("/hello", hello)
	m.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Trailer", "X-Sum")
		fmt.Fprintf(w, "method=%s len=%d te=%v cl=%d trailer=%q hdr=%q", r.Method, len(b), r.TransferEncoding,
			r.ContentLength, r.Trailer.Get("X-Req-Trailer"), r.Header.Get("X-Req"))
		w.Header().Set("X-Sum", strconv.Itoa(len(b)))
	})
	m.HandleFunc("/dump", func(w http.ResponseWriter, r *http.Request) {
		var hs []string
		for k, v := range r.Header {
			hs = append(hs, k+"="+strings.Join(v, ","))
		}
		sort.Strings(hs)
		fmt.Fprintf(w, "method=%s uri=%s host=%s proto=%s hdrs=%v", r.Method, r.RequestURI,
			strings.Replace(r.Host, port, "PORT", 1), r.Proto, hs)
	})
	m.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		b := make([]byte, 1<<20)
		for i := range b {
			b[i] = byte('a' + i%26)
		}
		_, _ = w.Write(b)
	})
	m.HandleFunc("/stall", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	return m
}

// httpPeer is a running peer: its port, and a port it bound and closed, so a send there is refused.
type httpPeer struct {
	srv          *httptest.Server
	port, closed string
}

func startHTTPPeer(t *testing.T, hello http.HandlerFunc) *httpPeer {
	t.Helper()
	gone, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := strconv.Itoa(gone.Addr().(*net.TCPAddr).Port)
	_ = gone.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	srv := httptest.NewUnstartedServer(httpPeerMux(port, hello))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	// The client connections first: a witness whose Close failed leaves a request stalled at the peer, and Close alone
	// would wait for it, turning the witness's failure into a hang of the whole binary.
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return &httpPeer{srv: srv, port: port, closed: closed}
}

// p3httpRow is one row of p3http.reading: its stdout lines, a failed run's cause, and its exit code.
type p3httpRow struct {
	stdout []string
	cause  string
	exit   int
}

// readP3HTTPReading parses testdata/p3http.reading into its rows, in order.
func readP3HTTPReading(t *testing.T) ([]string, map[string]*p3httpRow) {
	t.Helper()
	b, err := os.ReadFile("testdata/p3http.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	var order []string
	rows := map[string]*p3httpRow{}
	var cur *p3httpRow
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "row: "):
			name := strings.TrimPrefix(ln, "row: ")
			cur = &p3httpRow{exit: -1}
			rows[name] = cur
			order = append(order, name)
		case cur == nil || strings.HasPrefix(ln, "#"):
		case strings.HasPrefix(ln, "stdout: "):
			cur.stdout = append(cur.stdout, strings.TrimPrefix(ln, "stdout: "))
		case strings.HasPrefix(ln, "cause: "):
			cur.cause = strings.TrimPrefix(ln, "cause: ")
		case strings.HasPrefix(ln, "exit: "):
			code, cerr := strconv.Atoi(strings.TrimPrefix(ln, "exit: "))
			if cerr != nil {
				t.Fatalf("p3http.reading's exit line %q: %v", ln, cerr)
			}
			cur.exit = code
		}
	}
	for name, r := range rows {
		if r.exit < 0 {
			t.Fatalf("p3http.reading's row %s has no exit line", name)
		}
	}
	return order, rows
}

// runP3HTTP runs one p3http row on Burroughs and returns its stdout lines and its run's error.
func runP3HTTP(t *testing.T, grant bool, args ...string) ([]string, int, error) {
	t.Helper()
	b, err := os.ReadFile("testdata/p3http/p3http.wasm")
	if err != nil {
		t.Fatalf("p3http fixture missing (it is committed): %v", err)
	}
	var out, errb bytes.Buffer
	h := NewHost(&out, &errb, nil)
	h.Args = append([]string{"p3http.wasm"}, args...)
	h.AllowHTTP = grant
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("p3http did not instantiate: %v", err)
	}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() { code, e := in.CallRun(); done <- result{code, e} }()
	var code int
	var rerr error
	select {
	case r := <-done:
		code, rerr = r.code, r.err
	case <-time.After(20 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("p3http's %s row did not return within the 20s hang guard; stdout so far: %q", args[0], out.String())
	}
	in.Close()
	if rerr == nil && errb.Len() != 0 {
		t.Errorf("stderr = %q, want nothing, as wasmtime writes", errb.String())
	}
	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if out.Len() == 0 {
		got = nil
	}
	return got, code, rerr
}

// httpGoroutines are the goroutines wasi:http's sends and response bodies run on, by the function that started each:
// a send's, and a producer's whose Read is a response body's.
func httpGoroutines() []string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		created := ""
		for _, ln := range strings.Split(g, "\n") {
			if strings.HasPrefix(ln, "created by ") {
				created = ln
			}
		}
		if strings.Contains(created, "(*Host).httpSend") ||
			(strings.Contains(created, "(*streamProducer).startReadLocked") && strings.Contains(g, "(*httpRespBody).Read")) {
			out = append(out, g)
		}
	}
	return out
}

// requireNoHTTPGoroutines fails if a send's or a response body's goroutine is still running, after a hang guard for
// one that has called its ended and not yet returned.
func requireNoHTTPGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second) // a harness hang guard, not a measurement
	for {
		gs := httpGoroutines()
		if len(gs) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d wasi:http goroutine(s) outlived Close:\n%s", len(gs), strings.Join(gs, "\n\n"))
		}
		runtime.Gosched()
	}
}

// TestP3HTTPMatchesWasmtime is E2-out's witness (#1383, ADR 0135): p3http, a Rust wasm32-wasip3 guest with
// wit-bindgen 0.61.1 bindings, drives wasi:http@0.3's four resources, request.new and the consume-bodies with their
// streams and futures, and client.send against httpPeerMux on 127.0.0.1. Every row's stdout and exit code must equal
// wasmtime 49.0.3's under `-S http` (testdata/p3http.reading), read through CallRun as Run reads them, with
// gate:async on and gate:cm-threads off and on. A row wasmtime ends with a trap must trap here, naming the same rule.
// The denied row, wasmtime's link failure, is TestASendIsDeniedAtTheCallWithoutTheGrant's. After Close no send or
// response-body goroutine is left running.
//
// The neuters each ran, and each is recorded at its site as a `NEUTER, run:` comment.
func TestP3HTTPMatchesWasmtime(t *testing.T) {
	order, rows := readP3HTTPReading(t)
	for _, gate := range []string{"0", "1"} {
		for _, name := range order {
			if name == "denied" {
				continue
			}
			want := rows[name]
			t.Run(name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				peer := startHTTPPeer(t, nil)
				got, code, err := runP3HTTP(t, true, name, peer.port, peer.closed)
				if want.cause != "" {
					var trap *interp.Trap
					if !errors.As(err, &trap) {
						t.Fatalf("the run ended with %v, want a trap: wasmtime's was %q", err, want.cause)
					}
					// wasmtime names the intra-component rule; Burroughs' trap names the pin's, End.copy's.
					if strings.Contains(want.cause, "non-numeric payload") && !strings.Contains(trap.Reason, "non-numeric payload") {
						t.Errorf("trap = %q, want one naming the non-numeric payload rule wasmtime's names: %q", trap.Reason, want.cause)
					}
				} else if err != nil {
					t.Fatalf("p3http did not run to its exit: %v\nstdout so far: %q", err, got)
				} else if code != want.exit {
					t.Errorf("exit code = %d, want wasmtime's %d", code, want.exit)
				}
				if !equalLines(got, want.stdout) {
					t.Errorf("stdout differs from wasmtime 49.0.3's reading.\n got:\n  %s\nwant:\n  %s",
						strings.Join(got, "\n  "), strings.Join(want.stdout, "\n  "))
				}
				requireNoHTTPGoroutines(t)
			})
		}
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestASendIsDeniedAtTheCallWithoutTheGrant is Scott's ruling on #1375 (ADR 0135): with AllowHTTP false, the zero
// Host, p3http instantiates, its resources work, and send answers `HTTP-request-denied` at the call, as does the
// request's transmission future. **wasmtime 49.0.3 diverges**, and the reading records how: without `-S http` the
// component does not link, the run's cause "resource implementation is missing", exit 1.
func TestASendIsDeniedAtTheCallWithoutTheGrant(t *testing.T) {
	_, rows := readP3HTTPReading(t)
	denied := rows["denied"]
	if denied == nil || denied.exit != 1 || denied.cause != "resource implementation is missing" || len(denied.stdout) != 0 {
		t.Fatalf("p3http.reading's denied row = %+v, want wasmtime's link failure: no stdout, the missing resource, exit 1", denied)
	}
	want := []string{
		`request headers set: Err("HeaderError::Immutable")`,
		`send: err ErrorCode::HttpRequestDenied`,
		`sent: Err("ErrorCode::HttpRequestDenied")`,
	}
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			arrived := make(chan struct{}, 1)
			peer := startHTTPPeer(t, func(w http.ResponseWriter, _ *http.Request) {
				arrived <- struct{}{}
				w.WriteHeader(http.StatusTeapot)
			})
			got, code, err := runP3HTTP(t, false, "get", peer.port, peer.closed)
			if err != nil || code != 0 {
				t.Fatalf("the denied run = (%d, %v), want exit 0", code, err)
			}
			if !equalLines(got, want) {
				t.Errorf("stdout:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
			select {
			case <-arrived:
				t.Error("the peer was reached by a send the grant denies")
			default:
			}
		})
	}
}

// TestCloseEndsASendInFlightAndItsGoroutine: an instance closed while a send's round trip waits on a peer that never
// answers ends the round trip and its goroutine, and Close returns. The peer's /hello signals the request's arrival,
// which is the place the Close is made at.
func TestCloseEndsASendInFlightAndItsGoroutine(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	arrived := make(chan struct{}, 1)
	peer := startHTTPPeer(t, func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	})
	b, err := os.ReadFile("testdata/p3http/p3http.wasm")
	if err != nil {
		t.Fatalf("p3http fixture missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.Args = []string{"p3http.wasm", "get", peer.port, peer.closed}
	h.AllowHTTP = true
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("p3http did not instantiate: %v", err)
	}
	ran := make(chan error, 1)
	go func() { _, e := in.CallRun(); ran <- e }()
	select {
	case <-arrived:
	case <-time.After(20 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the send never reached the peer")
	}
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(20 * time.Second): // a harness hang guard
		t.Fatal("Close did not return with a send in flight")
	}
	select {
	case <-ran:
	case <-time.After(20 * time.Second): // a harness hang guard
		t.Fatal("the run did not end at Close")
	}
	requireNoHTTPGoroutines(t)
}

// TestACancelledSendResolvesCancelledAndItsGoroutineEnds drives send's impl directly: a send whose round trip waits on
// a peer that never answers is cancelled at the place the peer has the request, and its subtask resolves with no
// value, the cancel's, and its goroutine ends.
func TestACancelledSendResolvesCancelledAndItsGoroutineEnds(t *testing.T) {
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer func() { // the client connections first, startHTTPPeer's reason
		srv.CloseClientConnections()
		srv.Close()
	}()
	h := NewHost(io.Discard, io.Discard, nil)
	h.AllowHTTP = true
	async, res := newAsyncHandles(), newHostResources()
	authority, path := srv.Listener.Addr().String(), "/"
	r := &httpRequest{
		method: httpCase{label: "get"}, scheme: &httpCase{label: "HTTP"}, authority: &authority, path: &path,
		headers: &httpFields{}, h: async, reg: res.http,
	}
	idx := res.mint(httpRequestResource, r)
	resolved := make(chan canon.Value, 1)
	cancel, err := h.httpSend(async, res)(nil, func() []interp.Value { return []interp.Value{interp.I32(idx)} },
		func(v canon.Value) { resolved <- v })
	if err != nil || cancel == nil {
		t.Fatalf("send = (%v, %v), want a pending subtask with its cancel", cancel != nil, err)
	}
	select {
	case <-arrived:
	case <-time.After(20 * time.Second): // a harness hang guard
		t.Fatal("the send never reached the peer")
	}
	cancel()
	select {
	case v := <-resolved:
		if !v.IsNoResolution() {
			t.Errorf("the cancelled send resolved with %s, want no value, the cancel's", v.Type)
		}
	case <-time.After(20 * time.Second): // a harness hang guard
		t.Fatal("the cancelled send never resolved")
	}
	requireNoHTTPGoroutines(t)
	res.http.stop()
	res.http.wait()
}

// TestARequestBodysEndWaitsForItsTrailers: a request body whose guest writer has dropped does not end until the
// trailers future's value arrives, since the declared trailers are filled from it. The witness asserts a place: the
// Read parked in the host-I/O condition's wait, found in its stack. Then the trailers arrive, and the Read returns the
// end with the declared trailer filled. p3http cannot see this: its guest writes its trailers before net/http's Read
// reaches the end.
func TestARequestBodysEndWaitsForItsTrailers(t *testing.T) {
	async := newAsyncHandles()
	async.hostIO = sync.NewCond(&async.mu)
	b := &httpReqBody{h: async, reg: newHostHTTP(), reader: &readableStreamEnd{}, closing: true, declared: http.Header{"X-T": nil}}
	done := make(chan error, 1)
	go func() {
		_, err := b.Read(make([]byte, 8))
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second) // a harness hang guard, not a measurement
	for !goroutineAt("(*httpReqBody).Read", "sync.(*Cond).Wait") {
		if time.Now().After(deadline) {
			select {
			case err := <-done:
				t.Fatalf("the body's Read returned %v before its trailers arrived", err)
			default:
				t.Fatal("the body's Read never parked in the host-I/O wait")
			}
		}
		runtime.Gosched()
	}
	async.mu.Lock()
	b.tSet, b.tFields = true, &httpFields{groups: []fieldGroup{{name: "x-t", values: [][]byte{[]byte("v")}}}}
	async.hostIO.Broadcast()
	async.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Read = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second): // a harness hang guard
		t.Fatal("the body's Read did not return once its trailers arrived")
	}
	if got := b.declared["X-T"]; len(got) != 1 || got[0] != "v" {
		t.Errorf("the declared trailer = %q, want [\"v\"]", got)
	}
}

// goroutineAt reports a goroutine whose stack holds both frames.
func goroutineAt(frames ...string) bool {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		all := true
		for _, f := range frames {
			all = all && strings.Contains(g, f)
		}
		if all {
			return true
		}
	}
	return false
}

// TestAFutureHandedToTheHostTwiceTraps: a future argument lifted once is the host's, so a second lift of the same
// end, as a second request.new would make, traps as an index the guest no longer holds (def:Table.get), where the pin
// removed the handle at the first. A future of another payload traps as the pin's type check does.
func TestAFutureHandedToTheHostTwiceTraps(t *testing.T) {
	async := newAsyncHandles()
	want := elemOfCanon(trailersPayloadType())
	async.mu.Lock()
	defer async.mu.Unlock()
	fe := &readableFutureEnd{elem: want}
	fe.index = async.addLocked(fe)
	if _, err := liftFutureArgLocked(async, "request.new", uint32(fe.index), want); err != nil {
		t.Fatalf("the first lift: %v", err)
	}
	var trap *interp.Trap
	if _, err := liftFutureArgLocked(async, "request.new", uint32(fe.index), want); !errors.As(err, &trap) ||
		!strings.Contains(trap.Reason, "handed to the host already") {
		t.Errorf("the second lift = %v, want a trap naming the end as the host's", err)
	}
	other := &readableFutureEnd{elem: elemOfCanon(transmitPayloadType())}
	other.index = async.addLocked(other)
	if _, err := liftFutureArgLocked(async, "request.new", uint32(other.index), want); !errors.As(err, &trap) ||
		!strings.Contains(trap.Reason, "different type") {
		t.Errorf("a lift of another payload = %v, want the type check's trap", err)
	}
}

// TestADeclaredSignatureMustMatchTheHosts: an import whose whole type the host declares (ADR 0135) is refused at
// instantiate when the guest declares another. http-declared-sig's fields.has returns a u32 where the host lowers a
// bool; its handles are the host's, so only the whole-type check can refuse it.
func TestADeclaredSignatureMustMatchTheHosts(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/http-declared-sig.wasm")
	if err != nil {
		t.Fatalf("http-declared-sig fixture missing (it is committed): %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err == nil {
		in.Close()
	}
	if !errors.Is(err, ErrLinkRefused) || !strings.Contains(err.Error(), "fields.has") {
		t.Fatalf("instantiate = %v, want ErrLinkRefused naming fields.has", err)
	}
}
