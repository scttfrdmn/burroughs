// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestANestedLowerIsCheckedAsATopLevelOneIs is #1257's witness: a nested component's canon lower is resolved
// through the child's own import, so the signature checks a top-level lower gets run for it too. The two fixtures
// lower the parent's `wasi:cli/stdout@0.3.0` write-via-stream from a child that imports it as `stdout`, and differ in
// `error-code`'s labels. Each row of nested-write-via-stream.reading is wasmtime 49.0.3's, 10 of 10: the control's
// `seven()` is 7, and the other is refused at link for its enum. Burroughs refuses it by name at instantiate, as
// TestWriteViaStreamPayloadMustAgreeWithTheHosts refuses the top-level one, with checkConsumerPayload's message.
//
// Neutered, run: the walk's lower resolving its signature by `w.lowerSignature(cf.stubName)` (main's code before
// #1257), and the other-error-code row instantiates and `seven()` returns 7 where the oracle refuses, while the
// control still passes.
func TestANestedLowerIsCheckedAsATopLevelOneIs(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	raw, err := os.ReadFile("testdata/nested-write-via-stream.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		fx, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("nested-write-via-stream.reading: row %q has no `: `", ln)
		}
		rows++
		t.Run(fx, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + fx + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
			if strings.HasPrefix(reading, "refused: ") {
				if err == nil {
					got, cerr := callU32(in, "seven")
					in.Close()
					t.Fatalf("instantiated, and seven() = %d, %v; the oracle refuses: %s", got, cerr, reading)
				}
				if !errors.Is(err, ErrLinkRefused) {
					t.Fatalf("refused with %v, want ErrLinkRefused", err)
				}
				for _, want := range []string{
					"wasi:cli/stdout@0.3.0::write-via-stream", "enum{io, pipe}",
					"enum{io, illegal-byte-sequence, pipe}",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal %q does not name %s", err, want)
					}
				}
				return
			}
			want, perr := strconv.ParseUint(reading, 10, 32)
			if perr != nil {
				t.Fatalf("reading %q is neither a refusal nor a u32", reading)
			}
			if err != nil {
				t.Fatalf("instantiate: %v; the oracle reads %d", err, want)
			}
			t.Cleanup(in.Close)
			if got, err := callU32(in, "seven"); err != nil || uint64(got) != want {
				t.Fatalf("seven() = %d, %v; the oracle reads %d", got, err, want)
			}
		})
	}
	if rows != 2 {
		t.Fatalf("nested-write-via-stream.reading has %d rows, want 2", rows)
	}
}

// TestHostResourceNestedLowerHasItsSignature is #1257 on host-resource-nested, the specimen the issue names: the
// child's lower of `make` resolves a signature through the child's import `things`, whose resource is the parent's
// host type, so the ADR 0118 checks ran over it and passed. Before #1257 the signature was nil and they did not run.
// TestCloseReachesTheOwnsANestedInstanceHolds is the specimen's behaviour, and still passes.
//
// Neutered, run: lowerSignatureOf's import trace answering nil, and the fallback by name finds no import of the
// child's named `test:res/things@0.1.0`, so the lower's signature is nil. With the trace kept and its rename
// dropped, the child is refused at instantiate for `own<things::thing>`.
func TestHostResourceNestedLowerHasItsSignature(t *testing.T) {
	th := newThingHost()
	b, err := os.ReadFile("testdata/host-resource-nested.wasm")
	if err != nil {
		t.Fatal(err)
	}
	in, err := InstantiateWithHost(b, th.Host)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	childLowerReturnsOwnOf(t, in, "test:res/things@0.1.0::make", "test:res/things@0.1.0::thing")
}

