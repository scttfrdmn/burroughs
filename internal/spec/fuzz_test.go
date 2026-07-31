package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// FuzzWastLexer fuzzes the s-expression reader. This is grave #18's home turf:
// the reader failed on `annotations.wast` because its atom loop could consume
// zero bytes and then error on the very delimiter that stopped it — the loop's
// exit condition and its error condition were the same predicate.
//
// So the invariant asserted here is **progress**, not just absence of panic.
// A parser should prove progress rather than assume it: every call to parseNode
// that succeeds must advance the offset, and parseAll must terminate. The first
// is checked directly; the second is what the fuzzer's timeout enforces.
func FuzzWastLexer(f *testing.F) {
	seedFromSuiteText(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		p := newParser(src)

		// parseAll must either return a node list or an error, and must not hang
		// or panic on any input. Go's fuzzer catches the hang as a timeout.
		nodes, err := p.parseAll()
		if err != nil {
			return
		}

		// On success the whole input is consumed — a successful parse that left
		// bytes on the table would mean parseAll returned early without saying so.
		if p.off < len(src) {
			t.Fatalf("parseAll succeeded with %d of %d bytes unconsumed", len(src)-p.off, len(src))
		}

		// Every node must be well-formed in the one way the type permits being
		// asked: a list node's children are reachable, and a string node's bytes
		// are non-nil even when empty (isS distinguishes "" from an absent value).
		var walk func(n node)
		walk = func(n node) {
			if n.isS && n.str == nil {
				t.Fatalf("string node with nil bytes at line %d", n.line)
			}
			if n.isList() && !n.isS && n.atom != "" {
				t.Fatalf("node is both a list and an atom (%q) at line %d", n.atom, n.line)
			}
			for _, c := range n.list {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	})
}

// FuzzParseNodeProgress isolates the progress property that grave #18 violated.
// Separate from FuzzWastLexer because a targeted target finds a targeted bug
// faster: this one drives parseNode directly and asserts the offset moves.
func FuzzParseNodeProgress(f *testing.F) {
	for _, s := range []string{
		`(module binary "\00asm")`,
		`;`,                                 // grave #18: a lone semicolon
		`(@a , ; ] [ }} }x{ ({) ,{{};}] ;)`, // annotations.wast:14, verbatim
		`(; unterminated block`,
		`;; line comment with no newline`,
		`"\ff"`,
		`()`,
		`(((())))`,
		`$name`,
		`0x7f`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		p := newParser(src)
		for {
			before := p.off
			p.skipSpace()
			if p.off >= len(p.src) {
				return
			}
			afterSpace := p.off

			if _, err := p.parseNode(); err != nil {
				return
			}
			// The core claim: a successful parseNode consumed at least one byte
			// beyond whatever skipSpace ate. Without this, a zero-progress node
			// turns the caller's loop into an infinite one — the failure mode
			// grave #18 produced as an error only because ';' happened to be a
			// delimiter. A different byte class could have hung instead.
			if p.off <= afterSpace {
				t.Fatalf("parseNode made no progress at offset %d (was %d) in %q", p.off, before, src)
			}
		}
	})
}

// seedFromSuiteText seeds the lexer corpus with the suite's own text. These are
// the inputs the reader must survive by contract (TestParseEverySuiteFile), so
// they are the right starting point for mutation — and unlike a hand-written
// corpus, they cost nothing to keep current.
//
// License for running without the corpus: a seedless fuzz target is weaker, not
// broken, so a fresh clone can still fuzz. That degradation is quieter than a skip
// — the target passes and only an f.Log says why — which is exactly why
// BURROUGHS_NO_SKIP=1 turns it into a failure.
func seedFromSuiteText(f *testing.F) {
	f.Helper()

	paths := testenv.SuiteFiles(f, suiteDir)
	if len(paths) == 0 {
		f.Log("spec suite not vendored; fuzzing with literal seeds only (run: make spec-tests)")
		f.Add([]byte(`(module binary "\00asm\01\00\00\00")`))
		f.Add([]byte(`(@a , ; ] [ }} }x{ ({) ,{{};}] ;)`))
		return
	}

	// Whole .wast files are large seeds; the fuzzer prefers small ones, so seed
	// the awkward files whole and let mutation shrink them.
	want := map[string]bool{
		"annotations.wast":            true, // grave #18's origin
		"binary.wast":                 true,
		"comments.wast":               true, // nested block comments
		"utf8-custom-section-id.wast": true,
		"names.wast":                  true, // exotic identifiers and escapes
	}
	var n int
	for _, p := range paths {
		if !want[filepath.Base(p)] {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		f.Add(src)
		n++
	}
	f.Logf("seeded %d suite files into the lexer corpus", n)
}
