// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// ehOn is the gate the family needs at the *decoder*, which is the layer none of these rows is about.
// `ExceptionHandling` is absent from `DefaultFeatures()`, so without it every module below is refused
// one layer early and every assertion here would be about `binary` — `tailCallOn`'s reason next door.
func ehOn(f *binary.Features) { f.ExceptionHandling = true }

// ehAndRefsOn adds GC, and it is needed by more rows than it should be.
//
// Two rows want it legitimately: `(ref $t)` and `(ref null $t)` as a block's result type are
// function-references/GC syntax, and `checkCatch`'s relation cannot be witnessed as a *relation*
// rather than as equality without them.
//
// The rest want it because of **#395**, found by writing this file: the decoder puts `exn` (-0x17) and
// `noexn` (-0x0C) in the same arm as GC's eight abstract heap types, so `exnref` — the exception
// handling proposal's own value type, `Exceptions.md:337-349` — is refused with `gc: feature gate
// disabled` when only `ExceptionHandling` is on. Every row below that spells `exnref` therefore opens a
// gate that has nothing to do with what it is testing, and the rows say which reason applies at their
// own site. Neither board lane can see this (default has both gates off, all-on has both on), which is
// why it is filed with a unit witness owed in `internal/binary` rather than a board delta.
func ehAndRefsOn(f *binary.Features) { f.ExceptionHandling, f.GC = true, true }

// The exception-handling witnesses (slice 10, ADR 0036), and what the board can and cannot say about
// this family.
//
// # The board's half, measured before these rows were written
//
// ADR 0036's criterion is 25 declines plus 4 admissions, pre-registered per file and per expected
// string, and it closed exactly. So the *reject* direction of this family is witnessed thickly by the
// corpus: 14 `assert_invalid (module)` vectors across `throw.wast`, `throw_ref.wast` and
// `try_table.wast`, of which 11 expect the bare `type mismatch` and 3 pin a longer string.
//
// The marginal value of a row here is therefore the same as slice 9's, in the same two places:
//
//   - the **accept** direction, which no `assert_invalid` can score (contract §9 G-3). All 11 accept-side
//     rows in the criterion are `module text` definitions, and 0032's reading applies — a definition
//     going green says the validator stopped refusing it, not that any particular rule is right. Every
//     row below marked valid is a module a plausible wrong rule rejects.
//   - the **discriminators the corpus is thin on.** `grep -rn "instruction requires" testdata/spec/*.wast`
//     returns exactly two rows, both in `throw.wast`, and they are the whole corpus witness for
//     `popSeqExpect`'s wording *and* for `pop`'s padding rule. ADR 0036 said that thinness is where the
//     falsification bill should be aimed; two of the rows below are that aim, pinning both messages
//     verbatim so a helper that pads unconditionally fails here as well as on the board.
//
// # The one rule the corpus cannot separate at all, and it is the family's trap
//
// `check_catch c ct ts2` takes **`c`, the enclosing context**, not `c'` (`valid.ml:583`). Reading it as
// `c'` shifts every clause's label depth by one — and the idiomatic vector cannot see it: a `try_table`
// inside a `block` whose handler targets that block reads plausibly under either numbering, which is
// what `try_table.wast:30` is. `internal/text` had to answer the same question one layer down and
// `label_test.go` records the same finding, that the spelling which separates the two readings is
// *nowhere in the suite*.
//
// So the first battery below is two rows written for that seam, one in each direction, and they are the
// only witnesses in this repository that the depths are numbered from outside. Neither is a corpus
// vector. That is the shape this project files under a rule whose two readings coincide on every module
// anyone writes by hand — `enterBlock`'s loop-label rule is the same shape, one file over.
func TestTryTableClauseLabelsAreNumberedFromTheEnclosingContext(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
	}{
		{
			// The enclosing frame is the function body, whose label takes nothing, so a `catch_all`
			// handing it nothing is fine. Under `c'` label 0 would be the `try_table`'s own frame,
			// which takes `[i32]`, and this module would be refused.
			name:  "catch_all targets the function body past a try_table that returns a value",
			wat:   `(module (func (try_table (result i32) (catch_all 0) (i32.const 1)) (drop)))`,
			valid: true,
		},
		{
			// The mirror, and the row that matters more: under `c'` this module is *accepted*, because
			// label 0 would be the clauseless `try_table` frame. Read correctly, label 0 is the
			// enclosing `block`, which takes `[i32]`, and a `catch_all` hands it nothing.
			//
			// An accept-direction row cannot catch the `c'` misreading in this direction — the module
			// is invalid and a wrong rule says nothing — which is why the pair is written rather than
			// the first row alone.
			name:  "catch_all targets an enclosing block that takes a value",
			wat:   `(module (func (block (result i32) (try_table (catch_all 0)) (i32.const 1)) (drop)))`,
			msg:   "catch handler provides [] but label 0 takes [i32]",
			valid: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertCatchRow(t, c.wat, c.msg, c.valid, ehOn,
				"a clause's label depth is numbered in the context *enclosing* the try_table "+
					"(`check_catch c`, valid.ml:583), and no corpus vector distinguishes that from "+
					"numbering it inside")
		})
	}
}

