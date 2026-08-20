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
// So the primary assertion is a **differential** instead: every vector is driven down two paths in
// lockstep — the published API, and `interp` directly, as the harness drives it — and the paths must
// agree. What that buys is the reading of the vector comparison: given agreement, a mismatch here *is*
// a board fail, already counted, already owned. The claim the two instruments make together is the one
// Scott asked for — the engine is conformant through the public path wherever it is conformant at all —
// and neither can make it alone.
//
// The vector comparison is **also** asserted at zero, and that is a ruling rather than a drafting
// choice (chat-Claude, PR #302): it currently holds, and a disagreement between the two paths over one
// module and one export is a defect by construction, with no legitimate population to census. See the
// long note at the assertion itself for what happens if it ever moves, and for why the exemption
// shape is "named, with a reason each" and never a tolerated count.
//
// The two arms are deliberately independent implementations. `specToPublic`/`publicToSpec` cross the
// boundary under test; `specToRaw`/`rawToSpec` do not touch it, and re-derive the same mapping in
// fifteen lines against `interp.Value`'s exported fields. A shared helper would make both arms wrong
// together, which is the one way a differential can report agreement it has not earned.
//
// # What is asserted at zero, and what is only counted
//
// Asserted: path agreement; agreement with the vectors over the population this driver can drive; that
// no gate refusal is classified as anything but ErrGated (grave #301); that the validator now on the
// run path refuses **no** module the corpus offers as valid; that no import-free module arms carrying a
// deferred shortfall (grave #421); that the buckets partition. Counted and logged: the decline census,
// which is #9's frontier stated in modules a user would actually hand this engine, and the domain
// split, a decline being callable rather than refused.
//
// The split's *figures* are printed by the run and deliberately not repeated here — see the long
// domain section at the vector assertion for the two generations of stale arithmetic that decided
// that.
//
// (An earlier draft of this paragraph listed vector agreement under "counted and logged" while the code
// below asserted it. Comments are testimony and the executable outranks; corrected here rather than
// left as a second, softer account of what this file does.)
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
	case spec.KindAnyRef:
		// **A refusal with an arm, not an absence.** 0039 added this kind and `publicToSpec` below
		// carries it, so this *argument* direction not carrying it is a claim that needs saying out
		// loud: a `(ref.host N)` argument needs the reference's *type*, and the harness takes that
		// from the declared parameter (`toInterpValue` is handed `t`), while this converter sees a
		// `spec.Val` and nothing else. It would have to **guess** `anyref` — wrong wherever the
		// parameter is narrower, and reported as a decline rather than as the refusal it really is.
		// The one corpus row concerned stays in `unpassable`, counted. `exhaustive` is what asked for
		// the arm, and the arm is the better spelling: absence relying on a fallthrough is
		// indistinguishable from an omission.
		return Value{}, false
	case spec.KindUnnameableRef:
		// **Refused twice over, and neither reason is a limit of this boundary.** ADR 0040's sentinel
		// says "the harness has no member for that reference type", so asking this API to spell one as
		// an *argument* is asking it to spell a type whose name was the thing that went missing — there
		// is nothing to hand `AbstractRefType`. And it cannot arrive here anyway: a vector literal
		// never carries it (`fromInterpValue` puts it on *results*, and `refPatterns` puts it on five
		// bare `(ref.<ht>)` patterns, which are expectations refused by the Class switch below). So the
		// arm exists because `exhaustive` asks every member to be answered on the nose, and answering
		// is free where the answer is this clear.
		return Value{}, false
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
	case KindFuncRef, KindExternRef, KindAnyRef:
		kind := spec.KindFuncRef
		switch k {
		case KindExternRef:
			kind = spec.KindExternRef
		case KindAnyRef:
			kind = spec.KindAnyRef
		default:
		}
		if v.IsNull() {
			return spec.Val{Kind: kind, Class: spec.RefLiteralNull}, true
		}
		// The payload joins both arms since 0039, because `spec.Val.String` prints it and this
		// differential compares the two arms' printed Vals — an arm that dropped it would report
		// disagreement on every reference result rather than on a real difference.
		payload := specPayload(v)
		if id, ok := v.ExternID(); ok {
			return spec.Val{Kind: kind, Class: spec.RefExternIdentity, Extern: id, Payload: payload}, true
		}
		return spec.Val{Kind: kind, Class: spec.RefConcrete, Payload: payload}, true
	default:
		// KindNone and the remaining GC reference kinds — the nine other abstract kinds and
		// `KindTypedRef` — refused here and counted at the call site rather than approximated.
		//
		// **This arm's reason changed under ADR 0040 and the refusal did not, which is the part worth
		// saying.** It used to be that `spec.ValKind` had no member to map these *to*, so the refusal
		// was forced. It now has one: `spec.KindUnnameableRef`, whose meaning is exactly "this
		// harness cannot name that type", and the raw arm below reaches it. So a mapping is available
		// and is **not taken**, and the reason is a measurement rather than a principle: **no result
		// reaches this arm** in the corpus — every reference result the differential sees is nullable
		// `funcref` or nullable `externref` — so widening it would move zero rows out of `unpassable`,
		// and an unwitnessed widening of a converter is one whose first exercise would be by whoever
		// changes it next. The asymmetry with the raw arm cannot manufacture a disagreement either:
		// a refusal on *either* arm lands the row in `unpassable`, which is a counted bucket and not
		// a pass. If GC results ever arrive through this boundary, that bucket is where they show up,
		// and this arm is the first place to look.
	}
	return spec.Val{}, false
}

