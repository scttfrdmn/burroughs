package keywordgen

import (
	"fmt"
	"os"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// lexerPinSuffix is the subpath every reference pin spells its text lexer with. A pin that
// licenses no such file contributes no keywords and is skipped — which is how a future pin
// whose authority is a decoder or a corpus stays out of this table without naming itself here.
const lexerPinSuffix = "interpreter/text/lexer.mll"

// LexerFor returns the pin's licensed text lexer, derived from the pin's own file list rather
// than from a path spelled here. `refPins` already holds every licensed path, and a
// second-place spelling is the claim gen.FromRoot exists to stop anyone making.
func LexerFor(pin testenv.RefPin) (string, bool) {
	for p := range pin.Floors {
		if strings.HasSuffix(p, lexerPinSuffix) {
			return p, true
		}
	}
	return "", false
}

// BuildFromPins composes the keyword table over the pin set, in the order refPins declares.
//
// # Why this is a function and not a loop in the command
//
// The drift control has to build the table the same way the generator does, or it compares
// the committed file against a differently-composed one and reports drift that is really a
// disagreement between two copies of the composition. That is what happened on the first
// draft: the command composed two pins, the control extracted one, and the control failed
// against a table that was correct. A generator and its drift check are *one* fact about how
// the artifact is made, so they call one function.
//
// # The order is the composition order and the first pin is the base
//
// That is a property of the declared list rather than of this loop: `RefPins` returns a fresh
// slice precisely so a caller cannot reorder the package's own list, and the core pin is
// declared first because every other pin is a proposal whose baseline predates part of core.
// Composing in the other direction deletes core clauses rather than adding proposal ones —
// see Compose for the measurements.
func BuildFromPins() (*Table, error) {
	var (
		tab  *Table
		seen int
	)
	for _, pin := range testenv.RefPins() {
		path, ok := LexerFor(pin)
		if !ok {
			continue
		}
		next, meta, err := extractPin(pin, path)
		if err != nil {
			return nil, err
		}
		seen++
		if tab == nil {
			tab = next.WithSource(meta)
			continue
		}
		if tab, err = Compose(tab, next, meta); err != nil {
			return nil, err
		}
	}

	// A floor on the *pin set*, not on the table: Compose's own vacuity check catches an
	// overlay that contributes nothing, and checkFloor catches an extraction that reads
	// nothing, but neither can see a pin set that never offered the overlay in the first
	// place. A `refPins` whose threads entry lost its lexer licence would compose one
	// authority, emit a table 70 keywords short, and drift-check clean against a file
	// regenerated the same way — the shape this package's Floor doc is about, one level up.
	if seen < 2 {
		return nil, fmt.Errorf("%w: only %d pin licenses a %s, want >=2 (core and threads); a "+
			"table built from one authority omits a tracked proposal's grammar and reads as "+
			"complete", ErrVacuous, seen, lexerPinSuffix)
	}
	return tab, nil
}

// extractPin reads one pin's lexer at that pin's own revision.
//
// The revision comes from the pin's `Script`, which is what gen.go's note about the deleted
// `PinnedThreadsRefRev` prescribed: *a generator that needs the threads revision reads its
// pin's own Script and passes it to PinnedRev, which is why that function keeps a path
// parameter.* This is that caller.
func extractPin(pin testenv.RefPin, path string) (*Table, Source, error) {
	script, err := gen.FromRoot(pin.Script)
	if err != nil {
		return nil, Source{}, err
	}
	sha, err := gen.PinnedRev(script)
	if err != nil {
		return nil, Source{}, err
	}
	// Resolved from the repo root for the *read* while `path` stays repo-relative for the
	// emitted header: the generator runs from the root and the drift check runs from the
	// package directory, and a header that recorded whichever spelling the caller happened
	// to use would make the same table emit two different provenances.
	abs, err := gen.FromRoot(path)
	if err != nil {
		return nil, Source{}, err
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, Source{}, fmt.Errorf("%w (run: make %s)", err, pin.Target)
	}
	tab, err := Extract(string(src), sha)
	if err != nil {
		return nil, Source{}, fmt.Errorf("%s: %w", path, err)
	}
	return tab, Source{Path: path, SHA: sha, Scope: pin.Why}, nil
}
