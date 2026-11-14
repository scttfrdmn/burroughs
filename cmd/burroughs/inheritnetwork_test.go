// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

const p3tcp = "../../internal/component/testdata/p3tcp/p3tcp.wasm"

// readingStdout is a committed reading's stdout: its `stdout: ` lines, each newline-terminated.
func readingStdout(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	var want strings.Builder
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, "stdout: "); ok {
			want.WriteString(rest + "\n")
		}
	}
	return want.String()
}

// echoPeer is an echo listener on 127.0.0.1 and the port of a listener it bound and closed, p3tcp/echo's shape.
func echoPeer(t *testing.T) (port, closed string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed = strconv.Itoa(gone.Addr().(*net.TCPAddr).Port)
	_ = gone.Close()
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), closed
}

// TestRunAComponentWithInheritNetworkConnects is `--inherit-network` at the CLI (ADR 0123): `burroughs run
// --inherit-network p3tcp.wasm -- <port> <closed>` exits 0 with wasmtime 49.0.3's `-S inherit-network` reading,
// and without the flag with its no-flag reading, both connects PermissionDenied.
//
// Neutered, run: runComponent not passing the flag to ComponentConfig.InheritNetwork, and the granted row prints the
// denied reading.
func TestRunAComponentWithInheritNetworkConnects(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	for _, c := range []struct {
		name    string
		flags   []string
		reading string
	}{
		{"granted", []string{"--inherit-network"}, "../../internal/component/testdata/p3tcp.reading"},
		{"denied", nil, "../../internal/component/testdata/p3tcp-denied.reading"},
	} {
		t.Run(c.name, func(t *testing.T) {
			port, closed := echoPeer(t)
			var out, errBuf bytes.Buffer
			argv := append(append([]string{"run"}, c.flags...), p3tcp, "--", port, closed)
			if code := dispatch(&out, &errBuf, argv); code != exitOK {
				t.Fatalf("exited %d, want 0\nstderr: %q", code, errBuf.String())
			}
			if want := readingStdout(t, c.reading); out.String() != want {
				t.Errorf("stdout = %q, want wasmtime's %q", out.String(), want)
			}
		})
	}
}

// TestInheritNetworkIsAComponentsFlag is #1128's rule for `--inherit-network` (ADR 0123): a wasip1 command's
// preview-1 host serves no sockets and a core module takes no capabilities, so the flag is refused by name as a usage
// error rather than dropped.
//
// Neutered, run: the refusal dropped, and the wasip1 command runs, exit 0, with the flag ignored.
func TestInheritNetworkIsAComponentsFlag(t *testing.T) {
	cmd := writeModule(t, "cmd.wasm", `(module
		(import "wasi_snapshot_preview1" "proc_exit" (func (param i32)))
		(memory (export "memory") 1)
		(func (export "_start")))`)
	core := writeModule(t, "core.wasm", `(module (func (export "f")))`)
	for _, c := range []struct {
		name string
		argv []string
	}{
		{"a wasip1 command", []string{"run", "--inherit-network", cmd}},
		{"a core module", []string{"run", "--inherit-network", core, "f"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := dispatch(&out, &errBuf, c.argv)
			if code != exitUsage || !strings.Contains(errBuf.String(), "--inherit-network applies to a component") {
				t.Errorf("exit %d, stderr %q: want a usage refusal naming --inherit-network", code, errBuf.String())
			}
		})
	}
}
