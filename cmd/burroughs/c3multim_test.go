// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// C3's files (#1345), v0.15.0's witness, beside C2's in the fork's witness directory.
const (
	c3Source  = c2Dir + "/p3multim.go.txt"
	c3Reading = c2Dir + "/p3multim.reading"
)

// c3GoMod is p3multim's go.mod, for every build of it, so every build takes the same language version's GODEBUG
// defaults (C2's reason, c2GoMod).
const c3GoMod = "module p3multim\n\ngo 1.26\n"

// c3BuildFlags is what every build of p3multim passes beside -trimpath: its M identity is a linkname to
// runtime.acquirem, which the linker refuses without it.
var c3BuildFlags = []string{"-ldflags=-checklinkname=0"}

var updateC3 = flag.Bool("update-c3multim", false,
	"regenerate internal/component/testdata/forkp3/p3multim.reading from arm N (the stock-Go host build) instead of checking it")

// c3Conns is the number of concurrent clients in the concurrent and GC rows, and the number of /hold requests the
// program waits for before its forced GC.
const c3Conns = 8

// c3PerConn is the concurrent row's requests per connection.
const c3PerConn = 16

// c3Sizes are the concurrent row's request bodies' lengths: either side of the 4 KiB read and capture boundaries, and
// two larger than one read.
var c3Sizes = []int{0, 1, 63, 4095, 4096, 4097, 40000, 131072}

// driveC3 is C3's client (#1345), shared by every arm. It reads the server's stdout to its READY line and drives four
// rows over raw TCP and HTTP/1.1, recording each response's raw bytes (C2's captures):
//
//  1. keep-alive: on one connection, /hello, /work?id=0..7, a 404, and three 400s (/work, /hold and /sum refusing);
//  2. concurrent: c3Conns connections at once, each keep-alive over c3PerConn requests mixing /work, /hello, and
//     POST /echo and /sum with bodies of c3Sizes' lengths, each body written while its response is read;
//  3. gc: c3Conns connections each send GET /hold?id=i, all before any response is read. Every /hold parks in its
//     handler until main's forced GC returns, and the program forces it only once all eight are inside, so the GC
//     runs with eight requests in flight. Then the eight responses are read;
//  4. quit: GET /quit, Connection: close, and the server's exit.
//
// It returns the reading's lines: one per request, the server's stdout after READY, and its exit code; and the exit,
// whose stderr places the Ms. Every wait has c2RequestGuard, and a failure is returned at its place.
func driveC3(srv c2Server) ([]string, c2Exit, error) {
	lines, d, err := driveC3Rows(srv)
	if d != nil && d.exit != nil {
		return lines, *d.exit, err
	}
	return lines, c2Exit{}, err
}

func driveC3Rows(srv c2Server) ([]string, *c2Driver, error) {
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
		return nil, d, d.failed("READY", os.ErrDeadlineExceeded)
	}
	var lines []string
	port, served := strings.CutPrefix(first, "READY ")
	if served {
		d.port = strings.TrimSuffix(port, "\n")
		for _, row := range []func(*c2Driver) ([]string, error){c3KeepAlive, c3Concurrent, c3GC, c3Quit} {
			ls, err := row(d)
			if err != nil {
				return nil, d, err
			}
			lines = append(lines, ls...)
		}
	}
	exit, err := d.waitExit()
	if err != nil {
		return nil, d, err
	}
	var out string
	select {
	case out = <-rest:
	case <-time.After(c2RequestGuard):
		return nil, d, d.failed("the stdout's EOF", os.ErrDeadlineExceeded)
	}
	if served {
		lines = append(lines, "stdout after READY: "+strconv.Quote(out))
	} else {
		lines = append(lines, "stdout, with no READY line: "+strconv.Quote(first+out))
	}
	return append(lines, "exit: "+strconv.Itoa(exit.code)), d, nil
}

// c3PostHead is a kept-alive POST's head for a body of n bytes.
func c3PostHead(path string, n int) string {
	return "POST " + path + " HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: " + strconv.Itoa(n) + "\r\n\r\n"
}

