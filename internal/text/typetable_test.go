package text

import (
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The controls for the deferred type-resolution phase (#64's second half).
//
// The board moved 41 → 16 on 25 vectors, all in one bucket family, and that is the weakest kind of
// evidence this phase could have: a bucket keyed on `inline function type does not match explicit
// type` is satisfied by *any* implementation that rejects those 24 modules, including several that
// reject valid ones too. So the controls here are almost entirely about the half no vector reaches —
// the deferral, the ordering, the index arithmetic, and the accept direction.
//
// # Every control below was falsified, and the board's reaction is the point
//
// Each row is one defect introduced deliberately, the control that caught it, and what the board did.
// *A green that survives the bug it names is a control in name only*, and the third column is why
// these tests exist rather than being replaced by a pass count:
//
//	defect introduced                              caught by                      board
//	resolveTypeIdx at the typeuse, not deferred     ForwardTypeReference           4147→4145 ✓
//	early return before the name lookup             EmptyInlineSignatureDefers…    unchanged
//	declareBlockImplicit interns unconditionally    BlockSugarInternsConditionally unchanged
//	blockSignature(nil) — body read after           NestedBlockTypeInterns…        unchanged
//	equal() compares arity only                    FunctypeEqualityIsStructural   unchanged
//	`non-function type` reported as `unknown type`  NonFunctionTypeMessage         unchanged
//	inlineFuncType appends without searching        ImplicitTypesShiftLaterIndices unchanged
//	inlineFuncType reuses a rec-group member        InlineFuncTypeReusesOnly…      64900→64903 (all-on)
//	subtype comparison defers to the supertype      SubtypeComparesItsOwnComptype  unchanged
//	block arms wired to the create-helper           TypeuseSitesAllResolve         4147→4135
//	externtype given a fifth typeuse+functype arm   ExterntypeTakesTypeuseXor…     unchanged
//	importOrderErr before runDeferred               ResolutionErrorPrecedes…       unchanged
//
// **Nine of twelve defects are invisible on the board.** Three are not, and the first two are in the
// direction the suite is built to see: an over-rejection that costs imports.wast a pass, and twelve
// mismatch vectors going unrejected. Everything else — the whole index arithmetic, the equality's five
// axes, the interning conditions, the error precedence — is a green either way. That is the measurement
// behind *the suite samples the spec, it does not define it*, taken on this phase specifically.
//
// The third is board-visible on **one lane only**, and the row says which: grave #402's rec-group
// condition moves the all-gates-on lane and leaves the default lane at rest, because every vector that
// can ask the question needs GC types to decode. So "invisible on the board" turned out to have a third
// value beyond yes and no — *visible, but not from here* — and a count of eleven taken on the default
// lane alone would have called this one invisible too.
//
// One entry in that table is a defect this control set found *live* rather than one introduced to
// test it: see TestNestedBlockTypeInternsBeforeItsEnclosingOne.

// TestForwardTypeReferenceParses is the vector that decided the whole design.
//
// `imports.wast:62-64` uses `(type $forward)` in two fields *before* defining it, and it is a valid
// module. A resolver that looked names up where they are used would reject it — accept-direction, so
// no `assert_malformed` can see the defect, and the file is on the board at 84/84 either way because
// its bare modules pass by *not being contradicted*.
//
// Falsified by making resolveTypeIdx run at the typeuse rather than in runDeferred: this fails and
// the board's imports.wast line drops. That is the one place the design is visible.
func TestForwardTypeReferenceParses(t *testing.T) {
	// Verbatim shape of imports.wast:62-64, reduced to the three fields that matter and given a
	// module wrapper. Derived rather than cited: the premise is that those lines use $forward
	// before defining it, and the inference is that the same order in a smaller module is legal
	// for the same reason.
	const src = `(module
		(import "spectest" "print_i32" (func (type $forward)))
		(func (import "spectest" "print_i32") (type $forward))
		(type $forward (func (param i32)))
	)`
	if err := ReadModule([]byte(src)); err != nil {
		t.Errorf("ReadModule = %v; a type name may be used before it is defined, because "+
			"module_ binds every name at stage 0 and runs the type-using arms at stage 2 "+
			"(parser.mly:1389-1392). imports.wast:62-64 is this module", err)
	}

	// The mirror inside a single type definition: a recursive group's members may reference each
	// other in either direction, and `heaptype`'s VAR arm resolves at parse time — so this only
	// works because the resolution is deferred past the whole field list.
	const mutual = `(module (rec
		(type $a (struct (field (ref $b))))
		(type $b (struct (field (ref $a))))
	))`
	if err := ReadModule([]byte(mutual)); err != nil {
		t.Errorf("ReadModule(mutual rec) = %v; type-rec.wast has this shape", err)
	}
}

// TestEmptyInlineSignatureDefersTheRangeCheck pins `inline_functype_explicit`'s first branch, and
// **the suite pins it in both directions on one module** — which is rare enough to be worth the
// test on its own.
//
//	func.wast:442  (func (type 2))            assert_invalid    — no lookup, so not the parser's
//	func.wast:454  (func (type 2) (param i32)) assert_malformed — the signature forces the lookup
//
// Same index, same module shape, two different layers, decided entirely by whether `ft = ([], [])`.
// An implementation that always resolves scores the 24-vector bucket exactly as well and turns
// `assert_invalid` vectors into parse errors, which the board *would* show — but only because those
// two vectors happen to exist. Everywhere else the branch is invisible.
func TestEmptyInlineSignatureDefersTheRangeCheck(t *testing.T) {
	// func.wast:442's module. A numeric index out of range with no inline signature: the range
	// check is deferred and never runs, so the parser must accept.
	if err := ReadModule([]byte(`(module (func (type 2)))`)); err != nil {
		t.Errorf("ReadModule = %v; func.wast:442 is assert_invalid, so the *parser* accepts "+
			"this and the validator rejects it. `ft = ([], [])` defers func_type entirely "+
			"(parser.mly:238-245)", err)
	}
	// func.wast:454's module, one `(param i32)` different, and now malformed.
	err := ReadModule([]byte(`(module (func (type 2) (param i32)))`))
	if err == nil {
		t.Error("ReadModule accepted `(func (type 2) (param i32))` in a module with no types; " +
			"func.wast:454 expects `unknown type`")
	} else if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("ReadModule = %v, want an `unknown type` error", err)
	}

	// **The half of the deferral that is NOT deferred**, and getting this wrong is a single
	// misplaced early return. `typeuse` is `$3 c type_` (parser.mly:471) and `idx`'s VAR arm is
	// `lookup c (var $1)` (:489) — so a *symbolic* index resolves whenever a typeuse is written,
	// in every arm, deferral or not. Only `func_type`'s range-and-func-ness check is skipped.
	//
	// The reference's own comment at :239 says as much: it defers so that "type lookup is only
	// triggered when symbolic identifiers are used" — and a symbolic identifier still has to exist.
	//
	// No suite vector: every `unknown type` vector in the corpus is numeric (measured — 32
	// occurrences, none symbolic). Synthetic, and named as such.
	err = ReadModule([]byte(`(module (func (type $nope)))`))
	if err == nil {
		t.Error("ReadModule accepted `(func (type $nope))` with $nope unbound; the empty-signature " +
			"branch defers func_type's range check, NOT typeuse's own name lookup " +
			"(parser.mly:471 → :489). synthetic: the corpus has no symbolic unknown-type vector")
	} else if !strings.Contains(err.Error(), "unknown type $nope") {
		t.Errorf("ReadModule = %v, want `unknown type $nope` — the message prints the "+
			"identifier here, where func_type's prints the resolved index", err)
	}
}

// TestFunctypeEqualityIsStructural pins the comparison itself, in both directions and across the
// axes an `==` on the wrong representation gets wrong.
//
// The 24 bucket vectors are all *mismatches*, so they cannot distinguish a correct comparison from
// one that rejects everything. The accept rows are the ones with teeth, and they are derived: the
// suite's own `(func (type $sig) (param i32) (result i32))` spellings in func.wast's valid modules
// are exactly these shapes.
func TestFunctypeEqualityIsStructural(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want bool // true = the reference accepts
		why  string
	}{
		// Agreement, spelled differently on each side. The inline signature groups its params
		// `(param i32 i32)` where the definition writes them singly — `functype` concatenates
		// across repeats (parser.mly:433), so both are the same type and equality must see past
		// the grouping.
		{
			`(module (type $s (func (param i32) (param i32))) (func (type $s) (param i32 i32)))`,
			true, "grouping is not part of the type",
		},
		{
			`(module (type $s (func (param i32 i32))) (func (type $s) (param i32) (param i32)))`,
			true, "the mirror",
		},
		{
			`(module (type $s (func (param $a i32))) (func (type $s) (param $b i32)))`,
			true, "a param's *name* is not part of the type — functype:436's bindidx sugar " +
				"binds a local and contributes only the valtype",
		},
		{
			`(module (type $s (func)) (func (type $s)))`,
			true, "empty against empty — but note this is the deferred branch, so it is not " +
				"even a comparison; see TestEmptyInlineSignatureDefersTheRangeCheck",
		},
		{
			`(module (type $s (func (param i32) (result i64))) (func (type $s) (param i32) (result i64)))`,
			true, "identical",
		},

		// Disagreement, one axis at a time.
		{
			`(module (type $s (func (param i32))) (func (type $s) (param i64)))`,
			false, "a different valtype",
		},
		{
			`(module (type $s (func (param i32))) (func (type $s) (param i32 i32)))`,
			false, "a different arity",
		},
		{
			`(module (type $s (func (param i32) (param i64))) (func (type $s) (param i64) (param i32)))`,
			false, "the same multiset in a different order — a comparison on sorted or counted " +
				"params passes this and is wrong",
		},
		{
			`(module (type $s (func (param i32))) (func (type $s) (result i32)))`,
			false, "params against results — a comparison that concatenated the two halves " +
				"before comparing passes this, which is why the pair is separate",
		},
		{
			`(module (type $s (func (result i32))) (func (type $s) (param i32)))`,
			false, "the mirror",
		},

		// Reference types, where the *resolution* is part of the comparison rather than the
		// spelling. `funcref` is sugar for `(ref null func)` (parser.mly:384), so these are the
		// same type written two ways — an equality on token text fails this and passes everything
		// above it.
		{
			`(module (type $s (func (param funcref))) (func (type $s) (param (ref null func))))`,
			true, "reftype:384's sugar expands to the same heap type and nullability",
		},
		{
			`(module (type $s (func (param (ref null func)))) (func (type $s) (param funcref)))`,
			true, "the mirror",
		},
		{
			`(module (type $s (func (param funcref))) (func (type $s) (param (ref func))))`,
			false, "null_opt differs — `funcref` is nullable, `(ref func)` is not",
		},
		{
			`(module (type $s (func (param anyref))) (func (type $s) (param eqref)))`,
			false, "different heap types; subtyping is validation's, equality is exact",
		},
		// A symbolic and a numeric heaptype naming the same type must compare equal, which only
		// works because both sides resolve to an index before comparing.
		{
			`(module (type $t (func)) (type $s (func (param (ref $t)))) (func (type $s) (param (ref 0))))`,
			true, "$t is index 0, so `(ref $t)` and `(ref 0)` are one type",
		},
		{
			`(module (type $t (func)) (type $u (func (param i32))) (type $s (func (param (ref $t)))) ` +
				`(func (type $s) (param (ref $u))))`,
			false, "different indices",
		},

		// A struct or array type is a `non-function type` to func_type. **Zero corpus vectors** —
		// the message is implemented and unreachable from the suite, so this is the only place it
		// is exercised. Synthetic, and stated as such rather than left looking cited.
		{
			`(module (type $s (struct)) (func (type $s) (param i32)))`,
			false, "synthetic: non-function type, parser.mly:167 — no suite vector",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if tc.want && err != nil {
			t.Errorf("ReadModule(%s) = %v, want accepted (%s)", tc.src, err, tc.why)
		}
		if !tc.want && err == nil {
			t.Errorf("ReadModule(%s) accepted, want rejected (%s)", tc.src, tc.why)
		}
	}
}

// TestNonFunctionTypeMessage prints the one message no vector reaches, because *a message that names
// a value from the input gets printed for real inputs before it is trusted* — grave #36, where a
// reconstruction quoted a byte the image never held. There are zero `non-function type` vectors in
// the corpus, so the board can never see this string; it is entirely ours to keep honest.
func TestNonFunctionTypeMessage(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The index is `Int32.to_string x.it` — the *resolved* index, after any name lookup, not
		// the identifier the source wrote. So a symbolic reference prints a number.
		{
			`(module (type $a (func)) (type $s (struct)) (func (type $s) (param i32)))`,
			"non-function type 1",
		},
		{
			`(module (type (func)) (type (array i32)) (func (type 1) (param i32)))`,
			"non-function type 1",
		},
		{
			`(module (type (struct)) (func (type 0) (param i32)))`,
			"non-function type 0",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%s) accepted; want %q", tc.src, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ReadModule(%s) = %q, want it to contain %q — parser.mly:167 prints the "+
				"resolved index, which for a symbolic typeuse is a number and not the name",
				tc.src, err.Error(), tc.want)
		}
		t.Logf("%s\n\t→ %v", tc.src, err)
	}
}

