// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestAnAsyncExportReturningNothingLoads is grave #885's witness, on an artefact that was **already in
// the tree and refused**.
//
// # Why this artefact and not a fixture written for the fix
//
// `cancel-rust-parent/composed.wasm` was committed by #862 to measure cancellation, and it exports
// `go: async func()` — **no result**. Its `task.return` therefore encodes the result list in the empty
// form (`0x01 0x00`), which `canon task.return` refused:
//
//	INSTANTIATE refused: nested component 1: canon 0:
//	  canon task.return resultlist discriminant 0x1 is not 0x00
//
// So the defect was shipped and reachable with a real guest, and this witness is that guest rather than
// something built to be fixed. **No guest in the tree had an async export returning nothing until #862
// built one**, which is the whole reason the defect survived this long.
//
// This asserts only that it **loads and instantiates**. Running it needs a cross-component async call
// (#888), which is separate work — so the claim stops exactly where the decoder's responsibility does.
func TestAnAsyncExportReturningNothingLoads(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/cancel-rust-parent/composed.wasm")
	if err != nil {
		t.Fatalf("the committed composed artefact is missing: %v", err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("load: %v\n\nIts nested parent exports `go: async func()`, so its task.return carries an "+
			"EMPTY result list. A refusal here is grave #885 returning.", err)
	}
	// The nested parent is what carries the empty-result task.return; a load that silently produced no
	// nested components would pass the line above while testing nothing.
	if len(c.NestedComponents) == 0 {
		t.Error("the composed artefact decoded with no nested components, so the task.return under test " +
			"was never reached")
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	in.Close()
}

// TestResultListDecodesBothEncodings pins the decoder itself on both arms and on what it refuses.
//
// # Why the one-result arm is here too
//
// The repair moved `funcType` onto a shared decoder. If that decoder had regressed the `0x00` arm, every
// component func type in the tree would break loudly — but *loudly elsewhere*, and a witness for #885
// that only covered the empty form would have been a witness for half the function it created. The pair
// is what makes it a decoder test rather than a bug-fix test.
//
// Driven through `funcType` rather than by calling `resultList` directly, so the arms exercise a real
// caller's path. `task.return`'s path is covered by the artefact witness above — the two callers, one
// decoder.
//
// # The two witnesses have DIFFERENT subjects, which the falsification showed
//
// Reverting `task.return` alone to its `0x00`-only decoder failed the artefact witness and **left this
// one passing** — because this drives `funcType`, which already shared the decoder. Neutering the shared
// decoder's empty arm failed **both**, and moved the artefact's failure from instantiate to *load*, since
// `funcType` reaches it first.
//
// So: this arm guards the decoder, and the artefact arm guards `task.return`'s *use* of it. Neither
// substitutes for the other, and a single witness would have left one call site unguarded — which is the
// same shape as the defect, one level up.
func TestResultListDecodesBothEncodings(t *testing.T) {
	// A `functype` body: param vec (empty) + result list.
	cases := []struct {
		name    string
		body    []byte
		wantRes bool
		wantErr string
	}{
		{
			name:    "one_result",
			body:    []byte{0x00, 0x00, 0x7f}, // 0 params; 0x00 then valtype 0x7f (u32... see note)
			wantRes: true,
		},
		{
			name:    "no_result",
			body:    []byte{0x00, 0x01, 0x00}, // 0 params; the EMPTY form — #885's subject
			wantRes: false,
		},
		{
			name:    "empty_form_not_followed_by_zero",
			body:    []byte{0x00, 0x01, 0x01},
			wantErr: "resultlist 0x01 not followed by 0x00",
		},
		{
			name:    "unknown_discriminant",
			body:    []byte{0x00, 0x02},
			wantErr: "resultlist discriminant 0x2 is not 0x00/0x01",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &reader{b: c.body}
			ft, err := r.funcType()
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("decoded %v without error, want %q", c.body, c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q does not contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := ft.Result != nil; got != c.wantRes {
				t.Errorf("Result != nil = %v, want %v — the two encodings must not collapse into one "+
					"answer, which is what a single decoder makes easy to get wrong in the other direction",
					got, c.wantRes)
			}
		})
	}
}
