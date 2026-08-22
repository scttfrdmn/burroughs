package text

import "unicode/utf8"

// The UTF-8 decode sites, and the whole reason #62 built a control before a parser.
//
// The reference decodes UTF-8 in exactly **two** helpers, both in parser.mly's header:
//
//	let name s loc = try Utf8.decode s with Utf8.Utf8 -> error (at loc) "malformed UTF-8 encoding"   (:46-47)
//	let var s loc  = ... try ignore (Utf8.decode s); ... with Utf8.Utf8 -> error r "..."             (:49-52)
//
// and nowhere else in the module grammar. In particular `string_list` (:342) concatenates
// raw bytes with no decode at all, which is why `(data "\ef\ff\fe")` is **legal** and a
// blanket "reject invalid UTF-8 in string tokens" check is wrong about the grammar.
//
// That wrong check would pass all 176 `utf8-invalid-encoding.wast` vectors and 177/177 of
// the suite's invalid-UTF-8 tokens inside quote forms, because the suite's accept
// direction here is **empty** — measured, 0 sites. No vector can kill the mutation. The
// controls in utf8position_test.go are what stand in for the oracle: the grammar facts are
// pinned against vendored parser.mly, and the accept direction is pinned at LexAll.
//
// So: the decode goes *here*, at the two positions, and not in the lexer. The wrong fix was
// attempted once already in the lexer's `emitVarString` (PR #60).

// decodedName validates a `name` position — an import/export name — and returns its bytes.
//
// Named for the reference's helper. **It used to return only an error, and the paragraph that
// explained why is kept because the prediction in it came true rather than because it is still
// the rule.** The first draft returned `([]byte, error)` reasoning that a wat name is a byte
// sequence and the eventual binary encoder wants the bytes; `unparam` pointed out that no caller
// read them, and the lint was right — designing a return from a consumer that does not exist is
// the move 0011 declined for the module type, one scale down. The paragraph then said: *"When
// 0011's second half arrives the bytes come back, from `t.Value`, which is where they already
// live."* That is this change (#8's import section): the consumer exists, it is `encodeImports`,
// and the value comes from exactly the field named.
//
// A `string` rather than `[]byte`, and that is the encoder's requirement rather than a
// preference: `writer.name` takes a string, `binary.Import` holds strings, and the conversion is
// the **copy** that `decodeImport`'s comment argues for on the other side — a name outlives the
// token slice it was lexed from, so a view would make a module's identity depend on the source
// buffer not being reused.
//
// It checks t.Value, the *decoded* bytes, for the reason spelled out on decodedVar below: the
// reference's `name` receives the unescaped string, and Text would be the source spelling with
// its escapes intact and therefore always valid.
func decodedName(t Token) (string, error) {
	if !utf8.Valid(t.Value) {
		return "", errAt(t, "malformed UTF-8 encoding")
	}
	return string(t.Value), nil
}

// decodedVar validates a `var` position — an identifier, including the quoted `$"..."`
// form — and returns its text.
//
// A separate function from decodedName despite the identical check and message, because
// they are separate *positions* and the partition is the point: #62's control asserts two
// sites, and one function called from two places is one site with two callers. If the two
// ever diverge upstream (the reference's `var` already differs in returning the source
// slice rather than the decoded value), the divergence lands in the function that owns the
// position rather than in a shared helper's new parameter.
//
// Both report `malformed UTF-8 encoding`, the reference's message at both sites. The board
// carries a separate 1-vector `malformed UTF-8` bucket, and that is the *vector's expected
// string* (`id.wast:31`), matched by substring — not a second message. Two vectors want the
// short string and they sit at different layers: `annotations.wast:79` is already answered
// by the lexer, and `id.wast:31` is this one.
//
// **It checks Value, not Text**, and the difference is the whole vector. The reference's `var`
// receives the *unescaped* string — `'$'(string as s) { let s' = string s in ... VAR s' }`
// (lexer.mll:816-818) — so `Utf8.decode` runs over decoded bytes. Text is the source spelling,
// which for `$"\ef"` is the seven ASCII characters `$`, `"`, `\`, `e`, `f`, `"` and is *always*
// valid UTF-8: the check against it can never fail, and this whole site was a production that
// could not fire. The first draft did exactly that and the reject sweep caught it on
// `id.wast:31`'s vector — the one vector in the suite that reaches here, and the reason a
// one-vector bucket is still worth a case. Value is also the right *key* for the duplicate
// check, so `$"foo"` and `$foo` are one identifier, as the reference's VarMap has them.
func decodedVar(t Token) (string, error) {
	if !utf8.Valid(t.Value) {
		return "", errAt(t, "malformed UTF-8 encoding")
	}
	return string(t.Value), nil
}

// spaceKind identifies an index space, and carries the one word the reference's messages use
// for it.
//
// **The word is a property of the space, not an argument at the call site**, and that is a
// repair rather than a tidy-up. The reference has exactly one category literal per space,
// written twice — `bind_abs "function" c.funcs` at parser.mly:192 and `lookup "function"
// c.funcs` at :157 — so bind and lookup cannot disagree about a space's name there without
// someone editing two adjacent lines differently. Here the word was a `string` parameter
// threaded through `bindidxOpt` from twelve call sites, which made disagreement a typo away,
// and **three of the twelve were already wrong**: `func` for `function`, `data` for `data
// segment`, `elem` for `elem segment` (grave #120). Adding the *lookup* direction for
// `externidx` would have doubled the sites and re-run the same risk, so the word moved to
// where it can only be written once.
//
// Why the divergence was invisible: the harness matches expected strings by substring
// (decision 0003), and `func.wast:966`'s expected `"duplicate func"` is a *prefix* of the
// reference's actual `duplicate function $foo`. So the three suite vectors that touch this
// message score pass under either word, and no vector exists at all for `duplicate data` or
// `duplicate elem` — the accept-shaped blind spot of contract §9 G-3, in the half of a
// message the oracle never reads. Sibling of grave #36, where the verdict was right and the
// testimony was fabricated.
// The zero value is deliberately **not** a space. `context` is created as a zero value
// (`&parser{c: c}`), so if `spaceType` sat at ordinal 0 then a space whose kind was never set
// would silently claim to be the type space and print `duplicate type $f` for a func — the
// wrong-word defect again, reintroduced by an omission instead of a typo. spaceUnset makes that
// omission loud: `String` renders it as a marker no reference message contains, and
// TestEverySpaceHasItsKind walks context's fields by reflection so a *newly added* space with no
// kind fails too (scope the control to the space, not to today's fields).
type spaceKind int

const (
	spaceUnset spaceKind = iota
	spaceType
	spaceTag
	spaceGlobal
	spaceMemory
	spaceTable
	spaceFunc
	spaceData
	spaceElem
	spaceLocal
	spaceField
	spaceLabel
)

