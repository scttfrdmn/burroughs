package mllex

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// lexerSource reads the vendored reference's lexer.mll.
//
// Through `testenv.RequireSpecRef` rather than `os.ReadFile`, because that is the licensed door
// and it carries a registered size floor: *a skip is not a verdict*, and a reference file read
// directly would let an absent or truncated vendor tree produce zero arms and a green board. The
// floor is `MinRefLexerBytes`, asserted before this package ever sees a line.
func lexerSource(t *testing.T) []string {
	t.Helper()
	return strings.Split(testenv.RequireSpecRef(t, testenv.RefLexerMLL), "\n")
}

// TestWrappedArmsAreRecovered is this package's reason to exist, stated as a measurement.
//
// **The wrapped-arm shape has cost three graves** (#78 keywordgen, #105 opgen, and #128 would
// have been the third) and the defect is always the same: a regexp requiring a non-empty body
// after `->` silently drops every arm whose constructor sits on the following line. Silently is
// the operative word — the extraction succeeds, the count is short, and nothing says so. #105 was
// 411 rows where 436 were measured.
//
// So the assertion is not "some arms were found". It is that the arms **whose bodies wrap** are
// present, counted exactly, and that the count is what the authority contains. An exact count
// beside a floor, per the ruling on #108: a floor bounds the catastrophic case and cannot see a
// 6% loss, and here the exact number is knowable, so it is pinned.
func TestWrappedArmsAreRecovered(t *testing.T) {
	lines := lexerSource(t)
	b, err := FindBlock(lines)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	arms, err := Arms(lines, b)
	if err != nil {
		t.Fatalf("Arms: %v", err)
	}

	// Wrapped arms are the ones whose head line carries nothing after `->`, so the body had to
	// come from a following line. Counted by re-reading the head rather than by trusting Arms,
	// because a control that asks the mechanism whether the mechanism worked is a tautology
	// (grave #106).
	wrapped := 0
	for _, a := range arms {
		if m := reArmHead.FindStringSubmatch(lines[a.Line-1]); m != nil && strings.TrimSpace(m[2]) == "" {
			wrapped++
			if a.Body == "" {
				t.Errorf("lexer.mll:%d: arm %q wrapped and its body was not recovered — this "+
					"is #78/#105's defect, which this package exists to make impossible",
					a.Line, a.Keyword)
			}
		}
	}

	// Measured at the pinned revision: 589 arms, 25 of them wrapped. Both pinned exactly
	// because both are knowable; the floor below bounds the catastrophic case that an exact
	// count cannot survive an upstream edit to report.
	//
	// **25, and the two siblings disagreed about it, which is why this is pinned by measurement
	// rather than copied.** `opgen/extract.go` says "the 25 wrapped arms" and is right;
	// `keywordgen/parse.go` said *fourteen*, describing them as "the `v128.load*_splat`/`_lane`
	// family and the five `const` forms" — three families whose members sum to 19, counted as 14,
	// with six `extadd_pairwise`/`trunc_sat_*_zero` arms not mentioned at all. The extraction was
	// never wrong (the forward read has no count in it); the *prose* was, and it read as an
	// authority. Corrected there, and this constant is now the one place the number lives.
	//
	// The families, printed rather than described: 14 at lexer.mll:279-305 (the v128 splat/zero/lane
	// group), 5 at :308-320 (the `const` forms), 6 at :550-566 (SIMD widening pairs).
	const (
		wantWrapped = 25  // measured
		armsFloor   = 400 // measured 589; keywordgen.Floor's value, same authority
	)
	if wrapped != wantWrapped {
		t.Errorf("found %d wrapped arms, want %d — upstream changed how many arms wrap, which "+
			"is a real change to the authority and wants a re-measurement, not a wider bound",
			wrapped, wantWrapped)
	}
	if len(arms) < armsFloor {
		t.Errorf("found %d arms, floor %d — an extraction this small means the block was "+
			"mislocated, not that upstream shrank", len(arms), armsFloor)
	}
	t.Logf("%d arms, %d wrapped, block lines %d..%d", len(arms), wrapped, b.Start, b.Fallthrough)
}