// TestImplicitTypesShiftLaterIndices is the control for the one thing `inline_functype`'s return
// value does that a reject-only parser can still observe: **it appends, so it moves the boundary a
// numeric typeuse is checked against**.
//
// This is why the recording order is load-bearing rather than incidental, and it is invisible to
// every vector — the corpus's `unknown type` vectors all sit in modules with no implicit types, so
// the table length they are checked against is just the explicit count.
//
// Synthetic throughout, and the reasoning is the citation: `inline_functype` is called with a
// non-empty signature, finds no structurally equal entry, and calls `define_type` (parser.mly:232),
// which is the same appending the explicit `(type …)` fields do.
func TestImplicitTypesShiftLaterIndices(t *testing.T) {
	// One explicit type (index 0) plus one implicit (index 1, from the sugar `(param i64)`), so
	// `(type 1)` resolves and `(type 2)` does not.
	//
	// The implicit type is created by a field *before* the one that references index 1, which is
	// the ordering claim: parse order is stage-2 order.
	if err := ReadModule([]byte(
		`(module (type (func)) (func (param i64)) (func (type 1) (param i64)))`,
	)); err != nil {
		t.Errorf("ReadModule = %v; `(func (param i64))` interns an implicit type at index 1, "+
			"so the third field's `(type 1)` resolves. An engine that did not append would "+
			"report `unknown type 1` here", err)
	}
	// The same module without the implicit-creating field: now index 1 does not exist.
	if err := ReadModule([]byte(
		`(module (type (func)) (func (type 1) (param i64)))`,
	)); err == nil {
		t.Error("ReadModule accepted `(type 1)` in a module with one type; the vacuity check " +
			"on the row above — without it, an engine that resolved every index would pass both")
	} else if !strings.Contains(err.Error(), "unknown type 1") {
		t.Errorf("ReadModule = %v, want `unknown type 1`", err)
	}

	// **Reuse, not blind appending** (parser.mly:224-231: `index_where` before `define_type`). Two
	// functions with the same signature share one implicit type, so index 1 exists and index 2
	// does not. An implementation that appended unconditionally would accept `(type 2)` here —
	// and would pass the whole 24-vector bucket, because a mismatch is a mismatch either way.
	if err := ReadModule([]byte(
		`(module (func (param i64)) (func (param i64)) (func (type 0) (param i64)))`,
	)); err != nil {
		t.Errorf("ReadModule = %v; two identical implicit signatures intern once", err)
	}
	if err := ReadModule([]byte(
		`(module (func (param i64)) (func (param i64)) (func (type 1) (param i64)))`,
	)); err == nil {
		t.Error("ReadModule accepted `(type 1)` where the two identical implicit signatures " +
			"should have interned to one type — inline_functype searches before it defines " +
			"(parser.mly:224)")
	}
	// And the other direction: two *different* implicit signatures do occupy two indices.
	if err := ReadModule([]byte(
		`(module (func (param i64)) (func (param f32)) (func (type 1) (param f32)))`,
	)); err != nil {
		t.Errorf("ReadModule = %v; two distinct implicit signatures take indices 0 and 1", err)
	}
}

