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

// decodedName validates a `name` position — an import/export name.
//
// Named for the reference's helper. It returns **only an error**, which is decision 0011 applied
// to a helper rather than to a production: the first draft returned `([]byte, error)` reasoning
// that a wat name is a byte sequence and the eventual binary encoder wants the bytes, and
// `unparam` pointed out that no caller reads them. The lint is right and the reasoning was
// speculative — designing the return from a consumer that does not exist is the same move 0011
// declined for the module type, one scale down. When 0011's second half arrives the bytes come
// back, from `t.Value`, which is where they already live.
//
// It checks t.Value, the *decoded* bytes, for the reason spelled out on decodedVar below: the
// reference's `name` receives the unescaped string, and Text would be the source spelling with
// its escapes intact and therefore always valid.
func decodedName(t Token) error {
	if !utf8.Valid(t.Value) {
		return errAt(t, "malformed UTF-8 encoding")
	}
	return nil
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

// space is one index space: the symbolic names bound in it, and how many entries it holds.
//
// The reference's `type space = {mutable map : int32 VarMap.t; mutable count : int32}`
// (parser.mly:92). count advances for anonymous definitions too, which is why it is not
// len(names) — `(func)` with no identifier still occupies index 0 and shifts the next
// `(func $f)` to 1.
type space struct {
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
func (s *space) bindAbs(category string, t Token, name string) error {
	if s.names == nil {
		s.names = make(map[string]uint32)
	}
	if _, dup := s.names[name]; dup {
		return errf(t, "duplicate %s %s", category, t.Text)
	}
	s.names[name] = s.count
	s.count++
	return nil
}

// bindAnon reserves the next index for a definition with no identifier.
//
// Separate from bindAbs rather than bindAbs with an empty name, because an empty name is a
// *legal* identifier position the lexer already rejects (`(func $)` is "empty identifier")
// and threading "" through the duplicate map would make two anonymous funcs collide. The
// reference splits these the same way: `bind` for anonymous, `bind_abs` for named.
func (s *space) bindAnon() { s.count++ }

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
	sawStart bool
}

// importKind is the definition kind an `import after …` message names.
//
// Its own type rather than a string, so a typo is a compile error: the five messages are
// fixed by the reference and a misspelled map key would silently never match, which is the
// unreachable-branch shape. The values are the reference's own words at
// parser.mly:1322/1330/1338/1346/1354 — `tag`, `global`, `memory`, `table`, `function` —
// and note that the last is `function`, not `func`, while the *duplicate* message for the
// same space says `func`. Two different words for one space, in one grammar, and the suite
// pins both (`imports.wast:677` wants "import after function", `binary.wast` and
// `func.wast` want "duplicate func"). A single shared constant here would be wrong for one
// of them.
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
func (c *context) noteImport(t Token) {
	if c.haveDefined && !c.sinceDefined {
		c.badKind, c.badTok, c.haveBad = c.curDefined, t, true
		c.sinceDefined = true
	}
}

// importOrderErr returns the ordering error, if any. Called once the module's field list is
// complete, because a definition only qualifies if an import follows it.
func (c *context) importOrderErr() error {
	if !c.haveBad {
		return nil
	}
	return errf(c.badTok, "import after %s definition", c.badKind)
}

// checkStart rejects a second `(start …)`.
func (c *context) checkStart(t Token) error {
	if c.sawStart {
		return errAt(t, "multiple start sections")
	}
	c.sawStart = true
	return nil
}