// TestArmHeadStoppingAtArrowIsWhatFindsWrappedArms is the falsification, and it is the one that
// matters: it demonstrates the defect rather than asserting the fix.
//
// A regexp requiring a non-empty body — `->(.+)$` instead of `->(.*)$` — is the exact shape both
// graves had. Run against the same source it must find *fewer* arms, and the difference must be
// the wrapped count. If this test ever reports zero difference, the wrapped arms have gone from
// the authority and the whole package is asserting nothing: that is the vacuity case, and it
// fails here rather than passing quietly.
func TestArmHeadStoppingAtArrowIsWhatFindsWrappedArms(t *testing.T) {
	lines := lexerSource(t)
	b, err := FindBlock(lines)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	arms, err := Arms(lines, b)
	if err != nil {
		t.Fatalf("Arms: %v", err)
	}

	// The defective form, written out here rather than imported, because the point is to run
	// the bug and watch it lose rows.
	defective := regexpMustCompileNonEmptyBody()
	found := 0
	for i := b.Match; i < b.Fallthrough-1; i++ {
		if defective.MatchString(lines[i]) {
			found++
		}
	}
	lost := len(arms) - found
	if lost <= 0 {
		t.Fatalf("the defective regexp found %d arms against %d — it lost nothing, so either "+
			"no arm wraps at this revision or this test is comparing two spellings of the same "+
			"pattern. Either way the package's premise is unasserted",
			found, len(arms))
	}
	t.Logf("body-required regexp finds %d of %d arms: %d lost to wrapping", found, len(arms), lost)
}

// TestBodiesAreWholeNotJustTheirFirstLine pins the run-length distribution of wrapped bodies.
//
// **This test exists because the first draft of `Arms` truncated at one continuation line** and
// nothing would have said so. `keywordgen` reads a token constructor off the body's first line, so
// it is blind to the loss by construction; `opgen` scans the whole body for an instruction
// constructor, so it would have quietly lost rows for the five arms that wrap further. A substrate
// that is lossy for one consumer and not the other is #105's shape with a refactor's alibi, and the
// only reason it is a control rather than a grave is that the run lengths were *printed before the
// wiring*.
//
// The distribution, measured at the pinned revision: 20 arms with one continuation line, 4 with two
// (the scalar `const` forms, lexer.mll:308-317), 1 with four (`v128.const`, :320). Pinned exactly
// and per-partition rather than as a total, because a total absorbs a shift between buckets — 25
// arms and 33 continuation lines can be reached by distributions this test must distinguish.
func TestBodiesAreWholeNotJustTheirFirstLine(t *testing.T) {
	lines := lexerSource(t)
	b, err := FindBlock(lines)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	arms, err := Arms(lines, b)
	if err != nil {
		t.Fatalf("Arms: %v", err)
	}

	// Counted from the *source*, not from Arms' output, so this is not the mechanism vouching
	// for itself (grave #106). Body lines are compared against the run the source actually has.
	hist := map[int]int{}
	for _, a := range arms {
		want := 0
		for j := a.Line; j < b.Fallthrough-1; j++ {
			s := strings.TrimSpace(lines[j])
			if s == "" || strings.HasPrefix(s, "| ") {
				break
			}
			want++
		}
		if want == 0 {
			continue
		}
		hist[want]++
		// The head contributes a body line only when it carried something after `->`.
		head := 0
		if m := reArmHead.FindStringSubmatch(lines[a.Line-1]); m != nil && strings.TrimSpace(m[2]) != "" {
			head = 1
		}
		if got := len(strings.Split(a.Body, "\n")); got != want+head {
			t.Errorf("lexer.mll:%d %q: Arms kept %d body lines, the source has %d — a truncated "+
				"body is a row opgen's constructor scan cannot find and keywordgen cannot miss",
				a.Line, a.Keyword, got, want+head)
		}
	}

	for _, c := range []struct{ run, want int }{{1, 20}, {2, 4}, {4, 1}} {
		if hist[c.run] != c.want {
			t.Errorf("%d arms wrap across %d line(s), want %d — the distribution moved upstream, "+
				"which wants a re-measurement rather than a looser bound", hist[c.run], c.run, c.want)
		}
	}
	t.Logf("continuation-run histogram: %v", hist)
}

