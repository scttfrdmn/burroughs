// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestAllowHTTPReachesBothConfiguredPaths is ComponentConfig.AllowHTTP at the public boundary (ADR 0135, Scott's
// ruling on #1375): Run and LoadComponent each thread it to the host, and its zero value denies the send at the
// call, not the link. p3http's get row sends GET /hello to a peer on 127.0.0.1: granted, it prints the peer's status,
// wasmtime 49.0.3's `-S http` reading (internal/component/testdata/p3http.reading); denied, it instantiates and prints
// HttpRequestDenied, the named divergence from wasmtime's link failure, and the peer is never reached. LoadComponent's
// path calls the component's `wasi:cli/run@0.3.0#run` through Call.
//
// The bare LoadComponent gives the guest no argv, so it cannot be pointed at a peer; its host is built without the
// grant, which is the zero value this test reads through the configured paths.
func TestAllowHTTPReachesBothConfiguredPaths(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/p3http/p3http.wasm")
	if err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		w.Header().Set("X-Peer", "hello")
		fmt.Fprintf(w, "hello, %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	args := []string{"p3http.wasm", "get", port, "0"}
	paths := []struct {
		name string
		run  func(cfg burroughs.ComponentConfig) error
	}{
		{"Run", func(cfg burroughs.ComponentConfig) error {
			code, err := cfg.Run(wasm)
			if err == nil && code != 0 {
				err = fmt.Errorf("exit %d", code)
			}
			return err
		}},
		{"LoadComponent", func(cfg burroughs.ComponentConfig) error {
			comp, err := cfg.LoadComponent(wasm)
			if err != nil {
				return err
			}
			defer comp.Close()
			_, err = comp.Call(context.Background(), "wasi:cli/run@0.3.0#run")
			// **Call cannot lift run's `result` yet**, and refuses it by name once the guest has returned: a limit of
			// Call's sync-lift path that predates this slice. The guest has run to its end by then, so its stdout is the
			// reading; any other error fails the row.
			if err != nil && errors.Is(err, burroughs.ErrUnsupported) && strings.Contains(err.Error(), "one-word result") {
				return nil
			}
			return err
		}},
	}
	for _, p := range paths {
		for _, grant := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/grant=%v", p.name, grant), func(t *testing.T) {
				var out bytes.Buffer
				if err := p.run(burroughs.ComponentConfig{Args: args, Stdout: &out, AllowHTTP: grant}); err != nil {
					t.Fatalf("%s: %v\nstdout: %q", p.name, err, out.String())
				}
				want := "send: err ErrorCode::HttpRequestDenied\n"
				if grant {
					want = "status: 200\n"
				}
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout = %q, want a line %q", out.String(), want)
				}
				select {
				case <-reached:
					if !grant {
						t.Error("the peer was reached by a send the zero value denies")
					}
				default:
					if grant {
						t.Error("the granted send never reached the peer")
					}
				}
			})
		}
	}
}
