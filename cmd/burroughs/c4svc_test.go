// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// C4's files (#1380), v0.16.0's witness, beside C2's and C3's in the fork's witness directory: p3svc, an ordinary Go
// net/http service that serves through wasi:http, reads its configuration from a preopened directory, and sends
// outbound requests through http.DefaultTransport (PROVENANCE).
const (
	c4Wasm    = c2Dir + "/p3svc.wasm"
	c4Reading = c2Dir + "/p3svc.reading"
)

// c4Sources are p3svc's three committed sources, each under the name its builds give it: the handlers, which every
// build compiles, and the two serve files, of which a host build compiles serve_native.go (http.ListenAndServe, the
// directory a flag) and the fork's GOOS=wasip3 build serve_reactor.go (wasihttp.Handle, the preopen /config).
var c4Sources = map[string]string{
	"p3svc.go":         c2Dir + "/p3svc.go.txt",
	"serve_native.go":  c2Dir + "/p3svc_serve_native.go.txt",
	"serve_reactor.go": c2Dir + "/p3svc_serve_reactor.go.txt",
}

// c4GoMod is p3svc's go.mod, for both of its builds (C2's reason, c2GoMod). Its sha256 is the fork step's,
// 7fac6c2f....
const c4GoMod = "module p3svc\n\ngo 1.26\n"

var updateC4 = flag.Bool("update-c4svc", false,
	"regenerate internal/component/testdata/forkp3/p3svc.reading from arm N (the stock-Go host build) instead of checking it")

// c4Guard bounds every wait of C4's runs: the server's readiness, each request with its body, and serve's return
// after its stop. It is serveGuard, scaled by raceGuardScale: a harness hang guard, not a measurement, so a run that
// parks fails at its place ("parked at GET /proxy after 4 answered") rather than as the test binary's timeout.
const c4Guard = serveGuard

// c4SettleGuard bounds the wait for a goroutine between its last act and its end, once a run is torn down.
const c4SettleGuard = 10 * time.Second * raceGuardScale

// c4Runs is how many times TestC4BurroughsServeMatchesTheHostReading runs the whole witness, each run a fresh peer,
// a fresh configuration directory and a fresh `burroughs serve`. It is more than one because what can differ between
// runs is an interleaving, and one run samples one: when a task exits against the next request's arrival (ADR
// 0137's reuse, though the neuter below found that this witness does not tell reuse at the exit from reuse at the
// answer), the peer's chunked body, and the /proxy-post body streamed from the incoming request to the outgoing one.
// The second and third runs also start in a process that has already loaded, served and closed the component, so
// state one serve leaves behind is asked of the next. It is three, and not the plan's ten, because 10 of 10 is the
// fork step's recorded reading (PROVENANCE) and this test's job is to keep that reading true on every CI job and every
// -count, which multiply it. On an Apple M4 Pro a run takes about 1.9 s plain and 22 s under -race (each run logs its
// own), so three cost about 6 s and 66 s, where ten would cost about 3.7 minutes under -race in every CI job that runs
// the package.
const c4Runs = 3

// c4NoDirDigest is the fork step's record of the stdout both engines gave with no --dir (one run each, cmp-identical
// between wasmtime 49.0.3 and Burroughs daed9435): its length and the first 16 hex digits of its sha256, the most of
// it the record gives.
const (
	c4NoDirBytes  = 2459
	c4NoDirSHA256 = "d48397112b063e5e"
)

// c4PeerData is p3svcpeer.go.txt's peerData: /data's body.
func c4PeerData() []byte {
	b := make([]byte, 100_003)
	for i := range b {
		b[i] = byte(i*13+i>>9) ^ 0xc4
	}
	return b
}

// c4PeerMux is p3svcpeer.go.txt's peerMux, the server p3svc's outbound requests reach:
//
//	/data  200, X-Peer-Data: c4-peer-v1, and a fixed body of 100003 bytes, with no Content-Length, so it is chunked
//	/echo  the length and sha256 of the request body it read, and the method
func c4PeerMux() *http.ServeMux {
	data := c4PeerData()
	m := http.NewServeMux()
	m.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Peer-Data", "c4-peer-v1")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data) // the client's read of it is the check
	})
	m.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading the body failed", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "peer read %d bytes sha256 %x, method %s\n", len(b), sha256.Sum256(b), r.Method)
	})
	return m
}

