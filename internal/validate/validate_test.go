// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The behavioural witnesses, and the reason they are keyed on the *wrapped detail* rather than on
// the sentinel.
//
// 2288 of the corpus's 2714 `assert_invalid` vectors expect the bare string `type mismatch`
// (#291's census), so `errors.Is(err, ErrTypeMismatch)` is satisfied by 84.3% of this package's
// possible refusals and discriminates nothing inside the family. A witness that checks only the
// sentinel confirms that *something* was refused — which the suite already confirms, better, over
// thousands of modules. What the suite cannot do is say **which rule** refused, and that is what a
// witness is for: one row per implemented rule, keyed on a substring of the `%w`-wrapped detail
// that only that rule produces.
//
// The accept direction has no corpus half at all. Every one of the board's 4162 greens is a
// rejection, so a rule that refuses a *valid* module is invisible there (contract §9 G-3) — the
// class `brTable`'s comment records finding by hand. Those rows are first, below.

// validated runs the real path — wat → image → module → validator — and separates the stages.
//
// **The separation is the point, not plumbing.** A reject-direction row whose module the *encoder*
// refused passes while asserting nothing about this package, and it does so in the most convincing
// way available: the error is right, the message may even match, and the verdict came from two
// layers up. That is the wrong-layer class this project files under `assert_invalid` passes
// answered above the validator, and here it would be self-inflicted. So an encoder or decoder
// failure is `Fatalf` — the row is broken, not passing — and only the validator's own error is
// returned.
//
// `gate` opens a proposal for the rows whose rule has no subject without it: memory64 for the
// address-type rule, GC for the struct-type-where-a-func-type-is-wanted rule. Nil means v0's
// default policy, `DefaultFeatures()` — the same set the board's default lane runs under, so a row
// that needs more says so at its own site rather than every row running all-on.
func validated(tb testing.TB, wat string, gate func(*binary.Features)) (*Info, error) {
	tb.Helper()
	return Module(decodedModule(tb, wat, gate))
}

// decodedModule is validated's first two stages, for the rows that need the *module* rather than
// the verdict.
//
// Split out rather than copied: the two `Fatalf` messages below are the whole content of the
// "a row the encoder refused says nothing about this package" discipline, and a second copy of
// them is a second place for that discipline to be weakened one word at a time. The caller that
// asked is TestPrefixBulkIsTheRegionBinaryDispatches, which reads a decoded instruction's prefix.
func decodedModule(tb testing.TB, wat string, gate func(*binary.Features)) *binary.Module {
	tb.Helper()

	img, err := text.EncodeModule([]byte(wat))
	if err != nil {
		tb.Fatalf("the wat encoder refused this row's module, so it says nothing about the "+
			"validator: %v\n%s", err, wat)
	}
	f := binary.DefaultFeatures()
	if gate != nil {
		gate(&f)
	}
	m, err := (&binary.Decoder{Features: f}).DecodeModule(img)
	if err != nil {
		tb.Fatalf("the decoder refused this row's module, so it says nothing about the "+
			"validator: %v\n%s", err, wat)
	}
	return m
}

// mustValidate is the accept direction: the module is valid and this package must agree.
func mustValidate(tb testing.TB, wat string) *Info {
	tb.Helper()
	info, err := validated(tb, wat, nil)
	if err != nil {
		tb.Fatalf("valid module refused: %v\n%s", err, wat)
	}
	return info
}

