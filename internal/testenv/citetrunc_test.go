// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// truncCutByte is the byte offset `citecheck.sh`'s `trunc` cuts at — `substr(s, 1, 177)`, so the
// 178th byte is the first one dropped. Named because the fixture below is built to straddle exactly
// this offset with a multi-byte character, and a fixture that misses it proves nothing.
const truncCutByte = 177

// TestALongMultiByteSentenceIsNotAnInstrumentFailure asserts that `citecheck.sh` survives a PR body
// containing a sentence long enough to be truncated and non-ASCII enough for the cut to land inside a
// character.
//
// # The grave
//
// #620. `citecheck.sh`'s discharge-claim scanner quotes an offending sentence back to the reader
// through `function trunc(s) { return (length(s) > 180) ? substr(s, 1, 177) "..." : s }`, and the awk
// this repo runs is **byte-oriented** — `length` of one em-dash is 3. So the cut is at a byte offset,
// and when byte 178 falls inside a multi-byte sequence the truncated string keeps a *prefix* of that
// sequence. Two bytes of an em-dash, in the observed case.
//
// The next stage is `sort -u`, and BSD `sort` under a `.UTF-8` locale refuses invalid input: it exits
// non-zero having printed one line, `sort: Illegal byte sequence`. Under `set -o pipefail` that became
// the script's verdict. Observed as **exit 2 with that single line and no FAIL anywhere** — a verdict
// with no located failure, from the checker whose entire job is pointing at things. Confirmed as the
// whole difference by re-running the identical body under `LC_ALL=C`, where `sort` does not validate
// bytes and the check exits 0 over 0 findings: there was no citation defect in the body at all.
//
// # Two defects, and this control is for the second
//
// That `trunc` mangles a dash is cosmetic — a FAIL quoted back with a broken character is ugly and
// still legible. What makes it a grave is the *amplification*: a formatting defect in a message
// became a dead checker, and nothing between the two can tell a reader which of them happened. The
// failure also fires only on bodies long enough and non-ASCII enough to reach the boundary, which is
// to say on exactly the bodies this repo writes and never on a short fixture.
//
// # The fixture pre-registers its own sharpness
//
// A control whose fixture drifts into harmlessness passes vacuously, so the straddle is **asserted
// rather than assumed**: the test fails loudly if the multi-byte rune does not span byte 178 of the
// sentence, or if the sentence is not long enough to be truncated at all. That is the arm that keeps
// this from becoming a test of nothing after someone rewords the padding.
//
// # Three arms, and the third is the vacuity check
//
// Exit 0 and valid UTF-8 are the two halves of the grave. Neither is worth anything on its own,
// because a change that stopped printing the sentence would satisfy both while measuring nothing at
// all — so the sentence's own opening words are asserted **present** in the output, which is what
// establishes that the fixture reached `trunc`.
func TestALongMultiByteSentenceIsNotAnInstrumentFailure(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// `stated in the` puts a discharge verb in front of a locational preposition, which is what routes
	// the sentence into the quoted-back branch; `antechamber` names no artifact in the vocabulary, so
	// the location does not resolve and the sentence is printed rather than checked. One sentence
	// throughout — no `. ` inside it — because the scanner splits on a terminator followed by a space.
	const head = "The finding is stated in the antechamber, and this line is padded to a measured " +
		"length so that the truncation lands inside a character rather than between two of them"
	const tail = " and the tail keeps this one sentence past one hundred and eighty bytes long"

	// The rune is placed so that it *begins* at the last byte the cut keeps, which is the sharpest
	// case: `substr` retains one byte of three. Computed rather than written out, so the assertion
	// below is checking arithmetic and not a hand-typed count.
	const straddle = "—"
	pad := truncCutByte - len(head) - 1
	if pad < 0 {
		t.Fatalf("the fixture head is already %d bytes, past the %d-byte cut, so the padding cannot "+
			"place the multi-byte rune at the boundary and this control would measure nothing",
			len(head), truncCutByte)
	}
	sentence := head + strings.Repeat("x", pad) + straddle + tail

	// The pre-registration. Both halves matter: the rune must span the cut, and the sentence must be
	// long enough for `trunc` to fire at all (its own guard is `> 180`).
	if i := len(head) + pad; i != truncCutByte-1 {
		t.Fatalf("the multi-byte rune starts at byte %d, not %d, so the cut would not split it and "+
			"this fixture no longer exercises the grave", i, truncCutByte-1)
	}
	if got := len(sentence); got <= 180 {
		t.Fatalf("the fixture sentence is %d bytes, so trunc never fires on it and all three arms "+
			"below would pass over an untruncated string", got)
	}
	if utf8.ValidString(sentence[:truncCutByte]) {
		t.Fatalf("cutting the fixture at %d bytes leaves valid UTF-8, so the fixture does not "+
			"reproduce grave #620 and the arms below assert nothing", truncCutByte)
	}

	// The same `gh`-shim mechanism as TestPRFetchFailureIsNeverAPass: a `gh` on PATH that answers the
	// title-and-body fetch. The body cites nothing, so no lookup is attempted and the run needs no
	// network.
	dir := t.TempDir()
	shim := "#!/bin/sh\nprintf '%s\\n' \"A title with no citations.\" \"" + sentence + "\"\n"
	if writeErr := os.WriteFile(filepath.Join(dir, "gh"), []byte(shim), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}
	cmd := exec.Command("sh", filepath.Join(repo, "scripts", "citecheck.sh"), "--pr", "1")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("citecheck.sh could not be run at all (%v), which is not the same fact as a "+
				"non-zero exit and must not be read as one:\n%s", err, out)
		}
		t.Errorf("citecheck.sh --pr exited %d on a body whose only unusual property is a long "+
			"sentence with a multi-byte character at the truncation boundary — grave #620, where a "+
			"malformed message killed a downstream sort. Output:\n%s", ee.ExitCode(), out)
	}

	// Asserted on the raw bytes, because the defect *is* a byte-level one and a string conversion
	// would silently substitute U+FFFD for the fragment this is looking for.
	if !utf8.Valid(raw) {
		t.Errorf("citecheck.sh printed invalid UTF-8 for a body containing a multi-byte character at "+
			"the truncation boundary, so a message it quotes back to a reader is malformed even where "+
			"the sort tolerates it. Output:\n%q", raw)
	}

	// Vacuity: without this, a change that dropped the quoted-back sentence entirely would make both
	// arms above pass while the truncation path went unexercised.
	if !strings.Contains(out, "The finding is stated in the antechamber") {
		t.Errorf("citecheck.sh exited cleanly but never quoted the fixture sentence back, so nothing "+
			"above establishes that the truncation path ran at all. Output:\n%s", out)
	}
}
