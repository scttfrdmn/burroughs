// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// p3serve's witness (#1385, ADR 0137): one driver, serveRows, drives a server of testdata/p3serve/p3serve.wasm over raw
// TCP and HTTP/1.1, and records what each row's response says. Raw TCP so the request on the wire is the row's own,
// byte for byte, on every arm: no client of either side adds or drops a header.
//
// The oracle's reading is taken by the same driver against wasmtime 49.0.3's `serve` (`-update-p3serve`), the
// c2http shape: a go test flag rather than a separate command, because the reading is the driver's output and the
// driver is here.

// updateP3Serve regenerates p3serve.reading from wasmtime 49.0.3's serve instead of checking it.
var updateP3Serve = flag.Bool("update-p3serve", false,
	"regenerate internal/component/testdata/p3serve.reading from wasmtime 49.0.3's serve instead of checking it")

const (
	p3serveWasm    = "testdata/p3serve/p3serve.wasm"
	p3serveReading = "testdata/p3serve.reading"
	// p3serveRuns is how many drives of the oracle must agree, line for line, before its reading is written.
	p3serveRuns = 10
)

// p3serveGuard bounds every wait of the driver: a dial, a response, a body, a line on the guest's stderr. A harness
// hang guard, not a measurement: what it buys is that a row that parks fails at its place, naming the row and the
// wait, rather than as the test binary's timeout. Scaled under -race, as the package's other guards are.
const p3serveGuard = 60 * time.Second * raceGuardScale

// lineLog is a server's stderr as lines, which a row may wait on: the guest says on stderr what only it can see.
type lineLog struct {
	mu    sync.Mutex
	lines []string
	more  chan struct{} // closed and replaced at each line
	part  []byte        // a line not yet ended, for Write
}

func newLineLog() *lineLog { return &lineLog{more: make(chan struct{})} }

func (l *lineLog) add(s string) {
	l.mu.Lock()
	l.lines = append(l.lines, s)
	close(l.more)
	l.more = make(chan struct{})
	l.mu.Unlock()
}

// Write is the log as an io.Writer, for a server whose stderr is a writer rather than a pipe.
func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.part = append(l.part, p...)
	var done []string
	for {
		i := bytes.IndexByte(l.part, '\n')
		if i < 0 {
			break
		}
		done = append(done, string(l.part[:i]))
		l.part = l.part[i+1:]
	}
	l.mu.Unlock()
	for _, s := range done {
		l.add(s)
	}
	return len(p), nil
}

// waitPrefix returns the first line at or after index from that begins with prefix, and the index after it, waiting
// for it up to the guard.
func (l *lineLog) waitPrefix(from int, prefix string) (string, int, error) {
	deadline := time.NewTimer(p3serveGuard)
	defer deadline.Stop()
	for {
		l.mu.Lock()
		for i := from; i < len(l.lines); i++ {
			if strings.HasPrefix(l.lines[i], prefix) {
				s := l.lines[i]
				l.mu.Unlock()
				return s, i + 1, nil
			}
		}
		more := l.more
		l.mu.Unlock()
		select {
		case <-more:
		case <-deadline.C:
			return "", from, fmt.Errorf("no stderr line beginning %q within %s", prefix, p3serveGuard)
		}
	}
}

