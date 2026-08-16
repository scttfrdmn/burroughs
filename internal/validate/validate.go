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
// file's own header; what matters at this level is that the region is no longer declined, so
// `select t` (#294) is the last instruction in the single-byte space slice 1 left, and the other
// three prefixed regions (0xFB GC, 0xFC bulk, 0xFE threads) are the ones still dispatched to a
// decline.
//
// Out of scope by declaration, each with its own expected string in the suite and so its own
// measurable slice: alignment (99 vectors, and slice 2 raised its stake — see below and #306), GC
// subtyping (21), constant expressions (24), limits (16), reference instructions, the bulk
// memory/table ops, and exception handling. SIMD lane immediates (48) were on this list until
// slice 2, and are the reason `ErrInvalidLaneIndex` exists.
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
// export names, constant expressions in globals and segment offsets, and the alignment immediate
// (which the walk does visit, but whose check is a later slice's) are not instructions this
// package refuses to type, they are questions it never asks. 162 vectors are accepted for that
// reason.
//
// Those 162 are the admission stratum, and the harness reports them as **fails with a named
// cause**, not as passes — which is the property the original claim was reaching for and stated
// too strongly. The strong form is worth keeping as a target rather than as a description: an
// accepted-but-invalid module is invisible on any board that scores only refusals, so the arm's
// bucket key carries the cause for every one of them.
//
// Both figures moved with slice 2 and both are quoted from `spec_test.go`'s constants rather than
// counted here — 1059 → 391 and 142 → 162, the second a *rise*, whose twenty vectors are the
// alignment immediate this paragraph already names as the one out-of-scope rule the walk does visit.
// Typing the vector region removed the accidental cover a decline was giving them: the module used
// to be refused because `v128.store8_lane` had no rule, and it is now typed successfully and
// accepted, because the alignment the vector is *about* was dropped by the decoder. The account is
// at `validateAdmitCeiling`; the slice that fixes it is #306.
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
	// (`valid/valid.ml:41-42`, with the ten categories at `:44-53`.) So the message is
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

// Module validates every function body in m and returns the metadata the internal form needs.
//
// The module-level rules (limits, index-space well-formedness, element/data segment types,
// start-function signature) are later slices'; this walks the code section.
func Module(m *binary.Module) (*Info, error) {
	info := &Info{Funcs: make([]FuncInfo, len(m.Funcs))}
	for i := range m.Funcs {
		fi, err := funcBody(m, &m.Funcs[i])
		if err != nil {
			return nil, fmt.Errorf("func %d: %w", i, err)
		}
		info.Funcs[i] = fi
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
