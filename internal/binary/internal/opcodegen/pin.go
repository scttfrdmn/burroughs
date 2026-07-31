package opcodegen

import (
	"fmt"
	"go/format"
	"os"
	"regexp"
)

// rePin matches the reference pin in scripts/fetch-spec-ref.sh, which is the *one* place
// the revision is declared.
var rePin = regexp.MustCompile(`(?m)^rev="([0-9a-f]{40})"`)

// pinnedRevFromScript reads the reference SHA out of the fetch script.
//
// It lives here rather than in the generator's main because the drift check needs it
// too, and a SHA parsed at two sites is two places knowing the same fact — the drift
// risk 0006/#33 is about, in miniature. One reader, both callers.
//
// Asserted, not assumed: a caller that could not find the pin would otherwise stamp an
// empty revision into the generated header, which is condition 3's failure mode. A
// provenance header that says nothing is worse than none, because it looks stamped.
func pinnedRevFromScript(script string) (string, error) {
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

// PinnedRev is pinnedRevFromScript for the generator command, which is a separate
// package.
func PinnedRev(script string) (string, error) { return pinnedRevFromScript(script) }

// gofmtSource formats generated source with go/format — gofmt's rules, in process.
//
// Not the repo's formatting gate (gofumpt with extra-rules), and the reason it does not
// need to be is worth recording, because the opposite was assumed for a while and a
// control was written against the assumption.
//
// **The gate never judges this file.** golangci-lint excludes files whose first line
// matches `Code generated ... DO NOT EDIT.`, which the emitted header carries by
// convention. Measured, not deduced: the same badly-formatted file exits 0 with the marker
// and 1 without it. So there is no gofmt-versus-gofumpt divergence to close here — a
// control asserting the committed table is clean under the real formatter was asserting a
// property of an instrument that does not run on it, which is a green that could never
// fail. It was written, falsified by injecting `var   bad    =    1`, found to pass
// anyway, diagnosed, and deleted. The lesson is the deletion: *before controlling a gap,
// check the gap exists* — a control for an impossible failure is indistinguishable on the
// board from one that works.
//
// go/format also keeps this dependency-free and directory-independent: the drift check
// runs from this package's directory, where `-modfile=tools/go.mod` does not resolve, and
// gofumpt is not a declared `tool` in tools/go.mod anyway (only an indirect dependency),
// so `go tool gofumpt` fails outright. Adding a tool directive would be a toolchain
// change, which decision 0005 says lands on its own branch.
//
// The drift check compares formatted against formatted, so a whitespace difference is
// never mistaken for a table difference. That is this function's whole job.
func gofmtSource(src string) (string, error) {
	out, err := format.Source([]byte(src))
	if err != nil {
		return "", fmt.Errorf("formatting generated source: %w", err)
	}
	return string(out), nil
}

// GofmtSource is gofmtSource for the generator command.
func GofmtSource(src string) (string, error) { return gofmtSource(src) }
