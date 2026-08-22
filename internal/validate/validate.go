// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package validate is the type oracle decision 0002 Q3 names: the pass between decode and
// execution that establishes, statically, which type every value slot holds.
//
// That is not a hygiene check. It is what makes the interpreter's bare `uint64` slots sound —
// the validator decides which of the two parallel stacks (numeric or reference) each slot uses,
// and *that decision is what makes runtime tags redundant*. An unvalidated module reaching the
// interpreter is the condition ADR 0025's carve-out names, and this package exists to end it.
//
// # Slice 1: the typing spine
//
// This is the first of #9's slices (#291) and it implements the algorithm, not the whole rule
// set: the operand stack, the control-frame stack, the polymorphic-after-`unreachable` rule,
// function-body result arity, and the instruction signature table for the numeric, parametric,
// variable, and memory-access families. Every later rule hangs off this; none of them can be
// checked before it exists.
//
// # Slice 2: the vector region
//
// `vec.go` types the 0xFD region (#305) — 236 core SIMD opcodes across 20 constructor families,
// including the lane-index bounds, which is the second sentinel this package declares. Its argument
// for reading the classification out of `syntax/mnemonics.ml` instead of writing it here is that
// file's own header; what matters at this level is that the region is no longer declined.
//
// This paragraph then said `select t` (#294) was the last instruction in the single-byte space slice
// 1 left, which was true when it was written and is not now — slice 4 took it. It then said the
// single-byte space was **fully in vocabulary as of that slice** and that **0xFE (threads) alone**
// remained declined, and both of those clauses were **false when they were written** — eleven named
// single-byte opcodes were declined at the time, and ADR 0032 amended the sentence immediately after
// this one for staleness while leaving these standing, in the very motion that was auditing them.
// See slice 8 below for the measurement and for what replaced the sentence.
//
// Its prefixed-region list read "0xFB (GC), 0xFC (bulk memory/table), 0xFE (threads)", and it was
// **stale on 0xFC from the moment slice 5 landed** — found by slice 7 reading its own boundary
// rather than by anything that checks, which is the whole reason ADR 0032 amends both statements of
// this boundary in one motion instead of the one it was opening. Two places declaring a boundary is
// two places to update, and a list of regions is exactly the shape that goes quietly out of date:
// nothing about a region typed by a new slice makes a *sentence elsewhere* fail.
//
// # Slice 3: the memarg's alignment
//
// `align.go` is `check_memop`'s alignment `require` (#306), and it is the smallest slice here by
// line count and the one with the longest argument attached, because **it is the first rule in this
// package that was blocked on a different package**. The decoder read the alignment exponent and
// dropped it; the rule had no operand. Retention through `Imm1` plus the comparison against a
// per-opcode natural width closed 54 admissions and 4 wrong-message refusals together. The other two
// `require`s in the reference's function are named in `align.go`'s header — one unreachable across
// all 45 rows, one (`offset out of range`) reachable and **written by #310**, in the PR that landed
// this sentence's correction along with the rule.
//
// # Slice 4: `select`'s result-type annotation
//
// `selectAnnotated` in `instr.go` is `valid.ml:442-446` (#294), and it landed in the *same PR* as
// slice 3 because it is the same defect wearing a different opcode: the decoder read `select t`'s
// result-type vector and dropped it, so the arity rule had nothing to count and the type rule had
// nothing to pop against. `Func.Selects` retains it (0016's side table) and this arm reads it.
//
// **The two slices drain opposite sub-populations of the fail column, and that is the argument for
// having folded them.** Slice 3's vectors were *admissions* — rules this package knew and could not
// reach — so its reward is `validateAdmitCeiling` and `validateMismatchCeiling` falling with
// `validateDeclineCeiling` untouched. Slice 4's two were *declines*, which is the channel telling
// the truth: `select t` genuinely was not in vocabulary, and now it is. Same cause, disjoint
// destinations, one PR — see `passFloor`'s and `allOnPassFloor`'s accounts for both tables.
//
// It is also the last slice whose rule was blocked on a *different package*. What blocked the two
// rules named here next was this package itself: #311's `check_valtype` on a block's valtype
// annotation was a call the walk never made, and #310's `offset out of range` was a `require` never
// written. **Both are now written**, and the tense is corrected in place rather than the sentence
// deleted, because *which* rules this package was the blocker for is the durable half of it.
//
// This paragraph also called slice 4 "the slice that closes the single-byte space", which is the
// same false claim as the one above wearing a different subject and is what slice 8 closes for real.
//
// # The out-of-scope register, and it is empty
//
// Out of scope by declaration, each with its own expected string in the suite and so its own
// measurable slice — that was the register's form, and **every entry has now left it**. Eight
// departures, named as departures rather than deleted, because which entry each slice took is the
// durable half:
//
//   - **SIMD lane immediates (48)** with slice 2, which is why `ErrInvalidLaneIndex` exists.
//   - **alignment (99)** with slice 3, which is why `ErrAlignmentTooLarge` does.
//   - **GC subtyping (21)** with slice 5, which is why `ErrSubType` does — the first departure this
//     register had to be *authorized* to make (ADR 0031; see the next paragraph).
//   - **the bulk memory/table ops** with slice 5's 0xFC region.
//   - **reference instructions across two slices**, 6 taking the `ref.*`/table half and 8 the rest,
//     which is why that entry could not depart as a unit and why `ErrUndeclaredFunc` arrived one
//     slice before the entry closed.
//   - **exception handling** with slice 10 (`exception.go`, ADR 0036) — the second authorized
//     departure, and the one that closed the whole single-byte opcode space. Its criterion was 25
//     declines and 4 admissions, and it closed at exactly 29 in the all-gates-on lane.
//   - **limits (16)** with **#332**, whose subject was the module-level pass and not this entry:
//     `ErrMemorySize` and `ErrLimitsMinMax` are `check_limits`, and they landed as riders.
//   - **constant expressions (24)** across two packages and neither of them a slice — the decoder's
//     `binary.ErrConstExprRequired` refuses a non-constant instruction in a constant position, and
//     `checkConstGlobals` (#342) is `is_const`'s `GlobalGet` arm. **Two rows of it survive as
//     admissions** and they are named below rather than left inside a departure.
//
// **The last two entries were drained by riders, which is why this register went stale twice over.**
// A departure gets recorded when a *slice* takes an entry, because a slice writes an ADR and quotes
// its criterion; nothing recorded either of these, so the register kept naming two rules that were
// written and two figures that had stopped describing anything. Slice 10 found it while re-measuring
// rather than carrying the surviving figures, which is the only reason it is stated here: a correct
// repair makes its own site look settled, and *nothing was wrong* at either fix site.
//
// # The re-measurement, and it is per lane because the carried figures were not
//
// By expected string over the whole `assert_invalid` population, through the board's own entry point.
// `total` is the corpus population; the rest is where those vectors arrive:
//
//	                                                 default lane          all-gates-on lane
//	constant expression required                     22 pass, 2 gated      22 pass, 2 accepted
//	memory size                                      12 pass, 4 gated      16 pass
//	size minimum must not be greater than maximum     3 pass, 3 gated       6 pass
//
// Both carried figures were *population* sizes — 24 and 16 are still exactly right as populations —
// and that is what made them unreadable as work: the default lane's fail column had nothing from
// either class, so the figures were describing corpus rows rather than debt, and a gated row and a
// passing row are indistinguishable in a total. **The two surviving admissions are
// `array.wast:302,315`**, GC array constant expressions, which the default lane never asks because
// the GC gate is off; they are the whole of the constant-expressions entry that is still owed, and a
// register entry reading "constant expressions (24)" priced them at twelve times their size.
//
// **Those two rows are #471 as of 2026-08-21, and until then they were named here and tracked
// nowhere** — a debt stated in prose is declared, not tracked, and the half that decides whether
// anyone works on it is the tracked half. Filing them also promoted them from two rows to a
// diagnosis: `is_const`'s non-`GlobalGet` arms are answered by the decoder's const-expression reader,
// whose legality test is keyed on the *leading byte* and is called only from the single-byte dispatch
// arm, so the whole prefixed opcode space is const-legal by omission. The two visible rows are a
// lower bound on that population and the population is unmeasured. Recorded here because this
// paragraph is where the next reader will look for what "still owed" costs, and the answer is not two
// rows.
//
// # What replaces the register
//
// **No `assert_invalid` vector on the board is declined.** That is the property this
// register decayed into being a bad approximation of — the residual 8 declines in the validate
// stratum are *module-definition* declines on relaxed SIMD, whose gate is its own event (ADR 0025) —
// and it is what "out of scope" was reaching for when the register was written and the validator had
// one slice. A vocabulary boundary is worth declaring while there is a vocabulary to be outside of;
// once every vector gets a verdict, what is left is admissions, and an admission is not a declaration
// this file can make about itself. `TestTheSingleByteOpcodeSpaceIsFullyTyped` pins the instruction
// half of that closure, and the `assert_invalid` half is `declined: 0` in both rows of
// `TestAssertInvalidDestinationLedgerCloses` — pinned exactly, so it fails if a vector ever declines
// again.
//
// **The pin is the default lane's.** The all-gates-on lane measured 0 declines too when this was
// written, and that half is *unpinned*: the ledger runs one lane, and the figure is stated as a
// measurement rather than a property so a reader does not inherit a guarantee from a sentence. Pinning
// it means a second ledger run and is an instrument's own PR, not this one's rider.
//
// # Slice 5: the subtype relation
//
// The departure above is the one this register had to be *authorized* to make, and the difference is
// worth stating where the register is. Slices 2 and 3 implemented rules already inside the declared
// boundary; this one moved the boundary. It was declared in two places — here, and in `matches`'s own
// doc comment in `stack.go` — and ADR 0031 is the record of retiring both, on the same reasoning
// 0025's carve-out was recorded rather than absorbed. **The 21 are the criterion, not the estimate**:
// the ADR pre-registered all 21 plus the 9 rows of `spec_test.go`'s `moduleOverRejections`, and the
// slice is `match.go` — `match.ml` ported whole — plus `checkTypes` in `module.go`, which is
// `check_subtype_sub` and the phase that runs it.
//
// **One relation, two consumers, and they drain opposite directions.** `matches` at every operand
// comparison is the accept direction: the 9 over-rejections were valid modules this package refused,
// a defect class no board could see before #341 built the arm that asks. `checkTypes` is the reject
// direction: 21 invalid modules accepted, which is the direction this campaign had never had free
// coverage in. That both halves are witnessed is the whole argument for taking it as a slice rather
// than as a repair — and the halves turned out to be *coupled*, since the reject rule's supertype
// comparison is a consumer of the equality the accept rows witness. A port that landed one disjunct
// at a time regressed five previously-passing modules; the measurement is at `matchDefType`.
//
// # Out of scope divides in two, and only one half is a decline
//
// This paragraph claimed all of them were declined: "a module using any of them is not *accepted*
// here — it is refused with ErrUnsupported." The measurement says otherwise, and the distinction
// it forces is worth having.
//
// An out-of-scope rule attached to an *instruction* was declined, because the walk met the
// instruction and had nothing to say about it: 391 of the corpus's `assert_invalid` vectors landed
// that way, each naming its opcode — the board's own `declined:` buckets under the `assert_invalid`
// forms, summed (the bare form's key spells `assert_invalid (module) declined:` since #364),
// which is `validateDeclineCeiling`, not a separately-counted figure that could drift from it.
//
// **That half is empty**, and the tense above is past for that reason rather than deleted, because the
// two-way division is what the paragraph is for and one side of it having drained does not make the
// division wrong. 391 → 30 with slice 5's 0xFC region, 30 → 0 with the reference-type slice (#359) on
// the default lane, and the gated remainder — 25 exception-handling rows the default lane never asked
// — with slice 10. `validateDeclineCeiling` is where the figure lives, and it is **0**: the sentence
// that used to end this paragraph said the ceiling "now bounds the *other* command kinds, since every
// remaining decline board-wide is a module definition on relaxed SIMD", which #427 falsified by typing
// that range — there is no remaining decline of any command kind. It is recorded rather than replaced
// because of how it was found: not by the author of #427 re-reading this file, but by the
// foreclosing-word sweep in the same PR, which flagged the paragraph for resting a claim on a gate
// (`RelaxedSIMD`, on by default) without checking the gate. Two out of three of this shape have now
// been caught by an instrument rather than by a reader.
//
// An out-of-scope rule attached to anything the code-section
// walk never visits is **accepted**, because there is nothing to decline — limits, duplicate
// export names, and constant expressions in globals and segment offsets are not instructions this
// package refuses to type, they are questions it never asks. 104 vectors were accepted for that
// reason; the count is `validateAdmitCeiling` and its own history is two paragraphs down.
//
// Those 104 are the admission stratum, and the harness reports them as **fails with a named
// cause**, not as passes — which is the property the original claim was reaching for and stated
// too strongly. The strong form is worth keeping as a target rather than as a description: an
// accepted-but-invalid module is invisible on any board that scores only refusals, so the arm's
// bucket key carries the cause for every one of them.
//
// Both figures are quoted from `spec_test.go`'s constants rather than counted here, and their
// history is the argument for keeping them apart: **1059 → 391** on the declines, and **142 → 162 →
// 104** on the admissions, where the middle number was a *rise*. The alignment immediate was the one
// out-of-scope rule the code-section walk did visit, so typing the vector region removed the
// accidental cover a decline had been giving it — a module refused because `v128.store8_lane` had no
// rule became a module typed successfully and accepted, the alignment the vector was *about* having
// been dropped by the decoder. Slice 3 is the rule landing, and both faces of it drained: the account
// is at `validateAdmitCeiling`. The one class of admission that survived inside the 104 for the same
// shape of reason — `offset out of range`, a rule never written rather than a rule that could not be
// — is **104 → 103** as of #310, which is the whole of that class the corpus can reach: the other
// three vectors expecting the string are `module quote` forms the wast reader does not build.
//
// # The bounds checks are here because the alternative is a panic, and they are named
//
// Slice 1 declares the index-space rules out of scope, and then performs them anyway wherever
// omitting one would index a slice out of range. `local.get 99` in a two-local function has to
// be answered, and "answered" cannot mean a runtime panic in a package whose job is to decide
// whether a module is safe to run. So ErrUnknownLocal, ErrUnknownGlobal, ErrUnknownLabel,
// ErrUnknownFunc, ErrUnknownType, ErrUnknownTable and ErrUnknownMemory are declared and
// returned. They are *arrivals*, not scope creep, and they are listed here so the forecast can
// attribute the vectors they convert instead of crediting them to the typing rules.
//
// # The authority
//
// `third_party/spec/interpreter/valid/valid.ml`, with subtyping deferred to `valid/match.ml`.
// Both were licensed in `internal/testenv` before this file's first citation, deliberately —
// see RefValidML's comment for why that ordering is not a formality.
package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The declared error set. Messages are the suite's expected strings verbatim, which is the
// error contract decision 0003 fixes: the harness matches what the vector expects, so the
// sentinel *is* the expected string and never a paraphrase of it.
var (
	// ErrTypeMismatch is 84.3% of this campaign's corpus — 2288 of 2714 `assert_invalid`
	// vectors expect exactly this string (#291's census).
	//
	// **Which is why a green on it is not evidence that the right rule fired.** Every operand
	// mismatch, every arity disagreement, and every label-type conflict in the corpus reports
	// this one sentinel, so the suite cannot discriminate inside the family and the reject
	// direction has to be witnessed against valid.ml per rule instead. The `%w`-wrapped detail
	// below exists for that: the harness matches the sentinel, and the wrapped text says which
	// rule refused, for the witnesses to key on.
	ErrTypeMismatch = errors.New("type mismatch")

	// The index-space lookup failures. **One sentinel per category, each spelled `unknown ` +
	// the reference's own category word, and formatted `%w %d` at every site** — because
	// valid.ml does not have nine of these. It has *one*:
	//
	//	let lookup category list x =
	//	  try Lib.List32.nth list x.it with Failure _ ->
	//	    error x.at ("unknown " ^ category ^ " " ^ I32.to_string_u x.it)
	//
	// (`lookup`, `valid/valid.ml:41-42`, with the ten categories at `:44-53`.) So the message is
	// `unknown local 2` — category, space, index, and nothing else before it. That format is
	// not cosmetic: the corpus expects *both* `unknown local` and `unknown local 2`, matched as
	// a prefix per 0003 as amended by ADR 0045, so a message reading `unknown local: local 2`
	// satisfies the first and fails the second while being right about the module. Any detail this validator wants
	// to add goes *after* the index, never between the category and it.
	//
	// TestUnknownCategoriesMatchTheReference derives the category set by parsing those
	// `lookup "…"` lines, in both directions: every sentinel here must be one of the
	// reference's, and the three the reference has that slice 1 does not claim are pinned as a
	// literal set, so a renamed category fails the first check and a new one fails the second.
	ErrUnknownType   = errors.New("unknown type")
	ErrUnknownGlobal = errors.New("unknown global")
	ErrUnknownMemory = errors.New("unknown memory")
	ErrUnknownTable  = errors.New("unknown table")
	ErrUnknownFunc   = errors.New("unknown function")
	ErrUnknownLocal  = errors.New("unknown local")
	ErrUnknownLabel  = errors.New("unknown label")

	// The two segment index spaces, claimed by slice 5 (#9's 0xFC region). They were the
	// `wantUnclaimed` list's second and third entries until this slice landed, and the reason
	// they were on it is the reason they are off it now: nothing *read* a segment index while
	// every instruction that names one was declined at the prefixed-opcode arm, so the deferral
	// was a statement about dispatch rather than about the rules. `tag` remains, and remains a
	// gate's.
	//
	// `data segment` is the one category whose lookup the *decoder* can pre-empt: a module using
	// `memory.init` or `data.drop` without a data count section is malformed, not invalid
	// (`binary.ErrDataCountRequired`), so this sentinel is reached only for an index that
	// overruns a count the module did declare. That is a division of labour with the layer
	// below rather than a gap — see dataSegmentAt.
	ErrUnknownDataSegment = errors.New("unknown data segment")
	ErrUnknownElemSegment = errors.New("unknown elem segment")

	// ErrInvalidResultArity is `select`'s annotation carrying anything other than one type —
	//	`valid.ml:443`, the reference's own text verbatim (0003), *including its parenthetical*.
	//
	// **"not (yet) allowed" is part of the message and is kept**, because the hedge is the
	// reference's own statement about the rule's status: multi-value `select` is a shape the spec
	// has reserved rather than forbidden, and paraphrasing it to "invalid result arity" would make
	// this engine assert a stability the authority declines to. The corpus matches the prefix
	// (`select.wast:369,379` expect `invalid result arity`), so keeping the tail costs nothing and
	// the day the parenthetical disappears upstream is a day this string should move with it.
	//
	// One sentinel, two vectors, and they are the *only* two: arity 0 (`(select (result) …)`) and
	// arity 2 (`(select (result i32 i32) …)`). Both were declines until #294, because the decoder
	// dropped the vector whose length is the entire question.
	ErrInvalidResultArity = errors.New("invalid result arity other than 1 is not (yet) allowed")

	// ErrUninitializedLocal is `local.get` of a local whose init state is still `Unset` —
	// `require (init = Set) x.at "uninitialized local"` (`valid.ml:589-590`), reached only for a
	// **non-defaultable** local, since every defaultable one starts at `Set` — the reference's init
	// rule, which localInitStates implements and cites at the line that states it.
	//
	// **The citation to that rule lives there and not here**, because a doc block documenting a
	// sentinel is checked range-by-range against the message's own site: a second range cited for the
	// *shape* of a neighbouring rule reads to
	// TestReferenceRangeCitationsContainTheirSubjectsSite as a range that has retargeted. That check
	// is block-scoped where this block's claim was per-clause, which is a real limit of it — and the
	// repair that survives it is also the better prose, since the rule now has one home and the home
	// is the code implementing it.
	//
	// So the sentinel's whole population is the function-references non-nullable reference local, and
	// its absence was an *admission*: five corpus vectors asserting `uninitialized local` were red on
	// the all-on lane, four of them modules this package reported valid (#452).
	//
	// **The detail after it names the local and not the rule**, per ErrTypeMismatch's prescription for
	// a family the suite cannot discriminate inside — except that here the family has one member, so
	// the wrap is for a human rather than for a witness. `local %d` repeats `ErrUnknownLocal`'s
	// category-space-index shape deliberately: the two sentinels are the two ways a `local.get` can
	// fail, and a reader comparing them should not have to notice a formatting difference too.
	ErrUninitializedLocal = errors.New("uninitialized local")

	// ErrGlobalImmutable is `global.set` on a non-mutable global.
	//
	// **Its text was `global is immutable` until the probe caught it, and that is a grave in
	// this package's own first hour.** The string appears nowhere in the reference and nowhere
	// in the corpus; `valid.ml:607` says `immutable global`, and the two vectors in
	// `global.wast:286,291` agree with it. sig.go carries a long argument for deriving
	// signatures from the authority instead of transcribing them by hand — and the error
	// strings one file over were transcribed by hand anyway, from memory, with a doc comment
	// asserting the suite said so. The verdict was right and the testimony invented.
	ErrGlobalImmutable = errors.New("immutable global")

	// ErrInvalidLaneIndex is a vector lane immediate outside its shape's lane count —
	// `valid.ml:377,947,954` and the two `check_memop` bounds at `:677,684`, which is **five sites
	// in the reference producing one string**.
	//
	// Those five numbers are the lines the string is *on*, which is what makes the sentence above
	// literal rather than approximate: the third read 953, the `require` whose message continues
	// onto 954. Both resolve to the site, so it was a consistency defect rather than a wrong
	// citation — but a list described as "five sites producing one string" should be the five lines
	// producing it. TestLaneIndexCitationsResolveToTheReferencesSites checks all five, and admits
	// either half of a wrapped `require` so that only a citation pointing outside the site fails.
	//
	// That collapse is the point rather than an inconvenience: `i8x16.shuffle` bounds sixteen
	// indices at 32, `i8x16.extract_lane` bounds one at 16, and `v128.load32_lane` bounds one at
	// 4 — three genuinely different rules, and no vector can tell which refused, because all of
	// them expect the identical `invalid lane index`. So the sentinel is shared (the suite's
	// string, verbatim, per 0003) and the wrapped detail says which bound and which lane, exactly
	// as ErrTypeMismatch's comment prescribes for the 84.3% family. Slice 2 is the first sentinel
	// in this package added *knowing* its suite cannot discriminate inside it.
	ErrInvalidLaneIndex = errors.New("invalid lane index")

	// ErrAlignmentTooLarge is a memarg whose alignment exponent exceeds the access's natural
	// width — `check_memop`'s first `require` (`valid.ml:388`), and the one rule in this package
	// that **could not be written until the decoder kept its input**.
	//
	// That is the whole shape of #306 and it is worth stating beside the sentinel rather than only
	// in align.go: the decoder read the alignment, used it for nothing, and dropped it, so this
	// rule had no operand and 54 invalid modules were reported valid — sixteen of them in slice 2,
	// which is the raise `validateAdmitCeiling` spent one PR carrying under Scott's stamp. A
	// missing sentinel is a *quiet* accept: there is no wrong message for a vector to disagree
	// with, so `authority_test.go`'s message-match can no more see it than the count could.
	//
	// One sentinel for 62 vectors, and none of them can tell which row refused — 42 in
	// `align.wast`, 12 in `simd_align.wast`, 8 in the lane files. So the shared-sentinel,
	// detailed-wrap arrangement is ErrInvalidLaneIndex's above, adopted for the identical reason.
	ErrAlignmentTooLarge = errors.New("alignment must not be larger than natural")

	// ErrOffsetOutOfRange is a memarg whose static offset does not fit a 32-bit memory's address
	// space — `check_memop`'s third and last `require` (`valid.ml:392`), which completes the
	// function. The alignment rule above needed the decoder to stop dropping its operand; this one
	// needed nothing but writing, which is the less flattering of the two reasons a rule is absent.
	//
	// Four corpus vectors expect this string and exactly one of them reaches this package today
	// (`align.wast:1004`; the other three are `module quote` and sit in the unsupported column), so
	// the reward was a single admission before the work rather than after it. Written down in
	// advance on #310 for that reason: a one-vector rule is worth doing because it closes
	// `check_memop`, not because it moves the board, and those are different justifications that a
	// board delta cannot tell apart.
	//
	// **It reads the memory the instruction names, which the reference does not** — see
	// `checkOffset` for the ruling, and for the three-part condition under which the divergence
	// becomes observable at all.
	ErrOffsetOutOfRange = errors.New("offset out of range")

	// ErrSubType is `check_subtype_sub`'s two `require`s about a declared supertype relationship
	// — `valid.ml:170-174`, the finality rule and the relation itself.
	//
	// **The sentinel is the corpus's own prefix and the index sits inside the message**, which
	// is the one place this family's format differs from the `unknown ` categories above. The
	// reference's strings are `"sub type " ^ x ^ " has final super type " ^ xi` and `"sub type " ^
	// x ^ " does not match super type " ^ xi`, so the discriminating words come *after* an index
	// rather than after the sentinel. All 21 vectors expect the bare `sub type`, which is
	// ErrTypeMismatch's arrangement exactly: the harness matches the sentinel, the wrapped text
	// says which of the two rules refused, and no vector can tell them apart.
	//
	// One sentinel for both rules because the suite cannot discriminate, not because they are one
	// rule: four of the 21 are finality (a `sub` naming a `sub final` supertype) and seventeen are
	// the relation. Splitting the sentinel would invent a distinction the authority's own strings
	// do not carry — and `match.go` is where the seventeen are actually decided.
	ErrSubType = errors.New("sub type")

	// ErrForwardTypeUse is `check_subtype_sub`'s first `require` — a subtype naming a supertype at
	// a *higher* index than its own (`valid.ml:169`, `"forward use of type " ^ xi ^ " in sub type
	// definition"`).
	//
	// A separate sentinel from ErrSubType, and the reason is the reference's own word order rather
	// than a judgement about how related the rules are: this message does not begin with `sub
	// type`, so `%w`-wrapping ErrSubType could not produce it. It does *contain* the words the
	// corpus matches, which under the harness's old substring rule satisfied the vectors expecting
	// `sub type` either way — a coincidence of phrasing worth naming, because it means this
	// sentinel's own reward figure is zero and its justification is completing the function.
	//
	// **ADR 0045 removed the coincidence and neither board moved**, which is the measurement saying
	// none of the 21 was taking this path. It could not have been: the reference renders this rule
	// with this word order and matches by prefix too, so a vector expecting `sub type` that arrived
	// here would fail against the reference itself. The slack was the harness's alone.
	//
	// **It is also the property that makes the supertype walk terminate**, and that is not a
	// coincidence: `xi < x` is what makes every declared chain strictly decreasing. See
	// matchDeclaredSupertypes, which carries a depth bound anyway because it is reachable while
	// this rule is still being established.
	ErrForwardTypeUse = errors.New("forward use of type")

	// ErrStartFunction is `check_start`'s only `require` — a start function with a parameter or a
	// result (`valid.ml:1113-1114`, the reference's text verbatim per 0003).
	//
	// **The corpus expects `start function`, which is a prefix of this and not a paraphrase of it**,
	// so the prefix match of 0003 as amended by ADR 0045 is satisfied by keeping the whole sentence — the same
	// arrangement ErrAlignmentTooLarge has, and for the same reason: the reference's words say
	// *which* rule refused where the vector's three cannot.
	//
	// Two of `start.wast`'s three `assert_invalid` vectors expect it (a start with a result at
	// `:7`, one with a param at `:14`); the third, `(module (func) (start 1))` at `:2`, expects
	// `unknown function` and is ErrUnknownFunc's, arriving through `funcTypeIndexIn` before this
	// rule is reached. That split is `check_start`'s own order — `func c x` resolves before the
	// `require` runs — and it is why the start rule converts two vectors rather than three.
	ErrStartFunction = errors.New("start function must not have parameters or results")

	// ErrUnsupported is slice 1 declining an instruction whose rules belong to a later slice.
	//
	// **A decline, and deliberately not an accept.** The alternative — treating an unknown
	// opcode as a no-op and validating the rest — would report *valid* for modules this package
	// has not type-checked, which is the accept-direction failure no board can see (contract §9
	// G-3) and the exact condition 0025's carve-out was written around. A refusal lands in a
	// named bucket and stays visible; a silent accept becomes an unearned pass.
	ErrUnsupported = errors.New("validator: instruction not in this slice")
)