// TestAcceptsValidModulesTheBoardCannotSee is the accept-direction battery.
//
// Every row is a module the spec accepts, chosen because a plausible wrong rule rejects it. The
// board cannot host any of them: `assert_invalid` vectors are satisfied by any refusal, so a
// false reject shows up only as a vector that never existed. Two of these rows are graves — the
// nil-label-types `return` and the double-counted result arity — and one, `meet-bottom`, is the
// specimen that corrected `brTable`'s rule.
func TestAcceptsValidModulesTheBoardCannotSee(t *testing.T) {
	for _, c := range []struct {
		name string
		why  string
		wat  string
		gate func(*binary.Features)
	}{
		{
			name: "non-void body falling off the end",
			why: "the result arity was checked at `end` and again in funcBody, and the second " +
				"check ran against the stack the first had just emptied — so every valid " +
				"non-void function was refused with `expected i32, stack empty`",
			wat: `(module (func (result i32) (i32.const 1)))`,
		},
		{
			name: "body ending in return",
			why: "the other direction of the same duplicated check: the body frame is unreachable " +
				"after `return`, so a second demand against it is satisfied by anything",
			wat: `(module (func (result i32) (return (i32.const 1))))`,
		},
		{
			name: "loop label carries the loop's parameters",
			why: "a branch to a loop re-enters it, so `br 0` carries the loop's *params*; reading " +
				"the label types off the results instead is invisible on every loop whose params " +
				"and results coincide, which is nearly all of them — hence a row where they differ",
			wat: `(module
				(type $t (func (param i32) (result f64)))
				(func (param i32) (result f64)
					(local.get 0)
					(loop (type $t) (br 0))))`,
		},
		{
			name: "block label carries the block's results",
			why:  "the mirror of the row above, so a fix that swapped the two cannot pass both",
			wat: `(module
				(type $t (func (param i32) (result f64)))
				(func (param i32) (result f64)
					(local.get 0)
					(block (type $t) (drop) (f64.const 1))))`,
		},
		{
			name: "br_table arms disagree with each other over a bottom stack",
			why: "`unreached-valid.wast`'s meet-bottom. The arms are matched against the *operand " +
				"types*, not against the default, and after `unreachable` those are bottom — so " +
				"an f32 arm and an f64 default are both satisfied. Arm-versus-default rejects " +
				"this and passes all 133 br_table reject vectors",
			wat: `(module (func
				(block (result f64)
					(block (result f32)
						(unreachable)
						(br_table 0 1 1 (i32.const 1)))
					(drop)
					(f64.const 0))
				(drop)))`,
		},
		{
			name: "unreachable frame supplies operands it does not have",
			why: "valid.ml's bottom. Without it `(unreachable) (i32.add)` is a spurious mismatch, " +
				"and unreached-invalid.wast's 121 vectors are all about this axis",
			wat: `(module (func (result i32) (unreachable) (i32.add)))`,
		},
		{
			name: "select on two bottoms",
			why: "the result is whichever operand is concrete, and in an unreachable frame neither " +
				"is; a reference-type guard that fires on bottom refuses this",
			wat: `(module (func (result i32) (unreachable) (select)))`,
		},
		{
			name: "if without else whose params already are its results",
			why: "the implicit empty else-arm type-checks exactly when params and results match, " +
				"so this must be accepted while the mismatching form is refused",
			wat: `(module
				(type $t (func (param i32) (result i32)))
				(func (param i32) (result i32)
					(local.get 0)
					(if (type $t) (i32.const 1) (then))))`,
		},
		{
			name: "else-arm restarts from the then-arm's entry stack",
			why: "the else-arm begins where the then-arm did, which means the block's *parameters* " +
				"are pushed again — here the else-arm has no instructions at all and is valid " +
				"only because the restored param is the block's result. A reset that truncates " +
				"without re-pushing refuses this with `expected i32, stack empty`",
			wat: `(module
				(type $t (func (param i32) (result i32)))
				(func (param i32) (result i32)
					(local.get 0)
					(if (type $t) (i32.const 1)
						(then (drop) (i32.const 3))
						(else))))`,
		},
		{
			name: "call resolves a function index across the import/defined split",
			why: "an imported function occupies index 0 before any defined one does; resolving " +
				"defined-first type-checks every call in a module without imports and " +
				"misresolves every call in a module with them",
			wat: `(module
				(import "m" "f" (func (param f64)))
				(func (call 0 (f64.const 1))))`,
		},
		{
			name: "i64 memory access takes an i64 address",
			why: "the address type is a module fact read from the memory's own limits; " +
				"hardcoding i32 refuses every valid memory64 module while the gate is on",
			wat:  `(module (memory i64 1) (func (result i32) (i64.const 0) (i32.load)))`,
			gate: func(f *binary.Features) { f.Memory64 = true },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := validated(t, c.wat, c.gate); err != nil {
				t.Errorf("valid module refused: %v\n\nwhy this row exists: %s\n%s", err, c.why, c.wat)
			}
		})
	}
}

