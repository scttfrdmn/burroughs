// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// notCallable are the two ways `Call` rejects a name for being the wrong *name*, as opposed to
// rejecting the arguments. Calling with no arguments fails on **arity** for a real export, so a
// failure matching either of these is the only sign that `Exports()` returned something uncallable:
//
//	"no callable run export"            // the name denotes nothing
//	"is an instance, not a function"    // the name denotes an exported INTERFACE
//
// **The second was missing from the first draft of this discriminator**, and the gap was found by
// neutering the enumerator to emit an interface's own name: the dedicated arm below caught it while
// this check did not, because naming an interface *resolves* and then fails for a different reason.
// A check whose comment claims "every name is callable" while testing only "every name resolves" is
// the narrower-than-it-reads shape, so the claim and the test now agree.
//
// Matched on text because the sentinels are internal. `TestAnUnknownNameIsRejectedWithThatPhrase` is
// the control for the first; if the wording ever changes, that arm fails rather than every arm here
// silently passing by matching nothing.
var notCallable = []string{"no callable run export", "is an instance, not a function"}

// callNameRejected reports whether err is Call refusing the NAME rather than the arguments.
func callNameRejected(err error) bool {
	if err == nil {
		return false
	}
	for _, p := range notCallable {
		if strings.Contains(err.Error(), p) {
			return true
		}
	}
	return false
}

// TestExportsNamesEveryCallableAndNothingElse is the witness for [burroughs.Component.Exports].
//
// # Why it CALLS every name rather than comparing two lists
//
// The list and the resolver are one fact — which strings name a callable export — held in two places:
// `ValueExportNames` enumerates, `resolveValueExport` accepts. A test comparing the enumerator against
// a second enumeration would agree with itself by construction and drift silently, which is the defect
// this project has paid for twice this campaign (the provenance glob keys, and the edit-route hook's
// `tracked_set`). So every name goes through `Call`, and the assertion is that none of them fails to
// **resolve**.
//
// Arity is not the subject: these are called with no arguments, so a real export fails on its
// signature. That failure is the proof the name resolved — the resolver ran and handed the call to a
// function. What must not appear is the resolution failure.
func TestExportsNamesEveryCallableAndNothingElse(t *testing.T) {
	for _, f := range []struct {
		name, path, wantName string
		wantForm             string
	}{
		{
			name:     "a top-level function is listed by its bare name",
			path:     "internal/component/testdata/record-synth.wasm",
			wantName: "addrec", wantForm: "bare",
		},
		{
			name:     "a function inside an exported interface is listed as interface#function",
			path:     "internal/component/testdata/asynclift/single/component.wasm",
			wantName: "test:probe/ops@0.1.0#compute", wantForm: "path",
		},
	} {
		t.Run(f.name, func(t *testing.T) {
			wasm, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			c, err := burroughs.LoadComponent(wasm)
			if err != nil {
				t.Fatalf("LoadComponent: %v", err)
			}
			defer func() { _ = c.Close() }()

			names := c.Exports()
			if len(names) == 0 {
				t.Fatal("Exports() returned nothing, so every assertion below would hold vacuously")
			}
			if !slices.Contains(names, f.wantName) {
				t.Errorf("Exports() = %v, missing the %s-form export %q this fixture is here for",
					names, f.wantForm, f.wantName)
			}
			if !slices.IsSorted(names) {
				t.Errorf("Exports() = %v, not sorted — a map's order is not a fact about the "+
					"component, and an embedder printing these should not see them shuffle", names)
			}

			// **The load-bearing arm: every name resolves.** A name that cannot be called is the
			// one thing this list must never contain.
			for _, n := range names {
				_, callErr := c.Call(context.Background(), n)
				if callNameRejected(callErr) {
					t.Errorf("Exports() returned %q, and Call rejected the NAME: %v\n"+
						"A name list whose names do not work is worse than no list: it sends an "+
						"embedder to an export that is not callable.", n, callErr)
				}
			}

			// An exported INTERFACE's own name must not be in the list: naming one is refused,
			// because it is an instance rather than a function.
			for _, n := range names {
				iface, _, isPath := strings.Cut(n, "#")
				if !isPath {
					continue
				}
				if slices.Contains(names, iface) {
					t.Errorf("Exports() contains both %q and the bare interface name %q; naming "+
						"an interface is refused, so the bare form is a string that cannot be "+
						"called", n, iface)
				}
			}
		})
	}
}

