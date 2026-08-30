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
//
// # More than one authority
//
// The table is composed over the pin set, base-wins, for keywordgen's measured reason: the
// grammar this runtime accepts is the union of the tracked set (contract §9 G-2), and a proposal
// pin's baseline predates part of core, so a wholesale read of the overlay deletes core clauses
// rather than adding its own. The threads pin's 66 atomic mnemonics each carry a natural
// alignment nothing in core states, and *for atomics the default is also the only legal
// alignment* — every one of them is a naturally-aligned access by the proposal's own validation
// rule — so a missing row here is not a suboptimal flags byte but an instruction the encoder
// cannot write at all.
package memarggen

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen"
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

// ErrUndeclaredKind reports an arm that carries a natural alignment under a token kind
// memargKinds does not declare.
//
// **This is the reverse of ErrUnrecognized, and it is the half that was missing.** The forward
// check asks "this kind takes a memarg, so where is its `opt a N`?" — loud, and scoped to the
// kinds already declared. The complement asks "this arm states a natural alignment, so why is
// its kind not one we take?", and until the table was composed there was nothing to ask it of:
// the core lexer's 45 `opt a N` arms are exactly the six declared kinds. The threads pin brought
// 66 more under six kinds nothing here had heard of, and the *silent* failure was the one that
// mattered — an undeclared kind is skipped by the `!memargKinds[kind]` branch above, which reads
// as "the reference says this mnemonic takes no memarg" while the reference is saying the
// opposite in the same line the reader just declined to use.
//
// It is a derived complement rather than a second list: the domain is "arms whose body contains
// `opt a N`", which is the reader's own grammar, so the next proposal pin's kinds fire this
// without anyone predicting their names.
var ErrUndeclaredKind = errors.New("lexer.mll arm states a natural alignment under an undeclared kind")

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
	// Line is the 1-indexed line of the arm's head, in the file From names.
	Line int
	// From is the authority the row was read from, as gen.SourceTag renders it —
	// `spec/lexer.mll` or `spec-threads/lexer.mll`.
	//
	// **Both halves, or the citation is half a citation** (grave #529). Two pins put two files
	// named `lexer.mll` in the tree and their line numberings are unrelated: the core's
	// `i32.load` is at :195 in one and :195 in the other by coincidence, and a bare
	// `lexer.mll:266` resolves against whichever of the two the reader opens. Emit refuses a row
	// whose From is empty.
	From string
}

// Table is one extraction's result.
type Table struct {
	// Sources is every authority this table was composed from, in application order: the base
	// first, then each overlay. Stamped, not deduced — a generated artifact whose provenance
	// needs git archaeology has hearsay for authority (0007, condition 3).
	Sources []Source
	// Rows, sorted by mnemonic so the emitted output is stable and a diff means a real
	// change rather than a map iteration order.
	Rows []Row
}

// Authority is one pin's text lexer: the file, its contents, and the revision it was read at.
//
// The contents are passed in rather than read here so the falsification tests can hand this
// package a lexer with an injected defect — the seam Extract's signature has always had, kept.
type Authority struct {
	// LexerPath is the repo-relative source, in the spelling a row's citation carries (see
	// Row.From) and the generated header prints.
	LexerPath string
	// Lexer is its contents.
	Lexer string
	// SHA is this pin's revision, read from the pin's own fetch script.
	//
	// **Per authority, not per table.** `Table.SourceSHA` used to be one field and say so; that
	// is true of one pin's one file and false of the pin set, where a single stamp would have to
	// name the core revision for rows read at the threads one.
	SHA string
	// Scope is why this authority is consulted, in one clause, carried from the pin's `Why` —
	// *consultation is clause-scoped, never wholesale* (keywordgen.Source).
	Scope string
}

