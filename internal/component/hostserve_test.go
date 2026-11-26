// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// E2-in-b's lifecycle witnesses (#1385, ADR 0137): the pool's reuse, an abandoned request's task, and Close. The
// oracle rows are p3serve_test.go's.

// waitServedTasks waits until every task the server holds has ended and released its instance, up to the guard. A
// task not in the set has released already: run releases before it leaves the set.
func waitServedTasks(t *testing.T, srv *HTTPServer) {
	t.Helper()
	srv.mu.Lock()
	qs := make([]*serveRequest, 0, len(srv.reqs))
	for q := range srv.reqs {
		qs = append(qs, q)
	}
	srv.mu.Unlock()
	guard := time.NewTimer(p3serveGuard)
	defer guard.Stop()
	for _, q := range qs {
		select {
		case <-q.ended:
		case <-guard.C:
			t.Fatalf("a served task did not exit within %s", p3serveGuard)
		}
	}
}

// heldEcho is a POST /echo whose body stays open: once its response head has arrived, its guest has answered and is
// parked reading a body only the client can finish, so its task has returned and not exited.
type heldEcho struct {
	c    net.Conn
	resp *http.Response
}

func holdEcho(t *testing.T, a *serveArm) *heldEcho {
	t.Helper()
	c, err := a.dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, werr := io.WriteString(c, "POST /echo HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n\r\n"); werr != nil {
		t.Fatal(werr)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil) //nolint:bodyclose // finish reads it to its end, or the cleanup closes its connection
	if err != nil {
		t.Fatalf("the held echo's response head: %v", err)
	}
	return &heldEcho{c: c, resp: resp}
}

// finish writes the held body's last chunk and reads the echo.
func (h *heldEcho) finish(t *testing.T, part string) {
	t.Helper()
	if _, err := fmt.Fprintf(h.c, "%x\r\n%s\r\n0\r\n\r\n", len(part), part); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(h.resp.Body)
	if err != nil || string(b) != part {
		t.Fatalf("the held echo read %q, %v; want %q", b, err, part)
	}
}

func hello(t *testing.T, a *serveArm) {
	t.Helper()
	ls, err := a.exchange("GET /hello HTTP/1.1\r\nHost: p3serve.test\r\n\r\n")
	if err != nil || len(ls) == 0 || ls[0] != "status: 200 OK" {
		t.Fatalf("GET /hello: %v, %v", ls, err)
	}
}

// TestAServedInstanceIsReusedOnlyAfterItsTaskExits is the pool's witness (ADR 0137), by **places**, never durations:
//
//   - sequential requests share one instance: after two, the server holds one, idle;
//   - a request arriving while another's task has answered and not exited runs on a second instance. wasmtime counts
//     its concurrency to task.return, and would send it into the first (#1387's probe);
//   - no more than serveIdleMax instances are kept idle: after serveIdleMax+1 held requests finish, that many remain.
//
// NEUTER, run: release always closes, and the first check reads 0 instances where it wants 1. Release an instance at
// its answer instead of at its exit, wasmtime's count, and the hello sent while the echo is held enters the echo's
// instance and is answered with the 500 page: p3serve fails with a second task beside one that has returned and not
// exited on Burroughs too, though its context slots are per task here.
func TestAServedInstanceIsReusedOnlyAfterItsTaskExits(t *testing.T) {
	a := startBurroughsServe(t, "1")
	hello(t, a.serveArm)
	waitServedTasks(t, a.srv) // so the second request finds the first's instance released
	hello(t, a.serveArm)
	waitServedTasks(t, a.srv)
	if live, idle := a.srv.instances(); live != 1 || idle != 1 {
		t.Fatalf("after two sequential requests the server holds %d instances, %d idle; want 1, 1", live, idle)
	}

	held := holdEcho(t, a.serveArm)
	hello(t, a.serveArm)
	// The hello's instance is live whether its task has released it yet or not, so no wait is needed for the count.
	if live, _ := a.srv.instances(); live != 2 {
		t.Fatalf("a request served while another's task had answered and not exited left %d instances; want 2, the "+
			"held one kept out of reuse", live)
	}
	held.finish(t, "held")
	waitServedTasks(t, a.srv)

	hs := make([]*heldEcho, serveIdleMax+1)
	for i := range hs {
		hs[i] = holdEcho(t, a.serveArm)
	}
	if live, idle := a.srv.instances(); live != serveIdleMax+1 || idle != 0 {
		t.Fatalf("%d held requests are on %d instances, %d idle; want %d, 0", len(hs), live, idle, serveIdleMax+1)
	}
	for i, h := range hs {
		h.finish(t, fmt.Sprintf("body %d", i))
	}
	waitServedTasks(t, a.srv)
	if live, idle := a.srv.instances(); live != serveIdleMax || idle != serveIdleMax {
		t.Fatalf("after %d requests finished the server holds %d instances, %d idle; want %d of each", len(hs), live,
			idle, serveIdleMax)
	}
}