func (l *lineLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// serveArm is one server as the driver sees it: its address, the name its error page carries, and its stderr.
type serveArm struct {
	addr   string
	name   string
	stderr *lineLog
}

// p3serveRow is one row: its name, and the drive that returns its lines.
type p3serveRow struct {
	name  string
	drive func(a *serveArm) ([]string, error)
}

// serveRows is the driver's rows, in order. Each is one or more requests on connections of their own.
func serveRows() []p3serveRow {
	get := func(target string, extra ...string) string {
		return "GET " + target + " HTTP/1.1\r\nHost: p3serve.test\r\n" + strings.Join(extra, "") + "\r\n"
	}
	one := func(req string, body ...string) func(a *serveArm) ([]string, error) {
		return func(a *serveArm) ([]string, error) { return a.exchange(req, body...) }
	}
	chunked := func(path, extra string, chunks []string, trailer string) string {
		var b strings.Builder
		b.WriteString("POST " + path + " HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n" + extra + "\r\n")
		for _, c := range chunks {
			fmt.Fprintf(&b, "%x\r\n%s\r\n", len(c), c)
		}
		b.WriteString("0\r\n" + trailer + "\r\n")
		return b.String()
	}
	echoBody := string(patternBytes(0, 300<<10))
	return []p3serveRow{
		{"hello", one(get("/hello?x=1"))},
		{"dump", one(get("/dump?a=b&c", "X-One: 1\r\nX-Multi: a\r\nX-Multi: b\r\nConnection: keep-alive\r\n"+
			"Keep-Alive: timeout=5\r\nProxy-Authorization: Basic eA==\r\nTE: trailers\r\nUser-Agent: p3serve-reader\r\n"))},
		{"dump-post", one("POST /dump HTTP/1.1\r\nHost: p3serve.test\r\nContent-Type: text/plain\r\nContent-Length: 3\r\n\r\nabc")},
		{"dump-chunked", one(chunked("/dump", "Trailer: X-Req-Trailer\r\n", []string{"abc", "def"}, "X-Req-Trailer: tail\r\n"))},
		{"target", one(get("/target"))},
		{"target-absolute", one("GET http://p3serve.test/target HTTP/1.1\r\nHost: p3serve.test\r\n\r\n")},
		{"big", one(get("/big"))},
		{"echo", one(chunked("/echo", "", []string{echoBody[:100<<10], echoBody[100<<10 : 200<<10], echoBody[200<<10:]}, ""))},
		{"trailers", one(chunked("/trailers", "Trailer: X-Req-Trailer\r\n", []string{"0123456789"}, "X-Req-Trailer: tail\r\n"))},
		{"trailers-te", one(chunked("/trailers", "TE: trailers\r\nTrailer: X-Req-Trailer\r\n", []string{"0123456789"},
			"X-Req-Trailer: tail\r\n"))},
		{"status", one(get("/status"))},
		{"fixed", one(get("/fixed"))},
		{"err", one(get("/err"))},
		{"err-internal", one(get("/err-internal"))},
		{"trap", one(get("/trap"))},
		{"after-trap", one(get("/hello?after=trap"))},
		{"trap-after", one(get("/trap-after"))},
		{"late", one(get("/late"))},
		{"concurrent", func(a *serveArm) ([]string, error) { return a.concurrent(4) }},
		{"forever", func(a *serveArm) ([]string, error) {
			return a.disconnect(get("/forever"), "", 256<<10, "", "forever: ")
		}},
		{"stall", func(a *serveArm) ([]string, error) {
			return a.disconnect("POST /stall HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n\r\n",
				"3\r\nabc\r\n", -1, "", "stall: ")
		}},
		{"sleep", func(a *serveArm) ([]string, error) {
			return a.disconnect(get("/sleep"), "", -1, "sleep: the handler entered", "sleep: ")
		}},
		{"trailers-err", func(a *serveArm) ([]string, error) { return a.aborted(get("/trailers-err")) }},
	}
}

// patternBytes is the guest's pattern: byte i is 'a' + i mod 26.
func patternBytes(off, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (off+i)%26)
	}
	return b
}

// dial opens a connection to the arm with the guard as its deadline.
func (a *serveArm) dial() (net.Conn, error) {
	c, err := net.DialTimeout("tcp", a.addr, p3serveGuard)
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(p3serveGuard))
	return c, nil
}

