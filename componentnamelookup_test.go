// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestAllowNameLookupReachesBothConfiguredPaths is ComponentConfig.AllowNameLookup at the public boundary (ADR 0126,
// Scott's stamp on #1235): Run and LoadComponent each thread it to the host, its zero value denies the lookup, and
// the bare LoadComponent stays denied. name-lookup.wasm's run(1) resolves the literal 127.0.0.1, which no resolver
// answers: 16843008 granted, and 132 (permanent-resolver-failure) denied, wasmtime 49.0.3's two readings
// (name-lookup.reading). Run is p3lookup's, whose first line says whether localhost resolved.
//
// Neutered, run: either path's `h.AllowNameLookup = c.AllowNameLookup` dropped, and that row is denied.
func TestAllowNameLookupReachesBothConfiguredPaths(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	const granted, denied = 16843008, 132
	t.Run("LoadComponent", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/name-lookup.wasm")
		if err != nil {
			t.Fatal(err)
		}
		bare, err := burroughs.LoadComponent(wasm)
		if err != nil {
			t.Fatalf("LoadComponent: %v", err)
		}
		defer bare.Close()
		for _, c := range []struct {
			name string
			load func() (*burroughs.Component, error)
			want uint32
		}{
			{"granted", func() (*burroughs.Component, error) {
				return burroughs.ComponentConfig{AllowNameLookup: true}.LoadComponent(wasm)
			}, granted},
			{"zero config", func() (*burroughs.Component, error) { return burroughs.ComponentConfig{}.LoadComponent(wasm) }, denied},
			{"bare", func() (*burroughs.Component, error) { return bare, nil }, denied},
		} {
			comp, err := c.load()
			if err != nil {
				t.Fatalf("%s: LoadComponent: %v", c.name, err)
			}
			res, err := comp.Call(context.Background(), "run", burroughs.ComponentU32(1))
			if err != nil {
				t.Fatalf("%s: run(1): %v", c.name, err)
			}
			if v, _ := res[0].U32(); v != c.want {
				t.Errorf("%s: run(1) = %d, want %d", c.name, v, c.want)
			}
			if comp != bare {
				comp.Close()
			}
		}
	})
	t.Run("Run", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/p3lookup/p3lookup.wasm")
		if err != nil {
			t.Fatal(err)
		}
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
				go func() { // an echo peer, p3tcp/echo's shape
					defer c.Close()
					_, _ = io.Copy(c, c)
				}()
			}
		}()
		port := ln.Addr().(*net.TCPAddr).Port
		for _, grant := range []bool{true, false} {
			var out bytes.Buffer
			cfg := burroughs.ComponentConfig{
				Args:            []string{"p3lookup.wasm", strconv.Itoa(port)},
				Stdout:          &out,
				InheritNetwork:  true,
				AllowNameLookup: grant,
			}
			if _, err := cfg.Run(wasm); err != nil {
				t.Fatalf("Run (grant %v): %v", grant, err)
			}
			first, _, _ := bytes.Cut(out.Bytes(), []byte("\n"))
			want := "p3lookup: localhost: Uncategorized"
			if grant {
				want = "p3lookup: localhost: resolved"
			}
			if !strings.HasPrefix(string(first), want) {
				t.Errorf("grant %v: first line %q, want it to begin %q", grant, first, want)
			}
		}
	})
}
