// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// burroughs serve's witnesses (#1385, ADR 0137, Scott's ruling 2 on #1375). The oracle rows are internal/component's
// TestP3ServeMatchesWasmtime; these read the subcommand: its address line, its grant of outgoing HTTP, its refusal of a
// component with no handler, its grant flags, and its stop.

const p3serveWasm = "../../internal/component/testdata/p3serve/p3serve.wasm"

// serveGuard bounds a wait here: a harness hang guard, not a measurement, scaled under -race.
const serveGuard = 60 * time.Second * raceGuardScale

// syncBuffer is a writer the served component and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestServeAComponent runs `burroughs serve --addr 127.0.0.1:0 p3serve.wasm` in-process, reads the address from the
// line it prints, and asks it two rows: hello, the guest's answer, and send, a client.send from inside the handler to
// a peer on 127.0.0.1, which serve grants as wasmtime serve does. Then it stops serve, which returns nil.
func TestServeAComponent(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	pr, pw := io.Pipe()
	stop := make(chan os.Signal, 1)
	var stdout syncBuffer
	done := make(chan error, 1)
	go func() {
		err := serveCmd(&stdout, pw, []string{"--addr", "127.0.0.1:0", p3serveWasm}, stop)
		_ = pw.Close() // an io.Pipe's Close returns nil
		done <- err
	}()
	lines := bufio.NewScanner(pr)
	addr := make(chan string, 1)
	rest := make(chan string, 1)
	go func() {
		first := ""
		if lines.Scan() {
			first = lines.Text()
		}
		addr <- first
		var b strings.Builder
		for lines.Scan() {
			b.WriteString(lines.Text() + "\n")
		}
		rest <- b.String()
	}()
	var line string
	select {
	case line = <-addr:
	case err := <-done:
		t.Fatalf("serve returned %v before it printed an address", err)
	case <-time.After(serveGuard):
		t.Fatal("serve printed no address within the guard")
	}
	url, ok := strings.CutPrefix(line, "Serving HTTP on ")
	if !ok || !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		t.Fatalf("serve's first line is %q; want wasmtime serve's \"Serving HTTP on http://ADDR/\"", line)
	}
	url = strings.TrimSuffix(url, "/")
	client := &http.Client{Timeout: serveGuard}

	resp, err := client.Get(url + "/hello?x=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "hello, Method::Get /hello?x=1" {
		t.Fatalf("GET /hello: %d %q", resp.StatusCode, b)
	}

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "peer saw "+r.URL.Path) // the guest's read is the check
	}))
	defer peer.Close()
	resp, err = client.Get(url + "/send?to=" + peer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := `send: status 200, body "peer saw /peer"`; string(b) != want {
		t.Fatalf("the guest's send answered %q, want %q: serve grants outgoing HTTP", b, want)
	}

	stop <- os.Interrupt
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after its stop; want nil", err)
		}
	case <-time.After(serveGuard):
		t.Fatal("serve did not return within the guard of its stop")
	}
	if got := <-rest; got != "" {
		t.Errorf("serve wrote more to stderr than its address line: %q", got)
	}
}

// TestServeRefusesAComponentWithNoHandler reads ADR 0137's refusal: a component that exports wasi:cli/run and no
// wasi:http/handler is refused before anything listens, as a usage error naming what it lacks and that serve never
// calls run. p3http is such a component. wasmtime 49.0.3 exits 1 there.
func TestServeRefusesAComponentWithNoHandler(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	var stdout, stderr syncBuffer
	// The stop is delivered already, so a serve that did listen returns at once rather than serving for ever.
	stop := make(chan os.Signal, 1)
	stop <- os.Interrupt
	code := exitCode(serveCmd(&stdout, &stderr, []string{
		"--addr", "127.0.0.1:0",
		"../../internal/component/testdata/p3http/p3http.wasm",
	}, stop))
	if code != exitUsage {
		t.Fatalf("exit %d, want %d (usage); stderr %q", code, exitUsage, stderr.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "exports no wasi:http/handler@0.3 handle") || !strings.Contains(got, "wasi:cli/run") ||
		strings.Contains(got, "Serving HTTP") {
		t.Fatalf("stderr %q; want the refusal naming the handler it lacks and wasi:cli/run, before any address", got)
	}
}

// TestServeTakesRunsGrantFlags reads that serve's --dir, --scratch, --inherit-network and --allow-ip-name-lookup are
// run's (ADR 0137): a --dir grant run refuses, wasmtime's two-colon form, is refused by serve with run's own words,
// and an unknown flag is a usage error. The grants' meaning is run's flag types', which serve uses as they are.
func TestServeTakesRunsGrantFlags(t *testing.T) {
	dir := t.TempDir()
	var runErr, serveErr syncBuffer
	runCode := exitCode(runCmd(io.Discard, &runErr, []string{"--dir", dir + "::/g", p3serveWasm}))
	serveCode := exitCode(serveCmd(io.Discard, &serveErr, []string{"--dir", dir + "::/g", p3serveWasm},
		make(chan os.Signal)))
	if runCode != exitUsage || serveCode != exitUsage {
		t.Fatalf("--dir HOST::GUEST: run exit %d, serve exit %d; want %d from both", runCode, serveCode, exitUsage)
	}
	firstLine := func(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }
	if firstLine(runErr.String()) != firstLine(serveErr.String()) {
		t.Fatalf("serve refused --dir with %q, run with %q; want run's words", firstLine(serveErr.String()),
			firstLine(runErr.String()))
	}
	var unknown syncBuffer
	if code := exitCode(serveCmd(io.Discard, &unknown, []string{"--allow-http", p3serveWasm}, make(chan os.Signal))); code != exitUsage {
		t.Fatalf("serve --allow-http: exit %d, want %d: serve grants it and takes no flag for it", code, exitUsage)
	}
}