// TestRejectsPerRuleWithItsOwnTestimony is the reject direction, one row per implemented rule.
//
// `want` is a substring of the **wrapped detail**, not of the sentinel: `type mismatch` is shared
// by 2288 corpus vectors and so identifies nothing. Each row's string is chosen to be producible
// by exactly one rule in this package, which is what makes the row a witness that *that* rule
// fired rather than that something did.
//
// # What is not here, and why that is a domain statement rather than a gap
//
// Four of this package's refusals have no wat that reaches them, because the decoder gets there
// first: `else outside an if`, `a second else in one if`, `end with no block open`, and
// `instruction after the end of the function body` are all structural facts the decoder decides
// while reading the body. They are returned rather than left to panic — `endBlock`'s comment says
// exactly this — and a witness for them would have to build a `binary.Func` by hand, which tests
// the helper and not the path. `TestLocalMaterializationIsBoundedWithTestimony` is the one place
// this file does that, with its own account of why.
func TestRejectsPerRuleWithItsOwnTestimony(t *testing.T) {
	for _, c := range []struct {
		name     string
		wat      string
		sentinel error
		want     string
		gate     func(*binary.Features)
	}{
		{
			name:     "operand type",
			wat:      `(module (func (result i32) (i32.const 1) (f64.const 1) (i32.add)))`,
			sentinel: ErrTypeMismatch,
			want:     "expected i32, got f64",
		},
		{
			name:     "operand missing entirely",
			wat:      `(module (func (result i32) (i32.add)))`,
			sentinel: ErrTypeMismatch,
			want:     "expected i32, stack empty",
		},
		{
			// The arguments are pushed in the *wrong* order for a mixed signature, which is the
			// only shape that discriminates: a popExpectAll that walked left to right pops f64
			// first, finds an f64, and accepts this module. Its detail is necessarily the same
			// string as the row above — what makes it a separate witness is the module, not the
			// message.
			name:     "mixed signature, arguments in the wrong order",
			wat:      `(module (func (result i32) (i32.const 1) (f64.const 1) (call 1)) (func (param f64 i32) (result i32) (i32.const 0)))`,
			sentinel: ErrTypeMismatch,
			want:     "expected i32, got f64",
		},
		{
			// The reject half of the nil-label-types grave, and the direction that grave escaped
			// through: `funcBody` passed nil for the body frame's label types, so `return` popped
			// nothing and every one of func.wast's `type-return-*` vectors was accepted. The
			// accept-direction rows above cannot catch it — with nil there, they all still pass.
			name:     "return with the function's results missing",
			wat:      `(module (func (result i32) (return)))`,
			sentinel: ErrTypeMismatch,
			want:     "expected i32, stack empty",
		},
		{
			name:     "drop on an empty stack",
			wat:      `(module (func (drop)))`,
			sentinel: ErrTypeMismatch,
			want:     "drop on an empty stack",
		},
		{
			name:     "values left at the end of a block",
			wat:      `(module (func (unreachable) (i32.const 0)))`,
			sentinel: ErrTypeMismatch,
			want:     "value(s) left on the stack at the end of a block",
		},
		{
			name:     "if without else, params not results",
			wat:      `(module (func (result i32) (if (result i32) (i32.const 1) (then (i32.const 1)))))`,
			sentinel: ErrTypeMismatch,
			want:     "if without else must have matching parameters and results",
		},
		{
			// The reference operand comes from a local rather than from `ref.null`, which is
			// itself declined (0xd0) — a row built on it would be answered by the decline arm
			// two instructions earlier and would witness nothing about `select`.
			name: "select needs an annotation for a reference operand",
			wat: `(module (func (result funcref) (local funcref)
				(local.get 0) (local.get 0) (i32.const 0) (select)))`,
			sentinel: ErrTypeMismatch,
			want:     "needs a result-type annotation",
		},
		{
			name:     "select operands disagree",
			wat:      `(module (func (result i32) (i32.const 1) (f64.const 1) (i32.const 0) (select)))`,
			sentinel: ErrTypeMismatch,
			want:     "select operands are",
		},
		{
			name:     "block type index names a non-function type",
			wat:      `(module (type $s (struct)) (func (block (type $s))))`,
			sentinel: ErrTypeMismatch,
			want:     "want func",
			// A struct type needs the GC gate to decode at all; the *rule* under test is
			// slice 1's own — an index that resolves to the wrong composite kind is `type
			// mismatch`, not `unknown type`.
			gate: func(f *binary.Features) { f.GC = true },
		},
		{
			// Arity is enforced even though the arms are matched against the operands rather
			// than against each other: `ts` has the *default's* arity, so an arm of a different
			// arity fails however bottom the stack is. Here the default is the body frame
			// (one result) and the arm is a void block.
			name: "br_table arm of the wrong arity",
			wat: `(module (func (result i32)
				(block
					(i32.const 1)
					(i32.const 0)
					(br_table 0 1))
				(i32.const 9)))`,
			sentinel: ErrTypeMismatch,
			want:     "br_table arm 0 takes []",
		},
		{
			name:     "unknown local",
			wat:      `(module (func (local i32) (drop (local.get 5))))`,
			sentinel: ErrUnknownLocal,
			want:     "unknown local 5",
		},
		{
			name:     "unknown global",
			wat:      `(module (func (drop (global.get 4))))`,
			sentinel: ErrUnknownGlobal,
			want:     "unknown global 4",
		},
		{
			name:     "immutable global",
			wat:      `(module (global i32 (i32.const 0)) (func (global.set 0 (i32.const 1))))`,
			sentinel: ErrGlobalImmutable,
			want:     "immutable global",
		},
		{
			name:     "unknown label",
			wat:      `(module (func (br 4)))`,
			sentinel: ErrUnknownLabel,
			want:     "unknown label 4",
		},
		{
			name:     "unknown function",
			wat:      `(module (func (call 7)))`,
			sentinel: ErrUnknownFunc,
			want:     "unknown function 7",
		},
		{
			name:     "unknown table",
			wat:      `(module (type $t (func)) (func (call_indirect (type $t) (i32.const 0))))`,
			sentinel: ErrUnknownTable,
			want:     "unknown table 0",
		},
		{
			name:     "unknown memory",
			wat:      `(module (func (result i32) (i32.load (i32.const 0))))`,
			sentinel: ErrUnknownMemory,
			want:     "unknown memory 0",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			if err == nil {
				t.Fatalf("invalid module accepted\n%s", c.wat)
			}
			if !errors.Is(err, c.sentinel) {
				t.Errorf("refused with the wrong sentinel: want %v, got %v", c.sentinel, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused for the wrong reason, or by a rule other than this row's:\n"+
					"  want detail containing %q\n  got %v\n"+
					"A row keyed only on the sentinel would have passed here — %q is shared by "+
					"2288 corpus vectors", c.want, err, c.sentinel)
			}
		})
	}
}

