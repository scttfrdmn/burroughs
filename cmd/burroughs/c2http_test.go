// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// c2Dir holds p3http's source, its wasm, its readings and its PROVENANCE, beside the fork's other witnesses.
const c2Dir = "../../internal/component/testdata/forkp3"

const (
	c2Source  = c2Dir + "/p3http.go.txt"
	c2Reading = c2Dir + "/p3http.reading"
)

// updateC2HTTP regenerates p3http.reading from arm N instead of checking it: the gate-census and watref shape, a
// go test flag rather than a separate command, because the reading is the driver's output and the driver is here.
var updateC2HTTP = flag.Bool("update-c2http", false,
	"regenerate internal/component/testdata/forkp3/p3http.reading from arm N (the stock-Go host build) instead of checking it")

// c2RequestGuard bounds each request the driver makes, and each wait on the server: READY, its exit, and the stdout
// after it. A harness hang guard, not a measurement: what it buys is that a run that parks fails at a place, "parked
// at <request> after <k> answered", rather than as a test binary's timeout.
const c2RequestGuard = 120 * time.Second

// c2CaptureLimit is the size under which a response is recorded as its raw bytes, Go-quoted; at or over it, as its
// length and sha256.
const c2CaptureLimit = 4 << 10

// c2Exit is how a server ended: its exit code, and its stderr, for a failure's message.
type c2Exit struct {
	code   int
	stderr string
}

// c2Server is one arm's server as the driver sees it: its stdout, and a channel that receives its exit once the
// process, or `burroughs run`, has ended and its stdout has been closed.
type c2Server struct {
	stdout io.Reader
	exited <-chan c2Exit
}

// c2Driver is one drive of a server: its port, the number of requests answered so far, which a failure names, and
// the server's exit once the tail row has waited for it.
type c2Driver struct {
	srv      c2Server
	port     string
	answered atomic.Int64
	exit     *c2Exit
}

// driveC2 is C2's client (#1242), shared by every arm. It reads the server's READY line and drives four rows over raw
// TCP and HTTP/1.1, so what it records is each response's raw bytes:
//
//  1. keep-alive: on one connection, /hello, /work?id=0 through 63, a 404, and two 400s;
//  2. concurrent: 8 connections at once, each asking /work for a disjoint eighth of the ids;
//  3. echo: POST /echo with 1 MiB and Connection: close, written while the response is read, then half-closed; the
//     echoed body's sha256 is compared with the request's;
//  4. tail: POST /echo with 96 KiB and Connection: close, of whose response only the first byte is read, which orders
//     /quit after the handler has started; then /quit on a second connection; then the server's exit is waited for,
//     and only then is the first connection read to EOF.
//
// It returns the reading's lines: one per request, the server's stdout after READY, and its exit code. A server that
// prints no READY line (the network denied) is read for its stdout and exit only. Every wait has c2RequestGuard; a
// failure is returned at its place.
func driveC2(srv c2Server) ([]string, error) {
	ready := make(chan string, 1)
	rest := make(chan string, 1)
	go func() {
		br := bufio.NewReader(srv.stdout)
		line, _ := br.ReadString('\n')
		ready <- line
		b, _ := io.ReadAll(br)
		rest <- string(b)
	}()
	d := &c2Driver{srv: srv}
	var first string
	select {
	case first = <-ready:
	case <-time.After(c2RequestGuard):
		return nil, d.failed("READY", os.ErrDeadlineExceeded)
	}
	var lines []string
	port, served := strings.CutPrefix(first, "READY ")
	if served {
		d.port = strings.TrimSuffix(port, "\n")
		for _, row := range []func() ([]string, error){d.keepAlive, d.concurrent, d.echo, d.tail} {
			ls, err := row()
			if err != nil {
				return nil, err
			}
			lines = append(lines, ls...)
		}
	}
	exit, err := d.waitExit()
	if err != nil {
		return nil, err
	}
	var out string
	select {
	case out = <-rest:
	case <-time.After(c2RequestGuard):
		return nil, d.failed("the stdout's EOF", os.ErrDeadlineExceeded)
	}
	if served {
		lines = append(lines, "stdout after READY: "+strconv.Quote(out))
	} else {
		lines = append(lines, "stdout, with no READY line: "+strconv.Quote(first+out))
	}
	return append(lines, "exit: "+strconv.Itoa(exit.code)), nil
}

// failed is a driver failure at req: "parked at req" when a deadline expired, the error otherwise, and how many
// requests had been answered.
func (d *c2Driver) failed(req string, err error) error {
	k := d.answered.Load()
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("parked at %s after %d answered", req, k)
	}
	return fmt.Errorf("%s failed after %d answered: %w", req, k, err)
}

