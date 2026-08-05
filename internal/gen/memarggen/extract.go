// Package memarggen extracts the natural-alignment table from the reference interpreter's
// text lexer.
//
// # Why this exists
//
// A memarg's `align=` is optional, and when it is absent the instruction still encodes an
// alignment — `encode.ml`'s `memop` always writes the flags byte. So the *default* is a fact
// the encoder needs and the text does not carry, and the reference states it per mnemonic
// inside the lexer's own token closure:
//
//	| "i32.load8_u" -> LOAD (fun x a o -> i32_load8_u x (opt a 0) o)
//	| "i64.load"    -> LOAD (fun x a o -> i64_load    x (opt a 3) o)
//	| "v128.load"   -> VEC_LOAD (fun x a o -> v128_load x (opt a 4) o)
//
// `opt a N` is "the given alignment, or N" — 45 numbers, one per memory-accessing mnemonic,
// and no two derivable from each other by a rule this package could apply instead. `i32.load`
// is 2 and `i64.load32_u` is also 2; `v128.load8x8_u` is 3 while `v128.load8_splat` is 0.
// The pattern is *the width of the accessed value*, which is a property of the mnemonic's
// semantics rather than of its spelling, so a generator that tried to compute it from the
// name would be re-deriving the reference's semantics from its identifiers.
//
// # Why it is machine-read rather than typed
//
// 0007's argument, one field over, and the accept direction is where it bites. A wrong
// default produces a module that **decodes clean**: the flags byte is a legal alignment, the
// decoder accepts it, and the only thing wrong is that the image says `align=1` where the
// text meant `align=4`. No `assert_malformed` can see that — the alignment is not checked
// against the access width at decode time at all, only at validation, and even there an
// over-aligned access is the only error. So a mistyped default is invisible on the board by
// construction, which is exactly contract §9 G-3, and this repo's measured
// hand-transcription rate is seven wrong citations in twelve items.
//
// # What this package shares and what it owns
//
// The block locating and the wrapped-arm rejoining are `mllex`'s — **14 of these 45 arms wrap**
// their constructor onto the following line, so a reader that required `-> TOKEN` on one line
// would silently lose 14 of 45 rows: #78/#105's shape, and the third occurrence, which is what
// un-froze the tooling. Note the proportion. In `keywordgen`'s population wrapping is 25 of 589,
// a 4% loss a floor might plausibly survive; here it is **31%**, concentrated in exactly the
// splat/zero/lane families this table exists to serve, so the same defect would have been both
// larger and harder to attribute. That figure was *measured* — the first draft of this sentence
// said six, which was a count of the `extadd_pairwise`/`trunc_sat` families read off a sibling's
// prose rather than of this table's own rows. What this package owns is one regexp over an arm's
// *body*: finding `opt a N` and the token kind beside it.
package memarggen

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen/mllex"
)

// ErrUnrecognized reports an arm whose alignment could not be read.
//
// It fires on an arm that names a memory-accessing token kind and has no `opt a N` in its
// body, which would be the reference changing how the default is written. **An error rather
// than an omission**, because a missing row is a mnemonic the encoder would silently refuse
// (or worse, encode with a zero default) and the floor below is too coarse to see one.
var ErrUnrecognized = errors.New("unrecognized memarg arm in lexer.mll")

// ErrVacuous reports an extraction that produced implausibly little. See Floors.
var ErrVacuous = errors.New("extraction produced too few memarg rows")

var (
	// reOptAlign is the whole of this package's own grammar: `opt a N` inside the token
	// closure, where N is the log2 alignment.
	//
	// **Stored as log2 on both sides, which is why this is a lookup and not a conversion.**
	// `parser.mly:532`'s `align` production takes the *byte count* the text writes and calls
	// `log2_unsigned` on it, so the value the closure receives is already an exponent; and
	// `encode.ml:221`'s `memop` writes `of_int align` straight into the flags field. The
	// reference's own default is written as an exponent here, so the number this regexp
	// captures is the number the flags byte holds — no arithmetic in between to get wrong.
	reOptAlign = regexp.MustCompile(`\bopt\s+a\s+(\d+)\b`)

	// reKind takes the token kind: the first SCREAMING_CASE identifier in the body. Same
	// shape keywordgen uses, and safe for the same reason — the kind is the only uppercase
	// identifier an arm's body opens with.
	reKind = regexp.MustCompile(`^\s*([A-Z][A-Z_0-9]*)`)
)

