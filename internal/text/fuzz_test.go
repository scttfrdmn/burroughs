package text

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// suiteDir is where `make spec-tests` vendors the upstream suite, relative to this
// package. Gitignored — never committed (upstream corpora stay vendored).
const suiteDir = "../../testdata/spec"

// FuzzLexerProgress fuzzes `Lexer.Next` to exhaustion over arbitrary bytes.
//
// It is named for the property grave #18 named — *a parser proves progress, it does not
// assume it* — and the first thing to say is that **every assertion in the harness below is
// currently unreachable, and each one was measured rather than reasoned about.** What this
// target finds, it finds as a *panic from the lexer*. That is a smaller and differently
// shaped claim than the name suggests, and three drafts of this comment were wrong about it
// before a probe printed the numbers.
//
// # Why the progress assertion cannot fire
//
// `Next` takes the longest match over `arms`. The final arm matches any single byte, so
// `best >= 1` for every non-empty input — printed for four representative bytes, the winner
// is `control` (0x00), `reserved` (0x61), `space` (0x20), `any` (0xff), each at length 1. So
// `after <= before` cannot fire, and neither can `best <= 0`'s panic, while the catch-all
// exists.
//
// The first draft claimed a *single* zero-length arm was masked but the total case was
// reachable. Both halves were wrong: the total case needs the catch-all gone, which is an
// edit, not an input.
//
// # Why the bounds assertion cannot fire either
//
// This is the finding that reshaped the target. An arm reporting more than it read used to
// produce `slice bounds out of range [:10] with capacity 5` **inside `Next`**, at the slice
// — so a harness checking `after > len(src)` on the next iteration never ran, and the only
// witness to the defect was a runtime message naming Next's line number and none of the two
// dozen arms that could have lied. The check moved into `Next` itself, where it can name the
// arm; see the panic at lexer.go's `best > len(rest)`. *An error from the wrong layer is
// evidence about where structure was lost*, and here the wrong layer was holding the whole
// diagnosis.
//
// So the assertions are kept as **tripwires for their own preconditions**, not as live
// checks — declared-and-tracked unreachability named at its definition site (the
// `ErrTrailingData` ruling, #6). If `after <= before` ever fires, the catch-all has changed;
// if `after > len(src)` ever fires, the lexer's own bounds panic has been removed. Both are
// edits a reviewer should be told about by a failing test rather than by nobody.
//
// # What the target actually finds
//
// Panics, from the two guards in `Next` and from anything a `match*` function does to a
// short buffer. `scanBlockComment` and `scanAnnotBody` compute lengths over nested
// structure; `decodeUnicodeEscape` and `decodeString` index into escape bodies. None of that
// is exercised past what a corpus happens to contain, which is the gap a fuzzer exists to
// fill.
//
// # The two halves are certified separately (#28)
//
// Seed-replay: TestLexerCrashClassesAreFalsifiable reintroduces the over-long-length defect
// and confirms it is caught, with the arm named. Exploration: the suite's raw bytes were
// measured — every byte in 0x00–0xf4 appears somewhere, and **0xf5–0xff appears nowhere at
// all** — so the `utf8enc` and catch-all arms' ill-formed lead bytes are reachable by
// mutation only. A run passing on seeds alone would have certified a corpus.
func FuzzLexerProgress(f *testing.F) {
	seedLexerCorpus(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		l := NewLexer(src)
		for {
			before := l.Offset()
			tok, err := l.Next()
			after := l.Offset()

			if err != nil {
				// A rejection is terminal for the caller, and the offset need not have
				// moved past the span the arm read. Nothing to assert beyond here.
				return
			}
			// Both remaining assertions are unreachable by construction; see this
			// function's comment for what each one's firing would mean.
			if after > len(src) {
				t.Fatalf("offset %d past end of %d-byte input (token %v) in %q",
					after, len(src), tok.Kind, src)
			}
			if tok.Kind == EOF {
				// `after`, not `before`. `Next`'s skip path — whitespace, comments,
				// annotations — consumes bytes and loops without returning, so on `";;"`
				// the comment is skipped to offset 2 and EOF is returned from there while
				// `before` is still 0. The first draft compared `before` and the fuzzer
				// failed on seed #20 in its first run: *the harness's own defect, found by
				// the harness*, which is the most useful thing a control can do on its
				// first outing. It also means this assertion has fired exactly once, for a
				// reason that was never about the lexer.
				if after < len(src) {
					t.Fatalf("EOF at offset %d with %d bytes remaining in %q",
						after, len(src)-after, src)
				}
				return
			}
			if after <= before {
				t.Fatalf("Next made no progress at offset %d (token %v, text %q) in %q",
					after, tok.Kind, tok.Text, src)
			}
		}
	})
}