// waitExit waits for the server's exit, once.
func (d *c2Driver) waitExit() (c2Exit, error) {
	if d.exit != nil {
		return *d.exit, nil
	}
	select {
	case e := <-d.srv.exited:
		d.exit = &e
		return e, nil
	case <-time.After(c2RequestGuard):
		return c2Exit{}, d.failed("the server's exit", os.ErrDeadlineExceeded)
	}
}

func (d *c2Driver) dial(req string) (*net.TCPConn, *bufio.Reader, error) {
	c, err := net.DialTimeout("tcp4", "127.0.0.1:"+d.port, c2RequestGuard)
	if err != nil {
		return nil, nil, d.failed(req, err)
	}
	return c.(*net.TCPConn), bufio.NewReader(c), nil
}

// c2Get is a GET request's head, with Connection: close when closing.
func c2Get(path string, closing bool) string {
	h := "GET " + path + " HTTP/1.1\r\nHost: 127.0.0.1\r\n"
	if closing {
		h += "Connection: close\r\n"
	}
	return h + "\r\n"
}

// c2Post is a POST /echo request's head, for a body of n bytes, with Connection: close.
func c2Post(n int) string {
	return "POST /echo HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: " + strconv.Itoa(n) + "\r\nConnection: close\r\n\r\n"
}

// roundTrip writes a request head and reads one response, under one c2RequestGuard deadline.
func (d *c2Driver) roundTrip(c *net.TCPConn, br *bufio.Reader, req, head string, toEOF bool) ([]byte, error) {
	_ = c.SetDeadline(time.Now().Add(c2RequestGuard))
	if _, err := io.WriteString(c, head); err != nil {
		return nil, d.failed(req, err)
	}
	raw, _, err := readC2Response(br, toEOF)
	if err != nil {
		return nil, d.failed(req, err)
	}
	d.answered.Add(1)
	return raw, nil
}

// readC2Response reads one response's raw bytes and its body: the body by its Content-Length, or to EOF when toEOF
// (a Connection: close response, so a body shorter than its declared length is recorded rather than refused). A
// chunked response is refused: its framing would be the server's body reads, which no reading can fix.
func readC2Response(br *bufio.Reader, toEOF bool) (raw, body []byte, err error) {
	var head bytes.Buffer
	length := -1
	for {
		var line string
		line, err = br.ReadString('\n')
		head.WriteString(line)
		if err != nil {
			return nil, nil, err
		}
		if line == "\r\n" {
			break
		}
		k, v, ok := strings.Cut(strings.TrimSuffix(line, "\r\n"), ":")
		if !ok {
			continue // the status line
		}
		switch strings.ToLower(k) {
		case "content-length":
			if length, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, nil, fmt.Errorf("Content-Length %q: %w", v, err)
			}
		case "transfer-encoding":
			return nil, nil, fmt.Errorf("a %s response, whose framing is the server's reads", strings.TrimSpace(v))
		}
	}
	switch {
	case toEOF:
		body, err = io.ReadAll(br)
	case length < 0:
		return nil, nil, errors.New("a kept-alive response with no Content-Length")
	default:
		body = make([]byte, length)
		_, err = io.ReadFull(br, body)
	}
	if err != nil {
		return nil, nil, err
	}
	return append(head.Bytes(), body...), body, nil
}

// c2Capture is a response as the reading records it: its raw bytes Go-quoted under c2CaptureLimit, else its digest.
func c2Capture(raw []byte) string {
	if len(raw) < c2CaptureLimit {
		return strconv.Quote(string(raw))
	}
	return c2Digest(raw)
}

func c2Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes, sha256 %s", len(b), hex.EncodeToString(sum[:]))
}

// c2Payload is n deterministic bytes from seed: a 64-bit LCG's top byte, so the request a reading's digest depends
// on cannot move with a library's generator.
func c2Payload(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed
	for i := range b {
		x = x*6364136223846793005 + 1442695040888963407
		b[i] = byte(x >> 56)
	}
	return b
}

// keepAlive is row 1: every endpoint's GET on one kept-alive connection, in order.
func (d *c2Driver) keepAlive() ([]string, error) {
	paths := []string{"/hello"}
	for id := range 64 {
		paths = append(paths, "/work?id="+strconv.Itoa(id))
	}
	paths = append(paths, "/nope", "/work?id=64", "/work")
	c, br, err := d.dial(paths[0])
	if err != nil {
		return nil, err
	}
	defer c.Close()
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		raw, err := d.roundTrip(c, br, p, c2Get(p, false), false)
		if err != nil {
			return nil, err
		}
		lines = append(lines, "keep-alive GET "+p+": "+c2Capture(raw))
	}
	return lines, nil
}

// c2Conns is row 2's connection count; connection i asks /work for ids i, i+8, ... i+56.
const c2Conns = 8

