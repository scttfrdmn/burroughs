package memarggen

import (
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// BuildFromPins composes the table over the pin set, in the order refPins declares.
//
// # One function, because a generator and its drift check are one fact
//
// keywordgen.BuildFromPins states it: if the command composes two pins and the control extracts
// one, the control fails against a table that is correct. That already happened once in this tree.
// A generator and its drift check are one fact about how the artifact is made, so they call one
// function, and this is the only place that knows the table is composed at all.
//
// # The lexer path is keywordgen.LexerFor's, not a second spelling
//
// One suffix, one owner. `refPins` already holds every licensed path, and a pin licensing no text
// lexer contributes no alignments and is skipped — which is how a future pin whose authority is a
// decoder or a corpus stays out of this table without naming itself here.
func BuildFromPins() (*Table, error) {
	auths, err := pinAuthorities()
	if err != nil {
		return nil, err
	}
	return ExtractFrom(auths)
}

// pinAuthorities reads each pin's lexer at that pin's own revision, in declaration order.
//
// Split from BuildFromPins for the reason composeRows is split from ExtractFrom: a control cannot
// measure a count through a gate that refuses on that count, and
// TestEveryFloorIsBelowItsMeasuredCount needs the composed rows with the floors *not* applied.
func pinAuthorities() ([]Authority, error) {
	var auths []Authority
	for _, pin := range testenv.RefPins() {
		path, ok := keywordgen.LexerFor(pin)
		if !ok {
			continue
		}
		script, err := gen.FromRoot(pin.Script)
		if err != nil {
			return nil, err
		}
		sha, err := gen.PinnedRev(script)
		if err != nil {
			return nil, err
		}
		// Resolved from the repo root for the *read* while `path` stays repo-relative for the
		// emitted header and for every row's citation: the generator runs from the root and the
		// drift check from the package directory, and a citation recording whichever spelling the
		// caller used would make one table emit two provenances (keywordgen.extractPin, verbatim).
		abs, err := gen.FromRoot(path)
		if err != nil {
			return nil, err
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("%w (run: make %s)", err, pin.Target)
		}
		auths = append(auths, Authority{
			LexerPath: path, Lexer: string(src), SHA: sha, Scope: pin.Why,
		})
	}

	// A floor on the *pin set*, not on the table: checkContributions catches an authority that
	// contributes nothing and checkFloors catches an extraction that reads nothing, but neither
	// can see a pin set that never offered the overlay in the first place. A `refPins` whose
	// threads entry lost its lexer licence would compose one authority, emit a table 66 rows
	// short, and drift-check clean against a file regenerated the same way.
	if len(auths) < 2 {
		return nil, fmt.Errorf("%w: only %d pin licenses a text lexer, want >=2 (core and threads); "+
			"a table built from one authority omits a tracked proposal's alignments and reads as "+
			"complete", ErrVacuous, len(auths))
	}
	return auths, nil
}
