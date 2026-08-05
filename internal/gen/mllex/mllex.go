// Package mllex reads `lexer.mll`'s keyword block: where it starts, where it ends, and
// which lines are arms — including the ones whose body wraps onto following lines.
//
// # Why this package exists, and why it is not part of `gen`
//
// **Three occurrences un-froze the tooling.** The wrapped-arm shape — an arm head
// `| "v128.load8_splat" ->` with its constructor on the *next* line — has now been met
// three times in three packages that share nothing else:
//
//	#78   keywordgen   solved it: the arm-head regexp stops at `->`, body read forward
//	#105  opgen        reintroduced it: 411 rows where 436 were measured, silent
//	—     memarggen    would have been the third, and was met preventively (no grave: it
//	                   did not happen)
//
// The third was avoided by *reading the sibling first*, which is the ruling on #108
// working exactly as written. But avoidance is personal and does not survive the next
// author, so Scott's consolidation clause applies: at three occurrences the shape becomes
// **one shared helper both generators and the new one call**, so a fourth occurrence is
// structurally impossible rather than individually dodged. Lessons indexed by shape, now
// enforced by shape.
//
// # An issue-number citation with no local oracle, caught twice by accident
//
// The table above cited `#127` for two days and was **wrong both times, in opposite
// directions**, which is worth the paragraph because #116 has this class open and cannot
// yet check it.
//
// First it was fiction: the number was typed into these comments during the work, before
// any such PR existed, and resolved to nothing. Then it became right by luck — GitHub
// shares one sequence between issues and PRs, and the PR opened for this work happened to
// take 127. Then it went wrong again in a *different* way: #126's merge deleted the base
// branch, GitHub auto-closed #127 and refused to reopen it, and the work landed as #128 —
// so a number that resolved perfectly now pointed at a closed, superseded PR. A resolving
// citation aimed at the wrong one of two is exactly the drifted-fixture-citation defect,
// and it is invisible to a resolver that only asks whether the target exists.
//
// **What makes this the hard half of the class**: `constWalk` (grave #116's sibling) was
// catchable because a package's identifiers can be enumerated locally, and `#NN` cannot be
// resolved without the network — so neither error was findable by any control this repo
// has. Both were found by a human noticing, which is not a mechanism. Recorded rather
// than quietly corrected, because the value of a near-miss is that it locates a live gap:
// an issue-number checker needs an oracle the local tree does not contain, and until #116
// supplies one, prose citing `#NN` for *the current change* is a claim nothing verifies.
//
// **`gen` was the wrong home, and its own doc says so**: "This package holds the two facts
// that are about the repository — where the pin lives, how generated source is formatted —
// and nothing about OCaml, decoders, or lexers. A shared package that grew a `parseArm`
// would be the wrong seam." That sentence was right and stays true — this is a *different*
// seam, the one that knows OCaml lexer grammar, and it gets its own package rather than
// being smuggled into the one that deliberately refused it.
//
// # What is shared and what is not
//
// Shared: the block's extent, the arm-head shape, and the rejoining of wrapped bodies.
// That is the mechanism all three consumers were reimplementing, and it is the mechanism
// the graves were filed against.
//
// **Not** shared: what an arm's body *means*. `keywordgen` reads a token constructor from
// it, `opgen` mines it for lowercase identifiers to join against the opcode table, and
// `memarggen` reads an `(opt a N)` natural alignment out of it. Those are three different
// grammars over one substrate, and folding them together would be the wrong seam in the
// other direction — a shared `Body` field each consumer parses itself keeps the
// duplication where it is genuinely different.
package mllex

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnrecognized reports a line inside the keyword block that looks like an arm and could
// not be read as one.
//
// **An error, not a skip**, and this is the load-bearing property: an unreadable arm is
// exactly the case where silence costs a row, and a row lost silently is what #105 was.
// The discriminator is a leading `| "`, which is the head shape — anything else inside the
// block is a continuation, and a continuation whose own head was read is already
// accounted for.
var ErrUnrecognized = errors.New("unrecognized arm in lexer.mll")

// ErrNoBlock reports that the keyword block's delimiters could not be located.
//
// Separate from ErrUnrecognized because the remedies differ: an unrecognized arm means the
// arm grammar grew a form, while a missing block means the file moved or the rule was
// renamed. Both must be loud — a locator that returns zero arms and no error is the
// vacuity defect, and every consumer's floor is downstream of this error firing.
var ErrNoBlock = errors.New("could not locate lexer.mll's keyword block")

var (
	// reArmHead is the arm's head and **stops at `->`**, which is the whole point of this
	// package. Everything after the arrow is captured separately and may be empty; an
	// empty capture is a wrapped arm, not a broken one. A regexp that required a non-empty
	// body here is precisely the defect #78 solved, #105 re-derived, and this package now
	// makes unavailable.
	//
	// The keyword capture admits escapes (`(?:[^"\\]|\\.)*`) rather than `[^"]*`, taking
	// opgen's stricter form: the two differed, and the difference is invisible until the
	// reference grows a keyword containing a quote.
	reArmHead = regexp.MustCompile(`^\s*\|\s*"((?:[^"\\]|\\.)*)"\s*->(.*)$`)

	// reArmish is a line that looks like an arm head but did not parse as one.
	reArmish = regexp.MustCompile(`^\s*\|\s*"`)

	// The block's own delimiters: the `| keyword as s` arm of `rule token`, its `match`,
	// and the fallthrough that ends it. The fallthrough is one of the two producers of
	// `unknown operator` (lexer.mll:809; the other is `| reserved`, outside this block),
	// and it is what makes absence from a keyword table mean "not a token".
	reBlockStart  = regexp.MustCompile(`^\s*\|\s*keyword\s+as\s+s\s*$`)
	reBlockMatch  = regexp.MustCompile(`^\s*\{\s*match\s+s\s+with\s*$`)
	reFallthrough = regexp.MustCompile(`^\s*\|\s*_\s*->\s*unknown\s+lexbuf\s*$`)
)

