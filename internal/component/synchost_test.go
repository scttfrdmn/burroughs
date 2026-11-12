// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestSyncImplsAreServedAlongsideWASI is the extra-sync-impl path's control (#863).
//
// `Host.wasi()` builds its capability set as a literal, so before this there was no way for a host to
// answer a guest's plain (non-async) import. `asyncImpls` already had a field for the async half;
// [#862](https://github.com/scttfrdmn/burroughs/issues/862)'s receipt guest needs the sync one, because it
// calls `note` from `Drop` glue and a `Drop` cannot await.
//
// Two facts, asserted separately: the extra impl is reachable, and WASI's own set is still intact beside
// it. One assertion over the merged map would pass if the merge had replaced WASI wholesale.
func TestSyncImplsAreServedAlongsideWASI(t *testing.T) {
	h := NewHost(io.Discard, io.Discard, nil)
	h.syncImpls = map[string]interp.CanonFunc{
		"test:receipt/ops::note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
			return nil, nil
		},
	}
	m := h.wasi()

	if _, ok := m["test:receipt/ops::note"]; !ok {
		t.Error("the extra sync impl is absent from wasi()'s map, so a guest importing it would be refused")
	}
	// WASI must still be there. A merge that replaced the map rather than adding to it would pass the
	// assertion above and break every real guest, so the two are checked separately.
	//
	// Asked through lookupHost at a guest's versioned identity, not by indexing the map: since ADR 0115 a
	// WASI key carries its compatibility class (`wasi:cli/exit@0.2::exit`), so the bare name is no key.
	//
	// `get-stdout` and `output-stream.write` were the other two rows here. They moved to the resource-method
	// source in ADR 0117, so the WASI set this map must still hold is the plain imports'.
	for _, want := range []string{
		"wasi:cli/exit@0.2.3::exit",
		"wasi:cli/environment@0.2.3::get-arguments",
		"wasi:filesystem/preopens@0.2.3::get-directories",
	} {
		if _, ok, _ := lookupHost(m, want); !ok {
			t.Errorf("WASI impl %q disappeared when syncImpls were merged", want)
		}
	}
}

// TestSyncImplsRefuseToShadowWASI watches the collision refusal fire.
//
// Silently overriding a WASI name would be a capability change disguised as a map write — a guest asking
// for `wasi:cli/exit::exit` would get a test's function with no way to tell. So the merge refuses, and
// the refusal is witnessed rather than asserted to exist: a refusal nobody has seen fire is a
// permissiveness test.
func TestSyncImplsRefuseToShadowWASI(t *testing.T) {
	h := NewHost(io.Discard, io.Discard, nil)
	h.syncImpls = map[string]interp.CanonFunc{
		"wasi:cli/exit::exit": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
			return nil, nil
		},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("wasi() permitted an extra impl to shadow wasi:cli/exit::exit — a capability change " +
				"a guest cannot detect")
		}
		msg, ok := r.(string)
		if !ok || msg == "" {
			t.Fatalf("the refusal carried %v (%T); it must name the colliding import so the call site is "+
				"findable", r, r)
		}
		// The message must name the offending import. A bare "collision" would leave the author hunting.
		if !containsStr(msg, "wasi:cli/exit::exit") {
			t.Errorf("the refusal does not name the colliding import: %q", msg)
		}
	}()
	_ = h.wasi()
}

// containsStr avoids pulling `strings` in for one call in a test file that otherwise needs none.
func containsStr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
