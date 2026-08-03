// Package gen holds the machinery two code generators share: reading the vendored
// reference's pinned revision, and formatting emitted source.
//
// # Why this is a package rather than duplicated
//
// Decision 0006's condition, met. Declining to share a structure is legitimate while
// the second consumer does not exist — building it early means shaping it from its only
// consumer's requirements. `opcodegen` was that only consumer, and its pin reader
// carried the comment "one reader, both callers" about its own two call sites.
//
// The second consumer arrived: `keywordgen` reads the same pin from the same script and
// formats the same way. Duplicating `rev="<40 hex>"` into a second regexp would be two
// places knowing one fact with nothing asserting they agree — the drift risk 0006 is
// about, at the scale it was named for. So the sentence stays true one scope out: one
// reader, both generators.
//
// What is *not* here is anything either generator's grammar knows. This package holds
// the two facts that are about the repository — where the pin lives, how generated
// source is formatted — and nothing about OCaml, decoders, or lexers. A shared package
// that grew a `parseArm` would be the wrong seam.
package gen

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
)

// rePin matches the reference pin in scripts/fetch-spec-ref.sh, which is the *one* place
// the revision is declared.
var rePin = regexp.MustCompile(`(?m)^rev="([0-9a-f]{40})"`)

// FromRoot resolves a repo-root-relative path from wherever the caller is running, by
// walking up to the directory holding go.mod.
//
// **This exists because a hardcoded `..` depth is a claim about a package's location, and
// a claim that only a *skip* can falsify.** Every generator test spelled
// `filepath.Join("..", "..", "..", "..", …)` — correct at
// `internal/binary/internal/opcodegen`, wrong by one level the moment the generators were
// promoted to `internal/gen/opcodegen` for 0014's join. The wrong path did not fail: it
// made the vendored reference *look absent*, so `RequireSpecRef` licensed a skip and the
// whole drift check passed by asking nothing. It surfaced only under
// `BURROUGHS_NO_SKIP=1` — *a skip is not a verdict*, earning its keep on the exact defect
// it was written for, one level up from the corpus it was written about.
//
// So the fix is not five corrected literals, which would re-arm the same trap for the next
// move. It is deriving the root instead of enumerating the distance to it — *derive the
// domain, never enumerate it* (0006/#33), where the domain here is "where the repo
// starts". A file at the root is the only landmark that does not move when a package does.
//
// Fails loudly rather than returning a wrong path: a resolver that silently yields
// something plausible-but-wrong reproduces the defect it replaces.
func FromRoot(rel ...string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, rel...)...), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s: cannot resolve repo-root path %s",
				must(os.Getwd()), filepath.Join(rel...))
		}
		dir = parent
	}
}

// must is used only for the error message above, where a second failure of the call that
// already succeeded once has nothing useful to report.
func must(s string, _ error) string { return s }

// PinnedRev reads the reference SHA out of the fetch script.
//
// Asserted, not assumed: a caller that could not find the pin would otherwise stamp an
// empty revision into a generated header, which is 0007 condition 3's failure mode. A
// provenance header that says nothing is worse than none, because it looks stamped.
func PinnedRev(script string) (string, error) {
	b, err := os.ReadFile(script)
	if err != nil {
		return "", err
	}
	m := rePin.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf(`no rev="<40 hex>" pin found in %s`, script)
	}
	return string(m[1]), nil
}

// RefPinScript is where the reference revision is declared, relative to the repo root —
// the *one* place, per the fetch script's own comment.
const RefPinScript = "scripts/fetch-spec-ref.sh"

// PinnedRefRev reads the reference SHA from RefPinScript, resolved from the repo root.
//
// The location-free form of PinnedRev, and every caller that wants "the reference pin"
// rather than "the pin in this named file" should use it: the three existing callers each
// spelled the path with their own `..` depth, which is the claim FromRoot exists to stop
// anyone making. PinnedRev keeps its path parameter because gen_test.go points it at
// temp-file scripts to falsify it — the same split as SuiteDir versus RequireSuite.
func PinnedRefRev() (string, error) {
	script, err := FromRoot(RefPinScript)
	if err != nil {
		return "", err
	}
	return PinnedRev(script)
}

// GofmtSource formats generated source with go/format — gofmt's rules, in process.
//
// Not the repo's formatting gate (gofumpt with extra-rules), and the reason it does not
// need to be is worth recording, because the opposite was assumed for a while and a
// control was written against the assumption.
//
// **The gate never judges these files.** golangci-lint excludes files whose first line
// matches `Code generated ... DO NOT EDIT.`, which every emitted header carries by
// convention. Measured, not deduced: the same badly-formatted file exits 0 with the marker
// and 1 without it. So there is no gofmt-versus-gofumpt divergence to close here — a
// control asserting a committed table is clean under the real formatter was asserting a
// property of an instrument that does not run on it, which is a green that could never
// fail. It was written, falsified by injecting `var   bad    =    1`, found to pass
// anyway, diagnosed, and deleted. The lesson is the deletion: *before controlling a gap,
// check the gap exists* — a control for an impossible failure is indistinguishable on the
// board from one that works.
//
// go/format also keeps this dependency-free and directory-independent: the drift checks
// run from their own packages' directories, where `-modfile=tools/go.mod` does not
// resolve, and gofumpt is not a declared `tool` in tools/go.mod anyway (only an indirect
// dependency), so `go tool gofumpt` fails outright. Adding a tool directive would be a
// toolchain change, which decision 0005 says lands on its own branch.
//
// Each drift check compares formatted against formatted, so a whitespace difference is
// never mistaken for a table difference. That is this function's whole job.
func GofmtSource(src string) (string, error) {
	out, err := format.Source([]byte(src))
	if err != nil {
		return "", fmt.Errorf("formatting generated source: %w", err)
	}
	return string(out), nil
}
