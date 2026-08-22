// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"errors"
	"fmt"
	"testing"
)

// errFact3 is a plain, *ungated* instantiation failure — the thing fact 3 exists to score. It is a
// local sentinel rather than a real engine error for `errStubGate`'s reason: nothing here should
// depend on how the interpreter words a link failure, so the control keeps working when it is
// reworded.
var errFact3 = errors.New("stub: refuses to instantiate")

// moduleFact3Witnesses is the fact-3 witness table, and it is a package-level variable rather than
// a local so that `TestModuleDefinitionsAskTheValidator`'s Kind-census subtest can check its
// coverage against the corpus-measured domain. A re-typed copy of the same Kinds over there would
// agree with this table by construction and check nothing — the argument that made fact 2's table
// a named variable, one fact later.
//
// **Every row is a Kind with its own instantiation call, which is this table's admission
// criterion.** `KindModuleText` and `KindModuleQuote` share an arm, so they share a call and one
// row would seem to cover both — except that the fall-through the branch guards is *inside* the
// per-Kind guard, exactly as fact 2's is, so narrowing that guard is a one-token edit text survives
// and quote does not. `KindModuleInstance` is a row and not an excuse: it reaches instantiation
// through its own arm and its own hand-written key, which is the fourth member of the family
// `scoreModuleInstantiation`'s comment names.
var moduleFact3Witnesses = []struct {
	name string
	src  string
	want Kind
	// bucket is hand-typed, not derived as `want.String() + " must instantiate"`. Deriving it
	// would make the row an identity against the very closure it checks — a pin wearing a
	// cross-check's clothes (grave #362) — so it would agree with any rename, including a wrong
	// one. Fact 2's table gives the same reason for the same reason.
	bucket string
	// wantPass is what the *rest* of the script scores, and it is not always zero: the instance
	// row needs a definition in front of it, and that definition legitimately passes on facts 1
	// and 2. Stated per row rather than asserted as zero, because a zero here would have to be
	// bought by dropping the one row whose arm is reached any other way.
	wantPass int
}{
	{"text", `(module (func))`, KindModuleText, "module text must instantiate", 0},
	{"quote", `(module quote "(module (func))")`, KindModuleQuote, "module quote must instantiate", 0},
	{"binary", `(module binary "\00asm\01\00\00\00")`, KindModuleBinary, "module binary must instantiate", 0},
	// The definition supplies the instance form's subject and scores its own facts 1 and 2; the
	// instance form is the row. Its key is the pre-existing literal from that arm, which is why
	// this row also pins the near-collision `scoreModuleInstantiation`'s comment records: three
	// derived keys and one hand-written one, all reading as one family.
	{
		"instance",
		`(module definition $M (func))
		 (module instance $I $M)`,
		KindModuleInstance, "(module instance) must instantiate", 1,
	},
}

