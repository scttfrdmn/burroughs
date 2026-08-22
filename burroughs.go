// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/validate"
)

// The declared error set. Each is a *classification* a caller can branch on with errors.Is; the
// wrapped error underneath carries which rule, which instruction, or which reason.
//
// Five sentinels for five different questions about a module, because a single `error` at this
// boundary would make the outcomes decision 0029 separates indistinguishable to the one consumer
// that has to act on them. The fifth arrived by that argument being made the hard way: see ErrGated
// and grave #301.
var (
	// ErrMalformed is a module the decoder could not read: this is not a wasm binary, or not one
	// this version of the format describes. The wrapped error is the decoder's own, whose
	// messages are the spec's expected strings (decision 0003).
	ErrMalformed = errors.New("burroughs: malformed module")

	// ErrInvalid is a well-formed module that breaks a typing rule. The wrapped error names the
	// rule — the validator's sentinels are the spec's own texts, so a caller can match on
	// "type mismatch" or "unknown local" without this package restating them.
	ErrInvalid = errors.New("burroughs: invalid module")

	// ErrDeclined is the third outcome: the validator has **no rule yet** for a construct the
	// module uses, so the module is neither accepted nor refused — it is not fully asked.
	//
	// **Not a failure, and collapsing it into either neighbour is a mixture error.** Reported as
	// invalid, it refuses working modules for the length of #9's campaign; reported as valid, it
	// claims a check that did not run. It is returned from Instantiate only under Config.Strict;
	// otherwise the module runs and the decline is available from Instance.Decline. Decision 0029
	// records the ruling, including why running a partly-unvalidated module is defensible in a Go
	// host specifically and what would change that answer.
	ErrDeclined = errors.New("burroughs: validator declined a construct")

	// ErrUnsupported is the engine reaching a feature it does not implement in this phase — an
	// instruction with no arm, an import nothing supplied. Distinct from ErrDeclined, which is
	// the *validator's* gap: this one is the interpreter's, it fires at the point of use rather
	// than at load, and the two drain by different work.
	ErrUnsupported = errors.New("burroughs: feature not implemented in this phase")

	// ErrGated is a well-formed module using a proposal whose gate is off in this build. The
	// wrapped error names the proposal — `memory64: feature gate disabled`.
	//
	// **The fifth sentinel exists because the fourth verdict does** (grave #301). The board scores
	// a vector for a gated proposal `gated` and never `unsupported`, because the two drain by
	// different work — a stamped flip versus engine capability — and the identical argument holds
	// at this boundary, one channel over. Reporting a gated module as ErrMalformed is worse still
	// and is what this arm was built for: `binary.ErrFeatureDisabled`'s own doc comment says it is
	// deliberately not a malformed-string because *the module is well-formed and the spec would
	// accept it*, and the first version of this boundary wrapped it in ErrMalformed anyway,
	// re-manufacturing at the public surface the malformedness the decoder had refused to
	// manufacture. 429 module forms across the corpus were told they were broken when the truth was
	// that this build has GC and memory64 switched off.
	//
	// It has no phase-scheduled death, which is what separates it from ErrDeclined: gates drain one
	// stamped flip at a time and §9 keeps admitting new proposals, so this classification is
	// permanent furniture rather than a carve-out.
	ErrGated = errors.New("burroughs: proposal gate is off in this build")
)

// Trap is a wasm trap: the module executed correctly and the program went wrong.
//
// Converted from the engine's own trap rather than re-exported, for Value's reason. Reason is the
// spec's trap text verbatim — "integer divide by zero", "out of bounds memory access" — because
// that string is what the conformance suite matches, and a paraphrase here would make this
// package's testimony differ from the engine's about the same event.
type Trap struct {
	Reason string
}

// Error keeps Go's context-prefix idiom, and `interp.Trap.Error` deliberately does not (ADR 0045).
//
// The two renderings of one Reason differ on purpose. The conformance suite matches an expected trap
// text by **prefix**, so the engine's rendering has to put the spec's phrase at position 0 and says
// `integer divide by zero (trap)`; the reference's domain is the engine, because the harness builds
// its Engine over `internal/interp` and never sees this package. What this boundary owes is that the
// **Reason** is the engine's — the sentence above, and the reason a paraphrase would be a defect — and
// Reason is passed through unchanged. Text-identity with the engine's rendering was never the
// invariant, and buying it here would reposition a public error string for consistency rather than for
// a measurement.
func (t *Trap) Error() string { return "trap: " + t.Reason }