// Info is what the validator hands forward, and it is the reason this pass is not a predicate.
//
// #9's own statement of scope assigns block arity metadata here: the internal form needs, for
// every branch target, how many values the branch carries and how deep it unwinds to. The
// validator computes both while type-checking — the label types *are* the branch's arity — so
// recomputing them in the interpreter would be a second derivation of one fact.
//
// No ADR records this shape, by ruling: 0002 Q3 already decides the substance (validation is
// the type oracle), and a second record restating a decided rule creates two citable
// authorities for it, so the next correction has to find both. If Q3's text turns out not to
// cover the return form cleanly, the move is an appended note on 0002 — the resolution 0028
// took — and not a new record. (Ruling: chat-Claude, on the #290 relay; Scott's veto standing.)
type Info struct {
	// Funcs is indexed by position in Module.Funcs, not by function index — the imported
	// functions have no body to describe.
	Funcs []FuncInfo
}

// FuncInfo carries one function body's validated metadata.
type FuncInfo struct {
	// Blocks maps the instruction index of a block opener (`block`, `loop`, `if`) to the arity
	// of its label. Keyed by index into Func.Body, which is the internal form's own coordinate.
	Blocks map[int]Arity

	// MaxStack is the greatest operand-stack depth the body reaches, in slots. The frame
	// allocator's input: 0024 makes a v128 two slots, so this counts slots and not values.
	MaxStack int
}

