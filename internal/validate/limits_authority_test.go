// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestLimitsRangesMatchTheReference pins the four `check_limits` ranges against `valid.ml`, and it
// exists because **the corpus cannot pin two of them in either direction.**
//
// # The measurement that made this test necessary
//
// The i64 rows were transcribed from the reference and then, on the strength of a +7 all-on-lane
// asymmetry, described as having a live subject. Falsified: collapsing `memRangeI64` onto
// `memRangeI32` and `tabRangeI64` onto `tabRangeI32` — the exact "correct by coincidence of scope"
// bug #310 records for addrTypeAt — moved **nothing**. Both lanes stayed at 60786 and 64676, and
// `memory64.wast` stayed 68/68. The +7 was real and was something else: five memory64 and two
// table64 vectors that are gate-blocked in the default lane for reasons unrelated to the address
// type.
//
// The arms are *reached* — making them refuse unconditionally costs the all-on lane 284 passes — so
// this is not dead code and `deadcode` would never have flagged it. What is unfalsifiable is the
// **value**. Shrinking a range can only wrongly reject a *valid* module, and no valid vector in the
// corpus declares an i64 memory above 2^16 pages; enlarging it can only wrongly accept an invalid
// one, and no vector declares an i64 limit between the two candidate bounds. So every value from
// "the largest valid i64 limit in the corpus" to "the smallest invalid one minus one" passes the
// entire suite, and the reference is the only thing that can say which is right.
//
// That is *authority for accept-direction facts* with the sharpest edge it has had here: not a rule
// the corpus samples thinly, but a constant it does not constrain at all. A number in that position
// is only ever as good as its citation, so the citation is machine-checked.
//
// # What is checked
//
// All three things each caller hardcodes, because they are one fact split three ways and a drift
// check on the number alone would let the *text* rot while agreeing:
//
//  1. the range value, per address type;
//  2. the range text, which the message ends with;
//  3. the message head the sentinel carries.
//
// Both directions, as authority_test.go's siblings do: every row the reference has must be claimed
// here, and every row claimed here must be the reference's.
func TestLimitsRangesMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, filepath.Join("..", "..", testenv.RefValidML))

	for _, tc := range []struct {
		fn   string // the reference function to read
		sent error  // the sentinel whose text is the message head
		i32  uint64 // this package's constants, the subject of the comparison
		i64  uint64
		// refuse drives the *real* code path with these limits and returns its error, so the
		// message compared below is the one a vector would see. The expected text is never
		// spelled in this file; see the probe for why that is the whole point.
		refuse func(lim binary.Limits) error
	}{
		{
			fn: "check_memorytype", sent: ErrMemorySize,
			i32: memRangeI32, i64: memRangeI64,
			refuse: func(lim binary.Limits) error { return checkMemoryType(binary.Memory{Limits: lim}) },
		},
		{
			fn: "check_tabletype", sent: ErrTableSize,
			i32: tabRangeI32, i64: tabRangeI64,
			refuse: func(lim binary.Limits) error { return checkTableType(binary.Table{Limits: lim}) },
		},
	} {
		t.Run(tc.fn, func(t *testing.T) {
			body := refFuncBody(t, src, tc.fn)

			// The message head, from the `check_limits … at ("… " ^ s)` call. The reference
			// composes head-then-range-text with a space between, and the sentinel holds the head
			// with that trailing space trimmed — checked rather than assumed, since the space is
			// what makes the concatenation read as a sentence and losing it is invisible to a
			// substring match on the head alone.
			msgRe := regexp.MustCompile(`check_limits\s+lim\s+sz\s+at\s+\("([^"]*)"\s*\^\s*s\)`)
			m := msgRe.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("%s: no `check_limits lim sz at (\"…\" ^ s)` call found in %s. The "+
					"reference restructured this rule, so every constant below is a citation to "+
					"a line that no longer exists", tc.fn, testenv.RefValidML)
			}
			if want, got := strings.TrimSuffix(m[1], " "), tc.sent.Error(); want != got {
				t.Errorf("%s message head is %q in %s, sentinel says %q — the corpus matches this "+
					"as a substring, so a drift here changes which vectors this rule can satisfy",
					tc.fn, want, testenv.RefValidML, got)
			}

			// The two range rows, both directions.
			rowRe := regexp.MustCompile(`\|\s*(I32AT|I64AT)\s*->\s*(0x[0-9a-fA-F_]+)L\s*,\s*"([^"]*)"`)
			rows := rowRe.FindAllStringSubmatch(body, -1)
			claimed := map[string]uint64{"I32AT": tc.i32, "I64AT": tc.i64}
			seen := map[string]bool{}
			for _, r := range rows {
				at, litText, refText := r[1], r[2], r[3]
				lit, err := strconv.ParseUint(strings.ReplaceAll(strings.TrimPrefix(litText, "0x"), "_", ""), 16, 64)
				if err != nil {
					t.Fatalf("%s: cannot parse %s's range literal %q: %v", tc.fn, at, litText, err)
				}
				want, ok := claimed[at]
				if !ok {
					t.Errorf("%s: %s is an address type %s has and this package does not claim a "+
						"range for. A third address type means checkLimits has a case with no "+
						"caller and this test has no row for it", tc.fn, at, testenv.RefValidML)
					continue
				}
				seen[at] = true
				if lit != want {
					t.Errorf("%s: %s range is %#x in %s, this package uses %#x. **No vector in the "+
						"corpus can see this disagreement** — that is why the check is here and not "+
						"on the board", tc.fn, at, lit, testenv.RefValidML, want)
				}

				// The message, read off the real path rather than restated here. **This arrangement is
				// the fix to a hole this test shipped with, caught by watching it die**: the range text
				// used to be a literal in the row above, so the comparison ran reference-against-test
				// while module.go held a third, unchecked copy — paraphrasing the *code* text to
				// "2^16 pages for i32" left this green. A control comparing two copies of a fact must
				// own the copy the engine actually emits, which is the same lesson as calling a fix's
				// helper instead of the path that reaches it.
				wantMsg := strings.TrimSuffix(m[1], " ") + " " + refText
				if lit == math.MaxUint64 {
					// Unfalsifiable by construction, and stated rather than skipped: the range is the
					// whole u64 domain, so no binary.Limits can violate it and this arm's message can
					// never be emitted by any module. Worth knowing about a branch that looks exactly
					// like its falsifiable sibling — the value check above is all this row can assert.
					if refErr := tc.refuse(binary.Limits{Min: math.MaxUint64, Addr64: at == "I64AT"}); refErr != nil {
						t.Errorf("%s: %s range is the full u64 domain, so nothing can exceed it, yet "+
							"the maximum value refused: %v", tc.fn, at, refErr)
					}
					continue
				}
				err = tc.refuse(binary.Limits{Min: lit + 1, Addr64: at == "I64AT"})
				if err == nil {
					t.Errorf("%s: %s accepted a minimum of %#x against the reference range %#x — an "+
						"over-acceptance, the direction no vector can catch", tc.fn, at, lit+1, lit)
					continue
				}
				if got := err.Error(); got != wantMsg {
					t.Errorf("%s: %s refusal reads %q, %s composes %q — the engine's own message, "+
						"driven through the real path, against the reference's head and tail",
						tc.fn, at, got, testenv.RefValidML, wantMsg)
				}
			}
			for at := range claimed {
				if !seen[at] {
					t.Errorf("%s: this package claims a range for %s and %s has no such row. The "+
						"citation points at nothing", tc.fn, at, testenv.RefValidML)
				}
			}
		})
	}

	// The shared message, checked once because the reference shares it once (valid.ml:104-105) —
	// the asymmetry ErrLimitsMinMax's comment records. A vacuity guard rather than a formality: the
	// literal below is the whole of what three vectors match on.
	if !strings.Contains(src, `"size minimum must not be greater than maximum"`) {
		t.Errorf("%s no longer contains the min/max message literal, which ErrLimitsMinMax is a "+
			"verbatim copy of", testenv.RefValidML)
	}
	if got := ErrLimitsMinMax.Error(); got != "size minimum must not be greater than maximum" {
		t.Errorf("ErrLimitsMinMax = %q, want the reference's literal verbatim", got)
	}
}

