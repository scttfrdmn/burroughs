// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// TestP3EchoMatchesWasmtime is #715's exit: the second guest — which reads its arguments
// (host-lowered list<string>), reads stdin through input-stream.blocking-read (the blocking substrate),
// and echoes both — runs on Burroughs byte-identical to the committed wasmtime 48.0.1 reading. It is the
// differential validating the new lowerings: a wrong list<string> or blocking-read layout would make
// the guest echo different bytes than wasmtime. It also exercises version-independent host keying
// (p3echo is wasi @0.2.6, p3hello was @0.2.3) and the canon resource.drop no-op.
func TestP3EchoMatchesWasmtime(t *testing.T) {
	wasm, err := os.ReadFile("testdata/p3echo.wasm")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/p3echo.stdout")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	h := NewHost(&out, io.Discard, strings.NewReader("hello\n"))
	h.Args = []string{"p3echo.wasm"}
	in, err := InstantiateWithHost(wasm, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	if err := in.CallRun(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("stdout = %q, want %q (wasmtime 48.0.1's reading)", got, want)
	}
}

// errWriter fails every write, standing in for a closed pipe.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("broken pipe") }

// TestStreamErrArmLowersAnOwnErrorHandle is #694's deferred err arm, driven live: with a stdout that
// fails, output-stream.blocking-write-and-flush lowers result<_, stream-error> = Err(last-operation-
// failed(own<error>)) — the first non-nested variant with a handle payload — minting an error resource
// the host holds. The guest, told its write failed, aborts (its own response); the assertion is that the
// err arm was taken (an own<error> minted), not the ok arm.
func TestStreamErrArmLowersAnOwnErrorHandle(t *testing.T) {
	wasm, err := os.ReadFile("testdata/p3echo.wasm")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(errWriter{}, errWriter{}, strings.NewReader("hi\n"))
	h.Args = []string{"p3echo.wasm"}
	in, err := InstantiateWithHost(wasm, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	// The guest aborts once told its write failed — a trap, which is the guest seeing the err arm, not
	// the host failing. The err arm having run is what we assert.
	_ = in.CallRun()
	if len(h.errors) == 0 {
		t.Error("no own<error> was minted — the failing write did not lower the stream-error err arm")
	}
}