// concurrent is row 2: c2Conns connections dialled first, then driven at once, each kept alive over its ids.
func (d *c2Driver) concurrent() ([]string, error) {
	var conns [c2Conns]*net.TCPConn
	var brs [c2Conns]*bufio.Reader
	for i := range c2Conns {
		c, br, err := d.dial(fmt.Sprintf("connection %d of %d", i, c2Conns))
		if err != nil {
			return nil, err
		}
		defer c.Close()
		conns[i], brs[i] = c, br
	}
	var lines [c2Conns][]string
	var errs [c2Conns]error
	var wg sync.WaitGroup
	for i := range c2Conns {
		wg.Go(func() {
			for j := range 64 / c2Conns {
				p := "/work?id=" + strconv.Itoa(i+c2Conns*j)
				raw, err := d.roundTrip(conns[i], brs[i], fmt.Sprintf("%s (connection %d of %d)", p, i, c2Conns),
					c2Get(p, false), false)
				if err != nil {
					errs[i] = err
					return
				}
				lines[i] = append(lines[i], fmt.Sprintf("concurrent connection %d GET %s: %s", i, p, c2Capture(raw)))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs[:]...); err != nil {
		return nil, err
	}
	return slices.Concat(lines[:]...), nil
}

// echo is row 3: 1 MiB through POST /echo, written while the response is read, then half-closed, so a body that
// reaches the server short ends at its EOF and is recorded short rather than parking.
func (d *c2Driver) echo() ([]string, error) {
	const req = "POST /echo (1 MiB)"
	c, br, err := d.dial(req)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	payload := c2Payload(1<<20, 1242)
	_ = c.SetDeadline(time.Now().Add(c2RequestGuard))
	wrote := make(chan error, 1)
	go func() {
		_, werr := io.WriteString(c, c2Post(len(payload)))
		if werr == nil {
			_, werr = c.Write(payload)
		}
		if werr == nil {
			werr = c.CloseWrite()
		}
		wrote <- werr
	}()
	raw, body, err := readC2Response(br, true)
	werr := <-wrote
	if err != nil {
		return nil, d.failed(req, err)
	}
	if werr != nil {
		return nil, d.failed(req+"'s write", werr)
	}
	d.answered.Add(1)
	return []string{
		"echo " + req + ": " + c2Capture(raw),
		"echo body equals the request's, by sha256: " + strconv.FormatBool(sha256.Sum256(body) == sha256.Sum256(payload)),
	}, nil
}

// c2TailBytes is row 4's body: under the loopback socket buffers of every OS CI runs on, so the server's writes of
// its echo complete into the kernel while the client reads nothing.
const c2TailBytes = 96 << 10

// tail is row 4, the case F4 left to C2 (#1239): what reaches a client of a response the server finished writing,
// and the client had not read, when the server exits.
func (d *c2Driver) tail() ([]string, error) {
	req := fmt.Sprintf("POST /echo (%d KiB, unread)", c2TailBytes>>10)
	a, _, err := d.dial(req)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	_ = a.SetDeadline(time.Now().Add(c2RequestGuard))
	if _, err = io.WriteString(a, c2Post(c2TailBytes)); err != nil {
		return nil, d.failed(req, err)
	}
	if _, err = a.Write(c2Payload(c2TailBytes, 96)); err != nil {
		return nil, d.failed(req, err)
	}
	// One byte, read raw rather than through a buffer: the handler has started, so /quit's Shutdown finds this
	// connection active and waits for it rather than closing a listener it was never accepted from.
	var first [1]byte
	if _, err = io.ReadFull(a, first[:]); err != nil {
		return nil, d.failed(req+"'s first byte", err)
	}
	q, qbr, err := d.dial("/quit")
	if err != nil {
		return nil, err
	}
	defer q.Close()
	raw, err := d.roundTrip(q, qbr, "/quit", c2Get("/quit", true), true)
	if err != nil {
		return nil, err
	}
	if _, err = d.waitExit(); err != nil {
		return nil, err
	}
	_ = a.SetDeadline(time.Now().Add(c2RequestGuard))
	rest, err := io.ReadAll(a)
	if err != nil {
		return nil, d.failed(req+", read after the server exited", err)
	}
	d.answered.Add(1)
	return []string{
		"tail GET /quit: " + c2Capture(raw),
		"tail " + req + ", read after the server exited: " + c2Digest(append(first[:], rest...)),
	}, nil
}

// c2ReadingLines is a committed reading's lines: every line that is neither empty nor a # comment.
func c2ReadingLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	var lines []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln != "" && !strings.HasPrefix(ln, "#") {
			lines = append(lines, ln)
		}
	}
	return lines
}

// matchC2Reading compares a drive's lines with a committed reading's, naming each line that differs.
func matchC2Reading(t *testing.T, path, arm string, got []string) {
	t.Helper()
	want := c2ReadingLines(t, path)
	if slices.Equal(got, want) {
		return
	}
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
	t.Errorf("%s's %d lines differ from %s's %d:\n%s", arm, len(got), filepath.Base(path), len(want),
		strings.Join(diffs, "\n"))
}

// startC2Process runs name with args as a host process, its stdout an OS pipe so the driver reads what it prints as
// it prints it, and its exit delivered once it has exited.
func startC2Process(t *testing.T, name string, args ...string) c2Server {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = pw
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s did not start: %v", name, err)
	}
	_ = pw.Close()
	exited := make(chan c2Exit, 1)
	go func() {
		_ = cmd.Wait()
		exited <- c2Exit{cmd.ProcessState.ExitCode(), stderr.String()}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pr.Close()
	})
	return c2Server{stdout: pr, exited: exited}
}