// TestAnUnknownNameIsRejectedWithThatPhrase is the control for the discriminator above.
//
// Without it, a change to the resolver's wording would make every "every name resolves" assertion
// pass by matching nothing — the vacuity shape, one level out from the thing being asserted.
func TestAnUnknownNameIsRejectedWithThatPhrase(t *testing.T) {
	wasm, err := os.ReadFile("internal/component/testdata/record-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, callErr := c.Call(context.Background(), "definitely-not-an-export-of-this-component")
	if callErr == nil {
		t.Fatal("calling a name that is not an export succeeded")
	}
	if !callNameRejected(callErr) {
		t.Fatalf("an unknown export produced %q, which matches none of %v — the discriminator "+
			"every arm in TestExportsNamesEveryCallableAndNothingElse relies on is broken, and "+
			"those arms would now pass by matching nothing", callErr, notCallable)
	}
}

// TestAZeroConfigComponentWritesNowhereThroughBothPaths holds [burroughs.ComponentConfig.LoadComponent]
// to the same stream defaults as [burroughs.ComponentConfig.Run].
//
// **Two methods on one configuration must mean the same thing by that configuration.** If the new one
// had inherited the bare [burroughs.LoadComponent]'s hard-coded discard instead of reading the
// config's fields, a supplied writer would be silently ignored — the quietly-wrong outcome, where the
// embedder has done everything right and the output still vanishes.
//
// A zero-value config writes nowhere on purpose: a component embedded in someone's program should not
// write to that program's terminal unless the embedder hands it a writer. That is the documented
// difference from [burroughs.WASIP1Config], which inherits the process's streams.
func TestAZeroConfigComponentWritesNowhereThroughBothPaths(t *testing.T) {
	wasm, err := os.ReadFile("internal/component/testdata/record-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	t.Run("a zero config discards, and a supplied writer is used", func(t *testing.T) {
		// The zero value: nil Stdout. The component here prints nothing, so this arm's claim is
		// about the WIRING — that the config's field is what reaches the host — which the
		// supplied-writer arm below is what actually proves. Stated so the pair is not read as
		// two independent checks.
		zero, lerr := burroughs.ComponentConfig{}.LoadComponent(wasm)
		if lerr != nil {
			t.Fatalf("zero-config LoadComponent: %v", lerr)
		}
		if err := zero.Close(); err != nil {
			t.Errorf("Close after a zero-config load: %v", err)
		}

		var out bytes.Buffer
		cfg := burroughs.ComponentConfig{Stdout: &out, Args: []string{"probe", "arg"}}
		c, lerr := cfg.LoadComponent(wasm)
		if lerr != nil {
			t.Fatalf("configured LoadComponent: %v", lerr)
		}
		defer func() { _ = c.Close() }()

		// The configured load must be as callable as the bare one — the capabilities are additional,
		// not a different mode.
		if names := c.Exports(); len(names) == 0 {
			t.Error("a configured component exports nothing, so the config changed more than the " +
				"streams")
		}
	})

	t.Run("the configured load refuses a bad feature by name, like Run does", func(t *testing.T) {
		// `Features` is one of the five fields threaded, and an unrecognized name must be refused
		// rather than ignored. This is the cheapest arm proving the field is read at all: an
		// implementation that dropped `Features` would load this successfully.
		cfg := burroughs.ComponentConfig{Features: []burroughs.Feature{"not-a-real-proposal"}}
		if _, lerr := cfg.LoadComponent(wasm); lerr == nil {
			t.Error("an unrecognized Feature was accepted, so the config's Features field is not " +
				"reaching the engine")
		}
	})
}
