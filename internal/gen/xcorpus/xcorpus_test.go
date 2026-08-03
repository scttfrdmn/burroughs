package xcorpus_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/xcorpus"
)

// corpusDir resolves the committed corpus from wherever the test runs.
func corpusDir(t *testing.T) string {
	t.Helper()
	dir, err := gen.FromRoot(xcorpus.Dir)
	if err != nil {
		t.Fatalf("resolving the corpus dir: %v", err)
	}
	return dir
}

// TestCommittedCorpusLoads asserts the committed artifact reads back, with the floors that
// keep the assertion from being about an empty set.
//
// **No suite and no wabt required**, which is the artifact's whole purpose: the corpus is
// committed precisely so the #67 control does not depend on a fetch or a non-Go binary. A skip
// here would therefore be licensing the absence of something that is checked in — so there is
// no skip, and `requireSuite` is deliberately not called.
func TestCommittedCorpusLoads(t *testing.T) {
	m, err := xcorpus.Load(corpusDir(t))
	if err != nil {
		t.Fatalf("loading the committed corpus: %v", err)
	}

	// The floors, per partition rather than one total. A single module-count floor would be
	// satisfied by 1955 zero-length images, and the *interesting* content is bytes: the
	// control that consumes this compares instruction sequences, so an image population with
	// no bytes in it would agree with anything.
	//
	// Measured at generation: 1954 modules, 312866 blob bytes, 31 skipped files. Floors rather
	// than equalities because a suite bump legitimately moves all three — and a *ceiling* on
	// the skips for the same reason in the other direction, since a wabt upgrade that started
	// failing on half the corpus would otherwise pass every floor here while quietly halving
	// the population (*an unasserted distance is the vacuum*).
	if len(m.Modules) < 1900 {
		t.Errorf("corpus holds %d modules, want >=1900 (generated 1954): every claim a "+
			"consumer makes about this corpus is inside a loop over it", len(m.Modules))
	}
	var bytes int
	for _, mod := range m.Modules {
		bytes += len(mod.Image)
	}
	if bytes < 300_000 {
		t.Errorf("corpus images total %d bytes, want >=300000 (generated 312866): a module "+
			"count with no bytes behind it is a population of empty images", bytes)
	}
	if n := len(m.SkippedFiles); n > 40 {
		t.Errorf("%d files are recorded as skipped, want <=40 (generated 31): a rise here "+
			"means the generator's producer stopped understanding the corpus, which shrinks "+
			"the population without moving any floor above", n)
	}

	// Provenance, asserted rather than assumed present. *A provenance header that says
	// nothing is worse than none, because it looks stamped.*
	if m.WabtVersion == "" {
		t.Error("the manifest records no wabt version: the corpus's independence from this " +
			"engine is exactly what makes it evidence, and an unnamed producer cannot be " +
			"checked by a reader")
	}
	// And the pin must be the *suite* pin, not the reference pin — both resolve, so a mix-up
	// produces a header that is stamped, plausible, and about the wrong artifact.
	want, err := gen.PinnedSuiteRev()
	if err != nil {
		t.Fatalf("reading the suite pin: %v", err)
	}
	if m.SuiteRev != want {
		t.Errorf("the corpus was cut from suite %s but the pin is now %s: regenerate with "+
			"`make xcorpus`, or the images describe a corpus the board no longer measures",
			m.SuiteRev, want)
	}

	// Every skip carries a reason. An entry with an empty reason is the unexplained-absence
	// failure the manifest exists to prevent: it would read as "tracked" while saying nothing
	// about what is missing.
	for _, s := range m.SkippedFiles {
		if s.File == "" || s.Reason == "" {
			t.Errorf("a skipped-file entry is incomplete (%q: %q); a stated gap with no reason "+
				"is the shape of a gap nobody stated", s.File, s.Reason)
		}
	}

	// Keys are unique. Two rows under one (file, ordinal) would make Lookup return whichever
	// came last, silently — a consumer would then compare against *some other module*, which
	// is precisely the wrong-module failure the corpus exists to detect.
	seen := map[xcorpus.ModuleKey]bool{}
	for _, mod := range m.Modules {
		k := xcorpus.ModuleKey{File: mod.File, Ordinal: mod.Ordinal}
		if seen[k] {
			t.Errorf("duplicate corpus key %s#%d", k.File, k.Ordinal)
		}
		seen[k] = true
	}
	if got := len(m.Lookup()); got != len(m.Modules) {
		t.Errorf("Lookup indexed %d of %d modules", got, len(m.Modules))
	}

	t.Logf("committed corpus: %d modules, %d image bytes, %d files skipped (wabt %s, suite %s)",
		len(m.Modules), bytes, len(m.SkippedFiles), m.WabtVersion, m.SuiteRev[:12])
}

