// Command xcorpus regenerates the #67 cross-check corpus: independently produced binary
// images of the suite's must-succeed text modules.
//
// Usage:
//
//	go run ./internal/gen/xcorpus/cmd/xcorpus
//
// or, normally, 'make xcorpus'. Requires wabt on PATH and the suite vendored; the output is
// committed, so neither is needed to *use* the corpus — which is the whole point of
// generating it once (0011's second appendix: wabt is a generator, never a gate).
//
// The suite SHA is read from the fetch script's pin rather than passed in, for the reason
// opgen's cmd states: a SHA typed at a second site is a citation that can drift from the pin
// it claims to describe.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/xcorpus"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

func main() {
	out := flag.String("o", "", "output directory (default: the committed corpus dir)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "xcorpus: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	// The *suite* pin, not the reference pin: this corpus is cut from `testdata/spec`, so
	// stamping `fetch-spec-ref.sh`'s SHA would be a provenance header naming the wrong
	// artifact — a citation that resolves and is about something else, which is worse than a
	// missing one.
	rev, err := gen.PinnedSuiteRev()
	if err != nil {
		return err
	}
	suiteDir, err := gen.FromRoot(testenv.SuiteDir)
	if err != nil {
		return err
	}
	if out == "" {
		if out, err = gen.FromRoot(xcorpus.Dir); err != nil {
			return err
		}
	}

	// A temp dir rather than a path under the repo: wast2json writes one `.json` and one
	// `.wasm` per module — 5330 files for this corpus — and none of them is the artifact. Only
	// the images read back into the manifest are.
	work, err := os.MkdirTemp("", "xcorpus-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	m, err := xcorpus.Generate(suiteDir, work, rev)
	if err != nil {
		return err
	}
	if err := m.Write(out); err != nil {
		return err
	}
	fmt.Printf("xcorpus: %d modules from %s (wabt %s, suite %s), %d files skipped\n",
		len(m.Modules), suiteDir, m.WabtVersion, rev[:12], len(m.SkippedFiles))
	for _, s := range m.SkippedFiles {
		fmt.Printf("  skipped %-28s %s\n", s.File, s.Reason)
	}
	return nil
}