// Config is how a module is loaded. The zero value is the default policy.
type Config struct {
	// Strict makes a decline fatal: Instantiate returns ErrDeclined instead of running a module
	// the validator could not fully check.
	//
	// **A carve-out with a scheduled death.** It exists because #9's validator lands in slices,
	// and it stops meaning anything when the last slice does — at which point the *default*
	// flips to refusing declines and this field becomes a no-op, the same self-retiring shape as
	// ADR 0025's G-1 carve-out. Off by default today because a strict default would refuse
	// modules this engine executes correctly, for a reason that is about the validator's
	// schedule rather than about the module.
	Strict bool
}

// There is deliberately **no gate selection here**, and it is a scope statement rather than an
// omission. A public consumer gets `binary.DefaultFeatures` — the gates contract §9 has flipped
// on, each by its own stamped decision — because that is what "nothing defaults on without its
// suite green" means when the caller is a host rather than the harness. An all-gates-on lane is a
// measurement instrument (`internal/spec`'s own second lane) and exposing it here would publish a
// configuration whose defining property is that its suites are not green.

// Instance is an instantiated module.
type Instance struct {
	in      *interp.Instance
	decline error
}

// Instantiate decodes, validates, and instantiates a wasm module under the default policy.
func Instantiate(wasm []byte) (*Instance, error) { return Config{}.Instantiate(wasm) }

