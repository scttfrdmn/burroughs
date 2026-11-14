// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestInheritNetworkReachesBothConfiguredPaths is ComponentConfig.InheritNetwork at the public boundary (ADR 0123,
// Scott's stamp on #1235): Run and LoadComponent each thread it to the host, and its zero value denies the network.
// tcp-connect-park.wasm's run(port) connects to a listening port and returns SUBTASK << 16 | RETURNED << 8 | its
// result's discriminant: 66048 when the connect is ok, wasmtime 49.0.3's `listening` reading. Run is p3tcp's,
// whose first line says whether it connected.
//
// Neutered, run: either path's `h.InheritNetwork = c.InheritNetwork` dropped, and that row is denied.
func TestInheritNetworkReachesBothConfiguredPaths(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // an echo peer, p3tcp/echo's shape: echo to the client's shutdown, then close
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	t.Run("LoadComponent", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/tcp-connect-park.wasm")
		if err != nil {
			t.Fatal(err)
		}
		c, err := burroughs.ComponentConfig{InheritNetwork: true}.LoadComponent(wasm)
		if err != nil {
			t.Fatalf("LoadComponent: %v", err)
		}
		defer c.Close()
		res, err := c.Call(context.Background(), "run", burroughs.ComponentU32(uint32(port)))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v, _ := res[0].U32(); v != 66048 {
			t.Errorf("run(%d) = %d, want 66048 (connected)", port, v)
		}
	})
	t.Run("Run", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/p3tcp/p3tcp.wasm")
		if err != nil {
			t.Fatal(err)
		}
		for _, grant := range []bool{true, false} {
			var out bytes.Buffer
			gone, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			closed := gone.Addr().(*net.TCPAddr).Port
			_ = gone.Close()
			cfg := burroughs.ComponentConfig{
				Args:           []string{"p3tcp.wasm", strconv.Itoa(port), strconv.Itoa(closed), "0"},
				Stdout:         &out,
				InheritNetwork: grant,
			}
			if _, err := cfg.Run(wasm); err != nil {
				t.Fatalf("Run (grant %v): %v", grant, err)
			}
			first, _, _ := bytes.Cut(out.Bytes(), []byte("\n"))
			want := "p3tcp: connect: PermissionDenied"
			if grant {
				want = "p3tcp: connect: ok"
			}
			if string(first) != want {
				t.Errorf("grant %v: first line %q, want %q", grant, first, want)
			}
		}
	})
}