// TestCatchClausesHandTheirLabelWhatTheArmSays is `check_catch`'s four arms (`valid.ml:974-989`), one
// pair per arm where a pair is available.
//
// Each arm computes what the handler *hands to* its label: the tag's parameters for `catch`, those plus
// `(ref exn)` for `catch_ref`, nothing for `catch_all`, `(ref exn)` alone for `catch_all_ref`. The
// corpus refuses the wrong shapes; what it cannot do is confirm the right ones, and an arm that emits
// one type too few is refused by nothing on the reject side either — a shorter handler list simply
// fails a different vector's arity check for the same verdict.
//
// The last two rows are the relation. `matches` rather than equality, so `(ref exn)` flows into a label
// taking `exnref` and a tag's `(ref null $t)` does *not* flow into a label taking `(ref $t)` — which is
// what `try_table.wast:470,483` are, the only two vectors in this family that need slice 5's subtype
// relation. Their accept mirror is here because the board has none.
func TestCatchClausesHandTheirLabelWhatTheArmSays(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
		gate  func(*binary.Features)
	}{
		{
			name:  "catch hands the tag's parameters over",
			wat:   `(module (tag $e (param i32)) (func (block (result i32) (try_table (catch $e 0)) (i32.const 1)) (drop)))`,
			valid: true,
			gate:  ehOn,
		},
		{
			name:  "catch's parameters are the wrong type for the label",
			wat:   `(module (tag $e (param f32)) (func (block (result i32) (try_table (catch $e 0)) (i32.const 1)) (drop)))`,
			msg:   "catch handler provides [f32] but label 0 takes [i32]",
			valid: false,
			gate:  ehOn,
		},
		{
			// `catch_ref` appends `(ref exn)` (`valid.ml:983`). The accept row is the witness that it
			// is appended at all: with the exnref omitted the handler is `[i32]`, which this label
			// refuses and the *next* row's label accepts — so the two rows separate the arm's two
			// plausible shapes in a way neither does alone.
			name:  "catch_ref appends the exception reference",
			wat:   `(module (tag $e (param i32)) (func (block (result i32 exnref) (try_table (catch_ref $e 0)) (unreachable)) (drop) (drop)))`,
			valid: true,
			gate:  ehAndRefsOn, // `exnref` under the wrong gate — #395
		},
		{
			name:  "catch_ref's exnref has nowhere to go on a label that takes only the payload",
			wat:   `(module (tag $e (param i32)) (func (block (result i32) (try_table (catch_ref $e 0)) (i32.const 1)) (drop)))`,
			msg:   "catch handler provides [i32 (ref exn)] but label 0 takes [i32]",
			valid: false,
			gate:  ehOn,
		},
		{
			// `match_target c [] (label c x)`: a `catch_all` hands nothing over, so a label expecting
			// an exnref refuses it. `try_table.wast:399` is the corpus row; this is its accept mirror
			// one row up in the first battery.
			name:  "catch_all hands nothing to a label that wants an exnref",
			wat:   `(module (func (block (result exnref) (try_table (catch_all 0)) (unreachable)) (drop)))`,
			msg:   "catch handler provides [] but label 0 takes [(ref null exn)]",
			valid: false,
			gate:  ehAndRefsOn, // #395
		},
		{
			// Two rules in one row, and both are load-bearing: `catch_all_ref` hands over exactly one
			// value, and it is the **non-null** `(ref exn)` flowing into a nullable `exnref` label,
			// which only holds under a relation. A `checkCatch` written with `==` refuses this module.
			name:  "catch_all_ref hands a non-null exception reference to a nullable label",
			wat:   `(module (func (block (result exnref) (try_table (catch_all_ref 0)) (unreachable)) (drop)))`,
			valid: true,
			gate:  ehAndRefsOn, // #395
		},
		{
			name:  "catch_all_ref's exnref has nowhere to go on a label that takes nothing",
			wat:   `(module (func (try_table (catch_all_ref 0))))`,
			msg:   "catch handler provides [(ref exn)] but label 0 takes []",
			valid: false,
			gate:  ehOn,
		},
		{
			// The relation in the direction it holds: the tag hands a non-null reference to a label
			// taking the nullable form.
			name:  "a tag's non-null reference parameter flows into a nullable label",
			wat:   `(module (type $t (func)) (tag $e (param (ref $t))) (func (block (result (ref null $t)) (try_table (catch $e 0)) (unreachable)) (drop)))`,
			valid: true,
			gate:  ehAndRefsOn,
		},
		{
			// And in the direction it does not — `try_table.wast:470,483`'s shape, which the row above
			// cannot see because a relation checked backwards still accepts equal types.
			name:  "a tag's nullable reference parameter does not flow into a non-null label",
			wat:   `(module (type $t (func)) (tag $e (param (ref null $t))) (func (block (result (ref $t)) (try_table (catch $e 0)) (unreachable)) (drop)))`,
			msg:   "catch handler provides",
			valid: false,
			gate:  ehAndRefsOn,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertCatchRow(t, c.wat, c.msg, c.valid, c.gate,
				"a clause hands its label exactly what its arm computes, and an arm one type short is "+
					"refused by no vector on either side")
		})
	}
}