// String is the reference's category word, verbatim, for the `duplicate <category> <name>` and
// `unknown <category> <name>` messages.
//
// Transcribed from the reference's `bind_*` and `lookup` helper pairs (parser.mly:152-163 and
// :187-198), whose words agree per space for all nine absolute spaces. Two do not agree, and
// both are the reference's own trailing-space quirk on the *lookup* side only — `lookup "label
// "` (:161) and `lookup "field "` (:163) against `bind_rel "label"` (:196) and `bind_abs
// "field"` (:198), rendering `unknown label  $l` with two spaces. That asymmetry is honoured
// where it lives rather than here: lookupLabel writes the doubled space itself, and the field
// space has no lookup in this stratum. See TestSpaceKindWordsMatchTheReference, which pins all
// eleven against the authority.
func (k spaceKind) String() string {
	switch k {
	case spaceUnset:
		return "<space kind unset>"
	case spaceType:
		return "type"
	case spaceTag:
		return "tag"
	case spaceGlobal:
		return "global"
	case spaceMemory:
		return "memory"
	case spaceTable:
		return "table"
	case spaceFunc:
		return "function"
	case spaceData:
		return "data segment"
	case spaceElem:
		return "elem segment"
	case spaceLocal:
		return "local"
	case spaceField:
		return "field"
	default:
		return "label"
	}
}

// space is one index space: the symbolic names bound in it, and how many entries it holds.
//
// The reference's `type space = {mutable map : int32 VarMap.t; mutable count : int32}`
// (parser.mly:92). count advances for anonymous definitions too, which is why it is not
// len(names) — `(func)` with no identifier still occupies index 0 and shifts the next
// `(func $f)` to 1.
//
// `kind` is Burroughs' addition and has no counterpart in that record: it is the category word
// the reference passes to `bind_abs` and `lookup` as an argument, moved onto the space so the
// two directions read one field instead of two literals. See spaceKind for the three wrong
// words that motivated it.
type space struct {
	kind  spaceKind
	names map[string]uint32
	count uint32
}

// bindAbs binds a symbolic name to the next index, rejecting a duplicate.
//
// The reference's `bind_abs` (parser.mly:174):
//
//	if VarMap.mem x.it space.map then error x.at ("duplicate " ^ category ^ " " ^ print x);
//
// **The message carries the name**, and the suite reads only as far as `duplicate func` —
// so everything after the category is ours alone to keep honest, which is precisely where
// grave #36 lived. The name is printed from the token's own text, never reconstructed:
// `print` in the reference re-quotes a name whose decoded form differs from its source
// spelling, and reproducing that rendering from a decoded value is how an error comes to
// quote a byte the input never held. Using t.Text sidesteps the question entirely.
//
// The category comes from s.kind rather than from a parameter, which is grave #120's fix: the
// same word must serve this message and resolveSpaceIdx's, and a parameter let them differ.
func (s *space) bindAbs(t Token, name string) error {
	if s.names == nil {
		s.names = make(map[string]uint32)
	}
	if _, dup := s.names[name]; dup {
		return errf(t, "duplicate %s %s", s.kind, t.Text)
	}
	s.names[name] = s.count
	s.count++
	return nil
}

// resolveSpaceIdx resolves a symbolic index against this space, or reports `unknown <category>`.
//
// The reference's `lookup` (parser.mly:148-150) is **one** function that every space's helper
// calls with its own category and map:
//
//	let lookup category space x =
//	  try VarMap.find x.it space.map
//	  with Not_found -> error x.at ("unknown " ^ category ^ " " ^ print x)
//
// So this is one method, not five: `type_`, `tag`, `global`, `memory`, `table` and `func` at
// :152-157 differ only in the two arguments, and writing six Go functions would be six places
// for the word to drift — the defect spaceKind exists to close.
//
// A numeric index is returned unresolved, because there is nothing to resolve: `lookup` is
// reached only from the `var` arm of the reference's `idx` production, and `(func 3)` names
// index 3 whether or not a func 3 exists. Whether it does is the *validator's* question, which
// is why every suite vector for `unknown function` is `assert_invalid` — measured, all 9 of
// them — and why the accept direction here has no board coverage at all.
func (s *space) resolveSpaceIdx(r idxRef) (uint32, error) {
	if !r.isVar {
		return r.idx, nil
	}
	if i, ok := s.names[r.name]; ok {
		return i, nil
	}
	return 0, errf(r.tok, "unknown %s %s", s.kind, r.tok.Text)
}

// bindAnon reserves the next index for a definition with no identifier.
//
// Separate from bindAbs rather than bindAbs with an empty name, because an empty name is a
// *legal* identifier position the lexer already rejects (`(func $)` is "empty identifier")
// and threading "" through the duplicate map would make two anonymous funcs collide. The
// reference splits these the same way: `bind` for anonymous, `bind_abs` for named.
func (s *space) bindAnon() { s.count++ }

// labelSpace is the label index space, which is **relative** where every other space is absolute
// — so it is its own type rather than a `space` field.
//
// The reference's label space is a `space` like the rest, but the three helpers that touch it all
// do something no other space does (parser.mly:132, :196, :106):
//
//	let enter_block c loc = {c with labels = scoped "label" 1l c.labels (at loc)}
//	let bind_label c x = ignore (bind "label" c.labels 1l x.at); space.map <- VarMap.add x.it 0l space.map; 0l
//	let scoped category n space at = {map = VarMap.map (shift category at n) space.map; count = space.count}
//
// `enter_block` shifts **every existing binding up by one** and returns a *new* context, so the
// innermost label is always 0 and scope exit is structural: OCaml's old context is still there
// when the block's arm returns, and nothing pops. A Go parser holding one mutable context cannot
// get that for free, and the two obvious translations are both wrong in ways no vector shows:
// re-shifting a map on entry is O(depth) per block and mutates state the caller still needs, while
// a plain name→index map records absolute depths that are wrong the moment a block is entered.
//
// So: a **stack of the enclosing blocks' labels, innermost last**. An entry's index is its
// distance from the top, which is what `scoped`'s repeated shift computes, and popping is what
// `{c with labels = ...}` gets from immutability. Anonymous blocks push an empty name — they still
// occupy a level (see labelPushAnon) — and `lookupLabel` scans from the top so an inner `$l`
// shadows an outer `$l`, which is `VarMap.add`'s overwrite.
//
// Empty string is safe as the anonymous marker here, unlike in `space.bindAbs` where it would
// collide: `(block $)` is `empty identifier` from the lexer, so no *named* level can be "", and
// the scan skips empties explicitly rather than relying on that.
type labelSpace struct {
	names []string
}

// labelPush enters a named block's scope: `enter_block` then `bind_label` (parser.mly:519).
func (l *labelSpace) labelPush(name string) { l.names = append(l.names, name) }

