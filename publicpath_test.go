// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/spec"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// Conformance-shaped coverage **through the public path** (decision 0029, #299), which is the whole
// reason this package exists.
//
// # The claim, and why it is shaped like this
//
// The obvious test — run the corpus through the public API and assert every result matches its
// vector — cannot be written, and the reason is worth stating because it decides the design. The
// engine has 1328 known fails on the board; an assertion of zero mismatches here would fail on every
// one of them, so the only way to make it pass would be a ceiling on mismatches, and that is a
// **second fail ledger** with a different sampling filter from the board's. Two ledgers counting the
// same population drift and then argue, which is what *one concept, one trigger* forbids.
//
// So the assertion is a **differential** instead: every vector is driven down two paths in lockstep —
// the published API, and `interp` directly, as the harness drives it — and the paths must agree. What
// that buys is the licence to leave the vector comparison as an unasserted census: given agreement,
// a mismatch here *is* a board fail, already counted, already owned. The claim the two instruments
// make together is the one Scott asked for — the engine is conformant through the public path
// wherever it is conformant at all — and neither can make it alone.
//
// The two arms are deliberately independent implementations. `specToPublic`/`publicToSpec` cross the
// boundary under test; `specToRaw`/`rawToSpec` do not touch it, and re-derive the same mapping in
// fifteen lines against `interp.Value`'s exported fields. A shared helper would make both arms wrong
// together, which is the one way a differential can report agreement it has not earned.
//
// # What is asserted at zero, and what is only counted
//
// Asserted: path agreement; that no gate refusal is classified as anything but ErrGated (grave #301);
// that the validator now on the run path refuses **no** module the corpus offers as valid; that the
// buckets partition. Counted and logged: agreement with the vectors, and the decline census, which is
// #9's frontier stated in modules a user would actually hand this engine.
//
// Every command this driver cannot ask is counted into a named bucket. A skip is not a verdict, and
// an uncounted skip is worse, because it reads as coverage.

// specToPublic converts a vector's literal into a public Value, reporting false for shapes this API
// cannot carry as an argument.
//
// The refusals are each a real limit rather than an omission: a NaN *class* is a predicate over
// results and not a value, an `either` disjunction likewise, and `AnyNull` is a pattern. v128 is
// refused because the literal arrives as a lane shape whose packing is the harness's own machinery
// (`packV128Lanes`), and re-deriving it here would be a second implementation of a rule that has
// nothing to do with this boundary — unlike the deliberate second implementation below, which is the
// instrument. Every refusal is counted at the call site.
func specToPublic(v spec.Val) (Value, bool) {
	if v.Alts != nil || v.NaN != spec.NaNNone || v.AnyNull {
		return Value{}, false
	}
	switch v.Kind {
	case spec.KindV128:
		// Named rather than swept into a default, because it is the one refusal that is about *this
		// test* and not about the value: the literal arrives as a lane shape, and packing it here
		// would be a second implementation of `packV128Lanes`. Its cost is the `unpassable` bucket,
		// which is counted and printed rather than absorbed.
		return Value{}, false
	case spec.KindI32:
		return I32(int32(uint32(v.Bits))), true
	case spec.KindI64:
		return I64(int64(v.Bits)), true
	case spec.KindF32, spec.KindF64:
		// **Built from bits, not through F32/F64.** A vector's float literal is a bit pattern —
		// `nan:0x200000` and `nan` are different arguments to this corpus — and the public
		// constructors take a `float32`/`float64`, so routing a signalling payload through them
		// would put a float conversion between the corpus and the engine on the very axis the
		// last grave here was about. Whether that public path is payload-exact is a real question
		// and a separate one; TestPublicFloatConstructorsAreBitExact asks it, so this test does not
		// have to answer it in passing.
		kind := KindF32
		bits := v.Bits & 0xffffffff
		if v.Kind == spec.KindF64 {
			kind, bits = KindF64, v.Bits
		}
		return Value{typ: Type{kind: kind}, bits: bits}, true
	case spec.KindFuncRef, spec.KindExternRef:
		kind := KindFuncRef
		if v.Kind == spec.KindExternRef {
			kind = KindExternRef
		}
		switch v.Class {
		case spec.RefLiteralNull:
			typ, ok := AbstractRefType(kind, true)
			if !ok {
				return Value{}, false
			}
			return NullRef(typ)
		case spec.RefExternIdentity:
			return ExternRef(v.Extern), true
		default:
			// RefNone, RefTypePattern, RefConcrete: not spellable as an *argument*. A pattern is a
			// predicate over results and a concrete reference has no literal form the corpus can
			// hand in, so these fall to the refusal below and are counted there.
		}
	}
	return Value{}, false
}

