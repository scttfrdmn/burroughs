// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
)

// BenchmarkListRoundTrip is the list lift's cost reading (the chair's third ruling of 2026-10-18, item 2): a
// short list round trip through ADR 0103's sync adapter, read with `make lab-ab` against ADR 0059's bar. Short,
// because the bounded forms are what a short list must not pay for. A 64-element `list<u8>` is one chunk, and a
// 16-element `list<string>` is one segment. Each iteration's length is checked, so a skipped copy fails rather
// than timing as a faster one.
func BenchmarkListRoundTrip(b *testing.B) {
	for _, arm := range []struct {
		name, export string
		n            int
	}{{"list-u8", "run-list", 64}, {"list-string", "run-strs", 16}} {
		b.Run("class="+arm.name, func(b *testing.B) {
			img, err := os.ReadFile("testdata/adapter-copy-list.wasm")
			if err != nil {
				b.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := Instantiate(img)
			if err != nil {
				b.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			arg := adapterListArg(b, arm.export, arm.n)
			for b.Loop() {
				res, err := in.CallValues(arm.export, arg)
				if err != nil {
					b.Fatalf("%s: %v", arm.export, err)
				}
				if v, ok := res[0].U32(); !ok || v != uint32(arm.n) {
					b.Fatalf("%s = %v, want u32 %d: a copy was skipped", arm.export, res, arm.n)
				}
			}
		})
	}
}