// labelPushAnon enters an unnamed block's scope: `enter_block` then `anon_label` (parser.mly:514).
//
// **The anonymous level changes no lookup's answer, and saying otherwise would be a control that
// cannot fail.** `labeling_opt`'s empty arm calls `anon_label`, which in the reference shifts the
// enclosing names — so `(block $l (block (br $l)))` resolves `$l` to *1* upstream, where a reader
// that skipped the level would say 0. That difference is an **index**, and per decision 0011 this
// stratum computes no indices: `lookupLabel` answers bound-or-not by name, and an empty name is
// unmatchable (`(block $)` is `empty identifier` from the lexer). A first draft of this comment
// claimed the anonymous push was load-bearing for nesting and offered `(block $l (block (br $l)))`
// as its control — the row passes with the push *and* without it, because both spellings resolve
// by name. Written down because a claim that no probe can kill is exactly what the falsifiability
// rule is for, and this one nearly shipped as a comment asserting a property of code that does not
// run yet.
//
// What it *is* for is the invariant that stack depth equals block nesting depth, which is what
// keeps labelPop unconditional — every push site pops, named or not, so no caller has to remember
// which kind it made. When indices are needed (0011's second half, the binary bridge #67), the
// depth is already right and the shift falls out of the position in the slice.
func (l *labelSpace) labelPushAnon() { l.names = append(l.names, "") }

// labelPop leaves a block's scope, restoring the enclosing bindings.
//
// The reference has no `pop`: `{c with labels = ...}` builds a new context and the caller's own
// `c` is untouched. A mutable context has to undo the push explicitly, and the pairing is the
// invariant this type rests on — every push site pops on *every* exit path, including error
// returns, which is why the callers use `defer`.
func (l *labelSpace) labelPop() { l.names = l.names[:len(l.names)-1] }

// labelDepth is the number of enclosing label scopes, for save/restore across a func boundary.
func (l *labelSpace) labelDepth() int { return len(l.names) }

// labelReset clears the space and returns what it held, for `enter_func` (parser.mly:134).
//
// `let enter_func c loc = {(enter_let c loc) with labels = empty ()}` — a func body starts with
// **no** labels, so nothing leaks from one func into the next, or from a func into a global's
// constexpr. Returning the old contents rather than dropping them keeps the mutable context
// restorable, which the reference gets from `c` still being in scope.
func (l *labelSpace) labelReset() []string {
	old := l.names
	l.names = nil
	return old
}

// labelRestore puts back what labelReset took.
func (l *labelSpace) labelRestore(old []string) { l.names = old }

// lookupLabel resolves a symbolic label, or reports `unknown label`.
//
// The reference's `label` is `lookup "label " c.labels x` (parser.mly:161) — note the category's
// **trailing space**, so the rendered message is `unknown label  $l` with two spaces, from
// `"unknown " ^ category ^ " " ^ print x`. That is reproduced rather than tidied: the suite reads
// only as far as `unknown label`, so the tail is ours alone to keep honest (grave #36), and
// "improving" it would make this the one message in the package that does not match upstream.
//
// The name is printed from the token's own text for the same reason bindAbs does it: the
// reference's `print` re-quotes a name whose decoded form differs from its spelling, and
// reconstructing that rendering from a decoded value is how an error comes to quote a byte the
// input never held.
//
// Scans from the top so the innermost binding wins, and skips anonymous levels — they occupy a
// level but bind no name.
//
// **It returns the relative depth**, which is what `labelPushAnon`'s comment said this stratum did
// not need and named the arrival of: *"when indices are needed (0011's second half, the binary bridge
// #67), the depth is already right and the shift falls out of the position in the slice."* The depth
// does fall out — `len-1-i` counting from the innermost — so the anonymous push is load-bearing for a
// value now and not only for the pop invariant, and `(block $l (block (br $l)))` resolves to **1**
// where a reader that skipped anonymous levels answers 0. TestLabelIndexCountsAnonymousLevels pins
// that in both directions.
//
// **The value is not yet observable through a module, and that control is on this function rather
// than on the path for a measured reason.** Every block form is refused by `refuseUnencodable`
// *before* its body is parsed (blockinstr), so the only label index the code section writes is a
// NAT — `br 0`, whose arm makes no lookup at all. Seven spellings were probed and all seven refuse
// at the block, so no encodable module reaches the symbolic arm; in `recognize` mode the lookup runs
// and its depth is dropped. That makes the test *a control on the helper, not on the path*, which is
// declared here rather than left to be discovered, and the path's control arrives with the block
// instruction's encoding (#63/#64).
func (l *labelSpace) lookupLabel(t Token, name string) (uint32, error) {
	for i := len(l.names) - 1; i >= 0; i-- {
		if l.names[i] != "" && l.names[i] == name {
			return uint32(len(l.names) - 1 - i), nil
		}
	}
	return 0, errf(t, "unknown label  %s", t.Text)
}