// TestUnknownIndexMessagesRender is the depth half of the index-message rule, and the AST control
// (`TestUnknownIndexMessagesAreCategorySpaceIndex`) is the coverage half.
//
// That control checks the format *directive* over every site, which is a proxy: it cannot see what
// `%d` prints. These rows read the rendered string, which is what the corpus matches — the suite
// expects both `unknown local` and `unknown local 2`, as substrings per decision 0003, so the
// index has to be there and has to follow the category immediately.
func TestUnknownIndexMessagesRender(t *testing.T) {
	for _, c := range []struct{ name, wat, want string }{
		{"local", `(module (func (local i32) (drop (local.get 5))))`, "unknown local 5"},
		{"global", `(module (func (drop (global.get 4))))`, "unknown global 4"},
		{"label", `(module (func (br 4)))`, "unknown label 4"},
		{"function", `(module (func (call 7)))`, "unknown function 7"},
		{"table", `(module (type $t (func)) (func (call_indirect (type $t) (i32.const 0))))`, "unknown table 0"},
		{"memory", `(module (func (result i32) (i32.load (i32.const 0))))`, "unknown memory 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, nil)
			if err == nil {
				t.Fatalf("invalid module accepted\n%s", c.wat)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("rendered message does not contain %q: %v.\nThe corpus matches this as a "+
					"substring, so a message that separates the category from its index — "+
					"`unknown local: local 5` — satisfies the bare vector and fails the indexed "+
					"one while being right about the module", c.want, err)
			}
		})
	}
}

