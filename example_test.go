// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/scttfrdmn/burroughs"
)

// ExampleComponent_record is the component value surface as an embedder meets it: build a record,
// pass it to an export, read a `u32` back. It is `v0.7.0`'s release-note snippet, held equal to it by
// `TestCHANGELOGGoBlocksAreRealCode` — so the notes quote code `go test` runs, rather than prose that
// can drift away from the API it documents.
//
// **Every constructor error is handled, and that is the point rather than ceremony.** This interface's
// whole claim is that a mis-typed value is refused *at construction*: `ComponentTypeRecord` refuses a
// duplicate or non-kebab-case label and an empty field set, `ComponentRecord` refuses a missing or
// undeclared key and a field whose type does not match structurally. An example that discarded those
// with `_` would teach a reader to ignore the exact refusals the release is built around. The one call
// whose error is *expected* to be nil in a working build is still checked, because an example is also
// a test.
//
// The fixture is `internal/component/testdata/record-synth.wasm`, committed with the `.wat` it was
// assembled from. Its `addrec` export takes `record { a: u32, b: u32 }` and returns the sum, so the
// printed `42` is the guest's arithmetic and not this file's.
func ExampleComponent_record() {
	wasm, err := os.ReadFile("internal/component/testdata/record-synth.wasm")
	if err != nil {
		log.Fatal(err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		// Close ends the component's threads and host state. Checked, not deferred-and-forgotten:
		// a failure here means the instance did not shut down cleanly, which a caller wants to know.
		if cerr := c.Close(); cerr != nil {
			log.Fatal(cerr)
		}
	}()

	// The type carries the field names and their order. Order is the byte layout, which is why it
	// lives in the descriptor and not in the value.
	pair, err := burroughs.ComponentTypeRecord(
		burroughs.ComponentField{Name: "a", Type: burroughs.ComponentTypeU32()},
		burroughs.ComponentField{Name: "b", Type: burroughs.ComponentTypeU32()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// The value is a map, so a misordered field is unrepresentable rather than merely caught. The keys
	// must match the descriptor exactly: a missing field cannot be defaulted, because no WIT value
	// means "absent".
	rec, err := burroughs.ComponentRecord(pair, map[string]burroughs.ComponentValue{
		"a": burroughs.ComponentU32(10),
		"b": burroughs.ComponentU32(32),
	})
	if err != nil {
		log.Fatal(err)
	}

	res, err := c.Call(context.Background(), "addrec", rec)
	if err != nil {
		log.Fatal(err)
	}

	// The accessor's boolean is the refusal: a value of another kind reads as (0, false) rather than
	// reinterpreting its bits.
	sum, ok := res[0].U32()
	if !ok {
		log.Fatalf("the result is %v, not a u32", res[0])
	}
	fmt.Println(sum)
	// Output:
	// 42
}

// Example drives the engine from Go the way a host embedding it would: read a module, instantiate
// it, ask what it exports, call one, and tell a trap apart from an error of your own.
//
// **The external test package is deliberate.** `burroughs_test` can reach only the exported surface,
// so an example that compiles here is an example a caller can copy — one written in the internal
// test package would compile against fields no consumer has. It is also the README's "Use from Go"
// section: `TestREADMEGoBlocksAreRealCode` holds this body and that fenced block equal, so the
// documented snippet is this function or the build goes red.
//
// The fixture is `examples/add/add.wasm`, committed and derived from the `.wat` beside it — see
// TestExampleWasmIsDerivedFromItsWat, which is what keeps the binary honest about its own source.
func Example() {
	wasm, err := os.ReadFile("examples/add/add.wasm")
	if err != nil {
		log.Fatal(err)
	}

	// Instantiate decodes, validates, and instantiates. The validator runs before
	// the interpreter does, which is what this path exists to guarantee.
	in, err := burroughs.Instantiate(wasm)
	if err != nil {
		log.Fatal(err)
	}

	// The third outcome: nil means every construct in the module had a typing rule.
	// A non-nil decline names what went unchecked, and the module still ran.
	if d := in.Decline(); d != nil {
		fmt.Println("declined:", d)
	}

	fmt.Println("exports:", in.Exports())

	sum, err := in.Call("add", burroughs.I32(2), burroughs.I32(40))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("add(2, 40) =", sum[0].Int32())

	// A trap is the module executing correctly while the program goes wrong, so it
	// is a type a caller can match rather than a string to read.
	var trap *burroughs.Trap
	if _, err := in.Call("div", burroughs.I32(7), burroughs.I32(0)); errors.As(err, &trap) {
		fmt.Println("div(7, 0) trapped:", trap.Reason)
	}

	// Output:
	// exports: [add fib div]
	// add(2, 40) = 42
	// div(7, 0) trapped: integer divide by zero
}

// ExampleConfig shows the other half of the decline: `Strict` refuses a module the validator could
// not fully check, instead of running it.
//
// No fixture of its own, and that is the point of using the same one. A vector whose decline is
// *live* would make this example's output a function of #9's frontier — green today, wrong the week
// the slice lands — so the assertion here is the direction that stays true as the validator fills
// in: a fully-validated module is unaffected by Strict. The refusal itself is asserted where it can
// be asserted without dating, against a construct chosen for it, in `cmd/burroughs`' own tests.
func ExampleConfig() {
	wasm, err := os.ReadFile("examples/add/add.wasm")
	if err != nil {
		log.Fatal(err)
	}

	in, err := burroughs.Config{Strict: true}.Instantiate(wasm)
	switch {
	case errors.Is(err, burroughs.ErrDeclined):
		fmt.Println("refused: the validator has no rule for something in this module")
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println("fully validated, so Strict changed nothing:", in.Exports())
	}

	// Output:
	// fully validated, so Strict changed nothing: [add fib div]
}