// seedLexerCorpus seeds from the suite at run time — no transcription step, no drift.
//
// The literal seeds are not redundant with the suite files, and the reason is a
// measurement: every byte in 0x00–0xf4 appears somewhere in the vendored `.wast` corpus,
// and 0xf5–0xff appears nowhere. So the arms taking an ill-formed UTF-8 lead byte are
// unreachable from seeds, and the literals below name a few explicitly so the *replay*
// half covers them. The *exploration* claim is the complement: those bytes are absent from
// the seeds, so reaching them is evidence the fuzzer did something.
func seedLexerCorpus(f *testing.F) {
	f.Helper()

	// Boundary seeds first, so a fresh clone with no suite still exercises every arm's
	// edge. Deliberately tiny: a one- or two-byte input is where a length defect shows up
	// unambiguously, with nothing else in the buffer to hide behind.
	for _, s := range []string{
		"", "(", ")", "()", "$", "$x", `$""`, `$"\ef"`,
		"nop", "i32.const", "get_local", "i32.wrap/i64", "offset=0", "align=4",
		`"`, `"\`, `"\q"`, "\"\x00\"", "\"\x7f\"",
		";", ";;", ";; no newline", "(;", "(; (;", "(; (; half closed ;)", "(;;)",
		"(@", "(@)", `(@"")`, "(@a", "(@a )", "(@a (; ;))", `(@a "`,
		"\n", "\r", "\r\n", "\n\r", " \t",
		"0", "0x", "0x7f", "-0", "+0", "1e", "1.", "inf", "nan", "nan:0x1",
		// The arms no seed file can reach, per the measurement above.
		"\xf5", "\xff", "\xc0\x80", "\xed\xa0\x80", "\x80",
	} {
		f.Add([]byte(s))
	}

	paths := testenv.SuiteFiles(f, suiteDir)
	if len(paths) == 0 {
		// Licensed by SuiteFiles, which fails under BURROUGHS_NO_SKIP=1 rather than
		// degrading quietly. The boundary seeds are why a seedless run is weaker and not
		// broken — but "weaker" is not a thing CI may inherit silently.
		return
	}

	// Chosen for the arms they exercise, not for size; the fuzzer shrinks them.
	want := map[string]bool{
		"annotations.wast":            true, // grave #18's origin, and the annot scanner's arms
		"comments.wast":               true, // nested block comments, the closedness grave
		"names.wast":                  true, // exotic identifiers, escapes, `$"..."`
		"obsolete-keywords.wast":      true, // the eleven unknown-operator vectors
		"token.wast":                  true, // the lexeme boundaries themselves
		"id.wast":                     true,
		"utf8-custom-section-id.wast": true,
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
	// Every named file, not a floor — and that distinction was earned in the first draft,
	// which asked for `n >= 5` out of seven names and would have passed at six while
	// silently missing `token.wast`, which the first draft called `tokens.wast` and which
	// does not exist. A floor below the list's own length is decoration: it cannot
	// distinguish "the suite moved" from "I mistyped a name", which are the two things it
	// was written to catch. An exact count can.
	if n != len(want) {
		f.Fatalf("seeded %d of %d named suite files; a name has drifted or the suite moved, and "+
			"the corpus is now boundary literals wearing a suite's name", n, len(want))
	}
	f.Logf("seeded %d suite files plus boundary literals", n)
}

// TestLexerCrashClassesAreFalsifiable is the seed-replay half's certification: it
// reintroduces the defect FuzzLexerProgress can actually catch and confirms the harness
// fails on it.
//
// *A green that survives the bug it names is a control in name only*, and a fuzz target is
// unusually exposed to that — it passes loudly on every input where nothing is wrong. Run
// as an ordinary test so it executes in `make check` on every commit rather than once in a
// session.
//
// Two mutants, and both of them are findings about the *harness* rather than about the
// lexer:
//
//  1. an arm reporting more than it read. Caught — but by `Next`'s own bounds panic, not by
//     the harness. The panic is where the diagnosis had to move, because the runtime's
//     slice-bounds error fires first and names no arm. So what this half certifies is that
//     the panic exists and identifies the culprit.
//  2. a single arm reporting zero. **Not caught, and not catchable** — the catch-all arm
//     matches every byte at length 1, so a zero-length arm never wins the longest-match
//     comparison and the scan is byte-for-byte unchanged. Recorded rather than papered
//     over: the alternative is a comment claiming coverage the mechanism does not have.
//     This is where the progress assertion's unreachability is *asserted* instead of
//     described — if this half starts failing, the catch-all has changed and
//     FuzzLexerProgress's assertion has become load-bearing.
//
// Certifying the exploration half cannot be done in a unit test by construction — the
// claim is about inputs no seed reaches — so it rests on the byte measurement in
// seedLexerCorpus and on the target being budgeted in CI and the Makefile.
func TestLexerCrashClassesAreFalsifiable(t *testing.T) {
	saved := arms
	t.Cleanup(func() { arms = saved })

	// Mutant 1: an arm claiming more than it read.
	arms = make([]arm, len(saved))
	copy(arms, saved)
	for i := range arms {
		if arms[i].name == "space" {
			arms[i].length = func(_ *Lexer, b []byte) int {
				if len(b) > 0 && b[0] == ' ' {
					return len(b) + 5
				}
				return -1
			}
		}
	}
	msg := panicMessage(func() { _, _ = LexAll([]byte("  nop")) })
	switch {
	case msg == "":
		t.Error("an arm reporting a length past the end of the input did not panic; " +
			"lexer.go's `best > len(rest)` guard is what stands between a lying arm and a " +
			"bare slice-bounds error, and it just failed to stand")
	case !strings.Contains(msg, `arm "space"`):
		// The whole reason the check lives in `Next`: a diagnosis that does not name the
		// arm sends a reader through two dozen `match*` functions. *An error message is
		// testimony* — this asserts the testimony identifies the witness.
		t.Errorf("bounds panic does not name the offending arm: %q", msg)
	}

	// Mutant 2: an arm reporting zero for the byte it owns. What this half certifies is the
	// *shape* of the miss, and the shape took two measurements to get right.
	//
	// The claim was "the scan is unchanged, because a zero-length arm loses every
	// comparison". Half true: it does lose, but what wins is the catch-all, which
	// *rejects* — `malformed UTF-8 encoding` on a leading space. So the verdict changes
	// completely while the progress predicate stays silent, which is a sharper statement of
	// the gap than "unchanged" was: the defect is not invisible, it is invisible *to this
	// target*. A vector-shaped test sees it instantly; the fuzz property cannot see it at
	// all.
	//
	// Recorded rather than repaired, because repairing it means asserting token streams,
	// and that is the board's job (#53, PR B) over the suite rather than this target's over
	// random bytes.
	arms = make([]arm, len(saved))
	copy(arms, saved)
	for i := range arms {
		if arms[i].name == "space" {
			arms[i].length = func(_ *Lexer, b []byte) int {
				if len(b) > 0 {
					return 0
				}
				return -1
			}
		}
	}
	// A leading space, so the mutated `space` arm is actually consulted, and enough real
	// tokens for the count comparison to be non-vacuous. Printed rather than assumed: this
	// input lexes to 5 tokens clean — the annotation and the comment produce none, which is
	// why an earlier probe of the same shape yielded 2 and tripped the floor below.
	const probe = " ( nop 1 2 ) ;; c\n(@a)"
	gotOffs, gotErr := scanOffsets([]byte(probe))
	arms = saved
	wantOffs, wantErr := scanOffsets([]byte(probe))

	// The fuzz property's verdict on the mutant: silence. Asserted, because it is the
	// reason this half exists — if the harness ever starts catching this, the catch-all has
	// changed and FuzzLexerProgress's progress assertion has become load-bearing.
	arms = make([]arm, len(saved))
	copy(arms, saved)
	for i := range arms {
		if arms[i].name == "space" {
			arms[i].length = func(_ *Lexer, b []byte) int {
				if len(b) > 0 {
					return 0
				}
				return -1
			}
		}
	}
	if msg := panicMessage(func() { _, _ = LexAll([]byte(probe)) }); msg != "" {
		t.Errorf("zero-length arm panicked (%q) — it was measured as silently masked by the "+
			"catch-all, so the masking analysis in this test's comment is now wrong", msg)
	}
	arms = saved

	// And the observable that *does* move, which is what makes the gap a measurement rather
	// than a guess. Compared against a real second scan rather than a written-down expected
	// value, so the two cannot drift.
	if gotErr == wantErr && len(gotOffs) == len(wantOffs) {
		t.Errorf("zero-length `space` arm changed nothing at all (err %q, %d offsets both "+
			"ways); the recorded finding is that the catch-all wins and rejects, so if the "+
			"scan is genuinely identical this test is describing a mechanism that is gone",
			wantErr, len(wantOffs))
	}
	// A vacuity floor on the baseline: two empty scans differ about nothing, and an input
	// erroring on its first byte would produce exactly that.
	if len(wantOffs) < 4 {
		t.Fatalf("baseline scan produced only %d offsets on %q; this comparison is agreeing "+
			"about almost nothing", len(wantOffs), probe)
	}
	if wantErr != "" {
		t.Fatalf("baseline scan of %q errored (%q); the probe must lex clean or the "+
			"comparison above is between two rejections", probe, wantErr)
	}
	t.Logf("zero-length arm: invisible to the progress property, observable as %q "+
		"(%d offsets vs %d clean)", gotErr, len(gotOffs), len(wantOffs))
}

// panicMessage runs f and returns its panic value as a string, or "" if it did not panic.
//
// The message is *returned* rather than matched here so the caller asserts on its content:
// a guard that panics with the wrong text is a guard whose report sends the reader to the
// wrong place, and "did it panic" cannot see that.
func panicMessage(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// scanOffsets lexes to completion and returns the offset after each token, plus the error
// text if any. Two scans of the same bytes under different `arms` must agree, which is a
// stronger and more honest statement than any single expected offset.
func scanOffsets(src []byte) (offs []int, errMsg string) {
	l := NewLexer(src)
	for {
		tok, err := l.Next()
		if err != nil {
			return offs, err.Error()
		}
		if tok.Kind == EOF {
			return offs, ""
		}
		offs = append(offs, l.Offset())
	}
}