// TestInlineFuncTypeReusesOnlySingletonGroups is grave #402's control: `inline_functype` reuses
// `DefT (RecT [st'], 0l)` and nothing else, so a member of a multi-member `(rec …)` is never handed to
// an inline signature.
//
//	Lib.List.index_where (function | DefT (RecT [st'], 0l) -> st = st' | _ -> false) c.types.ctx
//
// Observed through the same keyhole its sibling above uses — a numeric `(type N)` resolving or not —
// because that is the one effect of an interning decision a reject-only front end can be asked about.
// Reuse keeps the table short and `(type N)` is `unknown type N`; appending makes the same spelling
// resolve. So each row below is a pair, and the *pair* is the measurement.
//
// # Why the corpus cannot be the control here
//
// The whole 3-vector gain is on the all-gates-on lane (`type-rec.wast:51`, `:204`, `:216`), and the
// repair moved **nothing** in this package's own expectations — `internal/text` was green before and
// after, unchanged. #402 predicted the opposite ("un-reusing a type shifts every subsequent type index
// in every module with an inline signature"), and that prediction was wrong for a reason worth keeping:
// the restriction only bites where a multi-member group holds a functype that an inline signature
// happens to match structurally, which is rare enough that no existing vector in this package contains
// one. **A change with no failing witness in its own package is a change whose control is the whole
// evidence**, so these rows are written to die, and each one was watched to.
//
// # The third row is the one that is not obvious
//
// A one-member `(rec (type $t (func)))` **is** reusable. Both `rectype` arms call `define_deftype`
// (parser.mly:1288-1298) and the REC arm with a single member produces `DefT (RecT [st], 0l)` — the
// same deftype the bare arm produces, indistinguishable by the pattern. So the condition is the group's
// *length*, not whether `rec` was written, and an implementation phrased as "skip anything spelled with
// `rec`" is correct on every vector in the suite and wrong on the grammar. Row 3 is the only thing that
// separates the two, which is why it is here rather than in a comment.
//
// # Every probe carries `(param i64)`, and the first draft of this test did not
//
// The observing field is `(func (type N) (param i64))` and the inline signature is **not decoration**:
// with an *empty* one, `checkExplicit` takes `inline_functype_explicit`'s deferring arm (parser.mly:239)
// and the range check never runs, so `(func (type 2))` in a two-type module is accepted and the row
// asks nothing at all. Two rows below were written that way, passed in the direction that needed the
// fix, and failed in the direction that did not — which is how the vacuity was found rather than
// shipped. Its cost is that the probe's signature has to *match* the type it names, so the interned
// signature is `([i64],[])` throughout and the group members are spelled to agree with it.
//
// The sibling above carries `(param i64)` on every one of its numeric probes for this same reason, and
// nothing said so.
//
// # Watched die, four times, one defect each
//
// Every row was falsified rather than argued for, and the interesting column is the third — which rows
// stay green, because a row that dies to every defect is not discriminating between them:
//
//	defect introduced into soleMemberOfItsGroup    rows that die      rows still green
//	the conjunct dropped (the pre-#402 engine)     1, 5               2, 3, 4
//	`return false` for every explicit type         2, 3               1, 4, 5
//	`return x == g.start+g.length-1` (last member)  5                  1, 2, 3, 4
//	`return x == g.start` (projection 0)           1                  2, 3, 4, 5
//
// The two single-row lines are the reason the table is five rows and not two: rows 1 and 5 each catch a
// defect the other passes, and they differ only in which projection of a two-member group holds the
// structural match. `typetable.go` was restored byte-identically after each, checked by hash rather
// than by eye.
func TestInlineFuncTypeReusesOnlySingletonGroups(t *testing.T) {
	for _, c := range []struct {
		name    string
		src     string
		wantErr string // "" = must be accepted
		why     string
	}{
		{
			name: "a two-member group's first member is not reused",
			src: `(module (rec (type $a (func (param i64))) (type $b (func (param i32)))) ` +
				`(func (param i64)) (func (type 2) (param i64)))`,
			wantErr: "",
			why: "`$a` is structurally `([i64],[])`, final, with no declared supertypes — every " +
				"condition the old code checked — but it is one of two members, so the third " +
				"field interns a *new* type at index 2 and `(type 2)` resolves. This row is the " +
				"whole defect: before the fix it was `unknown type 2`. It is also the row that " +
				"kills the misreading \"projection 0\", since the match here sits at projection 0 " +
				"of a group the reference excludes for its length",
		},
		{
			name: "the same types as singletons are reused",
			src: `(module (type $a (func (param i64))) (type $b (func (param i32))) ` +
				`(func (param i64)) (func (type 2) (param i64)))`,
			wantErr: "unknown type 2",
			why: "the vacuity twin of the row above, differing only in the `rec` wrapper. Two " +
				"explicit types either way, so a fix that appended unconditionally — or one that " +
				"read the group off the type *count* — would accept both and the pair would " +
				"measure nothing",
		},
		{
			name:    "a one-member rec group is reused",
			src:     `(module (rec (type $a (func (param i64)))) (func (param i64)) (func (type 1) (param i64)))`,
			wantErr: "unknown type 1",
			why: "`RecT [st]` is what a singleton REC arm produces, so `$a` is reusable and the " +
				"table stays one long. **This is the row that fails for an implementation keyed " +
				"on the `rec` spelling instead of the group's length**, and nothing in the suite " +
				"distinguishes the two",
		},
		{
			name:    "and that group's own index still resolves",
			src:     `(module (rec (type $a (func (param i64)))) (func (param i64)) (func (type 0) (param i64)))`,
			wantErr: "",
			why: "the twin for the row above: `unknown type 1` has to mean \"the table is one " +
				"long\", not \"this module is broken for some other reason\" — an assertion on an " +
				"error message cannot tell those apart on its own",
		},
		{
			name: "a two-member group's second member is not reused either",
			src: `(module (rec (type $a (func (param i32))) (type $b (func (param i64)))) ` +
				`(func (param i64)) (func (type 2) (param i64)))`,
			wantErr: "",
			why: "the matching member sits at projection 1 here, where row 1 put it at 0. Row 1 " +
				"cannot see a condition keyed on the member's *position* in its group rather than " +
				"the group's size — \"reusable iff it is the last member\" passes row 1 and fails " +
				"here, which is the off-by-one the extent scan is one character away from",
		},
	} {
		err := ReadModule([]byte(c.src))
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: ReadModule(%s) = %v; want accepted — %s", c.name, c.src, err, c.why)
		case c.wantErr != "" && err == nil:
			t.Errorf("%s: ReadModule(%s) accepted; want %q — %s", c.name, c.src, c.wantErr, c.why)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: ReadModule(%s) = %q, want it to contain %q — %s",
				c.name, c.src, err.Error(), c.wantErr, c.why)
		}
	}
}