// specPayload maps a public result's payload kind onto the harness's, for publicToSpec.
//
// Written out rather than reaching for `payloadKinds`: that table is part of what this file
// cross-examines, and a shared table is a shared defect. It is a *third* transcription of the same
// seven rows, which is the point — TestPayloadConversionCoversTheWholeVocabulary asserts that the
// vocabulary is covered, and this arm asserts, against the raw arm, that two independent readings
// of it agree.
func specPayload(v Value) spec.RefPayload {
	p, ok := v.RefPayload()
	if !ok {
		// Numeric, or a null reference — publicToSpec's null arm returns before reaching here, so
		// this is the numeric case, which has no payload to name.
		return spec.PayloadNone
	}
	switch p {
	case PayloadHost:
		return spec.PayloadHost
	case PayloadI31:
		return spec.PayloadI31
	case PayloadStruct:
		return spec.PayloadStruct
	case PayloadArray:
		return spec.PayloadArray
	case PayloadFunc:
		return spec.PayloadFunc
	case PayloadExn:
		return spec.PayloadExn
	case PayloadNone, payloadPastEnd:
		return spec.PayloadNone
	}
	// Not a default: a switch with no default over an enum that exists to grow is what makes the
	// growth visible. A new member reaches here and reads as `none`, and the coverage control is what
	// fails first — this line is the fallback for a build that somehow ran without it.
	return spec.PayloadNone
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
	// **Every nameable heaptype gets an arm, and the fallback says "no name".** `spec.ValKind` has
	// one member per *nameable* reference type — `funcref`, `externref`, and `anyref` since
	// #270/0039 — and `spec.KindUnnameableRef` for the rest: the nine other abstract heaptypes and
	// every `(ref $t)`. This arm's independent reading of that rule used to be spelled the other way
	// round, as `kind := spec.KindAnyRef` with two arms, so `anyref` came out right by *falling
	// through* rather than by being named. ADR 0040 took that spelling away, because falling through
	// now lands on a Kind whose whole meaning is "this harness cannot name the type" — which would be
	// a false statement about a genuine `anyref`.
	//
	// The old form also read a **spelling where the fact is a heaptype**: `binary.FuncRef` and
	// `binary.ExternRef` are the nullable abbreviations, so a non-null `(ref func)` matched neither
	// arm and took the fallback, answering `anyref` for a type the public arm's `Type().Kind()` calls
	// `funcref` on its side. `Kind()` here reads the heaptype byte, which covers both nullabilities
	// and is the same fact the other arm reads — so the two arms now agree by naming the same three
	// heaptypes rather than by sharing a fallback.
	//
	// **No corpus row reaches the fallback**, measured rather than assumed: every reference result
	// the differential sees is exactly nullable `funcref` or nullable `externref`. So this is a
	// change to what the arm *would* answer, and the `DISAGREED` count cannot witness it either way —
	// which is also why the mapping is stated as far as the vocabulary allows instead of stopping at
	// the population that happens to exist. `spec.Val.String` prints the payload for a concrete
	// reference and `ref.null` for a null, so the Kind is read only through `RefExternIdentity`'s
	// three spellings: the column is mostly a *statement* here, exactly as `refPatterns`' Kind column
	// became under the same decision.
	kind := spec.KindUnnameableRef
	if k, ok := v.Type.Kind(); ok {
		switch k {
		case binary.HeapFunc:
			kind = spec.KindFuncRef
		case binary.HeapExtern:
			kind = spec.KindExternRef
		case binary.HeapAny:
			kind = spec.KindAnyRef
		}
	}
	if v.Null {
		return spec.Val{Kind: kind, Class: spec.RefLiteralNull}, true
	}
	payload := rawPayload(v.RefKind)
	if v.RefKind == interp.PayloadHost {
		return spec.Val{Kind: kind, Class: spec.RefExternIdentity, Extern: v.RefID, Payload: payload}, true
	}
	return spec.Val{Kind: kind, Class: spec.RefConcrete, Payload: payload}, true
}

