// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// xcHandlesReading is the committed reading file for the cross-component handle fixtures (#1281).
const xcHandlesReading = "testdata/xc-handles.reading"

// runXCWasi runs a WASI 0.2 fixture's `wasi:cli/run` and renders what it wrote as the reading file spells it:
// `out=<stdout> err=<stderr>`, newlines as `|`.
func runXCWasi(t *testing.T, name string) (string, error) {
	t.Helper()
	wasm, err := os.ReadFile("testdata/" + name + ".wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	var out, errOut strings.Builder
	in, err := InstantiateWithHost(wasm, NewHost(&out, &errOut, nil))
	if err != nil {
		t.Fatalf("%s: instantiate: %v", name, err)
	}
	defer in.Close()
	cerr := in.Call("wasi:cli/run@0.2.3")
	bar := func(s string) string { return strings.ReplaceAll(s, "\n", "|") }
	return "out=" + bar(out.String()) + " err=" + bar(errOut.String()), cerr
}

// TestACrossComponentOwnMovesBetweenTables is #1281's witness. An own<output-stream> crossing a sync
// cross-component call moves out of the sender's handle table and into the receiver's, as def:lift_own and
// def:lower_own do, so the receiver writes through the handle it was sent rather than through whatever its own
// table holds at the sender's index. Each fixture arranges that collision: the receiver's table already holds a
// different live handle at that index.
//
// The readings are wasmtime 49.0.3's, 10 of 10 (`xc-handles.reading`). Burroughs at 2c4371d passed the index:
// xc-own-param read `out=1 1 1| err=child|`, the child writing to its own stderr handle and then dropping it,
// and xc-own-result wrote "parent" to the parent's own stderr handle and trapped at its last drop.
//
// NEUTER, run: pass `args` and `res.flat` through in bindCrossComponent's sync arm in place of moveParams and
// moveResult, and both fixtures read as at 2c4371d.
func TestACrossComponentOwnMovesBetweenTables(t *testing.T) {
	for _, name := range []string{"xc-own-param", "xc-own-result"} {
		want := readingLine(t, xcHandlesReading, name+", wasmtime 49.0.3, 10 of 10")
		for i := range 10 {
			got, err := runXCWasi(t, name)
			if err != nil {
				t.Fatalf("%s run %d: %v (printed %q)", name, i, err, got)
			}
			if got != want {
				t.Fatalf("%s run %d printed %q, want wasmtime's %q: the receiver used the handle its own "+
					"table holds at the sender's index (#1281)", name, i, got, want)
			}
		}
	}
}

// TestACrossComponentHandleThatDoesNotMoveIsRefusedByName is #1281's refusal witness. A borrow, a stream end and
// a future end crossing a cross-component call, and any handle on the async arm, are not moved between the two
// tables (cross_handles.go says why for each), so the call that would cross them is refused by name rather than
// handing the receiver the sender's index. On wasmtime 49.0.3 each runs (`xc-handles.reading`); at 2c4371d each
// ran with the receiver dropping its own handle or end, which is the reading a refusal replaces.
//
// NEUTER, run: have crossHandlesOf return an empty plan, and each call succeeds with 2c4371d's reading (the
// child's "child" on stderr; 1010199 for the three async guests).
func TestACrossComponentHandleThatDoesNotMoveIsRefusedByName(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	// Each case reports and the next still runs, so a neuter shows every fixture's reading at once.
	check := func(name, kind, ran string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s ran and read %s; want the cross-component call refused by name (#1281)", name, ran)
			return
		}
		if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "#1281") ||
			!strings.Contains(err.Error(), "carries "+kind) {
			t.Errorf("%s: %v; want ErrUnsupportedForm naming %s and #1281", name, err, kind)
		}
	}

	got, err := runXCWasi(t, "xc-borrow-param")
	check("xc-borrow-param", "borrow", got, err)
	if err != nil && strings.Contains(got, "child") {
		t.Errorf("xc-borrow-param printed %q before the refusal; the child must not run", got)
	}

	for _, c := range []struct{ name, kind string }{
		{"xc-stream-param", "stream"},
		{"xc-future-param", "future"},
		{"xc-stream-async-param", "stream"},
	} {
		wasm, rerr := os.ReadFile("testdata/" + c.name + ".wasm")
		if rerr != nil {
			t.Fatalf("the committed fixture is missing: %v", rerr)
		}
		in, ierr := Instantiate(wasm)
		if ierr != nil {
			t.Fatalf("%s: instantiate: %v", c.name, ierr)
		}
		res, cerr := in.CallValues("run")
		in.Close()
		check(c.name, c.kind, fmt.Sprint(res), cerr)
	}
}

// TestCrossHandlesOfMovesOnlyATopLevelHostOwn is the plan's predicate over hand-built signatures: a top-level own
// of a host resource is planned at its flat word, and every other handle, wherever it sits, is refused by name.
func TestCrossHandlesOfMovesOnlyATopLevelHostOwn(t *testing.T) {
	const hostRef, guestRef = 7, 8
	callee := func(sig *FuncType) *compFunc {
		return &compFunc{sig: sig, resAt: func(i uint32) *hostResource {
			if i == hostRef {
				return outputStreamResource
			}
			return nil
		}}
	}
	own := ValType{Kind: VOwn, Ref: hostRef}
	u32 := ValType{Kind: VU32}
	str := ValType{Kind: VString}

	plan, err := crossHandlesOf(callee(&FuncType{
		Params: []NamedVal{{Name: "a", Type: str}, {Name: "b", Type: own}, {Name: "c", Type: u32}, {Name: "d", Type: own}},
		Result: &own,
	}))
	if err != nil {
		t.Fatalf("a top-level host own was refused: %v", err)
	}
	if len(plan.params) != 2 || plan.params[0].word != 2 || plan.params[1].word != 4 || plan.result != outputStreamResource {
		t.Fatalf("plan = %+v; want owns at flat words 2 and 4 and an own result", plan)
	}
	if plan, err = crossHandlesOf(callee(&FuncType{Params: []NamedVal{{Name: "a", Type: u32}}, Result: &str})); err != nil ||
		plan.moves() {
		t.Fatalf("a signature with no handle planned %+v, %v; want nothing", plan, err)
	}

	for _, c := range []struct {
		name string
		vt   ValType
		kind string
	}{
		{"borrow", ValType{Kind: VBorrow, Ref: hostRef}, "borrow"},
		{"stream", ValType{Kind: VStream, Elem: &u32}, "stream"},
		{"future", ValType{Kind: VFuture}, "future"},
		{"error-context", ValType{Kind: VErrorContext}, "error-context"},
		{"guest own", ValType{Kind: VOwn, Ref: guestRef}, "own"},
		{"own in a record", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: own}}}, "own"},
		{"stream in an option", ValType{Kind: VOption, Elem: &ValType{Kind: VStream}}, "stream"},
	} {
		for _, asResult := range []bool{false, true} {
			sig := &FuncType{Params: []NamedVal{{Name: "p", Type: c.vt}}}
			if asResult {
				vt := c.vt
				sig = &FuncType{Result: &vt}
			}
			_, err := crossHandlesOf(callee(sig))
			if err == nil || !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "carries "+c.kind) ||
				!strings.Contains(err.Error(), "#1281") {
				t.Errorf("%s (result %v): %v; want a refusal naming %s and #1281", c.name, asResult, err, c.kind)
			}
		}
	}
}