// Source is one authority's contribution to a composed table: its provenance, plus how many rows
// it actually put in, per token kind.
//
// **Per kind and per authority, which is what makes the widening auditable.** A total per
// authority is not enough and the numbers say why: the threads pin contributes 66 rows of 111, an
// amount no aggregate could hide, but within them `MEMORY_ATOMIC_NOTIFY` is **one** row — so a
// reader that stopped recognizing that kind alone would lose 1 row of 111 and every floor in this
// file would absorb it. This is opgen.Source's per-partition count with the partition being the
// kind, and it is the instrument the floors cannot be.
type Source struct {
	// LexerPath, SHA and Scope are the Authority's, carried through.
	LexerPath string
	SHA       string
	Scope     string
	// ByKind is the rows this authority contributed, per token kind. Rows it *read* and lost to
	// base-wins are not counted: a row is the thing the emitted table has.
	ByKind map[string]int
	// Total is their sum, kept beside them because the contribution check is a total (see
	// checkContributions for why it is not per kind).
	Total int
}

// Kinds returns the token kinds this source contributed, sorted — for a deterministic header.
func (s Source) Kinds() []string {
	out := make([]string, 0, len(s.ByKind))
	for k := range s.ByKind {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
// The core pin's six kinds are its `parser.mly`'s: LOAD, STORE, VEC_LOAD, VEC_STORE take
// `idx_opt offset_opt align_opt` (:592-598), and VEC_LOAD_LANE / VEC_STORE_LANE take
// `lane_imms` (:661), which is the same three plus a mandatory laneidx.
//
// The threads pin's six are declared beside them at `spec-threads/parser.mly:221-222` and take
// `offset_opt align_opt` (:453-459) — the same immediates *minus* the memory index, the snapshot
// predating multi-memory. ATOMIC_FENCE is deliberately absent: it is the one atomic token with no
// memarg, and admitting it would make the forward check demand an `opt a N` the reference does
// not write.
//
// **An upstream quirk this set must not "fix", and the composition brought a second of them.**
// In core, the five narrowing stores — `i32.store8`, `i32.store16`, `i64.store8`, `i64.store16`,
// `i64.store32` (lexer.mll:265-269) — are tagged **LOAD**, not STORE. In the threads pin the
// mirror image: the five narrowing atomic *loads* — `i32.atomic.load8_u`, `i32.atomic.load16_u`,
// `i64.atomic.load8_u`, `i64.atomic.load16_u`, `i64.atomic.load32_u` — are tagged
// **ATOMIC_STORE**, not ATOMIC_LOAD, leaving ATOMIC_LOAD with exactly the two wide loads. Both are
// the reference as written and both are invisible in the reference's own behaviour, because the
// grammar arms a mistagged kind reaches parse identical immediates (:456-457 are the same
// production twice). Transcribed as-is, because this table is evidence and correcting an
// authority's oddity is editing evidence; each is asserted explicitly — see
// TestNarrowingStoresAreTaggedLOAD and TestNarrowingAtomicLoadsAreTaggedATOMIC_STORE — so neither
// can be quietly normalized by a reader who assumes it was a typo here.
var memargKinds = map[string]bool{
	"LOAD":           true,
	"STORE":          true,
	"VEC_LOAD":       true,
	"VEC_STORE":      true,
	"VEC_LOAD_LANE":  true,
	"VEC_STORE_LANE": true,

	// The threads pin's, `spec-threads/parser.mly:221-222`.
	"ATOMIC_LOAD":          true,
	"ATOMIC_STORE":         true,
	"ATOMIC_RMW":           true,
	"ATOMIC_RMW_CMPXCHG":   true,
	"MEMORY_ATOMIC_WAIT":   true,
	"MEMORY_ATOMIC_NOTIFY": true,
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
// **They are floors on the composed table, and FloorPerAuthority is the other question.** No
// single pin satisfies this map: the core lexer has zero ATOMIC_RMW arms and the threads lexer,
// predating SIMD, has zero VEC_LOAD ones. So these bound the artifact and a separate, coarser
// bound asks of each authority "did this read find anything at all" — two questions, and a
// per-kind floor applied per authority would forbid every pin in the set.
//
// **They are also not enough once the table is composed**, and that is arithmetic rather than
// principle: `MEMORY_ATOMIC_NOTIFY` is one row of 111, so its floor of 1 is the only thing between
// that kind and silence — and a floor cannot see *which authority* a kind's rows came from at all.
// What covers that is Source.ByKind, printed per authority in the header and pinned exactly in the
// test.
var Floors = map[string]int{
	"LOAD":           15, // measured 19
	"STORE":          3,  // measured 4
	"VEC_LOAD":       10, // measured 13
	"VEC_STORE":      1,  // measured 1
	"VEC_LOAD_LANE":  3,  // measured 4
	"VEC_STORE_LANE": 3,  // measured 4

	// The threads pin's. Every one of these is a naturally-aligned-only access, so a missing row
	// is an instruction with no legal encoding rather than one encoded suboptimally.
	"ATOMIC_LOAD":          1,  // measured 2 (the two wide loads; the narrow ones are ATOMIC_STORE)
	"ATOMIC_STORE":         10, // measured 12 (7 stores + the 5 mistagged narrowing loads)
	"ATOMIC_RMW":           36, // measured 42 (7 constructors x 6 operators)
	"ATOMIC_RMW_CMPXCHG":   6,  // measured 7
	"MEMORY_ATOMIC_WAIT":   1,  // measured 2
	"MEMORY_ATOMIC_NOTIFY": 1,  // measured 1
}

// FloorTotal is the minimum total row count of the composed table. Beside the per-kind floors,
// not instead of them.
const FloorTotal = 95 // measured 111 composed (45 core + 66 atomic)

// FloorPerAuthority is the minimum row count one authority's lexer must yield, and it answers a
// different question from Floors: *did this read happen at all.*
//
// A moved file or a renamed rule empties one pin's contribution, and the composed floors above
// cannot see it — the other pin's rows satisfy them on their own. So each authority is bounded as
// it is read, which is keywordgen.Extract's `checkFloor` in this grammar. Coarse deliberately: the
// per-*kind* question cannot be asked of a single pin, since neither pin holds every kind.
//
// Both pins are full interpreter snapshots, so both carry core's scalar memarg families; the
// measured numbers are 45 for the core lexer and 111 for the threads one (which holds its own
// pre-multi-memory spellings of core's 45, all of them lost to base-wins).
const FloorPerAuthority = 30

// CoreLexerAuthority is the path Extract records for its single authority: the core
// interpreter's text lexer, which is the base of every composition.
const CoreLexerAuthority = "third_party/spec/interpreter/text/lexer.mll"

// Extract reads one lexer.mll's source and returns the natural-alignment table.
//
// sha is recorded verbatim; the caller is responsible for it being the revision src was read
// from (scripts/fetch-spec-ref.sh pins and verifies it).
//
// **This is the one-pin entry point, and it is no longer how the committed table is built** — see
// BuildFromPins. It stays exported because the falsification tests need to hand the reader a
// lexer with an injected defect, an input no pin produces; the path it records is the core pin's
// because that is the only pin a single-authority read can honestly claim to be.
//
// It applies FloorPerAuthority and **not** the composed Floors, which is not a laxness: no single
// pin satisfies the per-kind map, so applying it here would make this entry point refuse every
// real input it could be given.
func Extract(src, sha string) (*Table, error) {
	t, err := composeRows([]Authority{{LexerPath: CoreLexerAuthority, Lexer: src, SHA: sha}})
	if err != nil {
		return nil, err
	}
	if err := t.checkContributions(); err != nil {
		return nil, err
	}
	return t, nil
}

// ExtractFrom composes the authorities in order, base first, and returns the table.
//
// # The composition is base-wins, and the direction is load-bearing
//
// Each authority contributes only the mnemonics the ones before it did not name. That is
// keywordgen.Compose's rule, here for the same measured reason: the threads pin's baseline
// predates SIMD, GC and memory64, so letting a later authority win would *delete* core rows
// rather than add proposal ones — it holds all 45 of core's memarg mnemonics at its own
// pre-multi-memory spellings, so an overlay-wins composition would leave the table the same size
// and every row of it stamped with the wrong revision.
func ExtractFrom(auths []Authority) (*Table, error) {
	t, err := composeRows(auths)
	if err != nil {
		return nil, err
	}
	if err := t.checkFloors(); err != nil {
		return nil, err
	}
	if err := t.checkContributions(); err != nil {
		return nil, err
	}
	return t, nil
}

// composeRows is ExtractFrom without the floor and contribution gates.
//
// Split out for one reason, and it was found by falsifying: **a control cannot measure a count
// through a gate that refuses on that count.** `TestEveryFloorIsBelowItsMeasuredCount` asserts
// `floor <= measured`, and with a floor set above its partition ExtractFrom returns ErrVacuous —
// so the test failed on the extraction rather than on its own assertion, and the `floor > got`
// branch it exists for was unreachable. That is a stillborn branch behind a red board: right
// verdict, dead assertion, indistinguishable on a green board from a working one. Reading the
// rows here lets the distance check see the numbers the gate would have hidden.
func composeRows(auths []Authority) (*Table, error) {
	if len(auths) == 0 {
		return nil, fmt.Errorf("%w: no authority to read", ErrVacuous)
	}

	t := &Table{}
	claimed := map[string]bool{}
	for _, a := range auths {
		if a.LexerPath == "" {
			return nil, fmt.Errorf("%w: an authority with no path: a row read from it would carry "+
				"a line number and no file, which is grave #529's half-citation", ErrVacuous)
		}
		rows, err := armRows(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.LexerPath, err)
		}
		// Bounded on rows *read*, before base-wins takes any of them away — the two questions are
		// different and both are asked. This one is "did this authority's lexer get read at all",
		// and it belongs here rather than in a gate outside because a moved file yields zero arms
		// and the composed floors are satisfied by the other pin alone. See FloorPerAuthority.
		if len(rows) < FloorPerAuthority {
			return nil, fmt.Errorf("%w: %s yielded %d memarg arms, floor %d — a read this small "+
				"means the block locator or the kind discriminator stopped matching this file, "+
				"which the composed floors cannot see because the other authority satisfies them",
				ErrVacuous, a.LexerPath, len(rows), FloorPerAuthority)
		}
		src := Source{
			LexerPath: a.LexerPath, SHA: a.SHA, Scope: a.Scope,
			ByKind: map[string]int{},
		}
		for _, r := range rows {
			if claimed[r.Mnemonic] {
				// Base-wins. Not counted into ByKind: a row this authority read and lost is not a
				// row the emitted table has, and counting it would let an overlay whose every
				// mnemonic core already holds look like it contributed.
				continue
			}
			claimed[r.Mnemonic] = true
			src.ByKind[r.Kind]++
			src.Total++
			t.Rows = append(t.Rows, r)
		}
		t.Sources = append(t.Sources, src)
	}

	// Sorted by mnemonic so the emitted table reads as a table rather than as a transcript of
	// the reference's line order (which is what the scan produces: `i32.load i64.load f32.load
	// f64.load i32.store …`, grouped by family). **Not for determinism** — the scan is already
	// deterministic, there being no map between `mllex.Arms` and here, and a control claiming
	// otherwise was found stillborn by falsifying it. What the order actually buys is that a
	// diff of the generated file localizes: an upstream mnemonic added to the `f64` family shows
	// up beside its neighbours instead of wherever upstream happened to put the line. Composition
	// makes it do more: without it the 66 atomic rows would all land after the 45 core ones, so
	// `i32.atomic.load` would sit a screen away from `i32.load` in a table keyed by neither.
	slices.SortFunc(t.Rows, func(a, b Row) int { return strings.Compare(a.Mnemonic, b.Mnemonic) })
	return t, nil
}

// armRows reads one authority's memarg arms, in file order.
func armRows(a Authority) ([]Row, error) {
	lines := strings.Split(a.Lexer, "\n")
	block, err := mllex.FindBlock(lines)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVacuous, err)
	}
	arms, err := mllex.Arms(lines, block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnrecognized, err)
	}
	from := gen.SourceTag(a.LexerPath)

	var out []Row
	for _, arm := range arms {
		km := reKind.FindStringSubmatch(arm.Body)
		// `len(km) < 2` rather than `km == nil`: a match against a one-group pattern is always
		// length 2, so the two conditions coincide today, and the length form is the one that
		// stays true of the index it guards if the pattern's group count ever changes.
		kind := ""
		if len(km) >= 2 {
			kind = km[1]
		}
		om := reOptAlign.FindStringSubmatch(arm.Body)
		if !memargKinds[kind] {
			if om != nil {
				// The reverse check. See ErrUndeclaredKind: this is the branch that used to be a
				// silent `continue`, and the arm it declined to read was stating the very fact
				// this package exists to carry.
				return nil, fmt.Errorf("%w: %s:%d: %q returns %s and states `opt a %s`; that kind "+
					"is not in memargKinds, so the row was about to be dropped as "+
					"\"takes no memarg\" while the reference says otherwise on the same line",
					ErrUndeclaredKind, from, arm.Line, arm.Keyword, kind, om[1])
			}
			// Not a memory-accessing arm. Silence is correct here and is *not* the skip the
			// disciplines forbid: the discriminator is the kind set above, and the branch that
			// could have made it a false negative is the error just above, so this is "the
			// reference says this mnemonic takes no memarg" — a fact rather than an unread line.
			continue
		}
		if om == nil {
			return nil, fmt.Errorf("%w: %s:%d: %s arm for %q has no `opt a N` — the "+
				"reference changed how the natural alignment is written, and a row missing "+
				"from this table is an instruction the encoder defaults wrongly and silently",
				ErrUnrecognized, from, arm.Line, kind, arm.Keyword)
		}
		align, err := parseSmallNat(om[1])
		if err != nil {
			return nil, fmt.Errorf("%w: %s:%d: %q: %w", ErrUnrecognized, from, arm.Line, arm.Keyword, err)
		}
		out = append(out, Row{
			Mnemonic: arm.Keyword, Kind: kind, Align: align, Line: arm.Line, From: from,
		})
	}
	return out, nil
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