// Instantiate decodes, validates, and instantiates a wasm module under c.
//
// The three outcomes, in the order they are decided:
//
//   - The decoder refuses a construct from a gated proposal: ErrGated, naming the proposal. Ahead
//     of ErrMalformed because a gated module is well-formed — see ErrGated and grave #301.
//   - The decoder refuses anything else: ErrMalformed, wrapping the spec's own text for what was
//     wrong.
//   - The validator refuses: ErrInvalid, wrapping the rule.
//   - The validator declines — no rule yet for a construct (#9) — and the module runs anyway,
//     with the decline available from Instance.Decline. Under Config.Strict this returns
//     ErrDeclined instead.
//
// **This is the first path in the engine that validates before running.** The conformance harness
// calls the validator only for `assert_invalid` vectors; its run path instantiates a decoded
// module directly, which is the condition ADR 0025's carve-out names. So the ordering here is not
// a restatement of what the suite already does — it is new behaviour, and it is the behaviour a
// host wants, since an unvalidated module reaching the interpreter is how a type confusion becomes
// a wrong answer instead of a refusal.
func (c Config) Instantiate(wasm []byte) (*Instance, error) {
	m, err := (&binary.Decoder{Features: binary.DefaultFeatures()}).DecodeModule(wasm)
	if err != nil {
		// The gate arm runs first, and the order is the whole of grave #301: a construct from a
		// gated proposal is *well-formed*, so the specific test has to precede the general one or
		// the general one absorbs it and tells the caller their module is broken. Same shape as the
		// decline arm below — narrow sentinel first, catch-all second, and the catch-all is the one
		// that must not be able to claim the narrow case.
		if errors.Is(err, binary.ErrFeatureDisabled) {
			return nil, fmt.Errorf("%w: %w", ErrGated, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	var decline error
	if _, verr := validate.Module(m); verr != nil {
		// The order of these two arms is the whole decision. `errors.Is` first, because the
		// decline sentinel is the *narrow* case and treating it as invalid is the failure that
		// refuses working modules — so the specific test runs before the general one, and a new
		// validator sentinel added later lands in ErrInvalid, which is the safe direction.
		if !errors.Is(verr, validate.ErrUnsupported) {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, verr)
		}
		decline = fmt.Errorf("%w: %w", ErrDeclined, verr)
		if c.Strict {
			return nil, decline
		}
	}

	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, &Trap{Reason: trap.Reason}
	}
	return &Instance{in: in, decline: decline}, nil
}

// Decline reports the construct the validator had no rule for, or nil when the module was fully
// validated.
//
// **The message is the campaign's public work plan**, which is Scott's reason for it existing as
// a readable string rather than as a bare boolean: a caller who reads "instruction not in this
// slice: ref.func (0xd2)" is reading #9's own queue, and that is worth more than a load path that
// looks complete. Decision 0029 records it.
func (in *Instance) Decline() error { return in.decline }

// Deferred reports a shortfall instantiation met that could not travel the trap channel — today,
// an active data segment whose target memory is imported and unsupplied.
//
// Exposed for the engine's own reason for exposing it: a nil trap says "this module did not die
// coming to life", which is not the same claim as "this module came to life completely", and a
// caller deciding what a successful load means needs the difference.
func (in *Instance) Deferred() error {
	if err := in.in.Deferred(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return nil
}

// Exports names the module's exported functions, in declaration order.
//
// Functions only, and that is a scope statement rather than an oversight: this surface calls
// exports, so the list is the set of names `Call` will accept. It is what makes `run` able to
// tell a user which names exist instead of demanding they already know.
//
// **The reason used to be wider than that and is no longer true.** It read "a memory or a global
// export has no operation here yet to be worth naming", and `Global` (#323) is an operation on a
// global export — so the sentence would now assert the absence of the method below it, which is
// *the defect stated as the rule* in the direction that reads as deliberate. What survives the
// narrowing is a real gap, stated rather than papered over: **a caller can read an exported global
// only by knowing its name**, because nothing here enumerates the global exports the way this
// enumerates the functions. Left that way on the same test the old sentence was passing under —
// `run` has no operation that takes a global name, so a discovery list would have no consumer —
// and it becomes wrong to leave the moment one does.
func (in *Instance) Exports() []string {
	var out []string
	for _, e := range in.in.Module().Exports {
		if e.Kind == binary.ExternFunc {
			out = append(out, e.Name)
		}
	}
	return out
}

// Call invokes an exported function.
//
// Arguments are checked by arity and by type against the declared signature before any frame is
// built, and results come back in declaration order. A trap during execution returns a *Trap; a
// feature the engine does not implement returns ErrUnsupported, at the point it is reached rather
// than at load.
func (in *Instance) Call(name string, args ...Value) ([]Value, error) {
	iargs := make([]interp.Value, len(args))
	for i, a := range args {
		iv, err := valueToInternal(a)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		iargs[i] = iv
	}

	res, err := in.in.Invoke(name, iargs...)
	if err != nil {
		return nil, publicError(err)
	}

	out := make([]Value, len(res))
	for i, r := range res {
		v, cerr := valueFromInternal(r)
		if cerr != nil {
			return nil, fmt.Errorf("result %d: %w", i, cerr)
		}
		out[i] = v
	}
	return out, nil
}

// Global reads an exported global's current value.
//
// `Call`'s counterpart for the script grammar's other action (#323): `invoke` runs a function,
// `get` reads a global. A name that no *global* export carries is an error even when a function
// of that name exists, which is the kind check in `exportedIndex` surfacing here — reading
// `"add"` as a global is a caller's mistake and not an empty answer.
//
// No mutability question in either direction: this reads, and there is no public write. An
// immutable global is as readable as a mutable one.
func (in *Instance) Global(name string) (Value, error) {
	iv, err := in.in.Global(name)
	if err != nil {
		return Value{}, publicError(err)
	}
	v, cerr := valueFromInternal(iv)
	if cerr != nil {
		return Value{}, cerr
	}
	return v, nil
}

// publicError re-channels an engine error onto this package's sentinels.
//
// **A translation, not a rewrap-everything.** A trap becomes a *Trap so a caller matching on the
// public type sees it; the engine's unsupported-feature sentinels become ErrUnsupported; anything
// else — a bad argument type, an unknown export — travels unchanged, because those messages are
// already about the caller's own request and adding a classification would say nothing the text
// does not. The two engine sentinels are matched with errors.Is rather than by text, for
// `isTrap`'s reason in the harness: the taxonomy is the engine's.
func publicError(err error) error {
	var t *interp.Trap
	if errors.As(err, &t) {
		return &Trap{Reason: t.Reason}
	}
	if errors.Is(err, interp.ErrUnsupported) || errors.Is(err, interp.ErrUnsupportedOp) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}
