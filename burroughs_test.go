// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/text"
)

// compile assembles a module from its text form, so the fixtures below read as the spec writes them
// rather than as byte arrays.
//
// The text format is this repo's own encoder (`internal/text`), which the conformance suite already
// exercises — a fixture that failed to assemble would be a failure of the fixture, and it is reported
// as one (t.Fatal, naming the source) rather than surfacing as a mysterious ErrMalformed from the
// path under test.
func compile(t *testing.T, src string) []byte {
	t.Helper()
	wasm, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("assembling the fixture failed, so this test's subject was never reached: %v\n%s", err, src)
	}
	return wasm
}

// The three fixtures, one per outcome. `declining` uses `ref.null`/`ref.is_null`, which #9's
// validator has no rule for yet — instructions the *interpreter* implements, which is what makes the
// decline case meaningful: the module runs and returns the right answer while carrying a statement
// that one construct was never checked. A construct neither half supported would conflate decline
// with ErrUnsupported.
//
// **Re-pointed by slice 5, and it is the fifth re-point in that one diff.** The specimen was
// `memory.size`, which slice 5 types, so this fixture reported "either the vocabulary grew (retire
// this fixture) or the decline was lost" — correctly, and the first of those. The pattern is now
// established well enough to name: a hand-named decline specimen is drawn from a population *every
// slice drains*, so it is not a risk that a specimen dissolves but a schedule. What the fixture
// asserts is that the three outcomes stay distinguishable, which is a property of the API and not of
// any instruction; deriving the specimen from the validator's own vocabulary instead of naming one is
// #326. Until then the constraint on a replacement is the one this comment opens with — implemented
// by the interpreter, not yet typed by the validator — and that pair is what makes candidates scarce.
const (
	validWAT = `(module
		(func (export "answer") (result i32) i32.const 42)
		(func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add)
		(func (export "divz") (param i32) (result i32) local.get 0 i32.const 0 i32.div_s)
		(memory (export "mem") 1))`

	invalidWAT = `(module (func (export "f") (result i32) i64.const 42))`

	decliningWAT = `(module (func (export "isnull") (result i32) (ref.is_null (ref.null func))))`
)

// gatedWATs are well-formed modules from proposals whose gates are off in this build — grave #301's
// fixtures, one per gate so that a single flip cannot empty the set.
//
// The spelling matters: each is a module the **spec accepts**, which is the entire reason reporting
// it as malformed is a lie rather than an approximation. They are three because a fixture population
// of one retires the day its proposal flips, and the corpus control in publicpath_test.go covers the
// space (429 module forms, nine proposals) while these cover the classification cheaply.
var gatedWATs = map[string]string{
	"memory64":  `(module (memory i64 1))`,
	"gc":        `(module (type $s (struct (field i32))))`,
	"tail call": `(module (func $f) (func (export "g") return_call $f))`,
}

// TestTheThreeOutcomesAreDistinguishable is decision 0029's central claim made executable: valid,
// invalid, and declined are three states a caller can tell apart, not two.
//
// **The decline assertions are the load-bearing ones.** That a valid module runs and an invalid one
// is refused would be true of the two-way design as well; what distinguishes this one is that the
// declining module *also runs*, returns the correct value, and says which construct went unchecked.
// A regression collapsing decline into either neighbour fails here in the direction it collapsed.
func TestTheThreeOutcomesAreDistinguishable(t *testing.T) {
	t.Run("valid runs with no decline", func(t *testing.T) {
		in, err := Instantiate(compile(t, validWAT))
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if d := in.Decline(); d != nil {
			t.Errorf("a fully validated module reported a decline: %v", d)
		}
		res, err := in.Call("answer")
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if len(res) != 1 || res[0] != I32(42) {
			t.Errorf("answer() = %v, want [i32:42]", res)
		}
	})

	t.Run("invalid is refused, naming the rule", func(t *testing.T) {
		_, err := Instantiate(compile(t, invalidWAT))
		switch {
		case err == nil:
			t.Fatal("an invalid module instantiated")
		case !errors.Is(err, ErrInvalid):
			t.Errorf("error is not ErrInvalid: %v", err)
		case errors.Is(err, ErrDeclined):
			t.Errorf("an invalid module was classified as a decline: %v", err)
		case !strings.Contains(err.Error(), "type mismatch"):
			t.Errorf("the refusal does not name the rule it broke: %v", err)
		}
	})

	t.Run("declined runs, and says what went unchecked", func(t *testing.T) {
		in, err := Instantiate(compile(t, decliningWAT))
		if err != nil {
			t.Fatalf("a declining module was refused, which is the mixture error this outcome "+
				"exists to prevent: %v", err)
		}
		d := in.Decline()
		if d == nil {
			t.Fatal("no decline reported for a module using an instruction the validator has no " +
				"rule for; either the vocabulary grew (retire this fixture) or the decline was lost")
		}
		if !errors.Is(d, ErrDeclined) {
			t.Errorf("the decline is not ErrDeclined: %v", d)
		}
		if errors.Is(d, ErrInvalid) {
			t.Errorf("the decline also matches ErrInvalid, so a caller branching on invalid "+
				"refuses this module: %v", d)
		}
		// The message is the campaign's public work plan (0029), so it has to name the construct.
		// Asserted on the mnemonic rather than the whole string, which is the validator's to word.
		//
		// `ref_null` and not `ref_is_null`: the validator reports the *first* instruction it cannot
		// type, and in this module that is the operand rather than the operator. Worth stating,
		// because a fixture asserting the outer mnemonic would be asserting an evaluation order
		// nothing promises.
		if !strings.Contains(d.Error(), "ref_null") {
			t.Errorf("the decline does not name the construct: %v", d)
		}

		res, err := in.Call("isnull")
		if err != nil {
			t.Fatalf("a declined module did not run: %v", err)
		}
		if len(res) != 1 || res[0] != I32(1) {
			t.Errorf("isnull() = %v, want [i32:1]", res)
		}
	})
}