// context is the parser's accumulated state: the index spaces, and the module-level facts
// the grammar's own checks read.
//
// **This exists even though decision 0011 makes the parser error-only**, and the reason is
// worth stating: the reference puts ordering and duplicate checks *inside* the grammar, so
// they need state regardless of whether anything is built from it. Three families, all
// #62's:
//
//   - `duplicate <category> <name>` — bind_abs, per space (13 suite vectors)
//   - `import after <kind> definition` — module_fields1:1321-1354 (16 vectors)
//   - `multiple start sections` — module_fields1:1372 (1 vector)
//
// Unexported, per 0011: nothing outside this package sees a context, and no module value
// comes out. When well-formed modules are needed, the parser will emit binary bytes into
// the proven decoder (#67 is the tripwire on that bridge being faithful).
// Every `space` field carries its own kind, set by newContext and never by a call site — the
// nine absolute spaces are the reference's nine `bind_*`/`lookup` pairs, and its category
// literal is theirs rather than each caller's.
type context struct {
	types    space
	tags     space
	globals  space
	memories space
	tables   space
	funcs    space
	datas    space
	elems    space
	locals   space

	// The label space, resolved *here* rather than deferred — the one index space that can be
	// (#80). A label's binding is lexically scoped, so there is no forward reference to wait
	// for: `(block $l (br $l))` has `$l` in scope at the `br`, and a `$name` not on the stack
	// cannot become bound by anything later in the module. Every other space needs #64's
	// deferred phase, because `imports.wast:62` uses `(type $forward)` before defining it and
	// `global.wast:668` names a global defined below.
	//
	// Its own type, because it is relative where the others are absolute: see labelSpace.
	labels labelSpace

	// The `import after <kind> definition` check, which needs three fields because the
	// message's *kind* and the error's *position* come from different places in the field
	// list.
	//
	// **This is the check's fourth reading, and the suite cannot tell any of them apart.**
	// All 16 vectors are `(<one definition>) (import …)` — one definition kind, one import —
	// so every plausible implementation scores 16/16, and the only way to get it right is to
	// trace the reference. Traced three times, because the first two were wrong:
	//
	//	| func module_fields
	//	  { … fun () -> let funcs, ims, exs = ff () in let m = mf () in
	//	    if funcs <> [] && m.imports <> [] then
	//	      error (List.hd m.imports).at "import after function definition" }
	//
	// Three facts, none of them suite-visible, each read off a different part of that arm:
	//
	//   - **The kind is the *latest* definition with an import after it.** `let m = mf ()`
	//     forces the inner arm's thunk before the outer arm's `if`, so the deepest qualifying
	//     arm raises first and `error` raises immediately (`:11`). For
	//     `(func) (global …) (import …)` the **global** arm wins. Draft one said "earliest in
	//     source order" — the fold direction read backwards, written confidently into a
	//     comment. *Comments are testimony too, and where prose and the reference's
	//     executable disagree, the executable outranks* (0003's grave).
	//   - **The position is the *first* import after that definition** — `List.hd m.imports`,
	//     where `m` is the module built from the *suffix*, so its head is the nearest import
	//     following this field. Draft two recorded at every import and overwrote, which is
	//     right for the kind and wrong for the offset on `(func) (global) (import A) (import
	//     B)`: it reports at B, the reference at A. Not in the message text, so no vector and
	//     no message-string check can see it — which is exactly the half that is ours alone
	//     to keep honest (grave #36).
	//   - **A definition after the last import does not count.** For
	//     `(func) (import …) (global …)` the global arm's suffix has no imports and stays
	//     quiet; the func arm raises.
	//
	// Hence: candidate kind and token, updated only when an import is the *first* one after a
	// newer definition. A later definition resets `sinceDefined` so its own first import can
	// take over; a definition with nothing after it never becomes a candidate.
	//
	// And not read off space.count, separately: count also advances for *imported* entries —
	// `(import "" "" (func))` binds a function index — so a count-based test would report
	// "function defined" for a module of pure imports and reject the legal
	// import-after-import case. No vector for that either.
	curDefined   importKind
	haveDefined  bool
	sinceDefined bool // an import has already been seen since curDefined was set

	badKind importKind
	badTok  Token
	haveBad bool

	// sawStart is `multiple start sections`. A bool, not a count, because the message is
	// about the second one existing.
	//
	// It is also section 8's *grammar-side* counter — the `importsSeen`/`exportsSeen` role — and
	// `haveStart` is the emitter's, which is why the pair reads redundantly two statements apart:
	// see `defineStart`.
	sawStart bool

	// startFunc is the retained start function's index and haveStart says it was retained — the
	// emitter's input for section 8 (#413, an #8 slice).
	//
	// **Two fields rather than a `*uint32`**, matching `Module.Start`/`HasStart` on the decoding
	// side, which is the value this eventually has to agree with byte for byte. A pointer would
	// make "no start section" and "a start section naming func 0" distinguishable only by a nil
	// check that every reader has to remember, where the decoder already spells the same fact as a
	// flag; two representations of one fact is how `textData.passive` nearly encoded an empty
	// offset expression as a passive segment.
	//
	// `startFunc` is zero until `runDeferred` fills it, because the index resolves in stage 2.
	//
	// **And the corpus does not require that, which is why it is argued rather than cited.** All
	// ten `(start …)` fields in the suite name a function defined *above* them — checked, the five
	// files are `start.wast`, `start0.wast`, `linking.wast`, `linking3.wast` and `ref_func.wast` —
	// so resolving at the cursor would score exactly the same board. The reference defers anyway:
	// `module_fields1`'s start arm resolves `let x = $1 c` inside the innermost `fun () ->`
	// (parser.mly:1367-1374 — the *export* arm's equivalent thunk, the one `defineExport`'s comment
	// cites, is four arms later at :1380-1384), by which time every field has bound its names.
	// So a `(start $main)` written before `$main` is a module the spec accepts, and resolving early
	// would reject it with no vector to say so — §9 G-3's accept-direction blind spot, and the
	// reason this is the *sibling* shape of `defineExport` rather than a simpler one. Written by
	// `defineStart`.
	startFunc uint32
	haveStart bool

	// The type index space's *content*, and the operations that read it — the reference's
	// `c.types.list`/`c.types.ctx` (parser.mly:120) and its `deferred` thunks. See
	// typetable.go, which owns all four fields and the phase that runs them; they live in this
	// struct because they are context state, and they are documented there because the
	// evaluation-order argument is the whole design.
	//
	// A fourth family joins the three above, and it is the last one the grammar owns:
	//
	//   - `inline function type does not match explicit type` — inline_functype_explicit,
	//     parser.mly:245 (24 suite vectors)
	//   - `unknown type <n>` / `unknown type $name` — func_type and lookup, :165/:152
	//   - `non-function type <n>` — func_type's StructT/ArrayT case, :167
	typeDefs []compType     // explicit definitions, in source order
	typeCtx  []resolvedComp // the resolved table, built by runDeferred and extended by implicit types
	deferred []func() error // stage-2 operations, in parse order

	// recExtents is one entry per `rectype` field in source order — the grouping `typeDefs`
	// flattens away, recorded because it is part of the types' identity rather than a detail of
	// how they were written.
	//
	// **A flat type list cannot express iso-recursive equality, and the encoder used to emit
	// one.** The wire form's type section is a `vec(rectype)` where each element is a bare
	// subtype *or* `0x4e vec(subtype)` (decode.ml:274-277), and `encodeTypes` wrote one bare
	// subtype per type — so `(rec (type $a …) (type $b …))` came out as two singleton groups.
	// That is a different module: intra-group references become ordinals under
	// `roll_deftypes`, so `type-equivalence.wast:49`'s two isomorphic two-member groups are
	// equal types when grouped and unequal when flattened, and `type-rec.wast:28`'s
	// cross-group forward reference is `unknown type` when grouped and legal when flattened.
	// The encoder was silently rewriting one to the other in both directions.
	//
	// Nothing had consumed grouping until `internal/validate`'s `sameDefType` did, which is
	// why no board saw it: the front end wrote bytes denoting a different type space and every
	// oracle downstream agreed with itself about them. Grave #349.
	//
	// Recorded here rather than on `compType` — the shape `binary.CompType` chose — because
	// this side's consumer is the *emitter*, which needs the groups in order and needs to know
	// where the explicit prefix ends; a per-entry extent would have the emitter rediscovering
	// boundaries it is being told. Every explicit type is in exactly one extent, and
	// `typeCtx`'s implicit tail (inlineFuncType's interned signatures) is in none, which is
	// exactly right: an implicit type has no `rec` spelling to preserve.
	recExtents []recExtent

	// The first module field that is not a type definition, for the encoder's frontier check
	// (#8, encode.go). The keyword token itself, so the message can quote `Token.Text` and the
	// error can carry `Token.Offset` — one field for both, because the token holds both. A bool
	// beside it rather than a zero-Token test, since offset 0 line 0 is a legal position: a bare
	// `(func)` module's keyword is at offset 1, but an `(import …)` inside `inline_module` sugar
	// can start the file.
	//
	// **Recorded here rather than derived from the index spaces**, and that is not a
	// convenience. `space.count` advances for imported entries too, so a count-based test
	// cannot tell `(func)` from `(import "" "" (func))` — the same trap the curDefined comment
	// above names for a different check. Worse, an `(export …)` field binds no space at all, so
	// a count-based frontier check would pass an export-only module and the encoder would emit
	// it with the export silently dropped: a module that decodes clean and means something
	// else, in the accept direction no vector covers (§9 G-3).
	//
	// This is the encoder's *only* new retention, and it is deliberately not a step toward
	// retaining fields. It records that a field kind was seen, never its content — because the
	// content each further section needs is a question about what that section's grammar
	// requires, answered when that section is written rather than guessed at now (0006).
	firstNonType Token
	haveNonType  bool

	// The retained memory and table definitions, in source order — the emitter's input for sections
	// 5 and 4. Read by encodeMemories and encodeTables; written by defineMemory and defineTable in
	// typetable.go, which owns the argument for why one is deferred and the other is not.
	//
	// **The paragraph above says this struct's frontier record is "deliberately not a step toward
	// retaining fields", and these two fields are the first exception it anticipated.** They are not
	// a reversal of it: the rule is 0006's, that retention grows out of what a *section's grammar*
	// requires when that section is written, and these are exactly that — written because sections 4
	// and 5 now have emitters, shaped by what `encode.ml:187-200` reads, and no wider. `tabDefs` holds
	// `resolvedTableDef` rather than `tabType` because the element type resolves in the deferred phase
	// and what survives it is the resolved form — and it is the `…Def` type rather than
	// `resolvedTable` because a *defined* table is a tabletype **and** an initializer (#419), which is
	// the `resolvedGlobalDef`/`resolvedGlobal` split one section along.
	//
	// Defined entries only. An imported memory or table is an `Import`, and its type belongs to a
	// section this emitter does not write — the same split `decodeTableForm` names on the binary side,
	// and on the table side that split is now a grammar difference rather than a population one: an
	// import's descriptor has no initializer arm at all (grave #420).
	memDefs []memType
	tabDefs []resolvedTableDef

	// The retained imports, in source order — the emitter's input for section 2 (#8). Both
	// spellings land here: the `(import …)` field and the five inline-import sugar arms, because
	// the reference makes no distinction either (`inline_import` produces an `Import` in the very
	// arm that would otherwise produce a definition, parser.mly:1082-1085 et al) and a section that
	// held one spelling would be short by every occurrence of the other.
	//
	// Written by `defineImport`, whose comment owns the argument for why the slot is appended during
	// the parse and filled by a thunk.
	imports []textImport

	// The retained exports, in source order — the emitter's input for section 7 (#8). Both
	// spellings land here for the same reason imports do: `(export …)` as a module field
	// (parser.mly:1265-1267) and `inline_export` inside a definition (:1269-1274), and the
	// reference makes no distinction downstream either.
	//
	// **The inline spelling needs no lookup at all**, which is the one place the two diverge:
	// `inline_export` is `fun d c -> Export ($3, d @@ $sloc)` — it receives the enclosing field's
	// own index `d` as a parameter, so `(func $f (export "e"))` exports index-of-$f without
	// consulting any space. Only the module-field spelling reaches resolveSpaceIdx.
	exports []textExport

	// The retained data segments, in source order — the emitter's input for sections 11 and 12 (#8).
	// Written by `defineData`, which owns the argument for the stage-2 split.
	//
	// **Both spellings land here**, as with imports and exports: the `(data …)` field and the
	// `(memory <addrtype> (data …))` sugar, which produces a `Data` in the very arm that produces a
	// `Memory` (parser.mly:1126-1131). A list holding one spelling would be short by every occurrence
	// of the other, and for this pair the sugar's omission is worse than a missing segment — the
	// memory it sizes would be emitted with `min`/`max` pages and no bytes in it.
	dataDefs []resolvedData

	// The retained element segments, in source order — the emitter's input for section 9 (#8, 0016).
	// Written by `defineElem`, which owns the argument for the stage-2 split.
	//
	// **Both spellings land here**, as with data segments: the `(elem …)` field and the
	// `(table <type> (elem …))` sugar, which produces an `Elem` in the very arm that produces a
	// `Table` (parser.mly:1188-1196). The sugar's omission would be worse than a missing segment for
	// data's reason — the table it sizes would be emitted with limits and nothing in it.
	elemDefs []resolvedElem

	// The retained *defined* globals, in source order — the emitter's input for section 6 (#8).
	// Written by `defineGlobal`, which owns the argument for the stage-2 split.
	//
	// **Unlike imports, exports, data and elem, this list holds one spelling.** `global_fields`'
	// inline-import arm produces an `Import` and no `Global` (parser.mly:1082-1085), so an imported
	// global never reaches here — the same split `memDefs` and `tabDefs` record. What the two
	// spellings do share is the *index space*, which `globalField` binds before either arm runs.
	globalDefs []resolvedGlobalDef

	// The retained *defined* tags' type-index thunks, in source order — the emitter's input for
	// section 13 (#199). Appended by `tagField` itself rather than by a `defineTag` helper: unlike
	// `defineGlobal`, there is no second field to resolve in a deferred op of this list's own, because
	// `deferSignature` already returns the thunk section 13 needs directly — `textFunc.typeIdx`'s
	// exact shape, copied for the identical reason (a tag's signature interning is func's).
	//
	// **One thunk per tag rather than a struct**, because a tag's wire payload
	// (`tagtype`/encode.ml:190-191) is `zero-byte, typeuse` and the zero byte is a fixed attribute
	// with no field to carry — `TagT ut` (types.ml:40) holds nothing else. `resolvedGlobalDef` needs
	// a struct because a global carries a mutability flag and an initializer; a tag carries only the
	// type index `deferSignature` already resolves.
	//
	// **Unlike imports, exports, data and elem, this list holds one spelling.** `tag_fields`'
	// inline-import arm produces an `Import` and no defined tag (parser.mly:1053-1065), so an
	// imported tag never reaches here — `tagField` binds the shared index space before either arm
	// runs, exactly as `globalField` does for globals.
	tagDefs []func() (uint32, error)

	// elemsSeen counts every element segment the *grammar* saw, for the withdrawal check.
	//
	// `datasSeen`'s instrument on its sibling section, and its argument applies unchanged:
	// incremented in `noteElem`, which both spellings pass through, so this number is the grammar's
	// and `len(elemDefs)` is the emitter's. Counting inside `defineElem` would compare a number
	// against itself.
	elemsSeen int

	// datasSeen counts every data segment the *grammar* saw, for the withdrawal check.
	//
	// `importsSeen`'s instrument, and its argument applies unchanged: incremented in `noteData`, which
	// both spellings pass through, so this number is the grammar's and `len(dataDefs)` is the
	// emitter's. Counting inside `defineData` would compare a number against itself.
	datasSeen int

	// sawDataRef records that some instruction referenced the **data index space**, which is section
	// 12's emission condition (#8).
	//
	// **Not `len(dataDefs) > 0`, and the difference is a live defect in both directions.**
	// `data_count_section` is guarded by `Free.((module_ m).datas <> Set.empty)` (encode.ml:1109) and
	// `free.ml`'s `data` for a *segment* is `segmentmode memories mode` (:217) — a segment
	// contributes **nothing** to the `datas` set. The set is fed only by instructions:
	// `MemoryInit (x,y)` (:165), `DataDrop x` (:166), `ArrayNewData` (:175), `ArrayInitData` (:181).
	// So a segment-only module gets no section 12, and a segment-*less* module with a `data.drop`
	// gets one.
	//
	// Measured before it was written, and the reject direction is real today:
	// `(module (func (data.drop 0)))` encoded to a code section holding `fc 09 00` and no section 12,
	// which `binary.DecodeModule` rejects with `data count section required` — the decoder's mirror of
	// this same set, `dataRefOps` in internal/binary/instr.go, derived from the same four `free.ml`
	// lines. So the two sides of the round trip are read off one authority rather than agreeing by
	// luck; TestSectionTwelveConditionIsTheReferences pins the set against the reference.
	//
	// A bool rather than a count, because the condition is set non-emptiness — `<> Set.empty` — and a
	// count would invite the false reading that section 12's *payload* is the number of references.
	// The payload is `List.length datas`, the segment count.
	sawDataRef bool

	// importsSeen counts every import the *grammar* saw, for the withdrawal check.
	//
	// Incremented in `noteImport`, which is the one place both spellings already pass through —
	// so the count is the grammar's and `len(imports)` is the emitter's, and the whole value of the
	// check is that the two are produced by different code. Counting inside `defineImport` would
	// compare a number against itself.
	//
	// It increments *before* the import's names are read, unlike `noteDefined`, which
	// `memoryField` deliberately calls after the closing paren. The asymmetry is safe rather than
	// sloppy: a malformed import returns an error out of `moduleFields`, so `encodableOrErr` is
	// never reached and no count is ever compared on a module that failed to parse. Stated because
	// the ordering rule next door is load-bearing and this looks like a violation of it.
	importsSeen int

	// exportsSeen counts every export the *grammar* saw, the same instrument for section 7.
	//
	// Its own counter rather than a reuse, on `importsSeen`'s argument: two spellings and one
	// retention list, so the honest check compares a number the grammar produced against one the
	// emitter reads. `noteExport` is called from the two `(export` recognizers — `exportField`'s
	// `lpar` and `inlineExports`' loop — and those are exactly the two places a spelling could
	// withdraw the frontier and forget to record, which for exports is not hypothetical: the sugar
	// discarded its name for four PRs and every board was green.
	exportsSeen int

	// defCount counts *defined* (non-imported) entries per kind, for the withdrawal check in
	// `encodableOrErr`.
	//
	// **This is what stops a withdrawal from lying.** `clearNonTypeField` is a claim that an arm
	// retained everything it parsed, and nothing about the call site proves it — an arm that marks a
	// definition, withdraws the frontier, and forgets to record the type would emit a module missing
	// a memory, decoding clean and meaning something else. So the claim is checked: on a module that
	// passes the frontier, the retained count must equal the defined count, per kind. An array over
	// `importKind` rather than a field per kind, so a sixth kind is a compile-time question rather
	// than a field somebody forgets.
	defCount [importFunc + 1]uint32
}