// Arity is what a branch to a label carries and what the block yields at its end.
//
// Two fields rather than one because `loop` is the case that makes them different: a branch to
// a loop's label re-enters it and therefore carries the loop's *parameters*, while falling off
// the end yields its *results*. A single "arity" would be right for `block` and wrong for
// `loop`, which is the kind of wrong that passes every `block`-shaped vector.
type Arity struct {
	Label int // values a `br` to this label carries
	End   int // values the block leaves on the stack when it completes
}

// Module validates m's module-level rules and every function body, and returns the metadata the
// internal form needs.
//
// **The module-level phase runs first, because the reference's does** (`check_module`, valid.ml:1151)
// — every module-level rule is checked before any body is walked, so a module with a bad memory
// limit *and* a bad body reports the limit. See module() for the phase list, which rules of it this
// slice implements, and why the remaining ones are named there in order rather than omitted.
//
// This sentence used to read "the module-level rules … are later slices'; this walks the code
// section." Limits and the data segments' memory index stopped being later slices' here; the rest
// of that list still is, and module()'s table is now where it is recorded.
func Module(m *binary.Module) (*Info, error) {
	// `check_module`'s own first line, before the context is built and therefore before any rule at
	// all (`valid.ml:1152`). Computed here rather than in funcBody because it is the module's
	// property: see declaredFuncs on why a body cannot contribute to the set it is checked against.
	//
	// **It moved above modulePre when `checkConst` began typing constant expressions**, which is the
	// reference's position for it and not a convenience: a global initializer may hold `ref.func`, so
	// the module-level phase now has a consumer for the set that the code-section walk used to be the
	// only one of. `ref_func.wast:68` is the vector, and computing refs after modulePre would have
	// meant either passing an empty set — every `ref.func` in a const expr an undeclared reference —
	// or computing it twice.
	refs := declaredFuncs(m)
	if err := modulePre(m, refs); err != nil {
		return nil, err
	}
	info := &Info{Funcs: make([]FuncInfo, len(m.Funcs))}
	for i := range m.Funcs {
		fi, err := funcBody(m, &m.Funcs[i], refs)
		if err != nil {
			return nil, fmt.Errorf("%w (func %d)", err, i)
		}
		info.Funcs[i] = fi
	}
	// The start section, between the bodies and the exports, which is `check_module`'s own position
	// for it (valid.ml:1166). See moduleStart.
	if err := moduleStart(m); err != nil {
		return nil, err
	}
	// `check_module` checks the exports *last*, after every body (valid.ml:1168-1169). See
	// moduleExports on why that placement is observable and therefore not ours to tidy.
	if err := moduleExports(m); err != nil {
		return nil, err
	}
	return info, nil
}