// TestServingAnAbandonedRequestLetsItsTaskExit is p3serve's sleep row, and then its task's exit (ADR 0137): the
// client goes away while the handler waits, the handler answers later, and then writes a body nobody reads. The
// answer is let go when it arrives, so the body's write is told its reader has gone and **the task exits cleanly**:
// its call returns nil. **The places**: the guest's own lines that it entered and that its wait ended, and then the
// task's end, whose verdict is read, not its duration.
//
// NEUTER, run: dropAnswer lets the answer lie, and the task's call returns the park's bound, "async-lift park expired
// with no event", where it wants nil: its body's write was parked with nothing to read it.
func TestServingAnAbandonedRequestLetsItsTaskExit(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			a := startBurroughsServe(t, gate)
			from := a.stderr.len()
			c, err := a.dial()
			if err != nil {
				t.Fatal(err)
			}
			if _, werr := io.WriteString(c, "GET /sleep HTTP/1.1\r\nHost: p3serve.test\r\n\r\n"); werr != nil {
				t.Fatal(werr)
			}
			if _, next, werr := a.stderr.waitPrefix(from, "sleep: the handler entered"); werr != nil {
				t.Fatal(werr)
			} else {
				from = next
			}
			a.srv.mu.Lock()
			var q *serveRequest
			for r := range a.srv.reqs {
				q = r
			}
			a.srv.mu.Unlock()
			if q == nil {
				t.Fatal("no served task in flight while the handler waits")
			}
			c.Close() // the client goes away
			line, _, err := a.stderr.waitPrefix(from, "sleep: ")
			if err != nil {
				t.Fatal(err)
			}
			if line != "sleep: the wait ended and the handler returned" {
				t.Fatalf("the guest said %q", line)
			}
			select {
			case <-q.ended:
			case <-time.After(p3serveGuard):
				t.Fatalf("the abandoned request's task did not exit within %s", p3serveGuard)
			}
			q.mu.Lock()
			terr := q.taskErr
			q.mu.Unlock()
			if terr != nil {
				t.Fatalf("the abandoned request's task ended with %v; want nil, its body's write told the reader had gone", terr)
			}
			if live, idle := a.srv.instances(); live != 0 || idle != 0 {
				t.Fatalf("the abandoned request's instance is held (%d live, %d idle); want it closed, not reused", live, idle)
			}
		})
	}
}

// TestAResponseBodyRecordedAfterCloseIsLetGoAtOnce is the response half of abortBodies' stickiness (ADR 0137's
// postscript), at the place #1401's CI run found by chance: Close has let a request's bodies go after its response's
// head left and before ServeHTTP recorded the body it copies. The body recorded then must be let go at once, so the
// guest's writer is told its reader has gone, which is what lets a guest writing a body nobody reads exit.
func TestAResponseBodyRecordedAfterCloseIsLetGoAtOnce(t *testing.T) {
	q := &serveRequest{incoming: &httpIncoming{}}
	q.abortBodies(nil) // Close's let-go, with no body recorded yet
	h := newAsyncHandles()
	h.hostIO = sync.NewCond(&h.mu)
	w := &writableStreamEnd{}
	re := &readableStreamEnd{conn: w}
	w.conn = re
	b := &httpReqBody{h: h, reg: newHostHTTP(), reader: re}
	q.setBody(b)
	h.mu.Lock()
	closed, dropped := b.closed, w.readerDropped
	h.mu.Unlock()
	if !closed || !dropped {
		t.Fatalf("a response body recorded after Close let the bodies go: closed %v, its guest writer told the reader "+
			"had gone %v; want both", closed, dropped)
	}
}

