// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs"
)

// The public serving surface (#1385, ADR 0137, Scott's ruling 2 on #1375): Component.Handler() over an httptest
// server, as an embedder serves it. The oracle rows are internal/component's TestP3ServeMatchesWasmtime; these read
// that the public door reaches the same serving, and that Close reaches it.

const p3serveWasm = "internal/component/testdata/p3serve/p3serve.wasm"

// serveGuard bounds a request or a read here: a harness hang guard, not a measurement.
const serveGuard = 60 * time.Second

// servingPlaces names where serving is, so a Close that reaches its bound names its place the first time it happens:
// every goroutine whose `created by` function line is internal/component's (a request's task driver, a request body's
// Read) and every goroutine net/http runs the handler's ServeHTTP on, each by its status line, its innermost frames by
// bare function name, and its creator.
func servingPlaces() string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	bare := func(ln string) string {
		fn := strings.TrimPrefix(strings.TrimPrefix(ln, "created by "), "github.com/scttfrdmn/burroughs/internal/")
		if i := strings.Index(fn, " in goroutine "); i > 0 {
			fn = fn[:i]
		}
		if i := strings.LastIndex(fn, "("); i > 0 && strings.HasSuffix(fn, ")") {
			fn = fn[:i]
		}
		return fn
	}
	var b strings.Builder
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		lines := strings.Split(g, "\n")
		created, serving := "", false
		for _, ln := range lines {
			switch {
			case strings.HasPrefix(ln, "created by "):
				created = bare(ln)
			case !strings.HasPrefix(ln, "\t") && bare(ln) == "component.(*HTTPServer).ServeHTTP":
				serving = true
			}
		}
		if !strings.HasPrefix(created, "component.") && !serving {
			continue
		}
		n++
		b.WriteString("\n  " + lines[0])
		frames := 0
		for _, ln := range lines[1:] {
			if frames == 12 || strings.HasPrefix(ln, "created by ") {
				break
			}
			if !strings.HasPrefix(ln, "\t") {
				b.WriteString("\n    " + bare(ln))
				frames++
			}
		}
		b.WriteString("\n    created by " + created)
	}
	return fmt.Sprintf("%d serving goroutine(s):%s", n, b.String())
}

func loadP3Serve(t *testing.T, cfg burroughs.ComponentConfig) *burroughs.Component {
	t.Helper()
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile(p3serveWasm)
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := cfg.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	return c
}

// TestComponentHandlerServesARequest is Handler at the public boundary: a GET answered by the guest's status, header
// and body, with wasmtime serve's chunked framing (p3serve.reading's hello row); a POST whose body the guest streams
// back after it has answered, through net/http's own client, which needs the request body readable after the
// response's head (ADR 0137's full duplex); and the err row's 500 page. The same handler is returned each time.
func TestComponentHandlerServesARequest(t *testing.T) {
	c := loadP3Serve(t, burroughs.ComponentConfig{})
	defer c.Close()
	h := c.Handler()
	if c.Handler() != h {
		t.Fatal("Handler returned a second handler; it returns the same one each time")
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	client := &http.Client{Timeout: serveGuard}

	resp, err := client.Get(ts.URL + "/hello?x=1")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("X-Guest") != "hello" ||
		string(body) != "hello, Method::Get /hello?x=1" || len(resp.TransferEncoding) != 1 {
		t.Fatalf("GET /hello: %d %v %q %v, %v; want 200, x-guest hello, the guest's line, chunked", resp.StatusCode,
			resp.Header, body, resp.TransferEncoding, err)
	}

	sent := strings.Repeat("abcdefghij", 10_000)
	resp, err = client.Post(ts.URL+"/echo", "text/plain", strings.NewReader(sent))
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != sent {
		t.Fatalf("POST /echo: %d, %d bytes back of %d, %v", resp.StatusCode, len(body), len(sent), err)
	}

	resp, err = client.Get(ts.URL + "/err")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusInternalServerError ||
		!strings.Contains(string(body), "<h1>500 Internal Server Error</h1>") ||
		!strings.Contains(string(body), "burroughs") || resp.Header.Get("Content-Type") != "text/html; charset=UTF-8" {
		t.Fatalf("GET /err: %d %q, %v; want the 500 page", resp.StatusCode, body, err)
	}
}

// TestHandlerInstancesTakeTheComponentsConfiguration reads that the instances Handler serves on are made with the
// Component's configuration (ADR 0137): p3serve's send row, a client.send from inside the handler to a peer on
// 127.0.0.1, answers the peer's status with AllowHTTP and HTTP-request-denied without it, ADR 0135's deny-at-call.
//
// NEUTER, run: make the handler's instances with a bare host, and the granted row reads "send: err
// ErrorCode::HttpRequestDenied".
func TestHandlerInstancesTakeTheComponentsConfiguration(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "peer saw "+r.URL.Path) // the guest's read is the check
	}))
	defer peer.Close()
	for _, row := range []struct {
		allow bool
		want  string
	}{
		{true, `send: status 200, body "peer saw /peer"`},
		{false, "send: err ErrorCode::HttpRequestDenied"},
	} {
		c := loadP3Serve(t, burroughs.ComponentConfig{AllowHTTP: row.allow})
		ts := httptest.NewServer(c.Handler())
		resp, err := (&http.Client{Timeout: serveGuard}).Get(ts.URL + "/send?to=" + peer.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ts.Close()
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if string(b) != row.want {
			t.Errorf("AllowHTTP %v: the guest's send answered %q, want %q", row.allow, b, row.want)
		}
	}
}

