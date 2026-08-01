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
	"regexp"
)

// rePin matches the reference pin in scripts/fetch-spec-ref.sh, which is the *one* place
// the revision is declared.
var rePin = regexp.MustCompile(`(?m)^rev="([0-9a-f]{40})"`)

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