// TestBlockSugarInternsConditionally pins the one arm of the shared chain that does not intern, and
// it is the difference between the block family and everything else.
//
//	| ([], []) -> ValBlockType None          parser.mly:747
//	| ([], [t]) -> ValBlockType (Some t)     :748
//	| ft -> VarBlockType (inline_functype …) :749
//
// `func`'s sugar arm (:970) and `callexpr_type`'s (:847) have no such cases. Since `(block)` and
// `(block (result i32))` are the overwhelmingly common spellings in the corpus, a version that
// always interned would shift indices in most files that contain a block at all — and shift them
// *silently*, because nothing on the board reads a type index.
//
// **Every row's enclosing function carries `(type 0)` rather than an inline signature**, so the
// function itself interns nothing (`ft = ([], [])` is the deferred case, :238) and the block's
// implicit type — if it makes one — is index 1 exactly. The first draft used the sugar `(func …)`,
// whose own empty signature takes index 0, and three rows were off by one against a parser that was
// right. Beyond the arithmetic it buys a *distinguishing* failure: a row asserting nothing was
// interned now expects `unknown type 1`, so a version that interned unconditionally fails with that
// index resolving, not merely with "some error somewhere".
func TestBlockSugarInternsConditionally(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string // "" for accepted, else the substring the rejection must carry
		why  string
	}{
		// No params and no results: nothing interned, so index 1 does not exist.
		{
			`(module (type (func)) (func (type 0) (block)) (func (type 1) (param i32)))`,
			"unknown type 1",
			"`(block)` is ValBlockType None — parser.mly:747, no type created",
		},
		// No params and exactly one result: still nothing interned.
		{
			`(module (type (func)) (func (type 0) (block (result i32))) (func (type 1) (param i32)))`,
			"unknown type 1",
			"`(block (result i32))` is ValBlockType (Some t) — :748",
		},
		// Two results: past the sugar, so a type is created at index 1 and matches.
		{
			`(module (type (func)) (func (type 0) (block (result i32 i32))) ` +
				`(func (type 1) (result i32 i32)))`, "",
			"two results falls to :749 and interns",
		},
		// One param, no results: also past the sugar — the `([], [t])` case requires *no* params.
		{
			`(module (type (func)) (func (type 0) (block (param i32) drop)) ` +
				`(func (type 1) (param i32)))`, "",
			"a param falls to :749 even with no result",
		},

		// `call_indirect` is the contrast, and it is the same signature shape as the second row:
		// `callinstr_type_instr_list`'s sugar arm (:709) interns unconditionally, so one result
		// *does* create a type here where it does not in a block.
		{
			`(module (type (func)) (table 0 funcref) ` +
				`(func (type 0) (call_indirect (result i32) (i32.const 0))) ` +
				`(func (type 1) (result i32)))`, "",
			"callexpr_type:847 has no ([], [t]) case — `(result i32)` interns",
		},
	} {
		err := ReadModule([]byte(tc.src))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("ReadModule(%s) = %v, want accepted (%s)", tc.src, err, tc.why)
		case tc.want != "" && err == nil:
			t.Errorf("ReadModule(%s) accepted, want %q (%s)", tc.src, tc.want, tc.why)
		case tc.want != "" && !strings.Contains(err.Error(), tc.want):
			t.Errorf("ReadModule(%s) = %v, want %q (%s) — a different error means the row is "+
				"testing something other than the index the sugar did not take",
				tc.src, err, tc.want, tc.why)
		}
	}
}