// TestCloseEndsATaskAwaitingTheTrailersOfABodyItStoppedReading is the request half of Close's let-go where no Read is
// in flight to end (ADR 0137's postscript). An echo's client streams its body and reads none of the response, so
// ServeHTTP is held in a Write, the host stops reading the guest's body, and the guest is parked in its write with no
// read of its request body pending, so its producer has no Read. **That is the place Close is asked at**, read from
// the engine's own record and ServeHTTP's stack, never waited for by a duration. Close tells the write DROPPED, and
// p3serve's echo then awaits its request body's trailers, a future only this host can resolve, so Close must end the
// body's stream itself.
func TestCloseEndsATaskAwaitingTheTrailersOfABodyItStoppedReading(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			a := startBurroughsServe(t, gate)
			c, err := a.dial()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, werr := io.WriteString(c, "POST /echo HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n\r\n"); werr != nil {
				t.Fatal(werr)
			}
			q := servedSince(t, a.srv, map[*serveRequest]bool{})
			// The body, written until the server stops reading it; the writer ends when the connection closes.
			chunk := strings.Repeat("x", 64<<10)
			go func() {
				for {
					if _, werr := fmt.Fprintf(c, "%x\r\n%s\r\n", len(chunk), chunk); werr != nil {
						return
					}
				}
			}()
			guard := time.Now().Add(p3serveGuard) // a harness hang guard, not a measurement
			for !echoParkedInItsWrite(q) {
				if time.Now().After(guard) {
					t.Fatalf("the echo never parked in its write with ServeHTTP held in a Write:\n  %s\n%s",
						servedPlace("echo", q), servePlaces())
				}
				runtime.Gosched()
			}

			asked := servedPlace("echo", q)
			if !a.srv.Close(time.Now().Add(p3serveGuard)) {
				t.Fatalf("Close reached its bound with the echo still running.\nWhen Close was asked:\n  %s\nAt its "+
					"bound:\n  %s\n%s", asked, servedPlace("echo", q), servePlaces())
			}
			deadline := time.Now().Add(p3serveGuard) // a harness hang guard for a goroutine between its last act and its end
			for gs := serveGoroutines(); len(gs) > 0; gs = serveGoroutines() {
				if time.Now().After(deadline) {
					t.Fatalf("%d serving goroutine(s) outlived Close:\n%s", len(gs), strings.Join(gs, "\n\n"))
				}
				runtime.Gosched()
			}
		})
	}
}

