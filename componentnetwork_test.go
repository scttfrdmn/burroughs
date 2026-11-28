// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"runtime"
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

// TestInheritNetworkGrantsUDPOnBothConfiguredPaths is ComponentConfig.InheritNetwork's UDP half at the public boundary
// (ADR 0140, Scott's ruling on #1376: the one grant is TCP and UDP). LoadComponent's granted row is udp.wasm's
// sendto-v4 (row 5), two sockets on loopback, which reads 16842752 (0x01010000: sent, received, the bytes, the
// sender); its ungranted row is denied (39), which reads 32896, both creates access-denied. Run's is p3udp, whose first
// line says whether it bound. Both readings are wasmtime 49.0.3's (internal/component/testdata/udp.reading,
// p3udp.reading). Run's granted row is not asked on Windows, where p3udp's closed-port receive waits for an ICMP error
// Go's net turns off.
//
// NEUTER, run: both paths' `h.InheritNetwork = c.InheritNetwork` dropped in one run, and each path's granted row
// failed on its own: LoadComponent's sendto-v4 traps at its first bind, *"handle index 0 is not live"*, create having
// answered access-denied, and Run's first line is p3udp's denied `bind: PermissionDenied`.
func TestInheritNetworkGrantsUDPOnBothConfiguredPaths(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	t.Run("LoadComponent", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/udp.wasm")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			grant bool
			row   uint32
			want  uint32
		}{{true, 5, 16842752}, {false, 39, 32896}} {
			comp, err := burroughs.ComponentConfig{InheritNetwork: c.grant}.LoadComponent(wasm)
			if err != nil {
				t.Fatalf("LoadComponent: %v", err)
			}
			res, err := comp.Call(context.Background(), "run", burroughs.ComponentU32(c.row))
			comp.Close()
			if err != nil {
				t.Fatalf("run(%d) with the grant %v: %v", c.row, c.grant, err)
			}
			if v, _ := res[0].U32(); v != c.want {
				t.Errorf("run(%d) with the grant %v = %d (%#08x), want wasmtime's %d", c.row, c.grant, v, v, c.want)
			}
		}
	})
	t.Run("Run", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/p3udp/p3udp.wasm")
		if err != nil {
			t.Fatal(err)
		}
		for _, grant := range []bool{true, false} {
			if grant && runtime.GOOS == "windows" {
				t.Logf("granted: not asked on windows (Go's net disables SIO_UDP_CONNRESET, so p3udp's closed-port receive waits)")
				continue
			}
			var out bytes.Buffer
			cfg := burroughs.ComponentConfig{Args: []string{"p3udp.wasm"}, Stdout: &out, InheritNetwork: grant}
			if _, err := cfg.Run(wasm); err != nil {
				t.Fatalf("Run (grant %v): %v", grant, err)
			}
			first, _, _ := bytes.Cut(out.Bytes(), []byte("\n"))
			want := "p3udp: bind: PermissionDenied"
			if grant {
				want = "p3udp: bind: ok"
			}
			if string(first) != want {
				t.Errorf("grant %v: first line %q, want %q", grant, first, want)
			}
		}
	})
}
