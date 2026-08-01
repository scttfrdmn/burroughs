package gen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two shared facts get their own controls, and they need them for a reason the
// generators' own suites cannot cover: both `opcode-drift` and `keyword-drift` require a
// vendored reference, so on a fresh clone nothing in the tree exercises this package at
// all. It was extracted from `opcodegen` with its callers' tests as its only witnesses,
// and those witnesses are exactly the ones `make check` cannot call.
//
// The failure mode each function's own comment names is the one worth pinning. PinnedRev's
// is a header stamped with an *empty* revision — "worse than none, because it looks
// stamped" — which is 0007 condition 3 failing quietly. GofmtSource's is a formatting
// error surfacing as a table difference in a drift check, sending a reader to regenerate a
// table that was never wrong.

// TestPinnedRevReadsTheRealPin points at the actual script rather than a fixture, because
// the claim is about *this repository's* pin, not about the regexp.
//
// A fixture would test that `rev="<40 hex>"` parses, which is not in doubt. What is in
// doubt is whether the script still declares its revision in the shape this reader
// expects — a reformatting upstream in our own tree, an added `export`, a switch to a
// variable name — and only the real file can answer that.
func TestPinnedRevReadsTheRealPin(t *testing.T) {
	const script = "scripts/fetch-spec-ref.sh"
	rev, err := PinnedRev(filepath.Join("..", "..", script))
	if err != nil {
		t.Fatalf("PinnedRev(%s): %v — the pin is the one place the reference revision is "+
			"declared, and both generators stamp it into their headers", script, err)
	}
	if len(rev) != 40 {
		t.Fatalf("PinnedRev returned %q (%d chars), want a 40-char SHA", rev, len(rev))
	}
	for _, c := range rev {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("PinnedRev returned %q, which is not lowercase hex", rev)
		}
	}
	t.Logf("reference pinned at %s", rev)
}

// TestPinnedRevErrorsRatherThanReturningEmpty is the assertion that matters, and it is an
// assertion about the *error path* precisely because the success path is self-announcing.
//
// A reader that returned "" with a nil error would produce a generated header reading
// `Revision:` followed by nothing, and both drift checks would still pass — they compare a
// fresh extraction against a committed file, and an empty revision on both sides agrees.
// So this failure is invisible to condition 4 and visible only here.
func TestPinnedRevErrorsRatherThanReturningEmpty(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct{ name, body string }{
		{"no pin at all", "#!/bin/sh\ngit clone --depth 1 https://example.invalid/spec\n"},
		{"pin is not 40 hex", "#!/bin/sh\nrev=\"deadbeef\"\n"},
		{"pin is uppercase", "#!/bin/sh\nrev=\"BDD7164BFE18CF0BD5C3D90EF8CC3B8919FB9C0A\"\n"},
		{"pin is not at line start", "#!/bin/sh\nfoo rev=\"bdd7164bfe18cf0bd5c3d90ef8cc3b8919fb9c0a\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "fetch.sh")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			rev, err := PinnedRev(path)
			if err == nil {
				t.Fatalf("PinnedRev returned %q with no error; a generated header stamped with an "+
					"empty revision passes both drift checks, because empty agrees with empty", rev)
			}
			if rev != "" {
				t.Errorf("PinnedRev returned both %q and an error; a caller checking only one gets "+
					"half an answer", rev)
			}
		})
	}
}

// TestPinnedRevReportsAMissingFile separately from a missing pin, because the diagnoses
// differ: a missing script is a wrong working directory, a missing pin is a changed
// script. Same argument as testenv's four doors.
func TestPinnedRevReportsAMissingFile(t *testing.T) {
	_, err := PinnedRev(filepath.Join(t.TempDir(), "does-not-exist.sh"))
	if err == nil {
		t.Fatal("PinnedRev on a missing file returned no error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("PinnedRev on a missing file gave %v, which does not wrap os.ErrNotExist — a "+
			"caller cannot distinguish a wrong directory from a changed script", err)
	}
}

// TestGofmtSourceFormats and its sibling below pin the property the drift checks depend on:
// formatted-versus-formatted comparison. If this function silently returned its input, a
// whitespace difference between the emitter's output and a committed file would be reported
// as a *table* difference, and the failure message would tell a reader to regenerate a
// table whose rows are correct.
func TestGofmtSourceFormats(t *testing.T) {
	got, err := GofmtSource("package x\n\nvar   bad    =    1\n")
	if err != nil {
		t.Fatalf("GofmtSource: %v", err)
	}
	const want = "package x\n\nvar bad = 1\n"
	if got != want {
		t.Errorf("GofmtSource(%q) = %q, want %q — a pass-through would make every drift check "+
			"report a whitespace difference as a table difference", "var   bad    =    1", got, want)
	}
}

// TestGofmtSourceRejectsUnparseableSource: a generator that emitted a syntax error would
// otherwise write an uncompilable file, and the failure would arrive at the *next* build
// rather than at the generation. Emit's own bug reported as the tree's.
func TestGofmtSourceRejectsUnparseableSource(t *testing.T) {
	out, err := GofmtSource("package x\n\nvar keywords = map[string]{\n")
	if err == nil {
		t.Fatalf("GofmtSource accepted unparseable source and returned %q", out)
	}
	if out != "" {
		t.Errorf("GofmtSource returned both %q and an error", out)
	}
	if !strings.Contains(err.Error(), "formatting generated source") {
		t.Errorf("GofmtSource error %q does not name what failed; a bare parser error reads as a "+
			"problem with the tree rather than with the emitter", err)
	}
}

// TestGofmtSourceIsIdempotent, because both drift checks format twice in effect — the
// committed file was formatted at generation, and the comparison formats a fresh emission.
// A non-idempotent formatter would make a table drift on the second regeneration with no
// change to its rows or its authority, which is a failure that looks exactly like real
// drift.
func TestGofmtSourceIsIdempotent(t *testing.T) {
	once, err := GofmtSource("package x\n\nvar   bad    =    1\n")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := GofmtSource(once)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Errorf("GofmtSource is not idempotent: %q then %q", once, twice)
	}
}
