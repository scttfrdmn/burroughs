// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestEveryEntryPointClassifiesARefusalTheSameWay is the boundary's own witness: one refusal must
// cross as one sentinel whichever door it came through.
//
// # The defect
//
// Three entry points load a component — [burroughs.LoadComponent],
// [burroughs.ComponentConfig.LoadComponent] and [burroughs.ComponentConfig.Run] — and each wrapped a
// different subset of the engine's refusals. Measured before the fix, with `gate:async` on and the
// committed `p3async-cancel` fixture:
//
//	bare LoadComponent            RAW — no public sentinel
//	ComponentConfig.LoadComponent ErrUnsupported
//	ComponentConfig.Run           ErrUnsupported
//
// So `errors.Is(err, ErrUnsupported)` was false through one door and true through the other two, and
// a CLI reading it fell to the catch-all exit code instead of the named one. **That is a bug rather
// than a missing convenience** — a sentinel exists so a caller can branch on it, and a boundary whose
// answer depends on which function you called is one a caller cannot branch on at all.
//
// # Why nine cells and not three
//
// Three methods that agree today agree by coincidence unless the agreement is asserted per refusal.
// The grid is every refusal against every entry point, so adding a fourth entry point or a fourth
// refusal shows up as a missing row rather than as silence.
//
// # Each cell asserts the refusal BEHIND the sentinel
//
// The chair's condition, and it is the difference between this table and a weaker one: `Run` accepts
// only components exporting `wasi:cli/run`, so a fixture built for one refusal can fail *earlier* at
// another entry point for an unrelated reason — a missing run export, a decode error — and produce
// the right sentinel for the wrong cause, or a different sentinel entirely. So every cell checks the
// message too, and a cell no fixture can reach is **declared unreachable with its reason** rather
// than passing on whatever error emerges.
func TestEveryEntryPointClassifiesARefusalTheSameWay(t *testing.T) {
	// The three doors. Each returns only an error, because that is the whole subject.
	type door struct {
		name string
		load func(wasm []byte) error
	}
	doors := []door{
		{"LoadComponent", func(w []byte) error {
			_, err := burroughs.LoadComponent(w)
			return err
		}},
		{"ComponentConfig.LoadComponent", func(w []byte) error {
			_, err := burroughs.ComponentConfig{}.LoadComponent(w)
			return err
		}},
		{"ComponentConfig.Run", func(w []byte) error {
			_, err := burroughs.ComponentConfig{}.Run(w)
			return err
		}},
	}

	for _, r := range []struct {
		refusal string
		fixture string
		async   string
		// want is the public sentinel every reachable door must produce.
		want error
		// because is a phrase from the refusal's own message. It is what stops a cell passing on
		// the right sentinel for the wrong reason.
		because string
		// unreachable, when non-empty, says this refusal cannot be produced through any public
		// entry point and why. The arm then asserts that rather than asserting a classification.
		//
		// **No row sets it today**, and the mechanism is kept rather than deleted: the one row that
		// did (ErrUnsupportedForm) went live when #957 gave it a fixture, which is what the field is
		// for — a refusal with no fixture is written down and starts asserting when one arrives,
		// instead of being left out and forgotten.
		unreachable string
	}{
		{
			refusal: "gate:async off, a well-formed async component",
			fixture: "internal/component/testdata/p3async-hello.wasm",
			async:   "0",
			want:    burroughs.ErrGated,
			because: "gate:async is off in this build",
		},
		{
			refusal: "gate:async on, an async surface whose execution is not built",
			fixture: "internal/component/testdata/p3async-cancel.wasm",
			async:   "1",
			want:    burroughs.ErrUnsupported,
			because: "execution is not yet implemented",
		},
		// **This row was declared unreachable and is now live**, which is the outcome it was written
		// for: *"the row is written and will start asserting the moment a fixture reaches it."*
		// #957's by-name decode refusal reaches it. What it does NOT make reachable is the
		// *binding-stage* producer the old note described — `ErrUnsupportedForm` raised while walking,
		// which is still only exercised by `walkComponent` called directly with a stub host
		// (internal/component's TestBindingRefusesImplementedFilesystemErrorCodeOnRealGuest), and the
		// public path still loads `p3hello` without error. So the SENTINEL is reached, at decode, by
		// another route. That distinction is kept rather than smoothed over: a row that claimed to
		// cover the binding path would be asserting something no arm here measures.
		//
		// **The fixture moved from `thread-index-synth` to `cmthreads/switch`, because 0x26 is modeled
		// now.** ADR 0100's first build slice lowers `thread.index`, so that fixture is *gated* rather
		// than unsupported and this row failed on it through all three doors for exactly one run. The
		// row's subject is an UNMODELED production, and `switch.wasm`'s first 🧵 canon is
		// `thread.resume-later` (0x28). **As forms land this row has to move again**, and it runs out
		// of subjects when the last one is modeled — at which point the row goes back to being
		// declared unreachable, with that as its reason.
		{
			refusal: "a canonical built-in this engine does not model",
			fixture: "internal/component/testdata/cmthreads/switch.wasm",
			async:   "1",
			want:    burroughs.ErrUnsupported,
			because: "shared-everything-threads built-in",
		},
	} {
		t.Run(r.refusal, func(t *testing.T) {
			wasm, err := os.ReadFile(r.fixture)
			if err != nil {
				t.Fatalf("the committed fixture is missing, so this row asserts nothing: %v", err)
			}

			if r.unreachable != "" {
				// The arm still runs, and still asserts something falsifiable: that the fixture
				// really does NOT produce this refusal. If one ever does, this fails and the row
				// becomes a live three-cell check — which is the point of keeping it.
				t.Setenv("BURROUGHS_ASYNC", r.async)
				for _, d := range doors {
					gotErr := d.load(wasm)
					if gotErr != nil && strings.Contains(gotErr.Error(), r.because) {
						t.Errorf("%s now produces %q, so this refusal is REACHABLE and the row "+
							"must start asserting it: %v\nrecorded reason for unreachability: %s",
							d.name, r.because, gotErr, r.unreachable)
					}
				}
				t.Logf("row declared unreachable: %s", r.unreachable)
				return
			}

			for _, d := range doors {
				t.Run(d.name, func(t *testing.T) {
					t.Setenv("BURROUGHS_ASYNC", r.async)
					gotErr := d.load(wasm)
					if gotErr == nil {
						t.Fatalf("%s accepted the fixture, so this cell measures nothing", d.name)
					}
					// The sentinel: what a caller branches on.
					if !errors.Is(gotErr, r.want) {
						t.Errorf("%s: errors.Is(err, %v) is false, so a caller cannot branch on "+
							"this refusal through this door.\ngot: %v", d.name, r.want, gotErr)
					}
					// The refusal behind it: what stops the cell passing for the wrong reason.
					if !strings.Contains(gotErr.Error(), r.because) {
						t.Errorf("%s: the refusal does not mention %q, so this cell may be the "+
							"right sentinel from the wrong cause — Run in particular rejects a "+
							"component with no wasi:cli/run export, which is not this refusal.\n"+
							"got: %v", d.name, r.because, gotErr)
					}
				})
			}
		})
	}
}