// startC4Peer is p3svcpeer.go.txt's main, in-test: a stock-Go server on 127.0.0.1:0, as http.Serve serves it, whose
// base URL is what the peer printed on its PEER line. stop closes its listener and its connections and waits for
// Serve to return.
func startC4Peer(t *testing.T) (url string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: c4PeerMux()}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = srv.Close() // closes the listener and every connection; what it reports is Serve's to return
		select {
		case <-served:
		case <-time.After(c4Guard):
			t.Errorf("parked at the peer's Serve, which did not return within %v of its Close", c4Guard)
		}
	}
	t.Cleanup(stop)
	return "http://" + ln.Addr().String(), stop
}

// c4ClosedPort is a port nothing listened on a moment ago, the harness's: a listener on 127.0.0.1:0, closed at once.
// app.conf's down names it, and /peer-down's GET of it must be refused.
func c4ClosedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// writeC4Config is p3svcrun.go.txt's writeConfig: app.conf (the greeting, the peer's base URL, and the closed port's),
// notes.txt and conf.d/extra.conf, written before each run.
func writeC4Config(t *testing.T, dir, peerURL string, down int) {
	t.Helper()
	conf := "greeting=hello from p3svc's app.conf\n" +
		"peer=" + peerURL + "\n" +
		"down=http://127.0.0.1:" + strconv.Itoa(down) + "\n"
	for name, body := range map[string]string{
		"app.conf":          conf,
		"notes.txt":         "p3svc's configuration directory, written before each run (C4, Burroughs #1380)\n",
		"conf.d/extra.conf": "# read by no route\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// c4Pattern is p3svcclient.go.txt's pattern: n deterministic bytes.
func c4Pattern(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7+i>>11) ^ seed
	}
	return b
}

// c4ClientKind is p3svcclient.go.txt's kind: an error printed by what it is.
func c4ClientKind(err error) string {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected-EOF"
	case errors.Is(err, io.EOF):
		return "EOF"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	}
	return "other"
}

// c4RewrittenGreeting is the greeting the client writes into app.conf before its second GET /.
const c4RewrittenGreeting = "hello again: app.conf was rewritten between two requests"

// c4Route is one of the client's requests.
type c4Route struct {
	method, path string
	body         []byte
	show         []string // the response headers printed
	rewrite      bool     // rewrite app.conf's greeting before the request
	config       bool     // the body is app.conf's sha256 and line count, compared with the host's file
}