// TestNestedBlockTypeInternsBeforeItsEnclosingOne pins the *other* evaluation order, and it is the
// opposite of `func`'s.
//
// Every arm of the block family reads `let ft, es = $2 c in` and then calls its helper, and `$2 c`
// forces the whole right-recursive chain whose innermost production is the body. So a nested block's
// implicit type is interned *first*. `func_fields` is the reverse — `fst $2 c'` then `snd $2 c'`
// (:966-967) — so a function's own signature precedes its body's.
//
// Both orders are in the grammar, both decide indices, and no vector reads an index. Synthetic, with
// the arms as the citation.
//
// **This control caught a live defect, which is the reason to write the ones no vector can see.**
// `blockSignature` took no tail and each of its three callers read the body after it returned, so the
// enclosing block's type was interned first: measured as index 1 outer, index 2 inner, the reverse of
// the grammar. The comment on `orderedTypeUse` asserted the correct order the whole time — it
// described the deferral inside that function, which *was* after its tail, and said nothing about a
// nil tail defeating it one level up. The board did not move by one vector in either configuration.
//
// **And the first version of this control only covered one of the three callers**, which the
// falsification found: re-introducing the nil tail in `block` (the flat form) left it green, because
// every row was spelled folded. `blockSignature` is called from `block` :741, `handlerBlock` :770 and
// `foldedBlock` (`if_block` :866 / `try_block` :902 / `block` again), and each one reads its own body
// — three places to lose the order, so the nesting rows below are spelled once per caller. *Scope
// controls to the space*, where the space here is the call sites and not the productions.
func TestNestedBlockTypeInternsBeforeItsEnclosingOne(t *testing.T) {
	// The enclosing function carries `(type 0)` so it interns nothing of its own; the outer construct
	// wants two results and the inner one param, both past the sugar, so they take indices 1 and 2 in
	// whichever order the family runs them. Inner-first means index 1 is `(param i32)`.
	//
	// One nesting per `blockSignature` caller. The *inner* construct is a folded block throughout —
	// it is the outer one whose form varies, because it is the outer one whose body-reading order is
	// under test.
	for _, tc := range []struct{ site, body string }{
		{
			"block:741 (flat)",
			`(func (type 0) block (result i32 i32) (block (param i32) drop) unreachable end)`,
		},
		{
			"block:741 (folded)",
			`(func (type 0) (block (result i32 i32) (block (param i32) drop) unreachable))`,
		},
		{
			"if_block:866 (folded)",
			`(func (type 0) (if (result i32 i32) (i32.const 0) ` +
				`(then (block (param i32) drop) unreachable)))`,
		},
		{
			"handler_block:770 (flat try_table)",
			`(func (type 0) try_table (result i32 i32) (block (param i32) drop) unreachable end)`,
		},
		{
			"try_block:902 (folded try_table)",
			`(func (type 0) (try_table (result i32 i32) (block (param i32) drop) unreachable))`,
		},
	} {
		if err := ReadModule([]byte(
			`(module (type (func)) ` + tc.body + ` (func (type 1) (param i32)))`,
		)); err != nil {
			t.Errorf("%s: ReadModule = %v; the *inner* block's `(param i32)` should hold index 1, "+
				"because the block family forces its body before calling inline_functype "+
				"(parser.mly:742: `let ft, es = $2 c`). synthetic: no vector reads an index",
				tc.site, err)
		}
		// The falsifier for the row above: if the outer were interned first, index 1 would be the
		// two-result type and this spelling would be the one accepted. Exactly one of the two can
		// pass, and this is the assertion that failed while the defect was live.
		if err := ReadModule([]byte(
			`(module (type (func)) ` + tc.body + ` (func (type 1) (result i32 i32)))`,
		)); err == nil {
			t.Errorf("%s: ReadModule accepted the outer signature at index 1; the body is forced "+
				"first, so the inner block's type is the earlier one. Both rows passing would mean "+
				"the comparison is not looking at the index at all", tc.site)
		}
		// And the outer really is at index 2 — without this the pair above is satisfied by a parser
		// that interned the inner block and dropped the outer one entirely.
		if err := ReadModule([]byte(
			`(module (type (func)) ` + tc.body + ` (func (type 2) (result i32 i32)))`,
		)); err != nil {
			t.Errorf("%s: ReadModule = %v; the outer construct's `(result i32 i32)` should hold "+
				"index 2, one past the inner block's", tc.site, err)
		}
	}

	// A function's own signature *does* precede its body's, which is the same test pointed the
	// other way: `func_fields` evaluates `fst $2 c'` before `snd $2 c'`. Here the function uses the
	// sugar arm, so its `(param i64)` is index 0 and the block inside it is index 1.
	if err := ReadModule([]byte(
		`(module (func (param i64) (block (result i32 i32) unreachable)) ` +
			`(func (type 0) (param i64)))`,
	)); err != nil {
		t.Errorf("ReadModule = %v; the function's own `(param i64)` should hold index 0, ahead "+
			"of the block inside its body (parser.mly:966-967)", err)
	}
	if err := ReadModule([]byte(
		`(module (func (param i64) (block (result i32 i32) unreachable)) ` +
			`(func (type 1) (result i32 i32)))`,
	)); err != nil {
		t.Errorf("ReadModule = %v; the block's type should hold index 1, behind the enclosing "+
			"function's own signature — the opposite nesting order from the rows above", err)
	}
}