// TestSelectAnnotatedTypesAgainstItsAnnotation is the behavioural check the opcode-table control
// cannot make: 0x1B and 0x1C both render as `select` in `binary.OpMnemonic`, so a swap of the two
// constants passes there and is caught only here.
//
// **This test was the decline's tripwire and is now the rule's, rewritten in the diff that flipped
// it.** Until #294 it asserted `ErrUnsupported` for the annotated form and cited the issue that
// would retire it; the retention landed, the assertion failed, and *the failure was the whole point
// of having written it that way* — a design debt discharged by a tripwire rather than by an
// intention. What replaces it is the same pair, read in the other direction: both forms are typed
// now, and the annotated one is typed *from the annotation*, which is the only thing the bare arm
// cannot do.
func TestSelectAnnotatedTypesAgainstItsAnnotation(t *testing.T) {
	const bare = `(module (func (result i32) (i32.const 1) (i32.const 2) (i32.const 0) (select)))`
	if _, err := validated(t, bare, nil); err != nil {
		t.Errorf("bare `select` is in slice 1's scope and must be typed, not declined: %v", err)
	}

	const annotated = `(module (func (result i32) (i32.const 1) (i32.const 2) (i32.const 0) (select (result i32))))`
	if _, err := validated(t, annotated, nil); err != nil {
		t.Errorf("`select t` is typed as of #294 and this module is valid: %v", err)
	}

	// The annotation is *load-bearing*, not decoration. A reference-typed `select` is exactly the
	// module the bare arm refuses (`needs a result-type annotation`, one table over), so its
	// acceptance here can only come from reading the retained vector — an arm that ignored the
	// annotation and re-derived the type from the operands would refuse this and pass every
	// numeric row above.
	const ref = `(module (func (result funcref) (local funcref)
		(local.get 0) (local.get 0) (i32.const 0) (select (result funcref))))`
	if _, err := validated(t, ref, nil); err != nil {
		t.Errorf("an annotated `select` over references is the case the annotation exists for: %v", err)
	}

	const wrong = `(module (func (result i64) (i64.const 1) (i64.const 2) (i32.const 0) (select (result i64))))`
	if _, err := validated(t, wrong, nil); err != nil {
		t.Errorf("i64 operands under an i64 annotation is valid: %v", err)
	}

	// And the annotation is *believed*, not merely consulted: the rule pops both operands against
	// the annotated type, so an annotation disagreeing with well-typed operands refuses.
	//
	// **The declared result is `i64` here deliberately, and the first version of this row had `i32`.**
	// That version was green under a falsification that read the annotation and then derived the type
	// from the operands anyway — because with `(result i32)` declared, the *frame's* end-of-body check
	// objected to the i64 left on the stack, and the row could not tell which rule had spoken. Making
	// the function agree with its operands leaves the select arm as the only thing with a complaint,
	// which is why the detail is asserted too: `instr 3 (select)` is this rule refusing, and a
	// mismatch reported anywhere else is the coincidence coming back.
	const mismatch = `(module (func (result i64) (i64.const 1) (i64.const 2) (i32.const 0) (select (result i32))))`
	_, err := validated(t, mismatch, nil)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("an i32 annotation over i64 operands must be a type mismatch, not an accept: %v", err)
	} else if !strings.Contains(err.Error(), "(select)") {
		t.Errorf("the mismatch is reported by some rule other than `select`: %v", err)
	}
}