// TestAnUnreadableArmIsAnErrorNotASkip pins the property that makes the extraction trustworthy.
//
// A line that looks like an arm and cannot be read must stop the extraction. The alternative —
// skipping it — is how a short table becomes a silent one, and every consumer's floor is too
// coarse to catch one lost row.
func TestAnUnreadableArmIsAnErrorNotASkip(t *testing.T) {
	src := []string{
		`  | keyword as s`,
		`  { match s with`,
		`  | "i32.add" -> BINARY i32_add`,
		`  | "unterminated -> BINARY nope`, // arm-shaped, unreadable: no closing quote
		`  | _ -> unknown lexbuf`,
	}
	b, err := FindBlock(src)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	if _, err := Arms(src, b); !errors.Is(err, ErrUnrecognized) {
		t.Errorf("an arm-shaped unreadable line gave err=%v, want ErrUnrecognized — a skip here "+
			"is how #105 lost 25 rows without a red board", err)
	}
}

// TestAWrappedBodyCannotInheritTheNextArm bounds the rejoining.
//
// The forward read stops at the next arm head, so a genuinely empty arm is an error rather than
// a row carrying its successor's meaning. That distinction is the expensive one: a missing row
// trips a floor, a *wrong* row does not.
func TestAWrappedBodyCannotInheritTheNextArm(t *testing.T) {
	src := []string{
		`  | keyword as s`,
		`  { match s with`,
		`  | "empty" ->`,
		`  | "next" -> BINARY i32_add`,
		`  | _ -> unknown lexbuf`,
	}
	b, err := FindBlock(src)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	arms, err := Arms(src, b)
	if err != nil {
		t.Fatalf("Arms: %v", err)
	}
	for _, a := range arms {
		if a.Keyword == "empty" && a.Body != "" {
			t.Errorf("the empty arm inherited %q from the arm below it — the forward read must "+
				"stop at the next head, or an unreadable arm becomes a wrong row instead of a "+
				"missing one", a.Body)
		}
	}
}

// TestAMissingBlockIsLoud is the vacuity control on the locator.
//
// A moved file or a renamed rule must error rather than yield zero arms: a consumer comparing an
// empty extraction against an empty committed table agrees perfectly, which is the defect class
// every floor in this repo is downstream of.
func TestAMissingBlockIsLoud(t *testing.T) {
	for _, c := range []struct {
		what string
		src  []string
	}{
		{"no block at all", []string{`let foo = 1`, `let bar = 2`}},
		{"head without a fallthrough", []string{`  | keyword as s`, `  { match s with`, `  | "a" -> A`}},
		{"match without the keyword head", []string{`  { match s with`, `  | _ -> unknown lexbuf`}},
	} {
		if _, err := FindBlock(c.src); !errors.Is(err, ErrNoBlock) {
			t.Errorf("%s: err=%v, want ErrNoBlock — a locator that returns zero arms and no "+
				"error is the vacuity defect", c.what, err)
		}
	}
}

// regexpMustCompileNonEmptyBody is the defective arm-head pattern, isolated in a function so the
// test above reads as "run the bug" rather than as a second declaration of the real one.
//
// `->(.+)$` where the real one has `->(.*)$`. One character, three graves.
func regexpMustCompileNonEmptyBody() *regexp.Regexp {
	return regexp.MustCompile(`^\s*\|\s*"((?:[^"\\]|\\.)*)"\s*->(.+)$`)
}