// newContext returns a context whose every index space knows which space it is.
//
// This is the one place a `spaceKind` is written, which is the whole point of the type: the
// reference names a space's category twice, adjacently, in a helper pair it defines once
// (parser.mly:152-157 and :187-192), and Burroughs' twelve `bindidxOpt` call sites had made that
// one fact into twelve — three of them wrong (grave #120). Assigning here means a caller cannot
// name a space at all, correctly or otherwise.
//
// A constructor rather than a `spaceKind` argument on the methods, because the *lookup* direction
// this PR adds would otherwise need the word too, at six more sites, on the accept side where the
// suite has no vectors. `labels` is absent because labelSpace is its own type with its own
// relative semantics and its own message quirk; see lookupLabel.
func newContext() context {
	return context{
		types:    space{kind: spaceType},
		tags:     space{kind: spaceTag},
		globals:  space{kind: spaceGlobal},
		memories: space{kind: spaceMemory},
		tables:   space{kind: spaceTable},
		funcs:    space{kind: spaceFunc},
		datas:    space{kind: spaceData},
		elems:    space{kind: spaceElem},
		locals:   space{kind: spaceLocal},
	}
}

// spaceFor is the index space an `externidx` kind names.
//
// The reference does this by *which helper the arm calls* — `externidx`'s five arms are `$3 c tag`,
// `$3 c global`, `$3 c memory`, `$3 c table`, `$3 c func` (parser.mly:1258-1263), each a partial
// application of `lookup` to one space. Written as a mapping here because Go has no such
// pre-application, and it is a `switch` returning a pointer rather than a `[importFunc+1]*space`
// array for a reason worth stating: an array of pointers into `c`'s own fields is initialised once
// and then aliases them, so a `locals`-style per-field reset would leave a stale pointer. A switch
// re-derives the pointer each call and cannot go stale.
//
// The default is deliberately the func space and deliberately unreachable from externidx, whose
// switch already rejected every other keyword — so this cannot silently misroute a sixth kind.
// TestSpaceForCoversEveryImportKind walks the enum, which is what makes that claim checkable
// instead of merely stated.
func (c *context) spaceFor(k importKind) *space {
	switch k {
	case importTag:
		return &c.tags
	case importGlobal:
		return &c.globals
	case importMemory:
		return &c.memories
	case importTable:
		return &c.tables
	default:
		return &c.funcs
	}
}

