// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scttfrdmn/burroughs"
)

// The serve subcommand (#1385, ADR 0137, Scott's ruling 2 on #1375): `burroughs serve --addr ADDR file.wasm` serves a
// component's wasi:http/handler@0.3 export over HTTP, mirroring `wasmtime serve`. Like run, it is a consumer of the
// public package only (decision 0029): it serves Component.Handler() on a net/http server of its own.

// serveDefaultAddr is wasmtime serve's default address.
const serveDefaultAddr = "0.0.0.0:8080"

// serveShutdownBound is how long a stop waits for requests in flight before closing their connections: a bound on a
// teardown, so a guest that never finishes a response cannot hold the process.
const serveShutdownBound = 5 * time.Second

// serveReadHeaderTimeout bounds a client's request head, so a connection that never sends one is not held for ever.
// The body and the response are the guest's to take as long as they take, as under wasmtime serve.
const serveReadHeaderTimeout = 30 * time.Second

// serveCmd implements `burroughs serve`, including the reporting of its own failure, runCmd's shape. It serves until
// stop delivers, then shuts the server down and closes the component.
func serveCmd(stdout, stderr io.Writer, argv []string, stop <-chan os.Signal) error {
	err := serve(stdout, stderr, argv, stop)
	if err != nil && !errors.Is(err, errUsage) {
		diagnose(stderr, err)
	}
	return err
}

// serve is serveCmd's body: everything but the reporting.
func serve(stdout, stderr io.Writer, argv []string, stop <-chan os.Signal) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", serveDefaultAddr, "the address to listen on, HOST:PORT; a port of 0 listens on one the "+
		"system chooses, and the address served is printed")
	// The four grant flags are run's, parsed by run's own flag types, so their syntax, refusals and meaning are
	// identical (ADR 0137).
	var dirs preopenFlag
	fs.Var(&dirs, "dir", "grant the component a READ-ONLY directory as HOST[:/GUEST], ONE colon (repeatable), "+
		"as for run")
	var scratch scratchFlag
	fs.Var(&scratch, "scratch", "grant the component a WRITABLE directory as HOST[:/GUEST] (repeatable), as for run")
	inheritNetwork := fs.Bool("inherit-network", false, "grant the component wasi:sockets TCP to any address, as for run")
	allowNameLookup := fs.Bool("allow-ip-name-lookup", false, "grant the component wasi:sockets/ip-name-lookup, as "+
		"for run")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: burroughs serve [--addr HOST:PORT] [--dir HOST[:/GUEST]]... [--scratch HOST[:/GUEST]]... "+
			"[--inherit-network] [--allow-ip-name-lookup] <file.wasm>")
		fmt.Fprintln(stderr, "\nServes a component's wasi:http/handler@0.3 export over HTTP, as wasmtime serve does: each "+
			"request is a call of\nits handle, on an instance of its own. It prints \"Serving HTTP on http://ADDR/\" on "+
			"stderr once it listens,\nand serves until interrupted. wasi:cli/run is never called.")
		fmt.Fprintln(stderr, "\nThe component may send outgoing HTTP (wasi:http/client), as under wasmtime serve, so serve "+
			"takes no --allow-http.\n--dir, --scratch, --inherit-network and --allow-ip-name-lookup are run's, with the "+
			"same syntax and meaning.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return errUsage
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	file := fs.Arg(0)
	wasm, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if isComp, cerr := burroughs.IsComponent(wasm); cerr != nil || !isComp {
		fmt.Fprintf(stderr, "%sserve serves a component's wasi:http/handler; %s is not a component\n", prefix, file)
		return errUsage
	}
	c, err := burroughs.ComponentConfig{
		Args:   []string{filepath.Base(file)},
		Stdout: stdout,
		Stderr: stderr,
		// NEUTER, run: drop this line, and TestServeAComponent's send row answers "send: err
		// ErrorCode::HttpRequestDenied", where serve grants outgoing HTTP as wasmtime's does.
		AllowHTTP:       true,
		InheritNetwork:  *inheritNetwork,
		AllowNameLookup: *allowNameLookup,
		Preopens:        append(dirs.preopens(), scratch.preopens()...),
	}.LoadComponent(wasm)
	if err != nil {
		return err
	}
	defer c.Close()
	// **A component with no handler is refused before anything listens** (ADR 0137): Handler would answer every
	// request with its 500 page, and serve can say why. wasmtime 49.0.3 exits 1 here, naming the export it looked for.
	//
	// NEUTER, run: drop this refusal, and TestServeRefusesAComponentWithNoHandler's run-only component is served: its
	// address printed, and exit 0 at the stop where it wants 2.
	if !exportsHandler(c.Exports()) {
		why := ""
		for _, name := range c.Exports() {
			if strings.HasPrefix(name, "wasi:cli/run@") {
				why = " (it exports wasi:cli/run, which serve never calls; run it with burroughs run)"
			}
		}
		fmt.Fprintf(stderr, "%s%s exports no wasi:http/handler@0.3 handle, so there is nothing to serve%s\n", prefix,
			file, why)
		return errUsage
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: c.Handler(), ReadHeaderTimeout: serveReadHeaderTimeout}
	// wasmtime serve's line, with the address bound, which is how a caller learns a port of 0.
	fmt.Fprintf(stderr, "Serving HTTP on http://%s/\n", ln.Addr())

	// The stop's watcher, ADR 0137's one goroutine of this command: it waits for a stop or for Serve to end, and on a
	// stop shuts the server down, closing what is still open after the bound. It is joined before serve returns.
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-stop:
		case <-done:
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), serveShutdownBound)
		defer cancel()
		if srv.Shutdown(ctx) != nil {
			_ = srv.Close() // the bound is reached; what Close reports is the listener already shut
		}
	}()
	err = srv.Serve(ln)
	close(done)
	<-joined
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// exportsHandler reports whether a component's exports include wasi:http/handler@0.3.x's handle.
func exportsHandler(exports []string) bool {
	for _, name := range exports {
		if strings.HasPrefix(name, "wasi:http/handler@0.3") && strings.HasSuffix(name, "#handle") {
			return true
		}
	}
	return false
}