// TestSelectAnnotationArityIsTheValidatorsRule is the pair of vectors #294's retention exists to
// answer, and it is here rather than in the table above because both rows share a sentinel the
// table has only one of.
//
// The decoder files arity-0 and arity-2 annotations knowing they are unusable — they are well-formed
// *encodings*, and a decoder rejecting them would manufacture malformedness out of a typing rule
// (`internal/binary`'s selectt_test.go carries that argument). This is the layer that says so, with
// `valid.ml:443`'s own string.
func TestSelectAnnotationArityIsTheValidatorsRule(t *testing.T) {
	for _, c := range []struct{ name, wat string }{
		// select.wast:368 and :373 — the two corpus vectors, and the only two arities that are
		// neither one nor unbounded.
		{"arity 0", `(module (func (i32.const 1) (i32.const 2) (i32.const 0) (select (result)) (drop)))`},
		{"arity 2", `(module (func (result i32) (i32.const 1) (i32.const 2) (i32.const 0)
			(select (result i32 i32))))`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, nil)
			if !errors.Is(err, ErrInvalidResultArity) {
				t.Fatalf("want ErrInvalidResultArity, got %v.\nThe decoder files this vector "+
					"deliberately; if nothing refuses it the module is accepted with an "+
					"annotation no rule ever read", err)
			}
			if !strings.Contains(err.Error(), "not (yet) allowed") {
				t.Errorf("the message drops the reference's parenthetical: %v. The corpus matches "+
					"a substring, so a paraphrase asserts a stability valid.ml declines to", err)
			}
		})
	}

	// The index-space half, which is `ref.wast:78`: an annotation naming a type index nothing
	// declares. `check_valtype` is the reference's rule and the arity rule cannot reach it — a
	// validator checking only the count accepts this.
	_, err := validated(t, `(module (func (i32.const 1) (i32.const 2) (i32.const 0)
		(select (result (ref null 1))) (drop)))`, func(f *binary.Features) { f.GC = true })
	if !errors.Is(err, ErrUnknownType) {
		t.Errorf("want ErrUnknownType for an annotation naming type 1 in a module with none: %v", err)
	}
}

// TestDeclinesAreDeclinesAndNameTheirOpcode checks the two shapes of out-of-scope refusal, and that
// each one says *what* it declined.
//
// The bucket these land in is read by whoever picks up the next slice, so `opcode 0xfc 0x0a` is a
// lookup task where `memory.copy` is a work item — `errNoSignature`'s own argument. A decline that
// names only a byte is a visible refusal with unusable testimony.
func TestDeclinesAreDeclinesAndNameTheirOpcode(t *testing.T) {
	for _, c := range []struct {
		name, wat, want string
		gate            func(*binary.Features)
	}{
		{
			// A single-byte opcode with no numeric type prefix: falls past every structural arm
			// and out of `signature` as errNoSignature.
			//
			// **Re-pointed by slice 6, exactly as the sentence below it predicted** — the
			// specimen was `ref.null`, which #359 types, and before that `memory.grow`, which
			// slice 5 typed. The population this row draws from is *drained by every slice*, so a
			// hand-named specimen here is a scheduled failure rather than a risk: what the row
			// asserts is that an unclaimed single-byte opcode declines by name, and the specimen is
			// only the current witness to it. Deriving the specimen instead of naming it is #326,
			// and each re-point is another quote for that issue's argument.
			//
			// `ref.as_non_null` (0xD4) is the new witness, and the choice is deliberate: slice 6
			// claims 0xD0-0xD2 and stops, so the boundary now runs *through* the `ref.*` family
			// rather than around it. 0xD3-0xD6 — `ref.eq` and three of the function-references five
			// (0008) — are what is left, and a specimen from inside the family this slice just
			// claimed is the one that would go stale silently if the arms were widened by mnemonic
			// prefix rather than by opcode.
			name: "single-byte, no signature",
			wat:  `(module (func (param funcref) (result (ref func)) (ref.as_non_null (local.get 0))))`,
			want: "ref_as_non_null",
			gate: func(f *binary.Features) { f.GC = true },
		},
		{
			// A prefixed region no slice has claimed. **Re-pointed by slice 5**: the specimen was
			// `memory.copy` under the sentence "the prefixed regions, all four of which are later
			// slices'", and two of the four are now this package's, so the sentence and the row moved
			// together. `ref.i31` is `0xfb 0x1c`, and the gate is on to clear the *decoder* — what
			// is being read is the validator's refusal below it.
			name: "prefixed region",
			wat:  `(module (func (result i31ref) (ref.i31 (i32.const 0))))`,
			want: "prefixed opcode 0xfb",
			gate: func(f *binary.Features) { f.GC = true },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("want a decline (ErrUnsupported), got %v.\nAccepting an instruction this "+
					"slice cannot type reports *valid* for a module nothing type-checked, which is "+
					"the accept-direction failure no board can see (§9 G-3)", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the decline does not name what it declined (want %q): %v", c.want, err)
			}
		})
	}
}