// noteNonTypeField records the first module field the encoder has no emitter for.
//
// First rather than last, so the message points at the earliest construct a reader would have to
// remove to get an image out — which is the actionable one.
//
// **The consequence for reading the frontier as a work plan: these refusals bucket *modules*, not
// constructs.** Measured over the suite — 2143 parser-accepted text modules, of which **926** encode
// in full today — the refusals partition by *mechanism* as structural-or-control instructions 363,
// SIMD 340, tier-2 immediates (memarg and the rest) 231, `elem` 97, `data` 60, `global` 47, GC
// parameterized references 36, struct-or-array comptypes 23, `table` 14, `memory` 6, `start` 3,
// `tag` 3. Each module is counted once, under whichever unencodable field it happens to reach
// first, so a bucket is a lower bound on the modules that construct blocks and says nothing about how
// many occurrences it has. *Bucket size estimates the reward, not the job* — the key is "first
// blocking field", which cuts across mechanism exactly as the board's spec-string key does.
//
// **The memory/table emitters are the measurement that turned that from a caution into a number.**
// Before them the same census read `memory` 467 and `table` 251 with 15 encodable. Both drained —
// those buckets now hold only the arms the emitter still refuses, the per-arm frontier showing up in
// the census — and the 718 modules went: **36** to encodable, and the rest re-sorted into the next
// field they contain (`func` +233, `elem` +157, `data` +115, `export` +18, `global` +7, comptypes +3).
// Two thirds of the largest two buckets bought a 5% move in the only column that counts. So the
// prediction stated here before the work is now the observation: **draining a bucket re-sorts the
// queue instead of emptying it**, and the honest reward estimate for the next bucket is not its size
// but the number of modules whose *last* blocker it is. That number is not knowable from this
// histogram, which is the limitation to carry forward rather than paper over — a histogram of first
// blockers cannot answer a question about last ones, and re-measuring it will not change that.
//
// **The export section is the counter-example to the re-sorting, and it is the first one.** The
// `export` bucket at 39 drained to **zero** — it is gone from the histogram entirely, not reduced —
// and 55 modules reached encodable off a bucket of 39. That arithmetic is the interesting part: an
// export is not a *field* one writes a section for so much as a construct that appears inside five
// other fields, so draining it moved `memory` 104→31 and `table` 29→13 as well. Those were modules
// whose first blocker was an inline export *on* a memory or table, which the field's own emitter
// could not withdraw for. So the honest generalization of the re-sorting rule gains a second case:
// a bucket keyed on a field that other fields *embed* pays more than its size, and a bucket keyed on
// a field that embeds others pays less. `func` at 1361 is the second kind.
//
// `start` at 1 is the useful smallness in the table now: exactly one module in the suite is blocked
// by the start section, so that refusal is correctly a frontier and not a queue. It replaced
// `table-elemtype` at 1 in that role, the elemtype modules having re-sorted into the GC bucket when
// the table emitter landed.
//
// **`func` at 1361 was partitioned by mechanism before being estimated, which is the rule this
// histogram's own limitation demands.** A first-blocker count cannot answer a question about last
// blockers — stated above, and the way around it is not to re-measure this table but to ask a
// *different* question of the same population. Of the 1361 func-blocked modules, **1228 contain no
// other unencodable construct** (counted by scanning each module's source for `(elem`, `(data`,
// `(global`, `(start`, `(struct`, `(array` — crude, a substring scan, and a lower bound on
// co-blockers rather than an exact one). The co-blocked remainder is `elem` 91, `global` 47, `struct`
// 29, `array` 3, `data` 2, `start` 2.
//
// So the code section is the **opposite** case from the export section, and both directions of the
// sharpened rule now have a measurement rather than an argument: `export` was a construct five fields
// embed, so a bucket of 39 paid 55; `func` is a field that embeds others, so a bucket of 1361 pays
// about 1228 and no more. What it embeds is mostly *instructions*, which is what the code section
// writes — the reason the number is high rather than a coincidence. Quoting it before the work makes
// the estimate falsifiable by the work, which is the only kind worth writing down.
//
// # What the code section actually paid, against that estimate
//
// **196 → 926, and the `func` bucket is gone from the histogram entirely.** The estimate said a
// bucket of 1361 pays "about 1228 and no more"; the section pays **730**, which is 59% of the
// forecast. So the forecast was optimistic and the *direction* it argued was right — the export
// case's re-sorting appeared again, and the 498 modules that did not reach encodable re-sorted into
// the frontier of instructions the first tier does not write.
//
// **These figures moved from 920/724 to 926/730 within this PR, and by a mechanism worth naming.**
// The typeuse frontier (#77) was first written as a refusal on *every* func with a typeuse, which
// cost six encodable modules including a row already in the round-trip table; narrowing it to the
// case that is actually unencodable — a symbolic *local* resolved against a possibly-short space,
// refused in `retainIdx` — returned them. So a frontier's *width* is worth six modules here, which
// is the concrete reason the narrow predicate was worth finding rather than a stylistic preference.
//
// **The refusal that narrowing produced is itself gone now (#77): `retainIdxIn` resolves the ordinal at
// the cursor and defers only the param offset, so the case encodes.** Left as written because the six
// modules are what this paragraph is evidence for and they are unaffected — a width that had been wrong
// by a whole grammatical form is the measurement, and it would have been the same measurement if the
// narrow predicate had been the last word instead of one slice's.
//
// The number missed for a reason the histogram could not have shown, and it is the same limitation
// this comment already states rather than a new one: a first-blocker count over *fields* cannot see
// a frontier *inside* a field. The substring scan that produced 1228 looked for `(elem`, `(data`,
// `(global` and their siblings — other **fields** — because at the time the only frontier the
// encoder had was per-field. A module whose sole blocker was `(func … block …)` counted as
// co-blocker-free and was forecast as a payer. It is now the largest bucket. *Bucket size estimates
// the reward, not the job*, and the sharper form the code section paid to learn: an estimate is only
// as fine-grained as the frontier that produced it, so the first emitter to introduce a frontier at
// a *new* granularity will overshoot by exactly what the old granularity could not distinguish.
//
// The new histogram is therefore keyed by mechanism rather than by field, which is what makes it a
// plan: `v128.const` alone is 252 modules and belongs to the SIMD gate, not to this section, while
// the 231 tier-2 immediates are almost all memarg — one shape, and the natural-alignment defaults it
// needs live as 45 per-mnemonic `opt a N` rows in `lexer.mll` and in no generated table, which is
// the extraction that tier costs. The 363 structural-and-control modules are the largest bucket this
// section could take and are also where the *interpreter* has to arrive anyway.
//
// **Both forecasts reproduce now that the section has landed and the buckets can be read off real
// refusals**: tier 2 forecast 231, measured **249**; structural-and-control forecast 363, measured
// **364** (242 block/loop/if/select/br_table/call_indirect, plus 73 `table.init` and 49
// `memory.init`, which the mechanism histogram grouped with control and the refusal strings name
// separately). A forecast agreeing to one module is worth stating precisely because the *previous*
// forecast — the field-keyed one this paragraph exists to correct — missed by a bucket-defining
// margin: keying by mechanism is what made the estimate an estimate.
//
// The caveat, because a bucket count is only as sharp as what produced it: `EncodeModule` returns
// its **first** refusal, so these count modules by their *first* blocker in parse order, not by
// their hardest. A module needing both a memarg and a block lands wherever the parser met one
// first. That biases nothing about the totals — every refused module is counted exactly once — but
// it means a tier's count is not the number of modules that tier would *unblock* on its own, and
// whichever PR takes tier 2 should expect its own gain to undershoot 249.
func (c *context) noteNonTypeField(kw Token) {
	if c.haveNonType {
		return
	}
	c.firstNonType, c.haveNonType = kw, true
}