// TestCheckLimitsOrderIsTheReferences pins the *order* of checkLimits' three predicates.
//
// **The board cannot see the order at all, and this comment claimed the opposite until the mutation
// ran.** It read: "`(table 0x1_0000_0000 0x1_0000_0000 funcref)` fails both the range test and the
// min-against-max test, so three vectors on the board do cover this." It does not — that module's
// min *equals* its max, so `min > max` is false and only the range predicate ever fires. Hoisting
// the min/max check to the top of checkLimits leaves the default board at 60786/239 with
// `0 wrong-message`, which is the measurement that refuted the sentence.
//
// Every min-over-max vector in the corpus has its minimum *inside* the range — `(memory 1 0)`,
// `(table 0xffff_ffff 0 funcref)`, and table64's mirror — so the two failing predicates never
// co-occur in a single module, and the reference's sequence is unobservable through the suite. The
// rows below are therefore the only instrument for it, which is the reason they exist as unit rows
// rather than as a note pointing at the board.
//
// That is twice in one file that a coverage claim about the corpus was asserted rather than measured
// (see the i64 ranges above). Both survived review and neither survived a mutation, which is the
// argument for the mutation and not for more careful reading.
func TestCheckLimitsOrderIsTheReferences(t *testing.T) {
	const r = 0x1_0000 // a stand-in range; the real ones are pinned above
	for _, tc := range []struct {
		name string
		lim  binary.Limits
		want error // nil means accept
	}{
		{"in range, no max", binary.Limits{Min: r}, nil},
		{"in range with max", binary.Limits{Min: 1, Max: r, HasMax: true}, nil},
		{"min == max", binary.Limits{Min: 7, Max: 7, HasMax: true}, nil},
		{"min over range, no max", binary.Limits{Min: r + 1}, ErrMemorySize},
		{"max over range", binary.Limits{Min: 0, Max: r + 1, HasMax: true}, ErrMemorySize},
		{"min > max, both in range", binary.Limits{Min: 9, Max: 8, HasMax: true}, ErrLimitsMinMax},
		// The row the order exists for: both predicates fail, and the range one must win because
		// the reference tests `min <= range` first. Swap checkLimits' first two blocks below the
		// min/max block and this is the only row that changes.
		{"min over range and min > max", binary.Limits{Min: r + 1, Max: 0, HasMax: true}, ErrMemorySize},
		// Max over range with min in range. **There is no "and min > max" mirror of the row above,
		// and the absence is the finding rather than a gap.** This row was originally written as one
		// — labelled "max over range and min > max" with `{Min: r, Max: r + 1}`, where min is *less*
		// than max, so it asserted nothing its name claimed. Caught by mutation: swapping the
		// `max <= range` and `min <= max` blocks in checkLimits left the whole suite green.
		//
		// That green is correct. The two predicates cannot conflict: `Max > range` and `Min > Max`
		// together give `Min > Max > range`, so `Min > range` and the *first* check fires whatever
		// order the other two sit in. The only observable ordering in checkLimits is therefore
		// `min <= range` before `min <= max`, which the row above covers — a fact worth stating,
		// because the code reads like three ordered checks and only one of the two orderings is
		// load-bearing. A test named for a partition gets checked against the partition.
		{"max over range, min in range", binary.Limits{Min: r, Max: r + 1, HasMax: true}, ErrMemorySize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLimits(tc.lim, r, ErrMemorySize, "test range")
			switch {
			case tc.want == nil && err != nil:
				t.Errorf("checkLimits(%+v) = %v, want accept — an over-rejection, which no "+
					"assert_invalid vector can catch", tc.lim, err)
			case tc.want != nil && err == nil:
				t.Errorf("checkLimits(%+v) accepted, want %v", tc.lim, tc.want)
			case tc.want != nil && !errorIs(err, tc.want):
				t.Errorf("checkLimits(%+v) = %v, want %v — right verdict, wrong testimony, which "+
					"is the failure 0003's message match exists for", tc.lim, err, tc.want)
			}
		})
	}
}

// errorIs is errors.Is, named locally only to keep the switch above readable. Identity and not a
// substring match on the text: checkLimits wraps with `%w` at every return, so the sentinel is
// recoverable, and comparing rendered strings here would make this test pass on a message that
// merely *contains* the right words while wrapping the wrong sentinel.
func errorIs(err, target error) bool { return errors.Is(err, target) }

// refFuncBody returns the text of one `let <name> …` definition in an OCaml source, up to the next
// top-level `let`.
//
// Scoped to the function rather than run over the whole file because both range tables use the
// identical `| I32AT -> …` shape, so a file-wide regex would match four rows and have no way to say
// which rule each belongs to — the two would agree with each other's numbers.
func refFuncBody(tb testing.TB, src, name string) string {
	tb.Helper()
	start := strings.Index(src, "let "+name)
	if start < 0 {
		tb.Fatalf("%s does not define %s — the citation every constant in this test carries points "+
			"at a function that is gone", testenv.RefValidML, name)
		return ""
	}
	rest := src[start+len("let "+name):]
	if next := regexp.MustCompile(`\nlet\s`).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}