// assertCatchRow is the two batteries' shared assertion, and it is a function rather than a copy for
// `decodedModule`'s reason: the reject rows are keyed on the **message** because `ErrTypeMismatch` is
// satisfied by most of this package's refusals and discriminates nothing inside this family, and a
// second copy of that argument is a second place for it to weaken into a sentinel check.
//
// The message is a substring of `catchMismatch`'s text — which no vector pins, all seven corpus clause
// rows expecting the bare `type mismatch` — so these rows are the only readers of it, and they read the
// operand lists in both slots. That is deliberate: the reference's own composition of this sentence
// swaps the two role names (`match_result_type "label" "catch handler"`, `valid.ml:976`), so a
// transcription of it would have been *checkable against nothing*, and asserting the payload here is
// what makes this port's decision to fix the wording a claim rather than a preference.
func assertCatchRow(tb testing.TB, wat, msg string, valid bool, gate func(*binary.Features), rule string) {
	tb.Helper()
	_, err := validated(tb, wat, gate)
	switch {
	case valid && err != nil:
		tb.Errorf("valid module refused: %v\n%s\nAn over-rejection, invisible to the board: every "+
			"`assert_invalid` vector is satisfied by any refusal, so this row is the witness that "+
			"%s.", err, wat, rule)
	case !valid && err == nil:
		tb.Errorf("invalid module accepted\n%s\n%s", wat, rule)
	case !valid && !errors.Is(err, ErrTypeMismatch):
		tb.Errorf("refused with the wrong sentinel: want %v, got %v", ErrTypeMismatch, err)
	case !valid && !strings.Contains(err.Error(), msg):
		tb.Errorf("refused, but not with %q: %v\nThe rows are keyed on the message because "+
			"`ErrTypeMismatch` is shared by most of this package's refusals, and a row refused by a "+
			"different check would pass a sentinel test while asserting nothing about the rule it "+
			"names.", msg, err)
	}
}