// TestModuleDefinitionsAskWhetherTheModuleInstantiates is #367's control, and it is the only thing
// standing between the arm and the arm not having landed.
//
// **The population it asserts on is measured at zero**, in both lanes, before the branch existed:
// 2233 module definitions reach fact 3's position in the default lane with 0 non-gated instantiation
// errors among them, 2241 with 0 in the all-on lane. So no board figure moves when the arms start
// asking fact 3, and without these rows the arms' green is indistinguishable from the arms having no
// such branch at all. That is #353's binary-arm situation exactly — a clean population does not make
// the question idle, because an instantiation failure at `StratumExec` produced *no* error for any
// bucket to catch and scored a **pass**.
//
// Three things are falsified here, and they fail for unrelated reasons (grave #34's rule applied
// within one control's rows rather than across files, because these share a mutation):
//
//  1. **The arms ask at all** — an engine that refuses to instantiate anything must turn every
//     module definition red. Deleting either `scoreModuleInstantiation` call leaves the other arm's
//     rows green, which is what makes four rows four rows.
//  2. **A gate decline still keeps its pass**, asserted as a discriminating pair against the same
//     source rather than on its own. This is the half with 446 rows behind it and the half #367 was
//     blocked on: before 0037 gave the registry a gated state, the 13 `imports`/`linking` rows this
//     exemption now covers were indistinguishable from a real `unknown import`, and a fact-3 branch
//     written then would have pushed them into the fail column.
//  3. **The failure names its own cause and its own stratum**, so a row that starts failing for a
//     different reason is reported instead of absorbed.
func TestModuleDefinitionsAskWhetherTheModuleInstantiates(t *testing.T) {
	for _, tc := range moduleFact3Witnesses {
		t.Run("an engine that instantiates nothing turns a "+tc.name+" definition red", func(t *testing.T) {
			s, err := Parse("fact3_test.wast", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// The row's subject is the *last* command; the instance row has a definition in front
			// of it. Checked rather than assumed, because a classifier change that reclassified the
			// subject would otherwise leave this row asserting something about a command it is not
			// about.
			if got := s.Commands[len(s.Commands)-1].Kind; got != tc.want {
				t.Fatalf("classified %v, want %v — this control needs the arm it is about", got, tc.want)
			}

			r := s.RunGated(fact3Engine(func(Command) error { return errFact3 }))

			if r.Pass != tc.wantPass || r.Fail != 1 {
				t.Fatalf("got %d pass, %d fail; want %d/1 — a module definition scored green under "+
					"an engine that instantiates nothing is the hole #367 closed, and it is the "+
					"third of the three facts a definition asserts (%v)",
					r.Pass, r.Fail, tc.wantPass, r.BucketsBySize())
			}
			b := r.Buckets[tc.bucket]
			if len(b) != 1 {
				t.Fatalf("no failure under %q; got keys %v", tc.bucket, r.BucketsBySize())
			}
			if b[0].Stratum != StratumExec {
				t.Errorf("Stratum = %v, want %v — an instantiation failure the caller did not "+
					"charge to a layer is normalized to exec by runOpts.instantiate, and a row "+
					"landing anywhere else would be blaming a front end whose ceiling is 0",
					b[0].Stratum, StratumExec)
			}
			if b[0].Got == "" || !containsBytes([]byte(b[0].Got), errFact3.Error()) {
				t.Errorf("Got = %q, want it to carry %q — a bucket whose Got is not the cause "+
					"reports that something failed without saying what", b[0].Got, errFact3.Error())
			}
		})
	}

	// **The exemption, as a discriminating pair.** One source, two instantiation errors that differ
	// only in whether `IsGated` answers yes, and the two verdicts must differ. Asserting only the
	// gated half would pass on an arm that never scores fact 3 at all; asserting only the ungated
	// half is the rows above. The pair is what says the *distinction* is doing the work — and it is
	// the same source, so nothing about the module can be the explanation.
	//
	// The gated shape spelled out is 0037's, not a proposal's own construct: `import "M" "x" names
	// module "M", whose registration was declined`. Both shapes reach here identically, and this is
	// the one whose 13 rows made #367 wait for #366.
	t.Run("a gated instantiation failure keeps the definition's pass", func(t *testing.T) {
		const src = `(module (import "M" "x" (func)))`
		for _, tc := range []struct {
			name     string
			err      error
			wantPass int
			wantFail int
		}{{
			"gated", fmt.Errorf(
				`import "M" "x" names module "M", whose registration was declined: %w`, errStubGate),
			1, 0,
		}, {
			"ungated", errFact3, 0, 1,
		}} {
			t.Run(tc.name, func(t *testing.T) {
				s, err := Parse("fact3_test.wast", []byte(src))
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				r := s.RunGated(fact3Engine(func(Command) error { return tc.err }))
				if r.Pass != tc.wantPass || r.Fail != tc.wantFail {
					t.Fatalf("got %d pass, %d fail; want %d/%d (%v)\n"+
						"\ta gate decline is not a verdict on the module (#124), and scoring one "+
						"here pushes the third verdict into the fail column one command "+
						"downstream — 446 rows in the default lane arrive at fact 3 this way",
						r.Pass, r.Fail, tc.wantPass, tc.wantFail, r.BucketsBySize())
				}
			})
		}
	})

	// **The other exemption: a run with no interpreter is not asked.** `runOpts.instantiate`
	// fabricates `this run supplied no InstantiateFunc` for exactly the callers its own comment
	// blesses — the ones that score only module and malformed forms — and charging a module for the
	// harness's missing component would be an error from the wrong layer wearing a verdict.
	//
	// This subtest is a **named** witness for a branch that already had an accidental one:
	// `TestParseAnnotationTokenSoup` found the defect by going red at `0 pass, 0 unsupported`, and it
	// still dies if the guard is removed. Two reasons to write this anyway. That test's subject is an
	// annotation lexer, so the day its engine gains an interpreter for an unrelated reason the
	// coverage leaves with it; and it asserts a *pass count*, where the property here is that no
	// bucket is opened — a fail keyed somewhere else would satisfy neither, but only this one says
	// which.
	t.Run("a run with no interpreter is not asked fact 3", func(t *testing.T) {
		s, err := Parse("fact3_test.wast", []byte(`(module (func))`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := fact3Engine(func(Command) error { return errFact3 })
		e.Instantiate, e.InstantiateLinked = nil, nil
		r := s.RunGated(e)
		if r.Pass != 1 || r.Fail != 0 || r.Unsupported != 0 {
			t.Fatalf("got %d pass, %d fail, %d unsupported; want 1/0/0 — a reader-and-validator "+
				"run scores facts 1 and 2 and must keep its pass (%v)",
				r.Pass, r.Fail, r.Unsupported, r.BucketsBySize())
		}
		if len(r.Buckets) != 0 {
			t.Errorf("buckets %v, want none — the run's own missing component was charged to the "+
				"module, which is the layer confusion the guard exists for", r.BucketsBySize())
		}
	})
}

// fact3Engine is a front end that accepts everything and an instantiation path that answers
// `fail`. Everything but instantiation is stubbed permissive on `TestRegistryCarriesItsGatedNames`'s
// reason: this control is about fact 3, and a reader or validator with an opinion would make the
// rows' verdicts depend on a component they are not about.
//
// **`InstantiateLinked` is the field set, and `Instantiate` is set beside it deliberately.**
// `instantiateRaw` prefers the linked entry point, so setting only the plain one would compile, read
// as though it had stubbed instantiation, and be called by nothing — the hazard `allOnLane`'s
// comment records having nearly shipped twice. Setting both means the stub is live whichever one the
// run loop reaches, so this helper cannot be silently bypassed by a change to that preference.
//
// **The harness's own `spectest` fixture is exempt, and it has to be**: `spectestRegistry` builds it
// through this same entry point and *panics* when it fails, on the ground that a failure there is a
// defect in `spectestFields` rather than a board number. A blanket refusal therefore never reaches
// the arm under test — it takes the script down first, which is how the first draft of this helper
// announced itself.
//
// The discriminator is `Line == 0`: the fixture is composed in Go from `spectestSource`, so it comes
// from no file and carries no line, where every command a script parses has a line of its own.
// `registry_gated_test.go` marks its subjects in the source instead, which does not transfer here —
// the instance row's instantiation is handed the *definition's* source with the instance's line
// restamped, so a marker would have to be written on a command that is not the row's subject.
// **And the premise fails loudly rather than silently if it ever stops holding**: a fixture that
// gained a line would be refused, and a refused fixture panics.
func fact3Engine(fail func(Command) error) Engine {
	instantiate := func(c Command) (Instance, Stratum, error) {
		if c.Line == 0 {
			return "spectest", StratumUnset, nil
		}
		return nil, StratumUnset, fail(c)
	}
	e := Engine{
		Validate:    stubValidate,
		IsDeclined:  stubDeclined,
		IsGated:     func(err error) bool { return errors.Is(err, errStubGate) },
		IsTrap:      func(error) bool { return false },
		Instantiate: instantiate,
		InstantiateLinked: func(c Command, _ Registry) (Instance, Stratum, error) {
			return instantiate(c)
		},
		Has: []Capability{CapWatReader, CapInterpreter},
	}
	e.ReadText = func([]byte) error { return nil }
	e.Assemble = func([]byte) ([]byte, error) { return []byte{0}, nil }
	e.Decode = func([]byte) error { return nil }
	return e
}