// echoParkedInItsWrite is the place TestCloseEndsATaskAwaitingTheTrailersOfABodyItStoppedReading asks Close at: the
// guest's write of its response body is pending and unresolved, its request body's producer exists and has no Read in
// flight, and ServeHTTP is held in a Write to the client. The engine's record is read first, under its locks, and the
// stacks only once it holds.
func echoParkedInItsWrite(q *serveRequest) bool {
	q.mu.Lock()
	b := q.body
	q.mu.Unlock()
	q.incoming.mu.Lock()
	p := q.incoming.producer
	q.incoming.mu.Unlock()
	if b == nil || p == nil {
		return false
	}
	h := q.in.w.async
	h.mu.Lock()
	w := b.reader.conn
	parked := w != nil && w.hasWrite && !w.resolved && !p.reading && !p.done
	h.mu.Unlock()
	if !parked {
		return false
	}
	for _, g := range strings.Split(serveStacks(), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[IO wait") && hasFrame(g, "component.(*HTTPServer).copyBody") &&
			hasFrame(g, "net/http.(*response).Write") {
			return true
		}
	}
	return false
}

// TestARequestBodyConsumedAfterCloseIsEndedAtOnce is the request half's last arm (ADR 0137's postscript): a guest that
// calls consume-body only after Close's let-go, so that endIncoming found no producer to end, is given a stream already
// ended, its trailers future err(HTTP-protocol-error), and is told the end whether it reads the body or awaits the
// trailers. No p3serve row consumes its body after a Close, so this is the arm's only witness.
func TestARequestBodyConsumedAfterCloseIsEndedAtOnce(t *testing.T) {
	h := newAsyncHandles()
	res := newHostTable()
	res.http = newHostHTTP()
	b := &httpIncoming{body: readerFunc(func([]byte) (int, error) {
		t.Error("the body was read")
		return 0, io.EOF
	})}
	b.abort(nil) // Close's, before the guest consumed the body
	h.mu.Lock()
	_, _, err := b.consumeLocked(h, res, nil)
	p := b.producer
	done, final := p.done, p.final
	h.mu.Unlock()
	label, code := "", ""
	if final != nil {
		var payload *canon.Value
		label, payload, _ = final.Case()
		if payload != nil {
			code, _, _ = payload.Case()
		}
	}
	if err != nil || !done || label != "error" || code != "HTTP-protocol-error" {
		t.Fatalf("a body consumed after Close's let-go: %v, stream ended %v, trailers %s(%s); want nil, true, "+
			"error(HTTP-protocol-error)", err, done, label, code)
	}
}

// TestARequestBodyReadAfterCloseIsRefused is the request half of abortBodies' stickiness (ADR 0137's postscript), at
// its place: Close's abort has run while no Read of the body was in flight, the moment between one Read and the next,
// and a Read then starts. It must not read the body, which only the client could end, and must fail, as a Read the
// abort ended in flight fails.
func TestARequestBodyReadAfterCloseIsRefused(t *testing.T) {
	read := false
	b := &httpIncoming{body: readerFunc(func([]byte) (int, error) {
		read = true // a held body would block here, until a client that never sends more went away
		return 0, io.EOF
	})}
	b.abort(nil) // Close's, with no Read in flight
	n, err := b.Read(make([]byte, 8))
	if read || n != 0 || !errors.Is(err, errServeClosed) {
		t.Fatalf("a Read after Close's abort read the body %v and returned %d, %v; want no read and %v", read, n, err,
			errServeClosed)
	}
}

// serveGoroutines are the goroutines serving started, by the function that started each: a task's driver, and a
// producer's whose Read is a request body's.
func serveGoroutines() []string {
	var out []string
	for _, g := range strings.Split(serveStacks(), "\n\n") {
		created := createdBy(g)
		if created == "component.(*HTTPServer).ServeHTTP" ||
			(created == "component.(*streamProducer).startReadLocked" && hasFrame(g, "component.(*httpIncoming).Read")) {
			out = append(out, g)
		}
	}
	return out
}

func serveStacks() string {
	buf := make([]byte, 1<<22)
	return string(buf[:runtime.Stack(buf, true)])
}

// bareFrame is a stack line's function, without its module path or its arguments: what a match on a frame compares,
// so an argument list and a checkout's path never decide it.
func bareFrame(ln string) string {
	fn := strings.TrimPrefix(strings.TrimPrefix(ln, "created by "), "github.com/scttfrdmn/burroughs/internal/")
	if i := strings.Index(fn, " in goroutine "); i > 0 {
		fn = fn[:i]
	}
	if i := strings.LastIndex(fn, "("); i > 0 && strings.HasSuffix(fn, ")") {
		fn = fn[:i]
	}
	return fn
}

// createdBy is a goroutine's creator: its `created by` function line, bare.
func createdBy(g string) string {
	_, created, ok := strings.Cut(g, "\ncreated by ")
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(created, "\n")
	return bareFrame(line)
}

// hasFrame reports whether g's stack holds a frame of fn, matched by its bare name.
func hasFrame(g, fn string) bool {
	for _, ln := range strings.Split(g, "\n") {
		if !strings.HasPrefix(ln, "\t") && bareFrame(ln) == fn {
			return true
		}
	}
	return false
}

// servePlaces names where serving is, so a failure names its place the first time it happens: every goroutine whose
// creator is in this package (ServeHTTP's task drivers, a producer's Reads, the engine's own) and every goroutine
// net/http runs ServeHTTP on, each by its status line, its innermost frames bare, and its creator.
func servePlaces() string {
	var b strings.Builder
	n := 0
	for _, g := range strings.Split(serveStacks(), "\n\n") {
		created := createdBy(g)
		if !strings.HasPrefix(created, "component.") && !hasFrame(g, "component.(*HTTPServer).ServeHTTP") {
			continue
		}
		n++
		head, _, _ := strings.Cut(g, "\n")
		b.WriteString("\n  " + head)
		frames := 0
		for _, ln := range strings.Split(g, "\n")[1:] {
			if frames == 12 || strings.HasPrefix(ln, "created by ") {
				break
			}
			if !strings.HasPrefix(ln, "\t") {
				b.WriteString("\n    " + bareFrame(ln))
				frames++
			}
		}
		b.WriteString("\n    created by " + created)
	}
	return fmt.Sprintf("%d serving goroutine(s):%s", n, b.String())
}

// servedPlace names a served task's place by the engine's own record of it: whether it has ended, whether it has
// answered, whether its response body is recorded for the copy, and whether a Read of its request body is in flight.
func servedPlace(name string, q *serveRequest) string {
	select {
	case <-q.ended:
		return name + ": ended"
	default:
	}
	answered := false
	select {
	case <-q.answer:
		answered = true
	default:
	}
	q.mu.Lock()
	armed, abandoned, letGo := q.body != nil, q.abandoned, q.letGo
	q.mu.Unlock()
	q.incoming.mu.Lock()
	reading, closed := q.incoming.reading, q.incoming.closed
	q.incoming.mu.Unlock()
	return fmt.Sprintf("%s: running; answered %v, response body recorded %v, abandoned %v, bodies let go %v; request "+
		"body Read in flight %v, closed %v", name, answered, armed, abandoned, letGo, reading, closed)
}

// servedPlaces is servedPlace for each named task, in the order named.
func servedPlaces(names []string, tasks map[string]*serveRequest) string {
	ps := make([]string, len(names))
	for i, name := range names {
		ps[i] = "  " + servedPlace(name, tasks[name])
	}
	return strings.Join(ps, "\n")
}

// servedSince is the one request the server holds that seen does not, waited for under the guard and then added to
// seen: how a witness names each task by the request that made it.
func servedSince(t *testing.T, srv *HTTPServer, seen map[*serveRequest]bool) *serveRequest {
	t.Helper()
	guard := time.Now().Add(p3serveGuard)
	for {
		srv.mu.Lock()
		var q *serveRequest
		for r := range srv.reqs {
			if !seen[r] {
				q = r
			}
		}
		srv.mu.Unlock()
		if q != nil {
			seen[q] = true
			return q
		}
		if time.Now().After(guard) {
			t.Fatalf("no new served task within %s", p3serveGuard)
		}
		runtime.Gosched()
	}
}

// TestCloseEndsEveryServedTaskAndItsGoroutine is Close's witness (ADR 0137). Three requests are in flight: an echo
// that has answered and reads a body the client holds open, a stall that has not answered and reads one, and a forever
// whose endless body the client never reads. Close must end all three within its bound, and afterwards no goroutine
// serving started is running: no task's driver, and no request body's Read.
//
// **A response's head arriving is not its body being recorded**: ServeHTTP flushes the head and then records the body
// it copies (setBody), and a request body is Read only while its guest has a read pending, so Close is asked where
// each body may be recorded or not and Read or not. Every one is Close's to end (abortBodies): the arms this witness
// cannot hold at their place are TestAResponseBodyRecordedAfterCloseIsLetGoAtOnce's,
// TestCloseEndsATaskAwaitingTheTrailersOfABodyItStoppedReading's, TestARequestBodyReadAfterCloseIsRefused's and
// TestARequestBodyConsumedAfterCloseIsEndedAtOnce's. #1401's CI run found this one at the first: forever's body
// recorded after Close had let the bodies go, its task parked in a write and its ServeHTTP in a Write, until the bound.
func TestCloseEndsEveryServedTaskAndItsGoroutine(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			a := startBurroughsServe(t, gate)
			// Each task is named by the request that made it, so a Close that reaches its bound names which is left.
			names := []string{"echo", "stall", "forever"}
			tasks, seen := map[string]*serveRequest{}, map[*serveRequest]bool{}
			holdEcho(t, a.serveArm)
			tasks["echo"] = servedSince(t, a.srv, seen)

			stall, err := a.dial()
			if err != nil {
				t.Fatal(err)
			}
			defer stall.Close()
			if _, werr := io.WriteString(stall, "POST /stall HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: "+
				"chunked\r\n\r\n3\r\nabc\r\n"); werr != nil {
				t.Fatal(werr)
			}
			// The stall's head is the server's to read, so its task is waited for, under the guard.
			tasks["stall"] = servedSince(t, a.srv, seen)

			forever, err := a.dial()
			if err != nil {
				t.Fatal(err)
			}
			defer forever.Close()
			if _, werr := io.WriteString(forever, "GET /forever HTTP/1.1\r\nHost: p3serve.test\r\n\r\n"); werr != nil {
				t.Fatal(werr)
			}
			// The body is never read, so the server is held in a Write; the deferred Close of its connection ends it.
			if _, rerr := http.ReadResponse(bufio.NewReader(forever), nil); rerr != nil { //nolint:bodyclose // see above
				t.Fatalf("forever's head: %v", rerr)
			}
			tasks["forever"] = servedSince(t, a.srv, seen)

			// Three tasks in flight: the place the Close is asked at, recorded so a bound reached says where each was.
			asked := servedPlaces(names, tasks)
			if !a.srv.Close(time.Now().Add(p3serveGuard)) {
				t.Fatalf("Close reached its bound with a served task still running.\nWhen Close was asked:\n%s\nAt its "+
					"bound:\n%s\n%s", asked, servedPlaces(names, tasks), servePlaces())
			}
			deadline := time.Now().Add(p3serveGuard) // a harness hang guard for a goroutine between its last act and its end
			for {
				gs := serveGoroutines()
				if len(gs) == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%d serving goroutine(s) outlived Close:\n%s", len(gs), strings.Join(gs, "\n\n"))
				}
				runtime.Gosched()
			}
			if live, idle := a.srv.instances(); live != 0 || idle != 0 {
				t.Fatalf("Close left %d instances, %d idle", live, idle)
			}
			if !a.srv.Close(time.Now().Add(p3serveGuard)) {
				t.Fatal("a second Close reported a bound reached") // a second Close probes the first's closure
			}
		})
	}
}