// TestLoadRejectsAManifestOutOfStepWithItsBlob is Load's falsification, and it is the one
// that matters: the corpus is two files, so they can fall out of step, and a row read under
// those conditions returns *some other module's bytes* under the right key. That is the
// wrong-module failure #67 exists to catch, arriving through the instrument instead of through
// the encoder — the worst place for it, because the control would be reporting a defect it
// caused.
//
// Each case is a real corruption of a real manifest, written to a temp dir and loaded back.
func TestLoadRejectsAManifestOutOfStepWithItsBlob(t *testing.T) {
	src := corpusDir(t)
	good, err := os.ReadFile(filepath.Join(src, xcorpus.ManifestFile))
	if err != nil {
		t.Fatalf("reading the committed manifest: %v", err)
	}
	blob, err := os.ReadFile(filepath.Join(src, xcorpus.BlobFile))
	if err != nil {
		t.Fatalf("reading the committed blob: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, doc map[string]any) []byte // returns the blob to write
		wantSub string
	}{
		{
			name: "the blob is truncated",
			mutate: func(_ *testing.T, _ map[string]any) []byte {
				return blob[:len(blob)-1]
			},
			wantSub: "out of step",
		},
		{
			name: "module_count disagrees with the list",
			mutate: func(_ *testing.T, doc map[string]any) []byte {
				doc["module_count"] = 1
				return blob
			},
			wantSub: "disagree",
		},
		{
			name: "a row's extent runs past the blob",
			mutate: func(t *testing.T, doc map[string]any) []byte {
				t.Helper()
				mods, ok := doc["modules"].([]any)
				if !ok || len(mods) == 0 {
					t.Fatal("the manifest has no modules to corrupt: this case would pass by " +
						"asking nothing, which is the vacuity it is here to avoid")
				}
				row := mods[0].(map[string]any)
				row["length"] = float64(len(blob) + 10)
				return blob
			},
			wantSub: "extent",
		},
		{
			name: "a row has zero length",
			mutate: func(t *testing.T, doc map[string]any) []byte {
				t.Helper()
				mods, ok := doc["modules"].([]any)
				if !ok || len(mods) == 0 {
					t.Fatal("the manifest has no modules to corrupt")
				}
				mods[0].(map[string]any)["length"] = float64(0)
				return blob
			},
			wantSub: "extent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(good, &doc); err != nil {
				t.Fatalf("re-parsing the committed manifest: %v", err)
			}
			wantBlob := tc.mutate(t, doc)

			dir := t.TempDir()
			b, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if werr := os.WriteFile(filepath.Join(dir, xcorpus.ManifestFile), b, 0o644); werr != nil {
				t.Fatal(werr)
			}
			if werr := os.WriteFile(filepath.Join(dir, xcorpus.BlobFile), wantBlob, 0o644); werr != nil {
				t.Fatal(werr)
			}

			_, err = xcorpus.Load(dir)
			if err == nil {
				t.Fatalf("Load accepted a corpus whose manifest and blob are out of step (%s); "+
					"a consumer would then read some other module's bytes under the right key",
					tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("Load rejected it with %q, want a message containing %q: the "+
					"partition members share a failure and only the message distinguishes "+
					"which check fired", err, tc.wantSub)
			}
		})
	}
}

// TestLoadAcceptsTheUnmutatedCopy is the control on the control: every case above writes a
// mutated manifest into a temp dir, so if the *round trip itself* were broken they would all
// fail for that reason instead and still be green.
//
// This is the vacuity check for a falsification suite — the cases prove Load says no, and
// this proves it was capable of saying yes.
func TestLoadAcceptsTheUnmutatedCopy(t *testing.T) {
	src := corpusDir(t)
	dir := t.TempDir()
	for _, name := range []string{xcorpus.ManifestFile, xcorpus.BlobFile} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := xcorpus.Load(dir)
	if err != nil {
		t.Fatalf("Load rejected an unmutated copy of the committed corpus: %v\n\tEvery "+
			"rejection case in this package would then be passing for the wrong reason.", err)
	}
	if len(m.Modules) == 0 {
		t.Error("an unmutated copy loaded with no modules")
	}
}