// TestANestedLowerOfAUsedResourceRuns is #1257's rename: a nested lower's resource identities are the instances
// the child's imports are bound to, not the child's import names. nested-used-resource's child lowers the parent's
// get-stdout through imports it names `streams` and `stdout`, with output-stream `use`d from the first, and
// wasmtime 49.0.3 runs its `seven()` to 7, 10 of 10 (nested-used-resource.reading). Burroughs instantiates it, its
// `seven()` is 7, and the lower's result is an own of the host's `wasi:io/streams@0.2.3::output-stream`.
//
// Neutered, run: importedFuncSignature returning the signature unrenamed, and the child is refused at instantiate,
// its get-stdout returning `own<streams::output-stream>`, "a resource the host does not implement".
func TestANestedLowerOfAUsedResourceRuns(t *testing.T) {
	in := runsSevenAsTheOracleDoes(t, "nested-used-resource")
	childLowerReturnsOwnOf(t, in, "wasi:cli/stdout@0.2.3::get-stdout", "wasi:io/streams@0.2.3::output-stream")
}

// TestANestedTypeImportOfAHostResourceRuns is #1257's rename for a type import: nested-type-import's child takes
// output-stream as `(import "os" (type (sub resource)))`, which the parent binds with `(with "os" (type
// $output-stream))`. wasmtime 49.0.3 runs its `seven()` to 7, 10 of 10 (nested-type-import.reading). Burroughs
// instantiates it, its `seven()` is 7, and the lower's result is an own of the host's
// `wasi:io/streams@0.2.3::output-stream`, so the child's `os` was read as the parent's type.
//
// Neutered, run: compArgRef without its type arm, and the child is refused at instantiate, its get-stdout returning
// `own<os>`, "a resource the host does not implement". That is also what 4920e7f, which had no type arm, does.
func TestANestedTypeImportOfAHostResourceRuns(t *testing.T) {
	in := runsSevenAsTheOracleDoes(t, "nested-type-import")
	childLowerReturnsOwnOf(t, in, "wasi:cli/stdout@0.2.3::get-stdout", "wasi:io/streams@0.2.3::output-stream")
}

// runsSevenAsTheOracleDoes instantiates testdata/<fixture>.wasm on a WASI 0.2 host and checks that its `seven()`
// returns what <fixture>.reading's one row says wasmtime printed.
func runsSevenAsTheOracleDoes(t *testing.T, fixture string) *Instantiated {
	t.Helper()
	b, err := os.ReadFile("testdata/" + fixture + ".wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	raw, err := os.ReadFile("testdata/" + fixture + ".reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	var want uint64
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		reading, ok := strings.CutPrefix(ln, "seven: ")
		if !ok {
			t.Fatalf("%s.reading: row %q is not `seven: <u32>`", fixture, ln)
		}
		if want, err = strconv.ParseUint(reading, 10, 32); err != nil {
			t.Fatalf("%s.reading: reading %q is not a u32", fixture, reading)
		}
		rows++
	}
	if rows != 1 {
		t.Fatalf("%s.reading has %d rows, want 1", fixture, rows)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v; the oracle runs seven() to %d", err, want)
	}
	t.Cleanup(in.Close)
	if got, err := callU32(in, "seven"); err != nil || uint64(got) != want {
		t.Fatalf("seven() = %d, %v; the oracle reads %d", got, err, want)
	}
	return in
}

// childLowerReturnsOwnOf checks that the one nested instance's one lower is of stub, and that the signature it was
// checked against returns an own of resource.
func childLowerReturnsOwnOf(t *testing.T, in *Instantiated, stub, resource string) {
	t.Helper()
	if len(in.w.children) != 1 {
		t.Fatalf("%d nested instance(s), want 1", len(in.w.children))
	}
	var lowered []coreDef
	for _, d := range in.w.children[0].coreSpace[SpaceCoreFunc] {
		if d.lowerName != "" {
			lowered = append(lowered, d)
		}
	}
	if len(lowered) != 1 || lowered[0].lowerName != stub {
		t.Fatalf("the child's lowers are %v, want the one of %s", lowered, stub)
	}
	sig := lowered[0].lowerSig
	if sig == nil || sig.Result == nil || sig.Result.Kind != VOwn || sig.Result.Resource != resource {
		t.Fatalf("the nested lower's signature is %+v; want a result of own<%s>", sig, resource)
	}
}