// exchange writes one request on a connection of its own and records its response.
func (a *serveArm) exchange(req string, _ ...string) ([]string, error) {
	c, err := a.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := io.WriteString(c, req); err != nil {
		return nil, fmt.Errorf("writing the request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return nil, fmt.Errorf("reading the response's head: %w", err)
	}
	body, berr := io.ReadAll(resp.Body)
	return a.capture(resp, body, berr), nil
}

// aborted is a row whose response must not end well, recorded only as that: whether the exchange ended, or the
// connection closed first. Nothing before the close is recorded, since wasmtime 49.0.3 closes the connection at the
// error and how much of the response reached the client first is a race on its side: the head itself, in some runs
// (#1387's probe read 0 body bytes in 1 of 5 runs).
func (a *serveArm) aborted(req string) ([]string, error) {
	c, err := a.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := io.WriteString(c, req); err != nil {
		return nil, fmt.Errorf("writing the request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
	}
	if err == nil {
		return []string{"outcome: the response ended"}, nil
	}
	// The error's class is not recorded either: a head lost is io.EOF, and a body cut short io.ErrUnexpectedEOF.
	return []string{"outcome: the connection closed before the response ended"}, nil
}

// capture is a response as the reading records it: the status, the headers the guest sets or the server must, the
// framing without its count, the body, the trailers, and an error reading the body by its class. A body over 512
// bytes is its length and whether it is the guest's pattern; the error page's server name is SERVER.
func (a *serveArm) capture(resp *http.Response, body []byte, berr error) []string {
	out := []string{"status: " + resp.Status}
	for _, k := range []string{"Content-Type", "X-Guest", "Trailer"} {
		if vs := resp.Header.Values(k); len(vs) > 0 {
			out = append(out, fmt.Sprintf("header %s: %q", strings.ToLower(k), vs))
		}
	}
	switch {
	case len(resp.TransferEncoding) > 0:
		out = append(out, "framing: "+strings.Join(resp.TransferEncoding, ","))
	case resp.ContentLength >= 0:
		out = append(out, "framing: length")
	default:
		out = append(out, "framing: close")
	}
	text := strings.ReplaceAll(string(body), a.name, "SERVER")
	switch {
	case len(body) <= 512:
		out = append(out, "body: "+strconv.Quote(text))
	default:
		sum := sha256.Sum256(body)
		out = append(out, fmt.Sprintf("body: %d bytes, the pattern: %v, sha256 %s", len(body),
			bytes.Equal(body, patternBytes(0, len(body))), hex.EncodeToString(sum[:8])))
	}
	if berr != nil {
		out = append(out, "body error: "+errClass(berr))
	}
	if len(resp.Trailer) > 0 {
		var ts []string
		for k, vs := range resp.Trailer {
			ts = append(ts, strings.ToLower(k)+"="+strings.Join(vs, ","))
		}
		sort.Strings(ts)
		out = append(out, fmt.Sprintf("trailers: %q", ts))
	}
	return out
}

// errClass names a body read's error by what it says about the response, not by its text.
func errClass(err error) string {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected EOF"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "the guard expired"
	default:
		var ne net.Error
		if errors.As(err, &ne) {
			return "a network error"
		}
		return "other: " + err.Error()
	}
}

// concurrent is the concurrency row: n POSTs to /echo whose bodies stay open, so each guest's handler has returned
// its response and is parked reading a body only the client can finish. **The place, not a duration**: every
// response's head arrives while all n bodies are still open, which a server that answers one request at a time
// cannot do. Then each body is finished, and each echo read.
func (a *serveArm) concurrent(n int) ([]string, error) {
	type held struct {
		c    net.Conn
		br   *bufio.Reader
		resp *http.Response
	}
	hs := make([]*held, n)
	defer func() {
		for _, h := range hs {
			if h != nil {
				h.c.Close()
			}
		}
	}()
	for i := range hs {
		c, err := a.dial()
		if err != nil {
			return nil, err
		}
		hs[i] = &held{c: c, br: bufio.NewReader(c)}
		head := "POST /echo HTTP/1.1\r\nHost: p3serve.test\r\nTransfer-Encoding: chunked\r\n\r\n"
		if _, err := io.WriteString(c, head); err != nil {
			return nil, err
		}
	}
	for i, h := range hs {
		resp, err := http.ReadResponse(h.br, nil)
		if err != nil {
			return nil, fmt.Errorf("request %d's response head, with every body open: %w", i, err)
		}
		h.resp = resp
	}
	out := []string{fmt.Sprintf("heads: %d of %d with every body open", n, n)}
	for i, h := range hs {
		part := fmt.Sprintf("body %d", i)
		if _, err := fmt.Fprintf(h.c, "%x\r\n%s\r\n0\r\n\r\n", len(part), part); err != nil {
			return nil, err
		}
		body, berr := io.ReadAll(h.resp.Body)
		for _, ln := range a.capture(h.resp, body, berr) {
			out = append(out, fmt.Sprintf("%d %s", i, ln))
		}
	}
	return out, nil
}

// disconnect is a client that goes away: it writes head and then more, reads keep bytes of the response's body (none
// for -1, when the response is not awaited), closes the connection, and waits for the guest's stderr line beginning
// prefix, which says what the guest saw. stall's request body is never finished; sleep's has none. With entered set,
// the client goes away only once the guest has written that line: **a place**, so the handler is known to be running
// when the connection closes (a close that wins the race to hyper's dispatch is never handled at all).
func (a *serveArm) disconnect(head, more string, keep int, entered, prefix string) ([]string, error) {
	from := a.stderr.len()
	c, err := a.dial()
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(c, head+more); err != nil {
		c.Close()
		return nil, err
	}
	var out []string
	if entered != "" {
		line, next, err := a.stderr.waitPrefix(from, entered)
		if err != nil {
			c.Close()
			return nil, err
		}
		out, from = append(out, "guest: "+line), next
	}
	if keep >= 0 {
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("reading the response's head: %w", err)
		}
		got, rerr := io.ReadFull(resp.Body, make([]byte, keep))
		out = append(out, "status: "+resp.Status, fmt.Sprintf("read %d bytes, then went away (%v)", got, rerr))
	} else {
		out = append(out, "went away before the response")
	}
	c.Close()
	line, _, err := a.stderr.waitPrefix(from, prefix)
	if err != nil {
		return nil, err
	}
	return append(out, "guest: "+line), nil
}