// TestThrowConsumesItsTagsPayloadThenGoesPolymorphic is `Throw x` (`valid.ml:572-576`) and, through it,
// `popSeqExpect` — including the two rows the whole corpus has for the reference's operand-mismatch
// wording.
//
// # The two message rows are the point of this battery
//
// `throw.wast:53,55` are the *only* vectors in the suite that pin
// `type mismatch: instruction requires [i32] but stack has []` and its `[i64]` sibling. They are
// already on the board and green, so what these two rows add is not coverage but **locality**: a
// change to `popSeqExpect`'s wording or padding fails here, in the package that owns it, rather than
// two files away in a suite count. The `[]` row is the padding rule — `peekN` pads with bottom
// unconditionally and would print `[bot]` on a *reachable* frame, which is a message asserting an
// operand the module does not have (grave #36's class) while the verdict stays right.
//
// The unreachable row is the same rule from the other side, and it is an accept row: after
// `unreachable`, `pop` pads and the payload is satisfied by bottom, so a helper that failed on length
// unconditionally would refuse a valid module. The corpus has that shape for other instructions and
// not for `throw`.
//
// # What no row here can witness, stated because the helper has two halves
//
// `popSeqExpect` also *pops* the matched operands, and through `throw` that is unobservable: the only
// caller goes unreachable immediately, so the residue it leaves is never read again. `throwInstr` is
// the helper's sole caller today, which means half of it is witnessed by nothing in this repository or
// on the board — the same two-halves-cover-for-each-other shape `returnCall`'s falsification bill
// found, named here in advance of the second caller #394 will bring.
func TestThrowConsumesItsTagsPayloadThenGoesPolymorphic(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
	}{
		{
			name:  "the payload is on the stack",
			wat:   `(module (tag $e (param i32)) (func (i32.const 1) (throw $e)))`,
			valid: true,
		},
		{
			// `-->...`, the polymorphic tail: a `throw` does not fall through, so the frame goes
			// unreachable and the function's declared result is satisfied by nothing. A rule that
			// omitted `setUnreachable` refuses this module and passes every reject vector in the file.
			name:  "a value-returning function whose body only throws",
			wat:   `(module (tag $e) (func (result i32) (throw $e)))`,
			valid: true,
		},
		{
			// `pop`'s padding rule in the accept direction: bottom satisfies the payload on an
			// unreachable frame.
			name:  "the payload is missing but the frame is unreachable",
			wat:   `(module (tag $e (param i32)) (func unreachable (throw $e)))`,
			valid: true,
		},
		{
			name:  "the payload is the wrong type — throw.wast:55's wording",
			wat:   `(module (tag $e (param i32)) (func (i64.const 1) (throw $e)))`,
			msg:   "instruction requires [i32] but stack has [i64]",
			valid: false,
		},
		{
			// The padding rule in the reject direction: `[]`, not `[bot]`, on a reachable frame.
			name:  "the payload is missing on a reachable frame — throw.wast:53's wording",
			wat:   `(module (tag $e (param i32)) (func (throw $e)))`,
			msg:   "instruction requires [i32] but stack has []",
			valid: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertCatchRow(t, c.wat, c.msg, c.valid, ehOn,
				"`throw` pops its tag's parameters with the reference's own message and padding rule "+
					"and then goes polymorphic")
		})
	}
}

// TestThrowRefTakesANullableExnref is `ThrowRef` (`valid.ml:578-579`), whose whole rule is
// `[RefT (Null, ExnHT)] -->... []`.
//
// Two rows earn their place. The non-null one is the relation — `popExpect` uses `matches`, so
// `(ref exn)` flows into the `exnref` the rule names, and a rule written with `==` refuses a valid
// module that `throw_ref.wast` does not contain. The value-returning one is `setUnreachable`, the same
// witness the `throw` battery has, because the two arms set it independently and a missing call in one
// is invisible from the other.
func TestThrowRefTakesANullableExnref(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
		gate  func(*binary.Features)
	}{
		{
			name:  "a nullable exception reference",
			wat:   `(module (func (param exnref) (local.get 0) (throw_ref)))`,
			valid: true,
			gate:  ehAndRefsOn, // #395
		},
		{
			name:  "a non-null exception reference flows in",
			wat:   `(module (func (param (ref exn)) (local.get 0) (throw_ref)))`,
			valid: true,
			gate:  ehAndRefsOn, // #395
		},
		{
			name:  "the frame is polymorphic afterwards",
			wat:   `(module (func (param exnref) (result i32) (local.get 0) (throw_ref)))`,
			valid: true,
			gate:  ehAndRefsOn, // #395
		},
		{
			// `throw_ref.wast:117,118` expect the bare `type mismatch`, which is why this arm keeps
			// `popExpect`'s own wording rather than `popSeqExpect`'s — the divergence inside
			// `exception.go` that #394 converges. The message names the type in its *parameterized*
			// spelling, `(ref null exn)`, because that is what `exnRef` is: `exnref` is the wire
			// shorthand and `typeList` prints the constructor.
			//
			// This row needs no GC gate — nothing in it spells `exnref` — which makes it the one row in
			// this battery whose gate is the family's own. Left at `ehOn` deliberately rather than
			// aligned with its neighbours: a row running under more gates than its subject needs is a
			// row that would keep passing after #395 is fixed *and* if the fix were wrong.
			name:  "not an exception reference at all",
			wat:   `(module (func (param i32) (local.get 0) (throw_ref)))`,
			msg:   "expected (ref null exn), got i32",
			valid: false,
			gate:  ehOn,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertCatchRow(t, c.wat, c.msg, c.valid, c.gate,
				"`throw_ref` consumes one nullable exnref under the subtype relation and leaves the "+
					"frame unreachable")
		})
	}
}