// funcBody type-checks one body against its declared type.
func funcBody(m *binary.Module, f *binary.Func, refs map[uint32]bool) (FuncInfo, error) {
	ft, err := funcType(m, f.TypeIndex)
	if err != nil {
		return FuncInfo{}, err
	}

	// `List.map (check_local c) ls` (valid.ml:1021), whose body is `check_valtype c t` before it
	// decides `Set`/`Unset` (valid.ml:1007-1011) — so a body declaring `(local (ref 7))` in a module
	// with three types is `unknown type 7` and not a type mismatch downstream.
	//
	// **Per group rather than per local, and that is the same predicate rather than a shortcut**:
	// `check_local`'s only argument is the type, a `LocalGroup` is a run of one type
	// (`(count, valtype)`, per grave #138's retention), so checking the run's type once decides every
	// local in it. A group with `Count == 0` is a legal encoding that declares nothing, and the
	// reference does not visit it at all — the check runs anyway, because the type byte was still read
	// and a scope violation in it is still in the image. No vector distinguishes the two, which is why
	// this sentence exists rather than a branch.
	//
	// The scope is the whole type space: `check_func_body` runs at valid.ml:1165, long after
	// `check_module`'s type phase has folded every rec group in, which is the same reason
	// `validator.globalScope` below is `len(m.Globals)` and not an index.
	for i, g := range f.Locals {
		if terr := checkValTypeScoped(len(m.Types), g.Type); terr != nil {
			return FuncInfo{}, fmt.Errorf("%w (local group %d)", terr, i)
		}
	}

	locals, err := localTypes(ft, f)
	if err != nil {
		return FuncInfo{}, err
	}

	v := &validator{
		mod:       m,
		curFunc:   f,
		locals:    locals,
		localInit: localInitStates(len(ft.Params), locals),
		blocks:    map[int]Arity{},
		refs:      refs,
		// The body's own cast-family side table. Nil for a body that has none, which is
		// indistinguishable from an absent row and is exactly what castVector's second result
		// is for.
		casts: f.Casts,
		// Every global is in scope in a body: `check_func_body` runs at valid.ml:1165, after
		// :1162 has folded the last one in. See validator.globalScope.
		globalScope: len(m.Globals),
	}
	// The body's own frame. Its label types are the function's results, which is what makes a
	// bare `return` and a `br` to the outermost label the same check.
	//
	// **This call passed `nil` for the label types until the probe ran, under that exact
	// comment.** With nil there, `return` pops nothing: every one of func.wast's 30-odd
	// `type-return-*` vectors was *accepted*, because the check that was supposed to reject them
	// read an empty requirement and agreed. The comment stating the property the code lacked is
	// the whole grave — review reads the sentence, the sentence is right, and the argument list
	// two characters away is not.
	v.pushFrame(opFuncBody, ft.Results, ft.Results)

	if err := v.instrs(f.Body); err != nil {
		return FuncInfo{}, err
	}
	// **The result arity was checked here too, and checking it twice was worse than either
	// place.** The decoder keeps the body's terminating `end` as an instruction, so endBlock
	// already ran the check on the body frame; repeating it against the stack that check had just
	// emptied rejected every valid non-void function (`(func (result i32) (i32.const 1))` →
	// "expected i32, stack empty", the wording of the time; the same failure prints
	// `instruction requires [i32] but stack has []` since #394) *and* accepted every body ending
	// in `return`, whose frame is
	// unreachable by then and so satisfies any second demand. One bug in each direction from one
	// duplicated check.
	//
	// So the arity check lives at `end` alone, and what is left here is the structural question
	// that has no other home: every frame the body opened must have been closed.
	if len(v.frames) != 0 {
		return FuncInfo{}, fmt.Errorf("%w: %d block(s) still open at the end of the body",
			ErrTypeMismatch, len(v.frames))
	}
	return FuncInfo{Blocks: v.blocks, MaxStack: v.maxStack}, nil
}