// Arm is one keyword arm with its body rejoined.
type Arm struct {
	// Keyword is the literal from the arm's head.
	Keyword string
	// Body is everything after `->`, with wrapped continuation lines appended and
	// separated by newlines. The token kind is included: it is SCREAMING_CASE and every
	// consumer's own grammar can tell it apart from what it wants, so *not* having to
	// exclude it is what lets the head regexp stop at the arrow.
	Body string
	// Line is the 1-indexed line of the arm's *head* — the line a generated row cites, so
	// that an audit lands on the keyword rather than on its continuation.
	Line int
}

// Block is the keyword block's extent, in 1-indexed lines.
type Block struct {
	// Start is the `| keyword as s` line, Match the `{ match s with` line, and
	// Fallthrough the `| _ -> unknown lexbuf` line.
	Start, Match, Fallthrough int
}

// FindBlock locates the keyword block in lexer.mll's source.
//
// The two-line head is matched as a *pair* rather than by either line alone: `match s with`
// occurs in other rules, and `| keyword as s` on its own would admit a future rule that
// dispatched on keywords for a different purpose.
func FindBlock(lines []string) (Block, error) {
	for i := 0; i+1 < len(lines); i++ {
		if !reBlockStart.MatchString(lines[i]) || !reBlockMatch.MatchString(lines[i+1]) {
			continue
		}
		for j := i + 2; j < len(lines); j++ {
			if reFallthrough.MatchString(lines[j]) {
				return Block{Start: i + 1, Match: i + 2, Fallthrough: j + 1}, nil
			}
		}
		return Block{}, fmt.Errorf("%w: found `| keyword as s` / `{ match s with` at %d "+
			"but no `| _ -> unknown lexbuf` below it", ErrNoBlock, i+1)
	}
	return Block{}, fmt.Errorf("%w: no `| keyword as s` followed by `{ match s with`", ErrNoBlock)
}

// Arms reads every keyword arm in the block, rejoining wrapped bodies.
//
// **The rejoining rule, and its bound.** Every line after an arm's head that is neither
// blank nor another arm head belongs to that arm's body and is appended. The bound matters:
// without it a genuinely empty arm would silently inherit its *successor's* body, which is
// a wrong row rather than a missing one — the more expensive failure, since a missing row
// trips a floor and a wrong row does not.
//
// **All continuation lines, not just the first**, and that distinction was a measurement
// rather than a reading. The first draft stopped as soon as the body was non-empty, which is
// correct for 20 of the 25 wrapped arms and wrong for 5: the four scalar `const` forms carry
// two continuation lines and `v128.const` carries four (lexer.mll:308-323). `keywordgen`
// could not have noticed — `reKind` reads the token constructor off the first line — while
// `opgen` scans the whole body for an instruction constructor, so a truncating substrate
// would have silently unjoined rows in one consumer and not the other. That is #105's shape
// wearing a shared helper's clothes, and the only reason it is not a grave is that the run
// lengths were printed before the wiring rather than after. The distribution 20/4/1 is pinned
// by `TestBodiesAreWholeNotJustTheirFirstLine`.
//
// Continuation lines are joined with "\n" rather than " " so a consumer scanning for a
// pattern cannot have one manufactured across a line boundary that the reference does not
// contain.
func Arms(lines []string, b Block) ([]Arm, error) {
	// The head line and its `match` are consumed; scanning starts after them and stops
	// before the fallthrough, which is not an arm.
	lo, hi := b.Match, b.Fallthrough-1
	if lo < 0 || hi > len(lines) || lo > hi {
		return nil, fmt.Errorf("%w: block extent %d..%d does not lie inside %d lines",
			ErrNoBlock, lo, hi, len(lines))
	}
	var out []Arm
	for i := lo; i < hi; i++ {
		m := reArmHead.FindStringSubmatch(lines[i])
		if m == nil {
			if reArmish.MatchString(lines[i]) {
				return nil, fmt.Errorf("%w: lexer.mll:%d looks like a keyword arm but did "+
					"not parse: %q", ErrUnrecognized, i+1, strings.TrimSpace(lines[i]))
			}
			// Not an arm and not arm-shaped: a continuation, already appended to the arm
			// it belongs to, or a blank. Silence here is correct — the discriminator above
			// is what keeps it from being silence about a lost row.
			continue
		}
		var body strings.Builder
		body.WriteString(strings.TrimSpace(m[2]))
		for j := i + 1; j < hi; j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || reArmish.MatchString(lines[j]) || strings.HasPrefix(t, "| ") {
				break
			}
			if body.Len() > 0 {
				body.WriteString("\n")
			}
			body.WriteString(t)
		}
		out = append(out, Arm{Keyword: m[1], Body: body.String(), Line: i + 1})
	}
	return out, nil
}