// driveServe runs every row against an arm, returning the reading's lines, or the first row's failure at its place.
func driveServe(a *serveArm) ([]string, error) {
	var lines []string
	for _, r := range serveRows() {
		ls, err := r.drive(a)
		if err != nil {
			return nil, fmt.Errorf("row %s: %w", r.name, err)
		}
		lines = append(lines, "row: "+r.name)
		lines = append(lines, ls...)
	}
	return lines, nil
}

// oracleWasmtime is the pinned oracle, scripts/oracle-fetch.sh's install: the tree's own, or the main checkout's for a
// worktree, which has none. Never the wasmtime on PATH.
func oracleWasmtime(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		filepath.Join("..", "..", ".oracle", "wasmtime-49.0.3", "wasmtime"),
		filepath.Join(os.Getenv("HOME"), "src", "burroughs", ".oracle", "wasmtime-49.0.3", "wasmtime"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	t.Fatal("the pinned oracle wasmtime 49.0.3 is not installed (scripts/oracle-fetch.sh); -update-p3serve reads it")
	return ""
}

var serveAddrLine = regexp.MustCompile(`^Serving HTTP on http://([0-9.]+:[0-9]+)/`)

// startWasmtimeServe runs `wasmtime serve` on p3serve.wasm at an ephemeral port, with one request per instance (the
// oracle setting: R2 found its default instance reuse panics a wit-bindgen guest, #1372), and no log prefix, so the
// guest's stderr lines are its own. The address is read from the line it prints.
func startWasmtimeServe(t *testing.T) *serveArm {
	t.Helper()
	cmd := exec.Command(oracleWasmtime(t), "serve", "--addr", "127.0.0.1:0", "--no-logging-prefix",
		"--max-instance-reuse-count", "1", p3serveWasm)
	errp, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	log := newLineLog()
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		s := bufio.NewScanner(errp)
		for s.Scan() {
			log.add(s.Text())
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-scanned
		_ = cmd.Wait()
	})
	line, _, err := log.waitPrefix(0, "Serving HTTP on ")
	if err != nil {
		t.Fatalf("wasmtime serve printed no address: %v", err)
	}
	m := serveAddrLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("wasmtime serve's address line %q names no address", line)
	}
	return &serveArm{addr: m[1], name: "wasmtime", stderr: log}
}

