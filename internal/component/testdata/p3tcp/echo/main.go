// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Command echo is p3tcp's peer for build.sh's oracle readings (#1235 E7a, ADR 0123). It listens on 127.0.0.1:0,
// prints "<echo-port> <closed-port>" (the second a port it bound and closed, so a connect to it is refused), and
// echoes each connection's bytes back until the peer shuts its write half, then closes it. It runs until killed.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "echo:", err)
		os.Exit(1)
	}
	gone, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "echo:", err)
		os.Exit(1)
	}
	closed := gone.Addr().(*net.TCPAddr).Port
	_ = gone.Close()
	fmt.Println(ln.Addr().(*net.TCPAddr).Port, closed)
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
}
