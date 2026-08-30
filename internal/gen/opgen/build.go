package opgen

import (
	"fmt"
	"os"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/gen/opcodegen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// parserPinSuffix is the subpath every reference pin spells its text parser with, and it is a
// suffix rather than a path for keywordgen.LexerFor's reason: `refPins` already holds every
// licensed path, so a second-place spelling is the claim gen.FromRoot exists to stop anyone
// making. A pin licensing no such file contributes no constructors and is skipped.
const parserPinSuffix = "interpreter/text/parser.mly"

// ParserFor returns the pin's licensed text parser. The lexer's counterpart is
// keywordgen.LexerFor, used rather than re-derived: one suffix, one owner.
func ParserFor(pin testenv.RefPin) (string, bool) {
	for p := range pin.Floors {
		if strings.HasSuffix(p, parserPinSuffix) {
			return p, true
		}
	}
	return "", false
}

// BuildFromPins composes the join over the pin set, in the order refPins declares.
//
// # One function, because a generator and its drift check are one fact
//
// keywordgen.BuildFromPins states it: if the command composes two pins and the control extracts
// one, the control fails against a table that is correct. That already happened once in this
// tree, and the reason it is restated here rather than cited alone is that this generator has
// *three* composed inputs — the opcode table, the keyword table, and its own authorities — so
// there are three ways for a second composition site to disagree with this one.
//
// # The two predecessors are asked for their composed tables, not their base ones
//
// `opcodegen.BuildFromPins` and `keywordgen.BuildFromPins`, not `Extract`. This matters in
// opposite directions for the two: a keyword table missing the threads pin's mnemonics leaves
// 66 rows unjoinable and the *join* silently smaller, while an opcode table missing the 0xfe
// region leaves those same mnemonics resolving to constructors the table does not hold — which
// is ErrUnrecognized, a loud failure. So the quiet half is the keyword side, and it is the half
// the per-authority contribution check in ExtractFrom is aimed at.
func BuildFromPins() (*Table, error) {
	ot, err := opcodegen.BuildFromPins()
	if err != nil {
		return nil, err
	}
	kt, err := keywordgen.BuildFromPins()
	if err != nil {
		return nil, err
	}

	auths, err := pinAuthorities()
	if err != nil {
		return nil, err
	}
	// A floor on the *pin set*, keywordgen's shape one generator over: ExtractFrom's own checks
	// catch an authority that reads nothing and a composition that contributes nothing, but
	// neither can see a pin set that never offered the second authority at all. A `refPins` whose
	// threads entry lost its text licence would join one authority, emit a table 67 rows short,
	// and drift-check clean against a file regenerated the same way.
	if len(auths) < 2 {
		return nil, fmt.Errorf("%w: only %d pin licenses a %s and a lexer, want >=2 (core and "+
			"threads); a join over one authority omits a tracked proposal's constructors and reads "+
			"as complete", ErrVacuous, len(auths), parserPinSuffix)
	}
	return ExtractFrom(auths, KeywordsOf(kt), OpsOf(ot))
}

// pinAuthorities reads each pin's text sources at that pin's own revision, in declaration order.
//
// **Both files or neither, and a pin holding one is an error rather than a skip.** The partition
// check is over a pin's two files together — a kind named by its grammar and a keyword named by
// its lexer are the two halves 0014's premise is about — so a pin whose parser was read and whose
// lexer was not would have its grammar constructors composed against another pin's payloads, and
// the "overlap 0" premise would be checked across revisions. A skip is also how an authority
// joins the tree and contributes nothing while looking consulted (opcodegen's consultedClauses
// makes the same call for the same reason).
func pinAuthorities() ([]Authority, error) {
	var out []Authority
	for _, pin := range testenv.RefPins() {
		mly, hasMLY := ParserFor(pin)
		lex, hasLex := keywordgen.LexerFor(pin)
		switch {
		case !hasMLY && !hasLex:
			continue
		case hasMLY != hasLex:
			return nil, fmt.Errorf("%w: pin %s licenses one of its two text sources (parser=%v, "+
				"lexer=%v); this generator's rows come from both files read at one revision, and "+
				"half a pin would have its constructors joined against another pin's payloads",
				ErrPartition, pin.Dest, hasMLY, hasLex)
		}

		script, err := gen.FromRoot(pin.Script)
		if err != nil {
			return nil, err
		}
		sha, err := gen.PinnedRev(script)
		if err != nil {
			return nil, err
		}
		// Read from the repo root while the paths stay repo-relative for the emitted header and
		// for every row's citation: the generator runs from the root and the drift check from the
		// package directory, and a citation recording whichever spelling the caller used would
		// make one table emit two provenances (keywordgen.extractPin, verbatim reasoning).
		parser, err := readFromRoot(mly, pin.Target)
		if err != nil {
			return nil, err
		}
		lexer, err := readFromRoot(lex, pin.Target)
		if err != nil {
			return nil, err
		}
		out = append(out, Authority{
			ParserPath: mly, LexerPath: lex,
			Parser: parser, Lexer: lexer,
			SHA: sha, Scope: pin.Why,
		})
	}
	return out, nil
}

// readFromRoot reads a repo-relative path, naming the make target that fetches it on failure —
// the corpus-absence message every generator here gives, since a missing pin is machine state
// and the remedy is a fetch rather than a code change.
func readFromRoot(path, target string) (string, error) {
	abs, err := gen.FromRoot(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("%w (run: make %s)", err, target)
	}
	return string(b), nil
}