// TestTagIndexSpaceInterleavesImports is `tagTypeAt`, and it is here for the reason
// `TestReturnCallReadsTheFunctionIndexSpace` is: the mistake it catches is invisible to the corpus.
//
// A tag import keeps its type index in `Import.Index` rather than in a descriptor of its own
// (`decodeImport` case `0x04`, #204), and the imported tags occupy the low indices. Every `try_table`
// and `throw` vector in the suite is in a module whose tags are all *defined*, so a validator that
// walked `m.Tags` alone — the shape the retention makes easy to write — passes the entire board.
//
// The two accept rows are a module where the two readings disagree in both directions: tag 0 is the
// import and tag 1 the definition, with different parameter types, so a walk that skips imports refuses
// one row with `unknown tag 1` and the other with a payload mismatch naming a type the module never
// throws.
func TestTagIndexSpaceInterleavesImports(t *testing.T) {
	const mod = `(module
		(type $a (func (param i32)))
		(type $b (func (param f32)))
		(import "m" "t" (tag (type $b)))
		(tag (type $a))
		(func %s)
	)`
	for _, c := range []struct {
		name  string
		body  string
		msg   string
		valid bool
	}{
		{
			name:  "index 0 is the imported tag",
			body:  `(f32.const 1) (throw 0)`,
			valid: true,
		},
		{
			name:  "index 1 is the defined tag, one past the import",
			body:  `(i32.const 1) (throw 1)`,
			valid: true,
		},
		{
			// The tail of `ErrUnknownTag`'s message is `indexInScope`'s, and the count is the whole
			// space rather than either half: a validator counting only definitions would say `1 in
			// scope` here and would still refuse the module.
			name:  "one past both",
			body:  `(throw 2)`,
			msg:   "unknown tag 2 (2 in scope)",
			valid: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			wat := strings.Replace(mod, "%s", c.body, 1)
			_, err := validated(t, wat, ehOn)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s\nThe index space is imports-then-definitions, "+
					"and every tag in the suite is a defined one — so a walk that skips imports "+
					"passes the whole board and fails only here.", err, wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s", wat)
			case !c.valid && !errors.Is(err, ErrUnknownTag):
				t.Errorf("refused with the wrong sentinel: want %v, got %v", ErrUnknownTag, err)
			case !c.valid && !strings.Contains(err.Error(), c.msg):
				t.Errorf("refused, but not with %q: %v", c.msg, err)
			}
		})
	}
}

// TestTagTypesAreCheckedAtBothCallSites is `check_tagtype` (`valid.ml:191-195`), and the battery exists
// for the **second** site.
//
// The rule is one `require`: a tag is a thrown signature, so it has parameters and no results. It is
// reached from `check_tag` over the defined tags (`valid.ml:1049-1052`) *and* from
// `check_externtype`'s `ExternTagT` arm over the imported ones (`:222-223`), which `check_import`
// reaches one phase earlier. `tag.wast:18` and `tag.wast:22` are one vector per site, so the board does
// separate them — this battery's addition is that it says so **in the package**, with the two error
// prefixes asserted, because both vectors expect the same string and a board delta of two cannot
// distinguish two sites passing from one site firing twice.
//
// A rule written at the defined site alone passes `:18` and admits `:22`, which is exactly the state
// `modulePre`'s phase table described as "not this slice" for the import arm — a live admission
// standing in a table that named it.
func TestTagTypesAreCheckedAtBothCallSites(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
	}{
		{
			name:  "a defined tag with no results",
			wat:   `(module (type $ft (func (param i32))) (tag (type $ft)))`,
			valid: true,
		},
		{
			name:  "an imported tag with no results",
			wat:   `(module (type $ft (func (param i32))) (import "m" "t" (tag (type $ft))))`,
			valid: true,
		},
		{
			name:  "a defined tag that returns something — tag.wast:18",
			wat:   `(module (type $ft (func (result i32))) (tag (type $ft)))`,
			msg:   "tag 0: non-empty tag result type: type 0 returns [i32]",
			valid: false,
		},
		{
			// The second site. Deleting the `ExternTagT` arm from `modulePre` leaves every other row
			// in this file green.
			name:  "an imported tag that returns something — tag.wast:22",
			wat:   `(module (type $ft (func (result i32))) (import "m" "t" (tag (type $ft))))`,
			msg:   "import 0: non-empty tag result type: type 0 returns [i32]",
			valid: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, ehOn)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s", err, c.wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s\nA tag is a thrown signature and has no "+
					"results; the two sites are two phases apart and each needs its own arm.", c.wat)
			case !c.valid && !errors.Is(err, ErrNonEmptyTagResult):
				t.Errorf("refused with the wrong sentinel: want %v, got %v", ErrNonEmptyTagResult, err)
			case !c.valid && !strings.Contains(err.Error(), c.msg):
				t.Errorf("refused, but not with %q: %v\nThe prefix is the assertion: both vectors "+
					"expect the same string, so only the position in the message says which call "+
					"site fired.", c.msg, err)
			}
		})
	}
}