// TestResolutionErrorPrecedesImportOrder pins which of two errors a module carrying both reports.
//
// **No suite vector has both** — measured: the corpus has one `multiple start sections` vector and
// its 16 `import after` vectors are each a single definition plus a single import, none with a type
// mismatch. So this is a print check on the ours-alone-to-keep-honest half, and it is *derived*: both
// errors are raised from stage-2 thunks, the resolution helpers run as their arm's body runs, and
// `import after` is checked by the definition's arm only after that arm forced the whole suffix
// (`let m = mf ()`). The deepest thing to raise wins, and the body is deeper than the fold.
func TestResolutionErrorPrecedesImportOrder(t *testing.T) {
	// `(func (type 0) (param i64))` mismatches, and the `(import …)` after a definition is an
	// ordering violation. Both defects, one module.
	const src = `(module
		(type (func (param i32)))
		(func (type 0) (param i64))
		(import "m" "f" (func))
	)`
	err := ReadModule([]byte(src))
	if err == nil {
		t.Fatal("ReadModule accepted a module with two defects")
	}
	const want = "inline function type does not match explicit type"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("ReadModule = %q, want %q — the resolution error is raised from inside the "+
			"func arm's body, where `import after` is raised by that arm only after its "+
			"suffix was forced (parser.mly:1330 et al)", err.Error(), want)
	}
	t.Logf("both defects → %v", err)

	// The vacuity check the rule above needs: each defect alone must still be reported, or the
	// precedence test could pass on a module the parser rejects for a third reason entirely.
	err = ReadModule([]byte(`(module (type (func (param i32))) (func (type 0) (param i64)))`))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("the mismatch alone = %v, want %q", err, want)
	}
	err = ReadModule([]byte(`(module (func) (import "m" "f" (func)))`))
	if err == nil || !strings.Contains(err.Error(), "import after function definition") {
		t.Errorf("the ordering violation alone = %v, want `import after function definition`", err)
	}
}

// TestTypeuseSitesAllResolve is the *space* control rather than a sample of it.
//
// `inline_functype_explicit` is called at **exactly ten sites** in the reference grammar — 708, 743,
// 770, 847, 868, 905, 968, 979, 1049, 1059 — and a site wired to `inline_functype` instead is a
// **silently missing check**: the create-helper never fails, so such a site accepts every mismatched
// module and moves no bucket. The 24 vectors reach four productions (func, block/loop, if,
// call_indirect/return_call_indirect); the other six are covered here or nowhere.
//
// Scoped to the sites, not to the vectors, per *scope controls to the space*. Each row is a mismatch
// that must be rejected, so a site left on the create-helper fails visibly. Twelve rows for ten
// sites: :743 is reached by both `block` and `loop` and :708 by both `call_indirect` and
// `return_call_indirect`, and the pairs are spelled separately because a reader dispatching on the
// keyword could wire one and miss the other.
//
// **`externtype` is deliberately absent, and finding that out is why there are no rows for it.** Its
// two type-bearing arms are `LPAR FUNC bindidx_opt typeuse RPAR` (:1228) and `LPAR FUNC
// bindidx_opt functype RPAR` (:1247) — `typeuse` **XOR** `functype`, never both — so
// `(import "m" "f" (func (type 0) (param i64)))` is not a mismatch to detect, it is `unexpected
// token`, and the sugar arm reaches the create-helper (:1248) with nothing to compare against. Two
// rows here asserted the mismatch error on that spelling and failed against a parser that was right;
// they became the last two rows of TestExterntypeTakesTypeuseXorFunctype.
func TestTypeuseSitesAllResolve(t *testing.T) {
	for _, tc := range []struct{ site, src string }{
		{"func_fields:968", `(module (type (func (param i32))) (func (type 0) (param i64)))`},
		{
			"func_fields:979 (inline import)",
			`(module (type (func (param i32))) (func (import "m" "f") (type 0) (param i64)))`,
		},
		{"tag_fields:1049", `(module (type (func (param i32))) (tag (type 0) (param i64)))`},
		{
			"tag_fields:1059 (inline import)",
			`(module (type (func (param i32))) (tag (import "m" "t") (type 0) (param i64)))`,
		},
		{"block:743", `(module (type (func (param i32))) (func (block (type 0) (param i64) drop)))`},
		{"block:743 (loop)", `(module (type (func (param i32))) (func (loop (type 0) (param i64) drop)))`},
		{"if_block:868", `(module (type (func (param i32))) ` +
			`(func (if (type 0) (param i64) (then drop))))`},
		{"handler_block:770 (flat try_table)", `(module (type (func (param i32))) ` +
			`(func try_table (type 0) (param i64) drop end))`},
		{"try_block:905 (folded try_table)", `(module (type (func (param i32))) ` +
			`(func (try_table (type 0) (param i64) drop)))`},
		{"callinstr_type_instr_list:708 (flat)", `(module (table 0 funcref) ` +
			`(type (func (param i32))) (func call_indirect (type 0) (param i64)))`},
		{"callexpr_type:847 (folded)", `(module (table 0 funcref) (type (func (param i32))) ` +
			`(func (call_indirect (type 0) (param i64) (i32.const 0) (i32.const 0))))`},
		{"callinstr:708 (return_call_indirect)", `(module (table 0 funcref) ` +
			`(type (func (param i32))) (func return_call_indirect (type 0) (param i64)))`},
	} {
		if err := ReadModule([]byte(tc.src)); err == nil {
			t.Errorf("%s: ReadModule(%s) accepted a mismatched signature — this site is wired "+
				"to inline_functype, which never fails, rather than to "+
				"inline_functype_explicit", tc.site, tc.src)
		} else if !strings.Contains(err.Error(), "inline function type does not match") {
			t.Errorf("%s: ReadModule(%s) = %v, want the mismatch error — a different error "+
				"means this row is testing something other than the comparison",
				tc.site, tc.src, err)
		}
	}

	// The accept-direction vacuity check, and it is not optional: every row above is a rejection, so
	// a parser that rejected all twelve spellings outright would score twelve for twelve. The same
	// sites with a *matching* signature must be accepted.
	for _, tc := range []struct{ site, src string }{
		{"func_fields:968", `(module (type (func (param i64))) (func (type 0) (param i64)))`},
		{"tag_fields:1049", `(module (type (func (param i64))) (tag (type 0) (param i64)))`},
		{"block:743", `(module (type (func (param i64))) (func (block (type 0) (param i64) drop)))`},
		{"if_block:868", `(module (type (func (param i64))) ` +
			`(func (if (type 0) (param i64) (then drop))))`},
		{"try_block:905", `(module (type (func (param i64))) ` +
			`(func (try_table (type 0) (param i64) drop)))`},
		{"callexpr_type:847", `(module (table 0 funcref) (type (func (param i64))) ` +
			`(func (call_indirect (type 0) (param i64) (i64.const 0) (i32.const 0))))`},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("%s: ReadModule(%s) = %v, want accepted — without these rows the "+
				"rejection table above is satisfied by rejecting everything",
				tc.site, tc.src, err)
		}
	}
}

