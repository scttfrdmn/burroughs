// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Command peer is p3http's HTTP peer for build.sh's oracle readings (#1383, ADR 0135). It serves on 127.0.0.1:0,
// prints "<port> <closed-port>" (the second a port it bound and closed, so a send to it is refused), and serves until
// killed.
//
// **Its routes are httpPeerMux's in p3http_test.go, which serves Burroughs' rows**, and the two must answer alike: a
// reading is the guest's view of this peer, so a route that differs makes a row differ for a reason that is not the
// host's. A port never reaches a response: /dump writes the Host it read with the port replaced by PORT.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer:", err)
		os.Exit(1)
	}
	gone, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer:", err)
		os.Exit(1)
	}
	closed := gone.Addr().(*net.TCPAddr).Port
	_ = gone.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	fmt.Println(port, closed)
	_ = http.Serve(ln, mux(strconv.Itoa(port)))
}

func mux(port string) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Peer", "hello")
		fmt.Fprintf(w, "hello, %s %s", r.Method, r.URL.Path)
	})
	m.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Trailer", "X-Sum")
		fmt.Fprintf(w, "method=%s len=%d te=%v cl=%d trailer=%q hdr=%q", r.Method, len(b), r.TransferEncoding,
			r.ContentLength, r.Trailer.Get("X-Req-Trailer"), r.Header.Get("X-Req"))
		w.Header().Set("X-Sum", strconv.Itoa(len(b)))
	})
	m.HandleFunc("/dump", func(w http.ResponseWriter, r *http.Request) {
		var hs []string
		for k, v := range r.Header {
			hs = append(hs, k+"="+strings.Join(v, ","))
		}
		sort.Strings(hs)
		fmt.Fprintf(w, "method=%s uri=%s host=%s proto=%s hdrs=%v", r.Method, r.RequestURI,
			strings.Replace(r.Host, port, "PORT", 1), r.Proto, hs)
	})
	m.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		b := make([]byte, 1<<20)
		for i := range b {
			b[i] = byte('a' + i%26)
		}
		_, _ = w.Write(b)
	})
	m.HandleFunc("/stall", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answers: the client's first-byte timeout ends the request
	})
	return m
}
