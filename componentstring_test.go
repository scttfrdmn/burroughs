// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// The embedder's-eye tests for the public string surface (#902, ADR 0097 item 2).
//
// # Why these are in `burroughs_test` and not `burroughs`
//
// The claim is not "the mechanism works" — the engine's own tests and the differential against
// `definitions.py` established that. It is that the surface is **reachable from outside the module**, and
// only a test that cannot touch an unexported identifier can make that claim. The compiler is the
// assertion.

// TestAnEmbedderCallsAnExportAndReadsAString is item 2's deliverable: a string **result** crossing the
// public boundary.
//
// It uses the clobbering fixture on purpose, so the eager lift is witnessed **from outside the module
// too**. The guest resolves with nine bytes and then overwrites them; `peek` reports what is there
// afterwards. So "the embedder got the result" and "the buffer was overwritten" are both observable here,
// and only their conjunction says the engine read the result before the guest reused it.
func TestAnEmbedderCallsAnExportAndReadsAString(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/task-return-string-clobber-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer func() {
		if cerr := c.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	}()

	res, err := c.Call(context.Background(), "run")
	if err != nil {
		t.Fatalf("Call(run): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("Call(run) returned %d value(s), want 1", len(res))
	}
	got, ok := res[0].Str()
	if !ok {
		t.Fatalf("the result is %v, not a string — the accessor refuses a mis-typed read rather than "+
			"returning the empty string, so this means the kind crossed wrong", res[0])
	}
	if got == "XXXXXXXXX" {
		t.Fatalf("Call(run) = %q — the guest's scratch, not its result. The embedder is being handed "+
			"memory the guest reused after resolving", got)
	}
	if got != "burroughs" {
		t.Errorf("Call(run) = %q, want %q", got, "burroughs")
	}

	// The anti-vacuity half, observable from out here as well: if the clobber never ran, the assertion
	// above would pass for the trivial reason that nothing overwrote the bytes.
	pk, err := c.Call(context.Background(), "peek")
	if err != nil {
		t.Fatalf("Call(peek): %v", err)
	}
	b0, ok := pk[0].U32()
	if !ok {
		t.Fatalf("peek returned %v, not a u32", pk[0])
	}
	if b0 != 'X' {
		t.Fatalf("peek says the byte is %#x, want %#x — the guest did not reuse its buffer, so this test "+
			"did not exercise the window it exists for", b0, byte('X'))
	}
}

// TestAStringArgumentIsRefusedByName pins item 2's stated limit: a string crosses **out** and not yet
// **in**, and the refusal says which and why.
//
// Passing one in needs the guest's own `cabi_realloc`, a host-initiated guest call the engine does not
// yet make. A refusal is the right outcome rather than a best effort: a string lowered without a realloc
// has nowhere to live, and writing it anywhere else in guest memory would corrupt whatever is there.
//
// # What this does and does not reach, stated because the neuter showed it
//
// It pins the **boundary** refusal: `Call` converts its arguments before `CallValuesCtx` checks arity, so
// the conversion is what fires here. Neutering the conversion to accept the string makes this test fail
// on the *arity* check instead — `run` declares no parameters — which is the right failure for the wrong
// reason and is why the assertions below check the message, not just that an error occurred.
//
// **No committed fixture declares a string parameter**, so the case where an export genuinely wants one
// cannot be driven end to end yet. That arrives with item 3, and it is the fixture that work owes.
func TestAStringArgumentIsRefusedByName(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/task-return-string-clobber-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer func() { _ = c.Close() }() // this test's subject is the argument refusal, not teardown

	arg, err := burroughs.ComponentString("hello")
	if err != nil {
		t.Fatalf("ComponentString: %v", err)
	}
	_, err = c.Call(context.Background(), "run", arg)
	if err == nil {
		t.Fatal("a string argument was accepted; lowering one needs a realloc the engine does not call")
	}
	if !errors.Is(err, burroughs.ErrUnsupported) {
		t.Fatalf("the refusal is not ErrUnsupported: %v", err)
	}
	// It must name the mechanism, not just decline. An embedder reading "unsupported" cannot tell whether
	// to wait for a release or restructure their call.
	for _, want := range []string{"string", "realloc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
	// And it must say the other direction works, since that is the non-obvious half.
	if !strings.Contains(err.Error(), "RESULT") {
		t.Errorf("the refusal %q does not say a string result works", err)
	}
}

// TestComponentStringRefusesInvalidUTF8 is ADR 0097's stamped behaviour: the validity check is at
// construction, not at the boundary.
func TestComponentStringRefusesInvalidUTF8(t *testing.T) {
	// A lone 0xff is not a legal UTF-8 byte in any position; 0x80 is a continuation with no lead byte.
	for _, bad := range []string{"\xff", "\x80", "ok-then\xff", "\xc3"} {
		if _, err := burroughs.ComponentString(bad); err == nil {
			t.Errorf("ComponentString(%q) succeeded; the Canonical ABI's string is UTF-8", bad)
		} else if !errors.Is(err, burroughs.ErrUnsupported) {
			t.Errorf("ComponentString(%q) refused with a non-ErrUnsupported error: %v", bad, err)
		}
	}
	// And the legitimate cases are not caught by an over-eager check — including the empty string, which
	// is valid and is the one most likely to be mistaken for "unset".
	for _, good := range []string{"", "hello", "é", "日本語", "\x00embedded"} {
		v, err := burroughs.ComponentString(good)
		if err != nil {
			t.Fatalf("ComponentString(%q) was refused: %v", good, err)
		}
		if got, ok := v.Str(); !ok || got != good {
			t.Errorf("Str() = %q (ok=%v), want %q", got, ok, good)
		}
	}
}

// TestStrAndStringAreDifferentThings is ADR 0097's third stamped change, as a test rather than only a
// doc comment.
//
// `String()` is the debug rendering and exists for every kind; `Str()` is the content and only for a
// string. Both return a `string`, so the compiler cannot catch the confusion — which is exactly why the
// distinction is asserted.
func TestStrAndStringAreDifferentThings(t *testing.T) {
	v, err := burroughs.ComponentString("hello")
	if err != nil {
		t.Fatalf("ComponentString: %v", err)
	}

	content, ok := v.Str()
	if !ok || content != "hello" {
		t.Fatalf("Str() = %q (ok=%v), want hello", content, ok)
	}
	debug := v.String()
	if debug == content {
		t.Fatalf("String() and Str() returned the same thing (%q). String() is the debug rendering and "+
			"must be distinguishable from the content, or the confusion the doc comment warns about is "+
			"undetectable", debug)
	}
	// The rendering names its kind, so an unexpected value is legible in a log.
	if !strings.Contains(debug, "string") {
		t.Errorf("String() = %q, which does not name the kind", debug)
	}

	// Str refuses a non-string rather than returning its empty string: `("", false)` and `("", true)`
	// are different answers, and only the boolean separates "not a string" from "the empty string".
	u := burroughs.ComponentU32(7)
	if s, ok := u.Str(); ok {
		t.Errorf("Str() on a u32 returned (%q, true); it must refuse", s)
	}
	empty, err := burroughs.ComponentString("")
	if err != nil {
		t.Fatalf("ComponentString(\"\"): %v", err)
	}
	if s, ok := empty.Str(); !ok || s != "" {
		t.Errorf("Str() on an empty string returned (%q, %v), want (\"\", true)", s, ok)
	}
}

// TestComponentTypeNamesItsKind covers the type descriptor, which exists so a compound value can say what
// it is. Only `u32` and `string` ship a constructor — the list and record descriptors arrive with the
// value constructors that need them, because a descriptor whose values cannot cross is a name an embedder
// can reach and not use.
func TestComponentTypeNamesItsKind(t *testing.T) {
	for _, c := range []struct {
		t    burroughs.ComponentType
		kind burroughs.ComponentKind
		name string
	}{
		{burroughs.ComponentTypeU32(), burroughs.KindComponentU32, "u32"},
		{burroughs.ComponentTypeString(), burroughs.KindComponentString, "string"},
	} {
		if c.t.Kind() != c.kind {
			t.Errorf("Kind() = %v, want %v", c.t.Kind(), c.kind)
		}
		if c.t.String() != c.name {
			t.Errorf("String() = %q, want %q", c.t.String(), c.name)
		}
	}

	// The zero value names no type, mirroring ComponentValue's zero. It must not claim to be `bool` —
	// which is what it would do if the kind were read off the codec's type, whose own zero Kind IS bool.
	var zero burroughs.ComponentType
	if zero.Kind() != burroughs.KindComponentNone {
		t.Errorf("the zero ComponentType reports %v, want KindComponentNone — a zero struct must not "+
			"claim a type nobody constructed", zero.Kind())
	}
	if zero.String() != "none" {
		t.Errorf("the zero ComponentType renders as %q, want \"none\"", zero.String())
	}
}