// c3RoundTrip writes a request's head and body while it reads one kept-alive response, under one c2RequestGuard
// deadline, so a body larger than the socket buffers cannot park the client against the server's echo of it.
func c3RoundTrip(d *c2Driver, c *net.TCPConn, br *bufio.Reader, req, head string, body []byte) ([]byte, error) {
	_ = c.SetDeadline(time.Now().Add(c2RequestGuard))
	wrote := make(chan error, 1)
	go func() {
		_, werr := io.WriteString(c, head)
		if werr == nil && len(body) > 0 {
			_, werr = c.Write(body)
		}
		wrote <- werr
	}()
	raw, _, err := readC2Response(br, false)
	werr := <-wrote
	if err != nil {
		return nil, d.failed(req, err)
	}
	if werr != nil {
		return nil, d.failed(req+"'s write", werr)
	}
	d.answered.Add(1)
	return raw, nil
}

// c3KeepAlive is row 1.
func c3KeepAlive(d *c2Driver) ([]string, error) {
	paths := []string{"/hello"}
	for id := range 8 {
		paths = append(paths, "/work?id="+strconv.Itoa(id))
	}
	paths = append(paths, "/nope", "/work?id=128", "/hold?id=8", "/sum")
	c, br, err := d.dial(paths[0])
	if err != nil {
		return nil, err
	}
	defer c.Close()
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		raw, err := c3RoundTrip(d, c, br, p, c2Get(p, false), nil)
		if err != nil {
			return nil, err
		}
		lines = append(lines, "keep-alive GET "+p+": "+c2Capture(raw))
	}
	return lines, nil
}

// c3Request is the concurrent row's request j on connection i: its line's name, its head and its body.
func c3Request(i, j int) (name, head string, body []byte) {
	k := i*c3PerConn + j
	switch j % 4 {
	case 0:
		p := "/work?id=" + strconv.Itoa(k%128)
		return "GET " + p, c2Get(p, false), nil
	case 1:
		n := c3Sizes[k%len(c3Sizes)]
		return fmt.Sprintf("POST /echo (%d bytes)", n), c3PostHead("/echo", n), c2Payload(n, uint64(k)+1345)
	case 2:
		n := c3Sizes[(k+3)%len(c3Sizes)]
		return fmt.Sprintf("POST /sum (%d bytes)", n), c3PostHead("/sum", n), c2Payload(n, uint64(k)+3451)
	default:
		return "GET /hello", c2Get("/hello", false), nil
	}
}

