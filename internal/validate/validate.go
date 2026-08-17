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
// 1 left, which was true when it was written and is not now — slice 4 took it. **The single-byte
// opcode space is fully in vocabulary as of that slice**, and what remains declined is the three
// prefixed regions: 0xFB (GC), 0xFC (bulk memory/table), 0xFE (threads).
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
// It is also the slice that closes the single-byte space, and the last one whose rule was blocked on
// a *different package*. What blocked the two rules named here next was this package itself: #311's
// `check_valtype` on a block's valtype annotation was a call the walk never made, and #310's
// `offset out of range` was a `require` never written. **Both are now written**, and the tense is
// corrected in place rather than the sentence deleted, because *which* rules this package was the
// blocker for is the durable half of it.
//
// Out of scope by declaration, each with its own expected string in the suite and so its own
// measurable slice: GC subtyping (21), constant expressions (24), limits (16), reference
// instructions, the bulk memory/table ops, and exception handling. Two entries have left this list
// and are worth naming as departures rather than deletions — SIMD lane immediates (48) with slice 2,
// which is why `ErrInvalidLaneIndex` exists, and alignment (99) with slice 3, which is why
// `ErrAlignmentTooLarge` does.
//
// # Out of scope divides in two, and only one half is a decline
//
// This paragraph claimed all of them were declined: "a module using any of them is not *accepted*
// here — it is refused with ErrUnsupported." The measurement says otherwise, and the distinction
// it forces is worth having.
//
// An out-of-scope rule attached to an *instruction* is declined, because the walk meets the
// instruction and has nothing to say about it: 391 of the corpus's `assert_invalid` vectors land
// that way, each naming its opcode — the board's own `assert_invalid declined:` buckets summed,
// which is `validateDeclineCeiling`, not a separately-counted figure that could drift from it. An out-of-scope rule attached to anything the code-section
// walk never visits is **accepted**, because there is nothing to decline — limits, duplicate
// export names, and constant expressions in globals and segment offsets are not instructions this
// package refuses to type, they are questions it never asks. 104 vectors are accepted for that
// reason.
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
	// a substring per 0003, so a message reading `unknown local: local 2` satisfies the first
	// and fails the second while being right about the module. Any detail this validator wants
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
	// this engine assert a stability the authority declines to. The corpus matches the substring
	// (`select.wast:369,379` expect `invalid result arity`), so keeping the tail costs nothing and
	// the day the parenthetical disappears upstream is a day this string should move with it.
	//
	// One sentinel, two vectors, and they are the *only* two: arity 0 (`(select (result) …)`) and
	// arity 2 (`(select (result i32 i32) …)`). Both were declines until #294, because the decoder
	// dropped the vector whose length is the entire question.
	ErrInvalidResultArity = errors.New("invalid result arity other than 1 is not (yet) allowed")

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
	if err := modulePre(m); err != nil {
		return nil, err
	}
	info := &Info{Funcs: make([]FuncInfo, len(m.Funcs))}
	for i := range m.Funcs {
		fi, err := funcBody(m, &m.Funcs[i])
		if err != nil {
			return nil, fmt.Errorf("func %d: %w", i, err)
		}
		info.Funcs[i] = fi
	}
	// `check_module` checks the exports *last*, after every body (valid.ml:1168-1169). See
	// moduleExports on why that placement is observable and therefore not ours to tidy.
	if err := moduleExports(m); err != nil {
		return nil, err
	}
	return info, nil
}

// funcBody type-checks one body against its declared type.
func funcBody(m *binary.Module, f *binary.Func) (FuncInfo, error) {
	ft, err := funcType(m, f.TypeIndex)
	if err != nil {
		return FuncInfo{}, err
	}

	locals, err := localTypes(ft, f)
	if err != nil {
		return FuncInfo{}, err
	}

	v := &validator{
		mod:     m,
		curFunc: f,
		locals:  locals,
		blocks:  map[int]Arity{},
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
	// "expected i32, stack empty") *and* accepted every body ending in `return`, whose frame is
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

// maxMaterializedLocals bounds what localTypes will allocate.
//
// A number here rather than "whatever the allocator survives", because the failure mode being
// avoided has no testimony: an OOM kill produces no error, no bucket, and no board row. One
// million locals is four orders of magnitude past any real function and still a 16 MB slice.
const maxMaterializedLocals = 1 << 20