// Row is one mnemonic's natural alignment.
type Row struct {
	// Mnemonic is the wat spelling, from the arm's head.
	Mnemonic string
	// Kind is the token kind the arm returns, in the reference's vocabulary. Retained
	// because it is the partition the floors below are per, and because a consumer joining
	// this against the keyword table wants to see the two agree.
	Kind string
	// Align is the log2 alignment the reference defaults to — the value `opt a N` supplies.
	Align int
	// Line is the 1-indexed lexer.mll line of the arm's head.
	Line int
}

// Table is one extraction's result.
type Table struct {
	// SourceSHA is the reference revision the rows were read from. Stamped, not deduced.
	SourceSHA string
	// Rows, sorted by mnemonic so the emitted output is stable and a diff means a real
	// change rather than a map iteration order.
	Rows []Row
}

// memargKinds are the token kinds whose arms take a memarg, and therefore the kinds an
// `opt a N` is *required* in.
//
// **This set is what makes a missing alignment an error rather than a silence.** Without it
// the reader could only say "this arm has no `opt a N`", which is true of 544 of the 589 arms
// in the block
// and carries no information. With it, `i32.load` losing its default is a hard failure while
// `i32.add` having none is expected — the discriminator that turns an omission into a finding.
//
// The six kinds are `parser.mly`'s: LOAD, STORE, VEC_LOAD, VEC_STORE take
// `idx_opt offset_opt align_opt` (:592-598), and VEC_LOAD_LANE / VEC_STORE_LANE take
// `lane_imms` (:661), which is the same three plus a mandatory laneidx.
//
// **An upstream quirk this set must not "fix":** the five narrowing stores — `i32.store8`,
// `i32.store16`, `i64.store8`, `i64.store16`, `i64.store32` (lexer.mll:265-269) — are tagged
// **LOAD**, not STORE. That is the reference as written; the token kind only selects which
// grammar arm parses the immediates and all four scalar arms are identical, so the mistagging
// is invisible in the reference's own behaviour. Transcribed as-is, because this table is
// evidence and correcting an authority's oddity is editing evidence. It is asserted
// explicitly by TestNarrowingStoresAreTaggedLOAD so the oddity cannot be quietly normalized
// by a future reader who assumes it was a typo here.
var memargKinds = map[string]bool{
	"LOAD":           true,
	"STORE":          true,
	"VEC_LOAD":       true,
	"VEC_STORE":      true,
	"VEC_LOAD_LANE":  true,
	"VEC_STORE_LANE": true,
}

// Floors are the minimum row counts, **per token kind and in total**.
//
// Per-partition rather than one total, and that is the ruling on #108 rather than a
// preference: a total floor of 38 passes on the 19 LOAD rows plus the 13 VEC_LOAD rows and the
// 4 STORE rows alone while **every lane kind finds zero** — three empty partitions absorbed by
// three full ones, which is the vacuity law with a partner to hide behind. Each kind floors separately, so an upstream
// refactor that emptied one is a failure rather than a rounding error.
//
// The values are set below what was measured, with room for upstream to *remove* mnemonics
// without a false alarm. The **exact** counts live in the test beside them, because a floor
// bounds the catastrophic case and cannot see a small silent loss — `Floors.Lexer` at 350
// stayed green through #105's 411-of-436.
var Floors = map[string]int{
	"LOAD":           15, // measured 19
	"STORE":          3,  // measured 4
	"VEC_LOAD":       10, // measured 13
	"VEC_STORE":      1,  // measured 1
	"VEC_LOAD_LANE":  3,  // measured 4
	"VEC_STORE_LANE": 3,  // measured 4
}

// FloorTotal is the minimum total row count. Beside the per-kind floors, not instead of them.
const FloorTotal = 38 // measured 45

// Extract reads lexer.mll's source and returns the natural-alignment table.
//
// sha is recorded verbatim; the caller is responsible for it being the revision src was read
// from (scripts/fetch-spec-ref.sh pins and verifies it).
func Extract(src, sha string) (*Table, error) {
	t, err := extractRows(src, sha)
	if err != nil {
		return nil, err
	}
	if err := t.checkFloors(); err != nil {
		return nil, err
	}
	return t, nil
}