// funcType resolves a type index to its function type.
func funcType(m *binary.Module, idx uint32) (binary.FuncType, error) {
	if idx >= uint32(len(m.Types)) {
		return binary.FuncType{}, fmt.Errorf("%w %d (%d in scope)", ErrUnknownType, idx, len(m.Types))
	}
	ct := m.Types[idx]
	if ct.Kind != binary.CompFunc {
		// A struct or array type where a function type is wanted. The suite's string for this
		// is `type mismatch`, not `unknown type` — the index resolves, the *kind* is wrong.
		return binary.FuncType{}, fmt.Errorf("%w: type %d is a %s, want func", ErrTypeMismatch, idx, ct.Kind)
	}
	return ct.Func, nil
}

// localTypes flattens the parameters and the declared local groups into one indexable slice.
//
// **The groups are not pre-expanded, and that is grave #138's fix being load-bearing here.**
// `Func.Locals` holds `(count, valtype)` runs precisely because a body may legally declare
// 0xFFFFFFFE locals, and expanding that into one entry per local turns a 30-byte image into
// 4 GiB. So this function is where the sum is bounded: the reference's own check is
// `total >= 1<<32`, and a body that passes it can still declare more locals than any machine
// will hold. Slice 1 refuses to materialize a slice it cannot afford, and says which limit it
// hit rather than being OOM-killed with no testimony.
func localTypes(ft binary.FuncType, f *binary.Func) ([]binary.ValType, error) {
	total := uint64(len(ft.Params))
	for _, g := range f.Locals {
		total += uint64(g.Count)
	}
	if total >= 1<<32 {
		// The reference's verdict (`too many locals`), which the decoder already enforces —
		// reached here only if a caller hands over a Func built by something else.
		return nil, fmt.Errorf("%w: %d locals declared", ErrTypeMismatch, total)
	}
	if total > maxMaterializedLocals {
		return nil, fmt.Errorf("%w: %d locals declared, this validator materializes at most %d",
			ErrTypeMismatch, total, maxMaterializedLocals)
	}

	locals := make([]binary.ValType, 0, total)
	locals = append(locals, ft.Params...)
	for _, g := range f.Locals {
		for range g.Count {
			locals = append(locals, g.Type)
		}
	}
	return locals, nil
}