// TestFuncInfoArityDistinguishesLoopFromBlock is the metadata half, and it exists because nothing
// consumes `Info` yet.
//
// A return value no caller reads is a return value no test failure describes: the interpreter is
// #9's next slice, so until it lands, these fields are checked here or not at all. `Arity`'s two
// fields are the discriminating case — a single "arity" is right for `block` and wrong for `loop`,
// and wrong in a way that passes every block-shaped vector.
func TestFuncInfoArityDistinguishesLoopFromBlock(t *testing.T) {
	// Two openers with the *same* block type, params ≠ results, so the two fields must differ
	// for the loop and agree for the block.
	const src = `(module
		(type $t (func (param i32 i32) (result f64)))
		(func (param i32 i32) (result f64)
			(local.get 0) (local.get 1)
			(block (type $t) (drop) (drop) (f64.const 1))
			(drop)
			(local.get 0) (local.get 1)
			(loop (type $t) (drop) (drop) (f64.const 1))))`

	info := mustValidate(t, src)
	if len(info.Funcs) != 1 {
		t.Fatalf("Funcs has %d entries, want 1", len(info.Funcs))
	}
	blocks := info.Funcs[0].Blocks

	var got []Arity
	for i := range 64 {
		if a, ok := blocks[i]; ok {
			got = append(got, a)
		}
	}
	if len(got) != 2 {
		t.Fatalf("Blocks recorded %d openers, want 2 (one block, one loop); keyed by index into "+
			"Func.Body, which is the internal form's own coordinate: %v", len(got), blocks)
	}
	// In body order: the block first, then the loop.
	if want := (Arity{Label: 1, End: 1}); got[0] != want {
		t.Errorf("block arity is %+v, want %+v — a `br` to a block's label carries its results", got[0], want)
	}
	if want := (Arity{Label: 2, End: 1}); got[1] != want {
		t.Errorf("loop arity is %+v, want %+v — a `br` to a loop's label carries its two "+
			"*parameters* and falling off its end yields its one result. Equal fields here mean "+
			"one quantity is being computed twice", got[1], want)
	}
}

// TestMaxStackCountsSlotsNotValues pins the one place 0024's two-slot v128 is observable in slice 1.
//
// Every instruction that *produces* a v128 is in the 0xFD region and therefore declined, so the
// only way a v128 reaches this validator's operand stack is `local.get` of a v128 local — which
// makes this a narrow path and the sole witness for the slots-versus-values rule. A `MaxStack` that
// counted values would under-allocate every frame holding a vector, and the frame allocator is the
// consumer, so the error would surface as memory corruption in a later slice rather than as a
// wrong number here.
func TestMaxStackCountsSlotsNotValues(t *testing.T) {
	for _, c := range []struct {
		name string
		wat  string
		want int
	}{
		{"one i32", `(module (func (local i32) (drop (local.get 0))))`, 1},
		{"two i64", `(module (func (local i64 i64) (drop (local.get 0)) (drop (local.get 1))))`, 1},
		{
			"two i64 live at once",
			`(module (func (local i64 i64) (local.get 0) (local.get 1) (drop) (drop)))`,
			2,
		},
		{
			"one v128 is two slots",
			`(module (func (local v128) (drop (local.get 0))))`,
			2,
		},
		{
			"a v128 beside an i32 is three",
			`(module (func (local v128 i32) (local.get 0) (local.get 1) (drop) (drop)))`,
			3,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			info := mustValidate(t, c.wat)
			if got := info.Funcs[0].MaxStack; got != c.want {
				t.Errorf("MaxStack is %d, want %d slots (0024: a v128 occupies two adjacent "+
					"64-bit slots everywhere a slot is a thing)", got, c.want)
			}
		})
	}
}

