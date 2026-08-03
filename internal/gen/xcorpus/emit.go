package xcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Dir is where the committed corpus lives, relative to the repo root.
//
// Committed, unlike `testdata/spec` and `third_party/spec`, and the distinction is the
// never-commit-corpora rule's actual subject: *provenance*, not test data. Upstream material
// the project does not own stays vendored and gitignored; this is an artifact generated here,
// from a pinned input, by a tool named in its own manifest — the same standing as a committed
// fuzz crasher (0005, PR #21). A fresh clone gets the #67 control with no fetch and no wabt.
const Dir = "testdata/xcorpus"

// ManifestFile and BlobFile are the corpus's two halves.
//
// **Two files rather than 1954**, because a directory of that many images makes every
// regeneration a diff nobody can read, and rather than one Go source file because 0.3 MB of
// byte literals is a compile-time cost paid by everyone for a test's convenience. The
// manifest is JSON so the provenance — versions, the pin, the skipped files — is legible in a
// review; the blob is opaque by nature and its integrity is the manifest's per-row length.
const (
	ManifestFile = "manifest.json"
	BlobFile     = "images.bin"
)

// manifestDoc is the on-disk form. Offsets into the blob replace the in-memory images.
type manifestDoc struct {
	// Note is for the reader who opens this file first and has no other context.
	Note string `json:"note"`

	WabtVersion string `json:"wabt_version"`
	SuiteRev    string `json:"suite_rev"`

	// Modules, and the counts a consumer checks itself against. The counts are redundant with
	// len(Modules) *on purpose*: a truncated manifest that still parses is caught by the
	// disagreement, where a length derived from the array can only ever agree with itself.
	ModuleCount int         `json:"module_count"`
	BlobBytes   int         `json:"blob_bytes"`
	Modules     []moduleDoc `json:"modules"`

	// SkippedFiles is the stated gap. Present even when empty, so its absence is a
	// malformed manifest rather than a corpus that looks complete.
	SkippedFiles []SkippedFile `json:"skipped_files"`
}

type moduleDoc struct {
	File    string `json:"file"`
	Ordinal int    `json:"ordinal"`
	Line    int    `json:"line"`
	Offset  int    `json:"offset"`
	Length  int    `json:"length"`
}

const manifestNote = "Generated cross-check corpus for issue #67 half 2 — independently " +
	"produced binary images of the suite's must-succeed text modules. Produced once by wabt " +
	"(version below) over the suite revision below; wabt is not invoked by any test and is " +
	"not in the verdict path. Regenerate with `make xcorpus`. The join key is (file, " +
	"ordinal); `line` is wabt's and is a corroborating signal only, because the two splitters " +
	"legitimately disagree on it. `skipped_files` is the corpus's stated gap."

// Write emits the manifest and blob under dir, creating it if needed.
func (m *Manifest) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	doc := manifestDoc{
		Note:         manifestNote,
		WabtVersion:  m.WabtVersion,
		SuiteRev:     m.SuiteRev,
		SkippedFiles: m.SkippedFiles,
	}
	if doc.SkippedFiles == nil {
		doc.SkippedFiles = []SkippedFile{}
	}
	var blob []byte
	for _, mod := range m.Modules {
		doc.Modules = append(doc.Modules, moduleDoc{
			File:    mod.File,
			Ordinal: mod.Ordinal,
			Line:    mod.Line,
			Offset:  len(blob),
			Length:  len(mod.Image),
		})
		blob = append(blob, mod.Image...)
	}
	doc.ModuleCount = len(doc.Modules)
	doc.BlobBytes = len(blob)

	b, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, BlobFile), blob, 0o644)
}

// Load reads a committed corpus back.
//
// Every row's extent is checked against the blob's actual length rather than trusted, and the
// two redundant counts are required to agree: a manifest and a blob that fell out of step
// would otherwise hand the consumer *some other module's bytes* under the right key, which is
// the wrong-module failure #67 exists to catch, arriving through the instrument instead of
// through the encoder.
func Load(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("%w (run: make xcorpus)", err)
	}
	var doc manifestDoc
	if uerr := json.Unmarshal(b, &doc); uerr != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestFile, uerr)
	}
	blob, err := os.ReadFile(filepath.Join(dir, BlobFile))
	if err != nil {
		return nil, err
	}
	if doc.ModuleCount != len(doc.Modules) {
		return nil, fmt.Errorf("manifest says %d modules and lists %d: the two disagree, so "+
			"one of them is not being maintained", doc.ModuleCount, len(doc.Modules))
	}
	if doc.BlobBytes != len(blob) {
		return nil, fmt.Errorf("manifest says the blob is %d bytes and it is %d: the manifest "+
			"and the images are out of step, and a row read under those conditions would "+
			"return some other module's bytes", doc.BlobBytes, len(blob))
	}
	m := &Manifest{
		WabtVersion:  doc.WabtVersion,
		SuiteRev:     doc.SuiteRev,
		SkippedFiles: doc.SkippedFiles,
	}
	for _, row := range doc.Modules {
		end := row.Offset + row.Length
		if row.Length <= 0 || row.Offset < 0 || end > len(blob) {
			return nil, fmt.Errorf("%s ordinal %d has extent [%d,%d) in a %d-byte blob",
				row.File, row.Ordinal, row.Offset, end, len(blob))
		}
		m.Modules = append(m.Modules, Module{
			File:    row.File,
			Ordinal: row.Ordinal,
			Line:    row.Line,
			Image:   blob[row.Offset:end:end],
		})
	}
	return m, nil
}

// Lookup indexes the corpus by its join key, (file, ordinal).
func (m *Manifest) Lookup() map[ModuleKey]Module {
	out := make(map[ModuleKey]Module, len(m.Modules))
	for _, mod := range m.Modules {
		out[ModuleKey{File: mod.File, Ordinal: mod.Ordinal}] = mod
	}
	return out
}

// ModuleKey is the join key: a file's base name and the module's ordinal within it.
type ModuleKey struct {
	File    string
	Ordinal int
}
