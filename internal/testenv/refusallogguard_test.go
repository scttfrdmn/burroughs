// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
//
// ## The guard sees the artifact move; it does NOT see who moved it
//
// This distinction was missing until the modification actually happened for the **other** reason, and the
// message said the wrong thing with no hedge: *"this package's tests MODIFIED …"*, then *"Some test drove
// scripts/editroute.py without redirecting EDITROUTE_LOG."* Neither was true. The writer was the **live
// hook**, serving the session that had launched `make ci` in the background: the agent tripped a real
// `sleep-as-wait` refusal mid-run, the hook appended it to the production log exactly as it should, and
// this guard read the append as a test's.
//
// The hook was right and the guard's *reading* was wrong, which is why the repair is entirely on this
// side. A genuine refusal belongs in the log — it is a true positive, and the rate is what the log is
// for. What has to change is the testimony, because a control that names one mechanism when another
// produces the same observation sends its reader hunting for a cause that does not exist. It cost exactly
// that: a search for a test that was never there.
//
// **It still fails**, and the guard's own closing line is why — relaxing it reopens #852. What it gains is
// an honest account of what is and is not known, plus the two things a reader can actually check:
//
//   - **The delta.** When the log only grew, the appended lines are printed. They carry a timestamp and a
//     *hash* of the command rather than the command (Scott's rule, so a path or a secret cannot land in
//     the log), so printing them leaks nothing and a timestamp inside the run's window is visible.
//   - **Re-running discriminates the two writers.** A test's write is part of the suite and recurs on
//     every run. A session-driven refusal does not recur, so a clean re-run says the writer was not the
//     suite. That is a real check, not a hope, and it is the instruction the message was missing.
func TestMain(m *testing.M) {
	root, err := filepath.Abs("../..")
	if err != nil {
		fmt.Fprintf(os.Stderr, "refusal-log guard: cannot resolve the repo root: %v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(root, ".editroute-log")

	// The **content** is kept, not only the fingerprint, so the delta can be reported. The fingerprint
	// remains the comparison — it is what makes an append-plus-removal visible — and the bytes are only
	// ever used to describe a difference the fingerprint already found.
	beforeBytes, beforeErr := os.ReadFile(logPath)
	before := describeRefusalLog(beforeBytes, beforeErr)
	start := time.Now()
	code := m.Run()
	afterBytes, afterErr := os.ReadFile(logPath)
	after := describeRefusalLog(afterBytes, afterErr)

	// Only report a *change*. A tree with no log at all is the normal state on a fresh clone, and the guard
	// must not turn that into a failure — it is asserting that the tests leave the artifact alone, not that
	// the artifact exists.
	if before != after || (beforeErr == nil) != (afterErr == nil) {
		fmt.Fprint(os.Stderr, refusalLogGuardFailure(logPath, before, after, start, beforeBytes, afterBytes))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// refusalLogGuardFailure is the guard's testimony, factored out so **the message itself** is testable and
// not only the delta it embeds.
//
// Factored for the reason the repair exists: the old message was a flat false accusation, and nothing
// could have caught that, because the only way to read it was to make the guard fire — which means
// writing to the production log, the one artifact nothing here may touch. A message no test can see is a
// message whose claims drift from what the control observes, and that drift is what cost the lookup.
func refusalLogGuardFailure(logPath, before, after string, start time.Time, beforeBytes, afterBytes []byte) string {
	return fmt.Sprintf(`
refusal-log guard: FAIL — %s changed while this package's tests ran.

  before: %s
  after:  %s
  run started: %s

WHO wrote it is not something this guard can see. Two writers produce this observation:

  1. A test in this package drove scripts/editroute.py without redirecting EDITROUTE_LOG, so its refusals
     were appended to the production log. That is grave #852, which found 137 entries of which 2 were real.
     Fix: set EDITROUTE_LOG to a t.TempDir() path wherever the hook is invoked.
  2. The live hook, serving the session that launched this run. An agent that trips a real refusal while
     "make ci" runs in the background gets that refusal appended — correctly, since it is a true positive
     and the rate is what the log is for. Nothing is wrong with the tree in that case.

RE-RUN to tell them apart: writer 1 is part of the suite and recurs every run; writer 2 does not. A clean
re-run means the suite did not write.%s
Do not relax this guard either way — the instrument is worthless if its dominant writer is the suite that
tests it, and that is exactly what it failed to notice for 137 entries.
`, logPath, before, after, start.UTC().Format(time.RFC3339), appendedLines(beforeBytes, afterBytes))
}

// appendedLines renders the lines the log gained, when it gained some and lost none.
//
// **Only for a strict append**, because that is the only case where "what was added" is well defined: a
// log that was rewritten or truncated has no delta to show, and inventing one would be the guard
// testifying past what it observed — the defect this whole repair is about, one level in.
//
// The entries are safe to print: each is a timestamp, a rule name, and a **hash** of the command rather
// than the command itself, which is Scott's rule precisely because a command can carry a path or a secret.
func appendedLines(before, after []byte) string {
	if len(after) <= len(before) || !bytes.HasPrefix(after, before) {
		return "\n(The log was not a strict append, so there is no delta to show.)\n"
	}
	added := strings.TrimRight(string(after[len(before):]), "\n")
	if added == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe log gained these entries (a hash of the command, never the command):\n\n")
	for _, line := range strings.Split(added, "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\nA timestamp inside the window above was written during this run.\n")
	return b.String()
}

// TestTheRefusalLogGuardsDeltaIsOnlyShownForAnAppend is the guard's own witness.
//
// `TestMain` cannot test itself — it *is* the harness — but `appendedLines` is a pure function and carries
// the only branch the repair added, so it is the part that can be stillborn. A delta shown for a rewritten
// log would be the guard testifying past what it observed, which is the defect the repair is about.
func TestTheRefusalLogGuardsDeltaIsOnlyShownForAnAppend(t *testing.T) {
	const old = "2026-10-08T15:22:59Z\tinline-write\tec989acc99cf\n"
	const added = "2026-10-08T19:00:29Z\tsleep-as-wait\t96fb8bd5bdcc\n"

	// The real case: the live hook appended one entry while the suite ran.
	got := appendedLines([]byte(old), []byte(old+added))
	if !strings.Contains(got, "sleep-as-wait\t96fb8bd5bdcc") {
		t.Errorf("an appended entry was not reported:\n%s", got)
	}
	if strings.Contains(got, "no delta") {
		t.Errorf("a strict append was reported as having no delta:\n%s", got)
	}

	// A log that was rewritten rather than appended to has no well-defined delta, and saying so is the
	// point: the alternative is printing lines that were not added.
	for _, c := range []struct{ name, before, after string }{
		{"rewritten", old, added},
		{"truncated", old + added, old},
		{"unchanged length, different content", old, strings.Repeat("x", len(old))},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := appendedLines([]byte(c.before), []byte(c.after))
			if !strings.Contains(g, "no delta") {
				t.Errorf("a %s log claimed a delta:\n%s", c.name, g)
			}
			// And it must not print the would-be "added" bytes, which is the specific overreach.
			if strings.Contains(g, "sleep-as-wait") {
				t.Errorf("a %s log printed entries as added:\n%s", c.name, g)
			}
		})
	}

	// An absent-to-present transition is a modification `TestMain` reports through the fingerprints, and
	// `appendedLines` treats an empty "before" as the append it is.
	if g := appendedLines(nil, []byte(added)); !strings.Contains(g, "sleep-as-wait") {
		t.Errorf("a log that appeared during the run reported no entries:\n%s", g)
	}

	// **The testimony itself**, which is what actually went wrong: the old message stated as fact that a
	// test had written the log. Asserted here because the only other way to read this message is to make
	// the guard fire, and that means writing to the production log — the one artifact nothing here may
	// touch. So the message was unreadable by any test, and an unreadable claim is one that drifts.
	msg := refusalLogGuardFailure("/repo/.editroute-log",
		"1 bytes, sha256 aa", "2 bytes, sha256 bb", time.Unix(0, 0).UTC(),
		[]byte(old), []byte(old+added))
	for _, want := range []string{
		"WHO wrote it is not something this guard can see", // it does not accuse
		"The live hook, serving the session that launched this run",
		"RE-RUN to tell them apart",   // the discriminator is stated
		"Do not relax this guard",     // and it is still a failure
		"sleep-as-wait\t96fb8bd5bdcc", // the delta is shown
		"1970-01-01T00:00:00Z",        // and the window it is read against
		"/repo/.editroute-log",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the guard's failure message does not contain %q:\n%s", want, msg)
		}
	}
	// The old accusation must not come back: it is what sent a reader hunting for a test that did not
	// exist. A phrasing check and not a vague one — these are the exact words that were wrong.
	for _, gone := range []string{
		"this package's tests MODIFIED",
		"Some test drove scripts/editroute.py",
	} {
		if strings.Contains(msg, gone) {
			t.Errorf("the guard's message still asserts %q as fact; it cannot see the writer", gone)
		}
	}
}

// describeRefusalLog returns a content hash for the log, or a marker when it is absent. The read error is
// the caller's to keep, so "absent" and "present but empty" cannot collapse into the same reading.
func describeRefusalLog(b []byte, err error) string {
	if err != nil {
		return "<absent>"
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes, sha256 %s", len(b), hex.EncodeToString(sum[:8]))
}