// TestFuncsIsIndexedByPositionNotByFunctionIndex pins the offset `Info.Funcs`' own comment names.
//
// An imported function occupies function index 0 and has no body, so a consumer indexing `Funcs`
// by function index reads the wrong metadata for every function in a module with imports — and
// reads it *successfully*, which is the failure mode worth a test.
func TestFuncsIsIndexedByPositionNotByFunctionIndex(t *testing.T) {
	const src = `(module
		(import "m" "f" (func))
		(func (result i32) (i32.const 1) (i32.const 2) (drop))
		(func))`

	info := mustValidate(t, src)
	if len(info.Funcs) != 2 {
		t.Fatalf("Funcs has %d entries, want 2 — one per *defined* function, the import having no "+
			"body to describe", len(info.Funcs))
	}
	// Position 0 is the first defined function (function index 1), which is the one with a stack.
	if got := info.Funcs[0].MaxStack; got != 2 {
		t.Errorf("Funcs[0].MaxStack is %d, want 2: position 0 must be the first *defined* "+
			"function. A 0 here means the entries are offset by the one import", got)
	}
	if got := info.Funcs[1].MaxStack; got != 0 {
		t.Errorf("Funcs[1].MaxStack is %d, want 0", got)
	}
}

// TestLocalMaterializationIsBoundedWithTestimony is the one direct-helper witness in this file, and
// the reason is stated rather than assumed.
//
// `localTypes`' bound cannot be reached through wat: the text format has no repeat-count for
// locals, so a module declaring a million of them is a megabyte of source, and one declaring
// 0xFFFFFFFE is unwritable. The decoder *does* enforce the reference's `total >= 1<<32`, so the
// path that reaches this bound is a caller building a `binary.Func` itself — exactly the case the
// function's comment names. Calling it directly tests the helper and not the path, which is
// normally the weaker thing to do; here it is the only available thing, and the alternative is a
// bound with no witness at all.
//
// What is being protected is *testimony*, not correctness: grave #138 keeps the local groups
// unexpanded precisely so a 30-byte image cannot demand 4 GiB, and materializing without a bound
// would be OOM-killed — no error, no bucket, no board row.
func TestLocalMaterializationIsBoundedWithTestimony(t *testing.T) {
	ft := binary.FuncType{}

	// Past the reference's own limit: its verdict, which the decoder normally reaches first.
	over32 := &binary.Func{Locals: []binary.LocalGroup{{Count: ^uint32(0)}, {Count: 2}}}
	_, err := localTypes(ft, over32)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("a body declaring 2^32+1 locals must be refused with the reference's verdict: %v", err)
	}

	// Inside the reference's limit and past what this validator will allocate.
	huge := &binary.Func{Locals: []binary.LocalGroup{{Count: maxMaterializedLocals + 1}}}
	_, err = localTypes(ft, huge)
	if err == nil {
		t.Fatalf("%d locals were materialized; the bound exists so the failure has testimony "+
			"instead of being an OOM kill with none", maxMaterializedLocals+1)
	}
	if !strings.Contains(err.Error(), "materializes at most") {
		t.Errorf("the refusal must say it is this validator's bound and not the spec's, so a "+
			"reader is not sent to look for a rule that does not exist: %v", err)
	}

	// And the ordinary case still materializes, so the two bounds above are not a refusal of
	// everything — the vacuity check on a pair of limits.
	ok, err := localTypes(binary.FuncType{Params: []binary.ValType{binary.I32}},
		&binary.Func{Locals: []binary.LocalGroup{{Count: 3, Type: binary.F64}}})
	if err != nil {
		t.Fatalf("an ordinary body was refused: %v", err)
	}
	if len(ok) != 4 || ok[0] != binary.I32 || ok[3] != binary.F64 {
		t.Errorf("params then locals, flattened in order: got %v", ok)
	}
}
