package opcodegen

import (
	"fmt"
	"os"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// decodePinSuffix is the subpath every reference pin spells its decoder with. A pin that
// licenses no such file contributes no opcodes and is skipped — which is how a future pin
// whose authority is a lexer or a corpus stays out of this table without naming itself here.
const decodePinSuffix = "interpreter/binary/decode.ml"

// DecodeMLFor returns the pin's licensed decoder, derived from the pin's own file list rather
// than from a path spelled here. Same argument as keywordgen.LexerFor's: `refPins` already
// holds every licensed path, and a second-place spelling is a claim that can drift.
func DecodeMLFor(pin testenv.RefPin) (string, bool) {
	for p := range pin.Floors {
		if strings.HasSuffix(p, decodePinSuffix) {
			return p, true
		}
	}
	return "", false
}

// clause is what one consulted pin is consulted *for*: a single prefix region and the
// citation licensing it.
type clause struct {
	region byte
	cite   string
}

// consultedClauses is the licence, keyed by the pin's `Dest` — one entry per proposal pin
// whose decoder this table reads a region from.
//
// **Keyed by Dest and not by index or name**, because the identity that has to be stable is
// the checkout directory: `refPins` may be reordered or renamed, and both pins license a file
// called `interpreter/binary/decode.ml`, so `Dest` is the only field that already carries the
// job of telling the two decode.ml authorities apart (`refFloors`' construction panics if it
// does not).
//
// The base pin has no entry: it is not consulted for a clause, it *is* the grammar. Every
// other pin licensing a decoder must have one, checked below — a pin arriving with a decoder
// and no declared region is an error rather than a skip, since a skip is how an authority
// joins the tree and contributes nothing while looking consulted.
var consultedClauses = map[string]clause{
	"third_party/spec-threads/": {
		region: 0xfe,
		cite: "testenv.ThreadsRefDecodeML — \"the limits flags' shared bit, the shared-table " +
			"refusal, and the 0xfe region\"; contract §9 G-2",
	},
}

// BuildFromPins composes the opcode table over the pin set, in the order refPins declares.
//
// The first pin is the base and every later one is an overlay consulted for a single clause;
// see Overlay for why the clause is declared rather than discovered, and keywordgen's
// BuildFromPins for why a generator and its drift check must call one function rather than
// each composing the table their own way.
func BuildFromPins() (*Table, error) {
	base, overlays, err := pinSources()
	if err != nil {
		return nil, err
	}
	return Compose(base, overlays...)
}

// pinSources is everything BuildFromPins does except the composition: the base pin's table,
// and one overlay per later pin whose clause licenses a region.
//
// # Why this is a separate function
//
// So that a control can run the *real* overlay set over a mutated base. Two of
// TestVacuityIsCaughtByTheNamedMechanism's cases name a mechanism that only runs on the
// composed table (checkFloorDomain), and BuildFromPins reads its sources from disk — mutating
// a vendored file to exercise a control is a control that edits its own authority. The
// alternative was for the test to rebuild the overlay list itself, which is the shape where a
// control exercises a copy of the path and reports on the path
// ([controls.md](../../../docs/laws/controls.md)): the copy cannot drift into disagreement with
// the original, because there would no longer be an original.
func pinSources() (*Table, []Overlay, error) {
	var (
		base     *Table
		overlays []Overlay
		seen     int
	)
	for _, pin := range testenv.RefPins() {
		path, ok := DecodeMLFor(pin)
		if !ok {
			continue
		}
		tab, sha, err := extractPin(pin, path)
		if err != nil {
			return nil, nil, err
		}
		seen++
		if base == nil {
			base = tab
			continue
		}
		cl, licensed := consultedClauses[pin.Dest]
		if !licensed {
			return nil, nil, fmt.Errorf("%w: pin %s licenses a %s and has no entry in consultedClauses — "+
				"an authority in the tree that contributes nothing reads as one that was consulted; "+
				"declare the region and its citation, or drop the file from the pin's licence",
				ErrCompose, pin.Dest, decodePinSuffix)
		}
		overlays = append(overlays, Overlay{
			Name:   strings.TrimSuffix(strings.TrimPrefix(pin.Dest, "third_party/"), "/"),
			Path:   path,
			SHA:    sha,
			Region: cl.region,
			Clause: cl.cite,
			Table:  tab,
		})
	}

	// A floor on the *pin set*, for keywordgen's reason: Compose catches an overlay that
	// contributes nothing and checkFloors catches an extraction that reads nothing, but
	// neither can see a pin set that never offered the overlay. A `refPins` whose threads
	// entry lost its decoder licence would emit a four-region table and drift-check clean
	// against a file regenerated the same way.
	if seen < 2 {
		return nil, nil, fmt.Errorf("%w: only %d pin licenses a %s, want >=2 (core and threads); a table "+
			"built from one authority omits a tracked proposal's region and reads as complete",
			ErrVacuous, seen, decodePinSuffix)
	}
	return base, overlays, nil
}

// extractPin reads one pin's decoder at that pin's own revision.
//
// The revision comes from the pin's `Script`, which is what gen.go's note about the deleted
// `PinnedThreadsRefRev` prescribed. The path stays repo-relative for the emitted header while
// the read is resolved from the root, so the generator (run from the root) and the drift check
// (run from the package directory) stamp one provenance rather than two.
func extractPin(pin testenv.RefPin, path string) (*Table, string, error) {
	script, err := gen.FromRoot(pin.Script)
	if err != nil {
		return nil, "", err
	}
	sha, err := gen.PinnedRev(script)
	if err != nil {
		return nil, "", err
	}
	abs, err := gen.FromRoot(path)
	if err != nil {
		return nil, "", err
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", fmt.Errorf("%w (run: make %s)", err, pin.Target)
	}
	tab, err := Extract(string(src), sha)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	// Stamped here rather than inside Extract, which is handed a string: the reader is the
	// only party that knows which of the two decode.ml files it opened. Through StampPath, so
	// the arms carry it too — see Arm.Path.
	tab.StampPath(path)
	return tab, sha, nil
}