// c4Client is p3svcclient.go.txt's main, in-test: it requests each of p3svc's routes from the server at base
// ("http://127.0.0.1:PORT"), each on a connection of its own, and returns what the client prints, which is what is
// compared between arms. confDir is the host directory the server's configuration is in. Its stdout is the source's
// byte for byte; what it adds is c4Guard on each request with its body, and a request the guard ends is returned as
// its place rather than printed as its kind, so a run that parks fails there.
func c4Client(base, confDir string) ([]byte, error) {
	var out bytes.Buffer
	conf := filepath.Join(confDir, "app.conf")
	// A connection per request, so no request is retried on a reused connection.
	tr := &http.Transport{DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: c4Guard}
	ct := []string{"Content-Type"}
	routes := []c4Route{
		{method: "GET", path: "/", show: ct},
		{method: "GET", path: "/config", show: ct, config: true},
		{method: "GET", path: "/files", show: ct},
		{method: "GET", path: "/missing", show: []string{"Content-Type", "X-Content-Type-Options"}},
		{method: "GET", path: "/proxy", show: []string{"Content-Type", "X-Peer-Data"}},
		{method: "POST", path: "/proxy-post", body: c4Pattern(200_011, 0x3c), show: ct},
		{method: "GET", path: "/peer-down", show: []string{"Content-Type", "X-Content-Type-Options"}},
		{method: "GET", path: "/big", show: []string{"Content-Type", "Content-Length"}},
		{method: "GET", path: "/", show: ct, rewrite: true},
		{method: "GET", path: "/config", show: ct, config: true},
	}
	parked := func(rt c4Route, k int, err error) error {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("parked at %s %s after %d answered (c4Guard, %v)", rt.method, rt.path, k, c4Guard)
		}
		return nil
	}
	for k, rt := range routes {
		if rt.rewrite {
			if err := rewriteC4Greeting(conf); err != nil {
				fmt.Fprintf(&out, "rewriting app.conf's greeting failed: %s\n", c4ClientKind(err))
				return out.Bytes(), fmt.Errorf("rewriting app.conf's greeting before %s %s: %w", rt.method, rt.path, err)
			}
			fmt.Fprintln(&out, "app.conf's greeting rewritten")
		}
		var body io.Reader
		if rt.body != nil {
			body = bytes.NewReader(rt.body)
		}
		req, err := http.NewRequest(rt.method, base+rt.path, body)
		if err != nil {
			fmt.Fprintf(&out, "%s %s: request error %s\n", rt.method, rt.path, c4ClientKind(err))
			continue
		}
		if rt.body != nil {
			fmt.Fprintf(&out, "%s %s: sending %d bytes sha256 %x\n", rt.method, rt.path, len(rt.body), sha256.Sum256(rt.body))
		}
		resp, err := client.Do(req)
		if err != nil {
			if perr := parked(rt, k, err); perr != nil {
				return out.Bytes(), perr
			}
			fmt.Fprintf(&out, "%s %s: error %s\n", rt.method, rt.path, c4ClientKind(err))
			continue
		}
		fmt.Fprintf(&out, "%s %s: status %d %q\n", rt.method, rt.path, resp.StatusCode, resp.Status)
		for _, h := range rt.show {
			fmt.Fprintf(&out, "  header %s: %q\n", h, resp.Header.Values(h))
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // read to its end or its error, which is the check
		if perr := parked(rt, k, err); perr != nil {
			return out.Bytes(), perr
		}
		end := "ok"
		if err != nil {
			end = c4ClientKind(err)
		}
		if rt.config {
			printC4Config(&out, conf, b, end)
			continue
		}
		fmt.Fprintf(&out, "  body %d bytes sha256 %x, %s\n", len(b), sha256.Sum256(b), end)
		if len(b) <= 512 {
			fmt.Fprintf(&out, "  body %q\n", b)
		}
	}
	fmt.Fprintln(&out, "done")
	return out.Bytes(), nil
}