// clearNonTypeField withdraws a frontier record, for a field the emitter *can* write.
//
// Called at the end of an arm that retained everything it parsed, and bound to the token it
// withdraws: an arm that returns early — an inline import, a sugar form, an error — never reaches the
// call and the refusal stands. That is why the token is a parameter rather than implicit. A withdrawal
// keyed on nothing would clear whatever record happened to be current, so a `(memory 1)` following an
// unencodable `(func …)` would withdraw the *func's* refusal and emit a module with the function
// dropped — the accept-direction defect, arriving through the mechanism built to prevent it. The
// identity check is what makes that impossible, and it is the same law as binding a CI verdict to its
// SHA.
//
// Only the *first* record exists, so withdrawing a later field's token is a no-op by construction: if
// an earlier field already refused, this field's own encodability does not matter.
//
// **The identity check's own falsification is `TestEncodeRefusesWhatItCannotWrite`'s mixed rows, and
// it was watched die**: with the `Offset` comparison removed, all eleven of them encode — most
// recently re-measured with the `(table 1 funcref (ref.func $f))` leader (#413), where the module
// emitted has its *table* dropped. The vector this comment used to name, `(module (func) (memory 1))`,
// is no longer one of those rows: the code section landed and a `(func)` encodes, so the sentence
// outlived the row it cited. Recorded because the check reads like a defensive nicety and is the
// opposite — and re-cited because a falsification naming a vector that no longer exists is a
// falsification nobody can repeat.
func (c *context) clearNonTypeField(kw Token) {
	if c.haveNonType && c.firstNonType.Offset == kw.Offset {
		c.haveNonType = false
	}
}