// TestStrictRefusesDeclinesAndNothingElse pins Config.Strict's scope: it closes the carve-out and
// changes nothing else.
//
// The second half is the one worth asserting. A flag that also refused valid modules, or that turned
// an invalid module's refusal into a decline, would still pass a test that only checked the declining
// case — so both neighbours are checked under the same flag.
func TestStrictRefusesDeclinesAndNothingElse(t *testing.T) {
	strict := Config{Strict: true}

	_, err := strict.Instantiate(compile(t, decliningWAT))
	if err == nil {
		t.Fatal("--strict ran a module the validator could not fully check")
	}
	if !errors.Is(err, ErrDeclined) {
		t.Errorf("--strict refused with something other than ErrDeclined: %v", err)
	}

	in, err := strict.Instantiate(compile(t, validWAT))
	if err != nil {
		t.Fatalf("--strict refused a fully validated module: %v", err)
	}
	if d := in.Decline(); d != nil {
		t.Errorf("--strict reported a decline on a fully validated module: %v", d)
	}

	if _, err := strict.Instantiate(compile(t, invalidWAT)); !errors.Is(err, ErrInvalid) {
		t.Errorf("under --strict an invalid module is no longer ErrInvalid: %v", err)
	}
}

// TestAGatedProposalIsNotMalformed is grave #301: a well-formed module from a switched-off proposal
// is ErrGated, and specifically is not ErrMalformed.
//
// **The negative is the assertion.** That a gated module is refused was already true and is required
// — accept-and-ignore silently breaks semantics — so a test that only checked "refused" passed
// before the fix and after it. What was wrong was the *classification*: `internal/binary` takes
// deliberate care not to report a gated construct with a malformed-string (`ErrFeatureDisabled`'s own
// doc comment says so), and this boundary wrapped it in ErrMalformed anyway, re-manufacturing one
// layer up the malformedness the decoder had refused to manufacture. The lesson is the shape, not the
// file: a property established at one layer is not inherited by a new consumer of it.
func TestAGatedProposalIsNotMalformed(t *testing.T) {
	for name, src := range gatedWATs {
		wasm, err := text.EncodeModule([]byte(src))
		if err != nil {
			t.Fatalf("%s: assembling the fixture failed, so this test's subject was never reached: "+
				"%v\n%s", name, err, src)
		}
		_, ierr := Instantiate(wasm)
		switch {
		case ierr == nil:
			t.Errorf("%s: a gated proposal instantiated, so the gate is on and this fixture has "+
				"retired — replace it with a still-gated proposal or drop it if none remain", name)
		case !errors.Is(ierr, ErrGated):
			t.Errorf("%s: not classified ErrGated: %v", name, ierr)
		case errors.Is(ierr, ErrMalformed):
			t.Errorf("%s: a module the spec accepts was reported malformed, which lies about the "+
				"module to conceal this build's own configuration: %v", name, ierr)
		case errors.Is(ierr, ErrInvalid), errors.Is(ierr, ErrDeclined):
			t.Errorf("%s: a gated module was classified as the module's own fault: %v", name, ierr)
		case !strings.Contains(ierr.Error(), "feature gate disabled"):
			t.Errorf("%s: the refusal does not name the gate: %v", name, ierr)
		}
	}
}

