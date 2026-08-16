// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"os"
	"strings"
	"testing"
)

// The formatting gate exists twice — `make fmt-check` and ci.yml's gofumpt step — and #322 is the
// fact that both of them read a verdict off `golangci-lint fmt --diff`'s exit code, which says
// *no disagreement* and never *I looked*. A run where the formatter examined nothing exits 0, so
// the gate reports a formatted tree on the strength of having asked no question. That is
// verdict-channel-and-mechanism-channel with the two channels collapsed into one: the exit code is
// the verdict, and nothing anywhere was the mechanism witness.
//
// The fix in both gates is a liveness probe — feed the formatter a source it is known to disagree
// with, and require the disagreement. What the probe must be is the whole subtlety, and it is why
// this test asserts on the probe's *content* rather than merely on its presence:
//
//   - The violation is **gofumpt-only**. A blank line at the top of a block is removed by gofumpt
//     and left alone by plain gofmt, so a probe carrying it proves the pinned formatter is engaged
//     and not merely that some formatter ran. A probe using a gofmt-shared rule (bad indentation,
//     say) would pass under a silent downgrade to gofmt, which is one of the two failures #322 is
//     about.
//   - The comparison is **against the input**, not against emptiness alone. `--diff --stdin` prints
//     the *reformatted source* and exits 0 whether or not it changed anything — measured, not
//     assumed — so emptiness catches only the tool being absent. Unchanged-output is what catches
//     the tool being present and not opinionated.
//
// Both of those were established by running the four mutations (cat, gofmt, false, the pinned tool)
// past the probe and watching the first three trip; a control isn't born until it has been watched
// die. This test is the part of that battery that survives the session.
//
// # Why a text check is the right instrument here, having so often been the wrong one
//
// *Measure with the instrument, not a regex* would ordinarily send this at the gates themselves,
// and for the probe's own correctness it did — the battery above ran the real shell. But the
// residual risk after that battery is not "does the probe work", it is **"do the two copies stay
// the same"**, and two hand-maintained copies of a shell snippet drifting apart is a textual event
// with a textual witness. The Makefile's copy is exercised by every local `make check`; ci.yml's
// copy is exercised only on a runner, which is exactly where a silent divergence would live
// longest. So the property asserted is agreement, and the thing that would otherwise carry it is a
// comment in ci.yml reading "the two must stay mirrors" — an intention, and a design debt is
// discharged by a tripwire, never by an intention.
func TestBothFormatGatesCarryTheLivenessProbe(t *testing.T) {
	// The probe's discriminating source, spelled as it appears inside a `printf` format string in
	// both gates. `\n\nfunc F() {\n\n` is the load-bearing part — the second doubled newline is
	// the blank-line-at-top-of-block that only gofumpt removes. Written as one literal so that a
	// gate which kept the probe but softened it to a gofmt-shared violation fails here.
	const probeSource = `func F() {\n\n`

	// The trip condition, both halves. `-z` catches an absent tool, the `= "$probe"` comparison
	// catches a present but unopinionated one; a gate that kept only the first would pass under a
	// downgrade to gofmt, and the comment above is the argument for why that matters.
	wantTrip := []string{`-z "$got"`, `= "$probe"`}

	for _, g := range []struct {
		path string
		what string
		// esc is how a literal `$` is written in that file's own syntax: doubled in a make
		// recipe, bare in a workflow's `run:` block. Naming it here rather than normalizing the
		// files is deliberate — the two really are different languages, and a check that erased
		// the difference could not tell a correct make recipe from a broken one. The Makefile's
		// copy having originally been written with single `$` (make would have expanded it away
		// to nothing) is not hypothetical.
		esc string
	}{
		{path: "../../Makefile", what: "the fmt-check target", esc: "$$"},
		{path: "../../.github/workflows/ci.yml", what: "the gofumpt step", esc: "$"},
	} {
		b, err := os.ReadFile(g.path)
		if err != nil {
			t.Errorf("%s: %v", g.path, err)
			continue
		}
		src := string(b)

		if !strings.Contains(src, probeSource) {
			t.Errorf("%s: %s does not contain the gofumpt-only probe source %q.\n\tThe gate's exit "+
				"code cannot distinguish a formatted tree from a formatter that never ran (#322), "+
				"and the probe is what supplies that distinction. If the probe was deliberately "+
				"changed, change it in both gates and re-run the four-mutation battery — in "+
				"particular confirm plain gofmt still trips it, which is the mutation a "+
				"gofmt-shared violation would silently start passing",
				g.path, g.what, probeSource)
		}
		for _, want := range wantTrip {
			// Rendered into the file's own escaping so the assertion is about the shell the file
			// actually runs, not about a normalized fiction.
			w := strings.ReplaceAll(want, "$", g.esc)
			if !strings.Contains(src, w) {
				t.Errorf("%s: %s is missing the trip condition %q. Both halves are required: the "+
					"emptiness test catches an absent formatter, the unchanged-output test catches "+
					"a present one that is not gofumpt. A gate holding only one of them reports a "+
					"clean tree under the failure the other half covers", g.path, g.what, w)
			}
		}

		// The vacuity check, and it is not decorative: every assertion above is a `strings.Contains`
		// over a file, so all of them pass trivially against a path that exists and holds something
		// else entirely — a renamed workflow, a Makefile whose fmt-check was deleted wholesale. The
		// floor is on the *subject* rather than on the file: the gate's own invocation has to be
		// present for "the probe is in the gate" to mean anything.
		if !strings.Contains(src, "golangci-lint fmt --diff ./...") {
			t.Errorf("%s: does not invoke `golangci-lint fmt --diff ./...` at all, so the probe "+
				"assertions above are checking a file that no longer contains the gate they are "+
				"about. Either the gate moved — re-point this test at it — or it is gone, which is "+
				"a larger finding than a drifted probe", g.path)
		}
	}
}