// takeP3ServeReading drives the oracle p3serveRuns times, a fresh server each, requires every drive to agree, and
// writes the reading.
func takeP3ServeReading(t *testing.T) {
	var first []string
	for run := 1; run <= p3serveRuns; run++ {
		lines, err := driveServe(startWasmtimeServe(t))
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if first == nil {
			first = lines
			continue
		}
		if a, b := strings.Join(first, "\n"), strings.Join(lines, "\n"); a != b {
			t.Fatalf("run %d of the oracle differs from run 1; a reading must be stable:\n%s\n----\n%s", run, b, a)
		}
	}
	out, err := exec.Command(oracleWasmtime(t), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	header := []string{
		"# Committed reading of internal/component/testdata/p3serve/p3serve.wasm (#1385 E2-in-b; ADR 0137).",
		"# Regenerated by p3serve/build.sh, which runs: go test ./internal/component/ -run '^TestP3ServeMatchesWasmtime$' " +
			"-update-p3serve   (never hand-edited)",
		"# Invocation:  wasmtime serve --addr 127.0.0.1:0 --no-logging-prefix --max-instance-reuse-count 1 p3serve.wasm,",
		"#              driven by p3serve_test.go's serveRows over raw TCP and HTTP/1.1 on 127.0.0.1.",
		"# Engine:      " + strings.TrimSpace(string(out)) + ", " + runtime.GOOS + "/" + runtime.GOARCH + ".",
		fmt.Sprintf("# Runs:        %d of %d drives identical, each against a fresh server.", p3serveRuns, p3serveRuns),
		"#",
		"# Each block is one row: what its response's status, guest-set headers, framing, body and trailers were, and",
		"# for a client that went away, the line the guest wrote on stderr. The error page's server name is SERVER, and",
		"# framing is chunked or length, without the count: compared are status, body bytes and selected headers.",
		"",
	}
	body := strings.Join(header, "\n") + strings.Join(first, "\n") + "\n"
	if err := os.WriteFile(p3serveReading, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d lines", p3serveReading, len(first))
}

// readP3ServeReading is the committed reading's lines, its comment header dropped.
func readP3ServeReading(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(p3serveReading)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if strings.HasPrefix(ln, "#") || ln == "" {
			continue
		}
		lines = append(lines, ln)
	}
	return lines
}

// TestP3ServeMatchesWasmtime is E2-in-b's oracle witness (ADR 0137). With -update-p3serve it takes the reading from
// wasmtime 49.0.3's serve. Without it, it checks that the committed reading has every row the driver drives, in
// order; the comparison against Burroughs' serving lands with the engine.
func TestP3ServeMatchesWasmtime(t *testing.T) {
	if *updateP3Serve {
		takeP3ServeReading(t)
		return
	}
	var got []string
	for _, ln := range readP3ServeReading(t) {
		if name, ok := strings.CutPrefix(ln, "row: "); ok {
			got = append(got, name)
		}
	}
	var want []string
	for _, r := range serveRows() {
		want = append(want, r.name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the reading's rows are %v; the driver drives %v", got, want)
	}
}