// localInitStates is `check_local` reduced to its init half: `let init = if defaultable t then Set
// else Unset` (`valid.ml:1010-1011`), over the flat slice localTypes built, with the first
// `numParams` entries `Set` because an argument arrives with a value.
//
// **The parameter count is passed rather than recomputed**, since the flattened slice cannot say
// where the parameters stop — `localTypes` is the only place that boundary exists, and handing the
// number over is cheaper than a second traversal that would have to agree with it.
//
// The reference has no separate parameter rule: `check_func` builds the context as `ts1 @ List.map
// (check_local c) ls` where `ts1` are the parameters, already `LocalT (Set, t)` by construction
// (`valid.ml:1021-1024`). So "parameters start initialized" is a property of how the context is
// assembled there and a loop bound here, which is the same fact and worth saying once.
func localInitStates(numParams int, locals []binary.ValType) []bool {
	init := make([]bool, len(locals))
	for i, t := range locals {
		init[i] = i < numParams || defaultable(t)
	}
	return init
}

// maxMaterializedLocals bounds what localTypes will allocate.
//
// A number here rather than "whatever the allocator survives", because the failure mode being
// avoided has no testimony: an OOM kill produces no error, no bucket, and no board row. One
// million locals is four orders of magnitude past any real function and still a 16 MB slice.
const maxMaterializedLocals = 1 << 20