// rewriteC4Greeting is p3svcclient.go.txt's rewriteGreeting: app.conf's one greeting line replaced with
// c4RewrittenGreeting, every other line kept. It writes the host's file, whose grant is read-only for the guest only.
func rewriteC4Greeting(conf string) error {
	b, err := os.ReadFile(conf)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	n := 0
	for i, ln := range lines {
		if strings.HasPrefix(ln, "greeting=") {
			lines[i] = "greeting=" + c4RewrittenGreeting
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("app.conf has %d greeting lines, want 1", n)
	}
	return os.WriteFile(conf, []byte(strings.Join(lines, "\n")), 0o644)
}

// printC4Config is p3svcclient.go.txt's printConfig: /config's body printed as its comparison with the host's
// app.conf, which names the peer's port. Equal, it is quoted with the file's sha256 replaced by a name; different, it
// is quoted as it is.
func printC4Config(out io.Writer, conf string, got []byte, end string) {
	want, err := os.ReadFile(conf)
	if err != nil {
		fmt.Fprintf(out, "  reading the host's app.conf failed: %s\n", c4ClientKind(err))
		return
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(want))
	exp := fmt.Sprintf("sha256 %s\nlines %d\n", sum, bytes.Count(want, []byte("\n")))
	equal := string(got) == exp
	fmt.Fprintf(out, "  body %d bytes, %s, equal to the host app.conf's sha256 and line count: %v\n", len(got), end, equal)
	if equal {
		fmt.Fprintf(out, "  body %q\n", strings.Replace(string(got), sum, "<the host app.conf's sha256>", 1))
	} else {
		fmt.Fprintf(out, "  body %q\n", got)
	}
}

// c4Lines is a stdout's lines, without the empty one after its last newline.
func c4Lines(b []byte) []string { return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") }

// c4Describe is a stdout's length and sha256, for a failure's message and the log.
func c4Describe(b []byte) string { return fmt.Sprintf("%d bytes, sha256 %x", len(b), sha256.Sum256(b)) }

// c4FirstDiffs names up to ten lines where got differs from want, each with its line number.
func c4FirstDiffs(arm string, got, want []string) string {
	var diffs []string
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w && len(diffs) < 10 {
			diffs = append(diffs, fmt.Sprintf("line %d:\n  %s: %s\n  reading: %s", i+1, arm, g, w))
		}
	}
	return strings.Join(diffs, "\n")
}

// matchC4Reading compares a client stdout with p3svc.reading byte for byte, naming the lines that differ.
func matchC4Reading(t *testing.T, arm string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(c4Reading)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	// Vacuity: a reading of fewer lines than the client's ten requests print is not the witness's.
	if n := len(c4Lines(want)); n < 40 {
		t.Fatalf("p3svc.reading has %d lines; the client's ten requests print at least 40", n)
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf("%s's client stdout (%s) differs from p3svc.reading (%s):\n%s", arm, c4Describe(got), c4Describe(want),
		c4FirstDiffs(arm, c4Lines(got), c4Lines(want)))
}

// c4LiveGoroutines is every live goroutine's stack, by its id.
func c4LiveGoroutines() map[string]string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	gs := map[string]string{}
	for _, g := range strings.Split(string(buf), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if id, ok := strings.CutPrefix(head, "goroutine "); ok {
			id, _, _ = strings.Cut(id, " ")
			gs[id] = g
		}
	}
	return gs
}

// requireNoC4Goroutines fails if a goroutine that was not alive at before is still running once a run is torn down:
// the engine's (a served task's driver, a send, a body's producer, a file's writer), the servers' and the client's
// net/http connections, and the processes' waits. A goroutine between its last act and its end is waited for, under
// c4SettleGuard.
func requireNoC4Goroutines(t *testing.T, before map[string]string) {
	t.Helper()
	deadline := time.Now().Add(c4SettleGuard)
	for {
		var left []string
		for id, g := range c4LiveGoroutines() {
			if _, ok := before[id]; !ok {
				left = append(left, g)
			}
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutine(s) started by the run outlived it:\n%s", len(left), strings.Join(left, "\n\n"))
		}
		runtime.Gosched()
	}
}

// c4Native is arm N's server: p3svc's host build run as a host process, serving until it is killed.
type c4Native struct {
	cmd    *exec.Cmd
	out    bytes.Buffer // its stdout and stderr, read once it has exited
	exited chan struct{}
}

// startC4Native runs the host build with -addr and -config, as the fork step's arm N did.
func startC4Native(t *testing.T, bin, addr, dir string) *c4Native {
	t.Helper()
	n := &c4Native{exited: make(chan struct{})}
	n.cmd = exec.Command(bin, "-addr", addr, "-config", dir)
	n.cmd.Stdout, n.cmd.Stderr = &n.out, &n.out
	if err := n.cmd.Start(); err != nil {
		t.Fatalf("arm N's server did not start: %v", err)
	}
	go func() {
		_ = n.cmd.Wait() // the exit is read from exited; a kill's error is the stop's own
		close(n.exited)
	}()
	t.Cleanup(func() { n.stop() })
	return n
}

// stop kills the server and waits for its exit. It reports whether the server was still serving.
func (n *c4Native) stop() bool {
	select {
	case <-n.exited:
		return false
	default:
	}
	_ = n.cmd.Process.Kill() // it may exit between the select and the kill; the wait below is the fact
	<-n.exited
	return true
}

// waitAccepting is arm N's readiness: http.ListenAndServe prints nothing once it listens, so the native program has
// no line to wait on, and its readiness is its port accepting a connection. The dial is retried every 10 ms, which
// paces the retries and stands in for nothing: the accepted connection is the condition. It gives up at the server's
// exit, and at c4Guard.
func waitAccepting(addr string, exited <-chan struct{}) error {
	deadline := time.Now().Add(c4Guard)
	for {
		c, err := net.DialTimeout("tcp4", addr, time.Second)
		if err == nil {
			return c.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("parked at arm N's listen: %s accepted no connection within %v: %w", addr, c4Guard, err)
		}
		select {
		case <-exited:
			return errors.New("arm N's server exited before it accepted a connection")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestC4HostServerMatchesItsReading is C4's arm N (#1380): p3svc's three sources built by stock Go, the Go running
// this test, and run as a host process, `p3svc -addr 127.0.0.1:PORT -config DIR`. The peer and the client are the
// fork step's, in-test (c4PeerMux, c4Client), and the configuration directory is written as its harness wrote it
// (writeC4Config). The client's stdout must equal p3svc.reading byte for byte. The reading was taken from this arm,
// with go1.26.9, by the fork step's scratch harness, and `-update-c4svc` regenerates it here; the arms that serve the
// fork's build (wasmtime 49.0.3 offline, `burroughs serve` in TestC4BurroughsServeMatchesTheHostReading) are compared
// with what Go's own net/http answers, and this arm keeps that reading honest wherever CI runs.
func TestC4HostServerMatchesItsReading(t *testing.T) {
	bin, goVersion := buildForkModuleForHost(t, c4Sources, c4GoMod, "p3svc")
	before := c4LiveGoroutines()
	peer, stopPeer := startC4Peer(t)
	dir := t.TempDir()
	writeC4Config(t, dir, peer, c4ClosedPort(t))
	// The server's port is taken as the harness took it, from a listener closed at once: the host build cannot be told
	// to listen on port 0 and print the port. Another process could take it in between, and then the server exits
	// with its listen's error before it accepts, which waitAccepting names.
	addr := "127.0.0.1:" + strconv.Itoa(c4ClosedPort(t))
	n := startC4Native(t, bin, addr, dir)
	if err := waitAccepting(addr, n.exited); err != nil {
		n.stop()
		t.Fatalf("arm N (%s): %v; its output %q", goVersion, err, n.out.String())
	}
	got, err := c4Client("http://"+addr, dir)
	alive := n.stop()
	stopPeer()
	if err != nil {
		t.Fatalf("arm N (%s): %v; stdout so far:\n%s", goVersion, err, got)
	}
	if !alive {
		t.Errorf("arm N's server had exited before the client finished: %q", n.out.String())
	}
	if n.out.Len() != 0 {
		t.Errorf("arm N's server printed %q, where it prints nothing until it fails", n.out.String())
	}
	requireNoC4Goroutines(t, before)
	t.Logf("arm N (%s %s/%s): %s", goVersion, runtime.GOOS, runtime.GOARCH, c4Describe(got))
	if *updateC4 {
		// The reading is the client's stdout byte for byte, with no header: its sha256 is the one PROVENANCE names.
		if err := os.WriteFile(c4Reading, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s: %d lines", c4Reading, len(c4Lines(got)))
		return
	}
	matchC4Reading(t, "arm N ("+goVersion+")", got)
}

// c4Serve is `burroughs serve` run in-process, through serveCmd in a goroutine, as TestServeAComponent runs it: its
// stop a channel of the test's, its stdout the component's, and its stderr read for the address line it prints once
// it listens.
type c4Serve struct {
	stop    chan os.Signal
	done    chan error
	stdout  syncBuffer
	rest    chan string // stderr after the address line, once serve has returned
	base    string      // http://ADDR, from the address line
	stopped bool
	err     error
}

// startC4Serve runs `burroughs serve` with args, and waits for its own line, "Serving HTTP on http://ADDR/", which
// serve prints once its listener is bound: never for a duration. A serve that returns first, or prints anything else
// first, fails at that place, as does one that prints nothing within c4Guard.
func startC4Serve(t *testing.T, args ...string) *c4Serve {
	t.Helper()
	pr, pw := io.Pipe()
	s := &c4Serve{stop: make(chan os.Signal, 1), done: make(chan error, 1), rest: make(chan string, 1)}
	go func() {
		err := serveCmd(&s.stdout, pw, args, s.stop)
		_ = pw.Close() // an io.Pipe's Close returns nil
		s.done <- err
	}()
	first := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		line, err := readLine(pr)
		first <- line
		if err == nil {
			_, _ = io.Copy(&b, pr) // to the pipe's close, at serve's return
		}
		s.rest <- b.String()
	}()
	t.Cleanup(func() { s.close(t) })
	var line string
	select {
	case line = <-first:
	case <-time.After(c4Guard):
		t.Fatalf("parked at serve's address line: nothing on stderr within %v (args %q)", c4Guard, args)
	}
	url, ok := strings.CutPrefix(line, "Serving HTTP on ")
	if !ok || !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		rest, err := s.close(t)
		t.Fatalf("serve's first line on stderr is %q, where it prints \"Serving HTTP on http://ADDR/\" once it "+
			"listens; serve returned %v, and its stderr after that line is %q", line, err, rest)
	}
	s.base = strings.TrimSuffix(url, "/")
	return s
}

// readLine reads one line from r a byte at a time, so nothing after it is taken from the pipe: the line without its
// newline, and the error that ended it before one, if any.
func readLine(r io.Reader) (string, error) {
	var b []byte
	var c [1]byte
	for {
		if _, err := io.ReadFull(r, c[:]); err != nil {
			return string(b), err
		}
		if c[0] == '\n' {
			return string(b), nil
		}
		b = append(b, c[0])
	}
}

// close stops serve once, as an interrupt does, and returns its stderr after the address line and what serve
// returned. A serve that does not return within c4Guard of its stop fails at that place.
func (s *c4Serve) close(t *testing.T) (rest string, err error) {
	t.Helper()
	if s.stopped {
		return "", s.err
	}
	s.stopped = true
	s.stop <- os.Interrupt
	select {
	case s.err = <-s.done:
	case <-time.After(c4Guard):
		t.Fatalf("parked at serve's return: it did not return within %v of its stop", c4Guard)
	}
	select {
	case rest = <-s.rest:
	case <-time.After(c4Guard):
		t.Fatalf("parked at serve's stderr: no end within %v of its return", c4Guard)
	}
	return rest, s.err
}

// runC4Serve is one run of the witness on `burroughs serve`, the fork harness's order: the peer, a closed port, the
// configuration directory, serve with dirArgs (the grant, or none), its line, the client, and then serve's stop and
// the peer's. serve must return nil at its stop, its stdout (the component's) must be empty, and its stderr must be
// its address line alone, as the fork step read for both engines. After the run no goroutine it started is left.
func runC4Serve(t *testing.T, grant bool) []byte {
	t.Helper()
	before := c4LiveGoroutines()
	peer, stopPeer := startC4Peer(t)
	dir := t.TempDir()
	writeC4Config(t, dir, peer, c4ClosedPort(t))
	args := []string{"--addr", "127.0.0.1:0", c4Wasm}
	if grant {
		args = append([]string{"--dir", dir + ":/config"}, args...)
	}
	t0 := time.Now()
	s := startC4Serve(t, args...)
	ready := time.Since(t0)
	c0 := time.Now()
	got, cerr := c4Client(s.base, dir)
	client := time.Since(c0)
	rest, serr := s.close(t)
	stopPeer()
	if cerr != nil {
		t.Fatalf("burroughs serve %q: %v; stdout so far:\n%s", args, cerr, got)
	}
	if serr != nil {
		t.Errorf("serve returned %v at its stop, where it returns nil", serr)
	}
	if out := s.stdout.String(); out != "" {
		t.Errorf("serve's stdout, the component's, is %q, where the guest writes nothing", out)
	}
	if rest != "" {
		t.Errorf("serve's stderr after its address line is %q, where it is that line alone", rest)
	}
	requireNoC4Goroutines(t, before)
	t.Logf("burroughs serve %q: %s; ready %.2fs, client %.2fs (logged, not asserted)", args[:len(args)-1],
		c4Describe(got), ready.Seconds(), client.Seconds())
	return got
}

// TestC4BurroughsServeMatchesTheHostReading is C4's arm B (#1380), v0.16.0's outcome witness: p3svc.wasm, the fork's
// GOOS=wasip3 reactor build of p3svc's sources, served by
//
//	burroughs serve --dir DIR:/config --addr 127.0.0.1:0 p3svc.wasm
//
// in-process, through serveCmd, with the peer and the client outside the instance. Its address is read from the line
// serve prints, so no port is chosen ahead of the listen (the fork step's harness chose one, and named the window).
// The client's stdout, its mid-run rewrite of app.conf's greeting included, must equal p3svc.reading byte for byte in
// each of c4Runs runs: the reading arm N (TestC4HostServerMatchesItsReading) takes from stock Go's net/http, and which
// wasmtime 49.0.3's serve and Burroughs daed9435's gave 10 of 10 each (PROVENANCE). serve grants outgoing HTTP itself
// (ADR 0137), so --dir is its one flag. Each run also asks serve's own record: nil at its stop, nothing on stdout, its
// address line alone on stderr, and no goroutine left.
//
// The neuters, each made with the editor, run, and reverted, and each recorded at its site as a `NEUTER, run:` comment:
//
// NEUTER, run: serve's --dir grant not passed to the component (serve.go's Preopens without dirs.preopens()): each of
// the 3 runs reads the no-dir stdout, TestC4BurroughsServeWithNoDirAnswersNotExist's byte for byte (2459 bytes,
// d48397112b063e5e...), its first differing line line 1, `GET /: status 500 "500 Internal Server Error"` where the
// reading has 200. The control passes under it, as it must: it is the state the neuter makes.
// NEUTER, run: serve's always-on outgoing HTTP grant off (serve.go's AllowHTTP false): each run fails at line 18,
// `GET /proxy: status 502 "502 Bad Gateway"`, its body at line 22 "peer: permission\n", HTTP-request-denied as the
// fork's RoundTripper maps it; then /proxy-post's 502 at line 24, and /peer-down's "permission\n" at line 32 where the
// reading has "ECONNREFUSED\n". Lines 1 to 17, the directory's routes, are the reading's.
// NEUTER, run: the preopen's path resolution dropped (hostfs.go's openAt opening the guest's path with os.OpenFile, so
// it resolves against the process's working directory and not the grant's os.Root): each run fails at line 1, `GET /:
// status 500 "500 Internal Server Error"`, every open of app.conf answering "config: not-exist", while /files, at line
// 9, still lists the grant: 2452 bytes, which is neither the reading nor the no-dir stdout.
// NEUTER, run, vacuous: os.Root's resolution replaced by path arithmetic under the grant (openAt's own neuter): 3 of
// 3 pass. p3svc opens no path that leaves its grant, so this witness does not ask confinement;
// TestFSDescriptorMatchesWasmtime's escapes and nested rows do.
// NEUTER, run, vacuous: reuse counted to task.return (hostserve.go: the instance released in answered, wasmtime's
// count, rather than in finish, at the task's exit): 9 of 9 pass, and 3 of 3 under -race. The client sends each
// request only once it has read the last response to its end; TestAServedInstanceIsReusedOnlyAfterItsTaskExits,
// whose held echo is answered and not exited, is the neuter's place, and failed under it again (its GET /hello
// answered with the 500 page).
func TestC4BurroughsServeMatchesTheHostReading(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	for i := range c4Runs {
		t.Run(fmt.Sprintf("run=%d", i+1), func(t *testing.T) {
			matchC4Reading(t, "burroughs serve", runC4Serve(t, true))
		})
	}
}

// c4StatusLine is a client stdout's line for a response: the request and its status.
var c4StatusLine = regexp.MustCompile(`^(GET|POST) (\S+): status (\d+) `)

// c4Block is one response in a client stdout: its request, with "#2" for a route's second, its status, its line
// number, from 1, and its lines, the status line and the indented ones after it.
type c4Block struct {
	req    string
	status int
	at     int
	lines  []string
}

// c4Blocks is a client stdout's responses, in order.
func c4Blocks(lines []string) []c4Block {
	var bs []c4Block
	seen := map[string]int{}
	for i, ln := range lines {
		if m := c4StatusLine.FindStringSubmatch(ln); m != nil {
			req := m[1] + " " + m[2]
			seen[req]++
			if seen[req] > 1 {
				req += "#" + strconv.Itoa(seen[req])
			}
			status, _ := strconv.Atoi(m[3]) // the pattern's \d+
			bs = append(bs, c4Block{req: req, status: status, at: i + 1, lines: []string{ln}})
			continue
		}
		if n := len(bs); n > 0 && strings.HasPrefix(ln, "  ") {
			bs[n-1].lines = append(bs[n-1].lines, ln)
		}
	}
	return bs
}

// c4NoDirBodies is what each route that reads the configuration directory answers when no preopen holds it: its
// handler's http.Error, 500, with the error's kind, since a path under no preopen is ENOENT (the fork's F11).
var c4NoDirBodies = map[string]string{
	"GET /": "config", "GET /config": "config", "GET /files": "files", "GET /proxy": "config",
	"POST /proxy-post": "config", "GET /peer-down": "config", "GET /#2": "config", "GET /config#2": "config",
}

// TestC4BurroughsServeWithNoDirAnswersNotExist is C4's control (#1380): the same run with no --dir, so p3svc's
// /config resolves under no preopen. It must give the fork step's no-dir reading (c4NoDirDigest), and it asserts
// that reading's places rather than only that it differs from p3svc.reading:
//   - its first line, the first that differs from the reading, is `GET /: status 500 "500 Internal Server Error"`;
//   - every route that reads the directory answers 500 with `config: not-exist` (`files: not-exist` for /files);
//   - /missing, which answers not-exist with the grant too, and /big, which reads nothing, are the reading's lines.
//
// Its falsification target is TestC4BurroughsServeMatchesTheHostReading, the positive arm: a grant that does not
// reach the component reads as this control, so this control cannot tell it from working, and that arm can.
//
// NEUTER, run: the grant given (runC4Serve(t, true) here): it fails at line 1, `GET /: status 200 "200 OK"` where it
// wants 500, then at each of the eight responses that read the directory (lines 1, 5, 9, 18, 24, 28, 38 and 42), and
// at the digest, 2622 bytes where the fork step read 2459.
func TestC4BurroughsServeWithNoDirAnswersNotExist(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	got := runC4Serve(t, false)
	want, err := os.ReadFile(c4Reading)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	gl, wl := c4Lines(got), c4Lines(want)
	if first := `GET /: status 500 "500 Internal Server Error"`; gl[0] != first || wl[0] == first {
		t.Errorf("line 1 reads %q, where the no-dir reading's first line, and the first that differs from "+
			"p3svc.reading's %q, is %q", gl[0], wl[0], first)
	}
	gb, wb := c4Blocks(gl), c4Blocks(wl)
	if len(gb) != 10 || len(wb) != 10 {
		t.Fatalf("%d responses here and %d in the reading, where the client makes ten requests:\n%s", len(gb), len(wb), got)
	}
	byReq := map[string]c4Block{}
	for _, b := range wb {
		byReq[b.req] = b
	}
	for _, b := range gb {
		if kind, ok := c4NoDirBodies[b.req]; ok {
			body := `  body "` + kind + `: not-exist\n"`
			if b.status != http.StatusInternalServerError || !strings.Contains(strings.Join(b.lines, "\n"), body) {
				t.Errorf("line %d: %s answers:\n%s\nwhere with no preopen it answers 500 and %s", b.at, b.req,
					strings.Join(b.lines, "\n"), strings.TrimSpace(body))
			}
			continue
		}
		if w := byReq[b.req]; strings.Join(b.lines, "\n") != strings.Join(w.lines, "\n") {
			t.Errorf("line %d: %s answers:\n%s\nwhere it answers as p3svc.reading's line %d does, with or without "+
				"the grant:\n%s", b.at, b.req, strings.Join(b.lines, "\n"), w.at, strings.Join(w.lines, "\n"))
		}
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(got))
	if len(got) != c4NoDirBytes || !strings.HasPrefix(sum, c4NoDirSHA256) {
		t.Errorf("the no-dir stdout is %s, where the fork step read %d bytes, sha256 %s..., on both engines:\n%s",
			c4Describe(got), c4NoDirBytes, c4NoDirSHA256, got)
	}
}
