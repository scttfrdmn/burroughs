// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
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

// serveGoroutines are the goroutines serving started, by the function that started each: a task's driver, and a
// producer's whose Read is a request body's.
func serveGoroutines() []string {
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
		if strings.Contains(created, "(*HTTPServer).ServeHTTP") ||
			(strings.Contains(created, "(*streamProducer).startReadLocked") && strings.Contains(g, "(*httpIncoming).Read")) {
			out = append(out, g)
		}
	}
	return out
}

// TestCloseEndsEveryServedTaskAndItsGoroutine is Close's witness (ADR 0137). Three requests are in flight, each held
// at a place: an echo that has answered and is reading a body the client holds open, a stall that has not answered
// and is reading one, and a forever whose endless body the client never reads, so ServeHTTP is held in a Write.
// Close must end all three within its bound, and afterwards no goroutine serving started is running: no task's
// driver, and no request body's Read.
func TestCloseEndsEveryServedTaskAndItsGoroutine(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			a := startBurroughsServe(t, gate)
			holdEcho(t, a.serveArm)

			stall, err := a.dial()
			if err != nil {
				t.Fatal(err)
			}
			defer stall.Close()
			if _, werr := io.WriteString(stall, "POST /stall HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: "+
				"chunked\r\n\r\n3\r\nabc\r\n"); werr != nil {
				t.Fatal(werr)
			}

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
			// Three tasks in flight: the place the Close is asked at. The stall's head is the server's to read, so its
			// task is waited for, under the guard.
			guard := time.Now().Add(p3serveGuard)
			for {
				a.srv.mu.Lock()
				n := len(a.srv.reqs)
				a.srv.mu.Unlock()
				if n == 3 {
					break
				}
				if time.Now().After(guard) {
					t.Fatalf("%d served tasks in flight, want 3", n)
				}
				runtime.Gosched()
			}

			if !a.srv.Close(time.Now().Add(p3serveGuard)) {
				t.Fatalf("Close reached its bound with a served task still running")
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