// publicToSpec maps a public result back into the harness's value type, so that `spec.Val.Matches`
// can judge it and so that the two arms of the differential are comparable.
//
// A result-direction converter only. The asymmetry with specToPublic is the harness's own: an
// expectation is a predicate and a result is a value.
func publicToSpec(v Value) (spec.Val, bool) {
	switch k := v.Type().Kind(); k {
	case KindI32:
		return spec.Val{Kind: spec.KindI32, Bits: uint64(uint32(v.Int32()))}, true
	case KindI64:
		return spec.Val{Kind: spec.KindI64, Bits: uint64(v.Int64())}, true
	case KindF32:
		// v.bits, for specToPublic's reason: a result's NaN payload is part of the answer, and the
		// public accessor hands back a float32.
		return spec.Val{Kind: spec.KindF32, Bits: v.bits & 0xffffffff}, true
	case KindF64:
		return spec.Val{Kind: spec.KindF64, Bits: v.bits}, true
	case KindV128:
		lo, hi := v.V128()
		return spec.Val{Kind: spec.KindV128, Bits: lo, Hi: hi}, true
	case KindFuncRef, KindExternRef:
		kind := spec.KindFuncRef
		if k == KindExternRef {
			kind = spec.KindExternRef
		}
		if v.IsNull() {
			return spec.Val{Kind: kind, Class: spec.RefLiteralNull}, true
		}
		if id, ok := v.ExternID(); ok {
			return spec.Val{Kind: kind, Class: spec.RefExternIdentity, Extern: id}, true
		}
		return spec.Val{Kind: kind, Class: spec.RefConcrete}, true
	default:
		// KindNone and the GC reference kinds. The harness's `spec.Kind` has no member for them, so
		// there is nothing to map *to* — a result of one is unrepresentable rather than unhandled,
		// and it is refused here and counted at the call site rather than approximated.
	}
	return spec.Val{}, false
}

// specToRaw and rawToSpec are the differential's other arm: the same mapping, against the engine's
// own value type, reaching none of the code under test.
//
// **A deliberate second implementation, which is what makes the comparison an instrument.** Sharing
// a converter with the arm above — or calling `valueToInternal`, the very function this test exists
// to cross-examine — would let one defect satisfy both sides and report agreement it never earned.
// Fifteen lines is the price of an independent witness, and the passability predicate is *not*
// duplicated: specToPublic decides what is askable, and this arm only converts what it already
// admitted.
func specToRaw(v spec.Val) interp.Value {
	switch v.Kind {
	case spec.KindI32:
		return interp.Value{Type: binary.I32, Bits: uint64(uint32(v.Bits))}
	case spec.KindI64:
		return interp.Value{Type: binary.I64, Bits: v.Bits}
	case spec.KindF32:
		return interp.Value{Type: binary.F32, Bits: uint64(uint32(v.Bits))}
	case spec.KindF64:
		return interp.Value{Type: binary.F64, Bits: v.Bits}
	case spec.KindExternRef:
		if v.Class == spec.RefExternIdentity {
			return interp.ExternRef(v.Extern)
		}
		return interp.NullRef(binary.ExternRef)
	default:
		return interp.NullRef(binary.FuncRef)
	}
}

func rawToSpec(v interp.Value) (spec.Val, bool) {
	switch v.Type {
	case binary.I32:
		return spec.Val{Kind: spec.KindI32, Bits: uint64(uint32(v.Bits))}, true
	case binary.I64:
		return spec.Val{Kind: spec.KindI64, Bits: v.Bits}, true
	case binary.F32:
		return spec.Val{Kind: spec.KindF32, Bits: uint64(uint32(v.Bits))}, true
	case binary.F64:
		return spec.Val{Kind: spec.KindF64, Bits: v.Bits}, true
	case binary.V128:
		return spec.Val{Kind: spec.KindV128, Bits: v.Bits, Hi: v.Hi}, true
	}
	if !v.Type.IsRef() {
		return spec.Val{}, false
	}
	kind := spec.KindFuncRef
	if v.Type == binary.ExternRef {
		kind = spec.KindExternRef
	}
	switch {
	case v.Null:
		return spec.Val{Kind: kind, Class: spec.RefLiteralNull}, true
	case v.IsHost:
		return spec.Val{Kind: kind, Class: spec.RefExternIdentity, Extern: v.RefID}, true
	}
	return spec.Val{Kind: kind, Class: spec.RefConcrete}, true
}