// checkContributions is the check the floors cannot be: every authority put a row in.
//
// Distinct from FloorPerAuthority, which bounds the rows an authority *read*. This bounds the rows
// it *kept* — an overlay every one of whose mnemonics the base already holds reads 111 arms,
// clears that floor, and contributes nothing, leaving the composed table silently back to the
// base's content and drift-checking clean against a file regenerated the same way. Two failure
// modes, two checks; a read that happened and a contribution that survived are not the same fact.
//
// **A total per authority, not a per-kind requirement**, and the line is drawn where the honest
// claim is. "This pin contributes at least one row" is true of any pin worth composing — a pin
// naming nothing is a pin the caller should not have passed. "This pin contributes at least one
// row of a kind of its own" is not: a proposal adding mnemonics of existing kinds only
// (memory64's `i32.load` at a wider index, say) would contribute zero new kinds legitimately, and
// a check that fired on it would teach the next author to route around the instrument. The
// per-kind numbers are pinned instead where a change is visible without being forbidden — Emit
// prints them per authority, so the drift check sees `MEMORY_ATOMIC_NOTIFY 1` become `0` as a
// diff.
func (t *Table) checkContributions() error {
	for i, s := range t.Sources {
		if s.Total > 0 {
			continue
		}
		return fmt.Errorf("%w: authority %d (%s at %s) contributed no rows at all; the floors "+
			"cannot see this, since one authority's rows satisfy them on their own",
			ErrVacuous, i, s.LexerPath, s.SHA)
	}
	return nil
}
