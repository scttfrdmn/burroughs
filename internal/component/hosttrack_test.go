// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The host's import keying (#1229, ADR 0115): a lowered import meets a host entry by its name and its
// semver compatibility class, so WASI 0.2 and 0.3, which share names with different signatures, are never
// merged. These are the witnesses: the class rule, the cross-track refusal in both directions, what still
// serves, and the control that keeps a production entry from becoming a silent wildcard.

// TestSplitVersionKeysByCompatibilityClass pins the class rule. The cases that matter most are the pairs:
// two 0.2 patches are one class, 0.2 and 0.3 are two, and a pre-release does not leave its track.
func TestSplitVersionKeysByCompatibilityClass(t *testing.T) {
	const name = "wasi:cli/stdout::get-stdout"
	cases := []struct {
		key, class string
	}{
		{"wasi:cli/stdout@0.2.3::get-stdout", "0.2"},
		{"wasi:cli/stdout@0.2.6::get-stdout", "0.2"},
		{"wasi:cli/stdout@0.3.0::get-stdout", "0.3"},
		{"wasi:cli/stdout@0.3.2::get-stdout", "0.3"},
		{"wasi:cli/stdout@0.3.0-rc-2026-02-09::get-stdout", "0.3"},
		{"wasi:cli/stdout@1.2.0::get-stdout", "1"},
		{"wasi:cli/stdout@0.1.0::get-stdout", "0.1"},
		{"wasi:cli/stdout@0.0.4::get-stdout", "0.0.4"},
		{"wasi:cli/stdout@0.2.3+build.7::get-stdout", "0.2"},
		{"wasi:cli/stdout::get-stdout", classNone},
		// Malformed: not three numeric parts. Its own class, which nothing serves.
		{"wasi:cli/stdout@0.3::get-stdout", classUnparseable + "0.3"},
		{"wasi:cli/stdout@0.03.0::get-stdout", classUnparseable + "0.03.0"},
		{"wasi:cli/stdout@0.3.0-::get-stdout", classUnparseable + "0.3.0-"},
	}
	for _, c := range cases {
		gotName, gotClass := splitVersion(c.key)
		if gotName != name || gotClass != c.class {
			t.Errorf("splitVersion(%q) = (%q, %q), want (%q, %q)", c.key, gotName, gotClass, name, c.class)
		}
	}
	// The export name keeps its own '.' and '@'-free shape; only the interface's version is cut.
	if n, c := splitVersion("wasi:io/streams@0.2.6::[method]output-stream.write"); n != "wasi:io/streams::[method]output-stream.write" || c != "0.2" {
		t.Errorf("a method export's name was cut wrongly: (%q, %q)", n, c)
	}
	// A bare world-level import has no "::" and no version.
	if n, c := splitVersion("tick"); n != "tick" || c != classNone {
		t.Errorf("splitVersion(tick) = (%q, %q), want (tick, %q)", n, c, classNone)
	}
}

// productionWalker is a walker carrying the real production host tables, wired exactly as
// InstantiateWithHostFeatures wires them, so resolveHost is the lookup a guest's lower meets.
func productionWalker(h *Host) *walker {
	return &walker{wasiHost: h.wasi(), asyncWasiHost: h.asyncWasi(), streamConsumers: h.streamConsumers(), async: newAsyncHandles()}
}

// TestCrossTrackImportRefusesNamingBothTracks is the defect's witness. Each import names a function the
// host implements for one WASI track only, at the other track's version. Under the version-stripped keys
// each one bound the other track's implementation; now each binds a refusal that, called, returns
// ErrLinkRefused and names both the track the host serves and the one the guest asked for.
//
// The reverse direction matters on its own: `write-via-stream` lives in the stream-consumer table, which
// a 0.2 import misses before it plainly misses the sync table, so a refusal built from the last table
// alone would call it "not provided".
func TestCrossTrackImportRefusesNamingBothTracks(t *testing.T) {
	cases := []struct {
		key, served, asked string
	}{
		{"wasi:cli/stdout@0.3.0::get-stdout", "WASI 0.2", "0.3"},
		{"wasi:io/streams@0.3.0::[method]output-stream.write", "WASI 0.2", "0.3"},
		{"wasi:cli/stdout@0.2.6::write-via-stream", "WASI 0.3", "0.2"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			r := productionWalker(NewHost(io.Discard, io.Discard, nil)).resolveHost(c.key, false)
			if r.sync != nil || r.consumer != nil || r.async != nil {
				t.Fatalf("%s bound an implementation; the host serves it only for %s", c.key, c.served)
			}
			_, err := r.refusal("actual", "0", c.key)(nil, nil)
			if !errors.Is(err, ErrLinkRefused) {
				t.Fatalf("calling %s returned %v, want ErrLinkRefused", c.key, err)
			}
			msg := err.Error()
			for _, want := range []string{c.key, "only a " + c.served + " implementation", "not served for " + c.asked} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal %q does not contain %q", msg, want)
				}
			}
			if strings.Contains(msg, "is not provided") {
				t.Errorf("refusal %q reads as a plain miss; the host does provide %s, for another track", msg, c.key)
			}
		})
	}
}