// c3Concurrent is row 2: c3Conns connections dialled first, then driven at once.
func c3Concurrent(d *c2Driver) ([]string, error) {
	var conns [c3Conns]*net.TCPConn
	var brs [c3Conns]*bufio.Reader
	for i := range c3Conns {
		c, br, err := d.dial(fmt.Sprintf("concurrent connection %d of %d", i, c3Conns))
		if err != nil {
			return nil, err
		}
		defer c.Close()
		conns[i], brs[i] = c, br
	}
	var lines [c3Conns][]string
	var errs [c3Conns]error
	var wg sync.WaitGroup
	for i := range c3Conns {
		wg.Go(func() {
			for j := range c3PerConn {
				name, head, body := c3Request(i, j)
				raw, err := c3RoundTrip(d, conns[i], brs[i], fmt.Sprintf("%s (connection %d of %d)", name, i, c3Conns),
					head, body)
				if err != nil {
					errs[i] = err
					return
				}
				lines[i] = append(lines[i], fmt.Sprintf("concurrent connection %d %s: %s", i, name, c2Capture(raw)))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs[:]...); err != nil {
		return nil, err
	}
	return slices.Concat(lines[:]...), nil
}

// c3GC is row 3: c3Conns /hold requests in flight at once, which the program's forced GC waits for.
func c3GC(d *c2Driver) ([]string, error) {
	var brs [c3Conns]*bufio.Reader
	for i := range c3Conns {
		p := "/hold?id=" + strconv.Itoa(i)
		c, br, err := d.dial(p)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(c2RequestGuard))
		if _, err := io.WriteString(c, c2Get(p, false)); err != nil {
			return nil, d.failed(p, err)
		}
		brs[i] = br
	}
	lines := make([]string, 0, c3Conns)
	for i := range c3Conns {
		p := "/hold?id=" + strconv.Itoa(i)
		raw, _, err := readC2Response(brs[i], false)
		if err != nil {
			return nil, d.failed(p+", in flight through the forced GC", err)
		}
		d.answered.Add(1)
		lines = append(lines, "gc connection "+strconv.Itoa(i)+" GET "+p+": "+c2Capture(raw))
	}
	return lines, nil
}

// c3Quit is row 4: /quit, then the server's exit.
func c3Quit(d *c2Driver) ([]string, error) {
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
	return []string{"quit GET /quit: " + c2Capture(raw)}, nil
}

// c3Rendezvous is the guest's stderr line that places the rendezvous: "rendezvous M of N", M participants having
// met while all N were spinning.
var c3Rendezvous = regexp.MustCompile(`(?m)^rendezvous (\d+) of (\d+)$`)

// c3Places is what a run's stderr says about its Ms: the rendezvous line, and the lines the guest marks as varying
// between arms (how many Ms ran the CPU jobs and the handlers, and their shares), for the log.
func c3Places(stderr string) (rendezvous string, ms []string) {
	if m := c3Rendezvous.FindStringSubmatch(stderr); m != nil {
		rendezvous = m[1] + " of " + m[2]
	}
	for _, ln := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(ln, "gomaxprocs ") || strings.Contains(ln, "ms ran ") {
			ms = append(ms, ln)
		}
	}
	return rendezvous, ms
}

// TestC3HostServerMatchesItsReading is C3's arm N (#1345): p3multim.go.txt built by stock Go, the Go running this
// test, and run as a host process at GOMAXPROCS 4 (its default) and 1 (its argv). Its responses, stdout and exit,
// read by driveC3, must equal p3multim.reading line for line in both. The reading was taken from this arm at 4, by
// `-update-c3multim`, and committed before any wasip3 build of the source.
//
// The rendezvous is logged here, not asserted: a host process's goroutines are preempted asynchronously, so its
// rendezvous is not the place it is on wasm.
func TestC3HostServerMatchesItsReading(t *testing.T) {
	bin, goVersion := buildForkSourceForHost(t, c3Source, c3GoMod, "p3multim", c3BuildFlags...)
	for _, procs := range []string{"4", "1"} {
		t.Run("procs="+procs, func(t *testing.T) {
			got, exit, err := driveC3(startC2Process(t, bin, procs))
			if err != nil {
				t.Fatalf("arm N: %v (stderr %q)", err, exit.stderr)
			}
			rendezvous, ms := c3Places(exit.stderr)
			t.Logf("arm N, procs %s: rendezvous %s; %s (logged, not asserted)", procs, rendezvous, strings.Join(ms, "; "))
			if *updateC3 {
				if procs == "4" {
					writeC3Reading(t, goVersion, got)
				}
				return
			}
			matchC2Reading(t, c3Reading, "arm N ("+goVersion+", procs "+procs+")", got)
		})
	}
}

// writeC3Reading writes p3multim.reading from arm N's lines, with the header that says how it was taken.
func writeC3Reading(t *testing.T, goVersion string, lines []string) {
	t.Helper()
	header := []string{
		"# Committed reading of p3multim.go.txt, C3's witness (#1345): arm N, the source built by stock Go and run as a",
		"# host process at GOMAXPROCS 4, driven by cmd/burroughs/c3multim_test.go's driveC3.",
		"# Regenerated by: go test ./cmd/burroughs/ -run TestC3HostServerMatchesItsReading -update-c3multim   (never hand-edited)",
		"# Built:          go build -trimpath -ldflags=-checklinkname=0, CGO_ENABLED=0, go.mod " + strconv.Quote(c3GoMod) +
			", by " + goVersion + " " + runtime.GOOS + "/" + runtime.GOARCH,
		"# Taken:          " + time.Now().Format("2006-01-02"),
		"#",
		"# One line per request, in the driver's order, \"<row> <request>: <capture>\": a capture is the response's raw bytes,",
		"# Go-quoted, under 4 KiB, and \"N bytes, sha256 H\" otherwise. Then the server's stdout after its READY line, Go-quoted,",
		"# and its exit code. The port is not in the reading, and neither is stderr, which carries what differs between arms.",
		"",
	}
	body := strings.Join(header, "\n") + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(c3Reading, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d lines", c3Reading, len(lines))
}