// TestComponentCloseClosesItsHandler is Component.Close reaching the handler (ADR 0137). A request is held at a
// place: the guest has answered POST /echo and is reading a body the client holds open. Close returns nil, within
// its bound, and the held response ends rather than staying open; a request after Close is answered with the page.
func TestComponentCloseClosesItsHandler(t *testing.T) {
	c := loadP3Serve(t, burroughs.ComponentConfig{})
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()

	conn, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), serveGuard)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(serveGuard))
	if _, werr := io.WriteString(conn, "POST /echo HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n\r\n"); werr != nil {
		t.Fatal(werr)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("the held echo's head: %v", err)
	}
	defer resp.Body.Close()

	if cerr := c.Close(); cerr != nil {
		t.Fatalf("Close = %v; want nil, every served task ended within the bound. Where serving is:\n%s", cerr,
			servingPlaces())
	}
	if _, rerr := io.ReadAll(resp.Body); rerr != nil && os.IsTimeout(rerr) {
		t.Fatalf("the held response is still open after Close: %v", rerr)
	}

	after, err := (&http.Client{Timeout: serveGuard}).Get(ts.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(after.Body)
	after.Body.Close()
	if after.StatusCode != http.StatusInternalServerError || !strings.Contains(string(b), "burroughs") {
		t.Fatalf("a request after Close: %d %q; want the 500 page", after.StatusCode, b)
	}
	if cerr := c.Close(); cerr != nil {
		t.Fatalf("a second Close = %v", cerr) // probes that the first closed everything
	}
}

// TestHandlerOfAComponentWithNoHandleExportAnswersThePage reads ADR 0137's choice for a component that exports
// wasi:cli/run and no handler: Handler has no error to return, so every request is answered with the 500 page, and
// Exports is how an embedder asks first. p3http is such a component.
func TestHandlerOfAComponentWithNoHandleExportAnswersThePage(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/p3http/p3http.wasm")
	if err != nil {
		t.Fatal(err)
	}
	c, err := burroughs.ComponentConfig{}.LoadComponent(wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, name := range c.Exports() {
		if strings.HasPrefix(name, "wasi:http/handler@") {
			t.Fatalf("p3http exports %s; the row needs a component with none", name)
		}
	}
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()
	resp, err := (&http.Client{Timeout: serveGuard}).Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(b), "<h1>500 Internal Server Error</h1>") {
		t.Fatalf("a request to a component with no handler: %d %q; want the 500 page", resp.StatusCode, b)
	}
}