// publicTally is what the walk measures. Every command lands in exactly one bucket, which is what
// makes the totals readable: a bucket that grew took its members from a named neighbour.
type publicTally struct {
	files int

	modules           int // module forms offered to the public path
	ran               int // instantiated, fully validated
	declined          int // instantiated, carrying a decline
	refusedInvalid    int // the validator refused — asserted at zero, see below
	refusedGated      int // a proposal this build has switched off (#301)
	refusedMalformed  int // the decoder refused an image it could not read
	refusedOther      int // a trap at time zero, an unsupplied import, an encoder frontier
	gateMisclassified int // a gate refusal wearing another sentinel — asserted at zero

	asserts    int // assert_return commands seen
	compared   int // driven down both paths and judged
	noInstance int // nothing trustworthy to call: the module was refused, or the script diverged
	unpassable int // an argument or result shape this API cannot carry
	callFailed int // both paths failed the call the same way
	disagreed  int // THE assertion: the two paths answered differently
	mismatched int // judged against the vector and wrong — counted, owned by the board

	declines map[string]int
}

// TestConformanceThroughThePublicPath is the coverage decision 0029 exists to buy.
func TestConformanceThroughThePublicPath(t *testing.T) {
	dir, err := gen.FromRoot(testenv.SuiteDir)
	if err != nil {
		t.Fatalf("resolving the suite directory: %v", err)
	}
	testenv.RequireSuite(t, dir)
	paths, err := filepath.Glob(filepath.Join(dir, "*.wast"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob %s after RequireSuite passed: %d paths, err=%v", dir, len(paths), err)
	}

	tally := publicTally{declines: map[string]int{}}
	var disagreements, mismatches []string

	for _, path := range paths {
		s, err := spec.ParseFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		tally.files++
		base := filepath.Base(path)

		// The two instances, driven in lockstep, plus the honesty bit. `trusted` goes false the
		// moment a command this driver does not model could have changed either instance's state —
		// a registered module, a named invoke, an action whose arguments it cannot spell. Without it
		// a later assert_return would be judged against state that only one of the two paths has,
		// which is how a driver defect gets reported as an engine defect: the first version of this
		// test skipped standalone `(invoke …)` commands and produced 388 confident mismatches in
		// `bulk.wast`, every one of them a memory nobody had filled.
		var pub *Instance
		var raw *interp.Instance
		trusted := false

		for _, c := range s.Commands {
			switch c.Kind {
			case spec.KindModuleBinary, spec.KindModuleText, spec.KindModuleQuote:
				pub, raw, trusted = nil, nil, false
				tally.modules++

				image := c.Module
				if c.Kind != spec.KindModuleBinary {
					// The encoder's own frontier, upstream of the boundary under test and charged
					// to a bucket that says so rather than to the decoder's.
					img, eerr := text.EncodeModule(c.Source)
					if eerr != nil {
						tally.refusedOther++
						continue
					}
					image = img
				}

				in, ierr := Instantiate(image)
				// The gate control, and it is scoped to the *space* rather than to the decoder:
				// whichever layer produces a gate refusal, the public classification must be
				// ErrGated. Written this way so that a gate error appearing one day out of the
				// validator or the interpreter is caught here rather than reintroducing #301 at a
				// new layer.
				if ierr != nil && errors.Is(ierr, binary.ErrFeatureDisabled) && !errors.Is(ierr, ErrGated) {
					tally.gateMisclassified++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: a gate refusal was classified as something other than ErrGated: %v",
						base, c.Line, ierr))
				}
				switch {
				case ierr == nil:
					if d := in.Decline(); d != nil {
						tally.declined++
						tally.declines[declineConstruct(d)]++
					} else {
						tally.ran++
					}
				case errors.Is(ierr, ErrGated):
					tally.refusedGated++
					continue
				case errors.Is(ierr, ErrInvalid):
					tally.refusedInvalid++
					mismatches = append(mismatches, fmt.Sprintf(
						"%s:%d: the validator refused a module the corpus offers as valid: %v",
						base, c.Line, ierr))
					continue
				case errors.Is(ierr, ErrMalformed):
					tally.refusedMalformed++
					continue
				default:
					tally.refusedOther++
					continue
				}

				// The other arm: the same image, instantiated the way the harness does it, with no
				// validation and no boundary.
				m, derr := (&binary.Decoder{Features: binary.DefaultFeatures()}).DecodeModule(image)
				if derr != nil {
					// The public path decoded this image and this one did not, with the same
					// features. That is not a possible difference, so it is reported as one.
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: the public path instantiated an image the raw decoder refused: %v",
						base, c.Line, derr))
					continue
				}
				rin, rtrap := interp.Instantiate(m)
				if rtrap != nil {
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: instantiation trapped on the raw path and not the public one: %v",
						base, c.Line, rtrap))
					continue
				}
				pub, raw, trusted = in, rin, true

			case spec.KindInvoke, spec.KindAssertTrapAction, spec.KindAssertException:
				// State mutations, run for their effects on both instances. The outcome is not
				// judged here — a trap is the vector's business, and this driver's business is that
				// the two instances stay in the same state.
				if !trusted {
					continue
				}
				args, ok := publicArgs(c.Args)
				if !ok {
					// An action this driver cannot spell has run on neither instance, so neither is
					// the script's current state any more. Silence here is what produces confident
					// nonsense later.
					trusted = false
					continue
				}
				_, perr := pub.Call(c.Invoke, args...)
				_, rerr := raw.Invoke(c.Invoke, rawArgs(c.Args)...)
				if (perr == nil) != (rerr == nil) {
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: action %q succeeded on one path only: public=%v raw=%v",
						base, c.Line, c.Invoke, perr, rerr))
					trusted = false
				}

			case spec.KindAssertTrapModule:
				// A module that dies coming to life still replaces the script's current module, so
				// what follows has nothing to run against.
				pub, raw, trusted = nil, nil, false

			case spec.KindRegister, spec.KindNamedInvoke, spec.KindNamedAssertReturn,
				spec.KindNamedAssertTrap, spec.KindUnsupported:
				// Linking and named-module selection have no public surface yet — a scope statement
				// of this API, not a gap in the test — and the harness's own catch-all could be
				// anything. Either way the script has moved somewhere this driver cannot follow.
				trusted = false

			case spec.KindAssertReturn:
				tally.asserts++
				if !trusted {
					tally.noInstance++
					continue
				}
				args, ok := publicArgs(c.Args)
				if !ok {
					// **Trust breaks here too, and forgetting that is this driver's own grave twice
					// over.** An `assert_return` is an assertion *and* a call, so one it cannot make
					// is a state mutation that did not happen — `simd_const.wast:1068` sets four
					// v128 globals through one, and skipping it silently left the four asserts after
					// it reading the module's initializers and disagreeing with their vectors in a
					// tidy arithmetic progression. The identical guard three arms up caught the
					// standalone-invoke form of this and I had installed it at one of the two places
					// the condition arises, which is why the residue looked like an engine defect
					// small enough to believe.
					tally.unpassable++
					trusted = false
					continue
				}

				pres, perr := pub.Call(c.Invoke, args...)
				rres, rerr := raw.Invoke(c.Invoke, rawArgs(c.Args)...)

				if (perr == nil) != (rerr == nil) {
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: %s succeeded on one path only: public=%v raw=%v",
						base, c.Line, c.Invoke, perr, rerr))
					continue
				}
				if perr != nil {
					// Both paths declined to answer, the same way. The vector's verdict on that is
					// the board's business; this test's is that they agreed.
					tally.callFailed++
					continue
				}
				if len(pres) != len(rres) {
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: %s returned %d results publicly and %d raw",
						base, c.Line, c.Invoke, len(pres), len(rres)))
					continue
				}

				// Both arms into the harness's own representation, then compared to each other and
				// to the vector. Unrepresentable on either side is unpassable, not a pass.
				pvals, pok := specVals(publicToSpec, pres)
				rvals, rok := specValsRaw(rres)
				if !pok || !rok {
					tally.unpassable++
					continue
				}
				if pvals != rvals {
					tally.disagreed++
					disagreements = append(disagreements, fmt.Sprintf(
						"%s:%d: %s answered %s publicly and %s raw",
						base, c.Line, c.Invoke, pvals, rvals))
					continue
				}

				tally.compared++
				if len(pres) != len(c.Results) {
					tally.mismatched++
					mismatches = append(mismatches, fmt.Sprintf(
						"%s:%d: %s returned %d results, the vector expects %d",
						base, c.Line, c.Invoke, len(pres), len(c.Results)))
					continue
				}
				for i, want := range c.Results {
					got, _ := publicToSpec(pres[i])
					if !want.Matches(got) {
						tally.mismatched++
						mismatches = append(mismatches, fmt.Sprintf(
							"%s:%d: %s result %d: got %v, want %v",
							base, c.Line, c.Invoke, i, got, want))
						break
					}
				}

			default:
				// The four refusal-direction kinds — assert_malformed, assert_malformed_text,
				// assert_unlinkable, assert_invalid. Deliberately not driven here: each is a claim
				// that loading *fails*, which this differential cannot strengthen. The board already
				// scores them, and `internal/spec` is where their strata (1201 validator, of which
				// 142 are silent admissions) are bounded. They do not touch the current instance, so
				// `trusted` is untouched too.
			}
		}
	}

	t.Logf("the public path over %d scripts:\n"+
		"  modules  %5d = %d ran + %d declined + %d gated + %d malformed + %d other + %d INVALID\n"+
		"  asserts  %5d = %d compared + %d no-instance + %d unpassable + %d call-failed + %d DISAGREED\n"+
		"  of the %d compared, %d disagreed with their vector — engine fails the board already owns,\n"+
		"  which is a reading the zero above licenses and nothing else would",
		tally.files,
		tally.modules, tally.ran, tally.declined, tally.refusedGated, tally.refusedMalformed,
		tally.refusedOther, tally.refusedInvalid,
		tally.asserts, tally.compared, tally.noInstance, tally.unpassable, tally.callFailed,
		tally.disagreed,
		tally.compared, tally.mismatched)
	t.Logf("the declining constructs — #9's frontier as a host meets it: %s", topN(tally.declines, 8))
	if tally.mismatched > 0 {
		t.Logf("a sample of the vector disagreements:\n%s", sample(mismatches, 10))
	}

	// The buckets partition their populations. Without this, a command lost between two arms of the
	// switch would quietly shrink the denominator that every floor below is measured against.
	if got := tally.ran + tally.declined + tally.refusedGated + tally.refusedInvalid +
		tally.refusedMalformed + tally.refusedOther; got != tally.modules {
		t.Errorf("the module buckets sum to %d, not %d", got, tally.modules)
	}
	if got := tally.compared + tally.noInstance + tally.unpassable + tally.callFailed +
		tally.disagreed; got != tally.asserts {
		t.Errorf("the assert buckets sum to %d, not %d", got, tally.asserts)
	}

	// **The assertion.** Same engine, same image, two paths: any difference is the boundary's, and
	// it is the boundary that is new.
	if tally.disagreed != 0 {
		sort.Strings(disagreements)
		t.Errorf("%d results differ between the public path and the engine's own, so the boundary "+
			"changed an answer (showing %d):\n%s",
			tally.disagreed, min(len(disagreements), 20), sample(disagreements, 20))
	}

	// **And the population it can drive, it gets right.** Asserted rather than logged, which is a
	// judgement about instruments and is flagged for Scott in the PR rather than settled here: the
	// argument against is that the board owns the engine's fail count and a second assertion over an
	// overlapping population is a second ledger; the argument for, which is why it is written this
	// way, is that "the engine works through the published API" is exactly the claim this PR was
	// asked to make executable, and a census nobody asserts makes no claim at all. The population is
	// not the board's: it is what the public boundary can drive end to end, which excludes every
	// command kind this driver skips and every script tail after a trust break.
	//
	// If engine capability growth ever makes this non-zero — a validator slice admitting vectors the
	// interpreter then fails — the movement is a finding to triage, **not a ceiling to raise**. The
	// difference matters: `unsupportedCeiling` bounds a column that drains, and this bounds a column
	// that should never fill.
	if tally.mismatched != 0 {
		t.Errorf("%d of %d assertions driven through the public path disagreed with their vector "+
			"(showing %d):\n%s", tally.mismatched, tally.compared, min(len(mismatches), 20),
			sample(mismatches, 20))
	}

	// Grave #301's control. Zero is the only readable answer: the message names a proposal, so a
	// caller told "malformed" goes looking for a defect in a module the spec accepts.
	if tally.gateMisclassified != 0 {
		t.Errorf("%d gate refusals were classified as something other than ErrGated",
			tally.gateMisclassified)
	}

	// The new behaviour this surface introduces, and the number that says what it cost. The public
	// path validates before running and the harness's run path does not (ADR 0025's carve-out), so
	// this is the first measurement of validation-on-the-run-path against real modules: it refuses
	// none of them. A regression that started refusing valid modules would land here, and nowhere
	// else in the repo, since the harness only ever asks the validator about `assert_invalid`.
	if tally.refusedInvalid != 0 {
		t.Errorf("the validator refused %d modules the corpus offers as valid; validating on the "+
			"run path is supposed to cost nothing in false refusals:\n%s",
			tally.refusedInvalid, sample(mismatches, 10))
	}

	// The vacuity guards. Every count above is zero if the walk finds nothing, and zero
	// disagreements over zero comparisons is the cleanest possible result and says nothing at all.
	// Exact where the figure is a property of the corpus at its pin, floors where engine progress
	// moves it in a known direction — and stated as counted here rather than copied from the board,
	// whose filter is not this one.
	if tally.files != 257 {
		t.Errorf("walked %d scripts, want 257 at this suite pin", tally.files)
	}
	if tally.modules != 2238 {
		t.Errorf("offered %d module forms to the public path, want 2238 at this pin; the corpus or "+
			"this driver's command vocabulary moved", tally.modules)
	}
	// A floor rather than a pin, with the slack stated: this number moves *up* on engine work — a
	// validator slice that turns declines into runs, a decoder gate flip, a public v128 argument —
	// and pinning it exactly would make every one of those a failing test with a number to edit.
	// Measured at 25666, floored with room for the drift a growing driver causes in the other
	// direction, which is a trust break earlier in a script taking the tail with it.
	if tally.compared < 25000 {
		t.Errorf("only %d assertions were driven down both paths; the differential's whole claim "+
			"rests on this number, so a drop is a loss of coverage rather than a detail",
			tally.compared)
	}
	if tally.ran+tally.declined < 1200 {
		t.Errorf("only %d modules instantiated through the public path", tally.ran+tally.declined)
	}
	if tally.declined == 0 {
		t.Error("no module declined, so the third outcome went unexercised by the corpus: either " +
			"#9's vocabulary is complete — retire this guard and flip Config.Strict's default — or " +
			"the decline is being lost")
	}
	if tally.refusedGated == 0 {
		t.Error("no module was refused for a gate, so grave #301's control has no subject: either " +
			"every tracked proposal has flipped, or the classification is gone")
	}
}