// TestSharedTrackEntriesStillServe is the arm that keeps the refusal from being a permissiveness change in
// reverse: what each track's guests run on still binds, at a patch version no fixture carries. `exit` is
// called and observed, so the binding is the real implementation, not merely some entry.
func TestSharedTrackEntriesStillServe(t *testing.T) {
	h := NewHost(io.Discard, io.Discard, nil)
	w := productionWalker(h)

	r := w.resolveHost("wasi:cli/exit@0.3.2::exit", false)
	if r.sync == nil {
		t.Fatalf("wasi:cli/exit@0.3.2::exit is not served (servedFor %v); exit is the same in 0.2 and 0.3", r.servedFor)
	}
	if _, err := r.sync(nil, []interp.Value{interp.I32(1)}); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if !h.Exited || h.ExitCode != 1 {
		t.Errorf("exit@0.3.2 did not reach the host's exit: Exited=%v ExitCode=%d", h.Exited, h.ExitCode)
	}

	for _, key := range []string{
		"wasi:cli/environment@0.3.0::get-arguments",
		"wasi:cli/environment@0.2.3::get-arguments",
		"wasi:cli/stdout@0.2.6::get-stdout",
		"wasi:io/streams@0.2.3::[method]output-stream.write",
	} {
		if r := w.resolveHost(key, false); r.sync == nil {
			t.Errorf("%s is not served (servedFor %v)", key, r.servedFor)
		}
	}
	if r := w.resolveHost("wasi:cli/stdout@0.3.0::write-via-stream", false); r.consumer == nil {
		t.Errorf("write-via-stream@0.3.0 is not served (servedFor %v)", r.servedFor)
	}
}

// TestUnversionedHostKeyIsAWildcard: a test-only registration's unversioned key serves every parseable
// class, including an unversioned import, and no unparseable one; a name served nowhere is still a plain
// miss, with no track to name.
func TestUnversionedHostKeyIsAWildcard(t *testing.T) {
	table := map[string]interp.CanonFunc{"test:pkg/iface::make-thing": dummyCanon}
	for _, key := range []string{
		"test:pkg/iface::make-thing",
		"test:pkg/iface@0.2.3::make-thing",
		"test:pkg/iface@0.3.0::make-thing",
		"test:pkg/iface@1.0.0::make-thing",
	} {
		if _, ok, _ := lookupHost(table, key); !ok {
			t.Errorf("the wildcard does not serve %s", key)
		}
	}
	if _, ok, served := lookupHost(table, "test:pkg/iface@0.3::make-thing"); ok || served != nil {
		t.Errorf("an unparseable version was served (ok=%v, servedFor %v); it is its own class", ok, served)
	}
	if _, ok, served := lookupHost(table, "test:pkg/other@0.2.0::f"); ok || served != nil {
		t.Errorf("a plain miss reported ok=%v servedFor %v", ok, served)
	}
}

// TestProductionHostEntriesDeclareTheirTracks is the control on the registration side. An unversioned key
// is a wildcard, so a production entry written without `serve` would answer every track silently — which
// is the merge this slice removed, back in by one map literal. Every production key must carry a class.
func TestProductionHostEntriesDeclareTheirTracks(t *testing.T) {
	h := NewHost(io.Discard, io.Discard, nil)
	keys := []string{}
	for k := range h.wasi() {
		keys = append(keys, k)
	}
	for k := range h.streamConsumers() {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatal("the production host tables are empty, so this control checks nothing")
	}
	for _, k := range keys {
		if _, class, versioned := cutVersion(k); !versioned || (class != "0.2" && class != "0.3") {
			t.Errorf("production host key %q is not registered for a WASI track; use serve()", k)
		}
	}
}