// noteDefined counts a defined entry of the given kind, for encodableOrErr's retention check.
//
// Separate from `markDefined`, which records the *latest* kind for the import-ordering message and
// therefore cannot count: conflating them would make that check's "latest definition" state a
// counter, and a counter cannot be reset the way `markDefined` needs. Two questions, two mechanisms.
func (c *context) noteDefined(k importKind) { c.defCount[k]++ }

// importKind is the definition kind an `import after …` message names.
//
// Its own type rather than a string, so a typo is a compile error: the five messages are
// fixed by the reference and a misspelled map key would silently never match, which is the
// unreachable-branch shape. The values are the reference's own words at
// parser.mly:1322/1330/1338/1346/1354 — `tag`, `global`, `memory`, `table`, `function` —
// and note that the last is `function`, not `func`.
//
// **The paragraph that stood here claimed the reference spells the same space two ways, and
// it was false** (grave #120). Verbatim, because what a wrong belief was is the part worth
// keeping: *"note that the last is `function`, not `func`, while the duplicate message for
// the same space says `func`. Two different words for one space, in one grammar, and the
// suite pins both … A single shared constant here would be wrong for one of them."*
//
// `bind_func` is `bind_abs "function"` (parser.mly:191) and `func` is `lookup "function"`
// (:157) — **one word per space**, written twice adjacently, and the only pair that differs
// is `label`/`label ` by the reference's own trailing-space quirk. What made the false claim
// survive is that `func.wast:966` writes `"duplicate func"` and the harness matches expected
// strings by *substring* — the six `strings.Contains(got, c.Expect)` sites in `internal/spec`'s
// run loop — so `duplicate function $foo` satisfies it as a prefix. (The line number this cited
// had drifted onto an unrelated parse arm; re-pointed by what the sites hold, since a count of
// them is checkable and an offset is not. How far that looseness reaches is #455's census.)
// A truncated expected string read as evidence about the reference's
// vocabulary — the oracle reading exactly as far as its expected string does, mistaken for
// the oracle reading everything.
//
// The category word is therefore *not* here at all: it belongs to the space, is written once
// in newContext, and is pinned against the authority by TestSpaceKindWordsMatchTheReference.
// This type keeps only the import-ordering word, which is a different message.
type importKind int

const (
	importTag importKind = iota
	importGlobal
	importMemory
	importTable
	importFunc
)

func (k importKind) String() string {
	switch k {
	case importTag:
		return "tag"
	case importGlobal:
		return "global"
	case importMemory:
		return "memory"
	case importTable:
		return "table"
	default:
		return "function"
	}
}

// markDefined records a non-import definition of the given kind.
//
// It clears sinceDefined so that this definition's own first following import can become the
// reported one, superseding any earlier candidate: the reference's deepest qualifying arm
// raises first, and "deepest" means the latest definition that has an import after it.
func (c *context) markDefined(k importKind) {
	c.curDefined, c.haveDefined, c.sinceDefined = k, true, false
}

// checkImportOrder rejects an import that follows a definition of any kind.
//
// The reference expresses this per-field, each arm of module_fields1 checking its own kind
// against `m.imports` *after* the fold — so the message names the kind of the *definition*,
// not of the import. That is why all four `imports.wast` vectors for a given definition
// kind expect the same string even though they import four different things:
//
//	(func) (import "" "" (func))    -> "import after function"
//	(func) (import "" "" (global …)) -> "import after function"
//
// A check keyed on "the import's kind matches the preceding definition's kind" takes one
// vector of four and looks plausible. Measured before writing this, which is the only
// reason the trap was visible.
//
// Called at each import, and it *records* rather than returns: whether a definition
// qualifies depends on what follows it, so the verdict is not known until the field list
// ends. See the curDefined comment block for the three-part trace — the kind is the latest
// qualifying definition, the position is that definition's first following import, and none
// of it is suite-visible.
//
// The `!c.sinceDefined` guard is what makes the position the *first* import rather than the
// last: only the earliest import after the current definition is recorded, and a later
// definition reopens the candidacy via markDefined.
// It also counts, for the encoder's withdrawal check — see `importsSeen`. Both spellings of an
// import already reach here, which is what makes this the right place: a second call site added at
// each spelling would be two places to forget one, and the omission would be a *silently short
// import section* rather than a compile error.
func (c *context) noteImport(t Token) {
	c.importsSeen++
	if c.haveDefined && !c.sinceDefined {
		c.badKind, c.badTok, c.haveBad = c.curDefined, t, true
		c.sinceDefined = true
	}
}

// noteExport counts one export the grammar recognized. See `exportsSeen` and `exportHead`.
//
// Takes no token, unlike `noteImport`: exports have no ordering rule to report a position for —
// section 7 comes after every definition section, so an export can never precede a definition it
// names. The counter is the whole content, which is why this is one line and its sibling is six.
func (c *context) noteExport() { c.exportsSeen++ }

// noteData counts a data segment the grammar saw, for section 11's withdrawal check.
//
// Called from the two `(data` recognizers — `dataField`'s `lpar` and `memoryDataSugar`'s — which are
// exactly the two places a spelling could withdraw the frontier and forget to retain. That is not
// hypothetical for this pair: the sugar arm parsed its payload and discarded it for as long as
// section 11 did not exist, and every board was green.
func (c *context) noteData() { c.datasSeen++ }

// noteElem counts an element segment the grammar saw, for section 9's withdrawal check.
//
// Called from the two `(elem` recognizers — `elemField`'s `lpar` and `tableElemSugar`'s — for
// `noteData`'s reason, and with the same history one section over: the table sugar parsed its element
// list and discarded it for as long as section 9 did not exist.
func (c *context) noteElem() { c.elemsSeen++ }

// importOrderErr returns the ordering error, if any. Called once the module's field list is
// complete, because a definition only qualifies if an import follows it.
func (c *context) importOrderErr() error {
	if !c.haveBad {
		return nil
	}
	return errf(c.badTok, "import after %s definition", c.badKind)
}

// pushLabel enters a block's label scope, named or anonymous.
//
// The two arms of `labeling_opt` (parser.mly:510-519) differ only in whether a `$name` follows, and
// both enter a scope — so the caller passes labelingOpt's result straight through and this picks the
// arm. Written here rather than at each block site because there are two of them (flat and folded)
// and the anonymous arm is the one easy to forget; see labelPushAnon for what the level is for.
func (c *context) pushLabel(name string) {
	if name == "" {
		c.labels.labelPushAnon()
		return
	}
	c.labels.labelPush(name)
}

// checkStart rejects a second `(start …)`.
func (c *context) checkStart(t Token) error {
	if c.sawStart {
		return errAt(t, "multiple start sections")
	}
	c.sawStart = true
	return nil
}