// publicArgs converts a vector's arguments, reporting false if any is unspellable at this boundary.
func publicArgs(vals []spec.Val) ([]Value, bool) {
	out := make([]Value, 0, len(vals))
	for _, a := range vals {
		v, ok := specToPublic(a)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// rawArgs is publicArgs' independent twin. It has no passability predicate of its own — the caller
// has already asked publicArgs, so this converts an argument list both paths agreed to accept.
func rawArgs(vals []spec.Val) []interp.Value {
	out := make([]interp.Value, 0, len(vals))
	for _, a := range vals {
		out = append(out, specToRaw(a))
	}
	return out
}

// specVals renders a public result list as a comparable string, or false if any result is
// unrepresentable in the harness's vocabulary.
//
// A string rather than a slice because it is compared with `==` and printed in the same breath, and
// because the two arms must be comparable without either's type appearing in the other's signature.
func specVals(conv func(Value) (spec.Val, bool), vals []Value) (string, bool) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		sv, ok := conv(v)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprint(sv))
	}
	return "[" + strings.Join(parts, " ") + "]", true
}

// specValsRaw is specVals for the engine's own values.
func specValsRaw(vals []interp.Value) (string, bool) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		sv, ok := rawToSpec(v)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprint(sv))
	}
	return "[" + strings.Join(parts, " ") + "]", true
}

// declineConstruct pulls the construct's name out of a decline message for the census.
//
// String surgery on an error's text, which is ordinarily the wrong instrument — but the subject here
// *is* the message, since the message is what a host reads (0029: the decline is the campaign's
// public work plan), and the census is a log line. Nothing branches on it.
func declineConstruct(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return strings.TrimSpace(msg[i+2:])
	}
	return msg
}

// topN spells the n most common keys of a census, with the tail counted rather than dropped.
func topN(counts map[string]int, n int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, n+1)
	for _, k := range keys[:min(n, len(keys))] {
		parts = append(parts, fmt.Sprintf("%s×%d", k, counts[k]))
	}
	if len(keys) > n {
		parts = append(parts, fmt.Sprintf("(+%d more)", len(keys)-n))
	}
	return strings.Join(parts, " ")
}

// sample renders at most n lines of a list, sorted, saying how many it withheld.
func sample(lines []string, n int) string {
	sort.Strings(lines)
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n  … and %d more", len(lines)-n)
}