// buildC2Host builds p3http.go.txt with the Go running this test, for the host, as the only file of a module whose
// go.mod the fork's build of it also has.
func buildC2Host(t *testing.T) (bin, goVersion string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go on PATH, which go test puts its own GOROOT/bin at the head of: %v", err)
	}
	src, err := os.ReadFile(c2Source)
	if err != nil {
		t.Fatalf("p3http's source is missing (it is committed): %v", err)
	}
	dir := t.TempDir()
	for name, b := range map[string][]byte{"main.go": src, "go.mod": []byte(c2GoMod)} {
		if werr := os.WriteFile(filepath.Join(dir, name), b, 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	bin = filepath.Join(dir, "p3http")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	env := append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "GOFLAGS=",
		"GOWORK=off", "GOTOOLCHAIN=local")
	build := exec.Command(goBin, "build", "-trimpath", "-o", bin, ".")
	build.Dir, build.Env = dir, env
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("go build of p3http.go.txt failed: %v\n%s", berr, out)
	}
	ver := exec.Command(goBin, "env", "GOVERSION")
	ver.Dir, ver.Env = dir, env
	out, err := ver.Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}
	return bin, strings.TrimSpace(string(out))
}

// c2GoMod is p3http's go.mod, for both of its builds: the language version is the repository's floor, so stock Go and
// the fork both build it with the same GODEBUG defaults.
const c2GoMod = "module p3http\n\ngo 1.26\n"

// TestC2HostServerMatchesItsReading is C2's arm N (#1242): p3http.go.txt built by stock Go, the Go running this test,
// and run as a host process. Its responses, stdout and exit, read by driveC2, must equal p3http.reading line for line.
// The reading was taken from this arm, by `-update-c2http`, and committed before any wasip3 build of the source, so
// the arms that run the fork's build (wasmtime offline, `burroughs run` in TestC2BurroughsRunMatchesTheHostReading)
// are compared with what Go's own net/http answers, and this arm keeps that reading honest wherever CI runs.
func TestC2HostServerMatchesItsReading(t *testing.T) {
	bin, goVersion := buildC2Host(t)
	got, err := driveC2(startC2Process(t, bin))
	if err != nil {
		t.Fatalf("arm N: %v", err)
	}
	if *updateC2HTTP {
		writeC2Reading(t, goVersion, got)
		return
	}
	matchC2Reading(t, c2Reading, "arm N ("+goVersion+")", got)
}

// writeC2Reading writes p3http.reading from arm N's lines, with the header that says how it was taken.
func writeC2Reading(t *testing.T, goVersion string, lines []string) {
	t.Helper()
	header := []string{
		"# Committed reading of p3http.go.txt, C2's witness (#1242): arm N, the source built by stock Go and run as a host",
		"# process, driven by cmd/burroughs/c2http_test.go's driveC2.",
		"# Regenerated by: go test ./cmd/burroughs/ -run TestC2HostServerMatchesItsReading -update-c2http   (never hand-edited)",
		"# Built:          go build -trimpath, CGO_ENABLED=0, go.mod " + strconv.Quote(c2GoMod) + ", by " + goVersion + " " +
			runtime.GOOS + "/" + runtime.GOARCH,
		"# Taken:          " + time.Now().Format("2006-01-02"),
		"#",
		"# One line per request, in the driver's order, \"<row> <request>: <capture>\": a capture is the response's raw bytes,",
		"# Go-quoted, under 4 KiB, and \"N bytes, sha256 H\" otherwise. Then the server's stdout after its READY line, Go-quoted,",
		"# and its exit code. The port is not in the reading: the server prints it only on the READY line.",
		"",
	}
	body := strings.Join(header, "\n") + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(c2Reading, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d lines", c2Reading, len(lines))
}