// rawPayload is specPayload's independent twin against the engine's own enum, for the same reason the
// rest of this arm is written twice. It reaches neither `payloadKinds` nor `interpPayloads`.
func rawPayload(p interp.RefPayload) spec.RefPayload {
	switch p {
	case interp.PayloadHost:
		return spec.PayloadHost
	case interp.PayloadI31:
		return spec.PayloadI31
	case interp.PayloadStruct:
		return spec.PayloadStruct
	case interp.PayloadArray:
		return spec.PayloadArray
	case interp.PayloadFunc:
		return spec.PayloadFunc
	case interp.PayloadExn:
		return spec.PayloadExn
	case interp.PayloadNone, interp.PayloadPastEnd:
		return spec.PayloadNone
	}
	return spec.PayloadNone
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

	// importing is **not one of the buckets above** — it cuts across them, which is why it is
	// stated apart from a partition whose whole readability rests on membership being exclusive. A
	// module carrying imports still instantiates, so it lands in `ran`, `declined` or a refusal
	// like any other; what this counts is that it was not *driven*, because nothing supplied what
	// it imports. Grave #421's counter.
	importing int
	// incomplete counts armed, import-free modules whose `Deferred` is non-nil — asserted at zero,
	// and orthogonal to the buckets for `importing`'s reason. Grave #421's other half.
	incomplete int

	asserts    int // assert_return commands seen
	compared   int // driven down both paths and judged
	comparedOn struct {
		ran, declined int // which kind of instance the comparison ran against
	}
	noInstance int // nothing trustworthy to call: the module was refused, or the script diverged
	unlinked   int // the module imports something no linking surface exists to supply (grave #421)
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
	var disagreements, mismatches, incompletes []string

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
		// Whether the armed instance carries a decline. A declined module is still *callable* — the
		// decline says this slice could not check every instruction, not that the module is refused —
		// so it arms like any other and its asserts are compared. Tracked because the comparison's
		// domain is a claim about this instrument, and "the declines are the excluded population" is
		// a plausible reading of the module census that happens to be false.
		declinedNow := false
		// Whether the armed module imports something nothing supplied — the trust break's reason,
		// kept apart from the others so the asserts it costs are named rather than pooled into
		// `noInstance`. Grave #421.
		unlinkedNow := false

		for _, c := range s.Commands {
			switch c.Kind {
			case spec.KindModuleBinary, spec.KindModuleText, spec.KindModuleQuote:
				pub, raw, trusted, declinedNow = nil, nil, false, false
				unlinkedNow = false
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
						declinedNow = true
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
				// **A module whose imports nothing supplied is not the script's module, and this is
				// grave #421.** The trust break for linking was installed on the *supply* side —
				// `(register …)` sets `trusted = false` three arms down — and a module command
				// clears the flag again, so it protected nothing: the demand side, an `(import …)`
				// in the module itself, armed and was driven with the imported global reading zero,
				// the imported function absent, the imported memory a different memory. The
				// condition arises at two places and the guard was at one of them, which is this
				// file's own grave twice already (`publicArgs` above, in both of its arms).
				//
				// Latent rather than harmless: `global.wast:634` is the module that surfaced it, an
				// imported `i32` read by two *defined* globals which are in turn two active segment
				// offsets, so the corpus asks a question whose answer survives instantiation and is
				// readable from an export. It reached this driver for the first time in #419 — its
				// table carries a spelled initializer, so the emitter had refused it — and answered
				// `ref.null` for a `funcref` at index 4 and 0 for `0x44444444` at address 4, both of
				// them the offsets collapsing to zero because global 0 was never supplied. Every
				// other import-bearing module in the corpus had been trusted too and got away with
				// it, because an absent import is usually reached *through a call*, which then fails
				// identically on both paths and lands in `callFailed`.
				//
				// The property is read off the module rather than off the failure: `len(m.Imports)`
				// is total and syntactic, where matching the decline's text would be a guess about
				// which absences the instance happened to notice. Imports are unsupplied *by
				// construction* here — this API has no linking surface at all (0029's scope, stated
				// at the `KindRegister` arm) — so the predicate needs no second condition.
				if len(m.Imports) > 0 {
					tally.importing++
					pub, raw, trusted, unlinkedNow = in, rin, false, true
					continue
				}
				// **And the channel the boundary already published for this fact, now read.**
				// `Instance.Deferred` exists precisely to say "a nil trap is not the same claim as
				// this module came to life completely", and its doc names the unsupplied-import case
				// as its example — so the fact was available at the boundary all along and this
				// driver did not ask. That is the other half of #421, and the reason the import
				// predicate above is not written as `Deferred() != nil`: the two catch different
				// populations. A shortfall is only recorded where instantiation *reached* it, so an
				// imported function nothing calls at load defers nothing while still being absent;
				// and conversely a module with **no imports at all** that defers anything is a
				// finding, since every other cause is an engine shortfall this driver would
				// otherwise drive straight past and judge.
				//
				// Asserted at zero rather than counted, on the same argument as `refusedInvalid`
				// below: the population is not a legitimate exclusion, it is a claim that the
				// instance is incomplete for a reason unrelated to linking.
				if d := in.Deferred(); d != nil {
					tally.incomplete++
					incompletes = append(incompletes, fmt.Sprintf(
						"%s:%d: an import-free module armed carrying a deferred shortfall, so the "+
							"instance is incomplete and its exports would be judged anyway: %v",
						base, c.Line, d))
					pub, raw, trusted = in, rin, false
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
					// Two reasons, two buckets. An unlinked module is an exclusion this API's scope
					// implies and can be quoted as such; a plain no-instance is a refusal or a
					// divergence upstream. Pooling them would put the linking gap's whole cost
					// inside a number that already had a different meaning.
					if unlinkedNow {
						tally.unlinked++
					} else {
						tally.noInstance++
					}
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
				if declinedNow {
					tally.comparedOn.declined++
				} else {
					tally.comparedOn.ran++
				}
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
		"  of the %d modules, %d carry imports and are therefore not driven (no linking surface)\n"+
		"  asserts  %5d = %d compared + %d no-instance + %d unlinked + %d unpassable + "+
		"%d call-failed + %d DISAGREED\n"+
		"  of the %d compared, %d ran on a fully-checked module and %d on a declining one\n"+
		"  of the %d compared, %d disagreed with their vector — engine fails the board already owns,\n"+
		"  which is a reading the zero above licenses and nothing else would",
		tally.files,
		tally.modules, tally.ran, tally.declined, tally.refusedGated, tally.refusedMalformed,
		tally.refusedOther, tally.refusedInvalid,
		tally.modules, tally.importing,
		tally.asserts, tally.compared, tally.noInstance, tally.unlinked, tally.unpassable,
		tally.callFailed, tally.disagreed,
		tally.compared, tally.comparedOn.ran, tally.comparedOn.declined,
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
	if got := tally.compared + tally.noInstance + tally.unlinked + tally.unpassable +
		tally.callFailed + tally.disagreed; got != tally.asserts {
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

	// **And the population it can drive, it gets right. Asserted, and ruled so.**
	//
	// The hedge this comment used to carry — asserted here but flagged for a principal, on the
	// argument that a second zero over a population overlapping the board's is a second fail ledger —
	// is discharged: **it stays an assertion.** The reasoning, which is worth keeping because it is
	// what makes the assertion legitimate rather than merely tolerated: a differential's disagreement
	// is *a defect by construction*. Same module, same export, two paths — if they differ one of them
	// is wrong, so there is no legitimate population to census. A nondeterminism between two paths in
	// one engine is the single most valuable thing this instrument can find, and burying it in a log
	// is the one outcome worth avoiding. That is not the shape `unsupportedCeiling` has: that bounds a
	// column which drains as work lands, and this bounds a column that should never fill.
	//
	// So if engine capability growth ever makes this non-zero — a validator slice admitting vectors the
	// interpreter then fails — the movement is a finding to triage, **never a ceiling to raise**. And
	// if an exemption is ever genuinely needed, it is **enumerated by name with a reason each, on the
	// nose**; a tolerated count is exactly the second ledger this design refuses.
	// (Ruling: chat-Claude, PR #302, on the flag this comment used to carry.)
	//
	// # The domain, stated — and every figure this section ever carried has gone stale once
	//
	// The ruling came with a reading of the census: that the declines carry the legitimate exclusions,
	// so the comparison's domain is the fully-checked modules alone. That is false in principle and
	// stays false — a decline is not a refusal, it says this validator slice could not check every
	// instruction, not that the module is rejected, so a declining module instantiates, arms, and has
	// its exports called like any other. What *has* changed is the arithmetic the section used to
	// argue it with: "11166 of the 25666 comparisons, 43%, run on one", over a domain of "1787 module
	// forms (1067 ran + 720 declined)".
	//
	// **Third generation — and this time the figures are deleted rather than corrected.** #419 replaced
	// the first set with "8 declined, and 0 comparisons run on a declining module", wrote down why the
	// first set had rotted — *a prose figure is a measurement with no instrument watching it* — and
	// then carried its own crop of figures in the paragraphs that followed. Most of them are wrong
	// now, and the ones that still hold do so by luck of the draw rather than by anything watching
	// them. The falsifier is **this same file, at the decline guard below**: #427 typed the last
	// untyped prefix region, so the declines drained to zero and that guard inverted to catch a
	// decline *re-appearing* — which retired, in passing, this paragraph's "seven relaxed-SIMD
	// constructs in the log line above". The module and comparison counts moved with the engine
	// underneath them. None of it was any one PR's doing, which is the complaint rather than an
	// excuse: two paragraphs 120 lines apart disagreed about the same measurement and nothing in this
	// tree could notice.
	//
	// Correcting is what the previous two generations did, and each correction re-created the channel
	// that had just failed. What a reader needs here is the rule, which has no number in it: the
	// domain is every module form this driver can arm — `ran` plus `declined`, less those importing
	// something no linking surface supplies (grave #421) — and the exclusions are the other buckets,
	// which are already what the ruling asks for, named with a reason each and never a tolerated
	// count: gated, encoder-frontier, no-instance (a refused module or a trust break upstream),
	// unlinked, unpassable argument or result shapes, and calls that failed identically on both paths
	// (a bucket that was itself large until grave #421, every one of them a call into a module whose
	// import was missing, which is why they failed the same way twice). Every count is in the log line
	// above, printed by the run that measured it, and the buckets are asserted to partition just above
	// this comment — so a row lost between two arms cannot quietly shrink the denominator the floors
	// are measured against.
	if tally.mismatched != 0 {
		t.Errorf("%d of %d assertions driven through the public path disagreed with their vector "+
			"(showing %d):\n%s", tally.mismatched, tally.compared, min(len(mismatches), 20),
			sample(mismatches, 20))
	}

	// The second half of grave #421, asserted rather than counted — see the module arm for why the
	// population is not a legitimate exclusion. Zero today, and the value is in the direction it
	// would move: engine capability growth cannot make this non-zero, only an engine shortfall
	// reached at instantiation can, and this driver is the only place that reads the channel.
	//
	// **Watched fire before being believed.** A zero over a condition the corpus may not contain is
	// an unreached branch wearing a pass, so the residual was certified against real data: neutering
	// the import predicate in the module arm — the only thing standing between this check and the 180
	// import-bearing modules — makes it report **51**, `data.wast:100` first, each one an active data
	// segment whose target memory is an unsupplied import. So the mechanism reaches the channel, reads
	// it, and formats it; with the predicate in place the 51 are all inside the 180 and the remainder
	// is a measured zero.
	if tally.incomplete != 0 {
		t.Errorf("%d import-free modules armed with a deferred shortfall (showing %d):\n%s",
			tally.incomplete, min(len(incompletes), 10), sample(incompletes, 10))
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
	// # The decline guard fired, its first branch was the true one, and so it is inverted here
	//
	// What stood here asserted `tally.declined != 0` and said: "either #9's vocabulary is complete —
	// retire this guard and flip Config.Strict's default — or the decline is being lost". #427 made it
	// the first, by typing `fd 0x100..0x12f`. The validator now has a rule for **every instruction the
	// decoder can name under `DefaultFeatures`**, and this boundary is default-features-only by
	// deliberate design (see burroughs.go's "no gate selection here"), so a decline has no reachable
	// subject at the public path — not "none in this corpus".
	//
	// The direction flips rather than the guard being deleted, because *a tripwire names a risk, not a
	// code shape* (#33) and the risk did not dissolve with its subject. A decline **re-appearing** is
	// now the event worth catching: it means either a new prefix region the validator does not type
	// (0xFE is threads', a v1 milestone), or a gate flip carrying opcodes past the decoder into a
	// validator with no rule for them — which is exactly how ADR 0025's G-1 carve-out is supposed to
	// be visible rather than silent.
	//
	// Two things this does *not* say, because both are easy to read into it. It does not say #9 is
	// complete: **#111's nine valtype positions accept `(ref null $undefined)` and no suite vector fails
	// on it**, so the population most likely to hold an accept-direction miss is the one no board
	// partition can even ask about, and an admission is a different stratum from a decline. And it does
	// not flip `Config.Strict`'s default, which its own doc comment schedules for this moment; that is a
	// default-behaviour change and so a stamp-tier event, flagged for Scott rather than taken here.
	//
	// **This paragraph's example has now been wrong twice, and the second was the repair of the first.**
	// Both are kept: the pair is what the site is worth reading for.
	//
	// First: *"alignment is not checked at all (`validate/vec.go`, `decodeMemop` drops the memarg)"*. It is
	// checked — `internal/validate/align.go` — and has been since #306 landed in #313. The clause came from
	// `vec.go`'s non-goals section, which #306 falsified and nobody re-read (grave #431), written into this
	// file by the PR whose own subject was that shape.
	//
	// The lesson recorded with it, at the time, was this: the failure was not picking a weak example, it
	// was **sourcing a premise from a paragraph when an instrument was one call away, and this file exists
	// to prefer the instrument.**
	//
	// Second, in the repair, one clause after that lesson: *"#328's 103 module-and-section vectors have no
	// vocabulary"*. **#328 closed as completed the day before**, its vocabulary supplied by #403,
	// `validateAdmitCeiling` re-based 28 → 0 (grave #434). The instrument one call away was `gh issue view
	// 328`; **no sweep's domain includes the tracker**, which is why the recurrence was silent. So the
	// lesson above was written, and then broken, in the same edit — because it had been applied to the
	// *fact* and not to the *sourcing*.
	if tally.declined != 0 {
		t.Errorf("%d modules declined at the public path, and since #427 nothing should: the "+
			"validator types every instruction the decoder can name under DefaultFeatures. A decline "+
			"here means a construct reached validation with no rule — name it, because it is either a "+
			"new region or a gate flip that outran its typing slice (#326 for the derivation that "+
			"would name it automatically)", tally.declined)
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