// TestMalformedIsSeparateFromInvalid keeps the decoder's verdict and the validator's apart, which is
// the same distinction the suite draws between `assert_malformed` and `assert_invalid`.
func TestMalformedIsSeparateFromInvalid(t *testing.T) {
	for name, wasm := range map[string][]byte{
		"empty":           {},
		"magic only":      {0x00, 0x61, 0x73, 0x6d},
		"wrong magic":     {'n', 'o', 'p', 'e', 1, 0, 0, 0},
		"truncated body":  append([]byte{0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0}, 0x01, 0x04, 0x01),
		"garbage section": append([]byte{0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0}, 0x7f, 0x01, 0x00),
	} {
		_, err := Instantiate(wasm)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: error is not ErrMalformed: %v", name, err)
		}
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrDeclined) {
			t.Errorf("%s: a malformed module was also classified as invalid or declined: %v", name, err)
		}
		// The other direction of #301: the gate arm now runs first, so it must not have swallowed
		// genuine malformedness on its way past.
		if errors.Is(err, ErrGated) {
			t.Errorf("%s: an unreadable image was reported as a gated proposal: %v", name, err)
		}
	}
}

// TestATrapCrossesAsAPublicTrap checks the one error class that is not a sentinel: a trap is a type,
// because a caller usually wants its reason.
//
// The reason string is asserted to be the spec's own text rather than a paraphrase — the conformance
// suite matches on that string, so a boundary that reworded it would make this package's testimony
// differ from the engine's about the same event.
func TestATrapCrossesAsAPublicTrap(t *testing.T) {
	in, err := Instantiate(compile(t, validWAT))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	_, err = in.Call("divz", I32(1))
	var trap *Trap
	if !errors.As(err, &trap) {
		t.Fatalf("a trap did not cross as *Trap: %v", err)
	}
	if trap.Reason != "integer divide by zero" {
		t.Errorf("trap reason %q, want the spec's own text", trap.Reason)
	}
	if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalid) {
		t.Errorf("a trap also matches an engine-shortfall sentinel: %v", err)
	}
}

// TestExportsNamesFunctionsOnly pins the scope statement on Instance.Exports. The fixture exports a
// memory as well as three functions, so a change that started listing every export shows up here as a
// name a caller cannot pass to Call.
func TestExportsNamesFunctionsOnly(t *testing.T) {
	in, err := Instantiate(compile(t, validWAT))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	got := in.Exports()
	want := []string{"answer", "add", "divz"}
	if len(got) != len(want) {
		t.Fatalf("Exports() = %v, want %v (declaration order, functions only)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Exports()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, name := range got {
		if _, err := in.Call(name, I32(0), I32(0)); errors.Is(err, ErrUnsupported) {
			t.Errorf("Exports() named %q, which Call cannot reach: %v", name, err)
		}
	}
}

// TestCallRejectsAWrongCallWithoutClassifyingIt is the negative half of publicError's "travels
// unchanged" clause: a caller's own mistake is an error, and deliberately not one of the five
// sentinels.
//
// Both are assertions. That the call fails is the obvious one; that it does *not* match ErrInvalid or
// ErrUnsupported is what keeps "the module is wrong" and "the engine is incomplete" from absorbing
// "you called it wrong", which is the mixture the CLI's exit codes then depend on.
func TestCallRejectsAWrongCallWithoutClassifyingIt(t *testing.T) {
	in, err := Instantiate(compile(t, validWAT))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	for name, call := range map[string]func() ([]Value, error){
		"unknown export": func() ([]Value, error) { return in.Call("nosuch") },
		"too few args":   func() ([]Value, error) { return in.Call("add", I32(1)) },
		"too many args":  func() ([]Value, error) { return in.Call("add", I32(1), I32(2), I32(3)) },
		"wrong type":     func() ([]Value, error) { return in.Call("add", I32(1), I64(2)) },
		"unset value":    func() ([]Value, error) { return in.Call("add", I32(1), Value{}) },
	} {
		_, err := call()
		if err == nil {
			t.Errorf("%s: Call succeeded", name)
			continue
		}
		for sentinel, s := range map[string]error{
			"ErrInvalid":     ErrInvalid,
			"ErrMalformed":   ErrMalformed,
			"ErrDeclined":    ErrDeclined,
			"ErrUnsupported": ErrUnsupported,
			"ErrGated":       ErrGated,
		} {
			if errors.Is(err, s) {
				t.Errorf("%s: a caller's own mistake matched %s: %v", name, sentinel, err)
			}
		}
	}
}
