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

// The three fixtures, one per outcome. `declining` uses `i8x16.relaxed_swizzle`, which #9's
// validator has no rule for yet — an instruction the *interpreter* implements, which is what makes
// the decline case meaningful: the module runs and returns the right answer while carrying a
// statement that one construct was never checked. A construct neither half supported would conflate
// decline with ErrUnsupported.
//
// **Re-pointed by the reference-type slice (#359), and this re-point changes the specimen's
// *kind*.** Every previous one — `memory.size` at slice 5, `ref.null`/`ref.is_null` here — was drawn
// from the population the next slice drains, so the note this comment used to carry was that a
// hand-named decline specimen is not a risk but a *schedule*. That is still true of that population
// and no longer true of this fixture: the relaxed-SIMD arm declines by **construction** rather than
// by omission, because relaxed SIMD's gate is its own event (ADR 0025, #227), so the arm is a
// deliberate refusal that no validator slice removes. This specimen retires when that gate flips,
// which is a governance event with a stamp attached rather than the next PR.
//
// So the constraint on a replacement is unchanged — implemented by the interpreter, not yet typed by
// the validator — and it is worth recording that it eliminated the obvious candidate: `ref.as_non_null`
// is what `validate_test.go`'s decline row re-pointed to in this same diff, and it is **not usable
// here**, because that test turns the GC gate on and this one exercises the default build, where the
// module is ErrGated before the validator is asked. A fixture for the *public* API is constrained by
// the default feature set as well as by the two halves.
//
// Deriving the specimen from the validator's own vocabulary instead of naming one is still #326, and
// still open; what this re-point does is take the pressure off it, since a structural decline does
// not dissolve underneath the fixture on a schedule.
const (
	validWAT = `(module
		(func (export "answer") (result i32) i32.const 42)
		(func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add)
		(func (export "divz") (param i32) (result i32) local.get 0 i32.const 0 i32.div_s)
		(memory (export "mem") 1))`

	invalidWAT = `(module (func (export "f") (result i32) i64.const 42))`

	// # There is no declining fixture any more, and the sixth re-point is the retirement
	//
	// `decliningWAT` stood here — `i8x16.relaxed_swizzle`, chosen because the interpreter executed it
	// and the validator had no rule for it. #427 gave the validator the rule, so the intersection
	// #326 describes ("implemented by the interpreter, untyped by the validator") is **empty** at this
	// boundary, and empty for a structural reason rather than a scheduling one: the public path is
	// default-features-only by design, and the validator types every instruction the decoder can name
	// under `DefaultFeatures`.
	//
	// #326 predicted this state precisely — "at the end of #9 it is empty, at which point these
	// fixtures do not need a better specimen, they need retiring" — and it stays open, because its
	// subject changes rather than dissolving: a derivation is now worth having to catch a decline
	// *re-appearing*, which is what a new prefix region or a gate flip outrunning its typing slice
	// would produce.
	//
	// The note this fixture carried is worth keeping as testimony, because it is the fifth instance of
	// the foreclosing word this PR is about. It said the specimen "is a *structural* decline and so
	// retires on a gate flip rather than on the next slice", which took the pressure off #326. The
	// gate it was waiting on had already flipped (`7315b57`, three days before), so the fixture was
	// living on borrowed time at the moment that sentence was written, and the pressure it claimed to
	// relieve was never relieved. `internal/testenv`'s foreclosing-word sweep does not catch this one:
	// it is an inline note on a fixture, not a bound account, a non-goals heading, or an
	// `ErrUnsupported` arm. Stated rather than fixed by widening the sweep, because widening it to
	// unscoped prose is the 276-occurrence transcription that control's header rules out — the gap is
	// named so it can be priced.
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

	// The third outcome's own subtest is gone with its fixture, and what replaces it is the assertion
	// that runs in the *other* direction: no module this boundary accepts reports a decline. It is
	// asserted over the whole corpus rather than over one specimen, in `publicpath_test.go` — which is
	// where the direction flip is accounted, and which is a stronger statement than any single fixture
	// made. What is no longer asserted anywhere, and is named here so the loss is legible rather than
	// discovered: that a decline, *if one occurred*, would be `ErrDeclined` and not `ErrInvalid`, would
	// name its construct, and would still run. Those are properties of code that now has no reachable
	// input at this boundary. #326 is the vehicle if a derivation ever gives them a subject again.
}

// TestStrictRefusesDeclinesAndNothingElse pins Config.Strict's scope: it closes the carve-out and
// changes nothing else.
//
// The second half is the one worth asserting. A flag that also refused valid modules, or that turned
// an invalid module's refusal into a decline, would still pass a test that only checked the declining
// case — so both neighbours are checked under the same flag.
func TestStrictRefusesDeclinesAndNothingElse(t *testing.T) {
	strict := Config{Strict: true}

	// **The flag's own arm is unexercisable, and that is the finding rather than a gap in this test.**
	// `Config.Strict`'s doc comment schedules its own death — "it stops meaning anything when the last
	// slice does" — and #427 typing `fd 0x100..0x12f` is the moment: nothing the decoder can name under
	// `DefaultFeatures` declines, so `Strict: true` and `Strict: false` classify every module this
	// boundary accepts identically. The flag is a no-op today.
	//
	// It is not removed, and its default is not flipped. Removal is an exported-API change and the flip
	// is a default-behaviour change; both are Scott's, flagged rather than taken, and the flip is worth
	// noting as *cheap* — it changes no observable behaviour, since the population it would newly refuse
	// is empty. What survives below is the half that still has subjects, and it was always the half
	// worth asserting: that the flag changes nothing for a valid module or an invalid one.
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
