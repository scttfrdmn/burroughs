// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

const p3http = "../../internal/component/testdata/p3http/p3http.wasm"

// p3httpRowStdout is the stdout of one row of p3http.reading, each line newline-terminated as the guest printed it.
func p3httpRowStdout(t *testing.T, row string) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/component/testdata/p3http.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	var out strings.Builder
	in := false
	for _, ln := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(ln, "row: "):
			in = ln == "row: "+row
		case in && strings.HasPrefix(ln, "stdout: "):
			out.WriteString(strings.TrimPrefix(ln, "stdout: ") + "\n")
		}
	}
	if out.Len() == 0 {
		t.Fatalf("p3http.reading has no stdout for row %s", row)
	}
	return out.String()
}

// TestRunAComponentWithAllowHTTPSends is `--allow-http` at the CLI (ADR 0135, Scott's ruling on #1375): `burroughs run
// --allow-http p3http.wasm -- get <port> <closed>` exits 0 with wasmtime 49.0.3's `-S http` reading of the get row,
// against a peer whose /hello is the oracle peer's. Without the flag the component still runs, exit 0, and its send
// answers HttpRequestDenied at the call, where wasmtime refuses to link it (the reading's denied row): the named
// divergence. The peer is never reached then.
func TestRunAComponentWithAllowHTTPSends(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	reached := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		w.Header().Set("X-Peer", "hello")
		fmt.Fprintf(w, "hello, %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	denied := "request headers set: Err(\"HeaderError::Immutable\")\n" +
		"send: err ErrorCode::HttpRequestDenied\n" +
		"sent: Err(\"ErrorCode::HttpRequestDenied\")\n"
	for _, c := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"granted", []string{"--allow-http"}, p3httpRowStdout(t, "get")},
		{"denied", nil, denied},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			argv := append(append([]string{"run"}, c.flags...), p3http, "--", "get", port, "0")
			if code := dispatch(&out, &errBuf, argv); code != exitOK {
				t.Fatalf("exited %d, want 0\nstderr: %q", code, errBuf.String())
			}
			if out.String() != c.want {
				t.Errorf("stdout = %q, want %q", out.String(), c.want)
			}
			select {
			case <-reached:
				if c.name == "denied" {
					t.Error("the peer was reached by a send the missing flag denies")
				}
			default:
				if c.name == "granted" {
					t.Error("the granted send never reached the peer")
				}
			}
		})
	}
}

// TestAllowHTTPIsAComponentsFlag is #1128's rule for `--allow-http` (ADR 0135): a wasip1 command's preview-1 host
// serves no wasi:http and a core module takes no capabilities, so the flag is refused by name as a usage error rather
// than dropped.
func TestAllowHTTPIsAComponentsFlag(t *testing.T) {
	cmd := writeModule(t, "cmd.wasm", `(module
		(import "wasi_snapshot_preview1" "proc_exit" (func (param i32)))
		(memory (export "memory") 1)
		(func (export "_start")))`)
	core := writeModule(t, "core.wasm", `(module (func (export "f")))`)
	for _, c := range []struct {
		name string
		argv []string
	}{
		{"a wasip1 command", []string{"run", "--allow-http", cmd}},
		{"a core module", []string{"run", "--allow-http", core, "f"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := dispatch(&out, &errBuf, c.argv)
			if code != exitUsage || !strings.Contains(errBuf.String(), "--allow-http applies to a component") {
				t.Errorf("exit %d, stderr %q: want a usage refusal naming --allow-http", code, errBuf.String())
			}
		})
	}
}