// TestSubtypeComparesItsOwnComptype pins that `expand_deftype` does not consult supertypes
// (types.ml:282-284: `SubT (_, _, st) -> st`).
//
// So `(type $b (sub $a (func (param i32))))` compares against `(param i32)`, its own comptype, and a
// typeuse naming `$b` with `$a`'s signature is a mismatch even though `$b` is a subtype of `$a`.
// Inheritance is validation's; equality here is exact. Synthetic — the corpus's subtyping vectors
// are `assert_invalid`, so none of them reaches this comparison.
func TestSubtypeComparesItsOwnComptype(t *testing.T) {
	const src = `(module
		(type $a (sub (func (param i32))))
		(type $b (sub $a (func (param i32))))
		(func (type $b) (param i32))
	)`
	if err := ReadModule([]byte(src)); err != nil {
		t.Errorf("ReadModule = %v; $b's own comptype is `(func (param i32))` and matches", err)
	}
	// The supertype's signature is *not* accepted for the subtype, which is the direction a
	// comparison that walked the supertype chain would get wrong.
	const mismatch = `(module
		(type $a (sub (func (param i32))))
		(type $b (sub $a (func (param i32) (result i32))))
		(func (type $b) (param i32))
	)`
	if err := ReadModule([]byte(mismatch)); err == nil {
		t.Error("ReadModule accepted $a's signature for a typeuse naming $b; expand_deftype " +
			"returns the subtype's own comptype and never consults its supertypes " +
			"(types.ml:282-284). synthetic: the corpus's subtyping vectors are assert_invalid")
	}
}

// TestExterntypeTakesTypeuseXorFunctype pins the one production in the type-bearing family whose arms
// are *alternatives* rather than a typeuse followed by an optional inline signature.
//
//	| LPAR FUNC bindidx_opt typeuse RPAR    parser.mly:1228   ExternFuncT (Idx ($4 c).it)
//	| LPAR TAG  bindidx_opt typeuse RPAR    :1231
//	| LPAR TAG  bindidx_opt functype RPAR   :1234  (sugar)   inline_functype
//	| LPAR FUNC bindidx_opt functype RPAR   :1247  (sugar)   inline_functype
//
// Four arms, no fifth, and the typeuse arms take the index *directly* — no `inline_functype_explicit`,
// hence no comparison and nothing to mismatch. So an import's `(func (type 0) (param i64))` is not a
// signature that disagrees with its declared type, it is a spelling outside the grammar, and the
// verdict is `unexpected token`.
//
// This test is where two rows of TestTypeuseSitesAllResolve went after they failed. They asserted the
// mismatch error on exactly this spelling, and the parser was right: `import.wast`'s type-bearing
// import vectors are all one form or the other, never both, so no vector distinguishes the two
// readings and the wrong one would have sat green. Derived from the arm list; the premise is the
// absence of a fifth arm, which the grep above resolves.
func TestExterntypeTakesTypeuseXorFunctype(t *testing.T) {
	// The two legal forms of each keyword, as the accept-direction floor: a reader that rejected the
	// combination by rejecting the typeuse arm outright would pass the rejection rows alone.
	for _, src := range []string{
		`(module (type (func (param i64))) (import "m" "f" (func (type 0))))`,
		`(module (import "m" "f" (func (param i64))))`,
		`(module (type (func (param i64))) (import "m" "t" (tag (type 0))))`,
		`(module (import "m" "t" (tag (param i64))))`,
	} {
		if err := ReadModule([]byte(src)); err != nil {
			t.Errorf("ReadModule(%s) = %v, want accepted — :1228/:1231 take a typeuse and "+
				":1234/:1247 take a functype, and all four are in the grammar", src, err)
		}
	}
	// Both together is no arm at all. Not a mismatch: the functype's tokens have nowhere to be.
	for _, src := range []string{
		`(module (type (func (param i32))) (import "m" "f" (func (type 0) (param i64))))`,
		`(module (type (func (param i32))) (import "m" "t" (tag (type 0) (param i64))))`,
		// And the *matching* combination is equally malformed, which is the discriminating case:
		// a reader that compared here would accept this one and only reject the row above.
		`(module (type (func (param i64))) (import "m" "f" (func (type 0) (param i64))))`,
	} {
		err := ReadModule([]byte(src))
		if err == nil {
			t.Errorf("ReadModule(%s) accepted a typeuse *and* a functype; externtype has no such "+
				"arm (parser.mly:1226-1248)", src)
			continue
		}
		if strings.Contains(err.Error(), "inline function type does not match") {
			t.Errorf("ReadModule(%s) = %v, want a syntax error — reporting the comparison's "+
				"message here claims externtype calls inline_functype_explicit, and it does not",
				src, err)
		}
	}
}

