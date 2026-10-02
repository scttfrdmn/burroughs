// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// # No test in this package may modify the production refusal log (grave #852)
//
// `TestMain` fingerprints `.editroute-log` before any test runs and again after all of them, and fails the
// package if it moved.
//
// ## Why this is a TestMain guard and not an audit of the call sites
//
// The rule is about an **artifact**, not about a code shape. Grave #852 was one test driving the hook with
// the environment unmodified, and the obvious repair — redirect `EDITROUTE_LOG` at that call site — fixes
// today's writer and says nothing about the next one. A future test could reach the log through a shell
// script, a helper, a `make` target, or the hook's own default path, and an audit of `exec.Command` calls
// would see none of those. Fingerprinting the file sees all of them, including a writer added by a route
// nobody has thought of yet.
//
// ## What #852 cost, which is why this is worth a package-wide guard
//
// The log's entire purpose is to replace a **recalled** false-positive rate with a measured one. Its own test
// suite had written 135 of its 137 entries. `scripts/refusals.sh` still printed a confident table, and
// nothing in that output could have told a reader the count was mostly synthetic — the failure mode of an
// instrument reporting its own activity is that it looks exactly like data.
//
// ## Why a hash and not a size
//
// A test that appended one line and another that removed one would leave the size unchanged. The content is
// what the rate is read from, so the content is what is fingerprinted. Absence is a state too: a log that
// did not exist before and does afterwards is a modification, and is reported as one.
func TestMain(m *testing.M) {
	root, err := filepath.Abs("../..")
	if err != nil {
		fmt.Fprintf(os.Stderr, "refusal-log guard: cannot resolve the repo root: %v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(root, ".editroute-log")

	before, beforeErr := fingerprintRefusalLog(logPath)
	code := m.Run()
	after, afterErr := fingerprintRefusalLog(logPath)

	// Only report a *change*. A tree with no log at all is the normal state on a fresh clone, and the guard
	// must not turn that into a failure — it is asserting that the tests leave the artifact alone, not that
	// the artifact exists.
	if before != after || (beforeErr == nil) != (afterErr == nil) {
		fmt.Fprintf(os.Stderr, `
refusal-log guard: FAIL — this package's tests MODIFIED %s.

  before: %s
  after:  %s

Some test drove scripts/editroute.py without redirecting EDITROUTE_LOG, so its refusals were appended to the
production log. That log exists to measure the hook's FALSE-POSITIVE RATE, and a test suite writing to it
destroys the figure: grave #852 found 137 entries of which 2 were real, its own tests having written the rest.

Fix: set EDITROUTE_LOG to a t.TempDir() path wherever the hook is invoked. Do not relax this guard — the
instrument is worthless if its dominant writer is the suite that tests it.
`, logPath, before, after)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// fingerprintRefusalLog returns a content hash for the log, or a marker when it is absent. The error is
// returned separately so "absent" and "present but empty" cannot collapse into the same reading.
func fingerprintRefusalLog(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<absent>", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes, sha256 %s", len(b), hex.EncodeToString(sum[:8])), nil
}
