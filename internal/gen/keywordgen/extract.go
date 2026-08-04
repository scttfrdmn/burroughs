// Package keywordgen extracts the wat keyword table from the reference interpreter's
// text lexer.
//
// # Why this exists
//
// The same argument as decision 0007, one grammar over. The keyword table is 589 facts
// of the form "`i32.add` is a token and `i32.wrap` is not", and the spec suite cannot
// check the accept half of any of them: a keyword the table wrongly *omits* produces
// `unknown operator <mnemonic>` on a module the spec calls well-formed, and no vector
// asserts that a valid mnemonic lexes, because valid mnemonics appear inside modules
// that are asserted *valid* as a whole. So a missing keyword surfaces as a failure
// somewhere downstream, attributed to whatever consumed the wrong token — invisible on
// the board as a table defect, exactly contract §9 G-3's shape.
//
// The reject half is oracle-covered, and generously: 555 vectors expect the bare string
// `unknown operator`, and 11 expect it with a mnemonic appended. But a table containing
// *nothing at all* would pass all 566 of those and fail every valid module, which is the
// asymmetry 0007 was written about. Hand-transcribing 589 mnemonics is not on the menu
// either; this repo's measured hand-transcription rate is seven wrong citations in
// twelve items.
//
// So the authority is read by a machine, and it is the same authority: the reference
// interpreter, at the same pinned revision, read from
// third_party/spec/interpreter/text/lexer.mll.
//
// # Why parsing ocamllex text is acceptable here, and why it is *easier* than decode.ml
//
// The keyword block is 589 consecutive arms of one shape, `| "<literal>" -> <rhs>`, with
// no alternations, no computed arm heads, and no head that wraps across lines — measured
// before this package was written, which is what made 8a standalone work rather than a
// subsection of the lexer. Compare decode.ml, where an arm head can be
// `| 0xc5 | ... | 0xcb` continued on the next line and a constructor can be chosen by an
// `if opcode = ...`. The head grammar here is a string literal; there is nothing to
// disambiguate.
//
// What this package inherits unchanged from opcodegen is the property that makes the
// extraction trustworthy: it **never skips a line it does not understand**. An
// unrecognized arm inside the block is a hard error, not an omission. And because that
// error cannot fire when *nothing* looks like an arm, the vacuity floor is what covers a
// moved file or a changed layout — see Extract.
//
// # What this package deliberately does not extract
//
// The lexer's token *constructors* (`BINARY i32_add`, `VEC_LOAD (fun x a o -> ...)`) are
// read only far enough to name the token kind. Their argument expressions are OCaml
// values in the reference's own AST vocabulary, and transcribing those would be
// transcribing the reference's *parser*, not its lexer. The keyword→kind mapping is the
// fact the future wat lexer needs; the instruction each kind builds is the parser's
// business and will be written against the spec, not scraped.
package keywordgen

import (
	"errors"
	"regexp"
)

// Kind is the parser token a keyword lexes to, named as the reference names it.
//
// The names are the reference's own (`BINARY`, `VEC_LOAD`, `NUMTYPE`) rather than
// Burroughs' — this table is a transcription of an authority, so it speaks the
// authority's vocabulary and the translation happens at the point of use. A renaming
// here would be an editorial change to evidence. Same posture as opcodegen's Imm.
//
// Unlike Imm, Kind is **open**: the vocabulary is whatever the reference's parser
// declares, 160-odd constructors at the pinned revision, and closing it would mean
// maintaining a list whose only authority is the file being read. What is closed is the
// *arm shape*, which is where an unrecognized line has to be caught.
type Kind string

// Arm is one keyword: the literal that lexes, and the token it lexes to.
type Arm struct {
	// Keyword is the literal string from the arm's head — the token text itself.
	Keyword string
	// Kind is the parser token constructor the arm returns.
	Kind Kind
	// Line is the 1-indexed line in lexer.mll this arm was read from, so a generated
	// row can be audited against the authority without a search.
	Line int
}

// errUnrecognized is the load-bearing error: it is what makes this extraction different
// from a careful reading.
//
// It fires when a line inside the keyword block is neither an arm this package
// understands nor a recognized continuation — but it cannot fire when nothing looks like
// an arm at all. A moved file, a renamed rule, or a wholesale refactor produces zero arms
// and zero unrecognized lines, and a drift check comparing an empty table to an empty
// table agrees perfectly. Extract's vacuity floor is that case's control.
var errUnrecognized = errors.New("unrecognized arm in lexer.mll")

// ErrVacuous reports an extraction that produced implausibly little. See Extract.
var ErrVacuous = errors.New("extraction produced too few keywords")

// The arm shape and the block's delimiters used to live here, in four regexps this package
// declared for itself. They are `mllex`'s now — the wrapped-arm shape's third occurrence
// un-froze the tooling and Scott's consolidation clause moved the substrate to one reader
// (see mllex's package doc). What is left is this generator's own grammar over an arm's
// body, which is genuinely per-consumer: `opgen` mines the same bodies for lowercase
// identifiers and `memarggen` for `(opt a N)`.
var (
	// reKind takes the token constructor: the first capitalised identifier in the body.
	// `BINARY i32_add` → BINARY; `VEC_LOAD (fun x a o -> ...)` → VEC_LOAD;
	// `PACKTYPE Types.I8T` → PACKTYPE.
	reKind = regexp.MustCompile(`^\s*([A-Z][A-Za-z0-9_]*)`)
	// reKeywordShape is the `keyword` production ocamllex matched *before* this block ran.
	// See checkShape for why an arm head outside it is the reference having a bug.
	reKeywordShape = regexp.MustCompile(`^[a-z][a-zA-Z0-9_.:]+$`)
)