// TestElemIndexFormNeedsExactlyRefFunc is the control `elemIdxOf` and `refFuncMnemonic` both cite,
// and its subject is the synthesized instruction those two are built out of: `elemidx_list`'s
// expansion writes a `ref.func` the source never spelled, so *nothing in the text* says whether it
// was built right.
//
// # Three facts, three failure modes, and each one is a legal module
//
// `(elem func $f)` expands to `[ref_func x]` (parser.mly:1149), and the expansion is assembled from
// three generated lookups. All three fail in the accept direction:
//
//   - **the opcode**, `opBytes("ref.func")` → `0xd2`. If it were absent both sites panic (`ref.func
//     has no opcode`), and that is what a *wrong* table looks like; but a table that had 0xd2 under
//     another spelling would encode the wrong instruction with no complaint.
//   - **the keyword**, `keywords["ref.func"]` → `REF_FUNC`, which is how `retainIdx` picks the index
//     space (`idxLookupKinds[mnemonic.Keyword]`). A wrong keyword resolves `$f` against a *different
//     space*, and this is the one that produces the quietest defect: `$f` bound in both the func and
//     the table space encodes a different, entirely valid index.
//   - **the round trip through `elemIdxOf`**, which is the same three facts read back on the way out:
//     it re-derives `0xd2`, compares it against the built instruction, and answers `is_elem_index`.
//     A false answer there moves the segment to the *expression* family — flag 5 instead of 1, more
//     bytes, same meaning, no vector to say so.
//
// # What the two comments claimed, measured
//
// `refFuncMnemonic`'s comment said a misspelled `keywordKind` would leave the index resolving "in
// whatever `idxLookupKinds[""]` names, which is `catType`'s zero value and not a refusal." Printed:
// `idxLookupKinds[""]` is `catNone` (0, not `catType`'s 5), and `idxSpaceFor(catNone)` returns nil —
// so an *unrecognized* keyword is refused, with `cannot yet encode a symbolic index on ref.func`.
// The comment named the wrong constant and the wrong outcome for that case.
//
// It is right about the hazard all the same, and the probe that shows it is the one this test is
// built on: a keyword that is wrong but **is** in `idxLookupKinds` gets a space, and the wrong one.
// Substituting `TABLE_GET` resolves `$f` against the table space — `unknown table $f` where the
// module has no table, and worse where it has one. That is the case worth a control, so the rows
// below carry a module where a name is bound in *both* spaces at different indices.
//
// # This is not covered by `encodableModules`' rows, and the gap was measured
//
// Two rows there (`(elem func $f $f)`, `(elem declare func $f)`) do fail under both keyword
// substitutions, so the *symbolic* path has some cover. What they cannot see is which space was
// consulted when the answer happens to be right: a `$f` bound only in the func space produces
// `unknown table $f`, a loud error, where a `$f` bound in both produces a wrong index and silence.
// The `$x`-in-two-spaces row below is the discriminator, and no row in that table has one.
func TestElemIndexFormNeedsExactlyRefFunc(t *testing.T) {
	// The generated facts, asserted directly, so a failure names which lookup drifted rather than
	// arriving as a panic out of a module encode.
	if op, ok := opBytes(refFuncSpelling); !ok || len(op) != 1 || op[0] != 0xd2 {
		t.Errorf("opBytes(%q) = % x, %v; want [d2], true (opcodes.go, generated from lexer.mll:327). "+
			"Both `elemIdxOf` and `elemIdxSink` panic on the !ok path, so this is the assertion that "+
			"distinguishes a missing row from a wrong byte", refFuncSpelling, op, ok)
	}
	kind, ok := keywords[refFuncSpelling]
	if !ok {
		t.Fatalf("keywords has no %q row, so `refFuncMnemonic` panics and no `(elem func …)` with a "+
			"symbolic index can be expanded", refFuncSpelling)
	}
	if got := idxLookupKinds[kind]; got != catFunc {
		t.Errorf("idxLookupKinds[%s] = %d, want catFunc (%d): this is the lookup that decides which "+
			"space a synthesized `ref.func`'s index resolves against, and a wrong space yields a "+
			"valid module denoting different functions", kind, got, catFunc)
	}

	// The whole path, end to end. Each row's want column is the section 9 payload, written from
	// `encode.ml`'s index-form arms by hand — the flag, the elemkind, then the vector of bare LEBs.
	// One row wants a *refusal* instead, and says at its site why a refusal is the only witness it
	// can have.
	for _, c := range []struct {
		what, src string
		want      []byte
		wantErr   string // when set, the row asserts EncodeModule declines and the message contains this
	}{
		{
			// The discriminator: `$x` is func index 1 *and* table index 0, so a resolution against
			// the wrong space encodes `00` where `01` belongs — a module that decodes clean, whose
			// table is initialized from the wrong function. `unknown table $x` cannot be the failure
			// here, because the name is bound in both spaces.
			"a name bound in the func and table spaces at different indices",
			`(module (func) (func $x) (table $x 1 funcref) (elem func $x))`,
			[]byte{0x01, 0x01, 0x00, 0x01, 0x01},
			"",
		},
		{
			// Forward reference: the segment precedes the func, so the index cannot be known at the
			// cursor and `retainIdx` defers through `immPatch` — `elemIdxOf` then re-runs the patch
			// to answer `is_elem_index`. A deferral that never resolved leaves an empty immediate,
			// and the flag would still be 1.
			"a forward-referenced symbolic index",
			`(module (elem func $f) (func $f))`,
			[]byte{0x01, 0x01, 0x00, 0x01, 0x00},
			"",
		},
		{
			// The numeric arm, which takes no lookup at all — so it is the row that stays green under
			// every keyword substitution, and it is here as the partition's protected side.
			"a numeric index",
			`(module (func) (elem func 0))`,
			[]byte{0x01, 0x01, 0x00, 0x01, 0x00},
			"",
		},
		{
			// `(item)` with no instructions: `is_elem_index`'s pattern is a list of length one, so an
			// empty expression fails it and the segment takes the *expression* family — a flag with an
			// elemtype and a bare `0x0b`, not flag 1. The row that says `elemIdxOf`'s
			// `len(s.instrs) != 1` is a length test and not a `> 1` test, which `(elem func)` alone
			// cannot catch because an empty *segment* is vacuously all-index (OCaml `for_all` over
			// `[]`) while an empty *element* is not an index.
			//
			// **The elemtype is `(ref func)` and not `funcref`, and the difference is the whole row.**
			// The obvious spelling — `(elem funcref (item))` — is **stillborn**: `is_elem_kind` is
			// `(NoNull, FuncHT)`, so a nullable `funcref` fails the *kind* half of encode.ml:1064's
			// conjunction before `allElemIndex` is ever consulted, and the segment takes flag 5 for a
			// reason that has nothing to do with the length test. Measured, not reasoned: under an
			// inversion that makes an empty element vacuously an index, `(elem funcref (item))`
			// encodes `01 05 70 01 0b` either way. It was carrying that spelling and passing.
			//
			// With `(ref func)` the two answers are distinguishable at the payload: answering "not an
			// index" takes flag 5, whose elemtype is the general parameterized production (prefix
			// `0x64` + heaptype `func`, `valTypeBytes` since decision 0018's encoder-side
			// implementation) — `05 64 70`; answering "index" takes flag 1, whose elemkind is a bare
			// `0x00` — `01 00`. Before this PR flag 5's byte did not exist and the row was a refusal
			// (#8's frontier); now that `valTypeBytes` answers it, the witness is the payload itself,
			// and it distinguishes the two answers exactly as well as the refusal used to.
			"an element with no instructions is not an index, so it needs the expression form (#8)",
			`(module (func) (elem (ref func) (item)))`,
			[]byte{0x01, 0x05, 0x64, 0x70, 0x01, 0x0b},
			"",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			b, err := EncodeModule([]byte(c.src))
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("EncodeModule(%s) produced % x, want a decline containing %q — this "+
						"segment's one element is not an index, so it must take the expression form, "+
						"and the expression form's elemtype is what this engine cannot yet write",
						c.src, b, c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("EncodeModule(%s) declined as %v, want a message containing %q — a "+
						"decline for a *different* reason would pass a check on error-ness alone "+
						"while saying nothing about which wire family was chosen", c.src, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("EncodeModule(%s): %v", c.src, err)
			}
			got, found := sectionPayload(decodeForTest(t, b), binary.SectionElement)
			if !found {
				t.Fatalf("the encoder produced % x with no element section", b)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s encodes section 9 as % x, want % x — an element index resolved in the "+
					"wrong space, or a segment moved to the wrong family, is a *valid* module either "+
					"way, so this payload is the only witness", c.src, got, c.want)
			}
		})
	}
}