// extractRows is Extract without the floor gate.
//
// Split out for one reason, and it was found by falsifying: **a control cannot measure a count
// through a gate that refuses on that count.** `TestEveryFloorIsBelowItsMeasuredCount` asserts
// `floor <= measured`, and with a floor set above its partition Extract returns ErrVacuous — so
// the test failed on the extraction rather than on its own assertion, and the `floor > got`
// branch it exists for was unreachable. That is a stillborn branch behind a red board: right
// verdict, dead assertion, indistinguishable on a green board from a working one. Reading the
// rows here lets the distance check see the numbers the gate would have hidden.
func extractRows(src, sha string) (*Table, error) {
	lines := strings.Split(src, "\n")
	block, err := mllex.FindBlock(lines)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVacuous, err)
	}
	arms, err := mllex.Arms(lines, block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnrecognized, err)
	}

	t := &Table{SourceSHA: sha}
	for _, a := range arms {
		km := reKind.FindStringSubmatch(a.Body)
		// `len(km) < 2` rather than `km == nil`: a match against a one-group pattern is always
		// length 2, so the two conditions coincide today, and the length form is the one that
		// stays true of the index it guards if the pattern's group count ever changes.
		if len(km) < 2 || !memargKinds[km[1]] {
			// Not a memory-accessing arm. Silence is correct here and is *not* the skip the
			// disciplines forbid: the discriminator is the kind set above, so this branch is
			// "the reference says this mnemonic takes no memarg", which is a fact rather than
			// an unread line.
			continue
		}
		om := reOptAlign.FindStringSubmatch(a.Body)
		if om == nil {
			return nil, fmt.Errorf("%w: lexer.mll:%d: %s arm for %q has no `opt a N` — the "+
				"reference changed how the natural alignment is written, and a row missing "+
				"from this table is an instruction the encoder defaults wrongly and silently",
				ErrUnrecognized, a.Line, km[1], a.Keyword)
		}
		align, err := parseSmallNat(om[1])
		if err != nil {
			return nil, fmt.Errorf("%w: lexer.mll:%d: %q: %w", ErrUnrecognized, a.Line, a.Keyword, err)
		}
		t.Rows = append(t.Rows, Row{Mnemonic: a.Keyword, Kind: km[1], Align: align, Line: a.Line})
	}

	// Sorted by mnemonic so the emitted table reads as a table rather than as a transcript of
	// the reference's line order (which is what the scan produces: `i32.load i64.load f32.load
	// f64.load i32.store …`, grouped by family). **Not for determinism** — the scan is already
	// deterministic, there being no map between `mllex.Arms` and here, and a control claiming
	// otherwise was found stillborn by falsifying it. What the order actually buys is that a
	// diff of the generated file localizes: an upstream mnemonic added to the `f64` family shows
	// up beside its neighbours instead of wherever upstream happened to put the line.
	slices.SortFunc(t.Rows, func(a, b Row) int { return strings.Compare(a.Mnemonic, b.Mnemonic) })
	return t, nil
}

// parseSmallNat reads a log2 alignment, bounded.
//
// Bounded because the value goes into a flags byte's low six bits: `memop` or's it with 0x40,
// so an alignment of 64 or more would collide with the has-idx bit and produce an instruction
// that decodes as a *different* instruction. The reference's largest is 4 (`v128.load`), so
// anything past a small bound is the extraction misreading rather than upstream growing.
func parseSmallNat(s string) (int, error) {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
		if n > 6 {
			return 0, fmt.Errorf("alignment exponent %q exceeds 6: the flags field's low bits "+
				"hold this and 0x40 is the has-idx bit, so a larger value would encode a "+
				"different instruction", s)
		}
	}
	return n, nil
}

// checkFloors is the vacuity control, per partition. See Floors.
func (t *Table) checkFloors() error {
	byKind := map[string]int{}
	for _, r := range t.Rows {
		byKind[r.Kind]++
	}
	for kind, floor := range Floors {
		if byKind[kind] < floor {
			return fmt.Errorf("%w: %d %s rows, floor %d — a partition this small means the "+
				"reader stopped recognizing that kind's arms, which an aggregate count would "+
				"absorb into the kinds that still work", ErrVacuous, byKind[kind], kind, floor)
		}
	}
	if len(t.Rows) < FloorTotal {
		return fmt.Errorf("%w: %d rows total, floor %d", ErrVacuous, len(t.Rows), FloorTotal)
	}
	return nil
}
