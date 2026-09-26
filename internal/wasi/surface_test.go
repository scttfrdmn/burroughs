// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// specAdditions are names a `wasip1` guest may import that are NOT in snapshot-01's witx, each with the
// authority that puts it in the domain.
//
// **Named individually rather than pattern-matched**, so the domain stays derived-plus-exceptions rather than
// becoming a hand-kept list with a spec-shaped preamble. An addition with no consumer does not belong here.
var specAdditions = map[string]string{
	// Go's own wasip1 port declares it: src/syscall/net_wasip1.go,
	// `//go:wasmimport wasi_snapshot_preview1 sock_accept`. Batch 2's `os` and `encoding/json` guests both
	// import it, which is how it was found.
	"sock_accept": "post-snapshot addition imported by Go's wasip1 port",
}

var witxFunc = regexp.MustCompile(`\(@interface func \(export "([a-z_0-9]+)"\)`)

// TestSuppliedSurfaceMatchesTheSpecification is the witness for #827, and its domain comes from the
// SPECIFICATION rather than from a list anyone maintained.
//
// # Why the domain is derived
//
// A hand-written list of "the functions we supply" can only ever agree with itself. `#264`'s closing lesson is
// the rule — *derive the domain from the space, never from the registry* — and the space here is the
// preview-1 interface definition, committed at `testdata/preview1/wasi_snapshot_preview1.witx` with its
// provenance. Adding a function to the host without adding it to the spec file is impossible, because the
// spec file is upstream's; forgetting to add one the spec has now fails here.
//
// # Both directions, because each catches a different mistake
//
//	spec -> host   a name a guest may import that the host lacks. THIS IS WHAT #827 WAS: a missing import
//	               makes link reject the WHOLE PROGRAM, so `os` could not run one read-only test for want of
//	               names those tests never call.
//	host -> spec   a name the host supplies that nothing may import — dead surface, or a typo in a key that
//	               would otherwise sit there resolving nothing forever.
//
// # What it deliberately does NOT check
//
// That each function *works*. Most of this surface refuses by name, and refusing is the decision ADR 0083
// made; `TestDeferredRefusalsFire` is what witnesses a refusal actually firing. This checks only that the
// surface is COMPLETE, which is the property link depends on.
func TestSuppliedSurfaceMatchesTheSpecification(t *testing.T) {
	witx, err := os.ReadFile(filepath.Join("testdata", "preview1", "wasi_snapshot_preview1.witx"))
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	m := witxFunc.FindAllStringSubmatch(string(witx), -1)
	if len(m) < 40 {
		// A witness whose domain collapsed would pass by asking nothing. snapshot-01 has 45; a floor well
		// below that catches a parse that silently matched almost nothing without pinning the count.
		t.Fatalf("parsed only %d functions from the witx; the extraction is broken, and a witness with an "+
			"empty domain passes by asking nothing", len(m))
	}
	want := map[string]bool{}
	for _, g := range m {
		want[g[1]] = true
	}
	specCount := len(want)
	for name := range specAdditions {
		want[name] = true
	}

	h := newHost(Config{})
	got := h.suppliedNamesForTest()

	var missing, extra []string
	for n := range want {
		if !got[n] {
			missing = append(missing, n)
		}
	}
	for n := range got {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("the host does not supply %d name(s) a guest may import: %s\n"+
			"\tA MISSING IMPORT REJECTS THE WHOLE PROGRAM at instantiation — every read-only test in it, "+
			"including ones that never call the missing function. That is what #827 was. Supply it, "+
			"implemented or refusing by name (ADR 0080: link refuses a gap).",
			len(missing), strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("the host supplies %d name(s) that are neither in the spec nor a documented addition: %s\n"+
			"\tEither it is dead surface, or it is a typo in a table key that would sit there resolving "+
			"nothing. If a guest really imports it, add it to specAdditions WITH the authority that says so.",
			len(extra), strings.Join(extra, ", "))
	}

	// The counts are reported even on success, because "the sets are equal" is also true of two empty sets,
	// and the floor above only bounds the spec side.
	t.Logf("SURFACE spec=%d additions=%d domain=%d supplied=%d",
		specCount, len(specAdditions), len(want), len(got))
}
