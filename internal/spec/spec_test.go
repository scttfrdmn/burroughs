package spec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
	"github.com/scttfrdmn/burroughs/internal/validate"
)

// suiteDir is where make spec-tests vendors the upstream suite. Gitignored;
// tests skip rather than fail when it is absent, so a fresh clone is green
// before the fetch.
//
// That skip license is exactly one thing — local-dev convenience on an unvendored
// clone — and internal/testenv revokes it under BURROUGHS_NO_SKIP=1, which every
// CI job that runs tests sets. See the package doc there for the grave (#29).
const suiteDir = "../../testdata/spec"

// boardFiles is the corpus the board scores: every vendored .wast file that holds at
// least one command the engine can answer.
//
// **Derived, not enumerated (#52).** It used to be a hand-written list of eight
// byte-string files, and that list was the enumerated-literal defect living in the
// corpus selector itself — *derive the domain, never enumerate it*, unapplied to the
// oracle's own inputs. Its blind spot was measured rather than argued — by the selector,
// not by a grep, since a regex over `(module\s+binary` counts *text* while binaryModule
// counts what the decoder will actually be handed: **six** files (align, binary-gc, elem,
// float_literals, global, simd_const) hold commands the engine already answers and were
// off the board because of their *neighbours* — an assert_return in the same file, not
// anything the decoder could not do. The result was 19 passes, 8 fails, and 6 gated
// vectors the board could not see, one of which (#51) is a live accept-direction defect:
// a valid module rejected with the spec's own word for malformed.
//
// The selection question changed with it. It was "which files did we list", and it is now
// "which files contain a command whose Kind the run loop scores" — a capability question,
// asked of the corpus rather than answered from memory. A file whose every command is
// unsupported is excluded because scoring it would add unsupported lines and no verdicts;
// a file with one answerable command is included, and its other commands are counted in
// the unsupported column where they are visible.
//
// The consequence is deliberate and is the doctrine this change carries: the board's
// unsupported count goes from zero to 1345. Zero-unsupported was a property of the
// byte-string corpus, never a law of the board. What is honest is that nothing hides —
// so the column is bucketed by command head, floored against growth, and expected to
// shrink monotonically as components land.
//
// **Admitting (module quote ...) widened it again, 14 files to 68 (decision 0010).** The
// selector did not change: it still asks which files hold a command whose Kind the run
// loop scores, and two new Kinds made 54 more files answer yes. That is the derived
// selector working as designed — a capability question re-asked when capability moves,
// rather than a list re-edited — and it is why the admission could not be scoped to the
// eleven vectors that prompted it. The unsupported ceiling rose 1345 → 26742 in the same
// motion, which is *corpus admitted*, not regression.
//
// **And again, 68 files to 253, when the bare `(module <wat body>)` form became scorable
// (#69).** Same sentence as the paragraph above, and that repetition is the finding: this
// is the third time a capability landing has moved the *file set* rather than the command
// mix inside a fixed set, because the selector's question is "does this file hold one
// scorable command".
//
// **And a fourth time, 253 → 254, when instantiation gained a trapping path (0015).**
// `data1.wast` was the one excluded file whose exclusion this list called "the
// interpreter's", and the label was a prediction that came due: every form in it is
// `assert_trap` wrapping a bare module, nothing else, so the file held no scorable command
// until that shape became a Kind. The list below is now three, and the shrink is the reason
// this paragraph exists — *an itemized exclusion outlives the exclusion unless someone
// re-prints it*, and this one had a file in it that the same PR admitted.
//
// 254 of 257 vendored files hold a scorable command, and the three that do not were
// *printed*, not predicted — two are the validator's and one is a real gap:
//
//	memory_size3.wast        2 assert_invalid     the validator's
//	unreached-invalid.wast 121 assert_invalid     the validator's
//	inline-module.wast       3 bare fields        **not** a later stratum's — see below
//
// `inline-module.wast` is the honest miss. Its commands are `(func)`, `(func)`, `(memory
// 1)` at top level: the reference's `inline_module` production (parser.mly:1447), where a
// script's module wrapper is elided and bare fields *are* the module. The classifier keys
// on the `module` head, so it sees three unrelated forms rather than one module and calls
// each unsupported. That is a harness gap of the same species #69 just closed, and it is
// left open deliberately — recognizing it means the classifier must fold a run of adjacent
// field forms into one synthetic module, which is a classification decision and not a span
// one. Named here rather than folded in, because a file excluded for a reason nobody wrote
// down is indistinguishable from one excluded correctly.
func boardFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		if scorableCommands(s) > 0 {
			files = append(files, filepath.Base(p))
		}
	}
	// Vacuity floor, per the comparisons-need-a-vacuity-check rule: a selector that
	// finds nothing agrees with a board of nothing, and every assertion downstream
	// would compare two empty sets and pass.
	//
	// **68 files, printed rather than reasoned.** The first draft of this floor said 20
	// on the strength of "27 files have no interpreter-dependent command" — a different
	// set, and the floor caught the error immediately by failing. Those 27 include files
	// whose every command is a text-bodied module or an assert_invalid, all unsupported;
	// what this selector wants is files with at least one *scorable* command, which was
	// 14 before the quote admission and is 68 after it. (data.wast is the instructive
	// miss: it has five (module binary ...) forms whose elements are not all string
	// literals, so binaryModule rejects them and the file has nothing scorable.)
	//
	// The floor tracks the measurement rather than staying at its historical value: a
	// floor of 12 against 68 selected files would tolerate the selector losing 56 files
	// silently, which is the vacuity hole one step short of empty. 60 left room for
	// upstream churn without leaving room for a selector that mostly stopped selecting.
	//
	// **Raised 60 → 240 by #69**, and the raise is the rule applied to itself. A floor of
	// 60 against 253 selected files is the same defect it was written to prevent, one
	// magnitude up: the selector could lose 193 files — every file the bare-module
	// admission just brought in, and therefore every vector the 4122 pass floor rests on
	// — and this guard would still report success. *A floor left at its historical value
	// is a floor that stopped bounding anything*, so it moves with the measurement or it
	// is decoration. 240 of 256 as of #9's `assert_invalid` arm (254 before it, which admitted
	// `memory_size3.wast` and `unreached-invalid.wast` — two files whose every command was
	// unsupported, so they were not on the board at all), the 16-file margin being upstream churn
	// room; the three
	// legitimately-excluded files are itemized at the top of this function, so a fourth
	// appearing is a fact somebody has to write down.
	//
	// Deliberately **not** raised to 241 by 0015's one extra file. The margin is churn room
	// measured against upstream, not slack to be reclaimed every time the number moves by
	// one, and re-pinning a floor to each new actual makes it a restatement of the actual
	// rather than a bound. It moves when the *distance* stops being meaningful — which is
	// the 60-against-253 case above — not when the actual moves inside it.
	if len(files) < 240 {
		t.Fatalf("boardFiles selected only %d files, want >=240 — the selector is not "+
			"finding answerable commands, so every count below is over a corpus that "+
			"is not there (#42 pins the suite by SHA)", len(files))
	}
	sort.Strings(files)
	return files
}

// scorableCommands counts the commands in a script that the run loop scores — the
// capability predicate, in one place.
//
// Derived from Kind rather than from a list of heads, and deliberately *not* a
// hand-maintained set: when #53 teaches the harness `(module quote ...)`, a new Kind
// appears and this predicate widens on its own. A head list would have to be edited,
// and the edit is exactly what gets forgotten.
func scorableCommands(s *Script) int {
	n := 0
	for _, c := range s.Commands {
		if c.Kind != KindUnsupported {
			n++
		}
	}
	return n
}

func decode(image []byte) error {
	_, err := binary.DecodeModule(image)
	return err
}

// readText is the wat entry point the board scores: the lexer, and now the module-field
// grammar above it (#62).
//
// **What this does not do is still the reason the reject-direction column reads the way it
// does**, only one stratum further up. `text.ReadModule` lexes, then parses module fields
// and the type algebra, and stops at the first instruction — so a vector whose
// malformedness lives in an instruction body, in a validator (`alignment`, `type
// mismatch`), or in name resolution (`unknown func`) is still a *fail*, in a named bucket,
// and not a skip. That is the bucketed-failures discipline: what remains is the work plan
// for #63/#64 and the validator, not a debt hidden behind a fourth verdict.
//
// It is called on the raw source and not on a pre-lexed token slice on purpose: ReadModule
// owns the lex-then-parse ordering (see cursor's header for why the ordering is load-bearing),
// and a harness that lexed first would be reimplementing that ordering in a second place.
func readText(src []byte) error { return text.ReadModule(src) }

// assemble is the build-mode half of the same front end: `spec.AssembleFunc`, feeding the
// `assert_invalid (module quote ...)` arm the image it must hand to the validator.
//
// **Separate from readText because a measurement said so, and the measurement's own domain is the
// lesson.** The first draft of the 17-head slice widened `ReadTextFunc` to return the image and
// pointed it here, which put the three Kinds that arm serves through the emitter. The board
// answered: `text 0 -> 58`, and the buckets named the cause — the emitter could not then write
// `(table ...)` or `(start ...)` fields, or #77's symbolic locals (#8). **All three have since closed
// (#413, #419, #77), so the 58 is a spent figure and #423 carries the re-measurement**; the tense is
// corrected here rather than the sentence deleted, because the paragraph below is about the probe's
// domain and that lesson does not expire with the number.
//
// It was pre-measured and the measurement still missed it, which is the part worth keeping. The
// probe compared `ReadModule` against `EncodeModule` over `KindModuleQuote` and
// `KindAssertMalformedText` and found **zero** disagreement across 1236 commands — while the arm
// serves a *third* Kind, `KindModuleText`, whose 1119 bare bodies were not in the domain at all.
// Coverage is a claim: an instrument's domain is an assertion it cannot check about itself, and
// this one asserted "every command this function sees" over 1236 of 2355.
//
// The zero was vacuous besides, and decomposing it is what said so: 1229 of the 1236 fail inside
// the shared `parseModule` before the encoder is reached, so seven modules and 146 bytes were the
// entire subject of the agreement. Recorded as grave #329.
func assemble(src []byte) ([]byte, error) { return text.EncodeModule(src) }

// isGated asks the engine, rather than reading its error text. The taxonomy is
// the engine's to define; a substring test here would be the harness guessing at
// the thing it exists to check.
func isGated(err error) bool { return errors.Is(err, binary.ErrFeatureDisabled) }

// isTrap is the same delegation for traps (#157): `errors.As` against the engine's own trap
// type, so an `assert_trap` action is judged on what the error *is* rather than on what it
// says. This is the one place in the board that legitimately knows both type systems, which
// is why the predicate is injected instead of the harness testing for a `trap: ` prefix.
//
// **A wrapped trap still answers yes**, which is deliberate and is what `errors.As` buys over
// a type assertion: an arm that annotates a trap on the way out — as the bulk arms' callers
// may — must not thereby turn a pass into a fail. The Reason text is what the substring match
// reads, and a wrapper adds to it rather than replacing it.
func isTrap(err error) bool {
	var t *interp.Trap
	return errors.As(err, &t)
}

// isException is isTrap's own delegation for exception handling's outcome — `errors.As` against
// `interp.Uncaught`, the third control-transfer type 0022 names (module-invalid layering debt,
// trap, exception), exported from `internal/interp` for exactly this boundary crossing the way
// `*interp.Trap` already is.
func isException(err error) bool {
	var u *interp.Uncaught
	return errors.As(err, &u)
}

// instantiate is the interpreter's module entry point: it turns a scored module command into
// something invoke can be called on.
//
// **It decodes a second time, and that is not waste.** The module arm has already asked
// `decode` whether the image is well-formed — that is the *verdict*, and its answer is a
// board number. This call wants the decoded module itself, which DecodeFunc deliberately
// does not return: `func([]byte) error` is the shape it is because a harness holding a
// `*binary.Module` would be a harness that imports the engine's representation. So the
// duplication buys the neutrality, and it buys it at a cost the board can afford (one extra
// decode per module command, ~2100 of them).
//
// The text path re-encodes rather than re-reading: `text.ReadModule` is error-only by design
// (0011), so EncodeModule is the only path from wat source to an image. That is the same
// second call the board's readText already made, for the same reason.
// **Its `registry` parameter is the whole of the linking wire-up on this side**, and it is why
// this is a LinkedInstantiateFunc rather than the plain kind: the harness owns the name→instance
// map and this function owns the conversion into `interp.Imports`, being the one place that
// legitimately knows both `spec.Instance` and `*interp.Instance`. Same seam `invoke` cuts for
// values, for the same reason.
func instantiateLinked(c Command, reg Registry) (Instance, Stratum, error) {
	return instantiateWith(binary.DefaultFeatures(), c, reg)
}

// imports turns the harness's registry into the engine's resolver.
//
// **The type assertion's failure is a refusal, not a panic**, which matters because it is the
// one place a foreign Instance could reach the engine: a registry entry the harness put there
// is always ours, but the map crosses a public boundary, and answering "no such export" for a
// thing that is not an instance is the honest reading — the import genuinely cannot be
// satisfied from it. A panic here would convert a caller's mistake into a harness crash mid-board.
func imports(reg Registry) interp.Imports {
	return func(module, name string) (interp.Extern, bool) {
		in, ok := reg.Instances[module].(*interp.Instance)
		if !ok {
			return interp.Extern{}, false
		}
		return in.Export(name)
	}
}

// declinedImport reports whether this module imports from a name whose registration the engine
// declined, as an error that answers yes to isGated — decision 0037, issue #366.
//
// **Why this is here and not in the harness.** The harness owns the fact (`reg.Gated`) and owns the
// classification (`isGated`), but the question is *which names does this module import from*, and
// answering it needs a decoder. `internal/spec` does not have one and must not acquire one, so the
// check lands on the side of the seam that already decoded the image two lines up. Nothing about
// the verdict is decided here: this returns an error, and the run loop's own gate paths — the same
// ones that handle a decoder decline — decide what it means.
//
// **After the decode rather than off `c.Source`**, which is the version that would have been wrong:
// deriving import names from the s-expression is blind to `(module binary "…")`, and
// `linking{1,2,3}.wast`'s 9 rows are exactly that form. Acting at resolution time covers all three
// module forms for free.
//
// **It also converted five passes into gates, and those five are grave #408.** `imports.wast`
// :136/:295/:440/:538 and `linking3.wast`:14 assert that a module *lacks one export* and expect
// `unknown import`; they passed because the whole target module was unbound, which produces the same
// string about a different fact. A substring match cannot tell the two apart, so the reject-direction
// instruments were all satisfied — the only thing that saw it was 0037 pre-registering a `pass`
// column it expected not to move.
//
// **The message names the module and the import, not just the feature**, because its whole job is to
// stop a reader chasing the wrong component: `unknown import: "test" "func"` sent them to the
// linker, which was working correctly. `%w` on the engine's own sentinel is the verdict channel;
// the sentence is testimony.
func declinedImport(m *binary.Module, reg Registry) error {
	for _, im := range m.Imports {
		if reg.Gated[im.Module] {
			return fmt.Errorf("import %q %q names module %q, whose registration was declined: %w",
				im.Module, im.Name, im.Module, binary.ErrFeatureDisabled)
		}
	}
	return nil
}

// instantiateWith is instantiate under a stated gate set.
//
// **The gate set has to reach this path, and the all-gates-on lane is what proved it.** The
// lane replaces Engine.Decode and nothing else, so while this function called
// `binary.DecodeModule` — the default-features helper — a module reaching the interpreter
// through instantiation was decoded with every gate *off* no matter what the lane asked for.
// 17 memory64 vectors were declined in the lane whose defining property is that nothing is
// declined: `Gated must be 0` failed, naming the two files, which is the structural bound
// working exactly as decision 0010's ruling says it must. A per-vector allowlist would have
// absorbed them silently.
//
// Note which instrument found it. TestGatedVectors keys on `decode(c.Module)` and cannot see
// this path at all — a text module has no `c.Module` — so the per-vector control was blind by
// construction and the *structural* one was not. That is the argument for the all-on lane
// restated by measurement rather than by claim.
// **The registry threads through rather than being read here** for the reason above: this
// function is about gates, and linking is a second axis. A caller that has no registry passes
// an empty map, which is not a special case — it is the accurate statement that nothing has
// been registered yet, and `interp.InstantiateLinked` under a resolver that answers no to
// everything is exactly the pre-0017 behaviour it replaces.
func instantiateWith(f binary.Features, c Command, reg Registry) (Instance, Stratum, error) {
	return instantiateWithGate(f, c, reg, true)
}

// instantiateWithGate is instantiateWith with decision 0037's declined-import gate switchable.
//
// **The switch exists for one caller and it is a control, not a mode.** `TestGatedVectors` demands
// every gated line be named, and the 71 lines this gate creates share one cause; establishing
// *which* lines those are by enumerating them would pin today's population and inherit today's
// blind spot. So the control derives the set the way a witness set is always derived here — neuter
// the line and read the board — by running each file a second time through this entry point with
// the gate off, and taking the difference. Nothing on the board's own path passes false.
func instantiateWithGate(f binary.Features, c Command, reg Registry, declinedImportGate bool) (Instance, Stratum, error) {
	image := c.Module
	stratum := StratumBinary
	if c.Kind != KindModuleBinary {
		// **StratumEncode, not StratumText.** The module arm already asked ReadModule and
		// scored its answer; this is EncodeModule, a different entry point with a different
		// frontier, and charging its 13775 unemitted instruction bodies to the reader's
		// column would raise a ceiling that is 0 and destroy the only instrument watching
		// the reader for regressions.
		stratum = StratumEncode
		img, err := text.EncodeModule(c.Source)
		if err != nil {
			return nil, stratum, err
		}
		image = img
		// Past the encoder, a failure is the decoder's — reading its own output.
		stratum = StratumBinary
	}
	m, err := (&binary.Decoder{Features: f}).DecodeModule(image)
	if err != nil {
		return nil, stratum, err
	}
	// **A trap here is a verdict, not a failure to instantiate** (0015). Instantiation is
	// execution at time zero, so an active data segment copied out of bounds makes this a
	// module that came to life and died doing it — which is exactly what `data1.wast`'s 14
	// `assert_trap`-wrapping-a-module vectors assert. Charged to StratumExec because the
	// interpreter is the component that produced it.
	// **The link failure is a third channel, and it is neither of the other two** (0015): a
	// module whose import cannot be resolved never came to life, so it is not a trap, and the
	// image was well-formed, so it is not the decoder's. Charged to StratumExec because the
	// interpreter is the component that reported it, and reported verbatim because
	// `assert_unlinkable` matches on the text.
	if derr := declinedImport(m, reg); declinedImportGate && derr != nil {
		// **A gate decline reported one command downstream, which is decision 0037.** The
		// harness cannot make this call: reading an import section needs a decoder, and this is
		// the side that has one. It is the same seam `imports` above cuts — the conversion
		// happens in the one place that knows both type systems — and the classification stays
		// the harness's, since the error answers yes to `isGated` and the arms' existing gate
		// paths do the rest.
		//
		// **StratumExec and not StratumBinary, and the first draft got it wrong in a way the
		// stratum ledger caught.** Nothing about *this* module was declined — a module it imports
		// from was — so this is not the decoder refusing an image, it is the link step reporting
		// that a dependency is missing for a stated reason, and a link failure is the
		// interpreter's phase by the same argument the trap and link channels below are charged
		// there. Charging it to the decoder put **13 rows** through the module arms'
		// two-decode-paths-disagree branch and broke a ceiling of 0.
		//
		// **The tempting fix was to exclude gate declines from that branch, and it would have
		// deleted a deliberate assertion.** The branch's own comment says a gate decline is *not*
		// excluded because one path declining on a gate the other honours is the lane-override
		// defect `allOnLane` twice nearly shipped. So the branch was right and the stratum was
		// wrong: a declined *import* is not a disagreement about decoding, and saying so with the
		// stratum is the fix that keeps the check's teeth.
		return nil, StratumExec, derr
	}
	in, trap, err := interp.InstantiateLinked(m, imports(reg))
	if err != nil {
		return nil, StratumExec, err
	}
	if trap != nil {
		return nil, StratumExec, trap
	}
	// **A nil trap is not "instantiation completed".** Instantiation can fall short without
	// trapping — an active data segment whose target memory is imported cannot be copied, and
	// "there is no linker" is neither a runtime event nor a verdict (0015), so it comes back on
	// this channel. Quoting it here is what makes `data1.wast`'s :80/:117/:136 read as *linking
	// is missing* instead of as "the module instantiated without trapping", which was true and
	// named no component. Charged to StratumExec, the layer that reported it.
	//
	// The instance is discarded rather than returned alongside the error, because a partially
	// initialized memory is exactly the thing a following assert_return must not read: it would
	// compute a confident answer over bytes that were never copied.
	if err := in.Deferred(); err != nil {
		return nil, StratumExec, err
	}
	return in, StratumUnset, nil
}

// validateModule is the validator's entry point: wat source → image → module → type check.
//
// **Three steps, and the Stratum it returns says which one refused** — the reason ValidateFunc
// returns one rather than letting the arm derive it. Only the third is `StratumValidate`; the
// first two are the harness reading its own output, and a failure there is not evidence about
// the type checker.
//
// **Both pre-validation steps are charged to StratumEncode, including the decoder's.** The
// decoder is refusing an image `text.EncodeModule` produced, so the two candidate causes are a
// decoder defect and an encoder that emitted something invalid, and the second is far likelier —
// the encoder is the younger component and it is being driven by the corpus's *invalid* modules,
// the one population nothing else exercises. Charging it to StratumBinary would raise a ceiling
// that is 0 and blind the only instrument watching the decoder, which is `instantiateWith`'s own
// argument one component over. The number is 0 today; it was 17 until the laneidx grave was
// fixed, and every one of those was the encoder.
//
// It does not re-use `instantiateWith`: that function instantiates, which is the step after the
// one being scored, and an invalid module must not reach the interpreter at all — that is the
// condition 0025's carve-out names and the reason this package exists.
func validateModule(c Command) (Stratum, error) {
	return validateWith(binary.DefaultFeatures(), c)
}

// validateWith is validateModule under a stated gate set.
//
// **It is split for the reason instantiateWith is split, and the grave that argument records
// was about to be re-earned here.** A lane that swaps `Engine.Decode` for an all-gates-on
// decoder and leaves this entry point alone would decode every `assert_invalid` module with the
// default features — so a vector for a gated proposal would be *declined* inside the lane whose
// defining property is that nothing is declined, and `Gated must be 0` would fail naming the
// proposal rather than a defect. That is memory64's 17 vectors again, one component over, and it
// is why the gate set is a parameter rather than a call to the default helper.
func validateWith(f binary.Features, c Command) (Stratum, error) {
	// **A pre-assembled image is used as given.** The binary form has no wat source at all, and the
	// quote form's arm assembles once and hands the image forward — so `Module` set means the
	// caller has already established that this module comes into being, and re-deriving it here
	// would be the double work #296 names *and* a second chance to disagree with the caller's own
	// fact 1. The text form leaves `Module` nil and takes the branch below, unchanged.
	image := c.Module
	if len(image) == 0 {
		var err error
		image, err = text.EncodeModule(c.Source)
		if err != nil {
			return StratumEncode, err
		}
	}
	m, err := (&binary.Decoder{Features: f}).DecodeModule(image)
	if err != nil {
		return StratumEncode, err
	}
	if _, err := validate.Module(m); err != nil {
		return StratumValidate, err
	}
	return StratumValidate, nil
}

// isDeclined is isGated's delegation for the validator's own third verdict: `errors.Is` against
// `validate.ErrUnsupported`, the sentinel slice 1 returns for an instruction whose rules belong to
// a later slice.
//
// A sentinel test rather than a text test, for isTrap's reason — the taxonomy is the engine's —
// and a *separate* predicate from isGated because the two populations drain by different
// mechanisms: a gated vector converts when Scott flips a gate, a declined one when the next slice
// lands. See Engine.IsDeclined for why the arm asks this before matching the expected string.
func isDeclined(err error) bool { return errors.Is(err, validate.ErrUnsupported) }

// invoke is the interpreter's call entry point, and it is where the two value models meet.
//
// This function is the *one* legitimate place that knows both `spec.Val` and
// `interp.Value` — the glue the ValKind doc comment names. The conversion is a bit-pattern
// copy plus a type-tag map in both directions and nothing else: no float arithmetic, no
// re-parsing, no NaN normalization. Anything cleverer here would be the harness recomputing
// a value it was handed, which is how a comparator starts agreeing with itself.
//
// **The reference half is a Class/Null/RefID mapping rather than a bit-pattern copy
// (#196/#197)**, because neither Val nor Value hold a reference's representation as a bit
// pattern at all — both types say so in their own doc comments — so the "no cleverness" rule
// above is honored by this function doing exactly the same kind of copy for the reference
// fields that it already does for Bits, not by forcing a reference through Bits to keep one
// code path.
func invoke(in Instance, name string, args []Val) ([]Val, error) {
	inst, ok := in.(*interp.Instance)
	if !ok {
		return nil, fmt.Errorf("instance is %T, not *interp.Instance", in)
	}
	vs := make([]interp.Value, len(args))
	for i, a := range args {
		v, ok := toInterpValue(a)
		if !ok {
			return nil, fmt.Errorf("argument %d is %s, which has no interp.Value", i, a)
		}
		vs[i] = v
	}
	out, err := inst.Invoke(name, vs...)
	if err != nil {
		return nil, err
	}
	res := make([]Val, len(out))
	for i, o := range out {
		v, ok := fromInterpValue(o)
		if !ok {
			// A result type the harness cannot name. Reported rather than mapped to a
			// default, because a silent coercion here would make every v128 result
			// compare as an i32 and the mismatch bucket would name the wrong defect.
			return nil, fmt.Errorf("result %d has type %v, which the harness cannot represent", i, o.Type)
		}
		res[i] = v
	}
	return res, nil
}

// readGlobal is the interpreter's `get` entry point — `invoke`'s counterpart for the script
// grammar's other action (#323).
//
// **The same one conversion, deliberately reused rather than re-derived.** `fromInterpValue` is
// the whole of what crosses here, and it is the identical call `invoke` makes on its results,
// because a global's value and a call's result are the same fact at this boundary. A second
// mapping written for this path would be a second answer to "what is this value", which is the
// grave `invoke`'s own comment cites (#78/#105/#106) and the reason the ValKind glue is one place.
//
// The unrepresentable case is reported rather than defaulted, for `invoke`'s stated reason: a
// silent coercion would make a `v128` global compare as an `i32`, and the mismatch bucket would
// then name the wrong defect — with the aggravation that a `(get …)` vector has *one* result, so
// the wrong reading would be the whole of what the vector said.
func readGlobal(in Instance, name string) (Val, error) {
	inst, ok := in.(*interp.Instance)
	if !ok {
		return Val{}, fmt.Errorf("instance is %T, not *interp.Instance", in)
	}
	out, err := inst.Global(name)
	if err != nil {
		return Val{}, err
	}
	v, ok := fromInterpValue(out)
	if !ok {
		return Val{}, fmt.Errorf("global %q has type %v, which the harness cannot represent", name, out.Type)
	}
	return v, nil
}

// valType and valKind are the two directions of the value-type map, written as explicit
// switches over both enums rather than as an arithmetic offset.
//
// A `binary.ValType(0x7f - k)` trick would work today and silently break the day either enum
// gains a member — the enumerated-literal defect in its most tempting form, since the two
// orderings genuinely do correspond right now. Both functions report `ok` rather than
// defaulting, so an unmappable type is a named failure at the boundary.
//
// **Widened to the two reference kinds, since #196/#197** — the type-tag half of a reference
// Val/Value *is* a ValType-shaped fact (which of FuncRef/ExternRef), even though the rest of a
// reference's representation (Null, Extern/RefID) is not; toInterpValue/fromInterpValue below
// call this for that one fact and handle the rest themselves, rather than this pair growing a
// second copy of the Class dispatch.
func valType(k ValKind) (binary.ValType, bool) {
	switch k {
	case KindI32:
		return binary.I32, true
	case KindI64:
		return binary.I64, true
	case KindF32:
		return binary.F32, true
	case KindF64:
		return binary.F64, true
	case KindFuncRef:
		return binary.FuncRef, true
	case KindExternRef:
		return binary.ExternRef, true
	case KindV128:
		return binary.V128, true
	case KindAnyRef:
		// #270/0039's widening, and the one row here that is not a named `binary` constant: `anyref`
		// has no `binary.AnyRef` and should not acquire one, because `AbstractRefType` already admits
		// all twelve abstract heaptypes and a thirteenth named constant beside it would be a second
		// registry for the same fact (which `TestValTypeNamedConstantsAreNotAlias` polices).
		return anyRefType, true
	case KindUnnameableRef:
		// **The refusal that keeps a sentinel out of the engine.** This Kind exists because
		// `valKind` could not name a type; handing it back a type here would invent the answer that
		// function declined to give, and the invented type would then be checked against a real
		// signature. `toInterpValue` has no other route to a `binary.ValType`, so refusing here
		// refuses every argument path at once — which is the sentence `valKind`'s default arm has
		// always carried ("still a refusal on the argument side"), stated where a linter can see it
		// rather than left to hold because of who calls whom.
		//
		// An arm rather than the post-switch return, so `exhaustive` confirms this member has a
		// stated reading — the same reason `width` uses a `default` and `String` names every case.
		return binary.NoValType, false
	}
	return binary.NoValType, false
}

// anyRefType is `anyref` as the decoder names it, resolved once because `valKind` switches on a
// ValType value and needs a comparable one.
//
// **Panics at init rather than reporting, and the choice is the same one `byteKinds` makes**: a
// harness that could not name `anyref` would decline #270's whole population and report it as
// `unsupported` — a wrong board rather than a loud failure — and `HeapAny` is one of the twelve
// heaptypes `AbstractRefType` is documented to admit, so a false here is the decoder having changed
// under the harness.
var anyRefType = func() binary.ValType {
	t, ok := binary.AbstractRefType(binary.HeapAny, true)
	if !ok {
		panic("spec: binary.AbstractRefType rejected HeapAny — the harness cannot name anyref")
	}
	return t
}()

func valKind(t binary.ValType) (ValKind, bool) {
	switch t {
	case binary.I32:
		return KindI32, true
	case binary.I64:
		return KindI64, true
	case binary.F32:
		return KindF32, true
	case binary.F64:
		return KindF64, true
	case binary.FuncRef:
		return KindFuncRef, true
	case binary.ExternRef:
		return KindExternRef, true
	case binary.V128:
		// **Widened, since decision 0024's forced question 5** — this comment used to name
		// V128 among the types "the harness cannot name," and that was true until now. See
		// toInterpValue/fromInterpValue for the Lanes<->Hi/Bits crossing this Kind enables.
		return KindV128, true
	case anyRefType:
		// #270/0039. Note what this row does **not** do: it names `anyref` and nothing else, so
		// `eqref`, `i31ref`, `structref`, `arrayref`, `exnref` and every `(ref $t)` still reach the
		// default below. That is deliberate — a Kind per heaptype would be twelve members whose only
		// consumer is a gate this harness wants inert, where one Kind plus a *payload* answers the
		// question `assert_ref_pat` actually asks (see ValKind's own comment on why KindAnyRef exists).
		return KindAnyRef, true
	default:
		// Every other GC reference form: types the *harness* cannot name (see ValKind's own scope
		// comment). Reported as `false` so the caller says so rather than coercing — a silent
		// map to KindI32 would make every such result compare as an i32 and bucket the wrong
		// defect.
		//
		// **On the result side this is no longer a refusal**, since 0039: `fromInterpValue` catches
		// the `!ok` and substitutes KindUnnameableRef, exactly as grave #266 already did for a null.
		// (KindAnyRef until ADR 0040, whose only reason was the Kind gate that decision deleted.) It
		// is still a refusal on the **argument** side, where a placeholder type would be handed to
		// the engine and checked against a real signature — and `valType` refuses the sentinel by
		// name, so that sentence is now checkable rather than a claim about who calls what.
		return 0, false
	}
}

// packV128Lanes packs a v128 Val's shaped Lanes (readV128Const's own output) into the raw
// (hi, lo) pair interp.Value carries — the argument-side crossing's own inverse of
// sliceV128Lanes (value.go), which reads the identical bit layout back apart for comparison.
// Byte layout matches interp's own `lanesToV128`: low-numbered lane in the low bits, low half
// of the v128 filled before the high half.
//
// Uses each lane's own `.LaneBits` (the wire width), never `.Kind.width()` — an i8x16/i16x8
// lane widens to KindI32 for storage (Val.Lanes's own doc comment) and `.Kind.width()` would
// report 32 for it, packing sixteen i8 lanes into 512 bits of a 128-bit vector. LaneBits's own
// doc comment has the fuller account of the bug this replaced.
func packV128Lanes(lanes []Val) (hi, lo uint64) {
	bitOff := uint(0)
	for _, lane := range lanes {
		w := lane.LaneBits
		v := lane.Bits & mask64(w)
		if bitOff < 64 {
			lo |= v << bitOff
			if bitOff+w > 64 {
				hi |= v >> (64 - bitOff)
			}
		} else {
			hi |= v << (bitOff - 64)
		}
		bitOff += w
	}
	return hi, lo
}

// toInterpValue converts a Val to an interp.Value, dispatching on whether Kind is a reference
// kind — the reference half of invoke's glue, kept as its own function because the mapping is a
// real branch (Class chooses among three shapes) rather than a field-for-field copy the way the
// numeric half is.
//
// **Only RefLiteralNull and RefExternIdentity convert; RefTypePattern and AnyNull report
// false** — `isPassable`'s own rule, checked again here as a second, narrower opinion at the
// one function that actually builds an interp.Value, per the falsifiability law's own
// "breaking the assertion" reasoning: a caller that reached here after somehow bypassing
// isPassable's check gets a named failure instead of a silently wrong Value.
//
// The two refuse for **different** reasons, and this comment used to give one reason for both.
// RefTypePattern is an expectation-only *predicate* — `(ref.func)` names any value of a shape,
// so there is no value to pass. A bare `(ref.null)` names exactly one value and is perfectly
// concrete; what it lacks is a **heaptype**, and `interp.NullRef(t)` needs one. Precision matters
// here because the wrong reason is what let the AnyNull arm go missing below: a "predicate" reads
// as a shape the switch already excludes, where "a null with no type to give it" reads as the
// special case it is.
//
// **This function and `isPassable` are two predicates, not one asserted twice, and they genuinely
// disagree in one place** — `RefConcrete`. isPassable admits it (nothing about the *grammar* makes
// it an expectation), and this function refuses it (it is fromInterpValue's own result-only shape,
// with no argument spelling to have come from). So the second opinion cannot be discharged by
// calling isPassable here: that would collapse a real distinction, and the disagreement at
// RefConcrete is what proves the distinction is real rather than stylistic. The two are kept in
// sync instead by TestToInterpValueRefusesEveryUnpassableShape, whose expectations are authored
// from the semantics rather than read out of either function — a control derived from one of two
// things it is comparing is the echo grave (#106).
func toInterpValue(a Val) (interp.Value, bool) {
	if a.NaN != NaNNone {
		// A NaN *class* names a set of bit patterns, and its Bits field is 0 — so this arm's
		// absence meant `nan:canonical` converted to a perfectly plausible `f32 0.0` and was
		// handed to the engine as an argument. The reference-shaped half of this defence existed
		// and the numeric half did not (grave #266's sweep, found by deriving the control's shapes
		// from isPassable instead of listing them).
		return interp.Value{}, false
	}
	if a.Kind == KindV128 {
		for _, lane := range a.Lanes {
			if lane.NaN != NaNNone {
				// The same refusal one level down: packV128Lanes reads `lane.Bits`, which is 0 for
				// a NaN class, so a v128 with one NaN lane would pack a zero into that lane's
				// position and pass it as a concrete vector. isPassable's own v128 arm refuses
				// this; that arm's comment says the corpus writes no such argument today, which
				// is what kept the gap unobservable rather than what closed it.
				return interp.Value{}, false
			}
		}
		hi, lo := packV128Lanes(a.Lanes)
		return interp.Value{Type: binary.V128, Bits: lo, Hi: hi}, true
	}
	if !a.Kind.isRef() {
		t, ok := valType(a.Kind)
		if !ok {
			return interp.Value{}, false
		}
		return interp.Value{Type: t, Bits: a.Bits}, true
	}
	t, ok := valType(a.Kind)
	if !ok {
		return interp.Value{}, false
	}
	switch a.Class {
	case RefLiteralNull:
		if a.AnyNull {
			// **The second opinion this function's doc comment promises, which did not exist.**
			// A bare `(ref.null)` carries Class RefLiteralNull, so it fell straight through to
			// the line below and became `interp.NullRef(binary.FuncRef)` — a concrete funcref
			// null built from the *placeholder* Kind readRefConst assigns a Val that names no
			// heaptype at all (see Val.AnyNull). Exactly the "silently wrong Value" the comment
			// above says a caller bypassing isPassable is protected from, for one of the two
			// shapes it names by name.
			//
			// Latent rather than live: both `Command.Args` sites in wast.go filter on
			// `isPassable` first, so nothing reaches here with this shape today. Fixed anyway,
			// because a defence documented and absent is worse than one never claimed — the
			// reader who checks the code against the comment finds agreement. Found by the sweep
			// grave #266 obliges, and it is #266's own shape (a claim asserting a property the
			// code lacks), which is why it is recorded there rather than as a new issue.
			return interp.Value{}, false
		}
		return interp.NullRef(t), true
	case RefExternIdentity:
		// **Two constructors for one Class, chosen by Kind** — the argument side of the distinction
		// `Val.String` prints and `readRefConst` reads. `(ref.extern N)` is an *externalized* host
		// reference and `(ref.host N)` is a bare one (`parser.mly:1502` against `:1501`), and passing
		// the bare one through `interp.ExternRef` would set `Externalized`, make `typeOfRef` report
		// `extern`, and fail `extern.wast:42`'s `anyref` parameter on a type mismatch the corpus does
		// not contain. That vector is why KindAnyRef exists at all.
		if a.Kind == KindExternRef {
			return interp.ExternRef(a.Extern), true
		}
		return interp.HostRef(t, a.Extern), true
	case RefNone, RefTypePattern, RefConcrete:
		// RefNone is unreachable given a.Kind.isRef(). RefTypePattern is the
		// expectation-only shape this function's own doc comment excludes. RefConcrete is
		// result-only (fromInterpValue's own construction) and never appears as an
		// argument. All three named explicitly so `exhaustive` confirms every RefClass
		// member has a stated reading here.
	}
	return interp.Value{}, false
}

// fromInterpValue converts an interp.Value to a Val, the reverse direction of toInterpValue.
//
// Every reference interp.Value converts: `Null` and `RefID` are always meaningful once Type is
// known (interp.Value's own doc comment), unlike the harness-argument direction above where a
// predicate shape has no interp.Value to become. So there is no third arm here mirroring
// RefTypePattern/AnyNull — a *result* is always a concrete value, never a pattern, and the
// pattern-matching happens entirely on the expectation side, in Val.Matches.
//
// **A null converts even when Type does not** (grave #266, the `!ok` arm below), and **since 0039 so
// does a non-null one.** The paragraph here used to read: *"What is left refusing is a non-null
// reference of a type the harness cannot name — a non-null `anyref` or `(ref $t)` — and that refusal
// is honest rather than pending: such a result would need a heaptype-bearing expectation shape to be
// compared against, and the harness deliberately has none yet."* That was accurate and it was
// **half** of #270's wall: the want side could not spell `(ref.array)` and this side could not build
// a got for it either, so the population declined twice for two unrelated reasons and fixing one
// would have moved nothing. `Val.Pat` is the expectation shape that paragraph said did not exist;
// `Val.Payload` is what this function now carries to meet it.
//
// **Nothing is refused here any more, and that is a real narrowing rather than a coercion.** What
// this paragraph used to add — that the placeholder Kind is *unobservable*, both sides of every such
// row being KindAnyRef so the `want.Kind != got.Kind` gate was inert — was the gate's property and
// not the Kind's, and ADR 0040 (#441) removed the gate. The Kind is observable now: `Val.String`
// reads it on the RefExternIdentity path and printed a fabricated `ref.host N` there (#450) for
// exactly as long as it was called KindAnyRef. It is KindUnnameableRef, a sentinel, and what makes
// the substitution safe is no longer that nothing looks — it is that everything which looks has an
// arm for it. The **argument** direction still refuses, and `valType` now says so in code rather
// than by way of the call graph — it is `toInterpValue`'s only route to a `binary.ValType`, so one
// arm there refuses every argument path: a type that came from a refusal is the one thing that must
// not reach the engine.
func fromInterpValue(o interp.Value) (Val, bool) {
	k, ok := valKind(o.Type)
	if !ok {
		if o.Null {
			// **A null needs no nameable type, because it has none** (grave #266). `valKind`
			// refuses every GC reference form — the harness's two reference Kinds cannot name
			// `anyref`, `eqref`, `(ref null $t)` — and refusing here made the *value* unrepresentable
			// on the strength of a *type* nothing downstream reads. In the reference there is one
			// null reference value, nullary and heaptype-free (`runtime/value.ml:20`, `:112`), so a
			// null `anyref` result and a null `funcref` result are the same value and neither needs
			// `o.Type` to be expressible.
			//
			// Kind is a placeholder, not a claim, and it is unobservable by construction: `Matches`
			// dispatches a null `got` on the pattern alone and never reads this Kind (value.go's own
			// #266 block), `String` renders a null as the reference does with no heaptype in it, and
			// a result never crosses back through `toInterpValue`. KindFuncRef specifically, for
			// consistency with the one other Val whose Kind is arbitrary for the same reason — the
			// bare `(ref.null)` expectation, see AnyNull. The two places that could make this Kind
			// load-bearing again are pinned by TestRefNullMatchesAcrossTwoHeaptypes.
			return Val{Kind: KindFuncRef, Class: RefLiteralNull}, true
		}
		// A **non-null** reference of a type the harness cannot name: `structref`, `arrayref`,
		// `exnref`, `(ref $t)` and the rest. Sentinel Kind for #266's reason one step further on —
		// the value is expressible even though its type is not, because what the oracle reads is the
		// constructor and Payload carries that faithfully.
		//
		// **KindUnnameableRef, and it used to be KindAnyRef "load-bearing where #266's was
		// arbitrary".** That sentence was true and its subject is gone: what it was load-bearing
		// *for* was making `Matches`' Kind gate agree instead of refusing 22 of #270's 28 rows on a
		// placeholder, and ADR 0040 deleted the gate. A constant whose only recorded reason has been
		// deleted is the next reader's invitation to infer a new one, so it is a sentinel now — see
		// KindUnnameableRef's own comment for what reads it and what refuses it.
		//
		// Note the direction of the fall-through: `o.Type` is discarded and `o.RefKind` is kept, which
		// is the whole of 0039 in one line. The type was the thing the harness could not name; the
		// constructor is the thing the oracle asks about.
		return refVal(KindUnnameableRef, o), true
	}
	if !k.isRef() {
		// Hi is meaningful only for KindV128 (Val's own doc comment) and zero for every other
		// numeric kind, since interp.Value.Hi is itself only ever set for a V128 result — this
		// one line is what makes fromInterpValue's existing shared arm correct for the widened
		// Kind rather than needing a KindV128 branch of its own.
		return Val{Kind: k, Bits: o.Bits, Hi: o.Hi}, true
	}
	if o.Null {
		return Val{Kind: k, Class: RefLiteralNull}, true
	}
	return refVal(k, o), true
}

// refVal builds the Val for a **non-null** reference result at the given (possibly placeholder)
// Kind — the one place `interp.Value`'s reference half becomes a Val's, called from both of
// fromInterpValue's non-null paths so the nameable and unnameable types cannot drift apart.
//
// Split out rather than inlined twice for exactly that reason: the two paths differ only in where
// their Kind came from, and the version of this code that predated 0039 had the identity logic on the
// nameable path only — so an unnameable type's host identity would have been dropped had that path
// been reachable at all.
func refVal(k ValKind, o interp.Value) Val {
	payload, ok := interpPayloads[o.RefKind]
	if !ok {
		// An engine payload kind the harness has no member for. **A Val rather than a refusal**,
		// because the harness's job here is to report what it got: PayloadNone makes every pattern
		// refuse (`RefPat.admits`), so the vector fails and names the value, where a refusal would
		// score it `unsupported` and read as a vector nobody had reached. The board's own
		// `TestInterpPayloadsCoverTheEngineVocabulary` is what makes this loud instead of quiet.
		payload = PayloadNone
	}
	if payload == PayloadHost {
		// A host identity, `(ref.extern N)` when Kind says it was externalized and `(ref.host N)`
		// when it was not — one Class, spelled by Kind, per Val.String and readRefConst.
		//
		// **The guard is the payload kind and used to be `k == KindExternRef && o.IsHost`**, whose
		// first conjunct is now redundant and was never the real question. `externalize-i`
		// (`extern.wast:29-31`) returns an externref wrapping an i31, a struct or an array; those have
		// no identity and `interp.Value.RefID` is 0 for all three, so an unguarded arm converted them
		// to `(ref.extern 0)` — a host identity belonging to a *different* reference in the same file
		// (`:37`). `:46-49` expect the bare `(ref.extern)` pattern, which admits any non-null
		// externref, so **both readings scored green on every vector in the corpus** and only the
		// accept-direction argument distinguished them. Since 0039 the wrapped payload's own kind
		// survives, so those three results are RefConcrete carrying `struct`/`array`/`i31` rather than
		// RefConcrete carrying nothing — which is what lets `extern.wast:53-55` be *asked*.
		return Val{Kind: k, Class: RefExternIdentity, Extern: o.RefID, Payload: payload}
	}
	// RefConcrete: "some non-null value", which is all a Class can say — and since 0039 it is no
	// longer all the *Val* can say, because Payload carries the constructor the oracle dispatches on.
	return Val{Kind: k, Class: RefConcrete, Payload: payload}
}

// interpPayloads maps the engine's payload vocabulary onto the harness's.
//
// **A table, and in this direction only.** The harness reads results and never constructs an engine
// payload kind — `toInterpValue` builds a reference through `interp.NullRef`/`ExternRef`/`HostRef`,
// which set the kind themselves — so there is no reverse map to compute, and inventing one would be a
// second registry with no consumer.
//
// A map rather than a switch because the two enums live in different packages and the sentinel that
// bounds the engine's is `interp.PayloadPastEnd`: a switch would have to name it to satisfy
// `exhaustive`, and naming a bound as a case reads as treating it as a kind. The map's coverage is
// checked instead, against a domain derived from that same bound —
// `TestInterpPayloadsCoverTheEngineVocabulary`, which is Scott's condition on the 0039 stamp for this
// boundary.
var interpPayloads = map[interp.RefPayload]RefPayload{
	interp.PayloadNone:   PayloadNone,
	interp.PayloadHost:   PayloadHost,
	interp.PayloadI31:    PayloadI31,
	interp.PayloadStruct: PayloadStruct,
	interp.PayloadArray:  PayloadArray,
	interp.PayloadFunc:   PayloadFunc,
	interp.PayloadExn:    PayloadExn,
}

// engine is the board's engine description, in one place so that every board test scores
// against the same set of components. A test that wants a narrower engine builds its own
// Engine literal, which is visible at the call site rather than hidden in a positional
// argument.
//
// **It supplies InstantiateLinked and leaves Instantiate nil, deliberately — one spelling per
// engine.** `instantiateRaw` prefers the linked func when both are set, so supplying both would
// leave a field that reads as live and is never called, and a lane or test that then overrode
// only that field would change nothing while appearing to change the gates. That is not
// hypothetical: it is the bug `instantiateWith`'s comment records, and the two-field version of
// this literal would have reintroduced it in a new place. Engine keeps both fields because a
// caller without a linker is a real configuration (sexpr_test's stubs are one); the board is not
// that caller.
func engine() Engine {
	return Engine{
		Decode: decode, ReadText: readText, Assemble: assemble, IsGated: isGated, IsTrap: isTrap,
		IsException: isException,
		Validate:    validateModule, IsDeclined: isDeclined,
		Invoke: invoke, ReadGlobal: readGlobal, InstantiateLinked: instantiateLinked,
	}
}

// run scores a script with gate declines separated from verdicts.
func run(s *Script) *Result { return s.RunGated(engine()) }

// requireSuite gates every board test on the corpus actually being there.
//
// License: local dev on a clone where `make spec-tests` has not been run.
// Revoked by BURROUGHS_NO_SKIP=1.
//
// It asserts a file *count*, not `os.Stat` on the directory as it once did: a
// partial or empty fetch passes an existence check and then produces a board over
// whatever happened to be present, which is a number with an unasserted input.
func requireSuite(t *testing.T) {
	t.Helper()
	testenv.RequireSuite(t, suiteDir)
}

// suitePin reports the suite revision the board's counts were measured against.
//
// Read from the *fetch script's pin* rather than from `git -C testdata/spec rev-parse`, and
// the difference is the point: the script's `rev` is what the corpus is *supposed* to be, and
// the script asserts on every path that the checkout matches it (#42). So quoting the pin
// quotes an already-verified fact, where shelling out to git would make the board's provenance
// depend on a second, unasserted reading — and would make the test invoke git at all, which a
// board test has no business doing.
//
// Failure is fatal rather than a blank: *a provenance header that says nothing is worse than
// none, because it looks stamped* (gen.PinnedRev's own reason).
func suitePin(t *testing.T) string {
	t.Helper()
	rev, err := gen.PinnedSuiteRev()
	if err != nil {
		t.Fatalf("reading the suite pin: %v\n\tThe board cannot name the corpus it measured, "+
			"which makes every count below unattributable.", err)
	}
	// And the pin is checked against the checkout it claims to describe, because the two
	// can disagree: the script asserts them equal *when it runs*, and nothing stops a later
	// `git -C testdata/spec checkout` from moving the tree underneath it. Printing the pin
	// without this check would quote what the corpus is supposed to be while the counts came
	// from whatever is actually there — a stamped provenance that is wrong exactly when it
	// matters, which is the hearsay #42 is about.
	//
	// **Resolved with git rather than by reading .git/HEAD**, and the first draft did the
	// latter: on a branch checkout HEAD holds a *symbolic ref* (`ref: refs/heads/utf8-names`,
	// which is what the vendored corpus actually has), so the comparison put a ref name
	// against a SHA and failed for every developer whose corpus was not detached. A false
	// alarm on the board's own provenance line — and the fix is not more parsing, it is
	// *measure with the instrument*: `rev-parse` is the thing that knows how to resolve a
	// ref, and reimplementing it here would be a second opinion about git's storage format.
	//
	// The earlier version of this comment argued a board test "has no business" invoking
	// git. That was wrong, and it is quoted rather than deleted: verifying what a working
	// tree is at *is* a git question, and answering it with a file read was the reasoning
	// that produced the false alarm.
	//
	// A tree that is not a git checkout at all (a tarball) is unverifiable rather than
	// violated, so the board says which of the two it is instead of implying either.
	out, err := exec.Command("git", "-C", suiteDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Logf("note: cannot resolve %s to a revision (%v), so the pin %s is unverified "+
			"against the tree", suiteDir, err, rev)
		return rev
	}
	if got := strings.TrimSpace(string(out)); got != rev {
		t.Errorf("suite pin is %s but %s is checked out at %s.\n\tThe board would name a "+
			"corpus it did not measure. Run `make spec-tests` to return to the pin, or bump "+
			"the pin deliberately.", rev, suiteDir, got)
	}
	return rev
}

// suitePaths returns every vendored .wast file, having already required the
// corpus. An empty result here is impossible rather than skippable — requireSuite
// asserted the count — so it is a Fatal: the two disagreeing would mean the glob
// and the assertion are looking at different things.
//
// Through testenv.SuitePaths rather than its own glob, per #340: the population is one
// definition or it is several, and this selector feeds the board itself (boardFiles), so a
// local glob here is the one that decides what the board is computed over.
func suitePaths(t *testing.T) []string {
	t.Helper()
	requireSuite(t)
	paths, err := testenv.SuitePaths(suiteDir)
	if err != nil || len(paths) == 0 {
		t.Fatalf("SuitePaths %s after requireSuite passed: %d paths, err=%v", suiteDir, len(paths), err)
	}
	return paths
}

// TestBinaryWast is the first real suite number: binary.wast is 107
// assert_malformed forms and nothing else, so phase 1 can execute all of it.
//
// This test reports; it does not gate. Failures are expected while the decoder
// is incomplete, and their buckets are the work plan (issues #5, #6). A hard
// pass-count floor guards against regression without pretending the suite is
// green.
func TestBinaryWast(t *testing.T) {
	requireSuite(t)
	s, err := ParseFile(filepath.Join(suiteDir, "binary.wast"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := run(s)
	t.Log("\n" + r.Board())

	if r.Unsupported != 0 {
		t.Errorf("binary.wast should be fully parseable by phase 1, got %d unsupported", r.Unsupported)
	}
	if r.Total() == 0 {
		t.Fatal("no assertions executed — harness is not wired")
	}
	// Regression floor, raised as decoder work lands; never lowered. 49 when the
	// harness first ran, 84 after section order and cross-section counts (#6), 104
	// after the section payload grammars (#5), 114 after the opcode table (#41), and
	// 127 — the whole file — after the instruction and function-body grammars
	// (#43/#39, with #22 closing inside).
	//
	// At the file's total the floor and the total coincide, so the inequality below can
	// no longer distinguish "held" from "improved". That is fine for a *floor*, and the
	// equality is asserted separately rather than left implied: a file that is fully
	// green has a stronger property than a floor, and stating the weaker one only would
	// let a vector go missing from the file unnoticed.
	const floor = 127
	if r.Pass < floor {
		t.Errorf("pass count %d fell below floor %d", r.Pass, floor)
	}
	if r.Fail != 0 {
		t.Errorf("binary.wast is fully green at %d/%d and must stay so; %d failing:\n%s",
			floor, r.Total(), r.Fail, r.Board())
	}
	if r.Total() != floor {
		t.Errorf("binary.wast scored %d vectors, want %d — the floor is the file's total, so a "+
			"changed count means the corpus moved and the floor is measuring a different file "+
			"than the one it was set against (#42 pins the suite by SHA)", r.Total(), floor)
	}
}

// TestClosedBuckets pins buckets that have reached zero. A bucket going to zero
// is a PR's measure of done (docs/laws/boards-and-buckets.md), and this is what stops it from quietly
// refilling: the floor above catches a net regression, but a bucket can refill
// while the total holds if another one drains at the same time.
//
// Entries are added only when the bucket is actually empty, and *because the grammar
// answers them* — a bucket emptied by declining to score its vectors is not closed, it
// is hidden. binary_leb128_64.wast is the live example and has no entry here: its one
// "integer too large" vector is `gated` under the default lane (memory64 off), so the
// bucket reads zero on a board that never asked the question. TestGatedVectors and the
// all-gates-on lane are what own that case; a closed-bucket entry would be the third
// verdict laundering itself into a green.
//
// Note what is *not* here from #5: "unexpected end of section or function" and
// "section size mismatch" both drained substantially (9 → 6, 8 → 5) without
// reaching zero, because their remainder needs the code, global, and element
// grammars. A partially-drained bucket earns no entry; that is the difference
// between this test and the pass-count floor.
func TestClosedBuckets(t *testing.T) {
	requireSuite(t)
	closed := map[string][]string{
		"binary.wast": {
			"unexpected content after last section",                 // #6, was 23
			"function and code section have inconsistent lengths",   // #6, was 4
			"data count and data section have inconsistent lengths", // #6, was 3
			"malformed section id",                                  // #6, was 5
			"malformed limits flags",                                // #5, was 7
			"malformed import kind",                                 // #5, was 6
			"length out of bounds",                                  // #5, was 1

			// The instruction and function-body grammars (#43/#39), which took the file
			// to 127/127. Every one of these is a *mechanism* that landed, not a vector
			// that was argued away, and the counts are the base measured at 9569cb7:
			"illegal opcode ff",                     // #43, was 1 — the one oracle-covered rendering
			"illegal opcode",                        // #43, was 1 — binary.wast:345, the elem-segment byte
			"data count section required",           // #22, was 2 — closed inside #39, four opcodes from free.ml
			"too many locals",                       // #39, was 2 — the sum, checked at 64 bits
			"END opcode expected",                   // #39, was 1 — `end_ s` on a byte that is not END
			"unexpected end of section or function", // #39, was 3 — the deferred const verdict's half
			"section size mismatch",                 // #39, was 1 — `sized` wraps each *body*
			"integer too large",                     // #39, was 2 — a locals count's own LEB width
		},
		"custom.wast": {
			"function and code section have inconsistent lengths",
			"data count and data section have inconsistent lengths",
			"malformed section id",
			"unexpected end", // #5, was 2 — the custom section's name-inside-its-extent rule
		},

		// The utf8-*.wast files are single-bucket by construction: 176 vectors each,
		// every one expecting "malformed UTF-8 encoding". Closing all three at once
		// is what a general rule looks like on the board — one predicate, three
		// name positions (#26).
		"utf8-import-module.wast":     {"malformed UTF-8 encoding"},
		"utf8-import-field.wast":      {"malformed UTF-8 encoding"},
		"utf8-custom-section-id.wast": {"malformed UTF-8 encoding"},
	}
	// The keys are a fifth file list, so pin them against the derived board corpus. A
	// closed bucket in a file the board does not score is a regression control
	// watching a number nobody reports.
	onBoard := make(map[string]bool)
	for _, f := range boardFiles(t) {
		onBoard[f] = true
	}
	for file := range closed {
		if !onBoard[file] {
			t.Errorf("%s has closed buckets but is not on the board; nothing scores it", file)
		}
	}

	for file, keys := range closed {
		s, err := ParseFile(filepath.Join(suiteDir, file))
		if err != nil {
			t.Errorf("%s: parse: %v", file, err)
			continue
		}
		r := run(s)
		for _, k := range keys {
			if got := len(r.Buckets[k]); got != 0 {
				t.Errorf("%s: bucket %q refilled to %d; it was closed and must stay closed", file, k, got)
				for _, f := range r.Buckets[k] {
					t.Logf("  line %d: got %q", f.Line, f.Got)
				}
			}
		}
	}
}

// wholeFileGated is TestGatedVectors's bulk allowance: file → exact Gated count, for a file
// whose *entire* gated population shares one reason (verified per file against the decoder,
// not assumed from the count alone — see the loop's own comment at its one call site).
//
// **Drained from 61 entries to 6, #227/ADR 0025's SIMD flip.** The harness v128 widening
// (decision 0024's forced question 5) had moved 24115 lines across 58 SIMD files from
// Unsupported straight into Gated in one PR, every one for the reason `simd: feature gate
// disabled` while SIMD stayed off by default. With `DefaultFeatures` now setting `SIMD: true`,
// every one of those 55 SIMD-only files measures Gated=0 (confirmed by running the harness over
// each entry, not assumed from the flip alone) and is removed. The 6 that remain are
// **relaxed-SIMD** content (the `fd 0x100..0x12f` window, a *separate* still-off gate,
// `RelaxedSIMD`) — confirmed by reading each file: every one is entirely `*.relaxed_*`
// instructions, so the SIMD flip does not touch their gated population at all, measured
// unchanged at the identical counts.
//
// **+32 across the same six, the `(either …)` widening (0028 d4).** `readResult` makes the
// reference's `EitherResult` askable in expected-result position, so each `assert_return` carrying
// one stops being refused by the reader and reaches the decoder, where the still-off `RelaxedSIMD`
// gate declines it. The per-file deltas are **+1 / +3 / +2 / +6 / +8 / +12**, and they are exactly
// each file's count of `(either` occurrences — a 1:1 attribution *per file*, which is what this
// control asked for by demanding "single out any line whose reason is no longer the file's one
// stated reason". The reason is unchanged for every one of the 32: `relaxed-simd: feature gate
// disabled`, the same reason the file's existing population carries, which is why these stay bulk
// entries rather than being singled out.
//
// `i32x4_relaxed_trunc.wast` is absent from this map and stays absent: it is eight lines with no
// assertions at all, so it has no gated population to allow — the corpus gap that is 0028 d5's
// whole reason for the author-supplied witnesses in `internal/interp/simd_relaxed_test.go`.
//
// **And its absence is structural rather than observed, which is the difference between this map
// being a domain and being a registry of whatever happened to decline.** Six keys for seven
// relaxed files invites exactly that reading, so the seventh is settled by measurement: the file
// holds **one** command, the `module text` at `:3`, and it scores `pass` with **every gate off**
// (the zero value, the most hostile configuration there is) — because #124 rules the text front
// end gate-blind, so the module arm scores `ReadModule`'s answer and carries any instantiation
// decline forward to the vectors that asked the question. This file has no such vectors. No gate
// can therefore reach a scorable command here, and a zero entry for it would be an assertion
// that cannot fail — the decoration this same commit removed from the empty-map case, re-added
// under a different name.
//
// What protects the *domain* is not this map at all: the per-line arm below requires every
// decline anywhere on the board to be named, so a file gaining one errors there whether or not
// it was ever listed. That check is derived from the space (all 254 files, all declines) and
// needs no registry, which is why the map is free to be what it is — pins on counts, witnessing
// the flip rather than surviving it. (Question: Scott, on the PR #285 stamp.)
//
// **Zeroed, not drained, by the relaxed-SIMD flip — and the difference is the whole point.**
// With `DefaultFeatures` now setting `RelaxedSIMD: true`, each of those six files measures
// Gated=0, confirmed by running the harness over each entry rather than inferred from the flip.
// Their 69 declines are 69 passes.
//
// The obvious edit was to delete the six keys, and it was wrong. **An empty allowance agrees
// with every gated population there is** — the bulk path below (`if n, ok :=
// wholeFileGated[f]; ok`) matches no file, so nothing exercises it, and a later edit that broke
// the whole-file mechanism outright would leave every board green. That is the
// comparison-against-an-empty-set shape arriving inside the mechanism this project built to
// enumerate declines, and it is *worse* than a deleted control, because the board reads a
// vacuous green as a verdict. It is also the standard this same commit applied to `passFloor`:
// a bound left entirely inside its own tolerance is decoration. An empty allowance is a bound
// entirely inside its tolerance. (Ruling: Scott, on the PR #285 stamp conditions.)
//
// So **the value 0 is the assertion, not the absence of one.** Each entry now claims "this
// file's gated population is exactly zero", checked on the nose by the same comparison that
// used to check 2 and 24 — which means a regression that started declining relaxed vectors
// again reports `Gated is 2, want 0` instead of passing quietly through a per-line path whose
// both loops are vacuous. The flip's own board delta is pinned here as a consequence: these six
// zeros are 69 of it.
//
// The mechanism's **nonzero** branch is certified by `TestWholeFileGatedCountIsAsserted` below,
// against a *constructed* homogeneous population rather than a suite file — which closes #284's
// remaining scope. The domain of the bulk path is "a file whose entire gated population shares
// one reason", not "one of the six relaxed-SIMD files", so the certification is written from the
// domain: if the last real entry going to 0 could silence it, the control was scoped to the
// sample and not to the space.
var wholeFileGated = map[string]int{
	"i16x8_relaxed_q15mulr_s.wast": 0, // 2 pre-flip
	"i8x16_relaxed_swizzle.wast":   0, // 5 pre-flip
	"relaxed_dot_product.wast":     0, // 10 pre-flip
	"relaxed_laneselect.wast":      0, // 11 pre-flip
	"relaxed_madd_nmadd.wast":      0, // 17 pre-flip
	"relaxed_min_max.wast":         0, // 24 pre-flip — the six sum to the flip's 69
}

// gatedAssertInvalid is TestGatedVectors's **second** bulk allowance, and it is keyed by Kind
// where wholeFileGated is keyed by file: for each file, the exact number of `assert_invalid`
// commands the decoder declines for a disabled feature gate.
//
// **463 across 40 files, all arriving in one PR with #9's first validator slice.** The
// `assert_invalid` arm made a population the harness had never scored askable, and the first
// thing that happens to a vector for a gated proposal is the decoder declining it — so 463
// commands moved from Unsupported straight into Gated, exactly as decision 0024's v128 widening
// moved 24115. That is the situation wholeFileGated was invented for, one axis over: writing 463
// per-line entries would restate `<gate>: feature gate disabled` 463 times, and *a reason
// repeated verbatim at that scale is testimony nobody could review by reading it*.
//
// **Keyed by Kind rather than by file, because these files are not wholly gated.** `func.wast`
// has one gated `assert_invalid` and 200-odd other commands that pass; `ref_eq.wast` has six and
// a per-line entry elsewhere. wholeFileGated's whole-population comparison cannot express that,
// and widening it to "some of this file" would destroy the property that makes it a bound. So
// the population is named by the thing it has in common — the Kind — and the per-line arm keeps
// policing every decline outside it. One concept, two keys.
//
// The gates are named per entry, measured through the run loop rather than read off the file:
// most files are homogeneous, seven mix two proposals, and every one of the 463 is a
// `feature gate disabled` decline. A file whose mix changes moves its own count, which is what
// the on-the-nose comparison at the call site still catches.
//
// What protects the *domain* is not this map: the per-line arm requires every decline anywhere
// on the board to be named, and the call site errors on any file with a bulk population and no
// entry here. So a new gated `assert_invalid` cannot arrive unnoticed whether or not its file is
// listed — which is the same argument wholeFileGated's comment makes about its own absent
// seventh file, and the reason this map is free to be pins on counts.
//
// **465 after the 17-head slice, and the +2 is a fact about this control's own domain rather than
// about the gates.** Keying the population "by the Kind" was written when `assert_invalid` had
// exactly one Kind, so the membership test read `c.Kind == KindAssertInvalid` — and when the slice
// split the head into three (`KindAssertInvalidBinary`, `KindAssertInvalidQuote`), that equality
// silently narrowed from *the population* to *one form of it*. It failed in the loud direction,
// which is the only reason this was cheap: the two vectors fell out of the bulk arm and into the
// per-line arm, which demanded them by name. Had they fallen the other way they would have been
// counted and never named. The predicate is now `Kind.isAssertInvalid()`, which is the property
// the prose above always claimed — *a guard's trigger predicate is its own claim about the space*,
// and this one had been true when written and quietly stopped being.
//
// The error message the failure printed was also wrong, and worth recording because following it
// would have hidden the defect: it said to add the lines to `allowed` with the feature named,
// which would have been a per-line entry papering over a bulk-arm predicate that had stopped
// matching. A bucket names where a symptom surfaces, not where the defect lives (#194). Recorded as grave #330.
//
// # 471 across 41 files after #419, and the +6 is the second legitimate cause the sum's own message
// # names — the harness learning to *ask*
//
// The emitter learned the `(table …)` field's initializer, so `assert_invalid` vectors whose table
// spells one now reach the decoder instead of stopping at `cannot yet encode`. The decoder declines
// the `0x40` form without GC (decision 0008), so each arrives here. Six rows, every one identified by
// line rather than inferred from a count moving:
//
//	table.wast:54,58,62,66   6 → 10  the four whose table field carries an initializer —
//	                                 `(i32.const 0)`, `(ref.null extern)`, `(ref.null func)` ×2
//	elem.wast:516            1 → 2   `(table 1 (ref func) (ref.func 0))` in text, beside the
//	                                 `(module binary …)` twin at :524 that was already here
//	global.wast:674          new     `(table $t 10 funcref (global.get $g))`, the vector whose
//	                                 expected `unknown global` is #419's own reject direction
//
// **The three that did not move are the check that this is the right cause.** `table.wast`'s other
// six (:70, :74, :78, :119, :127, :135) are the *plain* form with a non-nullable element type — no
// initializer to encode — and they were gated on main and are gated now, unchanged. So the delta is
// exactly the population whose encoding changed, which a total moving by 6 could not have said.
var gatedAssertInvalid = map[string]int{
	// memory64 — the whole of `align64`/`load64`/`memory_*64`, plus the mixed files below.
	"align64.wast":          37,
	"load64.wast":           46,
	"memory64.wast":         5,
	"memory_copy64.wast":    63,
	"memory_fill64.wast":    63,
	"memory_init64.wast":    65,
	"table64.wast":          2,
	"table_copy_mixed.wast": 3,
	// GC — the `gc` gate alone.
	"array.wast":            6,
	"array_copy.wast":       4,
	"array_fill.wast":       3,
	"array_init_data.wast":  2,
	"array_init_elem.wast":  3,
	"br_if.wast":            1,
	"br_on_cast.wast":       6,
	"br_on_cast_fail.wast":  6,
	"br_on_non_null.wast":   1,
	"br_on_null.wast":       1,
	"elem.wast":             2, // gc — the 0x40 table form at :516 and a `(module binary …)` twin at :524
	"func.wast":             1,
	"global.wast":           1, // gc — the 0x40 table form at :674, `(table $t 10 funcref (global.get $g))`
	"local_init.wast":       4,
	"local_tee.wast":        1,
	"ref.wast":              12,
	"ref_as_non_null.wast":  1,
	"select.wast":           1,
	"struct.wast":           4,
	"table.wast":            10, // gc — 6 plain `(ref …)` element types, 4 spelled initializers
	"type-equivalence.wast": 1,
	"type-rec.wast":         10,
	"type-subtyping.wast":   36,
	// Tail calls and typed function references, alone or mixed with gc.
	"call_ref.wast":             4,  // call_ref, return_call_ref
	"return_call.wast":          12, // return_call ×11 + gc ×1
	"return_call_indirect.wast": 16, // return_call_indirect ×15 + gc ×1
	"return_call_ref.wast":      11, // call_ref, return_call_ref ×4 + gc ×7
	"ref_eq.wast":               6,  // gc ×4 + ref.eq ×2
	"unreached-invalid.wast":    3,  // call_ref, return_call_ref ×2 + ref.as_non_null etc ×1
	// Exception handling.
	"exports.wast":   1,
	"tag.wast":       2,
	"throw.wast":     3, // exception handling ×2 + throw ×1
	"throw_ref.wast": 2,
	"try_table.wast": 9, // gc ×5 + try_table ×3 + exception handling ×1
	// Multi-memory, and the only entry here whose file is otherwise ungated.
	"align.wast": 1, // multi-memory: `memarg flags bit 6, an explicit memory index`
}

// gatedDeclinedRegistration is TestGatedVectors's **third** bulk allowance, and it is keyed by
// the gate's *cause* where wholeFileGated is keyed by file and gatedAssertInvalid by Kind: for
// each file, the exact number of lines gated because the command's module imports from a module
// name whose `(register …)` was itself gate-declined (decision 0037, issue #366).
//
// **71 across 7 files, all arriving in one PR, all one sentence.** Before 0037 these were not
// gated at all: the registry carried no gate state, so the name stayed unbound and the command
// failed with `unknown import` — 62 of the exec column's 81 fails, plus 4 with a different message
// and the same cause, plus 5 that *passed* on a message that happened to match (grave #408). One
// reason repeated 71 times is the situation the two allowances above were invented for.
//
// **Membership is derived, not enumerated, and that is the part worth reading.** A hand-written
// line list would pin today's population and inherit today's blind spot, which is the defect
// `gatedAssertInvalid`'s own predicate drifted into (grave #330). So the call site establishes the
// set by *neutering the mechanism*: it runs each file a second time with 0037's gate switched off
// (`instantiateWithGate`) and takes the difference between the two gated line sets. What the gate
// creates is exactly what the difference names, across every arm, including ones nobody thought to
// look in.
//
// **The counts are still what polices it**, and they have to be, because the derivation uses the
// mechanism it is policing: an over-gating 0037 would enlarge both the population and the derived
// set, and the exclusion would absorb it silently. Slack 0 per file closes that — any change in
// what this gate declines moves a number here and fails. The derivation's job is only to keep the
// per-line arm from demanding 71 names for one fact; the bound's job is to notice the 72nd.
var gatedDeclinedRegistration = map[string]int{
	// `imports.wast`'s auxiliary module carries `(tag …)` fields, so it is declined under EH-off
	// and its `(register "test")` gates — one decline, 39 dependent lines.
	"imports.wast": 39,
	// `linking.wast`: `$Mm`/`$Mt` declined on the multi-memory memarg bit.
	"linking.wast":  13,
	"linking1.wast": 3,
	"linking2.wast": 8,
	"linking3.wast": 3,
	// memory64's own gate, one register down.
	"memory64-imports.wast": 4,
	// One `assert_unlinkable` whose tag import reaches a declined registration.
	"type-rec.wast": 1,
}

// wholeFileGatedVerdict is the bulk allowance's whole decision: whether `bulk` claims file `f`,
// and if so whether the count it claims matches the gated population actually measured.
//
// It is a function rather than four inline lines inside `TestGatedVectors` for one reason, and it
// is the reason #284 stayed open after the relaxed-SIMD flip: **every live entry is now 0, so the
// count-mismatch branch has no subject in the suite**. A control whose only exercise comes from
// the current sample inherits the current sample's blind spot, and the flip emptied exactly the
// part of the sample that exercised this. Extracting the decision lets it be driven from a
// constructed population — see `TestWholeFileGatedCountIsAsserted`.
//
// It returns the complaint instead of taking a `*testing.T` so the certification can read the
// verdict without a fake `testing.T` and without a second copy of the comparison. That matters
// more than it looks: *a control can test the helper and not the path*, so the real loop in
// `TestGatedVectors` calls this same function — the only thing it keeps for itself is `t.Error`.
func wholeFileGatedVerdict(bulk map[string]int, f string, gated int) (handled bool, complaint string) {
	n, ok := bulk[f]
	if !ok {
		return false, ""
	}
	if gated != n {
		return true, fmt.Sprintf("%s: Gated is %d, want %d (whole-file gate entry) — the file's "+
			"gated population moved; update wholeFileGated's count in this PR, and single out "+
			"any line whose reason is no longer the file's one stated reason", f, gated, n)
	}
	return true, ""
}

// TestWholeFileGatedCountIsAsserted certifies the bulk allowance's **nonzero** branch, which
// closes #284.
//
// The population is constructed, deliberately. #284's subject is the bulk path, whose domain is
// "a file with a homogeneous gated population" — a space, not a list — and after the
// relaxed-SIMD flip zeroed all six live entries the suite contains no member of it that would
// exercise a *count mismatch*. Certifying against the six would then certify nothing: the zero
// entries only prove the path notices a file that started declining, never that it notices a
// file whose count moved by 3.
//
// Both directions and the negative, because the three failures are different: an entry that
// never matches (the allowance silently stops consuming its file, and every line falls through
// to the per-line loop, which fails loudly — the benign direction); an entry that matches but
// stops comparing (a moved population passes, which is the whole defect the count exists for);
// and a file with no entry being claimed anyway (the allowance swallowing vectors nobody
// allowed, the worst of the three, since it is silent).
func TestWholeFileGatedCountIsAsserted(t *testing.T) {
	// A name no suite file has, so the case cannot accidentally be a real measurement.
	const synthetic = "zz-constructed-homogeneous.wast"
	bulk := map[string]int{synthetic: 3}

	t.Run("count agrees", func(t *testing.T) {
		handled, complaint := wholeFileGatedVerdict(bulk, synthetic, 3)
		if !handled {
			t.Errorf("handled = false for a file the allowance lists; the entry would not consume "+
				"its file and %q's lines would be judged by the per-line path instead", synthetic)
		}
		if complaint != "" {
			t.Errorf("complaint = %q, want none: 3 gated against an entry of 3 agrees", complaint)
		}
	})

	t.Run("count moved", func(t *testing.T) {
		// The nonzero branch itself: a homogeneous file that gained a gated line.
		handled, complaint := wholeFileGatedVerdict(bulk, synthetic, 4)
		if !handled {
			t.Fatal("handled = false for a listed file, so the mismatch below was never reached")
		}
		if complaint == "" {
			t.Fatal("complaint is empty for 4 gated against an entry of 3 — the bulk path accepted " +
				"a moved population, which is the exact drift the exact count exists to catch (#284)")
		}
		// The message is testimony and its numbers come from the inputs, so they get printed
		// for real inputs before being trusted: a complaint naming neither count would report
		// the right verdict while telling the reader nothing they could act on.
		for _, want := range []string{synthetic, "Gated is 4", "want 3"} {
			if !strings.Contains(complaint, want) {
				t.Errorf("complaint does not name %q: %s", want, complaint)
			}
		}
	})

	t.Run("unlisted file is not claimed", func(t *testing.T) {
		handled, complaint := wholeFileGatedVerdict(bulk, "zz-not-listed.wast", 7)
		if handled {
			t.Errorf("handled = true for a file with no entry, complaint %q — the bulk path would "+
				"consume a file nobody allowed and its declines would never reach the per-line "+
				"allowlist, which is the third verdict hiding in the control against hiding",
				complaint)
		}
		if complaint != "" {
			t.Errorf("complaint = %q for an unlisted file, want none", complaint)
		}
	})
}

// TestGatedVectors pins exactly which vectors the engine is allowed to decline.
//
// Result.Gated is a third verdict, and a third verdict is a way to make a board
// look better by moving failures into it. This is the control on that: the gated
// set is enumerated here, so a decline that appears anywhere else is a test
// failure rather than a quietly emptier board.
//
// Every entry needs a reason naming the gated feature, on the same principle as
// the deadcode allowlist (decision 0005): an unexplained allowlist entry is a
// suppression wearing a disguise.
func TestGatedVectors(t *testing.T) {
	requireSuite(t)

	// file → line → why it is gated.
	allowed := map[string]map[int]string{
		// Both vectors carry i64 memory limits flags (0x04), which is memory64.
		// With that gate off the decoder must reject them, and neither vector is
		// asking a question the engine can answer in that state.
		"binary_leb128_64.wast": {
			1:  "memory64: i64 limits flags on the memory section",
			16: "memory64: i64 limits flags on the memory section",
		},

		// Seven (module binary ...) forms carrying the function-references table form:
		// `\40\00\64\70\00\01\d2\00\0b` — the 0x40 prefix, the reserved zero, tabletype
		// `(ref func)` with limits [1..], and a `(ref.func 0)` initializer. Decision
		// 0008 folds function references into the GC gate, so with GC off the decoder
		// must decline, and the decline must be feature-named rather than
		// `malformed reference type` (#51 was exactly that violation).
		//
		// Verified by reading all seven, not by trusting that seven declines in one
		// bucket share one cause: each carries the byte-identical table entry. The gate
		// is right, so these are allowlisted rather than treated as over-gating.
		//
		// **These lines were `fail` an hour ago**, which is the point of the entry. They
		// were the board's accept-direction bucket — a valid module rejected — and the
		// fix converts them to an honest decline. They are simultaneously *passing* in
		// the all-gates-on lane (798, up from 791), so the parked verdict is earned
		// there rather than deferred everywhere: a decline that cannot become a
		// disappearance.
		"elem.wast": {
			453: "gc/function-references: the 0x40 table form with an initializer",
			470: "gc/function-references: the 0x40 table form with an initializer",
			487: "gc/function-references: the 0x40 table form with an initializer",
			504: "gc/function-references: the 0x40 table form with an initializer",
			544: "gc/function-references: the 0x40 table form with an initializer",
			561: "gc/function-references: the 0x40 table form with an initializer",
			578: "gc/function-references: the 0x40 table form with an initializer",
			// The four extended-const offsets, each now followed by the `assert_trap (invoke …)`
			// that runs against its module (#157) — see the section 9 / call_indirect batch
			// below for why the feature is the *offset expression's* and not the segment's.
			1065: "extended-const: (offset (i32.add …)) in an element segment at :1057",
			1066: "extended-const: an arithmetic constant expression at :1057 — the module this action runs against",
			1076: "extended-const: (offset (i32.add …)) in an element segment at :1068",
			1077: "extended-const: an arithmetic constant expression at :1068 — the module this action runs against",
			1087: "extended-const: (offset (i32.add …)) in an element segment at :1079",
			1088: "extended-const: an arithmetic constant expression at :1079 — the module this action runs against",
			1109: "extended-const: (offset (i32.add …)) in an element segment at :1092",
			1110: "extended-const: an arithmetic constant expression at :1092 — the module this action runs against",
		},

		// The board's last `fail` became a `gated`, which is the gates doctrine working
		// rather than a deferral bought cheaply: the vector is an `assert_malformed` on an
		// **array type's** field mutability byte, and an array type is GC's. With the gate
		// off the module is declined for the feature before the mutability byte is read,
		// so the engine never gets to the question the vector asks (#86).
		//
		// This is exactly the entry the all-gates-on lane exists to keep honest: with
		// `GC: true` the decline does not happen, the mutability check runs, and
		// `malformed mutability` is reported — so the vector is *passed* there, not parked.
		// TestAllGatesOnLeavesNothingGated is what proves that, and it is why a gated
		// verdict here cannot become a disappearance.
		"binary-gc.wast": {
			1: "gc: an array type's fieldtype, whose mutability byte is what the vector asserts",
		},

		// # The interpreter's arrival opened a second decline path, and these were its 17
		//
		// Every entry is an `assert_return` whose module carries an **i64 index type** —
		// `(memory i64 0)` at memory_grow64.wast:1, :36 and :48, `(table $t64 i64 0 externref)`
		// at table_grow64.wast:2 — which is memory64's defining feature. With the gate off the
		// decoder must reject the module, so the vector's question is never asked and `gated`
		// is the only honest verdict for it.
		//
		// **Two of those three line numbers were wrong until this edit**, and the correction is a
		// finding rather than a typo fix: the comment said ":1 and :34", which named one module
		// this file has and one it does not — line 34 is blank, the second `(memory i64 0)` is at
		// :36, and a *third* module at :48 was unmentioned because the memarg emitter had not yet
		// made its vectors reachable. A citation nobody re-resolves is a claim, and this one had
		// been read past in every review since #124.
		//
		// **They are here because the trigger could reach them, and it could not before.**
		// These declines happen at *instantiation*, on a wat module with no `c.Module` for the
		// old re-derived trigger to decode — so they were invisible to this allowlist while
		// being scored as **fails** one command downstream, in the interpreter's column, for a
		// feature decision that is not the interpreter's. Two defects with one cause; see the
		// GatedAt doc comment.
		//
		// Verified by reading the two module headers rather than by trusting that 17 declines
		// in two files share one cause. Both are passed in the all-gates-on lane, where the
		// memory64 gate is on and the vectors answer on the merits — so the parked verdict is
		// earned there rather than deferred everywhere.
		// # The memarg emitter's 114, which is the same mechanism widened by one instruction shape
		//
		// #8's memarg emitter taught the encoder to write load and store immediates, so 199 modules that used to
		// stop at `cannot yet encode` now reach the decoder — and 114 of their vectors reach a gate
		// there. **They are all `gated` for one of two features and neither is the encoder's**: a
		// module declaring `(memory i64 …)` is memory64, and a module with two or more memories
		// makes some memarg carry flags bit 6, which is multi-memory. The emitter is right in both
		// cases; the decoder is configured not to read what it correctly wrote.
		//
		// **The count is exactly the fail column's drop** — fail 14330 → 14216, gated 33 → 147 —
		// which is what makes this a verdict moving to its honest column rather than a board being
		// emptied. Every one is passed in the all-gates-on lane, where both gates are on and the
		// vectors answer on the merits.
		//
		// Verified by reading each module head rather than by trusting that 114 declines in eleven
		// files share two causes: the `module@` line is quoted per entry, and the two reasons were
		// separated by the error string the engine actually produced (`memory64: feature gate
		// disabled` versus `memarg flags bit 6 … decodeMemop`), not by the file's name — five of
		// these files are named `*64` and two of those carry *no* i64 memory.
		// # The data section's 810, which is the same mechanism widened by a whole module field
		//
		// Section 11 (#8) taught the emitter to write data segments and the data count section, so
		// **9082 modules that used to stop at `cannot yet encode` now reach the decoder** — and 810
		// of their vectors reach a gate there. As with the memarg emitter's 114 above, the emitter is
		// right and the decoder is configured not to read what it correctly wrote; three features,
		// none of them the encoder's, and each named from **the error string the engine produced**
		// rather than from the file's name.
		//
		// The column arithmetic is the honest part and it is not flattering: encode fail 13775 → 4693
		// is −9082, of which **8272 became exec fails and 810 became these declines. Zero became
		// passes.** That is not a drained board, it is a queue moving one layer down — the 8272 are
		// `interp: no arm for opcode 2d` and its 60 siblings, because a `(data …)` module that now
		// encodes still needs a load arm to answer an `assert_return`. Said plainly rather than
		// buried, because a section landing that produces no new pass is exactly the shape a Board
		// line is supposed to make visible.
		//
		// Verified by re-deriving each decline's cause from the run loop's own `GatedAt` — a
		// throwaway probe that walked the same commands and asserted **symmetric-difference zero**
		// against it per file, since a reason list built by a second traversal describes that
		// traversal until the two sets are proven identical.
		//
		// **Two of the counts written above were wrong, and that is a grave rather than a typo.**
		// `memory_trap0.wast:1` was documented as "five memories" and has three; `memory_fill0.wast:2`
		// as "four" and has three. Both numbers came from counting `(memory` in the source, where
		// `(memory.size $m)`, `(memory.grow …)` and `(data (memory 2) …)` spell those characters
		// without defining anything — so the reason stated a false fact about the module while
		// naming the right feature, and no reader re-derived it. The counts here are the *decoder's*
		// (`len(Memories)` plus the memory imports, all gates on), which is the index space the
		// flags bit is actually about. Measure with the instrument, not a regex (grave #129).
		// # The bare-invoke Kind's 74, which add no module and no feature
		//
		// #7's memory work taught the harness to run a top-level `(invoke …)` as a command
		// rather than to walk past it, so 74 commands that were never scored now reach the run
		// loop — and every one of them stands after a module the gates had **already** declined.
		// They add no file, no module and no feature: each of the 74 is spliced into a block
		// that existed, carrying the *same reason string as its own module's other vectors*,
		// which is what "no new feature" means concretely rather than as a claim.
		//
		// Four modules are the exception and they are the only entries here read from source
		// rather than inherited: `memory_copy64.wast:4863`, `:4875`, `:4887` and
		// `memory_init64.wast:203` are followed by an `(invoke "test")` and by nothing else, so
		// no sibling vector had ever put them in this list. All four declare `(memory i64 …)`.
		//
		// **The reason strings were inherited by module line, not retyped**, because the
		// alternative is 74 hand-copied claims about features and grave #129 is what retyping a
		// module fact costs. Verified in the direction that can fail silently: the pre-existing
		// 963 entries were extracted before and after and compared as sets — identical, +74 —
		// so this edit cannot have quietly changed a reason while adding lines. And the reverse
		// check below stayed silent throughout, which is the load-bearing negative: it says the
		// vectors these join are still declined, so the 74 are additions to a live list and not
		// a list going stale in place.
		// # The block family's 454, from 24 module heads no entry here had ever named
		//
		// #7's `block`/`loop`/`if`/`select` work landed on both sides at once — the encoder emits the
		// four structural opcodes and `select`'s two, the interpreter executes them — so **711 vectors
		// that used to stop short now reach a verdict**: 257 became passes and 454 reached a gate.
		// The columns close exactly, which is the check rather than the claim: fail 5349 → 4638 is
		// −711, pass 26307 → 26564 is +257, gated 1031 → 1485 is +454, and 257 + 454 = 711. Nothing
		// went missing and nothing was moved sideways into a quieter column.
		//
		// Three features, none of them this PR's: `memory64`'s 315, `simd`'s 102, and multi-memory's
		// 37. The engine is right in all three cases and configured not to read what it correctly
		// wrote; every one is answered on the merits in the all-gates-on lane.
		//
		// **None of the 454 could inherit a reason, which is what makes this batch different from the
		// 74 above.** They stand behind 24 module heads that had never appeared in this list at all —
		// a module whose vectors all used to fail at the encoder contributes no sibling to inherit
		// from — so each of the 24 reasons is read fresh. Read from the **decoder with every gate on**
		// and printed (`len(Memories)` plus memory imports, `Limits.Addr64`, and a count of body
		// instructions whose `Instr.Prefix` is 0xfd), never from the source text: grave #129 is what
		// counting `(memory` in a `.wast` file costs.
		//
		// That probe was wrong once, in the direction the discipline names. Its first version asked
		// `in.Op == 0xfd` and reported **v128instr=0** for all seven simd modules — a suspiciously
		// clean zero, and the tell was that it was *exactly* zero on files named for the feature. The
		// prefix does not live in `Op`; `Instr.Prefix` carries it (module.go:231), and the seven
		// modules hold 72 to 136 v128 instructions each. The instrument was blind, not the modules
		// empty.
		//
		// Verified the way the 74 were, and the verification found a second blind instrument. The
		// before/after extraction was first written as a regexp over the source lines and reported
		// **1030 → 1484**; the same extraction done over the **AST** reports **1031 → 1485**, because
		// `binary-gc.wast` and `memory-multi.wast` are hyphenated and the pattern's character class
		// was not. Both readings agreed on the delta (+454) and on the load-bearing negative — zero
		// pre-existing rows changed, zero removed, and the added key set is *identical* to the set
		// the control named — but a comparator that cannot see three of its rows is not the
		// comparator to certify an edit with, so the numbers quoted here are the AST's. Which also
		// re-dates the paragraph above: its "963 pre-existing entries" was the same undercount and is
		// **957** measured properly. Its conclusion stands; its figure was a regex's.
		//
		// The reverse check below stayed silent throughout — the vectors these join are still
		// declined, so this is a live list growing rather than a stale one being padded.
		"address0.wast": {
			105: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			106: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			107: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			108: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			109: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			111: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			112: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			113: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			114: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			115: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			117: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			118: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			119: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			120: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			121: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			123: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			124: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			125: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			126: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			127: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			129: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			130: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			131: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			132: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			133: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			135: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			136: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			137: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			138: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			139: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			141: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			142: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			143: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			144: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			145: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			147: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			148: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			149: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			150: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			151: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			153: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			154: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			155: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			156: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			157: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			159: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			160: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			161: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			162: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			163: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			165: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			166: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			167: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			168: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			169: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			171: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			172: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			173: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			174: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			175: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			177: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			178: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			179: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			180: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			181: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			183: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			184: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			185: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			186: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			187: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			189: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			190: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			191: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			192: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			193: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			195: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			196: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			197: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			198: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			199: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			200: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			202: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			203: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			204: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			205: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			206: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			208: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			209: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			210: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			211: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			212: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
		},
		"address1.wast": {
			146: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			147: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			148: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			149: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			150: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			152: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			153: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			154: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			155: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			156: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			158: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			159: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			160: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			161: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			162: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			164: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			165: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			166: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			167: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			168: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			170: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			171: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			172: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			173: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			174: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			176: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			177: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			178: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			179: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			180: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			182: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			183: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			184: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			185: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			186: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			188: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			189: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			190: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			191: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			192: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			194: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			195: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			196: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			197: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			198: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			200: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			201: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			202: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			203: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			204: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			206: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			207: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			208: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			209: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			210: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			212: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			213: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			214: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			215: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			216: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			218: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			219: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			220: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			221: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			222: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			224: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			225: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			226: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			227: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			228: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			230: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			231: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			232: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			233: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			234: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			236: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			237: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			238: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			239: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			240: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			242: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			243: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			244: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			245: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			246: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			248: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			249: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			250: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			251: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			252: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			254: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			255: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			256: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			257: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			258: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			260: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			261: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			262: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			263: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			264: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			266: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			267: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			268: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			269: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			270: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			272: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			273: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			274: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			275: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			276: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			277: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			278: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			280: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			281: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			282: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			283: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			284: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			285: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			286: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			288: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			289: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			290: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			291: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			292: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			293: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
			294: "multi-memory: a memarg carrying flags bit 6 at :3 — the module this action runs against",
		},
		"address64.wast": {
			104: "memory64: (memory i64 1) at :3 — an i64 index type",
			105: "memory64: (memory i64 1) at :3 — an i64 index type",
			106: "memory64: (memory i64 1) at :3 — an i64 index type",
			107: "memory64: (memory i64 1) at :3 — an i64 index type",
			108: "memory64: (memory i64 1) at :3 — an i64 index type",
			110: "memory64: (memory i64 1) at :3 — an i64 index type",
			111: "memory64: (memory i64 1) at :3 — an i64 index type",
			112: "memory64: (memory i64 1) at :3 — an i64 index type",
			113: "memory64: (memory i64 1) at :3 — an i64 index type",
			114: "memory64: (memory i64 1) at :3 — an i64 index type",
			116: "memory64: (memory i64 1) at :3 — an i64 index type",
			117: "memory64: (memory i64 1) at :3 — an i64 index type",
			118: "memory64: (memory i64 1) at :3 — an i64 index type",
			119: "memory64: (memory i64 1) at :3 — an i64 index type",
			120: "memory64: (memory i64 1) at :3 — an i64 index type",
			122: "memory64: (memory i64 1) at :3 — an i64 index type",
			123: "memory64: (memory i64 1) at :3 — an i64 index type",
			124: "memory64: (memory i64 1) at :3 — an i64 index type",
			125: "memory64: (memory i64 1) at :3 — an i64 index type",
			126: "memory64: (memory i64 1) at :3 — an i64 index type",
			128: "memory64: (memory i64 1) at :3 — an i64 index type",
			129: "memory64: (memory i64 1) at :3 — an i64 index type",
			130: "memory64: (memory i64 1) at :3 — an i64 index type",
			131: "memory64: (memory i64 1) at :3 — an i64 index type",
			132: "memory64: (memory i64 1) at :3 — an i64 index type",
			134: "memory64: (memory i64 1) at :3 — an i64 index type",
			135: "memory64: (memory i64 1) at :3 — an i64 index type",
			136: "memory64: (memory i64 1) at :3 — an i64 index type",
			137: "memory64: (memory i64 1) at :3 — an i64 index type",
			138: "memory64: (memory i64 1) at :3 — an i64 index type",
			140: "memory64: (memory i64 1) at :3 — an i64 index type",
			141: "memory64: (memory i64 1) at :3 — an i64 index type",
			142: "memory64: (memory i64 1) at :3 — an i64 index type",
			143: "memory64: (memory i64 1) at :3 — an i64 index type",
			144: "memory64: (memory i64 1) at :3 — an i64 index type",
			146: "memory64: (memory i64 1) at :3 — an i64 index type",
			147: "memory64: (memory i64 1) at :3 — an i64 index type",
			148: "memory64: (memory i64 1) at :3 — an i64 index type",
			149: "memory64: (memory i64 1) at :3 — an i64 index type",
			150: "memory64: (memory i64 1) at :3 — an i64 index type",
			152: "memory64: (memory i64 1) at :3 — an i64 index type",
			153: "memory64: (memory i64 1) at :3 — an i64 index type",
			154: "memory64: (memory i64 1) at :3 — an i64 index type",
			155: "memory64: (memory i64 1) at :3 — an i64 index type",
			156: "memory64: (memory i64 1) at :3 — an i64 index type",
			158: "memory64: (memory i64 1) at :3 — an i64 index type",
			159: "memory64: (memory i64 1) at :3 — an i64 index type",
			160: "memory64: (memory i64 1) at :3 — an i64 index type",
			161: "memory64: (memory i64 1) at :3 — an i64 index type",
			162: "memory64: (memory i64 1) at :3 — an i64 index type",
			164: "memory64: (memory i64 1) at :3 — an i64 index type",
			165: "memory64: (memory i64 1) at :3 — an i64 index type",
			166: "memory64: (memory i64 1) at :3 — an i64 index type",
			167: "memory64: (memory i64 1) at :3 — an i64 index type",
			168: "memory64: (memory i64 1) at :3 — an i64 index type",
			170: "memory64: (memory i64 1) at :3 — an i64 index type",
			171: "memory64: (memory i64 1) at :3 — an i64 index type",
			172: "memory64: (memory i64 1) at :3 — an i64 index type",
			173: "memory64: (memory i64 1) at :3 — an i64 index type",
			174: "memory64: (memory i64 1) at :3 — an i64 index type",
			176: "memory64: (memory i64 1) at :3 — an i64 index type",
			177: "memory64: (memory i64 1) at :3 — an i64 index type",
			178: "memory64: (memory i64 1) at :3 — an i64 index type",
			179: "memory64: (memory i64 1) at :3 — an i64 index type",
			180: "memory64: (memory i64 1) at :3 — an i64 index type",
			182: "memory64: (memory i64 1) at :3 — an i64 index type",
			183: "memory64: (memory i64 1) at :3 — an i64 index type",
			184: "memory64: (memory i64 1) at :3 — an i64 index type",
			185: "memory64: (memory i64 1) at :3 — an i64 index type",
			186: "memory64: (memory i64 1) at :3 — an i64 index type",
			188: "memory64: (memory i64 1) at :3 — an i64 index type",
			189: "memory64: (memory i64 1) at :3 — an i64 index type",
			190: "memory64: (memory i64 1) at :3 — an i64 index type",
			191: "memory64: (memory i64 1) at :3 — an i64 index type",
			192: "memory64: an i64 index type at :3 — the module this action runs against",
			194: "memory64: an i64 index type at :3 — the module this action runs against",
			195: "memory64: an i64 index type at :3 — the module this action runs against",
			196: "memory64: an i64 index type at :3 — the module this action runs against",
			197: "memory64: an i64 index type at :3 — the module this action runs against",
			198: "memory64: an i64 index type at :3 — the module this action runs against",
			200: "memory64: an i64 index type at :3 — the module this action runs against",
			201: "memory64: an i64 index type at :3 — the module this action runs against",
			202: "memory64: an i64 index type at :3 — the module this action runs against",
			203: "memory64: an i64 index type at :3 — the module this action runs against",
			204: "memory64: an i64 index type at :3 — the module this action runs against",
			348: "memory64: (memory i64 1) at :209 — an i64 index type",
			349: "memory64: (memory i64 1) at :209 — an i64 index type",
			350: "memory64: (memory i64 1) at :209 — an i64 index type",
			351: "memory64: (memory i64 1) at :209 — an i64 index type",
			352: "memory64: (memory i64 1) at :209 — an i64 index type",
			354: "memory64: (memory i64 1) at :209 — an i64 index type",
			355: "memory64: (memory i64 1) at :209 — an i64 index type",
			356: "memory64: (memory i64 1) at :209 — an i64 index type",
			357: "memory64: (memory i64 1) at :209 — an i64 index type",
			358: "memory64: (memory i64 1) at :209 — an i64 index type",
			360: "memory64: (memory i64 1) at :209 — an i64 index type",
			361: "memory64: (memory i64 1) at :209 — an i64 index type",
			362: "memory64: (memory i64 1) at :209 — an i64 index type",
			363: "memory64: (memory i64 1) at :209 — an i64 index type",
			364: "memory64: (memory i64 1) at :209 — an i64 index type",
			366: "memory64: (memory i64 1) at :209 — an i64 index type",
			367: "memory64: (memory i64 1) at :209 — an i64 index type",
			368: "memory64: (memory i64 1) at :209 — an i64 index type",
			369: "memory64: (memory i64 1) at :209 — an i64 index type",
			370: "memory64: (memory i64 1) at :209 — an i64 index type",
			372: "memory64: (memory i64 1) at :209 — an i64 index type",
			373: "memory64: (memory i64 1) at :209 — an i64 index type",
			374: "memory64: (memory i64 1) at :209 — an i64 index type",
			375: "memory64: (memory i64 1) at :209 — an i64 index type",
			376: "memory64: (memory i64 1) at :209 — an i64 index type",
			378: "memory64: (memory i64 1) at :209 — an i64 index type",
			379: "memory64: (memory i64 1) at :209 — an i64 index type",
			380: "memory64: (memory i64 1) at :209 — an i64 index type",
			381: "memory64: (memory i64 1) at :209 — an i64 index type",
			382: "memory64: (memory i64 1) at :209 — an i64 index type",
			384: "memory64: (memory i64 1) at :209 — an i64 index type",
			385: "memory64: (memory i64 1) at :209 — an i64 index type",
			386: "memory64: (memory i64 1) at :209 — an i64 index type",
			387: "memory64: (memory i64 1) at :209 — an i64 index type",
			388: "memory64: (memory i64 1) at :209 — an i64 index type",
			390: "memory64: (memory i64 1) at :209 — an i64 index type",
			391: "memory64: (memory i64 1) at :209 — an i64 index type",
			392: "memory64: (memory i64 1) at :209 — an i64 index type",
			393: "memory64: (memory i64 1) at :209 — an i64 index type",
			394: "memory64: (memory i64 1) at :209 — an i64 index type",
			396: "memory64: (memory i64 1) at :209 — an i64 index type",
			397: "memory64: (memory i64 1) at :209 — an i64 index type",
			398: "memory64: (memory i64 1) at :209 — an i64 index type",
			399: "memory64: (memory i64 1) at :209 — an i64 index type",
			400: "memory64: (memory i64 1) at :209 — an i64 index type",
			402: "memory64: (memory i64 1) at :209 — an i64 index type",
			403: "memory64: (memory i64 1) at :209 — an i64 index type",
			404: "memory64: (memory i64 1) at :209 — an i64 index type",
			405: "memory64: (memory i64 1) at :209 — an i64 index type",
			406: "memory64: (memory i64 1) at :209 — an i64 index type",
			408: "memory64: (memory i64 1) at :209 — an i64 index type",
			409: "memory64: (memory i64 1) at :209 — an i64 index type",
			410: "memory64: (memory i64 1) at :209 — an i64 index type",
			411: "memory64: (memory i64 1) at :209 — an i64 index type",
			412: "memory64: (memory i64 1) at :209 — an i64 index type",
			414: "memory64: (memory i64 1) at :209 — an i64 index type",
			415: "memory64: (memory i64 1) at :209 — an i64 index type",
			416: "memory64: (memory i64 1) at :209 — an i64 index type",
			417: "memory64: (memory i64 1) at :209 — an i64 index type",
			418: "memory64: (memory i64 1) at :209 — an i64 index type",
			420: "memory64: (memory i64 1) at :209 — an i64 index type",
			421: "memory64: (memory i64 1) at :209 — an i64 index type",
			422: "memory64: (memory i64 1) at :209 — an i64 index type",
			423: "memory64: (memory i64 1) at :209 — an i64 index type",
			424: "memory64: (memory i64 1) at :209 — an i64 index type",
			426: "memory64: (memory i64 1) at :209 — an i64 index type",
			427: "memory64: (memory i64 1) at :209 — an i64 index type",
			428: "memory64: (memory i64 1) at :209 — an i64 index type",
			429: "memory64: (memory i64 1) at :209 — an i64 index type",
			430: "memory64: (memory i64 1) at :209 — an i64 index type",
			432: "memory64: (memory i64 1) at :209 — an i64 index type",
			433: "memory64: (memory i64 1) at :209 — an i64 index type",
			434: "memory64: (memory i64 1) at :209 — an i64 index type",
			435: "memory64: (memory i64 1) at :209 — an i64 index type",
			436: "memory64: (memory i64 1) at :209 — an i64 index type",
			438: "memory64: (memory i64 1) at :209 — an i64 index type",
			439: "memory64: (memory i64 1) at :209 — an i64 index type",
			440: "memory64: (memory i64 1) at :209 — an i64 index type",
			441: "memory64: (memory i64 1) at :209 — an i64 index type",
			442: "memory64: (memory i64 1) at :209 — an i64 index type",
			444: "memory64: (memory i64 1) at :209 — an i64 index type",
			445: "memory64: (memory i64 1) at :209 — an i64 index type",
			446: "memory64: (memory i64 1) at :209 — an i64 index type",
			447: "memory64: (memory i64 1) at :209 — an i64 index type",
			448: "memory64: (memory i64 1) at :209 — an i64 index type",
			450: "memory64: (memory i64 1) at :209 — an i64 index type",
			451: "memory64: (memory i64 1) at :209 — an i64 index type",
			452: "memory64: (memory i64 1) at :209 — an i64 index type",
			453: "memory64: (memory i64 1) at :209 — an i64 index type",
			454: "memory64: (memory i64 1) at :209 — an i64 index type",
			456: "memory64: (memory i64 1) at :209 — an i64 index type",
			457: "memory64: (memory i64 1) at :209 — an i64 index type",
			458: "memory64: (memory i64 1) at :209 — an i64 index type",
			459: "memory64: (memory i64 1) at :209 — an i64 index type",
			460: "memory64: (memory i64 1) at :209 — an i64 index type",
			462: "memory64: (memory i64 1) at :209 — an i64 index type",
			463: "memory64: (memory i64 1) at :209 — an i64 index type",
			464: "memory64: (memory i64 1) at :209 — an i64 index type",
			465: "memory64: (memory i64 1) at :209 — an i64 index type",
			466: "memory64: (memory i64 1) at :209 — an i64 index type",
			468: "memory64: (memory i64 1) at :209 — an i64 index type",
			469: "memory64: (memory i64 1) at :209 — an i64 index type",
			470: "memory64: (memory i64 1) at :209 — an i64 index type",
			471: "memory64: (memory i64 1) at :209 — an i64 index type",
			472: "memory64: an i64 index type at :209 — the module this action runs against",
			474: "memory64: an i64 index type at :209 — the module this action runs against",
			475: "memory64: an i64 index type at :209 — the module this action runs against",
			476: "memory64: an i64 index type at :209 — the module this action runs against",
			477: "memory64: an i64 index type at :209 — the module this action runs against",
			478: "memory64: an i64 index type at :209 — the module this action runs against",
			479: "memory64: an i64 index type at :209 — the module this action runs against",
			480: "memory64: an i64 index type at :209 — the module this action runs against",
			482: "memory64: an i64 index type at :209 — the module this action runs against",
			483: "memory64: an i64 index type at :209 — the module this action runs against",
			484: "memory64: an i64 index type at :209 — the module this action runs against",
			485: "memory64: an i64 index type at :209 — the module this action runs against",
			486: "memory64: an i64 index type at :209 — the module this action runs against",
			487: "memory64: an i64 index type at :209 — the module this action runs against",
			488: "memory64: an i64 index type at :209 — the module this action runs against",
			516: "memory64: (memory i64 1) at :492 — an i64 index type",
			517: "memory64: (memory i64 1) at :492 — an i64 index type",
			518: "memory64: (memory i64 1) at :492 — an i64 index type",
			519: "memory64: (memory i64 1) at :492 — an i64 index type",
			520: "memory64: (memory i64 1) at :492 — an i64 index type",
			522: "memory64: (memory i64 1) at :492 — an i64 index type",
			523: "memory64: (memory i64 1) at :492 — an i64 index type",
			524: "memory64: (memory i64 1) at :492 — an i64 index type",
			525: "memory64: (memory i64 1) at :492 — an i64 index type",
			526: "memory64: (memory i64 1) at :492 — an i64 index type",
			528: "memory64: (memory i64 1) at :492 — an i64 index type",
			529: "memory64: (memory i64 1) at :492 — an i64 index type",
			530: "memory64: (memory i64 1) at :492 — an i64 index type",
			531: "memory64: (memory i64 1) at :492 — an i64 index type",
			532: "memory64: an i64 index type at :492 — the module this action runs against",
			534: "memory64: an i64 index type at :492 — the module this action runs against",
			535: "memory64: an i64 index type at :492 — the module this action runs against",
			563: "memory64: (memory i64 1) at :539 — an i64 index type",
			564: "memory64: (memory i64 1) at :539 — an i64 index type",
			565: "memory64: (memory i64 1) at :539 — an i64 index type",
			566: "memory64: (memory i64 1) at :539 — an i64 index type",
			567: "memory64: (memory i64 1) at :539 — an i64 index type",
			569: "memory64: (memory i64 1) at :539 — an i64 index type",
			570: "memory64: (memory i64 1) at :539 — an i64 index type",
			571: "memory64: (memory i64 1) at :539 — an i64 index type",
			572: "memory64: (memory i64 1) at :539 — an i64 index type",
			573: "memory64: (memory i64 1) at :539 — an i64 index type",
			575: "memory64: (memory i64 1) at :539 — an i64 index type",
			576: "memory64: (memory i64 1) at :539 — an i64 index type",
			577: "memory64: (memory i64 1) at :539 — an i64 index type",
			578: "memory64: (memory i64 1) at :539 — an i64 index type",
			579: "memory64: an i64 index type at :539 — the module this action runs against",
			581: "memory64: an i64 index type at :539 — the module this action runs against",
			582: "memory64: an i64 index type at :539 — the module this action runs against",
		},
		"align0.wast": {
			39: "multi-memory: 3 memories at :3, so a memarg carries flags bit 6",
			40: "multi-memory: 3 memories at :3, so a memarg carries flags bit 6",
			41: "multi-memory: 3 memories at :3, so a memarg carries flags bit 6",
			42: "multi-memory: 3 memories at :3, so a memarg carries flags bit 6",
		},
		"align64.wast": {
			802: "memory64: (memory i64 1) at :458 — an i64 index type",
			803: "memory64: (memory i64 1) at :458 — an i64 index type",
			804: "memory64: (memory i64 1) at :458 — an i64 index type",
			805: "memory64: (memory i64 1) at :458 — an i64 index type",
			807: "memory64: (memory i64 1) at :458 — an i64 index type",
			808: "memory64: (memory i64 1) at :458 — an i64 index type",
			809: "memory64: (memory i64 1) at :458 — an i64 index type",
			810: "memory64: (memory i64 1) at :458 — an i64 index type",
			811: "memory64: (memory i64 1) at :458 — an i64 index type",
			813: "memory64: (memory i64 1) at :458 — an i64 index type",
			814: "memory64: (memory i64 1) at :458 — an i64 index type",
			815: "memory64: (memory i64 1) at :458 — an i64 index type",
			816: "memory64: (memory i64 1) at :458 — an i64 index type",
			817: "memory64: (memory i64 1) at :458 — an i64 index type",
			818: "memory64: (memory i64 1) at :458 — an i64 index type",
			819: "memory64: (memory i64 1) at :458 — an i64 index type",
			820: "memory64: (memory i64 1) at :458 — an i64 index type",
			821: "memory64: (memory i64 1) at :458 — an i64 index type",
			822: "memory64: (memory i64 1) at :458 — an i64 index type",
			823: "memory64: (memory i64 1) at :458 — an i64 index type",
			824: "memory64: (memory i64 1) at :458 — an i64 index type",
			825: "memory64: (memory i64 1) at :458 — an i64 index type",
			826: "memory64: (memory i64 1) at :458 — an i64 index type",
			828: "memory64: (memory i64 1) at :458 — an i64 index type",
			829: "memory64: (memory i64 1) at :458 — an i64 index type",
			830: "memory64: (memory i64 1) at :458 — an i64 index type",
			831: "memory64: (memory i64 1) at :458 — an i64 index type",
			832: "memory64: (memory i64 1) at :458 — an i64 index type",
			833: "memory64: (memory i64 1) at :458 — an i64 index type",
			834: "memory64: (memory i64 1) at :458 — an i64 index type",
			835: "memory64: (memory i64 1) at :458 — an i64 index type",
			836: "memory64: (memory i64 1) at :458 — an i64 index type",
			837: "memory64: (memory i64 1) at :458 — an i64 index type",
			838: "memory64: (memory i64 1) at :458 — an i64 index type",
			839: "memory64: (memory i64 1) at :458 — an i64 index type",
			840: "memory64: (memory i64 1) at :458 — an i64 index type",
			841: "memory64: (memory i64 1) at :458 — an i64 index type",
			842: "memory64: (memory i64 1) at :458 — an i64 index type",
			843: "memory64: (memory i64 1) at :458 — an i64 index type",
			844: "memory64: (memory i64 1) at :458 — an i64 index type",
			845: "memory64: (memory i64 1) at :458 — an i64 index type",
			846: "memory64: (memory i64 1) at :458 — an i64 index type",
			847: "memory64: (memory i64 1) at :458 — an i64 index type",
			848: "memory64: (memory i64 1) at :458 — an i64 index type",
			849: "memory64: (memory i64 1) at :458 — an i64 index type",
			850: "memory64: (memory i64 1) at :458 — an i64 index type",
			864: "memory64: an i64 index type at :854 — the module this action runs against",
			866: "memory64: (memory i64 1) at :854 — an i64 index type",
		},
		"bulk64.wast": {
			21:  "memory64: (memory i64 1) at :7 — an i64 index type",
			22:  "memory64: (memory i64 1) at :7 — an i64 index type",
			23:  "memory64: (memory i64 1) at :7 — an i64 index type",
			24:  "memory64: (memory i64 1) at :7 — an i64 index type",
			25:  "memory64: (memory i64 1) at :7 — an i64 index type",
			26:  "memory64: (memory i64 1) at :7 — an i64 index type",
			29:  "memory64: (memory i64 1) at :7 — an i64 index type",
			30:  "memory64: (memory i64 1) at :7 — an i64 index type",
			31:  "memory64: (memory i64 1) at :7 — an i64 index type",
			34:  "memory64: (memory i64 1) at :7 — an i64 index type",
			37:  "memory64: (memory i64 1) at :7 — an i64 index type",
			40:  "memory64: an i64 index type at :7 — the module this action runs against",
			60:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			62:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			63:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			64:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			65:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			66:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			67:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			70:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			71:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			72:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			73:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			74:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			75:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			76:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			79:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			80:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			81:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			82:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			83:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			84:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			85:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			86:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			89:  "memory64: an i64 index type at :45 — the module this action runs against",
			92:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			93:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			94:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			95:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			96:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			97:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			98:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			101: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			102: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			105: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			106: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			109: "memory64: an i64 index type at :45 — the module this action runs against",
			112: "memory64: an i64 index type at :45 — the module this action runs against",
			131: "memory64: (memory i64 1) at :117 — an i64 index type",
			132: "memory64: (memory i64 1) at :117 — an i64 index type",
			133: "memory64: (memory i64 1) at :117 — an i64 index type",
			134: "memory64: (memory i64 1) at :117 — an i64 index type",
			137: "memory64: (memory i64 1) at :117 — an i64 index type",
			140: "memory64: an i64 index type at :117 — the module this action runs against",
			142: "memory64: (memory i64 1) at :117 — an i64 index type",
			143: "memory64: (memory i64 1) at :117 — an i64 index type",
			146: "memory64: (memory i64 1) at :117 — an i64 index type",
			147: "memory64: (memory i64 1) at :117 — an i64 index type",
			150: "memory64: an i64 index type at :117 — the module this action runs against",
			153: "memory64: an i64 index type at :117 — the module this action runs against",
			158: "memory64: (memory i64 1) at :117 — an i64 index type",
			176: "memory64: (memory i64 1) at :161 — an i64 index type",
			177: "memory64: (memory i64 1) at :161 — an i64 index type",
			178: "memory64: (memory i64 1) at :161 — an i64 index type",
			179: "memory64: (memory i64 1) at :161 — an i64 index type",
		},
		"endianness64.wast": {
			133: "memory64: (memory i64 1) at :1 — an i64 index type",
			134: "memory64: (memory i64 1) at :1 — an i64 index type",
			135: "memory64: (memory i64 1) at :1 — an i64 index type",
			136: "memory64: (memory i64 1) at :1 — an i64 index type",
			138: "memory64: (memory i64 1) at :1 — an i64 index type",
			139: "memory64: (memory i64 1) at :1 — an i64 index type",
			140: "memory64: (memory i64 1) at :1 — an i64 index type",
			141: "memory64: (memory i64 1) at :1 — an i64 index type",
			143: "memory64: (memory i64 1) at :1 — an i64 index type",
			144: "memory64: (memory i64 1) at :1 — an i64 index type",
			145: "memory64: (memory i64 1) at :1 — an i64 index type",
			146: "memory64: (memory i64 1) at :1 — an i64 index type",
			148: "memory64: (memory i64 1) at :1 — an i64 index type",
			149: "memory64: (memory i64 1) at :1 — an i64 index type",
			150: "memory64: (memory i64 1) at :1 — an i64 index type",
			151: "memory64: (memory i64 1) at :1 — an i64 index type",
			153: "memory64: (memory i64 1) at :1 — an i64 index type",
			154: "memory64: (memory i64 1) at :1 — an i64 index type",
			155: "memory64: (memory i64 1) at :1 — an i64 index type",
			156: "memory64: (memory i64 1) at :1 — an i64 index type",
			158: "memory64: (memory i64 1) at :1 — an i64 index type",
			159: "memory64: (memory i64 1) at :1 — an i64 index type",
			160: "memory64: (memory i64 1) at :1 — an i64 index type",
			161: "memory64: (memory i64 1) at :1 — an i64 index type",
			163: "memory64: (memory i64 1) at :1 — an i64 index type",
			164: "memory64: (memory i64 1) at :1 — an i64 index type",
			165: "memory64: (memory i64 1) at :1 — an i64 index type",
			166: "memory64: (memory i64 1) at :1 — an i64 index type",
			168: "memory64: (memory i64 1) at :1 — an i64 index type",
			169: "memory64: (memory i64 1) at :1 — an i64 index type",
			170: "memory64: (memory i64 1) at :1 — an i64 index type",
			171: "memory64: (memory i64 1) at :1 — an i64 index type",
			173: "memory64: (memory i64 1) at :1 — an i64 index type",
			174: "memory64: (memory i64 1) at :1 — an i64 index type",
			175: "memory64: (memory i64 1) at :1 — an i64 index type",
			176: "memory64: (memory i64 1) at :1 — an i64 index type",
			178: "memory64: (memory i64 1) at :1 — an i64 index type",
			179: "memory64: (memory i64 1) at :1 — an i64 index type",
			180: "memory64: (memory i64 1) at :1 — an i64 index type",
			181: "memory64: (memory i64 1) at :1 — an i64 index type",
			184: "memory64: (memory i64 1) at :1 — an i64 index type",
			185: "memory64: (memory i64 1) at :1 — an i64 index type",
			186: "memory64: (memory i64 1) at :1 — an i64 index type",
			187: "memory64: (memory i64 1) at :1 — an i64 index type",
			189: "memory64: (memory i64 1) at :1 — an i64 index type",
			190: "memory64: (memory i64 1) at :1 — an i64 index type",
			191: "memory64: (memory i64 1) at :1 — an i64 index type",
			192: "memory64: (memory i64 1) at :1 — an i64 index type",
			194: "memory64: (memory i64 1) at :1 — an i64 index type",
			195: "memory64: (memory i64 1) at :1 — an i64 index type",
			196: "memory64: (memory i64 1) at :1 — an i64 index type",
			197: "memory64: (memory i64 1) at :1 — an i64 index type",
			199: "memory64: (memory i64 1) at :1 — an i64 index type",
			200: "memory64: (memory i64 1) at :1 — an i64 index type",
			201: "memory64: (memory i64 1) at :1 — an i64 index type",
			202: "memory64: (memory i64 1) at :1 — an i64 index type",
			204: "memory64: (memory i64 1) at :1 — an i64 index type",
			205: "memory64: (memory i64 1) at :1 — an i64 index type",
			206: "memory64: (memory i64 1) at :1 — an i64 index type",
			207: "memory64: (memory i64 1) at :1 — an i64 index type",
			209: "memory64: (memory i64 1) at :1 — an i64 index type",
			210: "memory64: (memory i64 1) at :1 — an i64 index type",
			211: "memory64: (memory i64 1) at :1 — an i64 index type",
			212: "memory64: (memory i64 1) at :1 — an i64 index type",
			214: "memory64: (memory i64 1) at :1 — an i64 index type",
			215: "memory64: (memory i64 1) at :1 — an i64 index type",
			216: "memory64: (memory i64 1) at :1 — an i64 index type",
			217: "memory64: (memory i64 1) at :1 — an i64 index type",
		},
		"float_exprs0.wast": {
			25: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			26: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			27: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			28: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			29: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			30: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			31: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			32: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			33: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			34: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			35: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			36: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			37: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
		},
		"float_exprs1.wast": {
			103: "multi-memory: 9 memories at :4, so a memarg carries flags bit 6",
			104: "multi-memory: 9 memories at :4, so a memarg carries flags bit 6",
		},
		"float_memory0.wast": {
			20: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			21: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			22: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			23: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			24: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			25: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			26: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			27: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			28: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			29: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			30: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			31: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			32: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			33: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			46: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			47: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			48: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			49: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			50: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			51: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			52: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			53: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			54: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			55: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			56: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			57: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			58: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			59: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
		},
		"float_memory64.wast": {
			15:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			16:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			17:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			18:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			19:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			20:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			21:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			22:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			23:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			24:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			25:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			26:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			27:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			28:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			40:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			41:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			42:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			43:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			44:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			45:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			46:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			47:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			48:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			49:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			50:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			51:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			52:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			53:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			67:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			68:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			69:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			70:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			71:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			72:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			73:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			74:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			75:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			76:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			77:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			78:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			79:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			80:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			92:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			93:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			94:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			95:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			96:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			97:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			98:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			99:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			100: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			101: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			102: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			103: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			104: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			105: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			119: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			120: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			121: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			122: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			123: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			124: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			125: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			126: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			127: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			128: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			129: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			130: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			131: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			132: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			144: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			145: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			146: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			147: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			148: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			149: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			150: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			151: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			152: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			153: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			154: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			155: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			156: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			157: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
		},
		"imports1.wast": {
			12: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			13: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			14: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			15: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
		},
		"imports2.wast": {
			17: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
			18: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
			19: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
			20: "multi-memory: a memarg carrying flags bit 6 at :9 — the module this action runs against",
		},
		"load0.wast": {
			18: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			19: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
		},
		"load1.wast": {
			31: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			32: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			33: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			34: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			35: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			37: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			38: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			39: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			40: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			41: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
		},
		"memory-multi.wast": {
			// The two rows grave #130's repair moved here. They were encode-column fails — the
			// module at :5 uses `$d` before the `(data $d …)` on :19 — and once it encodes, the
			// `assert_return`s meet the gate the file's other pairs already met: `(memory $mem1)`
			// and `(memory $mem2)` at :6–7, so `i32.load $mem1` carries flags bit 6. The module
			// row itself is a pass, not a decline, which is why this pair is two and not three.
			22: "multi-memory: 2 memories at :6, so a memarg carries flags bit 6",
			23: "multi-memory: 2 memories at :6, so a memarg carries flags bit 6",
			41: "multi-memory: 2 memories at :26, so a memarg carries flags bit 6",
			42: "multi-memory: 2 memories at :26, so a memarg carries flags bit 6",
		},
		"memory64.wast": {
			12:  "memory64: (memory i64 (data)) at :11 — an i64 index type",
			14:  "memory64: (memory i64 (data \"\")) at :13 — an i64 index type",
			16:  "memory64: (memory i64 (data \"x\")) at :15 — an i64 index type",
			159: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			160: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			162: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			163: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			164: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			165: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			167: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			168: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			169: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			170: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			172: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			173: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			174: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			175: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			176: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			177: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			178: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			179: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			181: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			182: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			183: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			184: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			185: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			186: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			188: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			189: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			190: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			191: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			192: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			193: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			195: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			196: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			197: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			198: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			199: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			200: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			201: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			202: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			203: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			204: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			205: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
			206: "memory64: an addr64 memory (min 1, no max) at :71 — an i64 index type",
		},
		"memory_copy0.wast": {
			19: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			21: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			22: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			23: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			24: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			25: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			26: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			29: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			30: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			31: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			32: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			33: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			34: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			35: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			38: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			39: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			40: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			41: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			42: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			43: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			44: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			45: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			48: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			49: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			52: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			53: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			56: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
			58: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
		},
		"memory_copy64.wast": {
			15:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			17:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			18:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			19:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			20:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			21:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			22:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			23:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			24:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			25:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			26:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			27:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			28:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			29:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			30:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			31:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			32:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			33:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			34:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			35:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			36:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			37:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			38:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			39:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			40:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			41:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			42:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			43:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			44:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			45:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			46:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			57:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			59:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			60:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			61:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			62:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			63:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			64:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			65:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			66:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			67:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			68:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			69:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			70:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			71:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			72:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			73:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			74:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			75:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			76:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			77:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			78:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			79:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			80:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			81:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			82:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			83:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			84:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			85:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			86:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			87:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			88:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			99:   "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			101:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			102:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			103:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			104:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			105:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			106:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			107:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			108:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			109:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			110:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			111:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			112:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			113:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			114:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			115:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			116:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			117:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			118:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			119:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			120:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			121:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			122:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			123:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			124:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			125:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			126:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			127:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			128:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			129:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			130:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			141:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			143:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			144:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			145:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			146:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			147:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			148:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			149:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			150:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			151:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			152:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			153:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			154:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			155:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			156:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			157:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			158:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			159:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			160:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			161:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			162:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			163:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			164:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			165:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			166:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			167:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			168:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			169:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			170:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			171:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			172:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			183:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			185:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			186:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			187:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			188:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			189:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			190:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			191:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			192:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			193:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			194:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			195:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			196:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			197:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			198:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			199:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			200:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			201:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			202:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			203:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			204:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			205:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			206:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			207:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			208:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			209:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			210:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			211:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			212:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			213:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			214:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			225:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			227:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			228:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			229:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			230:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			231:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			232:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			233:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			234:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			235:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			236:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			237:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			238:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			239:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			240:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			241:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			242:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			243:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			244:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			245:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			246:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			247:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			248:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			249:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			250:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			251:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			252:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			253:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			254:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			255:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			256:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			267:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			269:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			270:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			271:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			272:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			273:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			274:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			275:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			276:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			277:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			278:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			279:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			280:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			281:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			282:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			283:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			284:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			285:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			286:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			287:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			288:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			289:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			290:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			291:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			292:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			293:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			294:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			295:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			296:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			297:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			298:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			309:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			311:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			312:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			313:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			314:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			315:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			316:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			317:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			318:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			319:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			320:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			321:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			322:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			323:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			324:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			325:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			326:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			327:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			328:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			329:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			330:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			331:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			332:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			333:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			334:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			335:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			336:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			337:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			338:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			339:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			340:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			4780: "memory64: an addr64 memory (min 1, max 1) at :4763 — an i64 index type",
			4782: "memory64: an addr64 memory (min 1, max 1) at :4763 — an i64 index type",
			4784: "memory64: an addr64 memory (min 1, max 1) at :4763 — an i64 index type",
			4786: "memory64: an addr64 memory (min 1, max 1) at :4763 — an i64 index type",
			4806: "memory64: an addr64 memory (min 1, max 1) at :4789 — an i64 index type",
			4808: "memory64: an addr64 memory (min 1, max 1) at :4789 — an i64 index type",
			4810: "memory64: an addr64 memory (min 1, max 1) at :4789 — an i64 index type",
			4812: "memory64: an addr64 memory (min 1, max 1) at :4789 — an i64 index type",
			4819: "memory64: an i64 index type at :4815 — the module this action runs against",
			4825: "memory64: an i64 index type at :4821 — the module this action runs against",
			4831: "memory64: an i64 index type at :4827 — the module this action runs against",
			4837: "memory64: an i64 index type at :4833 — the module this action runs against",
			4857: "memory64: an addr64 memory (min 1, max 1) at :4839 — an i64 index type",
			4859: "memory64: an addr64 memory (min 1, max 1) at :4839 — an i64 index type",
			4861: "memory64: an addr64 memory (min 1, max 1) at :4839 — an i64 index type",
			4867: "memory64: (memory i64 1 1) at :4863 — an i64 index type",
			4873: "memory64: an i64 index type at :4869 — the module this action runs against",
			4879: "memory64: (memory i64 1 1) at :4875 — an i64 index type",
			4885: "memory64: an i64 index type at :4881 — the module this action runs against",
			4891: "memory64: (memory i64 1 1) at :4887 — an i64 index type",
			4897: "memory64: an i64 index type at :4893 — the module this action runs against",
			5115: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5117: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5119: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5121: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5123: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5125: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5127: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5129: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5131: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5133: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5135: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5137: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5139: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5141: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5143: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5145: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5147: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5149: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5151: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5153: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5155: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5157: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5159: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5161: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5163: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5165: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5167: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5169: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5171: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5173: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5175: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5177: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5179: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5181: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5183: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5185: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5187: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5189: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5191: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5193: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5195: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5197: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5199: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5201: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5203: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5205: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5207: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5209: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5211: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5213: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5215: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5217: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5219: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5221: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5223: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5225: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5227: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5229: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5231: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5233: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5235: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5237: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5239: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5241: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5243: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5245: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5247: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5249: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5251: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5253: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5255: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5257: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5259: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5261: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5263: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5265: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5267: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5269: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5271: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5273: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5275: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5277: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5279: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5281: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5283: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5285: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5287: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5289: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5291: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5293: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5295: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5297: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5299: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5301: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5303: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5305: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5307: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5309: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5311: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5313: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5315: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5317: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5319: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5321: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5323: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5325: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5327: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5329: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5331: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5333: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5335: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5337: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5339: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5341: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5343: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5345: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5347: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5349: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5351: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5353: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5355: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5357: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5359: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5361: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5363: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5365: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5367: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5369: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5371: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5373: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5375: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5377: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5379: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5381: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5383: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5385: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5387: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5389: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5391: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5393: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5395: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5397: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5399: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5401: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5403: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5405: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5407: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5409: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5411: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5413: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5415: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5417: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5419: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5421: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5423: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5425: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5427: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5429: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5431: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5433: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5435: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5437: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5439: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5441: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5443: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5445: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5447: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5449: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5451: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5453: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5455: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5457: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5459: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5461: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5463: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5465: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5467: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5469: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5471: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5473: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5475: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5477: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5479: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5481: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5483: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5485: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5487: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5489: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5491: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5493: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5495: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5497: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5499: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5501: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5503: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5505: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5507: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5509: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5511: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5513: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5515: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5517: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5519: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5521: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5523: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5525: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5527: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5529: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5531: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5533: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5535: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5537: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5539: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5541: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5543: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5545: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5547: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5549: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5551: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5553: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5555: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5557: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5559: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5561: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5563: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5565: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5567: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5569: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5571: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5573: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5575: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
			5577: "memory64: an addr64 memory (min 1, max 1) at :4899 — an i64 index type",
		},
		"memory_fill0.wast": {
			18: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			19: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			20: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			21: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			22: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			23: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			26: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			27: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			28: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			31: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			34: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
			36: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			37: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			40: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			43: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
		},
		"memory_fill64.wast": {
			22:  "memory64: an addr64 memory (min 1, max 1) at :6 — an i64 index type",
			24:  "memory64: an addr64 memory (min 1, max 1) at :6 — an i64 index type",
			26:  "memory64: an addr64 memory (min 1, max 1) at :6 — an i64 index type",
			44:  "memory64: an i64 index type at :28 — the module this action runs against",
			62:  "memory64: an i64 index type at :46 — the module this action runs against",
			80:  "memory64: an addr64 memory (min 1, max 1) at :64 — an i64 index type",
			82:  "memory64: an addr64 memory (min 1, max 1) at :64 — an i64 index type",
			100: "memory64: an addr64 memory (min 1, max 1) at :84 — an i64 index type",
			118: "memory64: an i64 index type at :102 — the module this action runs against",
			136: "memory64: an addr64 memory (min 1, max 1) at :120 — an i64 index type",
			138: "memory64: an addr64 memory (min 1, max 1) at :120 — an i64 index type",
			140: "memory64: an addr64 memory (min 1, max 1) at :120 — an i64 index type",
			142: "memory64: an addr64 memory (min 1, max 1) at :120 — an i64 index type",
			162: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			164: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			166: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			168: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			170: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			172: "memory64: an addr64 memory (min 1, max 1) at :145 — an i64 index type",
			638: "memory64: an i64 index type at :621 — the module this action runs against",
			641: "memory64: an addr64 memory (min 1, max 1) at :621 — an i64 index type",
			660: "memory64: an i64 index type at :643 — the module this action runs against",
			663: "memory64: an addr64 memory (min 1, max 1) at :643 — an i64 index type",
			682: "memory64: an i64 index type at :665 — the module this action runs against",
			685: "memory64: an addr64 memory (min 1, max 1) at :665 — an i64 index type",
		},
		"memory_grow64.wast": {
			14: "memory64: (memory i64 0) at :1 — an i64 index type",
			15: "memory64: an i64 index type at :1 — the module this action runs against",
			16: "memory64: an i64 index type at :1 — the module this action runs against",
			17: "memory64: an i64 index type at :1 — the module this action runs against",
			18: "memory64: an i64 index type at :1 — the module this action runs against",
			19: "memory64: (memory i64 0) at :1 — an i64 index type",
			20: "memory64: (memory i64 0) at :1 — an i64 index type",
			21: "memory64: (memory i64 0) at :1 — an i64 index type",
			22: "memory64: (memory i64 0) at :1 — an i64 index type",
			23: "memory64: (memory i64 0) at :1 — an i64 index type",
			24: "memory64: an i64 index type at :1 — the module this action runs against",
			25: "memory64: an i64 index type at :1 — the module this action runs against",
			26: "memory64: (memory i64 0) at :1 — an i64 index type",
			27: "memory64: (memory i64 0) at :1 — an i64 index type",
			28: "memory64: (memory i64 0) at :1 — an i64 index type",
			29: "memory64: (memory i64 0) at :1 — an i64 index type",
			30: "memory64: (memory i64 0) at :1 — an i64 index type",
			31: "memory64: (memory i64 0) at :1 — an i64 index type",
			32: "memory64: (memory i64 0) at :1 — an i64 index type",
			33: "memory64: (memory i64 0) at :1 — an i64 index type",
			41: "memory64: (memory i64 0) at :36 — an i64 index type",
			42: "memory64: (memory i64 0) at :36 — an i64 index type",
			43: "memory64: (memory i64 0) at :36 — an i64 index type",
			44: "memory64: (memory i64 0) at :36 — an i64 index type",
			45: "memory64: (memory i64 0) at :36 — an i64 index type",
			46: "memory64: (memory i64 0) at :36 — an i64 index type",
			53: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			54: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			55: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			56: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			57: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			58: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			59: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			60: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			85: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			86: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			87: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			88: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			89: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			90: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			91: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			92: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			93: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			94: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
			95: "memory64: an addr64 memory (min 1, no max) at :64 — an i64 index type",
		},
		"memory_init64.wast": {
			17:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			19:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			20:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			21:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			22:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			23:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			24:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			25:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			26:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			27:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			28:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			29:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			30:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			31:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			32:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			33:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			34:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			35:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			36:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			37:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			38:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			39:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			40:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			41:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			42:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			43:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			44:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			45:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			46:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			47:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			48:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			61:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			63:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			64:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			65:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			66:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			67:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			68:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			69:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			70:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			71:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			72:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			73:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			74:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			75:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			76:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			77:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			78:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			79:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			80:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			81:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			82:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			83:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			84:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			85:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			86:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			87:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			88:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			89:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			90:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			91:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			92:  "memory64: (memory (export \"memory0\") i64 1 1) at :50 — an i64 index type",
			105: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			107: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			108: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			109: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			110: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			111: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			112: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			113: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			114: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			115: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			116: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			117: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			118: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			119: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			120: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			121: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			122: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			123: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			124: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			125: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			126: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			127: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			128: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			129: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			130: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			131: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			132: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			133: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			134: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			135: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			136: "memory64: (memory (export \"memory0\") i64 1 1) at :94 — an i64 index type",
			157: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			159: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			160: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			161: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			162: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			163: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			164: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			165: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			166: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			167: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			168: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			169: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			170: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			171: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			172: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			173: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			174: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			175: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			176: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			177: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			178: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			179: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			180: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			181: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			182: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			183: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			184: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			185: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			186: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			187: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			188: "memory64: (memory (export \"memory0\") i64 1 1) at :138 — an i64 index type",
			209: "memory64: (memory i64 1) at :203 — an i64 index type",
			216: "memory64: (memory i64 1) at :211 — an i64 index type",
			224: "memory64: an i64 index type at :218 — the module this action runs against",
			232: "memory64: (memory i64 1) at :226 — an i64 index type",
			240: "memory64: an i64 index type at :234 — the module this action runs against",
			248: "memory64: (memory i64 1) at :242 — an i64 index type",
			256: "memory64: an i64 index type at :250 — the module this action runs against",
			263: "memory64: an i64 index type at :258 — the module this action runs against",
			285: "memory64: (memory i64 1) at :279 — an i64 index type",
			292: "memory64: an i64 index type at :287 — the module this action runs against",
			299: "memory64: an i64 index type at :294 — the module this action runs against",
			306: "memory64: an i64 index type at :301 — the module this action runs against",
			313: "memory64: an i64 index type at :308 — the module this action runs against",
			320: "memory64: (memory i64 1) at :315 — an i64 index type",
			327: "memory64: an i64 index type at :322 — the module this action runs against",
			334: "memory64: (memory i64 1) at :329 — an i64 index type",
			341: "memory64: (memory i64 1) at :336 — an i64 index type",
			348: "memory64: an i64 index type at :343 — the module this action runs against",
			872: "memory64: an i64 index type at :854 — the module this action runs against",
			875: "memory64: (memory i64 1 1) at :854 — an i64 index type",
			895: "memory64: an i64 index type at :877 — the module this action runs against",
			898: "memory64: (memory i64 1 1) at :877 — an i64 index type",
			918: "memory64: an i64 index type at :900 — the module this action runs against",
			921: "memory64: (memory i64 1 1) at :900 — an i64 index type",
			941: "memory64: an i64 index type at :923 — the module this action runs against",
			944: "memory64: (memory i64 1 1) at :923 — an i64 index type",
			964: "memory64: an i64 index type at :946 — the module this action runs against",
			967: "memory64: (memory i64 1) at :946 — an i64 index type",
			987: "memory64: an i64 index type at :969 — the module this action runs against",
			990: "memory64: (memory i64 1) at :969 — an i64 index type",
		},
		"memory_redundancy64.wast": {
			59: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			60: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			61: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			62: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			63: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			64: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			65: "memory64: (memory i64 1 1) at :5 — an i64 index type",
		},
		"memory_trap0.wast": {
			23: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			24: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			25: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			26: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			27: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			28: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			29: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			30: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			31: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			32: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			33: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			34: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			35: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
		},
		"memory_trap1.wast": {
			79:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			80:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			81:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			82:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			83:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			84:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			85:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			86:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			87:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			88:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			89:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			90:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			91:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			92:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			93:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			94:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			95:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			96:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			97:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			98:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			99:  "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			100: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			101: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			102: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			103: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			104: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			105: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			106: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			107: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			108: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			109: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			110: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			111: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			112: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			113: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			114: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			115: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			116: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			117: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			118: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			119: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			120: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			121: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			122: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			123: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			124: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			125: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			126: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			127: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			128: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			129: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			130: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			131: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			132: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			133: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			134: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			135: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			136: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			137: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			138: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			139: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			140: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			141: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			142: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			143: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			144: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			145: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			146: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			147: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			148: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			149: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			150: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			151: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			152: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			153: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			154: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			155: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			156: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			157: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			158: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			159: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			160: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			161: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			162: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			163: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			164: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			165: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			166: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			167: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			168: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			169: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			170: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			171: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			172: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			173: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			174: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			175: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			176: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			177: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			178: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			179: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			180: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			181: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			182: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			183: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			184: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			185: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			186: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			187: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			188: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			189: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			190: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			191: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			192: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			193: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			194: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			195: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			196: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			197: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			198: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			199: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			200: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			201: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			202: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			203: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			204: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			205: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			206: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			207: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			208: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			209: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			210: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			211: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			212: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			213: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			214: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			215: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			216: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			217: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			218: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			219: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			220: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			221: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			222: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			223: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			224: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			225: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			226: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			227: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			228: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			229: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			230: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			231: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			232: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			233: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			234: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			237: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			238: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			242: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			243: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			244: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			245: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			246: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			247: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			248: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			249: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			250: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
		},
		"memory_trap64.wast": {
			21:  "memory64: (memory i64 1) at :1 — an i64 index type",
			22:  "memory64: (memory i64 1) at :1 — an i64 index type",
			23:  "memory64: an i64 index type at :1 — the module this action runs against",
			24:  "memory64: an i64 index type at :1 — the module this action runs against",
			25:  "memory64: an i64 index type at :1 — the module this action runs against",
			26:  "memory64: an i64 index type at :1 — the module this action runs against",
			27:  "memory64: an i64 index type at :1 — the module this action runs against",
			28:  "memory64: an i64 index type at :1 — the module this action runs against",
			29:  "memory64: an i64 index type at :1 — the module this action runs against",
			30:  "memory64: an i64 index type at :1 — the module this action runs against",
			31:  "memory64: an i64 index type at :1 — the module this action runs against",
			32:  "memory64: an i64 index type at :1 — the module this action runs against",
			110: "memory64: an i64 index type at :34 — the module this action runs against",
			111: "memory64: an i64 index type at :34 — the module this action runs against",
			112: "memory64: an i64 index type at :34 — the module this action runs against",
			113: "memory64: an i64 index type at :34 — the module this action runs against",
			114: "memory64: an i64 index type at :34 — the module this action runs against",
			115: "memory64: an i64 index type at :34 — the module this action runs against",
			116: "memory64: an i64 index type at :34 — the module this action runs against",
			117: "memory64: an i64 index type at :34 — the module this action runs against",
			118: "memory64: an i64 index type at :34 — the module this action runs against",
			119: "memory64: an i64 index type at :34 — the module this action runs against",
			120: "memory64: an i64 index type at :34 — the module this action runs against",
			121: "memory64: an i64 index type at :34 — the module this action runs against",
			122: "memory64: an i64 index type at :34 — the module this action runs against",
			123: "memory64: an i64 index type at :34 — the module this action runs against",
			124: "memory64: an i64 index type at :34 — the module this action runs against",
			125: "memory64: an i64 index type at :34 — the module this action runs against",
			126: "memory64: an i64 index type at :34 — the module this action runs against",
			127: "memory64: an i64 index type at :34 — the module this action runs against",
			128: "memory64: an i64 index type at :34 — the module this action runs against",
			129: "memory64: an i64 index type at :34 — the module this action runs against",
			130: "memory64: an i64 index type at :34 — the module this action runs against",
			131: "memory64: an i64 index type at :34 — the module this action runs against",
			132: "memory64: an i64 index type at :34 — the module this action runs against",
			133: "memory64: an i64 index type at :34 — the module this action runs against",
			134: "memory64: an i64 index type at :34 — the module this action runs against",
			135: "memory64: an i64 index type at :34 — the module this action runs against",
			136: "memory64: an i64 index type at :34 — the module this action runs against",
			137: "memory64: an i64 index type at :34 — the module this action runs against",
			138: "memory64: an i64 index type at :34 — the module this action runs against",
			139: "memory64: an i64 index type at :34 — the module this action runs against",
			140: "memory64: an i64 index type at :34 — the module this action runs against",
			141: "memory64: an i64 index type at :34 — the module this action runs against",
			142: "memory64: an i64 index type at :34 — the module this action runs against",
			143: "memory64: an i64 index type at :34 — the module this action runs against",
			144: "memory64: an i64 index type at :34 — the module this action runs against",
			145: "memory64: an i64 index type at :34 — the module this action runs against",
			146: "memory64: an i64 index type at :34 — the module this action runs against",
			147: "memory64: an i64 index type at :34 — the module this action runs against",
			148: "memory64: an i64 index type at :34 — the module this action runs against",
			149: "memory64: an i64 index type at :34 — the module this action runs against",
			150: "memory64: an i64 index type at :34 — the module this action runs against",
			151: "memory64: an i64 index type at :34 — the module this action runs against",
			152: "memory64: an i64 index type at :34 — the module this action runs against",
			153: "memory64: an i64 index type at :34 — the module this action runs against",
			154: "memory64: an i64 index type at :34 — the module this action runs against",
			155: "memory64: an i64 index type at :34 — the module this action runs against",
			156: "memory64: an i64 index type at :34 — the module this action runs against",
			157: "memory64: an i64 index type at :34 — the module this action runs against",
			158: "memory64: an i64 index type at :34 — the module this action runs against",
			159: "memory64: an i64 index type at :34 — the module this action runs against",
			160: "memory64: an i64 index type at :34 — the module this action runs against",
			161: "memory64: an i64 index type at :34 — the module this action runs against",
			162: "memory64: an i64 index type at :34 — the module this action runs against",
			163: "memory64: an i64 index type at :34 — the module this action runs against",
			164: "memory64: an i64 index type at :34 — the module this action runs against",
			165: "memory64: an i64 index type at :34 — the module this action runs against",
			166: "memory64: an i64 index type at :34 — the module this action runs against",
			167: "memory64: an i64 index type at :34 — the module this action runs against",
			168: "memory64: an i64 index type at :34 — the module this action runs against",
			169: "memory64: an i64 index type at :34 — the module this action runs against",
			170: "memory64: an i64 index type at :34 — the module this action runs against",
			171: "memory64: an i64 index type at :34 — the module this action runs against",
			172: "memory64: an i64 index type at :34 — the module this action runs against",
			173: "memory64: an i64 index type at :34 — the module this action runs against",
			174: "memory64: an i64 index type at :34 — the module this action runs against",
			175: "memory64: an i64 index type at :34 — the module this action runs against",
			176: "memory64: an i64 index type at :34 — the module this action runs against",
			177: "memory64: an i64 index type at :34 — the module this action runs against",
			178: "memory64: an i64 index type at :34 — the module this action runs against",
			179: "memory64: an i64 index type at :34 — the module this action runs against",
			180: "memory64: an i64 index type at :34 — the module this action runs against",
			181: "memory64: an i64 index type at :34 — the module this action runs against",
			182: "memory64: an i64 index type at :34 — the module this action runs against",
			183: "memory64: an i64 index type at :34 — the module this action runs against",
			184: "memory64: an i64 index type at :34 — the module this action runs against",
			185: "memory64: an i64 index type at :34 — the module this action runs against",
			186: "memory64: an i64 index type at :34 — the module this action runs against",
			187: "memory64: an i64 index type at :34 — the module this action runs against",
			188: "memory64: an i64 index type at :34 — the module this action runs against",
			189: "memory64: an i64 index type at :34 — the module this action runs against",
			190: "memory64: an i64 index type at :34 — the module this action runs against",
			191: "memory64: an i64 index type at :34 — the module this action runs against",
			192: "memory64: an i64 index type at :34 — the module this action runs against",
			193: "memory64: an i64 index type at :34 — the module this action runs against",
			194: "memory64: an i64 index type at :34 — the module this action runs against",
			195: "memory64: an i64 index type at :34 — the module this action runs against",
			196: "memory64: an i64 index type at :34 — the module this action runs against",
			197: "memory64: an i64 index type at :34 — the module this action runs against",
			198: "memory64: an i64 index type at :34 — the module this action runs against",
			199: "memory64: an i64 index type at :34 — the module this action runs against",
			200: "memory64: an i64 index type at :34 — the module this action runs against",
			201: "memory64: an i64 index type at :34 — the module this action runs against",
			202: "memory64: an i64 index type at :34 — the module this action runs against",
			203: "memory64: an i64 index type at :34 — the module this action runs against",
			204: "memory64: an i64 index type at :34 — the module this action runs against",
			205: "memory64: an i64 index type at :34 — the module this action runs against",
			206: "memory64: an i64 index type at :34 — the module this action runs against",
			207: "memory64: an i64 index type at :34 — the module this action runs against",
			208: "memory64: an i64 index type at :34 — the module this action runs against",
			209: "memory64: an i64 index type at :34 — the module this action runs against",
			210: "memory64: an i64 index type at :34 — the module this action runs against",
			211: "memory64: an i64 index type at :34 — the module this action runs against",
			212: "memory64: an i64 index type at :34 — the module this action runs against",
			213: "memory64: an i64 index type at :34 — the module this action runs against",
			214: "memory64: an i64 index type at :34 — the module this action runs against",
			215: "memory64: an i64 index type at :34 — the module this action runs against",
			216: "memory64: an i64 index type at :34 — the module this action runs against",
			217: "memory64: an i64 index type at :34 — the module this action runs against",
			218: "memory64: an i64 index type at :34 — the module this action runs against",
			219: "memory64: an i64 index type at :34 — the module this action runs against",
			220: "memory64: an i64 index type at :34 — the module this action runs against",
			221: "memory64: an i64 index type at :34 — the module this action runs against",
			222: "memory64: an i64 index type at :34 — the module this action runs against",
			223: "memory64: an i64 index type at :34 — the module this action runs against",
			224: "memory64: an i64 index type at :34 — the module this action runs against",
			225: "memory64: an i64 index type at :34 — the module this action runs against",
			226: "memory64: an i64 index type at :34 — the module this action runs against",
			227: "memory64: an i64 index type at :34 — the module this action runs against",
			228: "memory64: an i64 index type at :34 — the module this action runs against",
			229: "memory64: an i64 index type at :34 — the module this action runs against",
			230: "memory64: an i64 index type at :34 — the module this action runs against",
			231: "memory64: an i64 index type at :34 — the module this action runs against",
			232: "memory64: an i64 index type at :34 — the module this action runs against",
			233: "memory64: an i64 index type at :34 — the module this action runs against",
			234: "memory64: an i64 index type at :34 — the module this action runs against",
			235: "memory64: an i64 index type at :34 — the module this action runs against",
			236: "memory64: an i64 index type at :34 — the module this action runs against",
			237: "memory64: an i64 index type at :34 — the module this action runs against",
			238: "memory64: an i64 index type at :34 — the module this action runs against",
			239: "memory64: an i64 index type at :34 — the module this action runs against",
			240: "memory64: an i64 index type at :34 — the module this action runs against",
			241: "memory64: an i64 index type at :34 — the module this action runs against",
			242: "memory64: an i64 index type at :34 — the module this action runs against",
			243: "memory64: an i64 index type at :34 — the module this action runs against",
			244: "memory64: an i64 index type at :34 — the module this action runs against",
			245: "memory64: an i64 index type at :34 — the module this action runs against",
			246: "memory64: an i64 index type at :34 — the module this action runs against",
			247: "memory64: an i64 index type at :34 — the module this action runs against",
			248: "memory64: an i64 index type at :34 — the module this action runs against",
			249: "memory64: an i64 index type at :34 — the module this action runs against",
			250: "memory64: an i64 index type at :34 — the module this action runs against",
			251: "memory64: an i64 index type at :34 — the module this action runs against",
			252: "memory64: an i64 index type at :34 — the module this action runs against",
			253: "memory64: an i64 index type at :34 — the module this action runs against",
			254: "memory64: an i64 index type at :34 — the module this action runs against",
			255: "memory64: an i64 index type at :34 — the module this action runs against",
			256: "memory64: an i64 index type at :34 — the module this action runs against",
			257: "memory64: an i64 index type at :34 — the module this action runs against",
			258: "memory64: an i64 index type at :34 — the module this action runs against",
			259: "memory64: an i64 index type at :34 — the module this action runs against",
			260: "memory64: an i64 index type at :34 — the module this action runs against",
			261: "memory64: an i64 index type at :34 — the module this action runs against",
			262: "memory64: an i64 index type at :34 — the module this action runs against",
			263: "memory64: an i64 index type at :34 — the module this action runs against",
			264: "memory64: an i64 index type at :34 — the module this action runs against",
			265: "memory64: an i64 index type at :34 — the module this action runs against",
			268: "memory64: (memory i64 1) at :34 — an i64 index type",
			269: "memory64: (memory i64 1) at :34 — an i64 index type",
		},
		// **Eight lines that arrived by a *frontier* being withdrawn, which is a provenance no other
		// entry in this map has** (#413). They were fail rows until the encoder learned section 8:
		// `start0.wast`'s module carries `(start $main)`, so the whole module was refused before its
		// memargs were ever decoded, and the multi-memory decline underneath was unreachable. Writing
		// the start section moves the refusal one layer down and the eight dependents from `fail` to
		// here — the module command itself keeping its pass on an instantiation decline (#124's ruling,
		// stated at the `KindModuleText` arm).
		//
		// So a first-blocker census over-predicts *passes* even when it is exactly right about
		// *fails*, and this is the specimen: #413's forecast said `start0.wast` 0/9 → 9/9 and the
		// board delivered 1 pass + 8 gated. The fail forecast was exact (91 → 56) because all 35 rows
		// did leave that column; the pass forecast missed by exactly these 8 because a *second* blocker
		// can be a gate, which moves a row to a third column rather than back to fail. The all-on lane
		// is what proves the attribution rather than asserting it: it gained the full 35.
		"start0.wast": {
			32: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			33: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			35: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			36: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			37: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			39: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			40: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			41: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
		},
		"store0.wast": {
			22: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			23: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			24: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			25: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
		},
		"store1.wast": {
			49: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			50: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			51: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			52: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
		},
		"store2.wast": {
			43: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			44: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			45: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			46: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			47: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			48: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			49: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			50: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			51: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			52: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			53: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			55: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			56: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			57: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			58: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			59: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			60: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			61: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			62: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			63: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			64: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
			65: "multi-memory: 2 memories (1 imported) at :6, so a memarg carries flags bit 6",
		},
		// **Every command's own line, since #196/#197** — the file is one module (a single
		// `(table $t64 i64 0 externref)` at :1), so once the boundary can accept and produce
		// externref arguments/results every action against it reaches the same memory64 gate
		// check, not only the six lines a pre-#196/#197 harness (which refused every
		// externref-typed action outright) could reach. Listed exhaustively rather than as a
		// range, matching this allowlist's own style elsewhere.
		"table_grow64.wast": {
			12: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
			13: "memory64: an i64 index type at :1 — the module this action runs against",
			14: "memory64: an i64 index type at :1 — the module this action runs against",
			16: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
			17: "memory64: an i64 index type at :1 — the module this action runs against",
			18: "memory64: an i64 index type at :1 — the module this action runs against",
			19: "memory64: an i64 index type at :1 — the module this action runs against",
			20: "memory64: an i64 index type at :1 — the module this action runs against",
			21: "memory64: an i64 index type at :1 — the module this action runs against",
			22: "memory64: an i64 index type at :1 — the module this action runs against",
			24: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
			25: "memory64: an i64 index type at :1 — the module this action runs against",
			26: "memory64: an i64 index type at :1 — the module this action runs against",
			27: "memory64: an i64 index type at :1 — the module this action runs against",
			28: "memory64: an i64 index type at :1 — the module this action runs against",
			29: "memory64: an i64 index type at :1 — the module this action runs against",
			30: "memory64: an i64 index type at :1 — the module this action runs against",
			31: "memory64: an i64 index type at :1 — the module this action runs against",
			32: "memory64: an i64 index type at :1 — the module this action runs against",
			33: "memory64: an i64 index type at :1 — the module this action runs against",
			34: "memory64: an i64 index type at :1 — the module this action runs against",
		},

		// # The element section's and call_indirect's 19, four of them behind a feature this
		// # engine has never met
		//
		// Section 9 plus `call_indirect` (#8, 0016) taught the encoder to write element segments
		// and the two indirect-call opcodes, so **637 vectors that used to stop at `cannot yet
		// encode` now reach a verdict**: 618 became passes and 19 reached a gate. The columns
		// close exactly, which is the check rather than the claim: fail 4319 → 3682 is −637,
		// pass 26833 → 27451 is +618, gated 1535 → 1554 is +19, and 618 + 19 = 637.
		//
		// Every one of the 19 was a **fail** at 165e77e, quoting one of two encoder refusals —
		// `cannot yet encode the call_indirect instruction (#8)` for seven of them and `cannot
		// yet encode this (elem …) field` for twelve. That is the same movement the memarg and
		// data-section batches above record: the emitter is right and the decoder is configured
		// not to read what it correctly wrote.
		//
		// **`extended-const` is new to this list, and it is the one reason here that is not
		// about a table.** `elem.wast:1057` and its three siblings put `(i32.add (i32.const 1)
		// (i32.const 2))` in an element segment's offset — an arithmetic constant expression,
		// which is the extended-const proposal's whole content — so the offset is what the gate
		// declines, not the segment. Worth separating because the file is `elem.wast` and the
		// vectors are `call_in_table` invokes: neither the file's name nor the vector's shape
		// names the feature, and only the error string does.
		//
		// Read from **the decoder with every gate on** and printed, never from the source text
		// (grave #129): `len(m.Tables)`, a count of `Limits.Addr64`, and `len(m.Elems)`. The
		// four `elem.wast` modules are `tables=1 addr64=0 elems=1` — no i64 anywhere, which is
		// how the extended-const attribution was separated from a memory64 guess — and all
		// eight `table_copy64.wast` modules are `tables=2 addr64=2 elems=4`.
		//
		// **`imports.wast:97` and `:98` attribute to a module 62 lines earlier, and that was
		// checked rather than accepted.** A gated verdict inherited from a stale `curGated`
		// would be a harness defect wearing a gate's name, so the command list was printed:
		// between :35 and :97 this file has no other module command, and :35's head carries
		// `(tag (import "test" "tag-i32") (param i32))` — exception handling. The distance is
		// the module's length, not a leak.
		// The four extended-const lines join the `elem.wast` entry above rather than opening a
		// second one — one file, one map, or the reverse check reads a subset it cannot name.
		"imports.wast": {
			// The "test" module (:3-17) carries three *defined* tag fields —
			// `(tag (export "tag"))`, `$tag-i32`, and the export-sugar
			// `(tag (export "tag-f32") …)` — so `register` itself declines, one line after the
			// module closes. #199's rung 1: before its encoder retention landed, `(tag …)`
			// fields refused at the text→binary bridge and this vector never reached the
			// decoder at all.
			19: "exception handling: (tag …) fields at :15-17 — the module this register runs against",
			97: "exception handling: (tag (import \"test\" \"tag-i32\") …) at :35",
			98: "exception handling: (tag (import \"test\" \"tag-i32\") …) at :35",
			// Five assert_unlinkable vectors whose module under test carries a `(tag …)`
			// field — the registry's arm made them askable, and the gate declines them one
			// layer earlier than the linker would. See the batch note below.
			239: "exception handling: (tag (import \"test\" \"unknown\")) — the module under test",
			243: "exception handling: (tag (import \"test\" \"tag\") (param f32)) — the module under test",
			247: "exception handling: (tag (import \"test\" \"tag-i32\")) — the module under test",
			251: "exception handling: (tag (import \"test\" \"tag-i32\") (param f32)) — the module under test",
			255: "exception handling: (tag (import \"test\" \"func-i32\") (param f32)) — the module under test",
		},
		"call_indirect64.wast": {
			26: "memory64: an i64-indexed table at :3 — tables=1 addr64=1, all gates on",
		},
		"table_copy64.wast": {
			1694: "memory64: an i64 index type at :1671 — the module this action runs against",
			1719: "memory64: an i64 index type at :1696 — the module this action runs against",
			1744: "memory64: an i64 index type at :1721 — the module this action runs against",
			1769: "memory64: an i64 index type at :1746 — the module this action runs against",
			1794: "memory64: (table $t0 i64 30 30 funcref) at :1771 — tables=2 addr64=2",
			1819: "memory64: (table $t0 i64 30 30 funcref) at :1796 — tables=2 addr64=2",
			1844: "memory64: an i64 index type at :1821 — the module this action runs against",
			1869: "memory64: (table $t0 i64 30 30 funcref) at :1846 — tables=2 addr64=2",
			1894: "memory64: an i64 index type at :1871 — the module this action runs against",
			1919: "memory64: (table $t0 i64 30 30 funcref) at :1896 — tables=2 addr64=2",
			1944: "memory64: an i64 index type at :1921 — the module this action runs against",
			1969: "memory64: an i64 index type at :1946 — the module this action runs against",
			1994: "memory64: an i64 index type at :1971 — the module this action runs against",
			2019: "memory64: an i64 index type at :1996 — the module this action runs against",
			2044: "memory64: an i64 index type at :2021 — the module this action runs against",
			2069: "memory64: (table $t0 i64 30 30 funcref) at :2046 — tables=2 addr64=2",
			2094: "memory64: (table $t0 i64 30 30 funcref) at :2071 — tables=2 addr64=2",
			2119: "memory64: an i64 index type at :2096 — the module this action runs against",
			2144: "memory64: (table $t0 i64 30 30 funcref) at :2121 — tables=2 addr64=2",
			2169: "memory64: an i64 index type at :2146 — the module this action runs against",
			2194: "memory64: (table $t0 i64 30 30 funcref) at :2171 — tables=2 addr64=2",
			2219: "memory64: an i64 index type at :2196 — the module this action runs against",
		},
		// **New entry, since #196/#197.** One module (`(table $t 10 externref)` at :2,
		// `(table $t64 i64 10 externref)` at :15), so every one of the file's 70 commands
		// crosses the boundary with an externref argument or result and reaches this
		// memory64 gate together — none reachable at all before #196/#197, all gated now.
		// Listed exhaustively per this allowlist's own style; verified against the file's
		// full command-line population (grep for assert_return/assert_trap/bare invoke),
		// not assumed from a range.
		"table_fill64.wast": {
			27:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			28:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			29:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			30:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			31:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			33:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			34:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			35:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			36:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			37:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			38:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			40:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			41:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			42:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			43:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			44:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			46:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			47:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			48:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			49:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			51:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			52:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			53:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			54:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			56:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			57:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			58:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			60:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			61:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			63:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			67:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			68:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			69:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			71:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			76:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			83:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			84:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			85:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			86:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			87:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			89:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			90:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			91:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			92:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			93:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			94:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			96:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			97:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			98:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			99:  "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			100: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			102: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			103: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			104: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			105: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			107: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			108: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			109: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			110: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			112: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			113: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			114: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			116: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			117: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			119: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			123: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			124: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			125: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			127: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
			132: "memory64: (table $t64 i64 10 externref) at :15 — the module this action runs against",
		},
		"table_get64.wast": {
			// **:24, :26, :27, :29 join since #196/#197** — `init`/`get-externref`/`get-funcref`
			// pass or return a reference through the boundary, which a pre-#196/#197 harness
			// refused before ever reaching this memory64 module's own gate.
			24: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			26: "memory64: an i64 index type at :1 — the module this action runs against",
			27: "memory64: an i64 index type at :1 — the module this action runs against",
			29: "memory64: an i64 index type at :1 — the module this action runs against",
			30: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			31: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			33: "memory64: an i64 index type at :1 — the module this action runs against",
			34: "memory64: an i64 index type at :1 — the module this action runs against",
			35: "memory64: an i64 index type at :1 — the module this action runs against",
			36: "memory64: an i64 index type at :1 — the module this action runs against",
		},
		// **The 167 arrivals from the encoder's global section (#8).** These four files
		// have no prior entry: every one of their scorable commands was `fail` or
		// `unsupported` before the (global …) field and ref.null's heaptype landed, and
		// draining that bucket carried them forward to the *next* frontier — a feature
		// gate. So the +167 gated is a measured consequence of pass +947, not a board
		// getting quietly emptier: fail 3682 → 2568 = pass +947 + gated +167.
		//
		// The features are **probed, not inferred**. This control keys on `decode(c.Module)`
		// and, as its own note above says, cannot see these — they are `curGated`
		// propagations from a root module the harness declined, so asking the decoder here
		// under-matches. A throwaway walker printed each root's *reported* gate string, and
		// the four roots resolved to three distinct features; the module facts quoted below
		// were then read out of the vectors. Two guesses made while writing this entry were
		// wrong and caught by that reading — `table_size64`'s first table is `externref`,
		// not funcref, and `global.wast:3` is the module's opening line, not its
		// extended-const initializer. Which is the print-don't-trust rule earning its keep
		// on an allowlist reason rather than on an error message.
		//
		// All three gates are *right*, so these are allowlisted rather than treated as
		// over-gating; all 167 are simultaneously answered on the merits in the all-on lane
		// (allOnPassFloor 28497 → 29516), so the deferral cannot become a disappearance.
		"global.wast": {
			204: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			205: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			208: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			209: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			210: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			211: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			212: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			213: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			214: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			215: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			217: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			218: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			219: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			220: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			222: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			223: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			225: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			226: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			228: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			229: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			230: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			231: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			233: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			234: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			237: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			238: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			239: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			240: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			243: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			244: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			245: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			247: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			248: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			249: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			251: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			252: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			253: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			255: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			256: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			258: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			259: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			261: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			262: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			263: "extended-const: an arithmetic constant expression at :3 — the module this action runs against",
			265: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			266: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			267: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			268: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			270: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			272: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			273: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			274: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			276: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			277: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			278: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			280: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			281: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			282: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			// **:206, :207, :235, :241 join since #196/#197** — `get-r`/`get-mr`/`set-mr` cross
			// the boundary with an externref, which a pre-#196/#197 harness refused before ever
			// reaching this same module's own extended-const gate.
			206: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			207: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			235: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
			241: "extended-const: (global $z3 i32 (i32.add …)) at :19, in the module at :3",
		},
		"load2.wast": {
			162: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			164: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			165: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			166: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			168: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			169: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			170: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			172: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			174: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			175: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			176: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			178: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			179: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			180: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			182: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			183: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			184: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			186: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			187: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			188: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			189: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			191: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			192: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			193: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			195: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			196: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			197: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			198: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			199: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			200: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			202: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			204: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			205: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			207: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			209: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			210: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			212: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
		},
		"load64.wast": {
			161: "memory64: (memory i64 1) at :4 — an i64 index type",
			163: "memory64: (memory i64 1) at :4 — an i64 index type",
			164: "memory64: (memory i64 1) at :4 — an i64 index type",
			165: "memory64: (memory i64 1) at :4 — an i64 index type",
			167: "memory64: (memory i64 1) at :4 — an i64 index type",
			168: "memory64: (memory i64 1) at :4 — an i64 index type",
			169: "memory64: (memory i64 1) at :4 — an i64 index type",
			171: "memory64: (memory i64 1) at :4 — an i64 index type",
			173: "memory64: (memory i64 1) at :4 — an i64 index type",
			174: "memory64: (memory i64 1) at :4 — an i64 index type",
			175: "memory64: (memory i64 1) at :4 — an i64 index type",
			177: "memory64: (memory i64 1) at :4 — an i64 index type",
			178: "memory64: (memory i64 1) at :4 — an i64 index type",
			179: "memory64: (memory i64 1) at :4 — an i64 index type",
			181: "memory64: (memory i64 1) at :4 — an i64 index type",
			182: "memory64: (memory i64 1) at :4 — an i64 index type",
			183: "memory64: (memory i64 1) at :4 — an i64 index type",
			185: "memory64: (memory i64 1) at :4 — an i64 index type",
			186: "memory64: (memory i64 1) at :4 — an i64 index type",
			187: "memory64: (memory i64 1) at :4 — an i64 index type",
			188: "memory64: (memory i64 1) at :4 — an i64 index type",
			190: "memory64: (memory i64 1) at :4 — an i64 index type",
			191: "memory64: (memory i64 1) at :4 — an i64 index type",
			192: "memory64: (memory i64 1) at :4 — an i64 index type",
			194: "memory64: (memory i64 1) at :4 — an i64 index type",
			195: "memory64: (memory i64 1) at :4 — an i64 index type",
			196: "memory64: (memory i64 1) at :4 — an i64 index type",
			197: "memory64: (memory i64 1) at :4 — an i64 index type",
			198: "memory64: (memory i64 1) at :4 — an i64 index type",
			199: "memory64: (memory i64 1) at :4 — an i64 index type",
			201: "memory64: (memory i64 1) at :4 — an i64 index type",
			203: "memory64: (memory i64 1) at :4 — an i64 index type",
			204: "memory64: (memory i64 1) at :4 — an i64 index type",
			206: "memory64: (memory i64 1) at :4 — an i64 index type",
			208: "memory64: (memory i64 1) at :4 — an i64 index type",
			209: "memory64: (memory i64 1) at :4 — an i64 index type",
			211: "memory64: (memory i64 1) at :4 — an i64 index type",
		},
		"table_size64.wast": {
			26: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			27: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			28: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			29: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			30: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			31: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			32: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			34: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			35: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			36: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			37: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			38: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			39: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			40: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			42: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			43: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			44: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			45: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			46: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			47: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			48: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			49: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			50: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			51: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			52: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			54: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			55: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			56: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			57: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			58: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			59: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			60: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			61: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			62: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			63: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
			64: "memory64: (table $t0 i64 0 externref) at :2 — tables=4 addr64=4",
		},
		// **14 lines join since #196/#197** — `get-externref`/`set-externref`/`get-funcref`/
		// `set-funcref` all cross the boundary with a reference argument or result, which a
		// pre-#196/#197 harness refused before ever reaching this memory64 module's own gate;
		// `set-funcref-from`/`is_null-funcref` do not (their arguments/results are all i64/i32)
		// and were already gated.
		"table_set64.wast": {
			29: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			30: "memory64: an i64 index type at :1 — the module this action runs against",
			31: "memory64: an i64 index type at :1 — the module this action runs against",
			32: "memory64: an i64 index type at :1 — the module this action runs against",
			33: "memory64: an i64 index type at :1 — the module this action runs against",
			35: "memory64: an i64 index type at :1 — the module this action runs against",
			36: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			37: "memory64: (table $t2 i64 2 externref) at :1 — tables=2 addr64=2",
			38: "memory64: an i64 index type at :1 — the module this action runs against",
			39: "memory64: an i64 index type at :1 — the module this action runs against",
			41: "memory64: an i64 index type at :1 — the module this action runs against",
			42: "memory64: an i64 index type at :1 — the module this action runs against",
			43: "memory64: an i64 index type at :1 — the module this action runs against",
			44: "memory64: an i64 index type at :1 — the module this action runs against",
			46: "memory64: an i64 index type at :1 — the module this action runs against",
			47: "memory64: an i64 index type at :1 — the module this action runs against",
			48: "memory64: an i64 index type at :1 — the module this action runs against",
			49: "memory64: an i64 index type at :1 — the module this action runs against",
		},
		// **These two files' original three entries each stayed exactly as they were** — a
		// `call_ref`/`return_call_ref` opcode at a position no reftype touches, gated on
		// function-references regardless of this PR — and gained a much larger batch below
		// them: decision 0018's encoder-side implementation (`valTypeBytes`) makes the module
		// headers at :1/:3 (a `(ref $t)`/`(ref null $t)` parameter or result somewhere in the
		// signature space) reach the decoder for the first time, where GC's gate declines them.
		// Both batches are real and independent — one opcode-shaped, one type-shaped — verified by
		// reading representative module heads rather than assumed from the count matching the
		// probe.
		"call_ref.wast": {
			94:  "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			95:  "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			97:  "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			99:  "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			100: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			101: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			102: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			103: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			104: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			105: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			106: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			111: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			112: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			113: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			114: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			115: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			117: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			118: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			119: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			120: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			121: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			122: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			123: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			124: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :1",
			136: "function-references: call_ref / return_call_ref at :129 — the module this action runs against",
			149: "function-references: call_ref / return_call_ref at :138 — the module this action runs against",
			165: "function-references: call_ref / return_call_ref at :151 — the module this action runs against",
		},
		"return_call_ref.wast": {
			168: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			169: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			170: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			171: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			173: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			174: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			175: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			176: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			178: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			179: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			180: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			181: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			183: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			185: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			186: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			187: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			188: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			193: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			194: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			195: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			197: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			198: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			199: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			200: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			201: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			202: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			203: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			204: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			205: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			206: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			207: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			208: "gc: feature gate disabled — (ref $t)/(ref null $t) in the module header at :3",
			306: "function-references: call_ref / return_call_ref at :299 — the module this action runs against",
			319: "function-references: call_ref / return_call_ref at :308 — the module this action runs against",
			334: "function-references: call_ref / return_call_ref at :321 — the module this action runs against",
		},
		"traps0.wast": {
			22: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			23: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			24: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			25: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			26: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			27: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			28: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			29: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			30: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			31: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			32: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			33: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			34: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
			35: "multi-memory: a memarg carrying flags bit 6 at :1 — the module this action runs against",
		},
		"unreached-valid.wast": {
			48: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			49: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			50: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			51: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			53: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			54: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			55: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			56: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
			58: "function-references: call_ref / return_call_ref at :1 — the module this action runs against",
		},

		// The six files below are decision 0021's encoder-side dividend (#8's struct/array
		// frontier closing): every one of these `assert_return`/`assert_trap`/`register`
		// commands used to be a *fail* — the module ahead of it refused to encode at all
		// ("a struct or array type's fields are not retained") — and now encodes clean and
		// meets an honest GC decline instead, one layer later. Measured against the fixed
		// board rather than assumed: `TestPhase1Files`'s before/after diff on this PR is what
		// found each line, and every one below traces to a struct or array declaration in the
		// module the command runs against.
		"ref_eq.wast": {
			// One module (:1-27) declaring GC struct/array types (`(sub (struct))`,
			// `(array i8)`) and a table of `(ref null eq)`. Every command below runs against
			// it, and it is the only module in the file — GC declines the module itself, not
			// any individual command.
			29:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			31:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			32:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			33:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			34:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			35:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			36:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			37:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			38:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			39:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			41:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			42:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			43:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			44:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			45:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			46:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			47:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			48:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			49:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			51:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			52:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			53:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			54:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			55:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			56:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			57:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			58:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			59:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			61:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			62:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			63:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			64:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			65:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			66:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			67:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			68:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			69:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			71:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			72:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			73:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			74:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			75:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			76:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			77:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			78:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			79:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			81:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			82:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			83:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			84:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			85:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			86:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			87:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			88:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			89:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			91:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			92:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			93:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			94:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			95:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			96:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			97:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			98:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			99:  "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			101: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			102: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			103: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			104: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			105: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			106: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			107: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			108: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			109: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			111: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			112: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			113: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			114: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			115: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			116: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			117: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			118: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
			119: "gc: (type $st (sub (struct))) at :2 — the module this action runs against",
		},
		"array_fill.wast": {
			// One module (:36-56) declaring `(type $arr8 (array i8))` and friends. Every
			// command below runs against it.
			59:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			62:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			65:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			68:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			71:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			72:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			73:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			74:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			77:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			78:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			79:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			80:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			81:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			84:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			85:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			86:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			87:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			88:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			91:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			92:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			93:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			94:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			97:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			98:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			99:  "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
			100: "gc: (type $arr8 (array i8)) at :37 — the module this action runs against",
		},
		"array.wast": {
			// #270's eight, and every one is a `(ref.array)`/`(ref.eq)` pair sitting *immediately
			// above* a group already listed below — same module, same gate, and the only reason they
			// were not here is that the harness could not read the expectation, so the command scored
			// `unsupported` and never reached a gate at all. The pairing is the check on the
			// attribution: 97/98 belong to the group at 99-104, 142/143 to 144-149, 202/203 to
			// 204-217, 276/277 to 278-279. A gate decline that arrived with a *different* module
			// citation than its neighbours would have meant #270 changed which module a vector runs
			// against, which it has no business doing.
			97:  "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			98:  "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			142: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			143: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			202: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			203: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			276: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			277: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			// Two unrelated modules, each declining for its own array type.
			99:  "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			100: "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			101: "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			103: "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			104: "gc: (type $vec (array f32)) at :61 — the module this action runs against",
			342: "gc: (type $t (array (mut i32))) at :332 — the module this action runs against",
			343: "gc: (type $t (array (mut i32))) at :332 — the module this action runs against",
			// GC's dividend on `array.new_fixed`'s `idx nat32` and the other array `idx idx`
			// mnemonics this PR retains: three more modules' `array.get`/`array.set`/`array.len`
			// vectors used to refuse to encode outright (their bodies carry `array.new_fixed` or
			// `array.new_data`/`array.new_elem`) and now encode clean, meeting an honest GC
			// decline instead — the same dividend shape as type-subtyping.wast's rows above.
			144: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			145: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			146: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			148: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			149: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
			204: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			205: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			206: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			207: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			209: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			210: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			211: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			212: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			214: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			216: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			217: "gc: struct/array opcode (0xfb prefix) — the module at :151 this action runs against",
			278: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			279: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			280: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			281: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			283: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			284: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			285: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			287: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			289: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
			290: "gc: struct/array opcode (0xfb prefix) — the module at :219 this action runs against",
		},
		"table_init.wast": {
			// `array.new_default $arr` inside the element segment's item expression, of a
			// `(type $arr (array (mut arrayref)))` declared at :2272 — the module this action
			// runs against.
			2286: "gc: (type $arr (array (mut arrayref))) at :2272 — the module this action runs against",
		},
		"type-rec.wast": {
			141: "gc: (type (struct)) in a rec group at :138 — the module this action runs against",
			148: "gc: (type (struct)) in a rec group at :144 — the module this action runs against",
			174: "gc: (type (struct)) in a rec group at :167 — the module this action runs against",
			183: "gc: (type (struct)) in a rec group at :176 — the module this action runs against",
			192: "gc: (type (struct)) in a rec group at :185 — the module this action runs against",
		},
		// # The bulk-segment batch's 244, every one of which was a *fail* an hour ago
		//
		// `table.init` and `memory.init` landed on both sides at once — the encoder learned the
		// two-index pair and the interpreter grew the four arms plus segment instances (#8, #7)
		// — so 1458 vectors that used to stop at `cannot yet encode` reached a verdict and 244
		// of them reached a gate instead. The columns close, which is the check rather than the
		// claim: fail 3401 → 1699 is −1702, pass 31898 → 33356 is +1458, gated 2264 → 2508 is
		// +244, and 1458 + 244 = 1702.
		//
		// **Their prior verdict was measured, not assumed.** Each of the 244 was scored at the
		// parent revision and every one was a `fail` quoting one of exactly two strings:
		// `cannot yet encode memory.init (#8)` for 151 and `cannot yet encode table.init (#8)`
		// for 93. So this is the same movement the memarg, data-section, block-family and
		// element-section batches above record — the emitter is right and the decoder is
		// configured not to read what it correctly wrote — and none of the 244 is a pass that
		// went quiet.
		//
		// **The feature strings are probed, and the field attribution is the part that needed
		// it.** Every reason here is derived from the decline the harness actually reported at
		// the module command each line runs against, so the two features are the engine's own
		// words rather than a guess from the filename: `memory64: feature gate disabled` for
		// 232 and `memarg flags bit 6, an explicit memory index — decodeMemop` for 12. The
		// second is the one a filename would have got wrong. `memory_init0.wast` is named for
		// `memory.init`, whose immediates are *not* a memarg — it is gated because the module
		// declares **four** memories, so the `i32.load8_u $mem2` in its read-back function
		// carries flags bit 6. The feature is multi-memory and the instruction that trips it is
		// a load, neither of which the file's name or the vector's shape says.
		//
		// **The declaring field is chosen by which one carries `i64`, not by document order.**
		// `table_init64.wast:385` declares `(table $t0 30 30 funcref)` first and the i64-indexed
		// `(table $t2 i64 30 30 funcref)` third, so a first-match extraction would have named a
		// table that is not why the module is declined — the same shape as the extended-const
		// attribution above, where the file is `elem.wast` and the feature is the offset's.
		//
		// Two of the four files are new here and two merge into existing blocks (`bulk64.wast`,
		// `memory_init64.wast`) — one file, one map, or the reverse check reads a subset it
		// cannot name.
		//
		// All 244 answer on the merits in the all-gates-on lane, which is what keeps the parked
		// verdict earned rather than deferred: TestAllGatesOnLeavesNothingGated is the control,
		// and its floor moves in this PR for exactly these vectors.
		"memory_init0.wast": {
			19: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			20: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			21: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			22: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			25: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			28: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
			30: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			31: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			34: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			35: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			38: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
			40: "multi-memory: a memarg carrying flags bit 6 at :2 — the module this action runs against",
		},
		"table_init64.wast": {
			412: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			413: "memory64: an i64 index type at :385 — the module this action runs against",
			414: "memory64: an i64 index type at :385 — the module this action runs against",
			415: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			416: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			417: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			418: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			419: "memory64: an i64 index type at :385 — the module this action runs against",
			420: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			421: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			422: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			423: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			424: "memory64: an i64 index type at :385 — the module this action runs against",
			425: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			426: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			427: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			428: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			429: "memory64: (table $t2 i64 30 30 funcref) at :385 — an i64 index type",
			430: "memory64: an i64 index type at :385 — the module this action runs against",
			431: "memory64: an i64 index type at :385 — the module this action runs against",
			432: "memory64: an i64 index type at :385 — the module this action runs against",
			433: "memory64: an i64 index type at :385 — the module this action runs against",
			434: "memory64: an i64 index type at :385 — the module this action runs against",
			435: "memory64: an i64 index type at :385 — the module this action runs against",
			436: "memory64: an i64 index type at :385 — the module this action runs against",
			437: "memory64: an i64 index type at :385 — the module this action runs against",
			438: "memory64: an i64 index type at :385 — the module this action runs against",
			439: "memory64: an i64 index type at :385 — the module this action runs against",
			440: "memory64: an i64 index type at :385 — the module this action runs against",
			441: "memory64: an i64 index type at :385 — the module this action runs against",
			442: "memory64: an i64 index type at :385 — the module this action runs against",
			471: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			472: "memory64: an i64 index type at :444 — the module this action runs against",
			473: "memory64: an i64 index type at :444 — the module this action runs against",
			474: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			475: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			476: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			477: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			478: "memory64: an i64 index type at :444 — the module this action runs against",
			479: "memory64: an i64 index type at :444 — the module this action runs against",
			480: "memory64: an i64 index type at :444 — the module this action runs against",
			481: "memory64: an i64 index type at :444 — the module this action runs against",
			482: "memory64: an i64 index type at :444 — the module this action runs against",
			483: "memory64: an i64 index type at :444 — the module this action runs against",
			484: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			485: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			486: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			487: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			488: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			489: "memory64: (table $t2 i64 30 30 funcref) at :444 — an i64 index type",
			490: "memory64: an i64 index type at :444 — the module this action runs against",
			491: "memory64: an i64 index type at :444 — the module this action runs against",
			492: "memory64: an i64 index type at :444 — the module this action runs against",
			493: "memory64: an i64 index type at :444 — the module this action runs against",
			494: "memory64: an i64 index type at :444 — the module this action runs against",
			495: "memory64: an i64 index type at :444 — the module this action runs against",
			496: "memory64: an i64 index type at :444 — the module this action runs against",
			497: "memory64: an i64 index type at :444 — the module this action runs against",
			498: "memory64: an i64 index type at :444 — the module this action runs against",
			499: "memory64: an i64 index type at :444 — the module this action runs against",
			500: "memory64: an i64 index type at :444 — the module this action runs against",
			501: "memory64: an i64 index type at :444 — the module this action runs against",
			538: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			539: "memory64: an i64 index type at :503 — the module this action runs against",
			540: "memory64: an i64 index type at :503 — the module this action runs against",
			541: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			542: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			543: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			544: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			545: "memory64: an i64 index type at :503 — the module this action runs against",
			546: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			547: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			548: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			549: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			550: "memory64: an i64 index type at :503 — the module this action runs against",
			551: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			552: "memory64: an i64 index type at :503 — the module this action runs against",
			553: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			554: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			555: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			556: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			557: "memory64: an i64 index type at :503 — the module this action runs against",
			558: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			559: "memory64: an i64 index type at :503 — the module this action runs against",
			560: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			561: "memory64: an i64 index type at :503 — the module this action runs against",
			562: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			563: "memory64: (table $t2 i64 30 30 funcref) at :503 — an i64 index type",
			564: "memory64: an i64 index type at :503 — the module this action runs against",
			565: "memory64: an i64 index type at :503 — the module this action runs against",
			566: "memory64: an i64 index type at :503 — the module this action runs against",
			567: "memory64: an i64 index type at :503 — the module this action runs against",
			568: "memory64: an i64 index type at :503 — the module this action runs against",
			// Decision 0021's dividend: `(type $arr (array (mut arrayref)))` at :2457, used in
			// an element segment's item expression — `table_init.wast`'s row, memory64's twin.
			2471: "gc: (type $arr (array (mut arrayref))) at :2457 — the module this action runs against",
		},

		// # The registry's 50, and the two partitions that have to agree
		//
		// 0017 Q1 gave the run loop a `register`/`assert_unlinkable`/`(invoke $M …)` triple, so
		// **50 commands that were previously fails or unaskable now reach a feature gate**. Every
		// one is a decline the engine already made elsewhere arriving at a *new* command shape:
		// no gate changed, three new arms did.
		//
		// **Two independent partitions, quoted because agreeing is the check.** By feature —
		// exception handling **7**, multi-memory **13**, memory64 **30**. By the arm that made the
		// command askable — `assert_unlinkable` **33**, `register` **7**, `(invoke $M …)` **10**.
		// Both sum to 50, and they cross-cut (memory64 supplies 26 unlinkables and 4 registers),
		// so a miscount in either would have to be matched by a compensating one in the other.
		// That is what the sum is for; a single total agrees with any story.
		//
		// **The gate strings are read from the run loop, not from the source text.** A throwaway
		// `gateProbe` hook on `Result.gate` printed the error every decline carried — grave #129's
		// rule, and it earned its keep here: the seven `register` declines report the *preceding
		// module's* string, which no amount of reading the register line would show. The modules
		// each entry names were then read out of the vector.
		//
		// **What is deliberately *not* in this list is the shape worth checking.** Four
		// `memory64-imports.wast` unlinkables (`:56`, `:64`, `:158`, `:166`) and
		// `linking1.wast:43` are 32-bit *importers* — nothing in the module under test is
		// declined, so they instantiate, meet a supplier whose register was gate-declined, and
		// fail honestly with `unknown import`. The board keeps them red on purpose: they are
		// answered on the merits in the all-on lane, which is what stops a deferral from becoming
		// a disappearance (0010). A version of this list that swept them in would have emptied
		// five vectors by fiat, and the tell is that the importer's own type is 32-bit.
		"tag.wast": {
			// The first module (:3-8) declares four *defined* tag fields — the anonymous
			// `(tag)`, and three more including the `$t3`/`export` pair — so `register` itself
			// declines them before this file's own `(import …)` rows even get a chance to.
			// #199's rung 1: before its encoder retention landed, `(tag …)` fields refused at
			// the text→binary bridge and this module never reached the decoder at all.
			11: "exception handling: four (tag …) fields at :4-7 — the module this register runs against",
			// The second module (:12-16) declares an exported `(tag (export "tag") (type $t1))`
			// inside a `rec` group; `register` is what needs it instantiated.
			38: "exception handling: (tag (export \"tag\") (type $t1)) at :35 — the module this register runs against",
			48: "exception handling: (tag (import \"M\" \"tag\") (type $t2)) — the module under test",
			59: "exception handling: (tag (import \"M\" \"tag\") (type $t)) — the module under test",
		},
		// `$Mm` at :1 declares three memories, so `i32.load8_u $mem1` in its exported `load`
		// carries memarg flags bit 6; `$Nm` at :14 has two (one imported) and the same shape.
		// The register at :12 inherits `$Mm`'s decline — a register whose module was declined is
		// gated rather than failed, which is the stratum ledger's finding and not a guess.
		"linking1.wast": {
			12: "multi-memory: 3 memories at :1, so `i32.load8_u $mem1` carries memarg flags bit 6",
			27: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
			28: "multi-memory: $Nm at :14 — 2 memories (1 imported), the module this action runs against",
			29: "multi-memory: $Nm at :14 — 2 memories (1 imported), the module this action runs against",
			40: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
			41: "multi-memory: $Nm at :14 — 2 memories (1 imported), the module this action runs against",
			42: "multi-memory: $Nm at :14 — 2 memories (1 imported), the module this action runs against",
		},
		"linking2.wast": {
			12: "multi-memory: 3 memories at :1, so `i32.load8_u $mem1` carries memarg flags bit 6",
		},
		"linking3.wast": {
			12: "multi-memory: 3 memories at :1, so `i32.load8_u $mem1` carries memarg flags bit 6",
			23: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
			36: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
			37: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
			49: "multi-memory: $Mm at :1 — 3 memories, the module this action runs against",
		},
		// Four registers whose supplier module declares an i64 table or memory, and 26
		// `assert_unlinkable` vectors whose **module under test** declares one. The split is the
		// importer's own index type: an i64 importer never reaches the linker, a 32-bit importer
		// does (see the four exclusions in the batch note above).
		"memory64-imports.wast": {
			11:  "memory64: (table (export \"table64-10-inf\") i64 10 funcref) at :10",
			13:  "memory64: (table (export \"table64-10-20\") i64 10 20 funcref) at :12",
			15:  "memory64: (memory (export \"memory64-2-inf\") i64 2) at :14",
			17:  "memory64: (memory (export \"memory64-2-4\") i64 2 4) at :16",
			36:  "memory64: (table i64 12 funcref) — the module under test",
			40:  "memory64: (table i64 10 20 funcref) — the module under test",
			44:  "memory64: (table i64 12 20 funcref) — the module under test",
			48:  "memory64: (table i64 10 18 funcref) — the module under test",
			52:  "memory64: (table i64 10 funcref) — the module under test, against a 32-bit supplier",
			60:  "memory64: (table i64 10 20 funcref) — the module under test, against a 32-bit supplier",
			82:  "memory64: (memory i64 0 1) — the module under test",
			86:  "memory64: (memory i64 0 2) — the module under test",
			90:  "memory64: (memory i64 0 3) — the module under test",
			94:  "memory64: (memory i64 2 3) — the module under test",
			98:  "memory64: (memory i64 3) — the module under test",
			102: "memory64: (memory i64 0 1) — the module under test",
			106: "memory64: (memory i64 0 2) — the module under test",
			110: "memory64: (memory i64 0 3) — the module under test",
			114: "memory64: (memory i64 2 2) — the module under test",
			118: "memory64: (memory i64 2 3) — the module under test",
			122: "memory64: (memory i64 3 3) — the module under test",
			126: "memory64: (memory i64 3 4) — the module under test",
			130: "memory64: (memory i64 3 5) — the module under test",
			134: "memory64: (memory i64 4 4) — the module under test",
			138: "memory64: (memory i64 4 5) — the module under test",
			142: "memory64: (memory i64 3) — the module under test",
			146: "memory64: (memory i64 4) — the module under test",
			150: "memory64: (memory i64 5) — the module under test",
			154: "memory64: (memory i64 2) — the module under test, against a 32-bit supplier",
			162: "memory64: (memory i64 2 4) — the module under test, against a 32-bit supplier",
		},

		// # The parameterized-reference-encoding batch — decision 0018's encoder-side
		// implementation, this PR
		//
		// `valTypeBytes` (text/encode.go) closes the frontier `encodableOrErr` used to refuse a
		// `(ref $t)`/`(ref null $t)` indexed reftype or a non-null abstract GC form (`(ref i31)`,
		// `(ref func)`) anywhere in a signature, a table's element type, or a global's type —
		// **489 vectors across 19 files, measured with the harness before this PR** (the
		// "parameterized reference encoding" bucket's exact size, `internal/spec`'s
		// `run(s).Buckets`, never a grep over board text). Of those 489, **401 land here as
		// honest GC declines** — the module now reaches the decoder for the first time, where the
		// GC gate declines it — and the remaining 88 are itemized in this PR's board report
		// (departures/arrivals accounting): 115 arrive in the sibling "struct or array type's
		// fields are not retained" bucket (a *different*, still-undecided frontier — 0019 —
		// this PR does not touch), and the "assert_unlinkable expected: incompatible import
		// type" bucket drops by 27 as those vectors move to a decline one command earlier in
		// the same script.
		//
		// **Every one of the 401 is verified by reading the module's own header**, not by
		// trusting that a batch this size shares one cause: each file's construct is named
		// below rather than a single reason repeated 401 times. All are GC — none is this PR's
		// own feature, since the encoder is right to write the bytes and the decoder is
		// configured not to read them with the gate off, exactly the SIMD/memory64 shape this
		// allowlist already documents elsewhere. Every one is answered on the merits in the
		// all-gates-on lane (`TestAllGatesOnLeavesNothingGated`), which is what keeps a decline
		// from becoming a disappearance.
		"br_on_non_null.wast": {
			49: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			51: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			52: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			53: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			54: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			55: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			86: "gc: (ref $t) as a func param, second module at :65",
			87: "gc: (ref $t) as a func param, second module at :65",
		},
		"br_on_null.wast": {
			32: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			34: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			35: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			36: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			65: "gc: (ref $t) as a func param, second module at :46",
			66: "gc: (ref $t) as a func param, second module at :46",
		},
		"ref_as_non_null.wast": {
			25: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			27: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			28: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			29: "gc: (ref $t) as a func param at :1 — the module this action runs against",
		},
		// One module, 15 exported functions each taking a `(ref $t)`, `(ref null $t)`, or
		// `funcref`-typed param, at :1 — read in full rather than assumed from its size, since
		// the file mixes GC-gated and ungated param types across its exports.
		"ref_is_null.wast": {
			// **:44, :45, :48, :50 join since #196/#197** — `funcref`/`externref`/`init` cross
			// the boundary with a reference, which a pre-#196/#197 harness refused before ever
			// reaching this same module's own gc gate (:1's `(ref $t)` func param).
			44: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			45: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			46: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			48: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			50: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			52: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			53: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			54: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			56: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			57: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			58: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			60: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			62: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			63: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			64: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			66: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			67: "gc: (ref $t) as a func param at :1 — the module this action runs against",
			68: "gc: (ref $t) as a func param at :1 — the module this action runs against",
		},
		// `(func (export "new") (result (ref i31)))` at :2 — the non-null abstract form, GC's own
		// value type, present from the file's first export. Two modules; the second (:61) and
		// third (:140) repeat the shape.
		"i31.wast": {
			// #270's one: `(ref.i31)` at :33, the file's only bare-pattern result.
			33:  "gc: (ref i31) result at :2 — the module this action runs against",
			35:  "gc: (ref i31) result at :2 — the module this action runs against",
			36:  "gc: (ref i31) result at :2 — the module this action runs against",
			37:  "gc: (ref i31) result at :2 — the module this action runs against",
			38:  "gc: (ref i31) result at :2 — the module this action runs against",
			39:  "gc: (ref i31) result at :2 — the module this action runs against",
			40:  "gc: (ref i31) result at :2 — the module this action runs against",
			41:  "gc: (ref i31) result at :2 — the module this action runs against",
			42:  "gc: (ref i31) result at :2 — the module this action runs against",
			44:  "gc: (ref i31) result at :2 — the module this action runs against",
			45:  "gc: (ref i31) result at :2 — the module this action runs against",
			46:  "gc: (ref i31) result at :2 — the module this action runs against",
			47:  "gc: (ref i31) result at :2 — the module this action runs against",
			48:  "gc: (ref i31) result at :2 — the module this action runs against",
			49:  "gc: (ref i31) result at :2 — the module this action runs against",
			50:  "gc: (ref i31) result at :2 — the module this action runs against",
			51:  "gc: (ref i31) result at :2 — the module this action runs against",
			53:  "gc: (ref i31) result at :2 — the module this action runs against",
			54:  "gc: (ref i31) result at :2 — the module this action runs against",
			56:  "gc: (ref i31) result at :2 — the module this action runs against",
			58:  "gc: (ref i31) result at :2 — the module this action runs against",
			59:  "gc: (ref i31) result at :2 — the module this action runs against",
			96:  "gc: (ref i31) result, second module at :61",
			97:  "gc: (ref i31) result, second module at :61",
			98:  "gc: (ref i31) result, second module at :61",
			99:  "gc: (ref i31) result, second module at :61",
			102: "gc: (ref i31) result, second module at :61",
			103: "gc: (ref i31) result, second module at :61",
			104: "gc: (ref i31) result, second module at :61",
			105: "gc: (ref i31) result, second module at :61",
			108: "gc: (ref i31) result, second module at :61",
			109: "gc: (ref i31) result, second module at :61",
			110: "gc: (ref i31) result, second module at :61",
			113: "gc: (ref i31) result, second module at :61",
			114: "gc: (ref i31) result, second module at :61",
			115: "gc: (ref i31) result, second module at :61",
			118: "gc: (ref i31) result, second module at :61",
			119: "gc: (ref i31) result, second module at :61",
			120: "gc: (ref i31) result, second module at :61",
			121: "gc: (ref i31) result, second module at :61",
			148: "gc: (ref i31) result, third module at :140",

			// #419: the module at :128 is `(table $t 3 3 (ref i31) (ref.i31 (global.get $g)))` — a
			// table initializer over an imported i32 global. The emitter writes the field now, so
			// these three reach the decoder, and here the gated construct **is** the `0x40` table
			// form: the import is a plain `i32`, the type section holds no GC type, and
			// `decodeTableForm` declines the 0x40 byte before it reads the `(ref i31)` element type.
			// Measured the same way as `table.wast`'s five — a module carrying nothing but a funcref
			// table with a spelled initializer is already gated — so the two files' reasons differ
			// because their modules do, not because the entries were written at different times.
			136: "gc: the 0x40 table form with an initializer at :130 — the module at :128 this action runs against",
			137: "gc: the 0x40 table form with an initializer at :130 — the module at :128 this action runs against",
			138: "gc: the 0x40 table form with an initializer at :130 — the module at :128 this action runs against",

			// #8: reftypeRetained's ref.cast now converts these — the module at :150 and :168
			// decode far enough to reach the GC gate rather than refusing at the encoder.
			164: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :150 this action runs against",
			165: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :150 this action runs against",
			166: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :150 this action runs against",
			203: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			204: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			205: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			206: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			209: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			210: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			211: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			212: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			215: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			216: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			217: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			220: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			221: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			222: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			225: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			226: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			227: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
			228: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :168 this action runs against",
		},
		// `(func $apply (param $f (ref $ii)) …)` at :4 — the indexed reftype as a func param,
		// present from the module's first non-type field. One module for the whole file.
		"return_call.wast": {
			101: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			102: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			103: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			104: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			106: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			107: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			108: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			109: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			111: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			112: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			113: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			114: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			116: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			117: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			118: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			119: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			124: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			125: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			126: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			128: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			129: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			130: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			131: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			132: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			133: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			134: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			135: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			136: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			137: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			138: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			139: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			140: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			141: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			// **:142 joins since #196/#197**, same reason as the rest of this module's entries.
			142: "gc: (ref $t) as a func param at :3 — the module this action runs against",
		},
		"return_call_indirect.wast": {
			247: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			248: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			249: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			250: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			252: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			254: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			255: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			256: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			257: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			259: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			260: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			261: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			262: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			264: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			265: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			266: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			267: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			268: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			269: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			270: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			271: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			272: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			274: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			275: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			276: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			277: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			278: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			279: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			281: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			282: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			283: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			285: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			286: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			287: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			288: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			290: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			291: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			292: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			293: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			294: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			295: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			296: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			297: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			298: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			299: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			300: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			301: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			303: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			304: "gc: (ref $t) as a func param at :3 — the module this action runs against",
			// **:305 joins since #196/#197**, same reason as the rest of this module's entries.
			305: "gc: (ref $t) as a func param at :3 — the module this action runs against",
		},
		// One giant module (:3-1063) exercising every `br_table` shape, including the four
		// `meet-funcref-*`/`meet-nullref`/`meet-multi-ref` exports whose blocks are typed
		// `(result (ref null $t))`/`(result (ref $t))` over `(table $t (ref null $t) (elem
		// $tf))` — the indexed reftype, both nullabilities, as a table type and a block result.
		"br_table.wast": {
			1068: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1069: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1070: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1071: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1073: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1074: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1075: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1076: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1078: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1079: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1080: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1081: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1082: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1083: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1085: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1086: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1087: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1088: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1089: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1090: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1092: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1093: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1094: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1095: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1096: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1097: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1099: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1100: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1101: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1102: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1103: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1104: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1106: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1107: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1108: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1109: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1110: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1111: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1112: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1113: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1114: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1115: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1117: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1118: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1119: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1120: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1121: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1122: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1123: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1124: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1125: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1126: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1128: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1129: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1130: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1131: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1132: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1133: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1134: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1135: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1137: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1138: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1139: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1140: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1142: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1143: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1144: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1146: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1148: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1149: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1150: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1152: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1153: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1154: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1156: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1158: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1159: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1160: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1161: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1162: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1164: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1165: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1166: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1167: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1168: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1170: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1171: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1172: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1174: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1175: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1176: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1177: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1179: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1180: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1181: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1183: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1184: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1186: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1187: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1188: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1189: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1191: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1193: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1194: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1196: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1198: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1199: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1201: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1203: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1205: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1206: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1207: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1208: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1209: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1210: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1212: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1213: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1214: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1215: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1216: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1217: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1219: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1220: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1221: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1222: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1223: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1224: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1226: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1227: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1228: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1229: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1230: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1231: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1233: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1234: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1235: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1236: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1237: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1238: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1240: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1241: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1242: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1243: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1244: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1245: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1247: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			// The same `(ref null $t) table` module's `meet-externref`/`meet-funcref-*` exports,
			// reachable only since #196/#197 taught the boundary to accept and produce
			// reference-typed arguments/results — these 15 lines are members of the module
			// gated above at :1016-1063, not a new gate site.
			1249: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1250: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1251: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1253: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1254: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1255: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1256: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1257: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1258: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1259: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1260: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1261: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1262: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1263: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
			1264: "gc: (ref null $t) table type and block results at :1016-1063 — the module this action runs against",
		},
		// `(global (export "g") (ref $f) (ref.func $f))` at :2 — the indexed reftype as a global
		// type. Second occurrence at :426.
		"linking.wast": {
			110: "gc: (ref $f) as a global type at :2",
			137: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			141: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			145: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			150: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			154: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			158: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			163: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			167: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			171: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			175: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			198: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			202: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			206: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			210: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			215: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			219: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			223: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			227: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			232: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			236: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			240: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			244: "gc: (global $g (import \"M\" \"g\") (ref $dummy)) at :112",
			432: "gc: (ref $f) as a global type, second module at :426",
			450: "gc: (global $g (import \"M\" \"g\") (ref $dummy)), second occurrence at :434",
			454: "gc: (global $g (import \"M\" \"g\") (ref $dummy)), second occurrence at :434",
		},
		// The module registered under "M" at :86, whose `(global (export "g") (ref $f) (ref.func $f))`
		// carries an indexed reftype — so the `register` at :91 is declined with it.
		//
		// **The reason text said "a table element type" and the module has no table.** Under the
		// pinned corpus :86-:90 is a global, and the drift is the ordinary kind: this entry was
		// written when the section's subject was table initializers, and the construct it named came
		// from the section rather than from the line. Corrected here because #419 is the change that
		// reads it, and a `file:N` in prose has no resolver (#412) — the only thing that checks it is
		// someone standing where these five new rows put them.
		"table.wast": {
			91: "gc: (ref $f) as a global type at :87 — the module the register at :91 exports from",
			// The module at :93, whose five tables are the corpus's own table-initializer suite:
			// two spelled `(ref.func $dummy)`, two `(global.get $g)`, one plain. #419's emitter
			// writes the field, so these five `assert_return`s reach the decoder for the first time
			// and meet the GC gate there.
			//
			// **The gated construct is the imported global's reftype at :94, not the `0x40` table
			// form**, and the difference was measured rather than read off the section heading: a
			// module cut down to `(global $g (import "M" "g") (ref $dummy))` with *plain* tables is
			// already `gc: feature gate disabled`, and the import section precedes the table section
			// on the wire. What the table initializer changed is which layer refuses — the emitter
			// used to, and now the decoder does. Naming the 0x40 form here would have been the
			// slice's subject standing in for the module's first gated byte.
			112: "gc: (ref $dummy) on the imported global at :94 — the module at :93 this action runs against",
			113: "gc: (ref $dummy) on the imported global at :94 — the module at :93 this action runs against",
			114: "gc: (ref $dummy) on the imported global at :94 — the module at :93 this action runs against",
			115: "gc: (ref $dummy) on the imported global at :94 — the module at :93 this action runs against",
			116: "gc: (ref $dummy) on the imported global at :94 — the module at :93 this action runs against",
		},
		// Two files sharing one shape — `(ref $s0)`-style indexed reftypes threaded through
		// param/result signatures used to test structural type equivalence and subtyping, which
		// is exactly what makes GC's gate load-bearing for these files: the vectors exist to
		// exercise the indexed form.
		"type-equivalence.wast": {
			131: "gc: (ref $s0) in a param signature at :107 — the module this action runs against",
			156: "gc: (ref $s0) in a param signature, second module at :136",
			188: "gc: (ref $s0) in a param signature, third module at :161",
			199: "gc: (ref $s0) in a param signature, fourth module at :195",
			217: "gc: (ref $s0) in a param signature, fifth module at :208",
			237: "gc: (ref $s0) in a param signature, sixth module at :233",
			256: "gc: (ref $s0) in a param signature, seventh module at :246",
			278: "gc: (ref $s0) in a param signature, eighth module at :268",
			307: "gc: (ref $s0) in a param signature, ninth module at :290",
		},
		"type-subtyping.wast": {
			398: "gc: (ref $f) in a param/result signature at :373 — the module this action runs against",
			399: "gc: (ref $f) in a param/result signature at :373 — the module this action runs against",
			400: "gc: (ref $f) in a param/result signature at :373 — the module this action runs against",
			549: "gc: (ref $f) in a param/result signature, second module at :540",
			564: "gc: (ref $f) in a param/result signature, third module at :551",
			574: "gc: (ref $f) in a param/result signature, third module at :551",
			584: "gc: (ref $f) in a param/result signature, third module at :551",
			712: "gc: (ref $f) in a param/result signature, fourth module at :706",
			730: "gc: (ref $f) in a param/result signature, fifth module at :722",
			// The six rows below are decision 0021's dividend: each `register` runs against a
			// module declaring `(struct (field (ref $f)))` — a struct field naming a func type
			// forward- or self-referenced within the same `(rec …)` group, 0021's own worked
			// example — which used to refuse to encode outright and now encodes clean and meets
			// an honest GC decline instead.
			625: "gc: (struct (field (ref $f2))) at :621 — the module this action runs against",
			641: "gc: (struct (field (ref $f1) (ref $f2) …)) at :633 — the module this action runs against",
			658: "gc: (struct (field (ref $f1))) at :653 — the module this action runs against",
			659: "gc: (struct (field (ref $f1))) at :661 — the module this action runs against",
			674: "gc: (struct (field (ref $f1))) at :668 — the module this action runs against",
			692: "gc: (struct (field (ref $f1) (ref $f2) …)) at :683 — the module this action runs against",

			// #8: reftypeRetained's ref.test/ref.cast now convert these — the modules below now
			// decode far enough to reach the GC gate rather than refusing at the encoder.
			336: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			337: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			338: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			339: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			340: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			341: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			342: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :283 this action runs against",
			368: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :344 this action runs against",
			369: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :344 this action runs against",
			370: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :344 this action runs against",
			371: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :344 this action runs against",
			412: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :402 this action runs against",
			430: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :414 this action runs against",
			442: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :432 this action runs against",
			453: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :444 this action runs against",
			473: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :455 this action runs against",
			488: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :476 this action runs against",
			510: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :492 this action runs against",
			523: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :515 this action runs against",
			534: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :525 this action runs against",

			// **Arrived with 0019's own named gap** — the text encoder used to discard a
			// subtype's `sub`/`sub final` wrapper and its declared supertypes entirely
			// (`subtype`'s old doc comment: "the supertype list is read and discarded"), so a
			// module spelling `(sub …)` or `(sub final …)` round-tripped through the binary
			// format as a bare functype, invisible to the GC gate. Retaining `Final`/
			// `Supertypes` and emitting the wrapper (`encodeTypes`'s new arm) makes these
			// modules correctly decline under the default gate posture — the same direction as
			// the ruling on `data count section required` (#22): a cheap check that used to
			// pass the vector by not looking is now looking, and looking is right here because
			// `sub`/`rec` genuinely is GC's own grammar (decode.ml:262-276), gated since
			// decision 0008.
			600: "gc: register \"M2\" names the module at :594, which declares (sub (func)) and " +
				"(sub final (func))",
			602: "gc: (sub (func))/(sub final (func)) at :594 — the module this action runs against",
			610: "gc: (sub (func))/(sub final (func)) at :594 — the module this action runs against",
			751: "gc: register \"M10\" names the module at :746, which declares two (rec …) groups " +
				"of (sub …) types",
			752: "gc: (rec …) groups of (sub …) types at :746 — the module this action runs against",
			766: "gc: register \"M11\" names the module at :760, which declares three (rec …) " +
				"groups of (sub …) types",
			767: "gc: (rec …) groups of (sub …) types at :760 — the module this action runs against",
			971: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
			972: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
			973: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
			974: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
			975: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
			976: "gc: (sub $t1 (func))/(sub final $t2 (func)) at :954 — the module this action runs against",
		},
		// GC's dividend on six more files whose struct/array `idx idx`/`idx nat32` mnemonics this
		// PR retains: every one of these `struct.get`/`struct.set`/`array.*` (0xfb-prefixed)
		// vectors used to refuse to encode outright and now encodes clean, meeting an honest GC
		// decline instead — the same dividend shape as type-subtyping.wast's six rows above, and
		// as `array.wast`'s new rows, one PR later on the instruction side.
		"array_copy.wast": {
			97:  "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			98:  "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			101: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			102: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			105: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			106: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			109: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			110: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			113: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			114: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			115: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			116: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			119: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			120: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			121: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			122: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			125: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			126: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			127: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			128: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			129: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			130: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			131: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			133: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			134: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			135: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			136: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			137: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			138: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
			139: "gc: struct/array opcode (0xfb prefix) — the module at :54 this action runs against",
		},
		"array_init_data.wast": {
			68:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			71:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			72:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			75:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			76:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			77:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			80:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			81:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			82:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			85:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			86:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			87:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			88:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			89:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			90:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			91:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			92:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			95:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			96:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			97:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			98:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			99:  "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			101: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			102: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			103: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			104: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			105: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			108: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			109: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			110: "gc: struct/array opcode (0xfb prefix) — the module at :31 this action runs against",
			200: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			201: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			202: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			203: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			204: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			205: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			207: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			208: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			209: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			210: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			211: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
			212: "gc: struct/array opcode (0xfb prefix) — the module at :113 this action runs against",
		},
		"array_init_elem.wast": {
			85:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			88:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			89:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			92:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			93:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			96:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			97:  "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			100: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			101: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			102: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			103: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			106: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			107: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			108: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			109: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			110: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			113: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			114: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			115: "gc: struct/array opcode (0xfb prefix) — the module at :44 this action runs against",
			144: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			145: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			148: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			149: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			150: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			151: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			154: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			155: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			156: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			157: "gc: struct/array opcode (0xfb prefix) — the module at :117 this action runs against",
			178: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
		},
		"array_new_data.wast": {
			// #270's four: the in-bounds `(ref.array)` results at :12-15, whose out-of-bounds
			// `assert_trap` neighbours at :18-21 were already here. The file's own structure is the
			// attribution — one module, its in-bounds cases expecting a reference and its out-of-bounds
			// cases expecting a trap, and only the reference half was unreadable.
			12:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			13:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			14:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			15:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			18:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			19:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			20:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			21:  "gc: struct/array opcode (0xfb prefix) — the module at :1 this action runs against",
			74:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			75:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			76:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			77:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			78:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			79:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			81:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			82:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			83:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			84:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			85:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			86:  "gc: struct/array opcode (0xfb prefix) — the module at :23 this action runs against",
			103: "gc: struct/array opcode (0xfb prefix) — the module at :89 this action runs against",
			118: "gc: struct/array opcode (0xfb prefix) — the module at :105 this action runs against",
			133: "gc: struct/array opcode (0xfb prefix) — the module at :120 this action runs against",
		},
		"array_new_elem.wast": {
			// #270's eight, the same in-bounds/out-of-bounds shape as `array_new_data.wast` above but
			// in both of this file's expression-style and function-style modules (:3 and :51).
			18:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			19:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			20:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			21:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			66:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			67:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			68:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			69:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			24:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			25:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			26:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			27:  "gc: struct/array opcode (0xfb prefix) — the module at :3 this action runs against",
			47:  "gc: struct/array opcode (0xfb prefix) — the module at :29 this action runs against",
			72:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			73:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			74:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			75:  "gc: struct/array opcode (0xfb prefix) — the module at :51 this action runs against",
			103: "gc: struct/array opcode (0xfb prefix) — the module at :77 this action runs against",
			121: "gc: struct/array opcode (0xfb prefix) — the module at :106 this action runs against",
		},
		"struct.wast": {
			// #188: the symbolic-field-name struct.get/struct.set resolution converts these six
			// from `fail` (encode-column "cannot yet encode a symbolic index on struct.get")
			// to askable — the module at :70 now decodes, and the GC gate (0xfb prefix, off by
			// default) is what declines the six assert_returns that invoke against it.
			// #270's one: `(ref.struct)` at :122, two lines above the six #188 recovered and in the
			// same module.
			122: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			124: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			125: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			126: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			127: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			129: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			130: "gc: struct/array opcode (0xfb prefix) — the module at :70 this action runs against",
			155: "gc: struct/array opcode (0xfb prefix) — the module at :145 this action runs against",
			156: "gc: struct/array opcode (0xfb prefix) — the module at :145 this action runs against",
			219: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			220: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			221: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			222: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			223: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			224: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			225: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			226: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			228: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
			229: "gc: struct/array opcode (0xfb prefix) — the module at :160 this action runs against",
		},

		// #8: reftypeRetained/brOnCastRetained convert these five files' assert_returns from
		// `fail` (encode-column "cannot yet encode the ref.test/ref.cast/br_on_cast(_fail)
		// instruction's immediates") to askable, and type-subtyping.wast's ref.test/ref.cast
		// assert_returns the same way — the GC gate (0xfb prefix, off by default) is what
		// declines them now that the modules they invoke against decode. Each line's "module at"
		// position is the nearest preceding top-level `(module …)` in the .wast file, read by
		// hand off the vector rather than assumed from proximity.
		"ref_test.wast": {
			// **:101 joins since #196/#197**, same module (:3) and gate as the rest of this
			// file's entries — its `init` setup invoke (:100, one line up) crosses the boundary
			// with an externref argument.
			101: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			103: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			104: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			105: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			106: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			107: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			108: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			109: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			110: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			112: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			113: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			114: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			115: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			116: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			117: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			118: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			119: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			121: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			122: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			123: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			124: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			125: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			126: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			127: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			128: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			130: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			131: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			132: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			133: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			134: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			135: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			136: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			137: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			139: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			140: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			141: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			142: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			143: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			144: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			145: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			146: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			148: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			149: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			150: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			151: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			152: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			153: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			154: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			155: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			157: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			158: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			159: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			161: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			162: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			163: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			165: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			166: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			167: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			168: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			169: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			170: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			172: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			173: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			174: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			175: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			176: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			177: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			329: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :182 this action runs against",
			330: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :182 this action runs against",
		},
		"ref_cast.wast": {
			// **:49 joins since #196/#197**, same reason as the rest of this module's entries.
			49:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			51:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			52:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			53:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			54:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			55:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			56:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			57:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			58:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			60:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			61:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			62:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			63:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			64:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			65:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			66:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			67:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			69:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			70:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			71:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			72:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			73:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			74:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			75:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			76:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			78:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			79:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			80:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			81:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			82:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			83:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			84:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			85:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			87:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			88:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			89:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			90:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			91:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			92:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			93:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			94:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			185: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :99 this action runs against",
			186: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :99 this action runs against",
		},
		"br_on_cast.wast": {
			// **:69 joins since #196/#197**, same reason as the rest of this module's entries.
			69:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			71:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			72:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			73:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			74:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			75:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			77:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			78:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			79:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			80:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			81:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			83:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			84:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			85:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			86:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			87:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			89:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			90:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			91:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			92:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			93:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			95:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			96:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			97:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			98:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			99:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			205: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :104 this action runs against",
			206: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :104 this action runs against",
		},
		"br_on_cast_fail.wast": {
			// **:69 joins since #196/#197**, same reason as the rest of this module's entries.
			69:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			71:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			72:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			73:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			74:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			75:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			77:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			78:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			79:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			80:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			81:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			83:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			84:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			85:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			86:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			87:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			89:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			90:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			91:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			92:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			93:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			95:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			96:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			97:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			98:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			99:  "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :3 this action runs against",
			220: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :104 this action runs against",
			221: "gc: ref.test/ref.cast/br_on_cast opcode (0xfb prefix) — the module at :104 this action runs against",
		},

		// Four new files, all since #196/#197 — none of their scorable commands could reach a
		// gate at all before the boundary accepted and produced reference-typed arguments and
		// results, so every line below is a new gate site rather than an addition to one.

		// One module (:1-35), GC throughout: `any`/`i31`/`struct`/`array` value types and the
		// `any.convert_extern`/`extern.convert_any` opcodes. The lines *not* listed here
		// (:39, :42, :53-56) stay Unsupported: they carry `ref.host`/`ref.i31`/`ref.struct`/
		// `ref.array`, forms readRefConst's own doc comment states are out of scope, so those
		// lines never reach the gate at all. Tracking for the widening is **#270**.
		//
		// **Grave #272. That list read `(:39, :41, :44, :51, :53-56)` until rung 5 slice 3 resolved it, and
		// three of those numbers are blank lines** — `:41`, `:44` and `:51` hold nothing, while the
		// real second member, `:42`'s `(ref.host 2)` argument, was missing. The enumeration was
		// therefore wrong in both directions at once and green throughout, no mechanism reading a
		// prose line number. It is the citation-list law's own specimen inside the allowlist that
		// exists to keep deferrals honest: measured now, `extern.wast` has 18 commands, 12 passing
		// all-on and 6 unsupported, and the six are `{39, 42, 53, 54, 55, 56}` — 11 gated entries
		// below plus 6 unsupported plus the module at :1 accounts for all 18, which is the check
		// the original list would have failed.
		"extern.wast": {
			// #270's six, and this file is the one where all six declining *shapes* appear together —
			// which is why it is the file the ADR forecast a pass-direction answer for. :39 is a
			// `(ref.host 1)` **result** and :42 a `(ref.host 2)` **argument**, the bare host identity
			// `script.ml:80` places at the `any` heaptype rather than wrapping in an `Extern.ExternRef`;
			// :53-56 are `(ref.i31)`, `(ref.struct)`, `(ref.array)` and a second `(ref.host 0)` against
			// `externalize-ii`, which internalizes what it just externalized, so the payload the
			// boundary used to drop is exactly what the expectation names.
			39: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			42: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			53: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			54: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			55: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			56: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			37: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			40: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			43: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			45: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			46: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			47: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			48: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			49: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			50: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			52: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
			57: "gc: any/i31/struct/array value types and any.convert_extern/extern.convert_any at :1 — the module this action runs against",
		},

		// `(param $p (ref extern))`/`(local $x (ref extern))` at :4,:5 etc — the indexed
		// non-null parameterized reftype, GC's own construct, in every one of this file's
		// four exported functions across its two modules (:3-17, :66-72).
		"local_init.wast": {
			21: "gc: (ref extern) parameter and local type at :4 — the module this action runs against",
			22: "gc: (ref extern) parameter and local type at :9 — the module this action runs against",
			23: "gc: (ref extern) parameter and local type at :14 — the module this action runs against",
			74: "gc: (ref extern) parameter and local type at :67 — the module this action runs against",
		},

		// Two modules. The first (:1-13) exports `anyref`/`funcref`/`exnref`/`externref`/`ref`
		// results and globals of the same shapes — `exnref`/`(ref null $t)` are GC's own forms,
		// so the whole module gates even though the *specific* line invoked may be a Wasm-2.0
		// shape (`funcref`/`externref`) on its own. The second module (:22-49) adds the four
		// `null*ref` abstract bottom heaptypes (`none`/`nofunc`/`noexn`/`noextern`), also GC's.
		"ref_null.wast": {
			16: "gc: exnref result/global and (ref null $t) result at :1-13 — the module this action runs against",
			17: "gc: exnref result/global and (ref null $t) result at :1-13 — the module this action runs against",
			18: "gc: exnref result/global and (ref null $t) result at :1-13 — the module this action runs against",
			19: "gc: exnref result/global and (ref null $t) result at :1-13 — the module this action runs against",
			20: "gc: exnref result/global and (ref null $t) result at :1-13 — the module this action runs against",
			55: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			56: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			57: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			58: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			59: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			60: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			61: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			62: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			63: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			64: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			65: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			66: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			67: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			68: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			69: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			70: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			71: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			72: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			73: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			74: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			75: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			76: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			77: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			78: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			79: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			80: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
			81: "gc: null/nofunc/noexn/noextern bottom heaptypes and (ref null $t) at :22-49 — the module this action runs against",
		},

		// # #199's rung 1 opened a third decline path for the EH gate — retention closed the
		// # encoder frontier that used to intercept these vectors as `fail` first
		//
		// Every entry below newly reaches the decoder's `exception handling: gate disabled` decline
		// because `try_table`/`(tag …)` now retain and encode instead of refusing at the text→binary
		// bridge — so a module that used to error out of `EncodeModule` before any byte was written
		// now produces real bytes the decoder correctly declines with the gate off. Verified by
		// reading each module: every one carries a `(tag …)` field, a `try_table` opcode, or both,
		// and none is asking a question the engine can answer with EH off. All are passed in the
		// all-gates-on lane (`allOnPassFloor`'s own move in this PR), so the parked verdict is earned
		// there rather than deferred everywhere. Measured rather than assumed: the board's `fail`
		// column moved by exactly the same 68 these entries total, pass unmoved (#199's own scope
		// statement — zero conversions, this PR is retention only).
		"instance.wast": {
			// # #426's six, and the step they decline at is not the one the forecast named
			//
			// The two script forms became askable here, and the pre-registration predicted the
			// **definitions** would gate on their tags. They do not: `$M` (:3) and `$N` (:109) read
			// and validate clean and both **pass**, because a definition's entire assertion is that
			// it is valid — it deliberately never instantiates, which is why upstream writes
			// `definition` for the 4 GiB memory in `memory.wast:8`. The EH gate is not reached until
			// an `instance` form asks for a live instance of a module carrying a tag.
			//
			// So the decline is instantiation-time, one command later than forecast, and the six
			// lines below are two generations of inheritance: three `instance` forms declined
			// directly, three `register` forms declined because the instance they name never came to
			// be. Written out per line rather than as a bulk count because the two generations are
			// two different claims, and a register inheriting from an instance that inherits from a
			// definition is exactly where a wrong-layer attribution would hide.
			10:  "exception handling: (tag (export \"tag\")) at :7 — instantiating the definition at :3, which is what this form asks for",
			11:  "exception handling: (tag (export \"tag\")) at :7 — instantiating the definition at :3, which is what this form asks for",
			12:  "exception handling: (tag (export \"tag\")) at :7 — the instance at :10 this register names was declined",
			13:  "exception handling: (tag (export \"tag\")) at :7 — the instance at :11 this register names was declined",
			125: "exception handling: (tag $tag) at :112 — instantiating the definition at :109, which is what this form asks for",
			126: "exception handling: (tag $tag) at :112 — the instance at :125 this register names was declined",
			// `$M` (:3-8) carries `(tag (export "tag"))`; the importing module (:15-49) both
			// imports two tag instances and writes a `try_table`/`throw` pair (:39-45) using them.
			54: "exception handling: (tag (export \"tag\")) at :7, and a try_table/throw pair at :39-45 — the module this action runs against",
			55: "exception handling: (tag (export \"tag\")) at :7, and a try_table/throw pair at :39-45 — the module this action runs against",
			56: "exception handling: (tag (export \"tag\")) at :7, and a try_table/throw pair at :39-45 — the module this action runs against",
			57: "exception handling: (tag (export \"tag\")) at :7, and a try_table/throw pair at :39-45 — the module this action runs against",
			// Same shape, both imports naming the same instance (:62-98).
			101: "exception handling: two tag imports of the same instance at :69-70, and a try_table/throw pair at :86-92 — the module this action runs against",
			102: "exception handling: two tag imports of the same instance at :69-70, and a try_table/throw pair at :86-92 — the module this action runs against",
			103: "exception handling: two tag imports of the same instance at :69-70, and a try_table/throw pair at :86-92 — the module this action runs against",
			104: "exception handling: two tag imports of the same instance at :69-70, and a try_table/throw pair at :86-92 — the module this action runs against",
			// `$N` (:108-122) carries `(tag $tag)` exported under two names; the importing module
			// (:127-160) is the same try_table/throw shape again.
			167: "exception handling: (tag $tag) exported under two names at :112,:120-121, and a try_table/throw pair at :150-156 — the module this action runs against",
			168: "exception handling: (tag $tag) exported under two names at :112,:120-121, and a try_table/throw pair at :150-156 — the module this action runs against",
			169: "exception handling: (tag $tag) exported under two names at :112,:120-121, and a try_table/throw pair at :150-156 — the module this action runs against",
			170: "exception handling: (tag $tag) exported under two names at :112,:120-121, and a try_table/throw pair at :150-156 — the module this action runs against",
		},
		"throw.wast": {
			// The sole module (:3-36) declares five tags and a `throw`-carrying `$throw-if`.
			38: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			49: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			// The `assert_exception` rows became askable at #201's rung 2a (a harness Kind,
			// not an engine capability) — same module, same declined gate, seven more lines.
			39: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			40: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			42: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			43: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			44: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			46: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
			47: "exception handling: five (tag …) fields and a throw at :22-25 — the module this action runs against",
		},
		"throw_ref.wast": {
			// Every action here invokes against the one module (:3-99), which declares tags and
			// both `try_table`/`throw_ref` pairs the file is named for.
			102: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			107: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			110: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			112: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			113: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			// `assert_exception` rows, askable since #201's rung 2a — same module, same gate.
			99:  "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			101: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			104: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			106: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			108: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			109: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
			115: "exception handling: try_table/throw_ref pairs at :53-96 — the module this action runs against",
		},
		"try_table.wast": {
			// The first module (:3-6) declares `(tag $e0 (export "e0"))`; `register` needs it
			// instantiated.
			8: "exception handling: (tag $e0 (export \"e0\")) at :4 — the module this register runs against",
			// The second module (:10-341) is the file's main subject: nine tags and every
			// `try_table`/`catch`/`catch_ref`/`catch_all`/`catch_all_ref` combination the suite
			// tests. Every action from :282 to :340 invokes against it.
			282: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			283: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			285: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			287: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			288: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			290: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			291: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			// The two `assert_exception` rows became askable at #201's rung 2a — same module,
			// same declined gate.
			292: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			296: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			294: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			295: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			298: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			299: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			300: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			302: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			303: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			305: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			306: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			307: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			309: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			310: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			312: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			313: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			314: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			316: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			317: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			319: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			320: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			321: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			323: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			324: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			326: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			328: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			329: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			331: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			332: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			// The two `assert_exception` rows became askable at #201's rung 2a — same module,
			// same declined gate.
			334: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			335: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			337: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			339: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			340: "exception handling: nine (tag …) fields and try_table at :10-341 — the module this action runs against",
			// The third module (:342-361) imports a tag and declares `(tag $e0)`, plus a nested
			// try_table pair.
			364: "exception handling: (tag $e0) at :344 and a nested try_table pair at :347-354 — the module this action runs against",
			// The fifth module (:420-459) declares `(tag $e (param (ref $t)))` and five
			// try_table/catch* combinations exercising catch_ref/catch_all_ref's exnref push.
			464: "exception handling: (tag $e (param (ref $t))) at :425 and five try_table/catch* forms at :428-458 — the module this action runs against",
			465: "exception handling: (tag $e (param (ref $t))) at :425 and five try_table/catch* forms at :428-458 — the module this action runs against",
			466: "exception handling: (tag $e (param (ref $t))) at :425 and five try_table/catch* forms at :428-458 — the module this action runs against",
			467: "exception handling: (tag $e (param (ref $t))) at :425 and five try_table/catch* forms at :428-458 — the module this action runs against",
			468: "exception handling: (tag $e (param (ref $t))) at :425 and five try_table/catch* forms at :428-458 — the module this action runs against",
			// The sixth module (:499-519) declares no tag at all — it is `try_table`'s own opcode
			// (0x1f) that gates, with an empty catch vector and a `br` inside as the only content.
			522: "exception handling: try_table with a br inside and no catch clauses at :502-506,:512-516 — the module this action runs against",
			523: "exception handling: try_table with a br inside and no catch clauses at :502-506,:512-516 — the module this action runs against",
		},
	}

	// **The bulk allowance must have a subject.** An empty `wholeFileGated` is not a smaller
	// allowlist, it is a comparison that agrees with every gated population there is: the branch
	// below matches no file, so a broken whole-file mechanism reports green on every board. That
	// is the same defect as a floor sitting entirely inside its own slack, and it is worse than a
	// deleted control because a vacuous green still reads as a verdict.
	//
	// It nearly happened here: the relaxed-SIMD flip zeroed all six entries, and the obvious edit
	// was to delete the keys. Zero is a claim; absence is not. See `wholeFileGated`'s own comment,
	// and #284 for the nonzero branch this guard cannot certify on its own.
	//
	// Falsified before being trusted, per the discipline: emptying the map fires this Fatal, and
	// changing any entry's 0 to 1 fires the count check below with `Gated is 0, want 1`.
	if len(wholeFileGated) == 0 {
		t.Fatal("wholeFileGated is empty, so the whole-file allowance below has no subject: it " +
			"would match no file, exercise nothing, and agree with any gated population at all.\n" +
			"\tIf a flip zeroed the last entry, keep the key with a value of 0 — that is a checkable " +
			"claim that the file declines nothing — rather than deleting it and leaving this control " +
			"vacuously green (#284)")
	}

	seen := map[string]bool{}
	files := boardFiles(t)
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		// **Read off the board's own count, not re-derived by asking the decoder.**
		//
		// This loop used to be `for _, c := range s.Commands { if isGated(decode(c.Module))
		// ... }`, which is a *second* trigger for the same concept — and it under-matched. It
		// can only see a decline that happens at `c.Module`, so when the interpreter added the
		// instantiation path (a **text** module declined for memory64, with no `c.Module` to
		// ask about) 17 of the board's 33 declines were outside its reach entirely. The
		// allowlist looked complete while covering half its population, and the 17 were
		// simultaneously *fails* one command downstream. One concept, one trigger (#82); the
		// run loop counts, and the control reads what it counted.
		r := run(s)
		if len(r.GatedAt) != r.Gated {
			t.Errorf("%s: Gated is %d but GatedAt has %d lines; the counter and the list "+
				"disagree, so this control is reading a subset it cannot name", f, r.Gated, len(r.GatedAt))
		}

		// **The third bulk allowance, keyed by cause and with its membership derived by neutering
		// the mechanism** — see gatedDeclinedRegistration, decision 0037. The second run is this
		// file scored with 0037's declined-import gate off; every line gated with it and not
		// without it is gated *by* it, which is a claim about the cause that no property of the
		// line itself could support.
		byDeclinedReg := map[int]bool{}
		{
			e := engine()
			e.InstantiateLinked = func(c Command, reg Registry) (Instance, Stratum, error) {
				return instantiateWithGate(binary.DefaultFeatures(), c, reg, false)
			}
			without := map[int]bool{}
			for _, line := range s.RunGated(e).GatedAt {
				without[line] = true
			}
			for _, line := range r.GatedAt {
				if !without[line] {
					byDeclinedReg[line] = true
				}
			}
			// A line gated *only* with the gate off is a contradiction: switching a decline on
			// cannot make a decline disappear. Asserted rather than assumed, because it is the one
			// way the difference above could be a subtraction of unrelated sets rather than of
			// nested ones — and a difference over non-nested sets names the wrong lines while
			// still producing a plausible count.
			with := map[int]bool{}
			for _, line := range r.GatedAt {
				with[line] = true
			}
			for line := range without {
				if !with[line] {
					t.Errorf("%s:%d is gated with 0037's declined-import gate *off* and not with "+
						"it on; the two gated sets are not nested, so the difference this control "+
						"takes does not name the population it claims", f, line)
				}
			}
		}
		if n, ok := gatedDeclinedRegistration[f]; ok {
			if len(byDeclinedReg) != n {
				t.Errorf("%s: %d lines gated by a declined registration, want %d — 0037's "+
					"population moved; re-base gatedDeclinedRegistration in this PR and say which "+
					"register's decline gained or lost a dependent",
					f, len(byDeclinedReg), n)
			}
		} else if len(byDeclinedReg) > 0 {
			t.Errorf("%s: %d lines are gated because a `register` was declined and the file has "+
				"no gatedDeclinedRegistration entry;\n\tadd one naming which register — an "+
				"unlisted bulk population is the enumeration this control exists to prevent",
				f, len(byDeclinedReg))
		}

		// **The fourth allowance, and its bound is the fix's own registry rather than a number
		// written here** — decision 0038, issue #414. Same derivation as the third: score the file a
		// second time with the mechanism neutered, and every line gated with it and not without it is
		// gated *by* it.
		//
		// The count it is checked against is `len(sideEffectOfDecline[f].Lines)`, which makes this
		// slack 0 in both directions **by construction**: a line the registry claims but the run does
		// not gate, and a line the run gates that the registry does not claim, are the same
		// disagreement seen from the two ends. Writing a separate integer here — the shape the three
		// allowances above have — would have created a third place for one fact to live, and the third
		// place is where it drifts.
		//
		// **Watched die on the mutation that makes the registry lie without changing a line of it**:
		// pointing `sideEffectOfDecline["load1.wast"].Module` at `:11`, which is not a module command,
		// so the consult's condition never holds and nothing is gated — `load1.wast: 0 lines gated by
		// decision 0038's side-effect registry, want 5`. `TestDeclineSideEffectsAreRegistered` stays
		// *quiet* under that same mutation, correctly: the rows are fails again and its derivation finds
		// them registered. Two controls, and the mutation each one catches is the other's blind spot.
		bySideEffect := map[int]bool{}
		{
			// Mirrors RunGated's one assignment on purpose: the capability derivation and the set
			// building stay in `runWith`, so this is the same path the board runs with one flag moved.
			e := engine()
			e.Has = EngineCapabilities()
			without := map[int]bool{}
			for _, line := range s.runWith(e, true).GatedAt {
				without[line] = true
			}
			for _, line := range r.GatedAt {
				if !without[line] {
					bySideEffect[line] = true
				}
			}
			// The nestedness check the third allowance carries, for the identical reason: neutering a
			// consult that only ever *adds* declines cannot make one disappear, so a line gated with
			// the registry off and not on means the difference is over unrelated sets and names the
			// wrong lines while still producing a plausible count.
			with := map[int]bool{}
			for _, line := range r.GatedAt {
				with[line] = true
			}
			for line := range without {
				if !with[line] {
					t.Errorf("%s:%d is gated with decision 0038's registry *off* and not with it on; "+
						"the two gated sets are not nested, so the difference this control takes does "+
						"not name the population it claims", f, line)
				}
			}
		}
		wantSideEffect := len(sideEffectOfDecline[filepath.Base(f)].Lines)
		if len(bySideEffect) != wantSideEffect {
			t.Errorf("%s: %d lines gated by decision 0038's side-effect registry, want %d — the "+
				"registry and the run disagree about this file;\n\tif a line stopped being gated the "+
				"entry is stale and the fail it hid is back, and if one started the entry is missing "+
				"the declined module that writes it (see sideEffectOfDecline)",
				f, len(bySideEffect), wantSideEffect)
		}

		// **A whole-file entry replaces a per-line one when the population is homogeneous.**
		// The harness v128 widening (decision 0024's forced question 5) moved 24115 lines
		// across 63 SIMD/relaxed-SIMD files from Unsupported into Gated in one PR — every one
		// of them for the identical reason (`simd: feature gate disabled`, measured directly
		// against the decoder rather than assumed), because the SIMD gate stays off by default
		// and every v128 argument/expectation the harness could not previously even construct
		// now reaches the decoder and is honestly declined. Writing 24115 individual line
		// entries would restate one fact 24115 times — the allowlist's own point is a named
		// reason per decline, and a reason repeated verbatim at that scale is testimony
		// nobody could review by reading it. So a file whose *entire* Gated population shares
		// one reason gets one entry naming the reason and the exact count, and the count is
		// what still catches drift: a file gaining or losing a gated line (a new vector, or a
		// vector converting to pass/fail once SIMD execution lands) moves its own count, which
		// this check still asserts on the nose.
		if handled, complaint := wholeFileGatedVerdict(wholeFileGated, f, r.Gated); handled {
			if complaint != "" {
				t.Error(complaint)
			}
			continue
		}

		// **The second bulk allowance, keyed by Kind rather than by file** — see
		// gatedAssertInvalid for why 463 per-line entries would be testimony nobody reads and
		// why this is the same mechanism as wholeFileGated rather than a new concept.
		//
		// The membership test is "every command at this line is an assert_invalid", not "some
		// command at this line is", and the difference is a claim about the corpus this loop
		// can check rather than assume: a line holding both an `assert_invalid` and something
		// else would otherwise let an unrelated decline into the bulk count and out of the
		// per-line arm. Measured 0 such lines across the board today; the guard is here because
		// the *reason* the count is safe has to survive the corpus moving.
		invalidOnly := map[int]bool{}
		for _, c := range s.Commands {
			if c.Kind.isAssertInvalid() {
				if _, seen := invalidOnly[c.Line]; !seen {
					invalidOnly[c.Line] = true
				}
			} else {
				invalidOnly[c.Line] = false
			}
		}
		bulkInvalid := 0
		for _, line := range r.GatedAt {
			if invalidOnly[line] {
				bulkInvalid++
			}
		}
		if n, ok := gatedAssertInvalid[f]; ok {
			if bulkInvalid != n {
				t.Errorf("%s: %d gated assert_invalid commands, want %d — the file's gated "+
					"validator population moved; update gatedAssertInvalid's count in this PR, "+
					"and single out any line whose gate is not one of the file's stated ones",
					f, bulkInvalid, n)
			}
		} else if bulkInvalid > 0 {
			t.Errorf("%s: %d assert_invalid commands are declined by a feature gate and the file "+
				"has no gatedAssertInvalid entry;\n\tadd one with the gate named, or single the "+
				"lines out in `allowed` — an unlisted bulk population is the enumeration this "+
				"control exists to prevent", f, bulkInvalid)
		}

		declined := make(map[int]bool, len(r.GatedAt))
		for _, line := range r.GatedAt {
			declined[line] = true
			if invalidOnly[line] {
				continue // covered by the bulk count above, with its gate named there
			}
			if byDeclinedReg[line] {
				continue // covered by gatedDeclinedRegistration, with its register named there
			}
			if bySideEffect[line] {
				continue // covered by sideEffectOfDecline, with its declined writer named there
			}
			if _, ok := allowed[f][line]; !ok {
				t.Errorf("%s:%d declined by a feature gate but is not in the allowed set;\n"+
					"\tif the gate is right, add it with the feature named; if not, the decoder is over-gating and hiding a failure",
					f, line)
			}
		}
		// The reverse: a stale entry would claim a decline that no longer happens,
		// overstating how much the gates are doing.
		for line := range allowed[f] {
			if !declined[line] {
				t.Errorf("%s:%d is in the allowed-gated set but is no longer declined; remove the entry", f, line)
			}
		}
		seen[f] = true
	}

	// The bulk allowance's own reverse direction. The on-the-nose comparison in the loop catches
	// an entry whose file is on the board and has stopped declining (`want 6, got 0`); it cannot
	// see an entry for a file `boardFiles` no longer selects, because that file is never visited
	// and the map is never asked. A key nobody reads is an allowance that cannot go stale, which
	// is the shape a registry decays into.
	for f := range gatedAssertInvalid {
		if !seen[f] {
			t.Errorf("gatedAssertInvalid has an entry for %s, which is not on the board — its "+
				"count is asserted against nothing; remove the entry or find out why the file left", f)
		}
	}

	// The total, pinned as one number beside the 41 that decompose it.
	//
	// **Not redundant with the per-file counts, and the direction it catches is the one they
	// cannot.** Forty-one comparisons each say "this file declines exactly n"; none of them says how
	// many files there are. A file dropping out of `boardFiles` takes its whole entry with it and
	// every remaining comparison still passes — the reverse check above catches that only because
	// the key survives, and a PR that deletes the key *and* the file passes both. This sum is the
	// second end: this is the validator arm's own gate population, and it moves only when a gate
	// flips, the corpus does, or the harness learns to ask a form it previously scored unsupported.
	// The figure is not restated here — it is the constant below, and a prose copy of a number the
	// code holds two lines down is the second place it can drift from (it read 463 against a live
	// 465 until #419).
	sum := 0
	for _, n := range gatedAssertInvalid {
		sum += n
	}
	if sum != 471 {
		t.Errorf("gatedAssertInvalid sums to %d, want 471 — the gated assert_invalid population "+
			"changed size. Two causes are legitimate and each re-bases this figure with its subject "+
			"named: a gate flip (name the proposal), or the harness learning to *ask* a form it "+
			"previously scored as unsupported (name the Kind — the 17-head slice added 2 this way, "+
			"`align.wast` and `elem.wast`, both `(module binary …)`, and #419's table-initializer "+
			"emitter added 6 across `table.wast`, `elem.wast` and `global.wast`). The second is not a gate "+
			"moving and not a corpus moving, which is why it is spelled out here rather than left "+
			"to be argued: the gates declined those two vectors all along and nothing was listening. "+
			"Anything else is a file or a population that moved unremarked", sum)
	}
}

// TestBareQuoteFormsPassUnearned names the seven passes the reader did not earn.
//
// A bare `(module quote "...")` asserts its source is *valid* wat. The engine's answer is
// `text.LexAll`, which asserts only that the source lexes — so a vector passes here by the
// absence of everything above the lexer, not by anything the lexer decided. That is
// **overfitting arrived at by omission** (§9 G-3): pass count bought by a check that is
// right on the vectors and wrong in general, and invisible on the board by construction
// because the board cannot tell an earned pass from an unopposed one.
//
// So it is reported rather than netted out. The seven are enumerated with what each one
// actually turns on, in the shape TestGatedVectors uses for the third verdict and for the
// same reason — an unexplained entry is a suppression wearing a disguise, and a category
// that can grow silently is a lever. When #8's parser lands, these seven become genuine
// verdicts and this test's job is to *shrink to zero*; a new bare form appearing here
// before then is a vector claiming a pass nobody looked at.
//
// The reverse direction matters more than the forward one, which is why both are checked:
// a listed vector that has started *failing* means the reader regressed on something it
// used to accept — an accept-direction defect, the class no negative vector can falsify
// (decision 0007) — and it would otherwise read as this list going stale.
//
// # Retirement is a third state, and it is not the same as either
//
// #62's parser turned one of the seven from unearned into a *named boundary*:
// `comments.wast:83` is a quote form whose payload has instruction bodies, so ReadModule now
// reaches it and stops with `unimplemented`. The reverse-direction arm caught that, which is
// the arm working — but its diagnosis was wrong, and taking it at face value would have been
// the worse outcome. It reads any error as an accept-direction defect, and there are now two
// ways a listed vector can stop passing:
//
//   - the reader **regressed**: it rejects a module the reference accepts, for a reason it
//     believes. That is the defect, and it stays a hard error.
//   - the reader **advanced past the lexer and stopped short honestly**, declaring the
//     stratum boundary. The pass was never earned; it is now not claimed at all, which is
//     strictly better than an unopposed green and is exactly the shrink-to-zero this test
//     was written to expect.
//
// Collapsing the two would be dishonest in whichever direction it resolved: treat retirement
// as a defect and the board goes red for progress; treat any error as retirement and a real
// accept-direction defect hides behind the same arm. So the two are separated by the one
// thing that distinguishes them — whether the error *names itself as unread* — and the
// boundary is not an excuse a vector can grow into, because a retired entry must be removed
// from the list rather than annotated in it, and `retiredThisStratum` is floored and counted
// like everything else here.
//
// # Retirement is reversible, and #63 reversed it
//
// `comments.wast:83` went back to `unearned` when #63's instruction grammar read the payload
// #62 had stopped at. The switch's retired arm offers two dispositions for an entry that
// starts passing again — the boundary moved, or the pass is now earned — and the choice
// between them is not "did the reader get further", it is **what does the vector assert**. A
// bare `(module quote ...)` asserts *validity*, so the only stratum that can earn its pass is
// the one that can disagree about types; a parser that reads the payload and says nothing is a
// better-informed silence, not a verdict. Retiring it a second time on the strength of the
// parser alone would have been the omission-pass laundered through a list built to expose it.
//
// The general shape, since this is the second control in the project to hit it: **a state a
// mechanism was built to drain can refill, so its account is kept on the sum and its list is
// not deleted when it empties.** Sibling of the re-pointed tripwire (#33) — there an
// obligation outlived its subject, here a state outlived its emptying.
func TestBareQuoteFormsPassUnearned(t *testing.T) {
	requireSuite(t)

	// file → line → what the vector actually tests, i.e. what the lexer is silent about.
	unearned := map[string]map[int]string{
		// Annotation *placement* and shape: the spec says an unrecognized annotation is
		// ignored wherever it may appear, and these assert it in six positions. The lexer
		// skips annotation bodies (they produce no token), so it agrees for the wrong
		// reason — it cannot tell a well-placed annotation from a misplaced one, because
		// it has no notion of position at all.
		"annotations.wast": {
			32:  "annotation before a module field — placement is the parser's judgement",
			33:  "annotation inside a func body — same",
			36:  "annotation with a nested s-expression body",
			55:  "annotation containing string and id atoms",
			206: "annotation in a type position",
			207: "annotation in an import position",
		},
		// Returned to this list by #63, and the round trip is the finding. #62 retired it by
		// reaching the payload's instruction bodies and stopping short; #63's flat instruction
		// grammar reads them, so the vector is accepted again — which is the retired arm's
		// *first* named outcome, "the boundary moved and the entry belongs back on the
		// unearned list", not its second.
		//
		// It is the first and not the second because a bare `(module quote ...)` asserts its
		// source is **valid**, and validity is the validator's word. #63 raised the answer from
		// "it lexes" to "it parses", which is a better reason to be silent and still not the
		// question the vector asks: `(func (export "f1") (result i32) (i32.const 1) (return
		// (i32.const 2)))` has a result type to agree with and a return to typecheck, and
		// nothing in the engine yet does either. The stratum that owns it is the validator.
		//
		// So the pass is unearned again rather than earned, and the reason it was ever retired
		// was the shape of #62's boundary, not the vector's difficulty. That is worth keeping:
		// an entry can leave `retired` in either direction, and only naming which one keeps the
		// list from reading as monotone progress.
		"comments.wast": {
			83: "instruction bodies now parse, but the vector asserts *validity* — the result " +
				"type and the return want a typechecker (the validator's stratum)",
		},
	}

	// retired lists the entries a parser took off the unearned list by reaching them and
	// declaring the boundary — the shrink this test was written to expect, recorded rather
	// than deleted so the arithmetic against the original seven stays checkable.
	//
	// **Empty as of #63**, and empty is not the same as finished. Its one entry,
	// `comments.wast:83`, went back to `unearned` rather than away: see the note there. The
	// map stays, and stays in the arithmetic, because retirement is a state vectors will keep
	// entering as strata land — #64's folded forms and the validator both have candidates —
	// and a list deleted the first time it empties is a mechanism that has to be re-derived
	// by whoever needs it next.
	retired := map[string]map[int]string{}

	// Vacuity floor. A list of unearned passes that finds nothing is indistinguishable
	// from a board with none, and the second is the state this test exists to detect the
	// arrival of. Floored at the list's own length rather than at 1, per *a floor below
	// the list's own length is decoration*.
	want, wantRetired := 0, 0
	for _, m := range unearned {
		want += len(m)
	}
	for _, m := range retired {
		wantRetired += len(m)
	}
	// The sum is what must hold at seven, not either part: an entry moving from unearned to
	// retired is progress, an entry *vanishing* from both is the list going stale, and only
	// the sum can tell those apart. Seven-plus-zero today — and it was six-plus-one under
	// #62, which is the clearest demonstration of why the invariant is on the sum: the
	// entry has now crossed in both directions and the guard never moved.
	if want+wantRetired != 7 {
		t.Fatalf("the unearned and retired lists hold %d+%d entries, want 7 between them; "+
			"the count is quoted in the pass floor's account and in PR #61, so the two must "+
			"not drift — and an entry that left both lists left without an account",
			want, wantRetired)
	}

	seen, seenRetired := 0, 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Kind != KindModuleQuote {
				continue
			}
			err := readText(c.Source)
			_, listed := unearned[f][c.Line]
			_, wasRetired := retired[f][c.Line]
			switch {
			case err == nil && !listed && !wasRetired:
				t.Errorf("%s:%d is a bare (module quote ...) that the reader accepts and is "+
					"not in either list; it passes because nothing above the parser's "+
					"current stratum can disagree with it, and a pass arrived at by "+
					"omission has to be named", f, c.Line)
			case err == nil && wasRetired:
				t.Errorf("%s:%d is listed as retired but the reader accepts it again; a "+
					"retired entry is one whose pass was *withdrawn*, so an acceptance "+
					"means either the boundary moved and the entry belongs back on the "+
					"unearned list, or the pass is now earned and the entry goes away",
					f, c.Line)
			case err != nil && listed:
				// Two ways this happens and they are not the same finding. A named boundary
				// is the reader declining to claim a pass it cannot earn; anything else is
				// the reader rejecting a module the reference accepts.
				if strings.Contains(err.Error(), "unimplemented") {
					t.Errorf("%s:%d is on the unearned list but the reader now stops short "+
						"of it with a named boundary (%v); that is progress, not a defect — "+
						"move the entry to `retired` with the stratum that owns the rest",
						f, c.Line, err)
				} else {
					t.Errorf("%s:%d is listed as an unearned pass but the reader now "+
						"rejects it (%v); a valid module rejected is an accept-direction "+
						"defect, which no negative vector in the suite can catch",
						f, c.Line, err)
				}
			case err != nil && wasRetired:
				// A retired entry must keep stopping short *honestly*. If it starts failing
				// for a reason the reader believes, it is the accept-direction defect the
				// unearned arm watches for, arriving one list over.
				if !strings.Contains(err.Error(), "unimplemented") {
					t.Errorf("%s:%d is retired behind a stratum boundary but now fails with "+
						"%v; a retired entry that stops naming itself unread is a valid "+
						"module being rejected on the merits — the accept-direction defect, "+
						"hiding in the list that was supposed to account for its silence",
						f, c.Line, err)
					continue
				}
				seenRetired++
			case err == nil && listed:
				seen++
			}
		}
	}
	if seen != want {
		t.Errorf("found %d of %d listed unearned passes; a listed vector the loop never "+
			"reached means the file left the board and this list is watching nothing",
			seen, want)
	}
	if seenRetired != wantRetired {
		t.Errorf("found %d of %d retired entries; a retired vector the loop never reached "+
			"means the file left the board, and the account of the original seven no longer "+
			"has anything behind it", seenRetired, wantRetired)
	}
	t.Logf("%d bare (module quote ...) forms pass unearned, %d retired behind a named "+
		"stratum boundary (#63/#64); 7 originally", seen, seenRetired)
}

// TestAssertInvalidPassesFromAboveTheValidator names the 17 `assert_invalid` passes that no
// validation verdict bought.
//
// An `assert_invalid` reaches the validator through two steps first — `text.EncodeModule` and
// `binary.Decoder.DecodeModule` (see validateWith) — and either may refuse. The board's arm asks
// four questions in order (gated, declined, message match, accepted), and the message match does
// not care *which* layer produced the error. So a module the encoder or decoder refuses with a
// string the vector happens to expect is scored a pass, and 18 of the 1023 the PR that built this
// list added arrived that way — 17 now, and the one that left is recorded on the retired entry
// below.
//
// They are **not unearned in the bare-quote sense** and the distinction is the point. A bare
// `(module quote ...)` passes because nothing above the lexer can disagree — a silence scored as
// agreement. These 17 are real refusals with the reference's own message: the module *is* invalid
// and the engine *did* say so. What is wrong is only the layer, which makes them evidence rather
// than a defect (*an error from the wrong layer is evidence about where structure was lost*).
// Specifically, all 17 say the decoder is enforcing the constant-expression rule the reference
// enforces in validation, which is the *one concept, one trigger* question this PR does not
// settle: when #9's slice for constant expressions lands there will be two authorities for it,
// and one of them has to go.
//
// So the list's retirement path is named per entry rather than assumed. Four things can happen to
// a listed vector and only one of them is this state; the switch below separates them, because
// "the validator now owns it" (progress: delete the entry) and "the validator now accepts it"
// (the admission stratum, a defect) both read as *stopped passing above the validator* and are
// opposite findings. The reverse direction is checked for the bare-quote test's reason: a new
// arrival here is a vector claiming a green nobody looked at.
//
// # Domain, stated because the control cannot check it
//
// This sweeps `KindAssertInvalid` commands in `boardFiles` only. It says nothing about the same
// layer confusion under `assert_malformed` (where an encoder refusal is arguably the right
// layer), nothing about the 11 `(module binary …)` and 6 `(module quote …)` invalid forms the
// parser leaves unsupported, and nothing about the all-gates-on lane, whose Features differ.
func TestAssertInvalidPassesFromAboveTheValidator(t *testing.T) {
	requireSuite(t)

	type above struct {
		layer string // "encoder" or "decoder" — which pre-validation step refuses, re-measured below
		why   string
	}
	// Every one of the 17 decoder entries is an init or offset expression holding an opcode
	// outside the constant set (`i32.ctz`, `nop`, `local.get`, `f32.neg`, `call`). The engine's
	// init-expression reader admits only the constant opcodes, so it refuses during decode with
	// exactly the message the reference produces during validation.
	const constExpr = "a non-constant opcode in an init/offset expression, refused by the " +
		"decoder's init-expression reader rather than by a constant-expression validation rule"
	listed := map[string]map[int]above{
		"data.wast": {
			464: {"decoder", constExpr + " — (data (i32.ctz ...))"},
			472: {"decoder", constExpr + " — (data (nop))"},
			480: {"decoder", constExpr + " — (data (offset (nop) ...))"},
			488: {"decoder", constExpr + " — (data (offset ... (nop)))"},
		},
		"elem.wast": {
			783: {"decoder", constExpr + " — (elem (i32.ctz ...))"},
			791: {"decoder", constExpr + " — (elem (nop))"},
			799: {"decoder", constExpr + " — (elem (offset (nop) ...))"},
			807: {"decoder", constExpr + " — (elem (offset ... (nop)))"},
			885: {"decoder", constExpr + " — (elem ... (item (call $f)))"},
		},
		"func_ptrs.wast": {
			39: {"decoder", constExpr + " — (elem (i32.ctz ...))"},
			43: {"decoder", constExpr + " — (elem (nop))"},
		},
		"global.wast": {
			298: {"decoder", constExpr + " — (global f32 (f32.neg ...))"},
			303: {"decoder", constExpr + " — (global f32 (local.get 0))"},
			308: {"decoder", constExpr + " — (global f32 (f32.neg ...))"},
			313: {"decoder", constExpr + " — (global i32 (i32.const 0) (nop))"},
			318: {"decoder", constExpr + " — (global i32 (i32.ctz ...))"},
			323: {"decoder", constExpr + " — (global i32 (nop))"},
			// # `global.wast:666` was the eighteenth and it is retired, by the mechanism its own
			// entry named
			//
			// It read: "The weakest of the 18, and the only encoder one. `(global $g1 i32
			// (global.get $g2))` forward-references a global declared on the next line, and the
			// vector expects `unknown global`. The reference resolves wat names in a collecting
			// pass, so $g2 *is* known there and the error is validation's rule that a constant
			// expression may only read a *preceding* import; our encoder resolves in one forward
			// pass, so it reports the same words for a different reason. Right answer, wrong
			// reason, wrong layer — **a two-pass name resolver would retire this entry to the
			// validator without any validation rule being written**."
			//
			// Grave #130's repair is that second pass for eight index spaces, globals among them,
			// so the encoder no longer refuses and the vector reaches the validator, which says
			// `global 0: unknown global 1 (0 in scope)`. Same words the vector wants, now from the
			// layer that owns the rule, and no constant-expression rule was written — the
			// prediction to the clause.
			//
			// **Kept as prose rather than deleted, because the entry is the evidence that the
			// prediction was made before the outcome.** A retirement recorded only as a smaller
			// count reads as drift; recorded here it is the one case in this list that closed the
			// way the list said it would, and it is worth more than the entry was.
		},
	}

	want := 0
	for _, m := range listed {
		want += len(m)
	}
	// Pinned on the total because the figure is quoted in the pass floor's account and in this
	// PR's Board section, and because a file leaving boardFiles would otherwise shrink the list
	// and the observed count together — the two agreeing while both fall (see *a comparison
	// against an empty set succeeds*).
	if want != 17 {
		t.Fatalf("the list holds %d entries, want 17; the count is quoted in passFloor's "+
			"account, so the two must not drift", want)
	}

	seen, byLayer := 0, map[string]int{}
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Kind != KindAssertInvalid {
				continue
			}
			// validateModule, not a hand-rolled pipeline: this must be the entry point the
			// board itself scores through, or it measures a lookalike.
			st, err := validateModule(c)
			if isGated(err) {
				continue // scored `gated`, never reaching the message match at all
			}
			entry, isListed := listed[f][c.Line]
			fromAbove := err != nil && st == StratumEncode && strings.Contains(err.Error(), c.Expect)
			switch {
			case fromAbove && !isListed:
				t.Errorf("%s:%d is an assert_invalid the engine refuses *above* the validator "+
					"(%v) with the string the vector expects, and it is not listed; the board "+
					"scores it a pass, so a green arrived at by the wrong layer has to be named",
					f, c.Line, err)
			case fromAbove:
				// Which step refused is re-measured rather than trusted from the table: the
				// stratum says "before validation", it cannot say which of the two.
				layer := "decoder"
				if _, eerr := text.EncodeModule(c.Source); eerr != nil {
					layer = "encoder"
				}
				if layer != entry.layer {
					t.Errorf("%s:%d is listed as refused by the %s but the refusal now comes "+
						"from the %s; the layer is the whole content of this entry, so a moved "+
						"one is a different finding wearing the same line number",
						f, c.Line, entry.layer, layer)
				}
				byLayer[layer]++
				seen++
			case isListed && err != nil && st == StratumValidate:
				t.Errorf("%s:%d is listed as answered above the validator but the module now "+
					"reaches the validator, which refuses it (%v); that is the rule migrating "+
					"to the layer that owns it — delete the entry and lower the count",
					f, c.Line, err)
			case isListed && err == nil:
				t.Errorf("%s:%d is listed as refused above the validator but the whole "+
					"pipeline now *accepts* it; the pass was not earned somewhere better, it "+
					"was withdrawn into the admission stratum (validateAdmitCeiling) — an "+
					"accept-direction defect, the class no negative vector can catch",
					f, c.Line)
			case isListed:
				t.Errorf("%s:%d is listed as refused above the validator with %q, but the "+
					"refusal no longer quotes it (%v); the vector has become an encode-column "+
					"fail, which is honest scoring and still a message regression",
					f, c.Line, c.Expect, err)
			}
		}
	}
	if seen != want {
		t.Errorf("found %d of %d listed entries; a listed vector the loop never reached means "+
			"its file left the board and this list is watching nothing", seen, want)
	}
	// 17/0 since grave #130 retired the one encoder entry (see `global.wast`'s retirement note).
	// The encoder leg is pinned at **0 rather than dropped**, because that is the direction this
	// list now has to watch: a new encoder refusal quoting a vector's expected string is the
	// accident that entry was, and a check that only counted decoder entries would score it green.
	if byLayer["decoder"] != 17 || byLayer["encoder"] != 0 {
		t.Errorf("layer split is decoder %d / encoder %d, want 17/0; the split is the finding "+
			"(the 17 are one validation rule living in the decoder, and the encoder's 0 is grave "+
			"#130's repair holding) and it is quoted in this PR's Graves section",
			byLayer["decoder"], byLayer["encoder"])
	}
	t.Logf("%d assert_invalid passes come from above the validator: %d decoder, %d encoder",
		seen, byLayer["decoder"], byLayer["encoder"])
}

// allFeaturesOn returns a Features with every gate the decoder knows about turned
// on, discovered by reflection rather than by an enumerated literal.
//
// The enumerated version is a drift vector of exactly the shape
// TestEveryFixtureFileIsChecked exists to close: adding a fifth gate and
// forgetting to add it here would leave the all-on lane quietly running with that
// gate off, so vectors could hide in `gated` in *both* lanes — the precise failure
// the lane is built to prevent. A non-bool field fails loudly rather than being
// skipped, because "I did not know how to turn this on" must not read as "it is on".
func allFeaturesOn(t *testing.T) binary.Features {
	t.Helper()
	var f binary.Features
	v := reflect.ValueOf(&f).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		fld := v.Field(i)
		if fld.Kind() != reflect.Bool {
			t.Fatalf("Features.%s is %s, not a bool: this test cannot turn it on, so the all-gates-on lane would silently run with it off",
				name, fld.Kind())
		}
		fld.SetBool(true)
	}
	return f
}

// TestAllGatesOnLeavesNothingGated is the structural control on the third verdict
// (Scott's condition on #27).
//
// TestGatedVectors bounds `gated` per vector, but per-vector allowlists are
// vigilance: they stop a vector from hiding *unnoticed*, not from hiding. This
// closes it structurally. Under every tracked gate on, no vector may be declined —
// every one answers on the merits, pass or fail, with nowhere to park.
//
// That makes `gated` a **deferral that cannot become a disappearance**: a vector
// sitting in `gated` on the default board is simultaneously being honestly *failed*
// here, and stays failed until its feature actually works. The default lane says
// "not asked"; this lane insists the question still exists.
//
// So this test is expected to be *red-ish* in aggregate — the fail counts below are
// higher than the default board's, and that is the point. What must be zero is
// Gated.
// allOnLane builds the every-gate-on lane: the features, a decode func carrying them, and an
// Engine factory.
//
// Extracted from TestAllGatesOnLeavesNothingGated when a second reader appeared
// (TestRelaxedLoweringChoicesArePinned). A lane assembled twice is the two-places-know-one-fact
// shape on the *definition of the measurement*, and the failure it invites is the one the
// InstantiateLinked note below records: the second copy sets a field nothing calls, reads as
// though it configured the lane, and quietly scores a different lane than the first.
//
// An Engine *factory* rather than an Engine, because RunGated is called once per file and the
// closures capture per-run state; returning a value would share it across files.
func allOnLane(t *testing.T) (binary.Features, func([]byte) error, func() Engine) {
	t.Helper()

	allOn := allFeaturesOn(t)
	d := &binary.Decoder{Features: allOn}
	decodeAllOn := func(image []byte) error {
		_, err := d.DecodeModule(image)
		return err
	}
	return allOn, decodeAllOn, func() Engine {
		// Still RunGated at the call sites, deliberately: the point is to *measure* Gated
		// and require it to be zero. Using Run would fold declines into Fail and the
		// requirement would be unfalsifiable — the counter it asserts on could not be
		// nonzero.
		e := engine()
		e.Decode = decodeAllOn
		// The instantiation path takes the lane's gates too — see instantiateWith for
		// why this line exists and what its absence cost.
		//
		// **The field overridden is the one `engine()` supplies**, which is now InstantiateLinked
		// rather than Instantiate. Writing `e.Instantiate` here instead would compile, read as
		// though it set the gates, and be called by nothing — `instantiateRaw` prefers the linked
		// func — so it would be this comment's own bug in a new field. A lane overrides the
		// spelling the engine actually uses, and the way to know which that is is to read
		// `engine()` rather than to assume the plain name is the live one.
		e.InstantiateLinked = func(c Command, reg Registry) (Instance, Stratum, error) {
			return instantiateWith(allOn, c, reg)
		}
		// **The validator's path is a third entry point that decodes**, and it takes the lane's
		// gates for the same reason the second one does. `validateModule` calls
		// `binary.DecodeModule` — the default-features helper — so leaving this line out would
		// decline every gated proposal's `assert_invalid` vectors inside the lane whose whole
		// claim is that nothing is declined. This is the third time this override has been
		// needed and the second time it was nearly forgotten; see validateWith.
		e.Validate = func(c Command) (Stratum, error) { return validateWith(allOn, c) }
		return e
	}
}

func TestAllGatesOnLeavesNothingGated(t *testing.T) {
	requireSuite(t)

	_, decodeAllOn, allOnEngine := allOnLane(t)

	files := boardFiles(t)
	var totalPass, totalFail, totalGated int
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := s.RunGated(allOnEngine())
		t.Log("\n" + r.Board())
		totalPass, totalFail, totalGated = totalPass+r.Pass, totalFail+r.Fail, totalGated+r.Gated

		if r.Gated != 0 {
			t.Errorf("%s: %d vectors declined with every gate on;\n"+
				"\ta gate that still declines under full features is not a gate — it is a rejection wearing a feature name, and the vector has nowhere left to be honestly scored",
				f, r.Gated)
			// Naming them, because "3 gated" is not an actionable board line.
			for _, c := range s.Commands {
				if isGated(decodeAllOn(c.Module)) {
					t.Errorf("  %s:%d still gated: %v", f, c.Line, decodeAllOn(c.Module))
				}
			}
		}
	}
	t.Logf("all gates on: %d pass, %d fail, %d gated (Gated must be 0; fail is expected to exceed the default lane's)",
		totalPass, totalFail, totalGated)

	// A pass floor for *this* lane too, and the gap it closes was found by landing #51.
	//
	// Asserting only Gated == 0 makes this lane blind to the thing it is otherwise best
	// placed to see: a gated feature that *breaks*. Turn every gate on, decode a
	// construct wrong, and Gated stays zero while a pass silently becomes a fail — the
	// lane reports success because the one number it checks is unaffected. #51 moved this
	// count 791 → 798, which is only visible because it was printed; nothing would have
	// noticed it moving back.
	//
	// So: the same floor discipline as the default board, on the lane where gated
	// features are the only place they can be measured at all. The default lane cannot
	// substitute — there these seven vectors are honestly `gated`, and a floor over a
	// number that excludes them says nothing about whether GC still works.
	// **This floor was 798 against an actual 4178** — stale by 3380, which means it could
	// not have caught a regression that erased four fifths of the lane. It was set in #56
	// (`git log -S`), 15 commits back, and the text strata that landed since moved the
	// count past it without anything noticing. Found by *reading the printed total next to the constant* while raising it
	// for #86, not by any control: nothing asserts that a floor is close to what it
	// floors, so a floor left behind by a large jump degrades silently into decoration.
	// The same defect class as a vacuity floor that passes on an empty set — the
	// comparison runs, agrees, and says nothing — and the reason the discipline says a
	// control's green must be falsifiable: this one was green at 798 whether the engine
	// worked or not.
	//
	// The general form, worth stating because it is not the same as "keep numbers
	// current": **a floor's distance from its measurement is itself a claim, and an
	// unasserted distance is where the assertion leaks out.** Raised to the measured
	// value here, with #86's +1; whether it should be *checked* for staleness is **#87**,
	// filed rather than decided in a PR about the type section.
	// **Now slack-checked** (decision 0013): a floor that drifts 3380 behind its
	// measurement is a failure, not a habit lapse. boardBound asserts both directions, and
	// the error it raises on the upper side names the raise it wants.
	// **4178 → 17939, and the gap to the default lane's 17923 is the 16 this lane is for.**
	// The interpreter raises both floors by the same 13761; what makes this lane's number
	// larger is the same set of gated vectors it always was, now including the 17 memory64
	// modules that reach the interpreter through instantiation. Those 17 are `gated` on the
	// default board and *answered* here, which is the deferral-cannot-become-a-disappearance
	// property working on a path it could not previously see — see instantiateWith for the bug
	// this lane caught in the course of it.
	// **17939 → 27137, and the gap to the default lane's 26307 is 830.** #7's memory raises both
	// floors; this lane's rises further because the default board's 1031 `gated` vectors answer
	// on the merits here, and with load and store arms in place most of them now answer
	// *correctly*.
	//
	// **The gap is the gated population's pass share, not the population** — a distinction this
	// comment's first draft got wrong by calling 830 "the third-verdict population", and the
	// numbers refute it in one line: 1031 declines become **830 passes and 201 fails** here, and
	// fail moves 5349 → 5550 to match. 830 + 201 = 1031, which is the deferral-cannot-become-a-
	// disappearance property as arithmetic rather than as a claim: every parked vector is
	// accounted for in this lane, and the 201 stay honestly red until their feature works.
	// Quoting 830 as the population would have implied 201 vectors vanished.
	// **27137 → 27486, and the gap to the default lane's 26567 is 919.** #7's block family raises
	// both floors; the arithmetic above holds at the new numbers and is worth re-running rather
	// than assumed, because the gated population grew by 454 in the same PR: 1485 declines become
	// **919 passes and 566 fails** here, and fail moves 4635 → 5201 to match. 919 + 566 = 1485.
	//
	// Re-measured rather than adjusted, and the difference was not zero: this floor read **27483**
	// before grave #135 was fixed and reads 27486 after, the same +3 `comments.wast` modules the
	// default lane gained. Arithmetic on the earlier reading would have set a floor three below
	// the measurement and been indistinguishable from a correct one on the board — a floor is only
	// as honest as the run it was read from.
	// **27486 → 27802, and the gap to the default lane's 26833 is 969.** `br_table` (#8, 0016)
	// raises both floors, and the deferral arithmetic is re-run at the new numbers rather than
	// assumed to hold: the default board's **1535** `gated` declines become **969 passes and 566
	// fails** here, and fail moves 4319 → 4885 to match. 969 + 566 = 1535, so every parked vector
	// is answered on the merits in this lane and the 566 stay honestly red until their feature
	// works.
	//
	// The gated population grew by 50 in this PR — `align0`'s multi-memory memarg flags and
	// `align64`'s i64 index type — and the fail side absorbed none of them: 566 before and 566
	// after. Worth stating, because the number *not* moving is the reason to re-run the
	// arithmetic rather than adjust it: the previous PR's 566 and this one's are different
	// populations that happen to be the same size.
	// **27802 → 28497, and the gap to the default lane's 27451 is 1046.** Section 9 and
	// `call_indirect` (#8, 0016) raise both floors, and the deferral arithmetic is re-run at the
	// new numbers rather than assumed to hold: the default board's **1554** `gated` declines
	// become **1046 passes and 508 fails** here, and fail moves 3682 → 4190 to match.
	// 1046 + 508 = 1554, so every parked vector is answered on the merits in this lane and the
	// 508 stay honestly red until their feature works.
	//
	// **The fail side moved for the first time in three PRs — 566 → 508 — and −58 is a net of
	// two movements in opposite directions, not 58 vectors improving.** The previous three
	// entries all read 566, which invited the reading that the number is structural; it is not.
	// Measured by set-differencing the failing (file, line) keys against 165e77e rather than
	// inferred from the total, which is the arithmetic that would have hidden the second half:
	//
	//	−73  `interp: no arm for opcode 10` — **plain `call`, not `call_indirect`.** These are
	//	     memory64 and SIMD modules whose vectors are ordinary direct calls; they decode in
	//	     this lane, and until this PR the interpreter had no `call` arm to run them with.
	//	+15  vectors that now get *further* and stop at the next missing arm: 8 at `fc 0e`
	//	     (`table.copy`), 4 at `0x25` (`table.get`), 1 at `0x23` (`global.get`), and 2 at the
	//	     linking refusal. A fail moving to a later cause is still a fail, and quoting only
	//	     the −73 would have implied the queue shortened by more than it did.
	//
	// −73 + 15 = −58. The first draft of this paragraph asserted the cause was `call_indirect`
	// on i64-indexed tables — plausible in a PR about `call_indirect`, in a lane whose gated
	// population is mostly memory64, and **wrong**: the diff says `0x10`. A cause guessed from
	// the PR's own subject is the reading that flatters the work, which is the one to measure.
	// **Section 6 and `ref.null`'s heaptype (#8): +1019, and the whole of it is fail→pass** — 4190
	// fail became 3171, exactly the pass gain, so no vector changed category in any other direction.
	// The all-on lane moves more than the default lane's +947 for the reason it always does: with GC
	// on, modules whose *only* remaining refusal was the global field or a `ref.null` immediate now
	// decode, and this lane has the gated population the default lane declines before the encoder is
	// ever reached.
	// **`global.get`/`global.set` (#7): 29516 → 29534, +18, all of it exec→pass with zero arrivals.**
	// Two more than the default lane's +16, and the two were **identified rather than attributed to
	// the lane's usual reason**: `load2.wast:193` and `load64.wast:192`, both `global.set` vectors in
	// modules the default lane declines at the gate — multi-memory and memory64 respectively, which
	// are two of the 167 entries registered in TestGatedVectors' allowlist one PR ago. So the +2 is a
	// gate opening, not a feature behaving differently under full features, and this lane's excess
	// over the default one has a name for once instead of a category.
	//
	// **The saturating truncations (#7): 29534 → 29714, +180 — the *same* figure as the default
	// lane.** Both revisions were measured in this lane rather than the delta being carried over
	// from the other one, because "the lanes agree" is a claim and the previous entry above is a
	// standing reminder that they usually do not.
	//
	// The agreement has a reason worth naming: `conversions.wast` declares no gated feature, so the
	// gate configuration has nothing to say about any of the 180. That is the negative case for the
	// entry above it — there the excess was two named vectors in gate-declined modules, here the
	// excess is zero because the file is gate-blind — and a lane divergence of zero is only
	// informative once the mechanism that would produce one has been checked for.
	//
	// **The bulk trio (#7): 29714 → 30477, +763 against the default lane's +411.** The largest
	// lane divergence this floor has recorded — 352 — and it is itemized rather than attributed,
	// by set-differencing this lane's departures against the default lane's on `(file, line)`:
	//
	//	265  memory_copy64.wast      36  bulk64.wast            2  memory-multi.wast
	//	 22  memory_copy0.wast       10  memory_fill64.wast
	//	  9  memory_fill0.wast        8  table_copy64.wast
	//
	// Every one of the 352 is a vector the default lane declines at a gate: five memory64/table64
	// files, two zero-length-limit files, and two multi-memory vectors. Nothing here is a feature
	// *behaving differently* under full features, which is what a lane divergence is supposed to
	// be able to reveal and what the itemization is how you tell.
	//
	// The useful half is that the divergence is 352 and not zero: the `*64` files put the i64
	// branches of `tableAddr` and `memory.addr` on a board for the first time, which is the closest
	// thing to a control those branches have — see `tableAddr`'s comment for why they have no local
	// one, and note that this lane is *where that gap is covered* rather than a place it is
	// tolerated.
	//
	// The two `memory-multi.wast` vectors are a near-miss worth recording, because the first draft
	// of this paragraph claimed them as the only place either board runs `memory.copy` with a
	// nonzero memory index — and they are `memory.fill $mem1`/`$mem2` (`:31`, `:36`), not copies.
	// So they do exercise `memoryFor` with a nonzero index, which no other vector on either board
	// does, but only through fill's *single* immediate. `memory.copy`'s two-index order stays
	// unwitnessed by any lane, exactly as `execMemoryCopy`'s comment says, and it is `table.copy`'s
	// order that has an oracle (`table_copy.wast:774`).
	// **30477 → 33851 with the trap-action Kind (#157).** The default lane gains 2893 and this lane
	// gains 3374 — the difference is the 543 vectors the default lane declines on a gate and this
	// lane, having every gate on, answers on the merits. That is the structural control working as
	// decision 0010 intends: a vector parked in `gated` on the default board is simultaneously
	// being scored here, so the 543 allowlist entries added in TestGatedVectors cannot become a
	// disappearance. This lane's fail is 3712.
	//
	// **The bulk-segment batch (#8, #7): 33851 → 35533, +1682 against the default lane's +1458,
	// and the divergence goes *both* ways for once.** Fail 3712 → 2030 is −1682, so this lane's
	// columns close with no arrivals and no gated (which must be 0 here by construction). The
	// two lanes' pass deltas differ by 224, and it is not one population:
	//
	//	+244  vectors the default lane declines at a gate and this lane answers on the merits —
	//	      the 244 registered in TestGatedVectors' allowlist in this PR. Measured as the
	//	      *same* set rather than as an equal count: all 244 were fails in this lane too, and
	//	      223 of them are passes here now.
	//	 −21  `table_init64.wast` vectors that depart the default lane's fail column and stay in
	//	      this one, because with memory64 on the module instantiates far enough to hit the
	//	      **table-slot linking frontier** (`table slot N names function M, which is an
	//	      import`, contract §3) — a later refusal, not a pass. Set-differenced, and they are
	//	      exactly the 21 of the 244 that still fail here.
	//	  +1  `memory_init64.wast:209`, which this lane already had past the encoder before this
	//	      PR (its module is memory64) and which was failing at `no arm for opcode fc 09`. The
	//	      default lane never saw it, so it cannot appear in that lane's delta — the one term
	//	      here that *adds* to this lane rather than subtracting from it.
	//
	// 244 − 21 + 1 = 224, and 1682 − 1458 = 224. It closes, and the sign on that last term is
	// where a first draft got it wrong: written as `−1` the itemization came to 222, and the
	// two-vector gap was then explained away as a bucket-key collision in `global.wast` — a
	// cause invented to absorb a residue, which measurement falsified immediately. Every lane's
	// dump has **zero** duplicate `(file, line)` rows and **zero** arrivals, so the fail-column
	// departures are 1702 and 1682 and differ by 20 = 21 − 1. An itemization that reaches the
	// total by having the right number of terms is what the memarg batch above was corrected
	// for; this one reached the wrong total, and the invented cause was the tell.
	//
	// **223 of the 244 pass here and 21 fail here, and the 21 are the honest kind.** A gated
	// verdict on the default board is licensed by this lane answering on the merits — it does,
	// and for 21 of them the merits are a *different* unbuilt feature. That is a decline that
	// cannot become a disappearance: they are red in this lane until §3 linking exists.
	//
	// # 35533 → 36428, +895 against the default lane's +741, and the excess is one population
	//
	// The registry (0017 Q1). Fail 2030 → 1485 with **749 departures and 204 arrivals**, measured
	// in this lane rather than carried over — the standing reminder above having been earned twice.
	// The lane divergence is 154, and it is the 21 vectors this floor's previous entry left red
	// *plus* the rest of the §3 table-slot frontier: `table slot N names function M, which is an
	// import` was this lane's largest single reason, and a registry is what supplies those imports.
	// So the feature this lane was holding red for is the one that landed, which is the cleanest
	// case a divergence can have.
	//
	// **Two same-key-new-reason rows, and they are the ones worth naming.** `imports.wast:97-98`
	// changed from the §3 sentinel to `unknown import: "test" "func-i64->i64"` — the `"test"`
	// module's register previously failed on its `(tag …)` fields, which #199's rung 1 fixed on
	// the *default* lane only (this is the all-gates-on lane, where EH is on and the register
	// always succeeded on the merits) — so the cascade was honest and the *reason* improved: a
	// wrong-layer message ("this engine has no linker") became a right-layer one ("nothing
	// supplied this name"). Zero other rows changed cause, so nothing regressed under cover of
	// the fall.
	//
	// **22 of the 204 arrivals are named `assert_return`s, and 19 of those are Q2's funcref-identity
	// defect showing in this lane too.** `linking.wast:342-353` is the paradigm: `$Ot` imports
	// `$Mt`'s table and writes its *own* functions into it with an element segment, so a
	// `call_indirect` through that table must resolve in the module that supplied the funcref.
	// Reported as `i32 4` and `i32 4294967292` against expectations of the same shape — plausible
	// small integers, which is exactly why the class is invisible without the flow account. Same
	// defect as the default lane's 23 (see execFailCeiling), filed as **#163**, and this lane sees
	// more of it because GC-on modules reach further into `elem.wast` and `table_grow.wast`.
	// **Moved 36428 → 36877 in this PR** — decision 0018's encoder-side implementation
	// (`valTypeBytes`) reaches 248 more pass in this lane, measured with the harness rather than
	// guessed (36629 → 36877 over the raw two-run delta before this floor's own slack).
	//
	// **Moved 36877 → 37162 in #199's rung 1.** Zero of this move is `try_table`/`throw`/`throw_ref`
	// converting — #199's own scope statement says so up front, and it holds: `internal/interp` has
	// no throw/catch/unwind machinery, so nothing this PR does reaches execution. The 285-pass gain
	// is retention-adjacent module shapes newly accepting under EH — mostly modules whose `(tag …)`
	// field or `try_table` opener used to trip the encoder's frontier ahead of some *other*,
	// unrelated construct in the same module, which this lane's `assert_return`/`assert_trap`
	// vectors for that other construct then score as pass now that the frontier does not intercept
	// them first.
	//
	// **Moved 37162 → 37218 in #201's rung 2c (+56).** `throw`/`throw_ref`/`try_table` execution
	// landed against decision 0022 — genuine engine capability, not retention. `TestGrave206-
	// KnownFailures` (below) pinned the vectors that rung's own falsification found still red
	// (`catch_ref`/`catch_all_ref` immediately followed by `drop`, grave #206 — a pre-existing,
	// EH-unrelated stack-array-confusion bug in `drop` itself, not a gap in that rung's own
	// mechanism) and the two already-tracked rec-group scope-boundary vectors `sameFuncType`'s
	// own doc comment names, reached here for the first time via tag-import linking.
	//
	// **Moved 37218 → 37233 in #206's own fix (decision 0023, +15).** `drop` now consults a
	// lazily-tracked push sequence number (`stack.numSeq`/`refSeq`, value.go) instead of always
	// popping the numeric array — 15 of the 17 originally-#206-attributed lines converted, exactly
	// matching `TestGrave206KnownFailures`'s own updated population. The remaining 2 were found to
	// be compound failures once #206's own symptom cleared: `try_table.wast:465,466` now fail with
	// the same pre-existing harness limitation `:464` already carried, not with #206's own text —
	// a correction the fix's own falsification surfaced, not a new engine gap.
	// **Moved 37406 → 58443 in the harness v128 widening (decision 0024's forced question 5),
	// +21037** (the floor itself sat at 37233, 173 stale within slack — the actual pre-change
	// measurement is the honest baseline, not the floor). Not new engine capability: every one
	// of the 21037 newcomers was previously scored Unsupported at the default lane too (the
	// harness could not build a v128 argument or expectation at all, so `readConst` refused the
	// vector's `v128.const` node before any gate was ever asked) and Gated here, under all
	// features on, since the SIMD arms this campaign has been landing since #212 could finally
	// be asked their own questions. This is the "converts all-on fails into earned passes" move
	// stated in the re-ordering that put this widening ahead of VecConvert/VecShift.
	//
	// **58443 is the arm64 count and does not hold on amd64 — grave #223.** The same widening
	// that made these vectors askable also surfaced a pre-existing, architecture-dependent
	// defect in `floatMin`/`floatMax` (`internal/interp/simd.go`): Go's `math.Min`/`math.Max`
	// special-case NaN with per-architecture assembly (`math/dim_$GOARCH.s`), and amd64's
	// version hardcodes a fixed non-canonical NaN bit pattern for *any* NaN input, discarding
	// the operand's own NaN class — arm64's hardware instruction happens not to. Measured
	// directly (`math.Min` on a canonical-NaN input returns canonical on arm64, non-canonical
	// on amd64), not inferred from the board alone. 80 vectors move pass→fail on amd64 as a
	// result: `simd_f64x2.wast` (787→713) and `simd_f64x2_rounding.wast` (191→185); f32x4's
	// equivalents show no divergence, and the rounding ops' own NaN handling is unaudited past
	// this. The floor was set to **58363**, the amd64 figure, because a floor that only holds on
	// one of the two tracked architectures (contract §9 G-1's whole reason for two runners) is
	// not a floor CI can trust — see #223 for the fix and the pre-existing (arch-independent)
	// baseline this uncovers.
	//
	// **Moved 58363 → 58578 (amd64) / 58443 → 58658 (arm64) landing VecShift, +215 both
	// architectures** (#212's own family, 12 mnemonics: shl/shr_s/shr_u across
	// i8x16/i16x8/i32x4/i64x2) — the identical 80-vector #223 gap persists unchanged on both
	// (58658-58578=80, matching 58443-58363=80 exactly), confirming this arm's own correctness
	// is architecture-independent and the gap is unrelated to it. The floor moves to the amd64
	// figure again, for the same reason as above.
	//
	// **Moved 58578 → 59155 (amd64) / 58658 → 59235 (arm64) landing VecConvert, +577 both
	// architectures** (#212's own last family, 22 standard mnemonics: extend/extadd_pairwise/
	// trunc_sat/convert/demote/promote) — the identical 80-vector gap persists unchanged again
	// (59235-59155=80).
	//
	// **This PR's own "#212's ladder is complete" claim, made here, was wrong** — corrected in
	// #212's own tracking (comment on the issue) rather than silently edited away, per the
	// second-order-honesty discipline. `VecBinary` (integer: add/sub/mul/sat-arith/min/max/
	// avgr/narrow/extmul/dot/q15mulr/swizzle/shuffle, 53 standard mnemonics) had zero arms —
	// found while measuring a SIMD-only-features gate-flip forecast, whose fail bucket named 54
	// still-missing opcodes, every one a `VecBinary` constructor. The float half of VecBinary
	// (add/sub/mul/div/min/max/pmin/pmax) landed earlier with the float-arithmetic sub-batch and
	// was the reason the gap read as "complete" — VecBinary the *AST constructor* is split
	// across two of this ladder's own PRs, and only the float half had actually landed.
	//
	// **Moved 59155 → 61065 (amd64) / 59235 → 61145 (arm64) landing integer VecBinary, +1910
	// both architectures** — the identical 80-vector gap persists unchanged (61145-61065=80).
	// #212's ladder is genuinely complete now: a SIMD-only-features run leaves zero `no arm for
	// opcode fd *` fails, confirmed directly rather than inferred from a family list.
	//
	// **Grave #223 fully fixed, not merely diagnosed — and it was never actually the NaN-payload
	// arch-assembly divergence the earlier entries above describe.** Triaging the gate-flip
	// forecast's own `assert_return value mismatch` bucket (per Scott's own instruction to test
	// the harness-side NaN-wildcard hypothesis first, which measured false — only 18 of 201
	// mismatches even mention `nan:`) found two real, architecture-*independent* defects in
	// `floatMin`/`floatMax`/`floatUnary`:
	//
	//  1. `floatMin`/`floatMax` delegated to Go's `math.Min`/`math.Max`, whose own documented
	//     special cases check `IsInf` *before* `IsNaN` (`math/dim.go`: "Min(x, -Inf) =
	//     Min(-Inf, x) = -Inf", no NaN exception stated) — so `math.Min(NaN, -Inf)` returns
	//     `-Inf`, not NaN, where the reference's own `min`/`max` (`fxx.ml`) fall through to a NaN
	//     branch for *any* failed three-way comparison, infinity included. Fixed by writing the
	//     reference's own branch order directly rather than delegating.
	//  2. `floatUnary` never quiets a NaN result at all, relying on Go's `math.Ceil`/`Floor`/
	//     `Trunc`/`Sqrt`/`RoundToEven` to do it — but those functions are compiler intrinsics on
	//     arm64/amd64/s390x/wasm that fire only for a *literal* call expression
	//     (`ssagen/intrinsics.go`), and `floatUnary` calls `fn` as a first-class value, which
	//     bypasses the intrinsic and falls to the pure-Go source. `RoundToEven`'s own pure-Go
	//     path (`math/floor.go`) computes an unsigned-subtraction shift amount that underflows
	//     for a NaN/Inf exponent and leaves a signaling NaN's bits completely unchanged — on
	//     *both* architectures, confirmed directly, not assumed. Fixed by quieting explicitly.
	//
	// **Both defects are the entirety of #223's own 80-vector gap** — fixing them closed it
	// completely, confirmed by the two architectures now producing the identical count (verified
	// live: 61163 pass / 1138 fail on both arm64 and amd64, zero gap remaining). The earlier
	// entries' own NaN-payload-arch-assembly diagnosis was not wrong about `math.Min`/`math.Max`
	// having architecture-specific assembly, but the actual defect that assembly difference was
	// masking is what this entry fixes — the diagnosis and the defect were adjacent, not
	// identical, which is why "confirmed, not merely diagnosed" is the operative distinction.
	//
	// **Moved 61065 → 61163 (amd64) / 61145 → 61163 (arm64), the two counts now equal.**
	//
	// **Moved 61163 → 61339, both architectures, landing a third and distinct member of the
	// same defect family plus a fourth, unrelated bug — found triaging what remained of the
	// gate-flip forecast's own mismatch bucket per Scott's own instruction ("go find out what
	// the 199 are") after #223's fix reduced it from 201 to 183:**
	//
	//  1. **`f32x4.pmin`/`f32x4.pmax` corrupted a NaN operand's exact bits, 176 of the 183.**
	//     Both went through `vecBinaryFloat`'s shared widen-to-float64/narrow-back-to-float32
	//     path, but `v128.ml`'s own `pmin`/`pmax` are a *selection* — one operand's bits
	//     returned completely unchanged, no arithmetic — and Go's float32↔float64 conversion is
	//     documented to canonicalize a NaN's payload during the round trip (confirmed directly,
	//     both architectures: `float32(float64(math.Float32frombits(0x7fa00000)))` returns
	//     `0x7fe00000`). f64x2's own pmin/pmax never widen and are unaffected — measured zero
	//     f64 mismatches. Fixed with a dedicated `vecPminPmax` that compares in float64 for
	//     ordering but selects and returns the operand's original, never-round-tripped lane
	//     bits. +176.
	//  2. **`v128.load8_splat`/`load16_splat`/`load32_splat` over-read past their own scalar
	//     width, tripping a spurious out-of-bounds trap** — a distinct bug, not a NaN-payload
	//     defect, found in the remaining 9 of the 183 (`simd_load_splat.wast:47,52,57`, each a
	//     boundary address 1/2/4 bytes from a one-page memory's end). `vecLoadWidth` routed
	//     every "packed form" opcode through one 8-byte branch, but the three narrower splats
	//     read only 1/2/4 bytes each before replicating — over-reading up to 7 bytes nobody
	//     asked for, harmless deep in memory and a trap at the edge. load64_splat genuinely
	//     reads 8 bytes and needed no change. +9.
	//
	// The remaining 7 (of the original 201) were already fully attributed by
	// `TestValueMismatchBucketIsEmptyAndSaysWhoWroteAnyRow`'s own registry (5 `load1.wast`
	// multi-memory-cascade, 2 encoder `(start …)` gap) before this session started — confirmed
	// still green with both fixes applied, nothing to do there. The 161 `module reached the
	// interpreter unvalidated` fails are **#9-orthogonal**: `ErrNotValidated`'s own doc comment
	// (`internal/interp/interp.go`) names every one of its call sites as unreachable once #9
	// (the validator, open) lands — a declared-and-tracked layering debt, not a SIMD defect, and
	// no SIMD work drives it to zero. 176+9=185 fixed, 161 validator (parked, #9), 7
	// already-registered — closing the triage the flip procedure (#227) asked for:
	// 61163 + 185 = 61348.
	//
	// **Moved 61348 → 61764, and the 416 is four PRs' worth of drift rather than one jump** —
	// three of which rode *inside* `boardBoundSlack` (250) and so were never obliged to move the
	// bound, which is decision 0013's "a bound rots by the system working" in its plainest form.
	// The decomposition, each figure quoted from the PR that measured it rather than reconstructed
	// here, because a total reached by having the right number of terms is #155's own corrected
	// defect:
	//
	//	61348 → 61423   +75    #235, gate:gc rung 1 (ref.eq, ref.as_non_null, br_on_null/non_null,
	//	                       call_ref/return_call_ref)
	//	61423 → 61559   +136   #244, one constant-expression evaluator over four call sites (#241)
	//	61559 → 61577   +18    #247, gate:gc rung 2 (the six struct.* arms, decision 0020)
	//	61577 → 61764   +187   this PR, gate:gc rung 3 (the fourteen array.* arms)
	//	                =416
	//
	// Only the fourth crosses the slack, and it crosses it because rung 3 is the largest single
	// arm batch the GC campaign has: 187 sole-blocked fails forecast on #249's co-blocking probe
	// and 187 delivered, with the pass side moving by the same 187 and the arrivals column zero.
	//
	// **Moved 61764 → 61891, +127, landing gate:gc rung 4 — the three i31 arms (#255).** Itemized
	// per file from a before/after census of this lane, because a total reached by having the right
	// number of terms is #155's corrected defect and #161 requires the redistribution to *sum*:
	//
	//	ref_eq.wast          14/69 →  83/0    +69
	//	i31.wast              7/64 →  54/17   +47
	//	array_new_elem.wast  11/5  →  16/0     +5
	//	br_on_cast.wast       4/27 →   7/24    +3
	//	br_on_cast_fail.wast  4/27 →   7/24    +3
	//	                                     =127
	//
	// Five files moved and no other file moved in either direction; fail fell by the same 127, and
	// the arrivals column is zero — nothing re-keyed into a new bucket, the `fb 1c`/`1d`/`1e`
	// buckets are gone corpus-wide, and `i31.wast`'s residual 17 is 14 on `fb 17` (rung 5's casts)
	// plus 3 on #8's encoder frontier.
	//
	// **The three-arm batch outpaid its own forecast by 2.9×, and the reason is on the record rather
	// than in its favour**: #255's co-blocking probe forecast **44 of 71** sole-blocked on the
	// `fb 1c` bucket, which is the arms' *own* bucket and nothing downstream of it. `ref.i31` writes
	// state — `ref_eq.wast`'s `init` fills a table with boxed integers — so its absence was also
	// costing 68 read-backs keyed as `assert_return value mismatch`, which is #155's write-state
	// multiplier exactly. What makes the miss worth writing down is that `refop.go` had **already
	// predicted that drain in prose**, down to the direction (drain, not invert), and the forecast
	// quoted only the bucket probe: two instruments each held half the answer and the PR quoted one.
	//
	// **Moved 61891 → 62046, +155, of which only 113 are this PR's** — gate:gc rung 5's first slice,
	// the four cast opcodes `fb 14`-`fb 17` (`ref.test`, `ref.test null`, `ref.cast`,
	// `ref.cast null`). The other 42 are #257's (tail calls), which moved this lane and left the
	// constant alone because +42 rides inside `boardBoundSlack`; that is 0013's rot-by-working again
	// and the accounting closes it here rather than letting it compound a fifth time. **Attributed by
	// measurement, not by inference**: this lane reads **61891 pass / 409 fail at 23fb864^** (#257's
	// parent, a worktree run with the corpus linked in), 61933/367 after #257, and 62046/254 after
	// this PR — so the distance #256 left was zero, exactly as its own note above claims, and the 42
	// has a named owner rather than a plausible one.
	//
	// This PR's 113 itemized per bucket, departures against arrivals, because #161 requires the
	// redistribution to *sum* and a zero residual is the only form of that claim worth reading. All
	// 134 departures are `no arm for opcode`:
	//
	//	ref_test.wast          fb 14  50 + fb 15  18   = 68
	//	ref_cast.wast          fb 16  25 + fb 17  12   = 37
	//	type-subtyping.wast    fb 14   9 + fb 16   6   = 15
	//	i31.wast                          fb 17  14    = 14
	//	                                               =134
	//
	//	ref_test.wast       :: assert_return value mismatch          14
	//	ref_test.wast       :: trap: unreachable                      1
	//	type-subtyping.wast :: assert_return value mismatch           5
	//	ref_cast.wast       :: assert_trap (invoke) cast failure       1
	//	                                                             =21
	//
	// 134 − 21 = 113, residual zero, unclassified arrivals zero, and all four `fb 14`-`fb 17` buckets
	// are gone corpus-wide. `i31.wast` paid its residual 17 down to 3, which is the encoder frontier
	// (#8) the note above forecast.
	//
	// **The forecast missed in the pessimistic direction, 29 predicted against 113 delivered, and the
	// reason generalizes**: #258's co-blocking probe reasoned at *file* granularity and read
	// `ref_test.wast`/`ref_cast.wast` as shadowed, because each file's `init` calls
	// `any.convert_extern` (`fb 1a`, slice 3). What the probe missed is that `init` is a *sequence of
	// independent state writes* — the `table.set` calls before the `any.convert_extern` line all run
	// and populate the low table slots — so a blocker sitting inside such a sequence shadows only the
	// vectors downstream of its own line, not the file. A file-granular co-blocking forecast
	// over-predicts shadowing exactly there, and the instrument that settles it is the one used here:
	// the exhaustive two-direction bucket census, measured with `run(s).Buckets` rather than a grep
	// over the board log.
	// **62046 → 62089 (rung 5 slice 2: `br_on_cast`/`br_on_cast_fail`, plus grave #261).** Fail
	// 254 → 211, and the census closes in two parts because two different things landed:
	//
	//	the two arms                                                   −44 + 3 = −41
	//	  interp: no arm for opcode fb 18                                       −22
	//	  interp: no arm for opcode fb 19                                       −22
	//	  assert_return value mismatch                                          +2
	//	  trap: unreachable                                                     +1
	//
	//	grave #261 (match_deftype disjunct 2 was subtyping, not equality)        −2
	//	  br_on_cast.wast:205  (invoke "test-sub")   trap: unreachable          −1
	//	  ref_test.wast:329    (invoke "test-sub")   trap: unreachable          −1
	//
	// Residual zero in both parts, unclassified arrivals zero, and the whole delta is confined to
	// the three files. The three arrivals are charged to slice 3 rather than to these arms: `init`'s
	// last `table.set` is `any.convert_extern` (`fb 1a`), so table slot 4 is never written and holds
	// `ref.null any` — which *does* match a nullable `(ref null struct)`, so `null-diff(4)` answers 1
	// where the vector wants 0. Three of the four other index-4 reads pass only because null fails to
	// match their **non**-nullable targets, which is protection by coincidence and is why the arrivals
	// are 3 rather than 1.
	//
	// **The forecast missed in the optimistic direction this time: 36 predicted sole-blocked against
	// 41 delivered, 8 predicted arrivals against 3.** Recorded because the previous entry's miss was
	// pessimistic, and the two together say the co-blocking probe's error is not signed — it is a
	// *variance*, so the honest use of a pre-registered figure is as a falsifiable prediction and
	// never as a floor the PR is scored against. The specific over-prediction has the same root as
	// the note above: reasoning about `init` as a unit rather than as a sequence, one layer finer.
	//
	// #261's two vectors are the reason this figure is not simply the arms' 41. Both are
	// `(invoke "test-sub")`, and `ref_test.wast:329` was **already failing at 6f6c18c** — the grave's
	// board witness pre-dates slice 2 by one merge, in slice 1's own file, which is what establishes
	// that the defect is the shared matcher's and not the branching arms'.
	// 62089 → 62113, +24 (grave #266, the harness's null fidelity). The whole delta is one file:
	// `ref_null.wast` 10/34 → **34/34**, measured by diffing this lane's per-file lines rather than
	// reasoning from the total. Two harness deviations from the reference, both accept-direction and
	// both invisible to every vector that was passing: `Matches` compared a null's Kind, where the
	// reference has exactly one heaptype-free null value (`runtime/value.ml:20`/`:112`/`:151`) and
	// `assert_ref_pat` answers `NullPat _, NullRef -> true` unconditionally (`runner.ml:476`); and
	// `fromInterpValue` refused a null whose *type* `valKind` could not name, making the value
	// unrepresentable on the strength of a type nothing downstream reads.
	//
	// **The pre-registered forecast on #258 was 21 and the answer is 24 — a wrong number with sound
	// reasoning, which is the more useful half to record.** The prediction was "all of
	// `ref_null.wast` pays; `try_table.wast` 464–466 does not", and the board confirms both clauses
	// exactly (try_table unmoved at 3 fail). The 21 came from subtracting those 3 non-null vectors
	// from the 24 nulls, two *disjoint* sets — an arithmetic slip inside a correct model, not a
	// mis-read of the corpus. Worth the space because the reflex it argues for is to reconcile a
	// miss against the file list before reaching for an explanation: had the extra 3 been assumed to
	// be try_table's, the coincidence of magnitude would have closed the account on a false story.
	// **62113 → 62173, rung 5 slice 3 (#258).** The two `ExternConvert` arms plus the host-reference
	// boundary moved this lane by +35 (62138 → 62173, fail 162 → 127) and the default lane by
	// nothing — `extern.wast`'s vectors are `gated` there, which is the structural zero a gate
	// campaign has instead of an `unsupported` delta. The distance was already 25 before this PR and
	// would have been 60 after it, both inside the 250 slack and therefore both silent; raised here
	// because the rule is a distance of zero in the PR that moves the board, not a distance under
	// the slack. `unsupportedCeiling` is deliberately unmoved at 2689: this PR answers no declined
	// question, and #270 is where the 28 reference-shape vectors go.
	// **62173 → 63329, the validator's first slice (#9), and the +1156 has two parts that must not
	// be reported as one.**
	//
	//	pre-existing drift, this bound against HEAD (052b45b)                      +69
	//	this PR: the 2697 assert_invalid commands, scored under every gate on    +1087
	//
	// The first figure is a finding about the instrument, not about the engine. This bound was
	// **already 69 low at HEAD** — the lane read 62242/90 there, against a bound of 62173 whose own
	// comment (above) says the rule is a distance of zero in the PR that moves the board. Some merge
	// between #258 and #290 moved the lane and left the bound behind, silently, because 69 is inside
	// the 250 slack. That is the degradation this bound's own history is the case study for, arriving
	// a third time; it is named here rather than folded into this PR's delta, because a bound that
	// absorbs someone else's drift into a big jump has laundered it.
	//
	// The second figure reconciles exactly and in both columns, measured by diffing this lane's
	// per-file lines against a HEAD worktree (decision 0007/#161's standing rule, never arithmetic
	// on the total):
	//
	//	                      default lane      all-gates-on lane
	//	assert_invalid pass          1023                   1087
	//	  declined                   1059                   1347
	//	  admitted                    142                    250
	//	  wrong message                10                     13
	//	  gated                       463                      0
	//	                             ----                   ----
	//	                             2697                   2697
	//
	// So this lane's pass delta is 1087 and its fail delta is 1347+250+13 = 1610 (90 → 1700), both
	// residual zero. The lane's fails exceed the default lane's 1211 because the 463 vectors the
	// default lane declines to a *gate* are here answered by the validator, which declines 288 more
	// of them to its own slice boundary and admits 108 more — the two populations drain by different
	// mechanisms (see DeclinedFunc), and this is the measurement that shows it.
	//
	// Two files enter this lane's per-file report for the first time (254 → 256): `memory_size3.wast`
	// and `unreached-invalid.wast` had *no* attempted command before, being wholly `assert_invalid`.
	// A file whose every vector was unsupported is invisible to a per-file board, which is worth
	// knowing about the report and not only about these two files.
	//
	// # Slice 2 (#305): 63329 → 63977, and the delta is the default lane's exactly
	//
	// Both lanes moved **+648 pass**, and the redistribution is identical in each — declined −668,
	// admitted +16, wrong-message +4:
	//
	//	                      all-gates-on lane, slice 1 → slice 2
	//	assert_invalid pass          1087 → 1735
	//	  declined                   1347 →  679
	//	  admitted                    250 →  266
	//	  wrong message                13 →   17
	//	  gated                         0 →    0
	//	                             ----    ----
	//	                             2697    2697
	//
	// The stratum's total closes against this lane's board too — **and the decomposition this
	// sentence used to give was wrong in both of its terms** (grave #312). It read "962 validate fails
	// + 90 from the other strata = the 1052 the lane reports"; re-measured at that same revision, the lane's
	// split is **952 validate + 100 other (46 encode, 54 exec)**. Both terms are off by 10 in
	// opposite directions, so the identity closed to 1052 either way — which is the defect the
	// destination ledger's own header names, *errors of opposite sign cancel into a plausible total*,
	// caught here by re-measuring the parts while re-basing the whole rather than by anything the sum
	// could ever have said. A total that agrees is not evidence that its addends do.
	//
	// **Two lanes moving by exactly the same 648 is a suspiciously clean agreement, so the mechanism
	// is stated rather than the coincidence admired**: core SIMD is default-*on* since #227/#233, so
	// the 0xFD vectors this slice converts are gated in neither lane and both see the same
	// conversion. What differs between the lanes — relaxed SIMD's 0x100..0x12f — contributes 0 to
	// this delta, because with the relaxed gate on those opcodes reach `vecSignature` and hit its
	// relaxed decline arm, which is a decline exactly as the region-wide decline before it was. A
	// figure identical across two lanes is either one cause visible twice or two errors of the same
	// size; this one is the first, and the sentence that says so is the difference.
	//
	// # Slice 3 (#306): 63977 → 64072, and this time the lanes differ — by exactly one file
	//
	// The default lane moved +58 and this one +95, so the *disagreement* is the figure worth reading.
	// Per-file, measured on both lanes at both revisions rather than inferred from the totals:
	//
	//	                       default lane    all-gates-on lane
	//	align.wast              121 → 159       121 → 159        +38
	//	align64.wast              (gated)       120 → 157        +37   ← the whole difference
	//	simd_align.wast          88 → 100        88 → 100        +12
	//	simd_{load,store}{8,16,32,64}_lane.wast   +1 each         +8
	//	                        ---------       ---------
	//	                             +58             +95
	//
	// **The extra 37 is `align64.wast` and nothing else**: memory64's own alignment corpus, gated on
	// the default board, converted by the same rule with the gate on. That is the property to want
	// from an alignment rule — it reads the memarg's alignment exponent and the operation's natural
	// width, never the memory's index type, so a 64-bit memory's vectors convert without a second
	// rule — and it is *checkable* here precisely because the two lanes' deltas are unequal. Slice 2's
	// paragraph above had to argue a suspiciously clean agreement down to one cause; this one has a
	// disagreement, and its account is complete only when every vector of the gap is attributed to a
	// file.
	//
	// The redistribution is the default lane's shape, at this lane's size — measured, since the
	// paragraph above is a record of what happens when a split is asserted from arithmetic:
	//
	//	                       all-gates-on lane, slice 2 → slice 3
	//	validate stratum            952 → 857
	//	  declined                  679 → 679    unmoved
	//	  admitted                  266 → 175    −91  = the default lane's −54 plus align64's 37
	//	  wrong message               7 →   3    −4   = the same four vectors, in both lanes
	//	other strata (46+54)        100 → 100    unmoved
	//	                           ----   ---
	//	                           1052   957
	//
	// `declined` unmoved in both lanes is the sentence: 95 rules became *right*, and none became
	// *known*. Note the difference from the default lane in what remains — `validateMismatchCeiling`
	// is 0 there and this lane keeps 3, so the wrong-message population is drained in the default
	// board's subject and not in the corpus.
	//
	// **The floor did not fire, and 95 < 250 is why**: this lane's measurement drifted a third of the
	// way through `boardBoundSlack` while the constant stayed green. That is not the slack
	// misbehaving — it is what slack *is* — but it is why the re-base is taken from the printed total
	// rather than from whether the bound complained. A floor left behind by a jump it can explain
	// stops catching anything smaller than the jump.
	//
	// # `select t` (#294): 64072 → 64075, three where the default lane took two
	//
	// Same PR as slice 3 and the same class — the decoder dropping what the validator needs — so the
	// two deltas are quoted apart rather than as the +98 this lane actually moved. The third vector is
	// the whole content of the asymmetry: `select.wast:368` and `:373` convert on both lanes, and
	// `ref.wast:78` (`(select (result (ref 1)))`, expecting `unknown type`) converts only here,
	// because a concrete `(ref 1)` annotation needs the GC gate to decode at all. Slice 3's extra 37
	// was one gated *file*; this one is one gated *type form* inside a file the default lane already
	// scores — `ref.wast` is 1/13 → 2/13 here and 1/1 with 12 gated there.
	//
	// The redistribution, measured at both revisions on this lane, and it moves the sub-population
	// slice 3 left alone:
	//
	//	                       all-gates-on lane, slice 3 → #294
	//	validate stratum            857 → 854
	//	  declined                  679 → 676    −3   ← the two select vectors plus ref.wast's
	//	  admitted                  175 → 175    unmoved
	//	  wrong message               3 →   3    unmoved
	//	other strata (46+54)        100 → 100    unmoved
	//	                           ----   ---
	//	                            957   954
	//
	// **Read the two tables together and the PR's shape is the argument for having folded the issues:**
	// slice 3 drained `admitted` and `mismatch` with `declined` untouched, #294 drains `declined` with
	// the other two untouched. One PR, one cause (retention through the decoder), and two disjoint
	// sub-populations — which is a stronger claim about the seam than either delta could make alone,
	// and it is only visible because the sub-partition is three counters rather than two and a
	// subtraction.
	//
	// # `blockType`'s valtype form (#311): 64075 → 64078, and **no instrument asked for this re-base**
	//
	// `check_blocktype` calls `check_valtype` on the single-valtype arm and `blockType` did not, so
	// `(block (result (ref 1)))` typed successfully against a module declaring no type 1. Accept
	// direction, which is why the default lane is **+0 and stays there**: a concrete `(ref N)`
	// annotation needs the GC gate to decode, so on that lane the rule has no subject and all four of
	// its stratum ceilings are unmoved. One file moves, `ref.wast` 2/13 → 5/13, and *nothing else in
	// either lane moves in either direction* — worth measuring rather than assuming, because the new
	// call fires when a block **opens** and could therefore preempt a later rule's message, turning a
	// correctly-refused vector into a wrong-message one. Zero preemptions, so the fail delta is
	// exactly −3 and not −3 net. A net figure hides a regression inside a reward.
	//
	//	                       all-gates-on lane, #294 → #311
	//	validate stratum            854 → 851
	//	  declined                  676 → 676    unmoved
	//	  admitted                  175 → 173    −2   ← the accept-direction drain
	//	  wrong message               3 →   2    −1   ← a module already refused, now refused
	//	                                              for the reason its vector names
	//	other strata (46+54)        100 → 100    unmoved
	//	                           ----   ---
	//	                            954   951
	//
	// **A third sub-population shape, after slice 3's and #294's**: slice 3 drained `admitted` and
	// `mismatch`, #294 drained `declined` alone, and this drains `admitted` and `mismatch` again while
	// `declined` holds — the same pair as slice 3, from a rule with an unrelated cause. So the pairing
	// is not a signature of the seam; what distinguished the seam was the *disjointness* of the two
	// drains within one diff, not either drain's own shape. Recorded because the earlier paragraph is
	// one careless reading away from claiming more than it established.
	//
	// **And the re-base itself was discretionary, which is the part worth keeping.** This floor's slack
	// is `boardBoundSlack` = 250; an actual of 64078 against a bound of 64075 is a distance of 3, so
	// `boardBound` neither errored nor reported staleness. This lane carries **exactly one** bound in
	// total — the three slack-0 ceilings that would have fired on a one-vector move are in
	// `TestPhase1Files`, where this delta is 0. So #311's reward is real and invisible to every
	// instrument in the repo, and every figure in the table above was **pre-registered on the issue
	// before the fix was applied** and then confirmed, because a silent floor cannot distinguish a
	// derived number from a copied one. The pre-registration is the check the instruments could not
	// perform; the tracker holds it, deliberately outside the diff it judges. (Condition: Scott, on
	// the #313 thread — *"otherwise the tool prints a number and agrees with itself, which is the same
	// shape as a frozen snapshot passing."*)
	//
	// That the corrected decomposition in the tables above is itself asserted by nothing is grave
	// #312's live remainder, and #315 rather than a fix here.
	// # 64078 → 64079: the offset bound (#310), discretionary again and for the same reason
	//
	// One vector, `align.wast:1004`, on both lanes. Distance 1 against a slack of 250, so this bound
	// said nothing — the third consecutive re-base here that no instrument asked for, where the
	// *default* lane's two sub-ceilings both fired on the identical single-vector move because their
	// slack is 0. Two lanes, one movement, opposite instrument behaviour: that contrast is #315's
	// subject and it is recorded here because this is the bound that is quiet.
	// # Slice 5 (#322): 64079 → 64631, +552 against the default lane's +358, and the gap is four files
	//
	// The index-space family, and this bound's own before-state was **exactly 64079** — distance 0,
	// no inherited drift for the first time in this comment's history. It fired here, at a distance
	// of 552 against a slack of 250, which makes slice 5 the first entry that is not discretionary.
	//
	// The 194-vector gap between the lanes, attributed per file by diffing both lanes' per-file lines
	// at both revisions (0007/#161, never arithmetic on the total):
	//
	//	                          default lane    all-gates-on lane    gap
	//	memory_init64.wast          29 →  31       183 → 250            65
	//	memory_fill64.wast          11 →  12        36 → 100            63
	//	memory_copy64.wast        3885 → 3886     4386 → 4450            63
	//	table_copy_mixed.wast          (0)           1 →   4             3
	//	                          ----------      -----------          ---
	//	                                                               194
	//
	// Every vector of the gap is memory64's, and the shape says something the totals cannot: the three
	// `*64` memory corpora move by 1 or 2 in the default lane and by 63-67 here, because the *rules*
	// are the same rules and what differs is whether a 64-bit memory can be declared at all. The
	// fourth file is the interesting one — `table_copy_mixed.wast` moves 1/4 → 4/4 here and **0** in
	// the default lane, and it is `minAddrType`'s only corpus subject: a copy between a 32-bit and a
	// 64-bit table, whose length operand the reference's `min at1 at2` makes an i32. So the rule with
	// the least corpus coverage in this slice is the one whose whole coverage is in this lane, which is
	// the argument for the lane restated per-rule rather than per-file.
	//
	// The redistribution, measured on this lane at both revisions rather than inferred:
	//
	//	                       all-gates-on lane, #310 → slice 5
	//	lane total pass           64079 → 64631   +552
	//	lane total fail             950 →   398   −552
	//	  declined                  676 →   124   −552   ← drains alone
	//	  admitted                  172 →   172   unmoved
	//	  wrong message              12 →    12   unmoved (2 of them the validator's, also unmoved)
	//	  gated                        0 →     0
	//
	// **The rows are the lane's own printed totals and its bucket census, not an `assert_invalid`
	// decomposition** — the tables above this one quote `assert_invalid pass` for their eras, and this
	// lane does not report that figure: `TestAllGatesOnLeavesNothingGated` prints pass/fail/gated and
	// per-file failure buckets, and the destination ledger runs on the default board. Writing an
	// `assert_invalid pass` row here would have meant deriving it by subtraction, which is grave
	// #312's exact defect — a decomposition asserted from arithmetic, agreeing with its own total. So
	// the row is absent rather than computed, and the three sub-populations quoted are counted from
	// the bucket keys the lane does print.
	//
	// **`declined` draining alone in both lanes, by each lane's own figure, is the slice's signature**
	// — and it is the exact complement of slice 3's shape, which drained `admitted` and `mismatch`
	// with `declined` untouched. A slice adds vocabulary; a rule fix makes vocabulary right. This one
	// is entirely the former in both lanes, which is what a correct slice looks like and is worth
	// having a measurement of, since the alternative (some of the 552 landing in `admitted`) is how a
	// slice that types things *wrongly* would also show a large pass delta.
	//
	// The 12 wrong-message rows are 10 board-wide non-validator ones plus this lane's 2, which is why
	// `validateMismatchCeiling` reads 0 on the default board while this lane keeps a pair. Stated
	// because the two figures look like a disagreement and are two different populations.
	// **64727 → 64563, #341: −164, and this lane is where the slice's finding actually lives.**
	//
	// The default board's −46 is the same event with four fifths of it gated off. Here: +22 encode
	// (the emitter frontier, identical rows in both lanes), +129 declined (the same 24 plus the GC and
	// tail-call operators the default lane never decodes), and **+13 over-rejections** — modules the
	// type checker ran on, finished, and refused, that the corpus says are valid. Those 13 are the
	// reason #341 was taken before more validator slices: they are engine defects that no board could
	// see, because an over-rejection produces no error for anyone to bucket. `validateOverRejectCeiling`
	// is the default lane's bound on the same population and reads 0 for a structural reason stated
	// there; TestModuleDefinitionsAskTheValidator is what pins these 13 by row.
	//
	// A floor falling on this lane says the same thing it says on the default one and is argued at
	// `passFloor`: the assertion arrived, the engine did not regress. Pre-registered on #341 with both
	// lanes' figures before the arm changed; the forecast was `64727 → 64563`.
	//
	// **64563 → 64567, #343 cause 2: +4, and this is the shape a repair makes rather than a slice.**
	// `call_indirect` now takes its index operand from the table's address type, so four valid table64
	// modules stop being refused. The +4 is *exactly* the four module commands and nothing else,
	// forecast before the board was run and confirmed: over-rejections 13 → 9, all-on fail 483 → 479,
	// **and no cascade**, because #341's arm produces the instance even when validation refuses — so
	// these modules' dependent vectors were already running and already scored. A repair that unblocked
	// downstream work would show a delta larger than its own row count; this one cannot, and the fact
	// that it cannot is a property of that design decision rather than of this fix.
	//
	// The default lane is unmoved at 60790, all four vectors being table64. That is the structural zero
	// `validateOverRejectCeiling` documents, seen from the other side: a lane that gates a feature off
	// can neither over-reject it nor be credited with the repair.
	// **64567 → 64592, #343's GC-subtyping slice: +25, and the ledger does not read +30.** The
	// criterion ADR 0031 pre-registered was 30 rows — 21 `assert_invalid` modules expecting `sub type`
	// that this validator accepted, and the 9 remaining `moduleOverRejections`. All 30 converted,
	// confirmed row by row rather than inferred from the total. The difference is **five vectors that
	// were passing on the defect being fixed**: `br_if.wast:667`, `br_on_null.wast:81`,
	// `br_on_non_null.wast:91`, `br_on_cast.wast:271` and `br_on_cast_fail.wast:286` are the
	// gc/issues/516 family, whose modules contain a `(ref null $t)` returned as `funcref` — valid, and
	// over-rejected by index equality with the message `type mismatch`, which is the very text the
	// `assert_invalid` expects. They were green because the wrong refusal happened to say the right
	// thing. With the relation correct the module reaches an out-of-slice `ref.null`, and each is now
	// an honest decline.
	//
	// *A total is not a ledger*, and this is what the ledger buys: +30 −5 = +25, all-on fail 479 → 454.
	// A reader given only the total would have to take "the slice landed" on faith, and a reader given
	// only the forecast would read the 5 as a regression. Neither is true: nothing the engine does got
	// worse, and five rows stopped being carried by a coincidence. Filed as its own finding, because
	// *the shape of what survives names the bug* — an `assert_invalid` vector can be held up by an
	// over-rejection inside the module, and the over-rejection instrument cannot see it because the
	// row is a pass. #350.
	//
	// The default lane is unmoved at 60790 pass / 235 fail / 66 unsupported, with its stratum split
	// byte-identical including the structural `0 over-rejected`. Every one of the 30 needs the GC gate,
	// so that lane could neither see the defects nor be credited with the repair — the same asymmetry
	// this bound's #341 and cause-2 entries above both record, and the reason #343's PR quotes this
	// lane's fail delta as its reward figure.
	// **64592 → 64654, the reference-type slice (#359): +62, and the number is smaller than the rows
	// it moved.** 107 declines drain from the five mnemonics this slice types — `ref_null` 41,
	// `ref_func` 36, `table_get` 18, `ref_is_null` 6, `table_set` 6, each to exactly zero — and **45 of
	// those vectors decline again on the next out-of-slice instruction in the same module**, mostly
	// `return_call_ref`, `ref_test` and `ref_i31`. So this lane's declined column reads 258 → 196
	// rather than 258 → 151, and 107 − 45 = 62 closes against the pass delta to the row.
	//
	// **That shape is invisible to any single figure, and it is why the composition was diffed rather
	// than the total quoted.** A decline that moves *within* the column looks identical to a rule that
	// did not land: both leave the column higher than the drain predicted. The discriminator is
	// per-mnemonic, and the five reaching exactly zero is what says the rules work — a partially
	// correct rule would leave a remainder under its own name. The default lane has no such effect: its
	// 47 drain straight to passes, because a module whose next unsupported instruction is a GC operator
	// never decodes there in the first place.
	//
	// The #359 forecast pre-registered `258 → 192` for this lane and it **missed by 4**, having
	// estimated 41 advances where there were 45. Recorded because the forecast's own text called this
	// lane's figure "deliberately looser" — a hedge that turns out to have been pointing at the right
	// mechanism and under-sizing it, which is a different thing from being right.
	// **64654 → 64798, slice 8 (ADR 0034): +144, and only 55 of that is slice 8.** The other **89 is
	// staleness this bound accumulated and nothing reported**, which is the finding of the entry and
	// the reason it is written before the slice's own arithmetic. The constant stood at 64654, written
	// by the #359 entry directly above, against an actual **64743** when this slice started. Two PRs
	// moved the lane and left the bound behind: #378 (the linker's type comparison) and **#382, slice 7
	// itself**, whose ADR records `377 fail → 303` on this very lane and which does not touch this
	// constant. Neither fired, because 89 is inside `boardBoundSlack`'s 250, and a bound sitting 89
	// under its subject cannot catch any regression smaller than 89.
	//
	// **The failure mode is accumulation, not a single large jump**, and that distinction is what the
	// slack's own comment did not predict. It predicted the abstract version — "a bound left behind by
	// a large jump degrades into decoration" — and the mechanism intended to make re-basing unnecessary
	// is exactly the mechanism that made forgetting it invisible: each move was too small to trip the
	// slack, so no PR was ever told. Recorded here and filed against the bound rather than against the
	// slack, because widening the slack is this defect one level up and narrowing it is #42's business.
	//
	// Slice 8's own contribution is **+55, and it closes exactly**: 303 fail → 248, declined 122 → 67,
	// pass 64743 → 64798, three columns on one figure, with all six mnemonics reaching exactly zero.
	// It is the first slice in this ledger whose forecast did not lose rows to *re-declining*, and
	// ADR 0034 argues that is a property of being **last** in the single-byte space rather than of the
	// forecast being sharper: slice 7's residue was precisely these six opcodes, and there is nothing
	// left behind them for these modules to decline on. A clean number that has an explanation is worth
	// more than a clean number, and the per-file verification is in the ADR because an exactly-closing
	// total is also what a blind instrument produces.
	//
	// The default lane is byte-identical — 60837 pass, 188 fail, 66 unsupported, 4053 gated, `fail by
	// stratum` unchanged term for term — all six opcodes being `gate:gc`. That is the fourth entry in
	// this comment to record the same asymmetry, and by now it is the expected shape of a gated slice
	// rather than an observation.
	// **64798 → 64833, slice 9 (ADR 0035): +35, and every one of the 35 was pre-registered.** The
	// forecast named 28 reject rows, 6 accept rows and 1 admission; the lane moved 248 fail → 213 and
	// 64798 pass → 64833. An exactly-closing total is also what a blind instrument produces, so it was
	// read three ways rather than once: per file (`return_call.wast` 34/49 → **49/49**,
	// `return_call_indirect.wast` +19 to 80/81, `call_indirect.wast` +1 to 169/170), per opcode (the
	// all-on decline census 67 → **33**, which is −34 and is exactly `return_call_indirect` 18 +
	// `return_call` 16), and by residue (the 33 that remain are exception handling 25 and relaxed SIMD
	// 8, both declared out of scope). Three readings of one number, agreeing.
	//
	// The two rows *not* claimed are the two this slice declared out of scope: `unknown function 0` in
	// `return_call_indirect.wast` and `unknown function 7` in `call_indirect.wast`, both #391's
	// elem-segment rule and both still admissions. A file at 80/81 is the honest shape of a slice that
	// closed its own stratum and not its neighbour's.
	//
	// # 64833 → 64862, slice 10 (ADR 0036): +29, and the split is the content
	//
	// **25 declines and 4 admissions**, pre-registered in the ADR as the criterion and closing at exactly
	// that. The 25 are the exception-handling family becoming vocabulary — `throw`, `throw_ref`,
	// `try_table` and the clause forms — and the 4 are refusals this validator was not making: 2 tag
	// result types (`check_tagtype` over both the defined and the *imported* tags, `tag.wast:18,22`) and
	// 2 element-segment `ref.func` resolutions, which are the two rows the entry above declined to claim
	// and #391 now closes. So the slice above's honest 80/81 becomes 81/81 here, by the rule it named.
	//
	// An exactly-closing total is what compensating errors produce, so it was read three ways with three
	// unshared mechanisms, on Scott's instruction: this floor's +29, the all-on **decline census 33 → 8**
	// (−25, per opcode), and the ledger's **`accepted` 71 → 67** (−4, per destination). −25 + −4 = −29,
	// and no reading shares an input with another. Per file: `tag.wast` 6 → 8, `throw.wast` 9 → 13,
	// `throw_ref.wast` 12 → 15, `try_table.wast` 48 → 63, `call_indirect.wast` 169 → 170,
	// `return_call_indirect.wast` 80 → 81, `instance.wast` 0 → 3.
	//
	// The residue is **8**, all relaxed SIMD, whose gate is its own event (ADR 0025) — so with this slice
	// the decline column stops naming any *unwritten rule* on either lane. `try_table.wast`'s three
	// remaining fails are the harness's own result-representation limit (`result 0 has type (ref null
	// 0)`), which is `TestGrave206KnownFailures`' population and not this bound's.
	//
	// # 64862 → 64900 with #328: +38, all of it out of `accepted`, and 10 of it invisible to the other
	// lane
	//
	// The all-on admission census goes **66 → 28** and the fail count 184 → 146, so three readings agree
	// on 38 with no shared input: this floor's +38, the census's −38, and the fail column's −38. Single
	// destination, so it is a *correctness* gain in the ledger's vocabulary — 38 rules became right and
	// none became known, `declined` unmoved at 8.
	//
	// **The 28 the default lane converted are a subset, and the 10 remaining are the reason this bound
	// is not redundant with `passFloor`.** Those 10 need a gate to decode at all — GC types and
	// reference subtyping in const expressions — so they are rules the default lane cannot ask about.
	// The residue of 28 breaks down 14 `unknown type`, 9 `type mismatch`, 4 `uninitialized local`, 1
	// `constant expression required`, and it is the *entire* remaining admission stratum on either lane.
	//
	// The part worth reading is `type-rec.wast`'s 8 `type mismatch` admissions going to **3**, because
	// those 8 are the pre-registered witnesses for iso- versus equi-recursive type equality — the
	// population `binary.CompType.RecStart`/`RecLen` was retained for, whose falsifier was blocked
	// behind exactly this slice's unwritten rule. Five convert, which is the answer: `sameDefType`'s
	// group-shape comparison is right. The three that survive (`:51`, `:204`, `:216`) are **not** that
	// relation failing — they are `inlineFuncType` reusing a functype that is a *member of a
	// multi-member rec group* where `inline_functype` only ever reuses `DefT (RecT [st'], 0l)`, a
	// singleton (parser.mly:222-235). So `ref.func $f` gets the group member's index, the want and the
	// got are literally the same index, and no relation over them can disagree — so the module is
	// admitted one layer below where anyone was looking. Filed as **grave #402** with that diagnosis
	// rather than fixed here: the repair needs rec-group extent in `resolvedComp`, which the text type
	// table does not carry, and un-reusing a type shifts every subsequent index in every module with an
	// inline signature. Its own slice, with the 3 pre-registered there.
	//
	// **A split, not a verdict** — the available oracle answered its half (5 conversions certify
	// `sameDefType`) and the residue turned out to be a different defect wearing the same vectors,
	// which is why the 8 do not stay one number.
	//
	// # 64900 → 64903 with #402: the 3 pre-registered admissions, and nothing else moved
	//
	// `inlineFuncType` now carries `inline_functype`'s third condition — the reused type must be the
	// sole member of its rec group — and `type-rec.wast` goes 19/26 → 22/26, the `type mismatch`
	// admission bucket 3 → 0. Three readings agree on 3 with no shared input, as with #328: this
	// floor's +3, the fail column's 146 → 143, and this lane's admission total 28 → 25 — that last
	// one summed from the per-file boards `RunGated` prints, by `admittedKeyPrefix`, because the
	// census *loop* below is the default lane's instrument and this lane has none of its own.
	//
	// **One of #402's own premises was false and the fix is smaller than its issue predicted.** It
	// said the repair "needs rec-group extent in `resolvedComp`, which the text type table does not
	// carry"; `context.recExtents` has carried exactly that since grave #349, for the encoder, and
	// `soleMemberOfItsGroup` reads it. No field was added and no type shifted that the extent list
	// could not already explain. The estimate was written from the wrong side of the seam — the
	// grouping is a property of the *table*, not of an entry — which is the same distinction
	// `recExtents`' own comment draws in the sentence declining to put it on `compType`.
	//
	// **#351's witness was spent here, and it performed** — which is the half that issue said a slice
	// could get wrong by converting the eight and counting them. With `sameDefType`'s ordinal-and-
	// group-length condition replaced by `if false`, `type-rec.wast` goes 22/26 back to 19/26: exactly
	// `:51`, `:204`, `:216` revert to admissions. Before #402 the same neuter left those three
	// untouched, because `inlineFuncType` had given want and got the same index and no relation over
	// them could disagree. So the rows now reach the comparison, and the comparison is what rejects.
	//
	// Two caveats on that, neither of them softening it. The neuter removes the *representation-
	// dependent* fact #351 said was asserted by construction; it is not a full bisimulation port, so it
	// falsifies that specific claim rather than standing in for equi-recursive equality entire. And the
	// same neuter costs a fourth row — `tag.wast`'s `assert_unlinkable` — which dies with or without
	// #402 applied, so the corpus had a live witness for this property all along, in the linking
	// stratum that #351's file-and-expected-text enumeration could not see. `sameDefType`'s header
	// carries that finding and ADR 0031 has a second dated falsification section for it.
	//
	// **The forecast pre-registered on #402 was 3 here and 0 on the default lane, and both halves
	// held**: `passFloor` did not move and the default board is 60868/157/66/4053 unchanged. That zero
	// is not structural — it is the gates doing their job, since every one of these vectors needs GC
	// types to decode at all, so the default lane cannot ask the question. Which is also why the
	// residue of this bucket was invisible until #328: it took a typing rule *and* a gate to reach.
	//
	// # 64903 → 64938 with #413's start section, and this lane's job in that slice was to hold the
	// # other lane's attribution
	//
	// The default lane gained 27 where its own census forecast 35 (`passFloor`), and the eight-row gap
	// was diagnosed to `start0.wast`: three memories at :2, so the row's *second* blocker is the
	// multi-memory gate and eight rows land gated instead of passing. That diagnosis is a claim about
	// a gate, and **this lane is the instrument that can refute it** — with every gate on, a
	// multi-memory blocker does not exist, so the eight must appear here or the diagnosis was wrong
	// and the engine is answering eight vectors incorrectly.
	//
	// +35, the full count, all 35 of the rows that left the default lane's fail column. So the gap is
	// the gate and not the engine, measured rather than argued. Stated as this lane's *purpose* in the
	// slice because the alternative reading of a +35 here is that this lane simply gained more than
	// the other one, which is the shape of every uninformative two-lane comparison.
	//
	// # 64938 → 64978 with #419's table initializer, and this lane's job was to hold the *layer*
	// # attribution rather than the gate one
	//
	// +40 where the default lane took +19, and 38 of the 40 are the four planned layers: the 19-row
	// difference is where the two lanes' accounts meet, 14 of it the `0x40` form's GC gate
	// (`passFloor`'s table names those rows per file, and with every gate on they pass here instead of
	// gating), and the remaining **5 are commands this lane scores and the default lane does not** —
	// `table.wast`'s all-on-only rows, 6 of which were failing on main and 5 of which now pass. So
	// 33 + 5 = 38, against a default 19 + 14 gated = 33, and the same 33 rows are visible in both.
	// The other 2 have their own section at the end of this comment.
	//
	// **The three layers' figures are removal tests, and they are not a partition — they overlap, and
	// they have to.** Each was measured by neutering one layer with the other three in place: dropping
	// `check_table`'s `check_const` costs 10 rows here and 0 on the default lane, neutering `newTable`'s
	// fill costs 7 and 0, and the encoder's own contribution is the 33 above. 33 + 10 + 7 = 50 against
	// a total of 38, which is not an error in any of the three: a vector whose module the emitter could
	// not write *and* whose verdict needs the new validator rule is counted by both tests, because
	// either omission alone leaves it red. Stated because summing removal tests into a partition is the
	// arithmetic this decomposition invites, and the sum being larger than the whole is the tell.
	//
	// The 0 on the default lane in both of those removal tests is not structural and not a gate doing
	// its job for free: it is the same fact from the other side. The rules' witnesses are vectors whose
	// tables carry a spelled initializer, which is the gated form, so **the default lane cannot ask
	// either question** — which is exactly why #419's validate and interp layers each shipped with a
	// unit control (`TestTableInitializerSeesNoDefinedGlobal`,
	// `TestTableSlotsHoldTheInitializersValue`) rather than leaning on this lane.
	//
	// **The last 2 of the 40 are the fifth layer nobody planned, and this lane is the only instrument
	// that found it.** `declaredFuncs` — the validator's `Free.module_` analog — did not walk a
	// table's initializer, so `(table $t 10 (ref func) (ref.func $f))` reported `$f` undeclared when
	// the table field is the very thing that declares it. That is an **over-rejection**: two valid
	// modules refused, `elem.wast:87` and `table.wast:93`, both of which had been arriving here as
	// encoder declines and started reaching the validator in this PR. The reject direction's whole
	// corpus is blind to it by construction — no `assert_invalid` watches a rule that refuses too
	// much — so the finder was the over-rejection table below, whose own vacuity guard this PR also
	// had to move. `declaredFuncs`' comment had *predicted* the failure and named the surfacing
	// condition ("the moment the encoder learns the field"), and its tracking citation was #8, the
	// encoder's issue; nothing tracked the gap itself. Which is the fifth layer's lesson and not a
	// grave: the prose was right, the tripwire was somebody else's issue number, and an instrument
	// caught what a citation did not.
	// # 64978 → 64982, +4, and the fact worth recording is that it equals the default lane's
	//
	// #77's four `func.wast` rows move both lanes by the same amount, which is what a fix with no gate in
	// it should do and is therefore a check rather than a note: a slice whose lanes move *unequally* has
	// touched a gated path, and the two entries before this one are both of that kind — #413's and #419's
	// falls were partly absorbed by the GC gate, so their default and all-on deltas differ by exactly the
	// gated share. Here the emitter's output is the same wire form with one index byte corrected, so a
	// divergence between the lanes would have meant the fix reached a feature path it has no business in.
	// # 64982 → 64985, +3 against the default lane's +1, and the gap is the check from the entry above
	//
	// Grave #130's repair lets `memory-multi.wast:5` encode, and with every gate on all three of that
	// module's rows pass: the module itself plus the two `assert_return`s. The default lane gains only
	// the module (`passFloor` +1) because the assertions need the interpreter to hold two memories.
	//
	// So the lanes move **unequally, by exactly 2**, and the entry above says what that means: a
	// divergence is the signature of a fall that a gate partly absorbs. #77's slice moved both lanes by
	// 4 and that equality was its check; this slice's inequality is its check, and the two are the same
	// instrument read in the two directions. What would have been the finding is a divergence that
	// *didn't* match the gated share — +3 here against +1 there with only one gated row in the file
	// would mean a row moved for a reason nobody named. Two gated rows, gap of 2, named above in
	// `gatedVectors`' allow-list: accounted.
	// # 64985 → 64993, +8 against the default lane's +8, and the *equality* is the check this time
	//
	// #427 types relaxed SIMD in the validator. Read through the instrument the two entries above set
	// up: an equal move means the repair touched no gated path, and that is exactly the property being
	// claimed, because the thing repaired was a **validator** decline and `internal/validate` reads no
	// feature set. Had these lanes diverged, the fix would have reached something gate-dependent and the
	// diagnosis in #427 — a typing gap wearing a gate's name — would have been wrong about which layer
	// it was in.
	//
	// So the pair of entries is now complete in both directions: #130's inequality of 2 matched its
	// gated share, #427's equality of 0 matches its gate-blindness, and each was pre-registered before
	// the run. All-on `fail` 61 → 53.
	// # 64993 → 65014, +21 against the default lane's +6, and this lane is where #426's reward is
	//
	// The harness learned `(module definition …)` and `(module instance …)`. Read through the
	// instrument the three entries above built: the lanes diverge by **15**, and the 15 is not a gated
	// share this time — it is the count of all-on **fails this slice drains**, `instance.wast` going
	// from `3/18 pass, 15 fail, 5 unsupported` to `20/20 pass, 3 bound`. Decomposed, because a +21
	// nobody can split is a number rather than a finding:
	//
	//   - **+4**, one per standalone definition — `memory`/`memory64`/`table`/`table64`.wast — each a
	//     valid module the harness could not previously ask about. Identical in both lanes, which is
	//     the equality check: these four forms carry no gated construct, and a divergence would have
	//     meant the definition arm reached a feature path.
	//   - **+17 in `instance.wast` alone**, and only 2 of those are the definitions themselves. The
	//     other 15 are downstream: three `register` forms now name instances that exist, so the three
	//     importing modules at :15, :62 and :128 link instead of failing on `unknown import`, and the
	//     12 `assert_return`s that run against them become real verdicts. **Bound +3** is the same
	//     fact in the registry's column.
	//
	// The default lane gains 6 of the 21 because the EH gate declines instantiating a module with a
	// `(tag …)` field, so `instance.wast`'s three instances and three registers are honestly gated
	// there and the 12 assertions stay gated behind them. That is the divergence accounted: 15 = the
	// 12 assertions + the 3 registrations, every one of them a vector that needs exception handling to
	// be answerable at all.
	//
	// All-on `fail` 53 → 38, and the drain's shape is worth one sentence: not one validator or
	// interpreter rule changed. Fifteen reds were the harness reporting its own missing classification
	// as engine failure, which is the wrong-layer attribution `unsupported`'s whole purpose is to
	// avoid — and they were *fails* rather than `unsupported` because the miss was invisible one
	// command downstream of where it happened.
	//
	// # 65014 → 65042, +28 against the default lane's 0, and the divergence is the whole of #270
	//
	// The harness learned `RefPat` and `RefPayload` — the pattern a `(ref.<ht>)` result expectation
	// writes and the constructor a non-null reference *is* (`unsupportedCeiling`'s account carries the
	// two-sidedness). Every one of the 28 sits in a GC-gated file, so the default lane's reward is
	// structurally 0 and lands in `gated` instead: 4159 → 4187, the reclassification that entry
	// forecasts. **This lane is where the 28 are answered**, and all 28 answer correctly —
	// `array.wast` 8, `array_new_elem.wast` 8, `extern.wast` 6, `array_new_data.wast` 4, `i31.wast` 1,
	// `struct.wast` 1.
	//
	// Read through the divergence instrument the entries above built, the reading is the *inverse* of
	// #426's: there the lanes diverged by the count of all-on fails a slice drained, and here they
	// diverge by the full reward because **not one of the 28 was a fail in either lane** — a command
	// the harness cannot classify scores `unsupported`, which is a third verdict and not a red. So
	// `allOnFailCeiling` beside this does not move, and that equality is the check: had it fallen, some
	// of the 28 would have been vectors the engine was getting wrong while the harness declined to
	// look, and the diagnosis "a spelling gap, not a capability gap" would have been wrong about which
	// layer #270 was in.
	//
	// The forecast (ADR 0039) claimed only `extern.wast`'s 6 in this column and **declined to forecast
	// the other 22**, on the ground that being able to ask a question predicts nothing about its
	// answer. It was conservative by 22, which is stated in that direction rather than left to read as
	// a 28-for-28 hit.
	// # 65042 → 65049, +7, and the figure was pre-registered before the diff existed (ADR 0040)
	//
	// #441's fork: `matches` no longer compares static types on the reference path, because the
	// authority does not — `assert_ref_pat` dispatches a pattern against a *constructor* and
	// `RefResult (RefPat r)` compares two concrete references by identity. Seven rows were refused by
	// the removed comparison and all seven now pass, `try_table` 3 and `local_init` 4, which is the
	// whole delta: `allOnFailCeiling` below falls by exactly 7 on the same run.
	//
	// **The +7 was measured and posted before Scott ruled, and it was measured on *two* candidate
	// diffs that produce byte-identical boards** — this fork, and deleting the comparison outright.
	// That identity is why the pin beside it (`TestCrossKindNumericComparisonsAreRefused`) is
	// hand-built rather than drawn from the corpus: the board cannot distinguish a correct
	// replacement from a reckless one here, so the board is not what chose.
	//
	// The default lane's reward is structurally 0 — all seven rows sit in GC- or EH-gated files, so
	// they are `gated` there and this is the only lane in which they can be counted.
	// # 65049 → 65064, +15, and the lanes agreeing exactly is the reading
	//
	// #440's `assert_exhaustion` arm. The default lane moved +15 too (`passFloor`), and the two lanes
	// landing on the same figure is what says **no gate is anywhere near this slice**: all 15 vectors
	// live in `call`, `call_indirect`, `fac` and `skip-stack-guard-page.wast`, all MVP core, so
	// turning every gate on changes nothing about them. That equality is the check on the diagnosis
	// — the last three entries above are all cases where the two lanes *diverged*, each divergence
	// naming a gate that was declining the slice, and a divergence here would have meant the arm was
	// admitting something the default lane could not see.
	//
	// `allOnFailCeiling` below does not move either: 15 conversions, 15 passes, 0 new reds. The
	// forecast's 10 predicted fails were predicted for both lanes and appeared in neither.
	// # 65064 → 65067, +3, and the lanes agree exactly for the second slice running
	//
	// #459's annotation skip. `passFloor` also moved +3, and as in the entry above the equality is
	// the check on the diagnosis: `annotations.wast` is MVP core, nothing in the three modules needs
	// a proposal, so turning every gate on must leave the slice untouched — and a divergence would
	// have said an annotation was reaching a gated path. Not a forecast this PR gets credit for
	// hitting; it is a consequence of the fix being in the s-expression reader, which sits below
	// every gate in the stack and cannot see one.
	// # 65067 → 65078, +11, and the lanes agree exactly for the third slice running
	//
	// #323's `(get …)` rows. `passFloor` moved +11 too, and the equality is the same check the two
	// entries above make: all 11 vectors live in `exports.wast` and `linking.wast`, both MVP core,
	// so turning every gate on cannot touch them and a divergence would have said a `get` was
	// reaching a gated path. It is not a forecast this PR gets credit for hitting — a global read is
	// below every gate in the stack, the same structural reason #459's annotation skip gave.
	//
	// **`allOnFailCeiling` below does not move, and that is the load-bearing half here.** This slice
	// is the first of the four `unsupported` drains that adds *engine* surface (`interp.Instance.Global`),
	// so it is the one where a new red was genuinely possible: 11 vectors that had never been answered
	// began to be answered, and a wrong answer would have landed in this lane's `fail` rather than
	// anywhere the `unsupported` column could absorb it. 11 conversions, 11 passes, 0 new reds, in a
	// lane that carries 31 standing fails to hide one in.
	// # 65078 → 65092, +14, and the two lanes diverging by the whole reward is the reading
	//
	// The `check_valtype` slice: `check_rectype`'s context scoped per rec group, `check_subtype`'s
	// typeuse-and-comptype walk, `check_globaltype` at both of its sites, `check_tabletype`'s element
	// type, `check_local`, and `check_elem`'s declared reftype. Fourteen rows, forecast as fourteen
	// before the rule was written — `array.wast:27,48,52`, `ref.wast:27,31,46,51,55,59`,
	// `struct.wast:36,40`, `type-rec.wast:21,28`, `type-equivalence.wast:76` — of which thirteen were
	// admitted and one (`ref.wast:55`) was a wrong-message row reporting a type mismatch downstream of
	// the reference it should have refused.
	//
	// **`passFloor` does not move at all, and unlike the last three entries that divergence is the
	// forecast rather than a surprise.** Every one of the fourteen needs the GC gate to *decode* — an
	// indexed reference type, a struct field, an array element, or a `sub` wrapper — so all fourteen are
	// `gated` in the default lane and this is the only lane in which the slice is observable. The three
	// entries above are the mirror case, MVP-core slices whose lanes had to agree; the instrument reads
	// the same way in both directions, which is what the divergence entries were built to do.
	//
	// `allOnFailCeiling` below falls by exactly 14 on the same run, 31 → 17. The two figures moving in
	// lockstep is the check on the diagnosis: a slice that also *broke* something would show a smaller
	// fall than the rise, and one that moved rows between fail buckets rather than out of them would
	// show a fall with no rise.
	const allOnPassFloor = 65092
	// **Slack 0 as of Scott's #387 ruling**, which this bound's own 89-row staleness above is what
	// prompted: a floor with 250 of tolerance cannot detect anything smaller than 250, so it is a
	// bound sitting inside its own tolerance. Exact from here — re-base it in the PR that moves the
	// lane, which is 0013's rule with the escape hatch removed. `boardbound_test.go`'s retirement
	// section carries the ruling, the argument it retired, and the #42 consequence it accepts.
	boardBound(t, "allOnPassFloor", totalPass, allOnPassFloor, 0, floorBound,
		"a gated feature regressed, which the Gated==0 assertion above cannot see: with every "+
			"gate on, a broken feature turns a pass into a fail and leaves Gated at zero")

	// **This lane's fail column, bounded for the first time, and the reason is that the work plan
	// just moved into it** (decision 0038). Every "All-on `fail` 61 → 53 → 38" above is *prose* — the
	// figure the ladder is steered by has been tracked in a comment chain and asserted by nothing,
	// which is exactly the unasserted distance the pass floor beside it exists to refuse.
	//
	// It was defensible while `execFailCeiling` held the default lane's residue: a rise there caught a
	// regression first, and this number was the larger, slower one. `execFailCeiling` is now **0**, so
	// the default lane's column is a tripwire with no population and this is the only fail column with
	// a subject. A work plan nothing bounds is how `allOnPassFloor` sat at 798 against 4178 for fifteen
	// commits (#87), one column over.
	//
	// **Charged as overhead to #414**, and the charge is the point rather than an aside: this is not a
	// new instrument invented alongside the fix, it is the fix relocating a column and its bound having
	// to follow. Predicate over data this lane already computes — `totalFail` is summed six lines up
	// for the log line.
	//
	// 38 across 9 files: `local_init` 8, `ref` 6, `array` 5, `type-subtyping` 5, `type-equivalence` 4,
	// `type-rec` 4, `try_table` 3, `struct` 2, `func` 1. GC in seven of the nine, EH in `try_table`,
	// one row in `func`. Slack 0 in both directions for the #387 reason the floor above carries: a
	// ceiling with tolerance cannot see anything smaller than its tolerance, and a *fall* is the drain
	// this lane exists to record, so it re-bases in the PR that earns it.
	//
	// **Unmoved by #270 at 38, and the zero is derived rather than merely observed.** The 28 vectors
	// that PR made askable were `unsupported` in both lanes — a command the harness has no case for is
	// a third verdict, never a red — so they cannot have been members of this column, and the 9-file
	// split above is unchanged in every row. The floor beside this moved +28 on the same run, which is
	// what makes the pair readable: reward in one column, no motion in the other, and a fall here would
	// have meant the 28 included vectors the engine was answering wrongly while unmeasured.
	// **38 → 31 with #441's fork (ADR 0040), and the fall is a re-base rather than a drain of the
	// kind this ceiling was built to record.** The seven rows the fork admits were not vectors the
	// engine was getting wrong; they were vectors the *harness* was refusing on a comparison the
	// reference does not make, so what left this column is harness error and not engine error. That
	// distinction is worth the sentence because a ceiling falling normally means capability landed,
	// and reading these 7 that way would credit the interpreter with work it did not do.
	//
	// 31 across 8 files: `ref` 6, `array` 5, `type-subtyping` 5, `local_init` 4, `type-equivalence` 4,
	// `type-rec` 4, `struct` 2, `func` 1. `try_table` leaves the list entirely (3 → 0) and
	// `local_init` halves (8 → 4); every other row is unchanged, which is the check that the fork
	// touched only what it was measured to touch.
	const allOnFailCeiling = 17
	boardBound(t, "allOnFailCeiling", totalFail, allOnFailCeiling, 0, ceilingBound,
		"the all-gates-on lane is the interpreter's and validator's remaining work plan now that "+
			"the default lane's exec column is empty: a rise here is a regression no gated-lane "+
			"bound can see, and a fall is the GC or EH work landing and re-bases this in its PR")
}

// moduleOverRejections are the modules this validator refuses that the corpus asserts are valid —
// the accept direction's own defect, and the finding #341 was taken for.
//
// **It is empty, and an empty table here is the whole point of having had one.** Thirteen rows were
// pre-registered on #341; four went with the `call_indirect` address-type repair and the last nine
// with #343's `match.ml` port. The documentation stays because the *subject* has not gone anywhere —
// the accept direction can acquire a new defect tomorrow, and the vacuity arm in
// TestModuleDefinitionsAskTheValidator is what keeps the drained state from reading as a green
// nobody measured.
//
// **All thirteen were all-gates-on-lane rows, and that was a property of the defects rather than of
// the instrument.** Every one needed a feature the default lane gates off, so
// `validateOverRejectCeiling` read 0 on the default board honestly; this map was where the population
// actually lived, which is why it was an exact table and not a count. *Floors bound the catastrophic
// case; only an exact count sees a small silent loss* — and here even a count would have been too
// weak, because a rule landing while a different one breaks moves nothing. The same asymmetry is why
// draining it moves the default board by zero: #343's PR states that zero as structural rather than
// as an absence of work.
//
// The four causes, which were the work plan rather than a taxonomy, and how each closed:
//
//  1. **recursive type equivalence and subtyping at a `call`'s arguments** (7) — the validator
//     compared type indices where the reference compares the types they name, so two structurally
//     identical rec-group members read as different. Closed by #343's `match.go`, whose
//     structural-equality disjunct needed the rec-group extent `binary` was not retaining.
//  2. **`(ref null none) <: (ref null any)` at a block result** (1) — bottom-of-the-heap-hierarchy
//     subtyping. Closed by the same port's four recursive bottom arms.
//  3. **element-type subtyping at `table.copy`** (1) — the check demanded equality where the spec
//     allows the source's element type to be a subtype of the destination's. Closed by routing that
//     comparison through the relation.
//  4. **`call_indirect`'s index operand** (4) — read as a hardcoded `i32` where the reference takes
//     it from the table's own address type (`valid.ml:537,542`), refusing four valid table64
//     modules: `call_indirect64.wast:3` and `table_init64.wast:385,444,503`.
//
// The rows were deleted as each cause closed, on the arm's own instruction. Recording the deletions
// rather than only performing them, because a table that silently shrinks cannot be told from a
// table someone trimmed to make a test pass — and that argument applies hardest to the deletion that
// empties it.
//
// Keyed `file:line` → the substring of the refusal that names the cause, so a row moving to a
// *different* wrong answer is a change this table reports rather than absorbs. Pre-registered on
// #341 before the arm was touched; the four causes were tracked as #343, which is the subject this
// table would otherwise have been a declaration without.
var moduleOverRejections = map[string]string{}

// TestModuleDefinitionsAskTheValidator is the oracle for the accept direction, and the missing half
// of every reward figure quoted before it existed.
//
// A module definition in a script asserts the module *is valid*, and until #341 the harness scored it
// on the reader's answer alone — so `internal/validate/global_test.go`'s M11 row could refuse **every**
// module from inside the validator and leave all 2143 `KindModuleText` commands green — the count as it
// stood at #341, era-marked because the census below now reads 2146 and this sentence is about that
// board rather than this one. Sixty thousand
// passes were carrying a claim they could not bear: an over-rejection produces no error for anyone to
// bucket, so the one defect class this direction can have was invisible by construction.
//
// Three things are asserted here and they fail in different ways on purpose:
//
//  1. **The arm asks at all**, falsified directly rather than argued: a validator that refuses every
//     module must turn a module definition red. This is the M11 mutation reproduced at the harness
//     boundary, and it is what makes the two subtests below more than a description of current output.
//  2. **The population is exactly `moduleOverRejections`**, both directions — a row that starts
//     passing is as much a finding as a new one appearing, and it is the direction that says a
//     validator slice landed.
//  3. **Each refusal still names its own cause**, so a row that moves to a *different* wrong answer
//     is reported instead of absorbed.
func TestModuleDefinitionsAskTheValidator(t *testing.T) {
	// The M11 mutation, once per form a module definition can take: `modulePre` refusing
	// unconditionally, run against a module the *reader* accepts, so the only thing that can score
	// these commands is fact 2.
	//
	// **Three rows because there are three ways to lose the call, not because three cases came to
	// mind — and the first draft of this comment got the reason wrong.** It said "two arms, so a
	// witness for one says nothing about the other", which is true of the binary row and over-claims
	// for quote: `KindModuleText` and `KindModuleQuote` reach fact 2 through the *same* arm.
	// Recorded rather than quietly reworded, because the corrected reason is the one that makes each
	// row falsifiable, and each was watched die separately:
	//
	//   - **binary** — `KindModuleBinary` has its own arm and its own `scoreModuleValidation` call.
	//     Deleting that call leaves the other two rows green.
	//   - **quote** — shares text's arm, where the call sits under a *per-Kind guard*. Narrowing
	//     that guard to `c.Kind == KindModuleText` is a one-token edit that text survives and quote
	//     does not. #341 landed fact 2 for both forms with the text row alone as witness, so this
	//     row is that omission repaired.
	//   - **text** — the original M11 reproduction, unchanged.
	//
	// **The binary row is #353's only observable.** That slice's pre-registered board delta was a
	// measured zero — 88 `KindModuleBinary` commands, all 88 validating clean in both lanes — so no
	// board figure moves when its arm starts asking fact 2, and without this row the arm's green is
	// indistinguishable from the arm not having landed. A clean population does not make the
	// question idle: an over-rejection produces no error for any reject-direction bucket to catch,
	// which is the hole this whole test exists for.
	//
	// The table was *enumerated* and the domain is derivable, which made this an unchecked claim
	// about its own coverage — declared and tracked as #354, and closed by the
	// `the witness rows are every Kind a module head classifies to` subtest below. The table stays a
	// named variable rather than an inline literal so that subtest derives its expectation from the
	// rows themselves; a re-typed copy of the same three Kinds would agree with the table by
	// construction and check nothing.
	rows := []struct {
		name   string
		src    string
		want   Kind
		bucket string
	}{
		{"text", `(module (func))`, KindModuleText, "module text must validate"},
		{"quote", `(module quote "(module (func))")`, KindModuleQuote, "module quote must validate"},
		// `module binary` since #364, and this row is where the previous slice's *deferred finding*
		// came due. #353 pinned this key as `module must validate` and said so as measured rather
		// than as intended: `Kind.String()` returned the bare word for this Kind, the shared closure
		// forms `c.Kind.String() + " must validate"`, and the resulting key read ambiguously beside
		// `(module binary …) must decode` — recorded there as "a board-legibility finding about
		// `Kind.String`, not about this slice". Scott's ruling on `Kind.String()` (PR #364) is what
		// closed it, and the honest scoring of the older pre-registration is that it named the right
		// string one era early: it predicted the mechanism this row now has, and was a miss against
		// the mechanism that existed.
		//
		// **The four expectations stay literal, which is not an oversight.** Deriving them as
		// `tc.want.String() + " must validate"` would make this row an identity against the very
		// closure it is checking — the shape a pin wearing a cross-check's clothes takes (grave
		// #362) — so it would agree with any rename, including a wrong one. A hand-typed key is the
		// only kind that can disagree, and the two disagreements #364 produced (this row and
		// `admittedKeyPrefix`) are the instrument working: a rename should have to walk past every
		// reader that claimed to know the string.
		{"binary", `(module binary "\00asm\01\00\00\00")`, KindModuleBinary, "module binary must validate"},
		// **The witness #426's widening owed, and the subtest below is what came to collect it.** Its
		// comment used to read "when #320 lands they acquire real Kinds and this control goes red,
		// which is the intended reading — a widening owes a witness"; the widening landed, the
		// control went red, and this row is the payment. Recorded that way rather than as a fourth
		// case that always existed, because a pre-registered red *arriving* is the only evidence that
		// the sentence predicting it was load-bearing.
		//
		// It is a row and not a note because `KindModuleDefinition` has its own arm and its own
		// `scoreModuleValidation` call. Deleting that call is the mutation, and it leaves the other
		// three rows green — the binary row's argument exactly, one Kind later.
		//
		// The definition form is *the* form whose whole content is fact 2: it reads and validates and
		// deliberately stops, so a lost fact-2 call here does not merely weaken the score, it leaves
		// the command asserting nothing at all while still counting a pass. `(module definition (func))`
		// rather than a corpus line, so the row survives a corpus that stops containing one.
		{"definition", `(module definition (func))`, KindModuleDefinition, "module definition must validate"},
	}
	for _, tc := range rows {
		t.Run("a validator that refuses every module turns a "+tc.name+" definition red", func(t *testing.T) {
			s, err := Parse("t.wast", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := s.Commands[0].Kind; got != tc.want {
				t.Fatalf("classified %v, want %v — this control needs the arm it is about", got, tc.want)
			}
			e := engine()
			e.Validate = func(Command) (Stratum, error) {
				return StratumValidate, errString("refuses every module")
			}
			r := s.RunGated(e)
			if r.Pass != 0 || r.Fail != 1 {
				t.Fatalf("got %d pass, %d fail; want 0/1 — a module definition scored green under a "+
					"validator that refuses everything is exactly the hole #341 closed, and this "+
					"assertion is the only thing standing between that hole and every reward figure "+
					"the board quotes", r.Pass, r.Fail)
			}
			b := r.Buckets[tc.bucket]
			if len(b) != 1 {
				t.Fatalf("no failure under %q; got keys %v", tc.bucket, r.BucketsBySize())
			}
			if !b[0].OverRejected || b[0].Stratum != StratumValidate {
				t.Errorf("OverRejected = %v, Stratum = %v; want true/%v — without the flag this row "+
					"lands in the wrong-message arm of the validate stratum's split, a population "+
					"whose ceiling is 0 (see Failure.OverRejected)",
					b[0].OverRejected, b[0].Stratum, StratumValidate)
			}
		})
	}

	// Named for the rows rather than for how many there are, because the count is the thing this
	// subtest exists to move: it went red on #426 to demand a fourth row, and a name carrying `three`
	// would have made the payment a rename of every citation to it. *A test name is a checkable
	// citation* — so the name should cite what the rows are, not how many were there when it was
	// written.
	t.Run("the witness rows are every Kind a module head classifies to", func(t *testing.T) {
		// #354, and the reason it is a separate control rather than a fourth row: it fails when the
		// *domain* moves, where the rows above fail when a *call* is lost. Two failure causes, two
		// controls (grave #34) — and the one thing it must not do is enumerate, since enumerating the
		// same three Kinds a second time is the blind spot it was filed against wearing a check's
		// clothes.
		//
		// The domain is corpus-measured: `classify` records `Head` verbatim, so every module
		// definition in the suite — whatever form it takes — answers `Head == "module"`, and the set
		// of Kinds those commands carry is the set the witness table has to cover. Measured over
		// `suitePaths` and **not** `boardFiles`: `boardFiles` selects on `scorableCommands > 0`, so a
		// module form arriving in a file the board does not yet select would be invisible to a domain
		// derived from it. That is one instrument's blind spot inherited by the instrument built to
		// remove another's, and the wider walk costs nothing this test was not already paying.
		//
		// **One Kind is excused, by name and with the reason, rather than by filtering** —
		// `KindModuleInstance`, the one Kind here that asks fact 2 **never**, deliberately. An
		// instance form asserts that a definition *instantiates*; the definition already answered the
		// validator on its own line, and re-asking would charge one module's fact 2 to four commands
		// in `instance.wast` alone. So there is no call for a mutation to lose, which is this list's
		// only admission criterion.
		//
		// # `KindUnsupported` was the excused Kind and is not a member of this walk any more
		//
		// It was excused for the same criterion — a module head reaching no validator has no fact-2
		// call to falsify — and its 9 members were exactly `(module definition …)` and
		// `(module instance …)`. #426 gave both arms, so the population is **0** and the entry had to
		// go: this control's own other direction reported it, saying the table "is asserting something
		// about a population that is not there, which passes and says nothing." Left in place it would
		// have been a live-looking excuse for nothing, which is the shape *an unasserted distance*
		// names. It comes back the moment a `module` head classifies to it again — and then this
		// control goes red demanding either a row or a reason, which is what it is for.
		//
		// The paragraph is a collected prediction twice over. It used to end "when #320 lands they
		// acquire real Kinds and this control goes red, which is the intended reading — a widening
		// owes a witness." That is what happened: #426 gave both forms arms, this subtest went red
		// naming `module definition`, and the debt was paid with a row rather than with an entry here.
		// The distinction is the whole content of the excuse list — an excused Kind is one no mutation
		// can falsify, not one nobody got round to.
		//
		// **What the walk still cannot see, measured rather than assumed.** `suitePaths` globs
		// `testdata/spec/*.wast`, one level, so the 11 script-module sites under
		// `proposals/custom-page-sizes/` and `proposals/custom-descriptors/` are outside it — including
		// the only `(module definition binary "…")` in the corpus, the sub-form the classifier guards
		// and keeps `unsupported` on purpose. That guard's witness is therefore synthetic and lives in
		// `TestScriptModuleFormsAreNotWatBodies`, not here. Said in the control whose name would
		// otherwise imply it had swept every one.
		//
		// **What this cannot see, stated because the issue's premise was slightly wider than the
		// mechanism.** #354 reasoned that "a new form arrives as a new Kind in `classify`"; that holds
		// for a form someone gives an arm, and not for a bare new keyword. `moduleFormKeyword` is
		// deliberately not an allowlist (`wast.go:1136`), so `(module newthing …)` falls through to
		// the wat reader as `KindModuleText`. The Kind census cannot report it — and does not need to:
		// a form landing on `KindModuleText` lands on the arm the text row above already falsifies.
		// The hole this control closes is the other one, a Kind gaining an arm without gaining a
		// witness, which is exactly how the quote row came to be missing until #353.
		want := map[Kind]bool{KindModuleInstance: true}
		for _, tc := range rows {
			want[tc.want] = true
		}
		// Counts pinned exactly beside the set, because set equality alone is satisfied by a
		// population that drained to one command: a floor bounds the catastrophic case, and the
		// interesting loss here is a classifier arm that stops firing for most of its inputs while
		// still firing for one. Re-base in the PR that moves the corpus or an arm.
		wantCounts := map[Kind]int{
			// 2143 → 2146 on #459: `annotations.wast:98,129,154` are `((@a) module …)`, so their
			// `Head` was `""` and this walk's `c.Head != "module"` filter skipped them entirely.
			// Dropping annotation nodes in the s-expression reader gives all three a `module` head,
			// which puts them in this census for the first time — the count moving is the *only*
			// signal here that the change reached the classifier, since the set of Kinds is unchanged.
			KindModuleText:   2146,
			KindModuleBinary: 88,
			KindModuleQuote:  7,
			// #426 moved these two out of `KindUnsupported`'s 9 and the sum is the check: 6 + 3 = 9,
			// so the widening reclassified its population and did not acquire one. The 6 are
			// `memory`/`memory64`/`table`/`table64`.wast at one each and `instance.wast` at two; the 3
			// are `instance.wast`'s instance forms.
			KindModuleDefinition: 6,
			KindModuleInstance:   3,
		}
		got := map[Kind]int{}
		where := map[Kind]string{}
		for _, p := range suitePaths(t) {
			s, err := ParseFile(p)
			if err != nil {
				t.Errorf("%s: parse: %v", p, err)
				continue
			}
			for _, c := range s.Commands {
				if c.Head != "module" {
					continue
				}
				got[c.Kind]++
				if _, seen := where[c.Kind]; !seen {
					where[c.Kind] = fmt.Sprintf("%s:%d", filepath.Base(p), c.Line)
				}
			}
		}
		for k, n := range got {
			if !want[k] {
				t.Errorf("a `module` head classifies to %v (%d commands, first %s) and no row above "+
					"witnesses it — add a row to the table, or, if the Kind reaches no validator, "+
					"excuse it here by name with the reason. This is the #353 omission's shape: an arm "+
					"that scores fact 2 with nothing falsifying that it does.", k, n, where[k])
			}
		}
		for k := range want {
			if got[k] == 0 {
				t.Errorf("the table witnesses %v but no `module` head in the corpus classifies to it — "+
					"the row is asserting something about a population that is not there, which passes "+
					"and says nothing", k)
			}
		}
		if len(got) != len(wantCounts) {
			t.Errorf("module-head Kinds: %d distinct, want %d — %v", len(got), len(wantCounts), got)
		}
		for k, n := range wantCounts {
			if got[k] != n {
				at := where[k]
				if at == "" {
					at = "no command carries it"
				}
				t.Errorf("module heads classifying to %v: %d, want exactly %d (%s). The set above "+
					"is satisfied by one surviving command; this is the figure that is not",
					k, got[k], n, at)
			}
		}
	})

	t.Run("an undecodable binary image is charged to fact 1, not to the validator", func(t *testing.T) {
		// **The tripwire for an ordering constraint that is invisible in the code it constrains.**
		// `ValidateFunc` implementations assemble before they decode and charge a decode refusal to
		// `StratumEncode` — a claim that the image came out of the encoder, true for the text and
		// quote forms and false here, there being no encoder on this path. The binary arm asks
		// fact 1 first and `continue`s on any decode failure, so the mis-charge is unreachable *by
		// statement order*, which is one edit away from a silently mis-charged column. Swap the two
		// facts and this image lands under `module must reach the validator` at `StratumEncode`
		// instead of where it belongs.
		//
		// The real `engine()` validator, deliberately: the mis-charge is `validateWith`'s own
		// behaviour, so a stub that refused cleanly would make this arm agree with itself.
		s, err := Parse("t.wast", []byte(`(module binary "\00asm")`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := s.Commands[0].Kind; got != KindModuleBinary {
			t.Fatalf("classified %v, want KindModuleBinary — this control needs the arm it is about", got)
		}
		r := s.RunGated(engine())
		if r.Pass != 0 || r.Fail != 1 {
			t.Fatalf("got %d pass, %d fail; want 0/1", r.Pass, r.Fail)
		}
		const key = "(module binary ...) must decode"
		b := r.Buckets[key]
		if len(b) != 1 {
			t.Fatalf("no failure under %q; got keys %v — a truncated image scored anywhere else "+
				"means fact 2 ran first and answered for the decoder", key, r.BucketsBySize())
		}
		if b[0].Stratum != StratumBinary || b[0].OverRejected {
			t.Errorf("Stratum = %v, OverRejected = %v; want %v/false — `StratumEncode` here would "+
				"assert this image came out of an encoder there is none of, and the ordering that "+
				"prevents it has no other assertion",
				b[0].Stratum, b[0].OverRejected, StratumBinary)
		}
	})

	t.Run("the all-on lane's over-rejections are exactly the pinned table", func(t *testing.T) {
		requireSuite(t)
		// **The table is drained, and this is the arm the guard that used to stand here specified.**
		// Its words were: "moduleOverRejections is empty, so the reconciliation below compares two
		// empty sets and agrees with any board at all. If a slice drained the last row, keep the
		// table's documentation and assert 0 explicitly rather than deleting the subject." #343's
		// GC-subtyping slice drained all nine, so the documentation is kept above and the zero is
		// asserted here — by the second reconciliation loop, which errors on any over-rejection not
		// in the table and against an empty table is exactly "there are none."
		//
		// What that loop cannot do is notice that it walked nothing, which is the vacuity the old
		// guard was really about: nine pinned rows *were* the proof the walk finds over-rejections
		// at all, and an empty expectation cannot prove its own instrument. So the walk's own extent
		// is floored below — *a comparison against an empty set succeeds*, and the set that has to be
		// non-empty now is the set of rows the walk looked at.
		_, _, allOnEngine := allOnLane(t)
		got := map[string]string{}
		files, asked, fails, scored := 0, 0, 0, 0
		for _, f := range boardFiles(t) {
			s, err := ParseFile(filepath.Join(suiteDir, f))
			if err != nil {
				t.Errorf("%s: parse: %v", f, err)
				continue
			}
			files++
			r := s.RunGated(allOnEngine())
			asked += r.Pass + r.Fail
			fails += r.Fail
			for _, fs := range r.Buckets {
				for _, fail := range fs {
					scored++
					if fail.OverRejected {
						got[fmt.Sprintf("%s:%d", f, fail.Line)] = fail.Got
					}
				}
			}
		}
		// A plausibility floor on the walk, not a bound on the board: pinned far below the all-on
		// lane's own figures, it says only that the suite was found, parsed, and asked. Its subject
		// is deliberately the *answered* count and not the fail count. **This floor stood at
		// `scored < 100` and #419 tripped it** — the all-on fail column went 108 → 70, so a bound
		// written as "well below the figures" had been aimed at a number this project is paid to
		// drive to zero, and the first slice large enough to move it reported the reward as a broken
		// instrument. A floor belongs on a quantity that *rises* as the engine improves; `Pass+Fail`
		// is that quantity and the fail column is its opposite. `allOnPassFloor` is where the
		// board's actual counts are tracked, and duplicating them here would make two places move
		// for one event.
		if files < 200 || asked < 10000 {
			t.Fatalf("the walk covered %d file(s) and asked %d command(s) — with the table drained "+
				"this loop's only remaining assertion is that `got` is empty, and an empty `got` "+
				"from a walk that ran over nothing is the reassuring answer for the wrong reason",
				files, asked)
		}
		// The second half of the same worry, and the half a floor cannot state: `got` is read out of
		// `Buckets`, so a `Buckets` map that under-reports would empty `got` without emptying the
		// board. Pinned as an identity against `Result.Fail`, a count kept by a different path in
		// the same run. At a zero-fail board this reads 0 == 0 and says nothing — which is why the
		// mechanism's own witnesses are the four sibling arms above ("a validator that refuses every
		// module turns a … definition red"), where a stub validator makes `OverRejected` true and
		// this arm's non-vacuity therefore does not rest on the fail column at all.
		if scored != fails {
			t.Errorf("the walk pulled %d row(s) out of `Buckets` against %d counted in `Result.Fail` "+
				"— `got` is filled from the former, so a shortfall here is an over-rejection this "+
				"arm cannot see rather than one that is not there", scored, fails)
		}
		// Reconciled in both directions, never floored: a row that starts passing is the reward a
		// validator slice earns, and a floor would report it as fine.
		for row, want := range moduleOverRejections {
			switch g, ok := got[row]; {
			case !ok:
				t.Errorf("%s no longer over-rejects — if a slice fixed it, drop the row here and "+
					"say so in the changelog; if it stopped being asked, that is a regression in "+
					"what the harness can see", row)
			case !strings.Contains(g, want):
				t.Errorf("%s still over-rejects but with a different cause:\n\tgot  %s\n\twant a "+
					"message containing %q\n\ta row moving to a new wrong answer is a change this "+
					"table exists to report rather than absorb", row, g, want)
			}
		}
		for row, g := range got {
			if _, ok := moduleOverRejections[row]; !ok {
				t.Errorf("%s over-rejects and is not in the table: %s\n\tthe validator refused a "+
					"module the corpus says is valid, which is the accept direction's own defect — "+
					"file it and add the row, never widen a count", row, g)
			}
		}
		t.Logf("all-on lane: %d module-definition over-rejections, %d pinned",
			len(got), len(moduleOverRejections))
	})
}

// TestModuleInstanceFailurePathsAreWatched fires the two `KindModuleInstance` fail arms the corpus
// cannot reach, and it exists because both of their comments say so in as many words.
//
// A fail path with no witness is a path nobody has watched fire, and its bucket key, stratum and
// message are unread prose until something reads them back. The arms' own comments admit the gap —
// "the non-gate half of this branch is reasoned, not measured" — and an admitted gap that a synthetic
// script can close is a gap that should be closed rather than declared: *a control isn't born until
// it's watched die.*
//
// **Both scripts are hand-written and neither is in the suite**, which is the point and not a
// shortcut. `instance.wast` has no instance form naming a missing definition and no definition that
// fails to read, so the board's 0 in these two buckets is a true zero about the corpus and says
// nothing about the code. Synthetic input is the only oracle available for a branch upstream declines
// to write a vector for.
//
// Watched dying on three mutations, one per subtest, each tripping a *different* assertion — which is
// the result that says they are three controls and not one written three ways:
//
//   - dropping the `r.Fail++` in the no-definition arm: `got 0 pass, 0 fail, 0 gated`, the silent
//     no-op this arm exists to refuse.
//   - keying the definition's read failure as `(module <wat body>) must read`: the bucket check
//     names both keys it found, which is the 6-into-1119 merge caught at the only place that can see
//     it.
//   - disabling the `d.gated` branch so an inherited decline falls through to the fail arm:
//     `1 fail, 1 gated`, the third verdict leaking into the fail column one command downstream.
func TestModuleInstanceFailurePathsAreWatched(t *testing.T) {
	t.Run("an instance naming no preceding definition is a fail that names the missing definition", func(t *testing.T) {
		// The reason this is a fail and not a no-op: without it the name is unbound, the register
		// after it binds nothing, and every import against it reports `unknown import` — the exact
		// wrong-layer attribution that put three rows in `execFailCeiling` until #426, where the
		// resolver's complaint was true about itself and silent about the cause.
		s, err := Parse("t.wast", []byte(`(module instance $I $M)`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := s.Commands[0].Kind; got != KindModuleInstance {
			t.Fatalf("classified %v, want KindModuleInstance — this control needs the arm it is about", got)
		}
		r := s.RunGated(engine())
		if r.Pass != 0 || r.Fail != 1 || r.Gated != 0 {
			t.Fatalf("got %d pass, %d fail, %d gated; want 0/1/0", r.Pass, r.Fail, r.Gated)
		}
		const key = "(module instance) must name a preceding definition"
		b := r.Buckets[key]
		if len(b) != 1 {
			t.Fatalf("no failure under %q; got keys %v", key, r.BucketsBySize())
		}
		// **The message must name `$M`, not just complain.** A bucket row whose `Got` omits which
		// name was missing sends the reader back to the file to work out what the harness already
		// knew, which is the whole difference between a verdict and a work plan.
		if !strings.Contains(b[0].Got, "$M") {
			t.Errorf("Got = %q, want it to name the missing definition `$M`", b[0].Got)
		}
		if b[0].Stratum != StratumExec {
			t.Errorf("Stratum = %v, want %v — the missing thing is an instance", b[0].Stratum, StratumExec)
		}
	})

	t.Run("a definition that fails to read fails in its own bucket and its instance inherits", func(t *testing.T) {
		// Two commands, two reds, two *different* keys — which is the assertion. Merging them would
		// make one broken definition indistinguishable from a broken definition plus a broken
		// instance arm, and the whole reason the definition form has its own read bucket is that its
		// 6 sites must not disappear into the bare-body form's 1119.
		s, err := Parse("t.wast", []byte("(module definition $M (memory 1))\n(module instance $I $M)"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := engine()
		e.ReadText = func([]byte) error { return errString("refuses every wat body") }
		r := s.RunGated(e)
		if r.Pass != 0 || r.Fail != 2 || r.Gated != 0 {
			t.Fatalf("got %d pass, %d fail, %d gated; want 0/2/0 — one red per command, since a "+
				"definition that did not read and an instance of it are two separate claims",
				r.Pass, r.Fail, r.Gated)
		}
		for _, key := range []string{
			"(module definition <wat body>) must read",
			"(module instance) definition did not read",
		} {
			if len(r.Buckets[key]) != 1 {
				t.Errorf("no failure under %q; got keys %v", key, r.BucketsBySize())
			}
		}
		// The inherited row must carry the *definition's* cause, not a restatement of its own
		// existence: "the definition it names produced nothing" is the fallback and reaching it here
		// would mean the cause was dropped between the two arms.
		if b := r.Buckets["(module instance) definition did not read"]; len(b) == 1 {
			if !strings.Contains(b[0].Got, "refuses every wat body") {
				t.Errorf("the inherited row's Got = %q, want the definition's own cause", b[0].Got)
			}
		}
	})

	t.Run("a gate-declined definition gates its instance rather than failing it", func(t *testing.T) {
		// This branch *does* have a corpus witness — `instance.wast`'s six gated lines, since both
		// its definitions carry `(tag …)` — and it is pinned synthetically anyway, because the
		// corpus witness depends on exception handling still being gated off. The day EH flips
		// default-on, those six lines convert and this branch loses its only witness silently. A
		// synthetic gate decline is a witness no flip can take away.
		//
		// Decision 0037's shape: a decline is not a defect, so a command downstream of one is a
		// question the engine was never asked. Scoring it `fail` would mark correct behaviour red.
		s, err := Parse("t.wast", []byte("(module definition $M (memory 1))\n(module instance $I $M)"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := engine()
		e.ReadText = func([]byte) error {
			return fmt.Errorf("pretend proposal: %w", binary.ErrFeatureDisabled)
		}
		r := s.RunGated(e)
		if r.Pass != 0 || r.Fail != 0 || r.Gated != 2 {
			t.Fatalf("got %d pass, %d fail, %d gated; want 0/0/2 — the definition's decline is its "+
				"own, and the instance's is inherited; a fail on either is the third verdict leaking "+
				"into the fail column one command downstream", r.Pass, r.Fail, r.Gated)
		}
		if len(r.GatedAt) != 2 {
			t.Errorf("GatedAt = %v, want two lines — the counter and the list must agree, since "+
				"TestGatedVectors reads the list", r.GatedAt)
		}
	})
}

// TestGrave206KnownFailures pre-registers the all-gates-on fails #201's rung 2c found still red
// after landing `throw`/`throw_ref`/`try_table` execution — so their persistence is a *prediction
// wearing a citation*, not a surprise the next reader has to re-diagnose. `TestGatedVectors`'s own
// per-vector allowlist is the precedent this follows, pointed at Fail instead of Gated.
//
// **Grave #206 closed** (decision 0023, `drop` now consults a lazily-tracked push sequence
// number rather than always popping the numeric array) — 15 of the original 19 lines converted
// to pass, matching the all-gates-on lane's own +15 move exactly (37218→37233, measured with the
// harness, decision 0007/#161's standing rule). This test's own population shrank in step: the
// two `throw_ref.wast` lines are gone entirely (that file is now fully green) and 12 of the 14
// `try_table.wast` lines are gone.
//
// **2 of the original 17 #206-attributed lines were compound failures, not pure #206**, found
// only by fixing #206 and reading what surfaced underneath: `try_table.wast:465,466` now fail
// with "which the harness cannot represent" — the same pre-existing harness limitation `:464`
// already carried — because `drop`'s bug was masking a second, unrelated gap the whole time. Not
// a new finding about the engine; a correction to which citation these two lines actually need,
// discovered by the fix's own falsification rather than assumed to hold from the original
// diagnosis.
//
// **2 lines were the already-tracked rec-group scope boundary** `sameFuncType`'s own doc comment
// named (`TestSameFuncTypeCorpusScope`'s M10/M11 shape) — `tag.wast`'s cross-module tag-import
// vectors reached the identical gap `sameTagType` inherited from `structFuncTypeEqual`. Not #206,
// and they are **gone**: grave #368 routed the linker's tag arm through `match_tagtype` over rolled
// deftypes, which is the relation those two vectors were always about, so `:48` and `:59` pass and
// their entries went stale. This test said so before the report did — the second stale-entry catch
// after `:334`, and the same mechanism: a fail list rots by the system working.
//
// **Those three lines are gone too, and not because the harness limitation was fixed.**
// `try_table.wast:464`/`:465`/`:466` are the unnameable-reference placeholder arriving at
// `matches`, and ADR 0040 (#441) split that function on `want.Kind.isRef()`, so a reference
// expectation is now decided by its pattern instead of by the placeholder's static kind. The
// placeholder is still unnameable — what changed is that nothing refuses it. Stated in that
// direction because the rule below cuts both ways: a stale pre-registration overstates what is
// broken, and removing one can *understate* it just as easily. The residue is filed rather than
// left sitting in this list, because these three lines have stopped witnessing it: the got-side
// sentinel is #441's own ruling 3, and the nullability defect `valKind` refuses `(ref extern)`
// on is #450. **A pre-registration whose subject stops being observable is removed here and
// re-pointed there**, never closed as no-longer-applicable.
//
// It was **2**, the other being `:334` — `return_call` had no arm in this engine at all — and #253
// landed 0026's tail-call mechanism, so that line passes and the entry became stale. This test
// caught it, which is the pre-registration behaving exactly as designed: a fail list is supposed to
// rot by the system working, and a stale entry overstates what is broken. Recorded here rather than
// silently decremented, because the count in a doc comment is a citation like any other.
//
// **The `tag.wast` pair is gone and the file's key with it, so this list is one file wide** — slice 10
// (ADR 0036) wrote `check_tagtype` at both its call sites. That is the *fourth* stale entry this
// pre-registration has reported, after `:334`, `:48` and `:59`, and the tally is worth keeping because
// it is the argument for the shape: every one of the four was found by the list refusing to pass, and
// none by anybody re-reading it. **The fifth report emptied the table**: the `try_table.wast` trio went
// stale under ADR 0040, so the map is zero files wide and the population has rotted from 19 to 0 by the
// system working. Nothing in this file is pre-registered as failing any more.
//
// **An emptied table takes its own domain with it, so the domain is no longer read off its keys.**
// The loop below used to range over `known`, which made the map two things at once: which lines are
// excused, and which files are watched at all. Drained to empty, that loop runs zero times and the
// second arm — *any* fail here must carry a citation — silently stops asserting, which is a table
// dying the way a stillborn control dies, with a green. `governed` is the domain now, and the
// allowance is just an allowance.
func TestGrave206KnownFailures(t *testing.T) {
	requireSuite(t)

	// file → line → citation. Every entry needs one, on TestGatedVectors' own principle: an
	// unexplained pre-registration is a suppression wearing a disguise.
	known := map[string]map[int]string{
		// `tag.wast` has no entries left and the key is gone with them.
		//
		// `:18` and `:22` were the admission stratum arriving with #9's first validator slice: `(tag
		// (result i32))` and its imported twin, modules the type checker *accepted* against an expected
		// "non-empty tag result type", because the rule is a tag-section rule and the code-section walk
		// never visits a tag definition. **Slice 10 wrote it** (`checkTagType`, ADR 0036) at both of
		// `check_tagtype`'s call sites — the defined tags and `check_import`'s `ExternTagT` arm — which
		// is what those two vectors were always a pair *for*: a rule written at the defined site alone
		// passes `:18` and leaves `:22` red. Removed rather than re-explained, as `:48`/`:59` were when
		// grave #368's rolled `match_tagtype` closed the rec-group boundary they reached, and as
		// `try_table.wast:334` was when 0026's tail-call mechanism landed.
		// `try_table.wast` has no entries left and the key is gone with them.
		//
		// `:334` was here — `return_call` had no arm — and 0026's mechanism (#253) gave it one, so
		// the entry went stale and this test said so. `:464`, `:465` and `:466` were the harness
		// limitation, correctly attributed to all three lines after #206's fix unmasked the two
		// that had been misattributed; ADR 0040's family split in `matches` stopped the
		// unnameable-reference placeholder from being refused, so all three pass and all three
		// went stale together. Removed rather than re-explained, as `:334` was and as
		// `tag.wast`'s two pairs were, and the doc comment's arithmetic is corrected above.
	}

	// The files this pre-registration governs. **Kept apart from `known`, which is now empty** —
	// the domain of the check and the list of excuses were the same map until the excuses ran out,
	// and a domain that dissolves with its allowances leaves the second arm below ranging over
	// nothing. These are the three files #201's rung 2c walked and the only ones whose fails this
	// test has ever spoken for; a fourth arriving is a new pre-registration's business, not a
	// silent extension of this one's.
	governed := []string{"tag.wast", "throw_ref.wast", "try_table.wast"}

	_, _, allOnEngine := allOnLane(t)
	for _, f := range governed {
		lines := known[f]
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Fatalf("%s: parse: %v", f, err)
		}
		r := s.RunGated(allOnEngine())
		// **The file has to have run something**, or both arms agree with an empty population.
		// This bounds the catastrophic case only — a file renamed upstream, or parsed to no
		// commands — and deliberately not the partial one: an exact per-file count here would be a
		// second pin on a number `allOnPassFloor` already holds, and two pins on one fact drift
		// apart without either saying so.
		if r.Pass == 0 {
			t.Errorf("%s: 0 pass in the all-on lane — the arms below are agreeing with an empty "+
				"population, not with the engine", f)
		}
		failed := make(map[int]bool)
		for _, fs := range r.Buckets {
			for _, fail := range fs {
				failed[fail.Line] = true
			}
		}
		for line, why := range lines {
			if !failed[line] {
				t.Errorf("%s:%d is pre-registered as failing (%s) but is no longer failing; "+
					"remove the entry — a stale pre-registration overstates what is broken, "+
					"the deadcode-allowlist principle applied to a fail list", f, line, why)
			}
		}
		for _, fs := range r.Buckets {
			for _, fail := range fs {
				// **A decline is not a verdict, so it is not a finding about #206.**
				// `try_table.wast` carried 10 `assert_invalid` vectors on the `try_table` opcode
				// itself, which the validator did not type; they were fails in their own named
				// bucket, held by validateDeclineCeiling, and they said nothing about whether
				// `drop` lost a value. Filtered on the flag rather than pre-registered by line,
				// because ten citations reading "the validator has not got there yet" is the
				// repeated-reason testimony the bulk allowances exist to avoid — and a per-line
				// list would have gone stale one entry at a time as slices landed.
				//
				// **The filter's population is empty**: slice 10 types the family (ADR 0036), so
				// none of those ten declines any more. The `continue` stays because the *reason*
				// it exists outlives them — an all-on lane can still decline (relaxed SIMD does,
				// eight operators' worth) and a proposal retained by the decoder ahead of its
				// typing rules is how this population filled in the first place. A filter deleted
				// with its subject is a filter the next arrival re-earns.
				//
				// The *admission* half was never filtered: an accepted-but-invalid module is a
				// real gap and gets a citation above. The two `tag.wast` entries were that half,
				// and slice 10 closed them too.
				if fail.Declined {
					continue
				}
				if lines[fail.Line] == "" {
					t.Errorf("%s:%d fails and is not pre-registered: %q — either cite it here "+
						"(grave #206, the rec-group scope boundary, or a new finding) or it is "+
						"an unexplained regression", f, fail.Line, fail.Got)
				}
			}
		}
	}
}

// TestVerdictsPartitionCommands checks the arithmetic the board depends on: every
// command lands in exactly one of pass, fail, unsupported, gated, or unimplemented.
//
// Without this, adding a verdict is a chance to lose vectors — a command that
// falls through every branch simply vanishes, and a board that does not sum is
// how a suite silently stops covering something. Decision 0010 added the fifth
// term, which is precisely the event this test was written in advance of: the
// arithmetic was asserted before there was a fourth verdict to lose vectors to.
func TestVerdictsPartitionCommands(t *testing.T) {
	requireSuite(t)
	files := boardFiles(t)
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		// **Six terms, and the sixth was added because this control found it missing.**
		// `Bound` counts a `register` that bound a name — a command whose success is a state
		// change and not a verdict, so it scores nothing and must still be *accounted*. The
		// distinction is the whole point of this test: 45 registers across 24 files summed
		// short here while every other board number looked correct, which is precisely the
		// "adding a verdict is a chance to lose vectors" event the doc comment above was
		// written in advance of. It caught the sixth outcome the same way it was written to
		// catch the fifth.
		if got, want := r.Pass+r.Fail+r.Unsupported+r.Gated+r.Unimplemented+r.Bound, len(s.Commands); got != want {
			t.Errorf("%s: verdicts sum to %d but the script has %d commands; %d vectors are unaccounted for",
				f, got, want, want-got)
		}
	}
}

// admittedKeyPrefix is the admission stratum's bucket-key prefix — `classify`'s own key for "ran to
// completion and said yes". A string the *corpus* supplies, not a name this test invents, which is
// what lets the census below be keyed by the vectors' own expected messages rather than by a
// file-name prefix or an era.
//
// It keyed a per-vector ledger for the alignment population until #306; see the census at
// `validateAdmitCeiling` for what replaced it and why.
//
// **Derived from the Kind rather than typed out, since #364 renamed the Kind under it.** The
// comment above says this prefix is a string the corpus supplies; a hand-typed copy of it is
// therefore a claim about `classify`'s key format maintained in a second place, and the rename from
// `assert_invalid` to `assert_invalid (module)` broke exactly that copy while every derived reader
// moved with it. The concatenation order matches `scoreValidation`'s (`form + " accepted, expected:
// "`) because that is the one producer of these keys.
var admittedKeyPrefix = KindAssertInvalid.String() + " accepted, expected: "

// TestPhase1Files runs every suite file that phase 1 can meaningfully score,
// so the board covers the byte-string corpus rather than one file.
func TestPhase1Files(t *testing.T) {
	requireSuite(t)
	files := boardFiles(t)
	totalPass, totalFail, totalUnsup, totalGated, totalUnimpl := 0, 0, 0, 0, 0
	byHead := map[string]int{}
	byCap := map[Capability]int{}
	aggBuckets := map[string][]Failure{}
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		t.Log("\n" + r.Board())
		totalPass += r.Pass
		totalFail += r.Fail
		totalUnsup += r.Unsupported
		totalGated += r.Gated
		totalUnimpl += r.Unimplemented
		for h, n := range r.UnsupportedByHead {
			byHead[h] += n
		}
		for c, n := range r.UnimplementedByCapability {
			byCap[c] += n
		}
		for k, fs := range r.Buckets {
			aggBuckets[k] = append(aggBuckets[k], fs...)
		}
	}
	// The corpus identity, printed *with* the counts and not merely available somewhere
	// (#42). A count is a claim about a corpus, so a board line that does not name its
	// corpus is a verdict without an identity check — two developers on different fetch
	// dates can quote incompatible numbers and both be honest. Pairing the suite pin with
	// the engine commit is what makes "never quote a suite count that wasn't run"
	// unambiguous about *which* run.
	t.Logf("corpus: suite pin %s (%s)", suitePin(t), suiteDir)
	t.Logf("board total over %d files: %d pass, %d fail, %d unsupported, %d gated, %d unimplemented",
		len(files), totalPass, totalFail, totalUnsup, totalGated, totalUnimpl)

	// The fail column as a work plan across the whole board, not per file. Printed every
	// run so the PR's Board line quotes measured buckets rather than a recollection.
	for _, k := range (&Result{Buckets: aggBuckets}).BucketsBySize() {
		t.Logf("  fail %5d  %s", len(aggBuckets[k]), k)
	}

	// The admission stratum, re-printed on its own. Every one of these lines is already above, in
	// the fail-bucket list it is filtered out of — the point is that up there it is interleaved by
	// size with a hundred other keys, and *this column is the accept direction*. A reader asking
	// "what does this validator still say yes to" should not have to grep a prefix out of the work
	// plan to find out.
	//
	// **A census and not a ledger, and that is the answer to #307's flag** ("a base that has stopped
	// having a documented majority is a count agreeing with any 104"). The residual now has a
	// documented majority and the document is this loop's output, regenerated every run: the fix for
	// an undocumented count was to make the count print its own decomposition, not to hand-name 104
	// sites. Extending the naming — a `file:line` set per member, as `alignmentAdmissions` held for
	// the alignment population until #306 drained it — is a decision to flag rather than to take: it
	// buys per-site membership checks and costs a ledger that a principal has already declined once.
	type admission struct {
		reason string
		n      int
	}
	var admitted []admission
	for k, fs := range aggBuckets {
		if reason, ok := strings.CutPrefix(k, admittedKeyPrefix); ok {
			admitted = append(admitted, admission{reason, len(fs)})
		}
	}
	// Size descending, then by reason, so two runs of the same board print the same order — a
	// census whose row order depends on map iteration cannot be diffed between PRs, which is the
	// only use it has.
	slices.SortFunc(admitted, func(a, b admission) int {
		if a.n != b.n {
			return b.n - a.n
		}
		return strings.Compare(a.reason, b.reason)
	})
	for _, a := range admitted {
		t.Logf("  admitted %5d  expected: %s", a.n, a.reason)
	}

	// The unsupported column as a work plan, not a number. Printed every run so the
	// PR's Board line can quote which component each unrun vector waits on.
	agg := &Result{UnsupportedByHead: byHead}
	for _, h := range agg.UnsupportedByHeadBySize() {
		t.Logf("  unsupported %5d  %s", byHead[h], h)
	}

	// Monotonicity ceiling on the unsupported column (#52).
	//
	// The column is honest only if it is *watched*: an unsupported count that grows
	// means either the corpus moved or a capability regressed, and both want an
	// alarm rather than a larger number nobody re-reads. This is the pass-count
	// floor's mirror image — that one may only rise, this one may only fall — and
	// the pair is what makes "shrinking monotonically as components land" a
	// checkable claim instead of an intention.
	//
	// **26742 after the quote admission (decision 0010), was 1345.** Raised exactly
	// once, with the reason stated: admitting (module quote ...) put 54 more files on
	// the derived board, and their commands are overwhelmingly forms the harness still
	// cannot ask about. That is the one licensed reason to raise this number — the
	// corpus grew — and it is why the rule is "never raised *without saying what
	// moved*" rather than "never raised".
	//
	// Lowered as components land. #42 (SHA-pin the suite) is what keeps this number
	// meaningful, since a corpus that drifts changes it for reasons that are not
	// findings.
	//
	// # 60872 after the bare-module admission (#69), was 26742
	//
	// Raised a second time, same licensed reason — the corpus grew — but the *shape* of the
	// growth is different from the quote admission's and the difference is the whole
	// account. Retaining a source span made `(module <wat body>)` askable, and that changed
	// two things at once:
	//
	//   - **Within the 68 files already on the board**, the column *fell* by exactly the
	//     bare-module count: 26742 → 25623, −1119, with pass +1114 and fail +5. Net zero on
	//     the total, which is the identity #69's definition of done asked for.
	//   - **185 further files entered the board**, because boardFiles selects on "has at
	//     least one scorable command" and these files had none until now. They bring 1016
	//     pass, 17 fail — and 35240 unsupported, since a file admitted for its module forms
	//     also brings its assert_return/assert_invalid population with it.
	//
	// So 60872 = 25623 + 35240 + 9, and the +9 is the `(module definition …)` /
	// `(module instance …)` forms newly *classified* as unsupported rather than handed to
	// the wat reader (see classify — asking the wrong reader manufactured 9 of the first 22
	// reds). Both movements are stated because reporting only the first would be the
	// invisibility decision 0010 exists to prevent: the honest sentence is "the column grew
	// by 34130 while the population it was measuring shrank by 1119", and a single number
	// cannot say that.
	//
	// The ceiling is deliberately *not* split per-population, though the temptation is
	// real. A per-file or per-cohort ceiling would bind tighter, and it is the right next
	// move if this number is raised again — noted rather than done, because #69 is
	// board-shape work and a ceiling redesign is its own decision.
	// Slack-checked (0013), and this is the bound where staleness runs *downward*: every
	// capability that lands moves the actual further below the ceiling, so the gap grows on
	// exactly the schedule of ordinary progress. boardBound measures the distance in the
	// direction that applies to a ceiling.
	//
	// **60872 → 32764, and this is the first time this number has fallen for the reason it
	// was built to measure.** Both previous movements were the corpus growing; this one is
	// 28108 vectors *answered*, because `assert_return` stopped being a head the classifier
	// records and became a command the harness can ask. The whole fall is one head: 52638
	// assert_return commands were unsupported, 24530 still are — the shapes assertReturn
	// declines, which it declines structurally and says so at each arm — and 28108 now get a
	// verdict. Nothing else moved; the corpus was the same 253 files. (Past tense as of the
	// next movement below, which selects a 254th — the sentence was true when written and the
	// tense is what keeps it true.)
	//
	// The lowering is the record of the progress, per the standing rule, and it is lowered
	// **in the PR that drained it** rather than left as slack for a later reader to notice.
	//
	// # 32764 → 32377, and the small number is the honest one
	//
	// −387 in the PR that gave the engine a memory, and the modesty is the finding rather than an
	// embarrassment to be dressed up: memory work paid in the **fail** column (exec 8713 → 440),
	// not here, because those 8,000 vectors were already *asked* and answered wrongly. This
	// column moves only when a command shape the classifier used to decline becomes askable, and
	// #7's memory work made two such shapes askable:
	//
	//	347  `invoke` — a bare top-level `(invoke …)`, 357 → 10. It is a state mutation, so
	//	     walking past it did not merely lose the command: it made the *following*
	//	     `assert_return` read a memory nobody had stored to, and 73 of those were being
	//	     scored as the interpreter computing a wrong value. A skip is not a verdict, with the
	//	     roles swapped — the skip passed by asking nothing and made a neighbour fail.
	//	 40  `assert_trap` wrapping a bare `(module …)` — instantiation itself trapping, which
	//	     0015 is the record of. 54 such forms exist and all 54 are on the board; **40 of them
	//	     moved off this column and 14 did not, because those 14 were not *on the board* at
	//	     all.** They are `data1.wast`, every form in which is this shape — so under
	//	     per-command selection the file held no scorable command, boardFiles did not select
	//	     it, and it contributed 0 to this column and 0 to the denominator. Its 14 therefore
	//	     arrive as **new** commands in a **new file**, which is the one place in this PR the
	//	     board's file count moves: **253 → 254**. Measured by set-differencing the
	//	     `assert_trap` command keys at b11a664 against this tree — 4963 → 4977, the entire
	//	     difference being `data1.wast:14` — rather than inferred from 54 − 40, which is the
	//	     arithmetic that hid the distinction in this comment's first draft. A conversion
	//	     lowers this column; an admission raises the denominator instead, and a bare
	//	     `54 − 40 = 14` calls them the same thing.
	//
	// 347 + 40 = 387, which is why the two lines are quoted rather than the total alone. The 10
	// `invoke` commands still unsupported are the shapes classify declines structurally.
	//
	// # 32377, unmoved, twice in a row — and by now that is a prediction rather than a report
	//
	// The saturating truncations (#7) left this column **unmoved**, as `global.get`/`global.set` did
	// before them, and the second consecutive zero is what turns the pattern into a stated rule:
	// *unsupported counts commands the harness cannot ask; the fail columns count questions the
	// engine cannot answer.* An interpreter arm answers a question that was already being asked, so
	// it moves pass and fail by 180 each and cannot touch this number by construction. Only a front
	// end admitting a grammar moves it — which is what the two entries above record, both of them
	// classifier work rather than engine work.
	//
	// That seam is why this PR does not lower the ceiling and is not overhead for anything: the
	// column's own recon (#153) priced its largest stratum and found the answer is **not** an
	// interpreter arm. 24,147 of the remaining 24,530 are one value form, `v128.const`, and teaching
	// the script reader to admit it would move those vectors from this column into `fail` as "no
	// instance" — because zero of them sit behind a module that instantiates today. *A column
	// drained into the wrong column is not progress.* The chain is #8's v128 immediates, then the
	// SIMD gate, then instantiation, with the script reader last, and it is scoped as its own arc
	// rather than smuggled into a PR about conversions.
	// # 32377 → 27501, the largest single drain this column has recorded — and no engine lines
	//
	// −4876, from a harness Kind rather than from an arm: `(assert_trap (invoke "f" arg*) "text")`,
	// a command shape the classifier declined and the run loop therefore never asked (#157). The
	// population is **4923** such commands, of which **4876** classify; the 47 that do not are the
	// two forms `invokeAction` declines structurally — 20 naming a module with `(invoke $M …)` and
	// 27 taking a reference-typed argument — and they stay in this column rather than being
	// admitted as questions the harness cannot yet phrase. (The recon quoted 4903/27; both halves
	// were short by the same twenty, because its probe shared `invokeAction`'s accept condition —
	// see the classifier arm for the correction and why it is grave #106's shape.)
	//
	// The drain partitions **2893 pass + 1440 fail + 543 gated = 4876**, which is the check rather
	// than the claim: every admitted command reached a verdict, and the three columns close against
	// the drain exactly. Note which way that cuts against the seam stated below — this *is* the
	// front end admitting a grammar, so it moves this column by construction, and it is the one
	// case where the admission also converts a majority to pass, because a trapping call needs no
	// value comparison and the engine already traps correctly.
	//
	// Product work by Scott's stamp and in his words: *for a conformance engine, a missing harness
	// Kind is a hole in the measuring column itself — the phase's product is the board's truth, and
	// this drains the column that defines the phase.*
	//
	// # 27501 → 27099, and the −402 decomposes exactly
	//
	// Three command shapes the classifier declined, admitted together because they are one
	// grammar — the script-level module registry of 0017's Q1 — and the drain is the sum of their
	// populations with no remainder:
	//
	//	 78  `(register "name" $M?)`         KindRegister
	//	200  `(assert_unlinkable (module …) "text")`  KindAssertUnlinkable
	//	124  module-named actions            KindNamedInvoke / KindNamedAssertReturn / KindNamedAssertTrap
	//	---
	//	402
	//
	// **The exactness is the check, not the claim.** Every command this PR admits reached a
	// verdict, so the drain closes against the other columns: pass +696, fail −389, gated +50, and
	// 696 − 389 + 50 = 357 is *not* 402 — the difference is 45, which is the board's total command
	// count *falling* (65064 → 65019), because `register` is the first Kind whose successful
	// outcome is neither pass nor fail. A register binds a name and scores nothing; there is
	// nothing for it to be right or wrong about, and inventing a pass for it would be the board
	// buying a count with a command that asked no question. So 78 admitted registers contribute
	// 33 verdicts (23 encode-column fails + 3 exec fails + 7 gated) and 45 silences.
	//
	// The 124 is the measured pair rather than one number, per the census law: **132** module-named
	// actions exist, **124** classify, and the 8 short are one twenty-line stretch of
	// `elem.wast:1016-1030` declining on `ref.extern` — 2 on an argument, 6 on an expected result.
	// Keying the census on arguments alone said 110-of-110 admitted and was wrong by six; see
	// `namedInvokeAction` in wast.go for the correction and why both halves have to be counted.
	//
	// # 27099 → 26822, and the −277 decomposes exactly (#196/#197)
	//
	// `readRefConst` (value.go) admits `ref.null <heaptype>` / `ref.extern N` as an argument, and
	// `invokeIndex`/`invoke` (internal/interp) accept and produce reference-typed parameters,
	// locals, and results instead of refusing every one unconditionally — the two blockers #196
	// and #197 each named, landed together because neither converts a vector alone (#197's own
	// finding). The drain partitions **87 pass + 11 fail − 179 gated's own sign flipped** — stated
	// properly: pass +87, fail +11, gated +179, and 87 + 11 + 179 = 277 exactly, which is the
	// check rather than the claim (`TestPhase1Files`'s own board line, run before and after).
	//
	// The +179 gated is every one of the 270-vector estimate's members that also needs the GC
	// gate for an unrelated reason (a struct/array/i31 heaptype elsewhere in the same module) —
	// expected and correctly attributed, per #196's own note that "some vectors may still be
	// Unsupported [or gated] afterward ... that's expected". The +11 fail is not a regression:
	// diffed bucket-by-bucket against the pre-change board, 3 vectors that used to fail on
	// `assert_return value mismatch` (the harness computing a value over state a setup `invoke`
	// never wrote, exactly `load1.wast`'s shape from #196's own issue text) now genuinely pass,
	// and the remaining delta reclassifies pre-existing encoder-frontier fails
	// (`try_table`/`(table …)` field encoding, #8, unrelated to this change) into their true
	// bucket — the vectors were always going to land there once the harness could even attempt
	// them; before this change they were short-circuited earlier and misfiled as a value
	// mismatch. Measured with the harness, not a grep, per decision 0007/#161's standing rule.
	// 26822 → 26804, −18 (#201 rung 2a): `KindAssertException` makes 18 previously-unclassified
	// `assert_exception` vectors askable, and all 18 land in `gated` rather than `pass` — the
	// EH gate is off by default and `isException` cannot yet answer yes (rung 2c's own
	// prerequisite), so the drain here is purely a reclassification into an honest verdict,
	// matching #199's own "gated, not pass" precedent for rung 1.
	// 26804 → 2689, −24115 (harness v128 widening, decision 0024's forced question 5):
	// `readV128Const`/`Matches`'s KindV128 branch make every `v128.const` argument and
	// expectation askable, so the SIMD corpus's `assert_return`/`invoke` vectors reach the
	// decoder instead of being refused by `readConst` before the gate is ever consulted. The
	// default lane's Gated column carries the exact same 24115 (SIMD stays off by default), so
	// this is a reclassification from "the harness cannot ask" to "the harness asked and the
	// gate declined" — the honest verdict this widening exists to produce, not new engine
	// capability.
	// 2689 → 2657, −32 (`(either …)` result-position widening, 0028 d4): `readResult` admits the
	// reference's `EitherResult` (`script.ml:44`, `runner.ml:485`) in expected-result position, so
	// the 32 `assert_return`s that use it become askable. **The same shape as the entry above, one
	// proposal later** — all 32 sit in the six relaxed-SIMD files, the relaxed gate is off by
	// default, and the default lane's Gated column carries the exact same +32 (3625 → 3657). A
	// reclassification from "the harness cannot ask" to "the harness asked and the gate declined",
	// not new engine capability, and the attribution is 1:1: 32 occurrences of `(either` in the
	// corpus, 32 commands drained.
	//
	// It is also the entry that falsified this PR's own pre-registered forecast, which said
	// `unsupported` would be **unmoved and structurally so**. That was wrong twice over — the
	// direction of `gated` too, forecast at 3625 → 3588 — and the precedent proving it wrong was
	// the paragraph immediately above, sitting on this very constant. A harness widening moves this
	// column *whatever the gate campaign does*, because the two changes are independent and 0028 d4
	// put them in one PR. Recorded here rather than only in the PR body, since the ledger is where
	// the next forecast will be read from.
	// 2657 → 83, −2574 (#9 slice 1's `assert_invalid` arm). The largest single drain this column has
	// taken. 2591 `assert_invalid` commands were unsupported; 17 remain (11 `(module binary …)`, 6
	// `(module quote …)`, both forms left unadmitted on purpose — see the parse arm), and the 2574
	// converted split 463 gated / 2111 verdicts.
	//
	// **This entry read "it is engine capability rather than a harness widening", and that was the
	// two entries above being contradicted rather than distinguished** (ruling: Scott, PR #295).
	// The −2574 *is* the harness widening, exactly as they describe: `classify`'s new arm is what
	// makes the command askable, and it would have drained this column by very nearly the same
	// 2574 with an always-accept validator behind it — the vectors would have landed in the
	// admission bucket instead of being spread across five destinations. So this column's figure
	// cannot be the engine's reward, and the entry claiming it was had made one number carry two
	// facts its own forecast (#291) had deliberately separated.
	//
	// The engine's contribution is the **destination split**, which lives in
	// `TestAssertInvalidDestinationLedgerCloses` because *a total is not a ledger*: at slice 1, 906 of
	// the 2574 passed, 1056 were named declines, 142 accept-direction admissions, 10 right-refusal
	// wrong-message, 460 gated — and it closed to the vector. The 906 is that slice's reward; the 1056
	// was the next slices' work plan, and slice 2 (#305) drew 648 of it down: the split reads
	// **1554 / 388 / 158 / 14 / 460** now, re-based at the ledger itself, which is where a figure that
	// moves per slice belongs. Quoted here in both eras rather than updated in place, because this
	// entry's subject is the −2574 *this* column took and that number did not move.
	// A previous draft accounted for the 2574 as "829 conversions +
	// 1201 validate stratum", which left **544 vectors unaccounted** and mixed a figure over a
	// restricted subject (board-visible `type mismatch` conversions only) into an identity over
	// the whole population — 829 of the 906 passes expect `type mismatch` and 77 expect something
	// else. Read the ledger, not this delta, for what the validator did.
	//
	// The gated 463 are a reclassification of exactly the kind the entries above describe, in the
	// same PR as the capability that made them askable, and they are the reason the drain here
	// (−2574) is larger than the pass+fail gain from conversions (2111).
	//
	// The remaining 83 are the column's whole content: 39 `assert_return`, 17 `assert_invalid`, 15
	// `assert_exhaustion`, 9 `module`, 3 with no head atom. A column this small is close to being
	// an equality, and the next drain should say so.
	//
	// # 83, unmoved by slice 2 (#305/#307), and this zero is *derived* rather than reported
	//
	// A validator slice cannot move this column, and the reason is upstream of any measurement: an
	// `assert_invalid` vector the validator declines is scored **`fail` with a named cause**, so
	// slice 2's 648 conversions were all *within* the fail column's sub-partition. The column counts
	// commands the harness has no case for, that case landed in slice 1, and slice 2 changed no
	// command vocabulary — `git diff -- internal/spec/wast.go` touches `Failure.Accepted` and nothing
	// in `classify`. So the zero follows from the column's definition plus the diff's shape *before*
	// the board is run, which makes measuring it a check on the derivation rather than a forecast
	// being confirmed.
	//
	// **The reward figure is therefore the ordinary one and not a substitution**: `passFloor` +648.
	// #235's carve-out exists for the case where the *ordinary* figure is structurally zero (a gate
	// campaign, where vectors score `gated`), and that is not this case — nothing needed to be
	// authorized here, because nothing was being substituted. (Ruling: Scott, PR #307, on the actor's
	// flag asking whether the carve-out extended. It did not need to.)
	//
	// # 83 → 66 elsewhere, and 66 → 57 here: #426 takes the whole `module` sub-column
	//
	// The 9 the entry above listed as "9 `module`" are gone, which is the drain that entry asked for
	// when it said "a column this small is close to being an equality, and the next drain should say
	// so." They were `(module definition …)` ×6 and `(module instance …)` ×3, and they are now
	// `KindModuleDefinition` and `KindModuleInstance` — 6 passes and 3 honest gate declines rather
	// than 9 commands the harness had no case for.
	//
	// **The remaining 57 no longer contain a single `module` head**, which is a sharper statement than
	// the count: every command form the corpus writes with a `module` head is now classified, and the
	// residue is 39 `assert_return`, 15 `assert_exhaustion`, and 3 with no head atom. So this column's
	// work plan has exactly two named subjects left plus a lexical one. The next drain can say which.
	//
	// # 57 → 29: #270 takes 28 of the 39 `assert_return`, and the entry above's question is answered
	//
	// The 39 were **not one subject**, and the split was done at classification time before any code
	// moved: 28 declining on a reference *result* the harness could not spell or could not build
	// (#270), and 11 on `either`/NaN-lane shapes with an unrelated cause (#323). 28 + 11 + 15
	// `assert_exhaustion` (#440) + 3 no-head-atom (#320) = 57, decomposing with nothing left over,
	// which is what made the forecast below a number rather than an estimate.
	//
	// #270 was **two-sided**, and either half alone would have moved this column by zero. The want side
	// could not spell six of `parser.mly:1517-1530`'s eight `RefTypePat` arms — `(ref.array)` had no
	// representation at all, and 17 of the 28 decline on that one — while the got side could not build
	// a non-null reference whose type `valKind` cannot name, so even a readable `(ref.array)` would
	// have had nothing to be judged against. The repair is `RefPat` and `RefPayload`: the pattern the
	// vector wrote and the constructor the result is, which is exactly `assert_ref_pat`'s two operands
	// (`runner.ml:464-476`).
	//
	// **The 28 land in `gated`, not in `pass`** — forecast in ADR 0039 and measured at 4159 → 4187 —
	// because every one of them is in a GC-gated file and this is a harness widening rather than an
	// engine capability. They are now *asked*, and the all-on lane is where the answers appear:
	// `array.wast` 8, `array_new_elem.wast` 8, `extern.wast` 6, `array_new_data.wast` 4, `i31.wast` 1,
	// `struct.wast` 1. (The per-file split is **not** the shape census the forecast was built from —
	// that one reads `result: ref.array` 17, `ref.eq` 4, `ref.host` 2, `ref.i31` 2, `ref.struct` 2,
	// one `ref.host` argument. Both are 28 and neither is a re-spelling of the other: a `(ref.array)`
	// expectation appears in three files.)
	//
	// **All 28 pass all-on**, against a forecast that claimed only `extern.wast`'s 6 and deliberately
	// declined to forecast the other 22 — being able to *ask* a question is not a prediction of its
	// answer. So `allOnPassFloor` moves +28 to 65042 and `allOnFailCeiling` does not move at all: the
	// 22 unforecast vectors were answered correctly by an engine that already had the capability and
	// was never being asked. That the forecast was conservative in this direction is worth stating
	// plainly rather than reading back as a 28-for-28 hit rate.
	//
	// The residue is 11 `assert_return` (#323), 15 `assert_exhaustion` (#440), 3 no-head-atom (#320) —
	// three named subjects, one issue each, and no unattributed remainder.
	//
	// # 29 → 14: #440 takes the 15 `assert_exhaustion`, and the decomposition holds a second time
	//
	// One `classify` arm and one predicate member, no engine change: `callBudget` and `trapExhaustion`
	// had been in `internal/interp/call.go` since the bulk trio, so these 15 were unsupported because
	// the *directive* was outside the harness's vocabulary, not because the engine could not answer.
	// Forecast −15 and measured −15, off a corpus enumeration that normalized every
	// `assert_exhaustion` line in the suite: 15 uses, 4 files, two spellings, zero naming a module.
	//
	// **The split inside that 15 was forecast wrong, and the record says so.** The pre-registration
	// predicted 5 pass and 10 fail — the ten `skip-stack-guard-page.wast` vectors were to fail *by
	// construction*, on the argument that the file probes a host guard page while `callBudget` is a
	// host-independent 10000-frame counter that a recursion bounded at 901 frames never reaches.
	// Measured: **15 pass, 0 fail.** The premise was wrong because it came from the first 30 lines of
	// a 2284-line file: `$function-with-many-locals` **calls itself** (`:154-155`, whose own comment
	// says it recurses first on purpose), so the recursion is unbounded and the budget is genuinely
	// reached. The engine's behaviour was right and the forecast's *reasoning* was not, which is a
	// different correction from a number being off — a design consequence was about to be escalated
	// to Scott on the strength of a sample.
	//
	// The residue is 11 `assert_return` (#323) + 3 no-head-atom (#320) = 14, still with no
	// unattributed remainder.
	//
	// # 14 → 11: #459 takes the 3 no-head-atom, and the fix is not where the issue said it was
	//
	// The three are `annotations.wast:98,129,154`, `((@a) module …)` forms where a custom annotation
	// precedes the head, so `head()` returns `""` and `classify` falls to its `(no head atom)`
	// placeholder. #320 named the drain as *teaching classify to skip annotation nodes when it looks
	// for the head*, and the reference says that is the wrong layer: `lexer.mll:821-828` records an
	// annotation into a side table and tail-calls `token lexbuf`, emitting no token, three rules
	// above the `;;` and `(;` cases that do the same. An annotation is transparent to the grammar
	// **wherever a token may appear**, not merely in first position — so the skip belongs in the
	// s-expression reader (`sexpr.go`'s `isAnnotation`, dropped in both node-assembly loops), and
	// `wast.go`'s six *positional* reads are covered by the same predicate instead of needing six of
	// their own. Forecast −3 / +3 and measured −3 / +3, with `gated` flat at 4187 and `fail` flat at
	// 0: one column into another, `annotations.wast` 71/71 pass, 3 unsupported → 74/74 pass.
	//
	// **The placeholder branch stays and its population is now zero** —
	// `TestUnsupportedIsBucketedByCommand` pins that no bucket key is ever empty, and an unlabelled
	// row in a work-plan column is the one nobody investigates. That makes it a guard with no
	// witnesses rather than a bucket with three, which is stated here because the branch's *previous*
	// comment claimed the opposite for two commits running (see `wast.go`'s quoted pair).
	//
	// # What the residue is made of, which is a finding and not a scheduling note
	//
	// **The whole of what remains in this column is harness debt.** Ordered recorded in the repo by
	// Scott on #459 rather than left in an issue comment, because *"nobody reading `unsupported → 0`
	// later should take it for an engine milestone"* — and this is where a reader of that zero comes
	// looking. The measurement ran Scott's #458 test — *does it change what the runtime can do, or
	// only what the harness can say about what it does?* — over all 14:
	//
	//   - the 3 above: a lexical transparency the reader lacked. The engine decoded, validated and
	//     instantiated all three modules already, which #459's probe asserts directly rather than by
	//     witness (`annot_test.go`, and the reason a probe was needed is that a `KindModuleText` row
	//     keeps its pass on an instantiation decline, so the board *cannot* see this).
	//   - the 11 `(get …)` rows under #323: the engine holds the global and the export; what is
	//     missing is a public read path (`Instance.Global`) for the harness to ask through. #323's
	//     own premises were verified true and its *conclusion* — that this requires engine work —
	//     does not follow from them: `spectestBuiltin` already synthesizes wat and instantiates it
	//     through the ordinary front end, so a synthesized reader module drains all 11 with zero
	//     engine change. Scott ruled for the direct read anyway, on fidelity: a proxy answer that
	//     agrees is a weaker claim than a direct read, and the column would otherwise hold two
	//     grades of pass with nothing marking which is which.
	//
	// So `unsupported` reaching 0 will mean the harness can finally *ask* every question the corpus
	// writes. It will not mean the engine gained anything, and on this slice's evidence the engine
	// had already answered — 3 of the 3 correctly, unasked, for as long as the rows sat here. The
	// figure that carries engine capability is `passFloor` and its all-on twin; this one carries
	// vocabulary. **A reward figure is not a classification** — that separation is #458's, and this
	// column is the clearest case of it in the file.
	// # 11 → 0: #323 takes the 11 `(get …)` rows, and the column is empty
	//
	// The verb became a **field on `Command`** rather than two new Kinds (ruling: Scott, #323
	// comment 5364724819), so all 11 arrive on the `assert_return` Kinds that already existed:
	// `KindNamedAssertReturn` for the 10 named forms and `KindAssertReturn` for the 1 unnamed one.
	// Zero new Kinds, against a forecast of two. The engine side is `interp.Instance.Global` and
	// the public `burroughs.Instance.Global`, reading through `global.value` — `global.get`'s
	// read-only twin, sharing its layout dispatch so grave #239's split (right on the write half,
	// missing on the read-back half) cannot recur on a third consumer.
	//
	// Forecast −11 / +11 and measured −11 / +11: `unsupported` 11 → **0**, `pass` 60946 → **60957**,
	// with `fail` flat at 0 and `gated` flat at 4187. `exports.wast` and `linking.wast` are the two
	// files, and both go to a full board.
	//
	// **The residue is now nothing, and the decomposition closed every time it was asked to.** 57 →
	// 29 → 14 → 11 → 0 across #270, #440, #459 and #323, with each step's remainder named by issue
	// and no unattributed rows at any point. That is the property worth recording, more than the
	// zero: a column drained by four PRs that each predicted their own subtraction is a different
	// artifact from a column that happened to empty.
	//
	// # The class, restated because the bound moved and because the zero invites the wrong reading
	//
	// **This column reaching 0 is a fact about the harness's vocabulary and not an engine
	// milestone.** Restated here rather than resting on the last statement of it, per README's own
	// rule that the class is repeated at this account each time the bound moves — and #323 is the
	// case that most needs it, being the one row-set whose drain *did* add engine surface
	// (`Instance.Global`) and therefore the one a reader could most plausibly mistake for
	// capability. It still is not: the engine already held the global and the export, `#459`'s
	// classification of all 11 as harness debt was measured under Scott's #458 test and stands, and
	// the new method is a *read path for the harness to ask through* rather than a computation the
	// engine could not do. The figures that carry engine capability are `passFloor` and
	// `allOnPassFloor`; this one carries what the harness can ask.
	//
	// **And a ceiling standing at 0 is now this file's weakest instrument, which is worth saying at
	// the moment it becomes true.** A zero-fail column loses its work plan and the gradient inverts
	// toward instruments; a zero-*unsupported* column loses more than that, because it can no longer
	// distinguish "the corpus grew a form" from "a classifier regressed" by size — every regression
	// is now the first one. What it still does is fire on any non-zero at all, which is the whole of
	// what a drained ceiling can do and is why it stays rather than being retired: the population it
	// bounds is the set of corpus forms this harness cannot ask, and that set growing silently is
	// exactly what it exists to prevent. Rows added by a suite bump land here with no attribution,
	// so the class above is asserted of *today's* zero and of nothing later.
	const unsupportedCeiling = 0
	// Slack 0 as of #387's ruling, with the other two tracked board counts — see
	// `boardbound_test.go`'s retirement section. This is the one of the three where the retired
	// slack's stated purpose bit hardest: a ceiling drains *toward* its column, so 250 of tolerance
	// on a column standing at 66 was four times the population it bounded.
	boardBound(t, "unsupportedCeiling", totalUnsup, unsupportedCeiling, 0, ceilingBound,
		"either a capability regressed or the corpus moved; both need an explanation rather "+
			"than a raised ceiling")

	// The fourth verdict's ceiling, and its purpose is the *drain* (decision 0010).
	//
	// **At its terminal value, which is the only value that makes it an assertion.** It
	// was 1236 from the column's creation until the wat reader landed, all of them
	// waiting on it (#53); the retirement converted every one, so the ceiling is 0.
	//
	// Lowering it is not bookkeeping. A ceiling of 1236 against an actual 0 permits 1236
	// vectors to reappear in the fourth column without a word — the *whole* population,
	// and precisely the disappearance guard 6 exists to prevent, wearing a ceiling's
	// clothes. A bound that no longer binds anything is a control asserting nothing while
	// looking like one, and this drain had a terminal value fixed by decision 0004 rather
	// than by taste: no minor version is cut while its milestone's unimplemented is
	// nonzero, and v0.1.0 requires zero.
	//
	// Still a ceiling rather than an equality, because at zero the two coincide and the
	// ceiling generalizes: the next capability admitted raises it with an account, and
	// drains it back down.
	// Slack 0: at its terminal value, where the distance to the actual is not a quantity
	// that can grow. 0004 fixes the terminal at zero, so this one cannot go stale even in
	// principle — unlike unsupportedCeiling above, which drains without a floor under it.
	const unimplementedCeiling = 0
	boardBound(t, "unimplementedCeiling", totalUnimpl, unimplementedCeiling, 0, ceilingBound,
		"a new capability gap appeared or one widened; the column exists to drain, so growth "+
			"needs an explanation")

	// Every unimplemented vector is attributed, or the column is a bare number again.
	attributed := 0
	for _, n := range byCap {
		attributed += n
	}
	if attributed != totalUnimpl {
		t.Errorf("unimplemented totals %d but attribution sums to %d; the column and its "+
			"work plan disagree", totalUnimpl, attributed)
	}
	for _, c := range (&Result{UnimplementedByCapability: byCap}).UnimplementedByCapabilityBySize() {
		issue, _ := CapabilityIssue(c)
		t.Logf("  unimplemented %5d  %s (%s)", byCap[c], c, issue)
	}

	// **The fail column is the board's instrument, and this is what keeps it one.**
	//
	// This assertion is the reason decision 0010 exists. Reading A of the admission
	// would have scored the 1236 quote vectors as failures, taking fail from 1 to 1237
	// — and a genuine regression landing tomorrow would arrive as 1238, invisible. A
	// ceiling on fail is only meaningful while fail means *defect*, so the two changes
	// are one change: admit the corpus, and keep the column that says what is broken.
	//
	// **The wat reader raised this 1 → 601, and the account is why that is not the
	// invisibility the paragraph above forbids.** 600 of the 601 are text-layer vectors
	// whose grammar is not written: the lexer answers 636 of the 1236 quote vectors and
	// the other 600 need the parser (#8), the validator, or the name decoder. Every one
	// of them is a *fail*, in a named bucket, and not a fourth-verdict entry — the
	// fourth verdict was for a component that did not exist, and the lexer exists, so a
	// vector it can be asked about has no excuse left. Reporting them as unimplemented
	// would be the disappearance guard 6 exists to prevent, one layer up.
	//
	// What would have been invisible is a single ceiling of 601, which is why the
	// ceiling is now **four ceilings over a structural partition**: a new decoder defect
	// arrives as `binaryFail 1 > 0` regardless of what the other three columns are doing.
	// The partition is not on the bucket string because the layers share strings —
	// `malformed UTF-8 encoding` is a bucket on both front ends — and *when a partition's
	// members share a value, an equality on that value is not a partition check*.
	//
	// **The key is Failure.Stratum, not Failure.Kind, and the change of key is #7's first
	// finding rather than a refactor.** Kind worked as the partition key for exactly as
	// long as every failure was caused by the command it was reported against. An
	// `assert_return` breaks that identity: the *command* is an assert_return, and the
	// *defect* belongs to whichever component failed to produce the instance. Measured at
	// this revision, **13775 of the 14216 fails are `assert_return`s whose module never
	// instantiated, and every one of them is text.EncodeModule's instruction frontier
	// (#8)** — so a Kind-keyed switch would have charged 13775 encoder gaps to the
	// interpreter's brand-new ceiling and reported the wrong stratum as broken on the day
	// the interpreter landed. That is the same defect as #69's `default` arm, one layer
	// deeper: not a Kind assigned to the wrong column, but a *column that cannot be
	// derived from Kind at all*.
	//
	// So the stratum is **stated at the failure site** by the code that knows which entry
	// point returned the error, never derived here. Deriving it is precisely what put 13
	// text reds in the decoder's column in #69.
	//
	// Falsified in both directions before being trusted, per the print-don't-trust rule:
	// with StratumEncode's arm folded into StratumExec, execFail reads 14347 and encodeFail
	// 0, and both arms fail. That is the check TestSectionSizeBothSigns's grave (#34) asks
	// for — a partition test verified against the partition, not against its labels.
	//
	// **Every arm is named and none is a `default`.** A `default` absorbs every stratum
	// added later and assigns it a layer by omission; StratumUnset is a loud failure rather
	// than a quietly larger number, which is the same move as the unregistered-capability
	// panic.
	binaryFail, textFail, encodeFail, execFail, validateFail := 0, 0, 0, 0, 0
	// The three parts of validateFail, sub-partitioned here rather than counted separately, so the
	// three ceilings below cannot drift from the stratum they decompose.
	//
	// **Two of these were one subtraction until slice 2**, and the reason they are three counters
	// now is at `Failure.Accepted`: `validateFail − validateDeclined` named the admission stratum
	// only while the stratum's wrong-message population was 0, and when slice 2 made it 4 the
	// subtraction reported 162 for a population of 158 without any bound noticing. A partition
	// recovered by subtraction is a partition that cannot grow an element.
	//
	// **Four since #341**, and the fourth arrived the way the third should have: the module-definition
	// arm reports over-rejections, so the arm that would otherwise absorb them (`default`, the
	// wrong-message case, standing at 0) got its flag *before* the first row rather than after four
	// had already been miscounted. See Failure.OverRejected.
	validateDeclined, validateAdmitted, validateMismatched, validateOverRejected := 0, 0, 0, 0
	for _, fs := range aggBuckets {
		for _, f := range fs {
			switch f.Stratum {
			case StratumBinary:
				binaryFail++
			case StratumText:
				textFail++
			case StratumEncode:
				// **A column of its own, not folded into StratumText** (#8). ReadModule
				// answers 254 files' module forms with 0 reds; EncodeModule cannot yet emit
				// most instruction bodies. Folding them would raise the reader's ceiling
				// from 0 to 13775 and destroy the only instrument watching the reader for
				// regressions — one instrument per component, or neither is an instrument.
				encodeFail++
			case StratumExec:
				// **A fourth partition, not a third arm on an existing one** (#7). The
				// others are the front ends; this is the execution layer, and merging it
				// into any of them would charge one layer's reds to another layer's ceiling.
				execFail++
			case StratumValidate:
				// **A fifth, and the boundary is causal rather than numeric** (#9 slice 1,
				// #291): a row is here when *validation is the layer that decided it*, not
				// when it came out of an `assert_invalid` vector. Three populations look like
				// this column and are not it, each held by an existing ceiling: the 11
				// encoder-frontier refusals (StratumEncode, which never reach the type
				// checker), the decoder's own answers on encoder output (StratumEncode too,
				// see validateModule), and the `assert_return`s whose module never validated
				// at all (StratumExec — 0025's carve-out, retiring by migration one slice at
				// a time rather than by a flag day).
				validateFail++
				// The stratum's own three-way split, from the arm's own flags rather than from
				// arithmetic over two of them. `default` is the wrong-message case and is the one
				// arm here with no flag of its own: it is what a validate-stratum failure is when
				// the component neither declined to answer nor accepted — an honest refusal whose
				// text the corpus disagrees with (0003).
				switch {
				case f.Declined:
					validateDeclined++
				case f.Accepted:
					validateAdmitted++
				case f.OverRejected:
					validateOverRejected++
				default:
					validateMismatched++
				}
			case StratumUnset:
				t.Errorf("failure at line %d (%q, kind %v) carries no stratum — the site "+
					"that reported it did not say which component failed, so its red would "+
					"be charged to whichever ceiling this switch defaulted to",
					f.Line, f.Expect, f.Kind)
			default:
				t.Errorf("failure of unhandled stratum %v at line %d (%q) — a new Stratum "+
					"was added without a ceiling under it", f.Stratum, f.Line, f.Expect)
			}
			if f.Kind == KindUnsupported {
				t.Errorf("a KindUnsupported command produced a failure bucket entry at "+
					"line %d (%q); unsupported commands are not scored, so this is the "+
					"run loop losing track of a verdict", f.Line, f.Expect)
			}
		}
	}
	if binaryFail+textFail+encodeFail+execFail+validateFail != totalFail {
		t.Errorf("fail partition sums to %d but the column is %d; a failure escaped every "+
			"arm, so one of the five ceilings below is watching a subset it cannot name",
			binaryFail+textFail+encodeFail+execFail+validateFail, totalFail)
	}
	if validateDeclined+validateAdmitted+validateMismatched+validateOverRejected != validateFail {
		t.Errorf("the validate stratum's four parts sum to %d but the stratum is %d; a "+
			"validate-stratum failure is declined, admitted, over-rejected, or wrong-message, so a "+
			"residual here is a fifth outcome with no ceiling under it",
			validateDeclined+validateAdmitted+validateMismatched+validateOverRejected, validateFail)
	}
	t.Logf("  fail by stratum: binary %d, text %d, encode %d, exec %d, validate %d "+
		"(%d declined + %d admitted + %d over-rejected + %d wrong-message)",
		binaryFail, textFail, encodeFail, execFail, validateFail,
		validateDeclined, validateAdmitted, validateOverRejected, validateMismatched)

	// **0 at the measured revision, and it was 1 for the whole life of this ceiling.**
	// The one member was binary-gc.wast:1, reported as "malformed function type: 0x5e"
	// under an expected "malformed mutability" — an *array* type read as a malformed
	// functype, because the type section decoded `functype` where the reference decodes
	// `rectype` (#86). Unmoved by the wat reader and unmoved again by #62's parser, which
	// is what the split column was for and what made this one durable enough to diagnose.
	//
	// It is 0 rather than gone because the ceiling's job is now the other direction:
	// **both columns are zero, so any new fail in either is a regression by definition**,
	// and a ceiling at 0 is the strongest form of that claim. Printed, not deduced:
	// `binaryFail=0 textFail=0` with the fix, `1` and `0` without it — the falsification
	// pass ran both ways, because a ceiling lowered to a number the code already meets
	// asserts nothing (which is how this ceiling was briefly wrong at 1 in #83).
	//
	// The vector itself is now a *gated* verdict on the default board, allowlisted in
	// TestGatedVectors with the feature named, and **passed** in the
	// all-gates-on lane, where 4178 pass / 0 fail / 0 gated.
	// Slack 0, and deliberately: a ceiling *at* zero cannot go stale, because the distance
	// between "at most 0" and "0" is not a quantity that can silently grow. That is not an
	// omission to be repaired by a future reader — see 0013 — and it is stated because
	// silence about a member of the space is how #48 happened.
	const binaryFailCeiling = 0
	boardBound(t, "binaryFailCeiling", binaryFail, binaryFailCeiling, 0, ceilingBound,
		"a defect landed in the binary decoder; this ceiling is deliberately not shared with "+
			"the text column so that a decoder regression cannot hide inside the text column's "+
			"unwritten grammars")

	// **391 at the measured revision, down from 600 when the module-field parser landed
	// (#62).** This is a **work plan with a ceiling** rather than a defect count: every
	// member is a vector the reader reached and could not answer, bucketed by the spec
	// string that names what is missing. It may only fall, and it falls as the remaining
	// strata land. The buckets printed above are the order to take them in.
	//
	// The fall is 209 vectors, itemized rather than absorbed: 176 `malformed UTF-8
	// encoding` (the whole bucket, at the `name` production), 12 `import after
	// {function,memory,table}`, 10 `duplicate {memory,local,table,func,global,field}`, 6
	// `unexpected token`, 1 `malformed UTF-8` (id.wast:31, the `var` site) — 210 answered
	// against 1 newly withdrawn, `comments.wast:83`, whose unearned pass the parser
	// replaced with a named boundary (see TestBareQuoteFormsPassUnearned). Net 209.
	//
	// Against #62's pre-registered 221–225 that is an **under-delivery of 12–16**, and the
	// cause is one shape rather than a spread: seven vectors whose check this stratum
	// implements sit behind an instruction body in the *same module*, so the check is never
	// reached — `imports.wast:692,696,700,704` (`import after global`, all four with a
	// `(global i32 (i32.const 0))` initializer ahead of the import), `global.wast:685`
	// (`duplicate global`), `start.wast:102` (`multiple start sections`), `func.wast:447`
	// (`unknown type`). They arrive with #63/#64's instruction grammar at no extra cost to
	// it, and they are the concrete form of the forecast's own caution — the classifier
	// asked whether a *vector* was instruction-bodied, and the question that decides
	// reachability is whether the *module* is.
	//
	// # 97 after #63's flat instruction grammar, and the 294 itemized
	//
	// **391 → 97 text, the fall being 294 vectors.** Six buckets emptied and one shrank, and the
	// account is per-bucket rather than net, measured by re-running the board at 1f86fa1 and
	// diffing the bucket tables:
	//
	//	92  alignment                          → 0   memarg's `align=`, the largest single win
	//	84  unexpected token                   146 → 62
	//	55  constant out of range              → 0   constImm at all four widths
	//	22  alignment must be a power of two   → 0
	//	15  i8 constant out of range           → 0   laneidx, nat8-reduced
	//	 8  wrong number of lane literals      → 0   vecConst
	//	 5  wrong number of lane indices       → 0   laneIdxList
	//	 4  import after global                → 0   ┐ four of the seven #62 predicted would
	//	 1  multiple start sections            → 0   │ arrive here free; `func.wast:447`
	//	 1  duplicate global                   → 0   ┘ (`unknown type`) is the one that did not
	//	 1  (module quote ...) must read        → 0   comments.wast:83, the retired pass returning
	//
	// **Against #63's pre-registered 353 that is an under-delivery of 59**, and unlike #62's it
	// is *not* one shape. Partitioned by mechanism from the engine's own error text — the failure
	// bucket keys name what the suite wanted, which is the wrong key for asking why we did not
	// deliver:
	//
	//   - **92 are `unimplemented: instruction body`, i.e. #64's**, and they are the forecast's
	//     real error. The 353 was a count of vectors whose *fault* lives in one of #63's readers;
	//     92 of them reach that fault only through a `block`/`loop`/`if`/`try_table` this stratum
	//     does not read. Same defect ownership, unreachable extent — the seam ruling put the
	//     `expr1` minimal arm here for exactly this reason, and it was not enough because folded
	//     *plain* instructions are #63's while folded *block* instructions are #64's.
	//   - **5 need a type context**, which is neither stratum's: `func.wast:601,608,615` and
	//     `:447` want `(type $sig)` compared against inline params/results
	//     (`inline_functype_explicit`, parser.mly:246) and a type index space to resolve `(type 2)`
	//     against. These are the five accept-direction members of the column — we accept modules
	//     the reference rejects — and they are the reason the fall is 294 and not 299.
	//   - **1 is the decoder's**, `binary-gc.wast:1`, held by binaryFailCeiling above.
	//
	// So the honest reading is that #63's forecast measured its own extent correctly and its
	// *reachability* wrongly, in the same direction #62's did and for a differently-shaped
	// reason. Recorded rather than smoothed: the 92 are #64's inventory, and #64's own forecast
	// starts from them rather than from a fresh classification.
	//
	// # 79 after the flat block family, and the "92 are #64's" above is corrected
	//
	// **97 → 79, the fall being 18 more vectors and the account correcting the one above.** The
	// paragraph naming 92 as #64's inventory was wrong, and it was wrong in the way a reconciliation
	// most easily goes wrong: it read the *surface* of the unanswered vectors instead of checking
	// them against the owning issue's Scope list. #63's Scope names `blockinstr` (parser.mly:726),
	// the block family (:740-:792), `labeling_opt` (:510) and `labeling_end_opt` (:521), and the
	// seam ruling moved the `expr1` minimal arm *in* — it moved nothing out. So the **flat** block
	// forms were always #63's, and the forecast's own table said so: it has a "17 flat" row.
	// Measured rather than re-argued, by classifying each unanswered vector on whether its boundary
	// token is a block keyword or a `(`:
	//
	//	flat boundary    17     #63's, and they were still red
	//	folded boundary  75     #64's, genuinely
	//
	// Landing `blockinstr` answered the 17 exactly, in two buckets:
	//
	//	14  mismatching label   → 0   labeling_end_opt, both arms (block.wast:1484/:1488 et al)
	//	 3  unexpected token    56 → 53  block/loop/if `(param $x …)`, the named-form grave
	//
	// Plus **1 more the controls found rather than the board**: `try_table.wast:366` and `:371`
	// (`(func (catch_all))`, `(func (catch $e))`) were reporting the *boundary* for a clause no
	// production admits in instruction position — 51 rather than 53 in the `unexpected token`
	// bucket, and the general form of that defect is #70.
	//
	// **The corrected partition of what remains, itemized from the engine's own error text:**
	//
	//	75  unimplemented: instruction body   #64's folded arms — the real inventory
	//	 5  accepted (no error at all)         the type context, neither stratum's
	//	 1  malformed function type: 0x5e      binary-gc.wast:1, the decoder's
	//	--
	//	81  … then 79 after the two try_table vectors
	//
	// **Against the 353 the shortfall is now 42, not 59**, and the whole difference is the 17 this
	// paragraph reassigns. The lesson is the one the seam ruling already stated and the
	// reconciliation then ignored: *seams follow defect ownership, not surface form*. Reading a
	// bucket's members off their spelling is the same manoeuvre as reading a test's coverage off its
	// case labels, and it produced a number that was wrong by exactly the vectors the issue was
	// chartered to fix. Check the Scope list, not the mnemonics.
	// # 67 after the derived instruction boundary (#70)
	//
	// **79 → 67, and the 12 are the correction of a "zero vectors" claim I made from five
	// hand-picked probes.** #70's issue said "board effect: none" on the strength of trying five
	// spellings at a Go prompt and generalizing; the figure was wrong by 12, and it was wrong in
	// the way the *cheap-is-a-grammar-claim* rule names: a board figure asserted without the
	// board. What the five probes could not reach is the class itself — **reachable keywords in
	// an unreachable position**. Every one of the 12 leads with a keyword the parser knows
	// (`type`, `param`, `result`, `local`), which is exactly why hand-picking failed: the
	// interesting inputs are not exotic tokens, they are ordinary ones where no instruction may
	// start, and you do not stumble onto those by inventing spellings.
	//
	// The 12, all `unexpected token`, all in func.wast, itemized from the engine's own text:
	//
	//	 6  :559 :566 :573 :580 :587 :594   type-use ordering — `(func (type $sig) (result i32)
	//	                                    (param i32) …)` and its five permutations
	//	 6  :937 :941 :945 :949 :953 :957   field-after-body and misordered fields — `(func (nop)
	//	                                    (local i32))`, `(func (local i32) (param i32))`, …
	//
	// They answer because `func_body` is `instr_list` (parser.mly:1017), which cannot begin with
	// `(param`/`(local`/`(result`/`(type`. The old boundary asked "is this `(` followed by a
	// handler clause?" and said `unimplemented` to everything else; the derived boundary asks
	// "can an instruction start here?" — `startsInstruction`, the union of `plaininstr`'s
	// mnemonics and `expr1`'s nine non-plain arms — and a `(param` in instruction position is
	// now `unexpected token` on the merits. Position-dependence comes free: the boundary only
	// runs where an instruction was *required*, so `(func (param i32))` still parses.
	//
	// **Nothing was withdrawn and no bucket grew**, checked per file rather than on the total:
	// the only line that moved was `func.wast: 6/23 → 18/23`. `unexpected token` 51 → 39;
	// `inline function type` 24, `unknown label` 2, `malformed mutability` 1, `unknown type` 1
	// all unmoved. What remains in func.wast is 4 `inline function type` and 1 `unknown type`,
	// both #64's.
	// # 28 after the folded/sugar stratum (#64, first half)
	//
	// **67 → 28, and the 39 that fell are the folded spellings of a grammar that was already
	// right.** Every `unexpected token` in block/loop/if/call_indirect/return_call_indirect went to
	// zero; the five files moved 3/15→11/15, 3/15→11/15, 11/24→20/24, 0/11→7/11, 0/11→7/11.
	// Nothing withdrawn, checked per file rather than on the total — the only lines that moved are
	// those five, all upward.
	//
	// The sequencing forecast I posted on #64 said **41** and the measurement says 39. The two
	// missing are `token.wast:101` and `:117` (`$l0`, `$l$l` in a `br_table`), which the folded
	// reader now *reaches* and which turn on name **resolution** rather than grammar — they are
	// `unknown label`, and they belong with the 24 below rather than here. A forecast wrong by two
	// in the direction of "I mistook a resolution question for a syntax one" is the same error the
	// #64 partition made twice at larger scale; recorded rather than rounded.
	//
	// **The residue is 28 and every one of them is a semantic question, not a grammatical one:**
	//
	//	24  inline function type   exactly 4 each in block, loop, if, call_indirect,
	//	                           return_call_indirect and func — a six-file × four grid, which is
	//	                           the tell that one production is responsible:
	//	                           `inline_functype_explicit` (parser.mly:238) compares an inline
	//	                           signature against the explicit `(type n)` and needs the type
	//	                           section resolved plus functype equality. #64's second half.
	//	 2  unknown label          token.wast:101/:117 — `$l0` and `$l$l` lex as one VAR, so the
	//	                           label does not resolve. Name resolution rather than grammar.
	//	 1  unknown type           func.wast:456 — `(func (type 2) (param i32))` where the module
	//	                           defines fewer types. The file holds four `unknown type` vectors
	//	                           and this is the only one in a `(module quote …)`; the other three
	//	                           (:444, :632, :640) are `assert_invalid` on real modules, which
	//	                           the text reader is never handed. Read from the file rather than
	//	                           from the bucket, because a bucket count of 1 against a grep count
	//	                           of 4 is exactly where a citation goes wrong.
	//	 1  malformed mutability   binary-gc.wast:1 — the *decoder's*, not the parser's.
	//
	// The `unimplemented: instruction body` bucket is at **zero**, and that is what retired the
	// boundary itself: with all four `instr_list` arms and all three `instr1` arms read, an
	// `unimplemented` promises a reader nobody will write. See internal/text's expectedInstr for
	// the sweep (493 of 494 admitted leaders consumed) and TestNoInstructionLeaderIsUnread for the
	// re-pointed tripwire.
	//
	// **Pre-registered: the next PR takes this to 3.** The 24 plus the 2 plus the 1 are one job —
	// `typeuse` resolution — leaving only binary-gc.wast:1, which is the decoder's. Stated here so
	// the claim is falsifiable before the work rather than after it.
	//
	// (That pre-registration is **unmet and deferred, not missed**: #69 came first, for the
	// reason in the next block — resolution is a *rejector*, and installing one under a
	// 7-vector accept oracle is the overfitting risk in its purest form. The 27 named above
	// are all still here, unmoved, and the forecast stands for #64's second half.)
	//
	// # 41 after the bare-module admission (#69), was 28
	//
	// **+13, and the forecast was wrong by an order of magnitude in the pessimistic
	// direction.** Pre-registered at 150–400 fails of the then-known 1119, centred near 250,
	// on the reasoning that a parser built against reject vectors plus *seven* accept vectors
	// would over-reject badly — grave #63 (flat `select`, every module containing one
	// rejected, invisible to the board) being exactly what that produces. The measurement is
	// **13 of 2152**. The parser accepts 2130 valid modules it had never been scored against.
	//
	// Recording the miss rather than quietly enjoying it, because the *reasoning* was sound
	// and the conclusion was still wrong: reject-direction construction predicts
	// over-rejection, and the honest lesson is that #62/#63/#64 tracked the reference's arm
	// lists closely enough that following the grammar bought the accept direction too. Also
	// note 1119 → 2152: #69's figure counted bare modules in *board* files, and the corpus
	// holds 2152 once the newly-admitted files are included.
	//
	// **The 13, partitioned by mechanism rather than quoted as one number** — two v0
	// grammar defects and one phase-v3 form, all accept-direction, none of them visible
	// before this admission:
	//
	//	 9  `lane_imms`, parser.mly:661   simd_{load,store}{8,16,32,64}_lane.wast:4 and
	//	   (#76)                          simd_memory-multi.wast:5. `v128.load8_lane 0 (…)` —
	//	                                  our memarg reads the leading `0` as a memory index,
	//	                                  but it is the bare `| laneidx` arm (:673). The
	//	                                  reference multiplies the production out into five
	//	                                  arms "to avoid spurious conflicts"; telling them
	//	                                  apart needs lookahead for a *second* nat.
	//	 3  `elem_list`, parser.mly:1155  elem.wast:539/:573 and array.wast:219. `(elem (ref
	//	   (#75)                          $b) …)` — the offset-sugar branch tests
	//	                                  `at(LParen) && !peek2Keyword(kwItem)`, which claims
	//	                                  a `(ref …)` is an offset and shadows the `reftype
	//	                                  elemexpr_list` arm entirely.
	//	 1  annotations.wast:1            `empty annotation id` — the lexer's, on a module
	//	   (#83)                          whose first field is `(@a …)`.
	//
	// The two grammar defects are engine fixes and are **not** in this PR: #69 is
	// board-shape work and travels alone, per *board-shape changes travel as their own
	// decisions*. They are the work plan this admission exists to produce, and each is
	// filed with the arm it misreads.
	//
	// **The partition above is the corrected one, and the correction is the lesson.** It
	// first read 10 / 2 / 1, with the tenth lane vector hedged as "one further `simd_*_lane`
	// file". There is no tenth: the vector is `array.wast:219`, a `(elem $e (ref $bvec) …)`
	// in a GC module, and it belongs to `elem_list`. Confirmed by running the reader —
	// `(module (type $b (array i8)) (elem $e (ref $b) (ref.null $b)))` errors while `(elem $e
	// func)` returns nil — after *printing the bucket's members*, which is what should have
	// produced the partition in the first place.
	//
	// *Bucket size estimates the reward, not the job* says partition by mechanism before
	// estimating. The failure here was one level in: the partition was made by mechanism, but
	// the file set for each mechanism came from memory of where the defect lived rather than
	// from the board. That is *derive the domain, never enumerate it* applied to the work plan
	// instead of to the engine — and an enumerated file set has a blind spot exactly the shape
	// of the defect one did not know was shared. Print the bucket; do not recall it.
	//
	// # 16 after typeuse resolution and functype equality (#64, second half), was 41
	//
	// **−25, and the forecast was met exactly** — pre-registered on #64 as 41 → 16 before the
	// work, itemized as 24 `inline function type does not match explicit type` plus 1 `unknown
	// type`, with the 2 `unknown label` vectors excluded as a separate mechanism. Both numbers
	// landed on the nose, which is worth stating plainly *and* discounting: the forecast was
	// made after the bucket had been printed and partitioned, so it predicted the size of a set
	// whose members were already known. An exact hit there is bookkeeping, not foresight. The
	// forecasts worth crediting are the ones over unlisted spaces, and this PR's two of those
	// were both wrong (see below).
	//
	// The 25, by file, from a per-file diff against the pre-change board — six files moved, all
	// upward, none losing a pass:
	//
	//	block.wast                12/16 → 16/16   +4
	//	call_indirect.wast        10/14 → 14/14   +4
	//	func.wast                 22/27 → 27/27   +5
	//	if.wast                   21/25 → 25/25   +4
	//	loop.wast                 12/16 → 16/16   +4
	//	return_call_indirect.wast 10/14 → 14/14   +4
	//
	// **The residue is 13 + 2 + 1 and none of it is this mechanism's**: 13 in `(module <wat
	// body>) must read` (#75's 3 `elem_list` and #76's 9 `lane_imms`, plus annotations.wast:1
	// which is #83's, the annotation lexer's), 2 `unknown label` (token.wast:101/:117 — `enter_block` and scoped
	// labels, parser.mly:132-134, deliberately its own PR), 1 `malformed mutability`
	// (binary-gc.wast, the decoder's). So the pre-registration two blocks up — "the next PR takes
	// this to 3" — is met on its own terms: the 24 + 2 + 1 it named as one job turned out to be
	// 25 + 2, the two labels being a different mechanism, and the 13 arrived in between from #69's
	// admission. The 3 it forecast is now 3 = 2 labels + 1 decoder, with #75/#76's 13 alongside.
	//
	// **`non-function type <n>` is implemented and corpus-unreachable.** Zero vectors: measured
	// across the whole corpus, no `assert_malformed` names it, because reaching it needs a struct
	// or array type used as a typeuse with an inline signature and the GC files' subtyping
	// vectors are all `assert_invalid`. It is implemented anyway — it is one of `func_type`'s
	// three outcomes (parser.mly:164-168) and omitting it would report `unknown type` for an
	// index that resolves perfectly well, which is the engine lying about its input. Pinned by a
	// print check (TestNonFunctionTypeMessage) rather than by the board, per *the oracle reads
	// exactly as far as its expected string does*.
	//
	// **Two forecasts over unlisted spaces, both wrong, both in the same direction.** (1) The
	// nesting order of implicitly-interned block types was written into a comment on
	// `orderedTypeUse` as inner-before-outer, correctly, and the code did outer-before-inner —
	// `blockSignature` passed a nil tail so its three callers read the body *after* the signature
	// op was recorded. Caught by a synthetic control, and the board is identical either way. (2)
	// `externtype` was assumed to compare an inline signature against its typeuse; its arms are
	// `typeuse` XOR `functype` (parser.mly:1226-1248), so two test rows asserting a mismatch
	// error there failed against a parser that was right. Both were found by *printing what the
	// code returns* rather than by reasoning, which is the same instrument that found #70's 12.
	//
	// Eleven controls in internal/text/typetable_test.go, each falsified by introducing the
	// defect it names; **nine of the eleven defects leave this board unchanged**, the two
	// exceptions being an over-rejecting resolver (4147 → 4145, imports.wast) and the block arms
	// wired to the create-helper (4147 → 4135). The table is in that file's header. That ratio is
	// the honest measure of what this bucket's fall is evidence for.
	//
	// # 7 after lane_imms' bare laneidx arm (#76), was 16
	//
	// **−9, and the forecast said 10.** #76 named eight `simd_{load,store}{8,16,32,64}_lane.wast`
	// files plus `simd_memory-multi.wast` — nine — and then hedged with "(one further `simd_*_lane`
	// file; the exact set is printed by the bucket)". There is no tenth: the per-file diff shows
	// exactly those nine going 0/1 → 1/1 and nothing else moving. The hedge was the error, and it
	// is the honest kind — the issue said the set was to be *printed*, and printing it is what
	// settled the count. A forecast that names its own oracle is falsifiable by consulting it.
	//
	// Residue 7 = 4 `(module <wat body>) must read` + 2 `unknown label` + 1 `malformed
	// mutability`. The 4 are `annotations.wast:1` (#83's), `array.wast` 1 and `elem.wast` 2
	// (#75's `elem_list` reftype arm) — so #75 is the whole remainder of that bucket, and the
	// bucket falls to 1 when it lands, exactly as #76's definition of done predicted.
	//
	// # 4 after elem_list's shadowed reftype arm (#75), was 7
	//
	// **−3, and the forecast said 2.** #75 named `elem.wast:539` and `:573`. The third,
	// `array.wast:219` — `(elem $e (ref $bvec) …)`, a reftype naming a *defined* type rather than
	// `func` — was in the same bucket the whole time and was not listed, because the bucket key is
	// the expected spec string and the string says nothing about which arm broke. Found by printing
	// every failing module's error rather than by trusting the issue's list: *bucket size estimates
	// the reward, not the job*, and this is the same lesson from the other side — a partition can
	// be finer than the issue that named it, not just coarser.
	//
	// The bucket is now **1** (`annotations.wast:1`, #83's), which is what #75's and #76's
	// definitions of done both predicted, from opposite ends of the same 13.
	//
	// Residue 4 = 1 `(module <wat body>) must read` + 2 `unknown label` + 1 `malformed mutability`.
	// **Nothing left in the text bucket is a typeuse, lane or elem question**: the labels are
	// `enter_block` and scoped labels (parser.mly:132-134), the mutability one is the decoder's, and
	// annotations is the lexer's.
	//
	// # 2 after symbolic label resolution (#80), was 4
	//
	// **−2, forecast 2, and this one was made against a printed set rather than a guessed one.**
	// `token.wast` 59/61 → 61/61 and the `unknown label` bucket closes. What made the forecast safe
	// is the measurement in #80: matching every `unknown *` vector's module body for a symbolic name
	// in a use position the same module never binds returns **exactly two rows across all 253
	// files**, both of them these. So the fix's reach was known before it was written, and the two
	// vectors are the whole population rather than a sample of it.
	//
	// That measurement is also what kept the change small. Read literally the reference resolves
	// *every* symbolic index in the parser — the lookup category is a parameter of `idx`
	// (parser.mly:487-489) and all 83 `plaininstr` arms supply one — which would have made this a
	// job spanning nine index spaces. The suite says only labels are reachable: numeric indices are
	// `nat32 $1` with no lookup, so all 13 `assert_invalid "unknown label"` vectors are `(br 1)`-
	// shaped and validation's, and the remaining names (`global.wast:668`'s forward `$g2`) are bound
	// later in the module and need #64's deferred phase. Labels are the one space whose scope is
	// *lexical*, so they resolve where they are read. Splitting at that seam is what made the
	// reachable half reachable now.
	//
	// **The residue is zero, and this ceiling is now the assertion that it stays there.** The
	// board's remaining fail is `malformed mutability` in `binary-gc.wast`, which is a
	// `(module binary ...)` vector and therefore charged to `binaryFail` — so *no* failing
	// vector on the board is a text-kind command any more.
	//
	// The ceiling was first written as 1 here, reasoning from the board's total of one fail.
	// That is the wrong quantity: this ceiling counts the *text* partition, and the one
	// survivor is in the other one. Caught by the falsification pass rather than by reading —
	// reverting #83's fix left `textFail` at exactly 1, so a ceiling of 1 sat green over the
	// defect it was being lowered to catch. **A ceiling is a claim about a partition, so it is
	// read off the partition, not off the total** — the same error as scoping a control to the
	// sample instead of the space, one aggregation level up. Printed, not deduced: `textFail=0
	// binaryFail=1` with the fix, `1` and `1` without it.
	//
	// **The residue was 2, and the second one was ours after all.** `annotations.wast:1` was
	// attributed to the lexer and dismissed as somebody else's for three PRs — the sentence above
	// said "neither is the text parser's" and it was half wrong, because the vector *is* this
	// package's and the attribution was to an issue number about the CHANGELOG. Grave #83. The
	// number was never checkable: this file's provenance guards resolve a `.wast:N` citation
	// against the suite, and nothing resolves an issue number to its subject, so a bare `#NN` in
	// prose is exactly the drifted-citation defect with the machine-checked half removed. It got
	// quoted forward five times here and three times in the changelog because quoting is cheaper
	// than checking. **A ceiling that names the residue is asserting a diagnosis, and a diagnosis
	// is falsifiable** — this one was falsified by running the vector, which took one probe.
	// Slack 0 for the same structural reason as binaryFailCeiling above.
	const textFailCeiling = 0
	boardBound(t, "textFailCeiling", textFail, textFailCeiling, 0, ceilingBound,
		"either the reader regressed on vectors it used to answer, or the corpus moved")

	// # The encoder's ceiling — 4693, after section 11 took 9082 out of it
	//
	// **This column exists because the interpreter's arrival created it, and it is the reason
	// Stratum replaced Kind as the partition key.** Every member is an `assert_return` whose
	// module `text.EncodeModule` could not emit, so the vector reaches the interpreter with no
	// instance to run against. The command is an assert_return; the defect is the encoder's.
	// Charged by Kind, all 13775 it held on the day it was born would have landed on `execFail`
	// and reported the interpreter as 40× more broken than it is.
	//
	// A **work plan with a ceiling**, not a defect count — the same shape textFailCeiling had
	// at 391. The buckets printed above are the order to take them in, and they are keyed by
	// the encoder's own message, so the column reads as #8's instruction work list:
	//
	//	1162  call_indirect               1087  block                 620  loop
	//	 420  if                           405  ref.null              318  select
	//	 221  table.init                   201  memory.init            42  return_call_indirect
	//	  41  br_table                      23  array.copy             23  array.init_data
	//	  17  ref.cast                      17  i8x16.extract_lane_s   14  (global …) field
	//	  12  (start …) field           …and 20 more, all #8, plus 3 for #77
	//
	// **The `(data …)` bucket is gone, and section 11 is what removed it** (13775 → 4693). It led
	// this list at 8882 with `(memory …)` behind it at 212, and both are absent: the emitter now
	// writes the data section, the data count section, and the memory field's inline-data sugar.
	// The 9082 is 8882 + 212 − 12, the twelve being modules whose *second* frontier is also in a
	// module field — which `code.go:195` pre-registered as the reason a per-shape census is
	// upper-bound-shaped, and which the memarg emitter's 199 demonstrated in the other direction.
	//
	// **Where the 9082 went matters more than that it left, and it did not become passes.** 8272
	// became `execFail` and 810 became `gated`; **zero became `pass`.** A `(data …)` module that
	// now encodes still needs a *load arm* to answer its `assert_return`, so the queue moved one
	// stratum down rather than draining — `interp: no arm for opcode 2d` alone holds 8001 of it.
	// Stated at the site because a ceiling falling by 9082 while the pass count does not move is
	// exactly the shape a reader would otherwise mis-read as progress.
	//
	// **Separate from textFailCeiling, and the separation is load-bearing.** `text.ReadModule`
	// answers all 254 files' module forms with **0** reds; `text.EncodeModule` cannot emit most
	// instruction bodies. Folding them would take the reader's ceiling from 0 to 4693 and
	// destroy the only instrument watching the reader for regressions — one instrument per
	// component, or neither is an instrument. They are two entry points in one package, which
	// is exactly the case a Kind-keyed partition cannot express and a stated stratum can.
	//
	// Slack 0 like the two above: it may only fall, and it falls as #8 lands.
	//
	// # Re-based 4693 → 4909, and a slack-0 ceiling rising is a claim that needs a diagnosis
	//
	// This bound says the encoder may only lose ground, so its own message read the rise as an
	// encoder regression. It is not one, and the way to tell is to **partition the column by the
	// Kind that produced it** rather than to trust either the message or this comment:
	//
	//	assert_return  4693   ← unchanged, to the vector
	//	invoke          216   ← a population that did not exist before this PR
	//
	// The `assert_return` half is *identical*. What grew is the denominator: #7's memory work
	// taught the harness to run a bare top-level `(invoke …)` as a command instead of walking
	// past it (KindInvoke), so 216 more commands now meet the encoder's **unmoved** frontier and
	// are honestly charged to it. Nothing the encoder used to emit stopped being emitted.
	//
	// Rebased rather than exempted, and the ceiling keeps slack 0 — but note what the instrument
	// could and could not say. It fired correctly (the number rose), and its *stated reason* was
	// wrong (nothing regressed), because a ceiling can express "may only fall" and cannot express
	// "may only fall per vector asked". A bound over a growing population is measuring two things
	// at once; the Kind split above is the one that answers the question this bound was for, and
	// it is written here as the check to run the next time this rises rather than as a fact about
	// this rise. Same shape as the exec ceiling's own note that it would rise as #8 landed.
	//
	// # 4909 → 994, and this is the fall the slack-0 direction exists to accept
	//
	// The bulk-segment pair (#8) is the largest single drain this column has taken: **1781
	// departures, 0 arrivals**, set-differenced on `(file, line)`, against the intermediate
	// revision this PR is measured from (2775). Every departing vector quoted one of exactly two
	// refusals — `cannot yet encode memory.init (#8)` and `cannot yet encode table.init (#8)` —
	// and the retirement of those two strings is the whole of the movement. The Kind partition
	// the note above asks for is not needed here because the direction is down and the population
	// did not grow; what replaces it is the departure/arrival split, which is the same question
	// (did anything come *in*) asked of a falling column.
	//
	// Where the 1781 went is the cross-check the exec ceiling holds the other end of, and it is
	// written in *flows* rather than in column deltas because those are different numbers here:
	// **1453 became passes, 244 became `gated`** (TestGatedVectors' new allowlist entries, all 244
	// of them encode-column fails at the parent), **and 84 moved to the exec column** — 1453 + 244
	// + 84 = 1781, nothing lost. The exec column's *net* is +79 rather than +84 because five of its
	// own members left in the same motion (the two drop arms), and those five are why the pass
	// column reads +1458 against this bucket's 1453. A drain quoted against net column movement
	// would have been off by exactly that five and looked like it closed anyway, since 1458 + 244 +
	// 79 also sums to 1781. That coincidence is the reason the flows are written out: two wrong
	// terms summing correctly is not a check.
	//
	// # Re-based 994 → 1017, and this is the check the 4693 → 4909 note said to run
	//
	// The rise is **0 departures, 23 arrivals**, set-differenced on `(file, line)` — and all 23
	// arrivals are `KindRegister`, a population that did not exist before this PR. That is the
	// precedent above repeating exactly: a `(register "M" $M)` whose `$M` is a `(module …)` the
	// encoder cannot emit now *asks* the encoder a question it was previously never asked, and is
	// honestly charged here. Nothing the encoder used to emit stopped being emitted, which is what
	// "0 departures" says and what the ceiling's own message cannot.
	//
	// Written this way because the note above pre-registered the check as *"the check to run the
	// next time this rises"* — the Kind split, plus departures separately from arrivals — and a
	// rebase that quoted only the net +23 would have been indistinguishable from an encoder losing
	// 23 vectors it used to emit. Slack stays 0.
	//
	// # 1017 → 517, decision 0021's encoder-side implementation
	//
	// **-126 departures, 0 arrivals** on the "a struct or array type's fields are not retained"
	// bucket — exactly #183's co-blocking probe's own count (117 + 6 + 3, the `no instance`/
	// `register` split the probe carried). Every one lands as either an honest GC decline
	// (`TestGatedVectors`' new allowlist entries: `ref_eq.wast`, `array_fill.wast`, `array.wast`,
	// `table_init.wast`/`table_init64.wast`, `type-rec.wast`, six new entries in
	// `type-subtyping.wast`) or a real pass in the all-gates-on lane — never a different fail —
	// because the struct/array frontier no longer exists; what remains gated is GC's own gate,
	// unrelated to this decision. `struct.wast`'s 18 fails are untouched (a different, still-open
	// frontier — `struct.get`/`struct.get_s` instruction immediates, #183's two-blocker chain) and
	// are why this column does not reach 0. Slack stays 0.
	// # 517 → 46, and 471 of the 481 is drift that grave #293's exemption hid
	//
	// Seven movements after 517 was set, every one of them silently excused, attributed by walking
	// this stratum line one checkout per merge from `9e0aa9d` (the commit that both set 517 and
	// measured it):
	//
	//	9e0aa9d   encode 517   the constant was correct here
	//	88053d2   encode 347   struct.get/struct.set/array.* immediate retention (#189)
	//	f6ba248   encode 341   symbolic field-name resolution (#190)
	//	30de899   encode 136   ref.test/ref.cast/br_on_cast* retention (#191)
	//	a7da5c1   encode 150   *a rise*: the harness began passing reference arguments (#198)
	//	5724e2c   encode  82   try_table's catch clauses and (tag …) fields (#200)
	//	b907024   encode  36   v128.const/shuffle/extract_lane/lane loads (#212 era)
	//	  … flat at 36 for the following 24 merges, through HEAD
	//
	// The `a7da5c1` row is why the walk was worth the checkouts rather than a bisect for the
	// minimum: this column **rose** by 14 in the middle of the drain, which a ceiling doing its job
	// would have reported as new encoder frontiers being asked new questions. It was invisible for
	// the same reason the falls were.
	//
	// **This PR's own contribution is +10, 36 → 46**, and it is the one part of the delta that is
	// mine: `assert_invalid` vectors now reach `text.EncodeModule`, and ten of them quote a frontier
	// it cannot emit. They are honest encode-column fails — the module never reached the validator,
	// so the validator's columns must not claim them. Their siblings are the 18 that *pass* from
	// above the validator (TestAssertInvalidPassesFromAboveTheValidator); the difference between the
	// two populations is only whether the refusal happened to quote the vector's expected string,
	// which is why both are named rather than either being read as a verdict about this stratum.
	// # 46 → 68, and the +22 is the same shape as the +10 above, one command class over
	//
	// Module-definition commands ask the validator since #341, so they reach `text.EncodeModule`
	// too, and 22 of them quote a frontier it cannot emit: 14 `(table …)` fields and 6 `(start …)`
	// fields (#8), one symbolic local under a typeuse (#77), and `memory-multi.wast:5`'s `unknown
	// data segment $d`. Pre-registered on #341 with the row list before the arm changed.
	//
	// **They are the encoder's and not the validator's, and the row that proves the boundary is
	// real is `func.wast:459`** — a module the type checker would have judged, refused two steps
	// earlier by the emitter. Charging these to `validateFail` would raise #9's column by 22 rows
	// the type checker never saw, which is the confusion `validateModule`'s own comment names and
	// this ceiling exists to keep separated. Slack stays 0.
	// # 68 → 40, and the −28 is one form leaving, verified by the 40 that stayed being *the same 40*
	//
	// #413 taught the emitter section 8, so every `(start …)` refusal is gone: `start.wast` 18,
	// `start0.wast` 9, `ref_func.wast:112` 1. The fall was measured rather than subtracted — the
	// stratum was dumped with file, line, kind and refusal text on both sides of the change, and the
	// pre-slice side decomposes 28 `(start …)` + 33 `(table …)` + 4 #77 + 3 `unknown data segment
	// $d` = 68, the post-slice side 33 + 4 + 3 = 40 over **the same file:line rows**. That identity
	// is the whole account: a delta of 28 with the residue unchanged row-for-row says one form
	// departed and nothing else moved, which a pair of totals could not have said. Compare the
	// previous section, which reports `6 (start …) fields` — that was the module-definition arm's
	// share of one wave, not the form's population, and reading it as the population would have
	// forecast −6.
	//
	// **The residue is now three frontiers and the largest is one issue.** 33 `(table …)` fields
	// (#8) across `table.wast` 13, `elem.wast` 9, `global.wast` 7, `i31.wast` 4; 4 symbolic locals
	// under a typeuse (#77) in `func.wast`; 3 `unknown data segment $d` in `memory-multi.wast`. Two
	// of the three are the encode work list this ceiling was built to expose, so the next fall this
	// bound should see is #8's, and it should be −33 with the other seven unchanged.
	// # 40 → 7, and the forecast above is what this move reports rather than the number
	//
	// #419 taught the emitter the `(table …)` field's initializer, and the fall is **−33 with the other
	// seven unchanged** — the pre-registered figure, to the row. The residue was dumped by line, kind,
	// expected text and refusal text, and it decomposes as 4 symbolic locals under a typeuse (#77) at
	// `func.wast:459,483,484,485` and 3 `unknown data segment $d` at `memory-multi.wast:5,22,23`: the
	// same seven rows the previous section named, still refused for the same two reasons.
	//
	// A pre-registered forecast that lands exactly is worth less than one that misses, and it is worth
	// something only if the *identity* is checked rather than the total: 40 − 33 = 7 is also what a
	// board that dropped 33 unrelated rows and gained the 33 `(table …)` ones would print. The
	// file:line residue is the check, and it is the same method the −28 above used, applied to its own
	// prediction.
	//
	// **The two that remain are not this slice's to drain and neither is #8's any more.** The `(table
	// …)` frontier was the largest member and the last one belonging to the emitter's table work; what
	// is left is #77's four (a parser-context gap, not an emitter one) and three rows whose subject is
	// a symbolic data-segment name. So the next fall this bound should see is #77's, −4, and there is
	// no longer a −33-sized member for it to see.
	// # 7 → 3, the forecast above to the row, and the residue is now a single file
	//
	// #77 binds a typeuse's params into the local index space, and the fall is **−4 with the other three
	// unchanged** — the figure the section above committed to. The identity check is the same one the −28
	// and −33 entries used, and here it is cheap enough to read straight off the per-file lines:
	// `func.wast` went 170/174 to **174/174**, so all four departures are that file's, and
	// `memory-multi.wast` still reports 1/4 pass, 3 fail, so all three survivors are that file's. No row
	// crossed between the two groups.
	//
	// **The board and this ceiling bucket the three survivors differently, and that is a fact about the
	// keys rather than a disagreement.** The board's fail list shows `no instance: unknown data segment
	// $d` twice and `module text must reach the validator` once, because it buckets by the *expected
	// spec text*; this ceiling buckets by the *refusal text*, where all three are the one refusal. Same
	// three rows, two totals, and reading either as the population is how a right figure gets
	// "corrected" into a wrong one.
	//
	// **What is left is one issue and it is not the emitter's**: three rows in `memory-multi.wast` whose
	// subject is a symbolic data-segment name (grave #130). So the next fall this bound should see is
	// −3, to zero — and a zero here is the condition this ceiling was built to make visible rather than
	// a target, because at zero the encode stratum stops being a work list and this bound stops being
	// able to catch anything at all (#87's shape, one column over).
	// # 3 → 0, the pre-registered figure, and the stratum stops being a work list
	//
	// Grave #130 is repaired — a deferred immediate now carries a *position*, so a symbolic index
	// bound after the instruction that uses it resolves in stage 2 for eight of the nine index
	// spaces — and the fall is **−3, to zero**, which is what the section above committed to. The
	// identity check is again per file: `memory-multi.wast` went 1/4 pass, 3 fail, 2 gated to
	// **2/2 pass, 4 gated**, and no other file's line moved, so all three departures are that
	// file's and there is no fourth row hiding a compensating gain.
	//
	// **The three did not all become passes, and that is the same split #419's fall had.** One
	// became a pass (the module row, which only ever needed the encode) and two became `gated` on
	// multi-memory (`assert_return`s, which need the *runtime* to hold two memories). So this
	// bound's −3 is the board's +1 pass and +2 gated, and reading the ceiling's fall as a pass
	// forecast would have over-predicted by two — see `passFloor`'s entry, which forecasts +1 for
	// exactly this reason.
	//
	// **At zero this bound stops being able to expose work and becomes a regression tripwire only**,
	// which the section above named in advance as a condition rather than a target (#87's shape, one
	// column over). What it can still catch is exact: any new encode refusal at all, since slack is
	// 0. What it can no longer do is rank the encoder's frontier, because the encoder no longer has
	// one in this stratum — the remaining text-column work is elsewhere (#8's `(table …)` frontier
	// was drained by #419, and #9's validator is a decline, not a fail). A bound at zero over a
	// column that should stay at zero is honest; a bound at zero *described* as a work list would
	// not be, so it is re-described here rather than retired.
	const encodeFailCeiling = 0
	boardBound(t, "encodeFailCeiling", encodeFail, encodeFailCeiling, 0, ceilingBound,
		"the wat encoder lost ground: either it stopped emitting an instruction it used to "+
			"emit, or the corpus moved. This ceiling is deliberately not shared with the text "+
			"column so an encoder regression cannot hide behind ReadModule's zero")

	// # The interpreter's ceiling — 8713, the whole of it #7's opcode work list
	//
	// **The first ceiling this project has had over executed code.** Every member is a vector
	// that reached `interp.Invoke` and got an error, bucketed by that error's own text, which
	// `ErrUnsupportedOp` renders with its bytes — so the column is per-opcode and reads as the
	// arms exec.go's switch still owes:
	//
	//	8001  2d i32.load8_u    89  3f memory.size   53  40 memory.grow  45  10 call
	//	  41  28 i32.load       35  2b f64.load      35  2a f32.load     32  29 i64.load
	//	  26  0f return         25  fc 03            24  fc 04          24  fc 06
	//	  23  fc 07             22  fc 00            22  fc 02          21  fc 01
	//	  19  fc 05             15  2c i32.load8_s    15  2e i32.load16_s  … 30 more, each ≤15
	//
	// **Re-based 441 → 8713 by section 11, and the +8272 is one bucket rather than a spread.**
	// `2d i32.load8_u` went 9 → 8001 by itself, because `address*.wast` and the two
	// `memory_copy*.wast` files are per-offset sweeps over a data segment — every vector in them
	// needed the data section to instantiate and needs a load arm to answer. So the largest number
	// on the board is now a single missing arm, which is the most actionable this column has ever
	// been: the next PR's product work is named by it rather than inferred.
	//
	// **Re-based 356 → 441 before that by #8's memarg emitter**, and that +85 was accounted per
	// opcode: `10 call` +43, the load/store region `28`/`2d`/`36`–`3e` +41, `40 memory.grow` +1.
	// Both re-basings were measured by diffing this column against the same corpus with the work
	// stashed — the rise this comment's next paragraph predicted, arriving twice for the reason it
	// predicted, and every member of it is `no arm for opcode`, which is #7's frontier and not a
	// wrong answer.
	//
	// Two things this ceiling is *not*. It is not the interpreter's total exposure: 4693
	// vectors never reach it at all, held upstream by encodeFailCeiling, so this number will
	// **rise** as #8 lands and more modules instantiate. That is the honest direction and it is
	// stated here so the rise is not read as a regression — but a ceiling cannot express "may
	// rise for a good reason", so when #8 moves it this constant is re-based *with the
	// instruction that unblocked it named*, exactly as textFailCeiling was re-based stepwise.
	//
	// And it is not a defect count either. `interp: no arm for opcode 3f` is a stratum boundary
	// honestly declared, the same category as the text reader's named boundaries — a vector the
	// engine reached and could not answer, reported as a fail with a bucket rather than hidden
	// behind a fourth verdict. *Bucketed failures are the work plan.*
	//
	// Slack 0.
	//
	// # Re-based 8713 → 440 by #7's memory work, which is what this column was for
	//
	// The `2d i32.load8_u` bucket above went **8001 → 0**, and with it the whole load/store
	// region, `3f memory.size` and `40 memory.grow`. What is left is 440, and it partitions to
	// exactly 440 — which is the point of a ceiling that names its members:
	//
	//	291  opcode arms this phase does not have, `no arm for opcode` and nothing else: `10
	//	     call` 74, `0f return` 26, the `fc 00`–`fc 07` saturating conversions 180, `23
	//	     global.get` 7, `fc 09`/`fc 10` 4. #7's remainder and #8's, unrelated to memory.
	//	 48  `assert_return value mismatch`, every one downstream of a missing `fc 0a`/`fc 0b`
	//	     — a vector that copies or fills before it loads reads bytes nobody wrote.
	//	 41  `memory %d is imported, and linking is not implemented (contract §3)` — the third
	//	     error category (ErrUnsupported), naming linking rather than blaming a table. 32
	//	     reached at the instruction, 9 as `no instance` from the module command.
	//	 34  `assert_trap (module)` where the trap did not arrive: 16 want *out of bounds table
	//	     access* (element segments, unbuilt), 15 *out of bounds memory access* against a
	//	     memory this phase cannot link, 3 *unreachable* from a start function.
	//	 26  `fc 0a`/`fc 0b` themselves, reached directly rather than through a later load.
	//
	// **The 95 value mismatches this work introduced are the number that had to be explained
	// rather than quoted**, because that bucket was 0 before this PR and a value mismatch reads
	// as the interpreter computing wrong answers. 22 of them were: `memory.size $mem1` returning
	// $mem3's page count, because the memory index space is shared between imports and
	// definitions and this table was sized `len(m.Memories)`. Not an unimplemented import
	// reported honestly — a *wrong answer about a different memory*, green on 22 vectors by
	// construction. 73 were the harness's, not the engine's: a bare top-level `(invoke …)` was
	// never run, so a following `assert_return` read a memory nobody had stored to. Both fixed;
	// the 48 that remain are the `fc 0a`/`fc 0b` line above.
	//
	// **The partition above was drafted from the census and then corrected by running it**, which
	// is worth the sentence because the draft was plausible: it said `10 call` 45 (it is 74), put
	// the residue at 289 (291), and omitted the 34 `assert_trap (module)` fails entirely — a
	// whole category, in a list that claimed to sum. The five lines now sum to 440 because they
	// were read off the buckets rather than reasoned toward them.
	//
	// The prediction in the paragraph above — that this number would **rise** as #8 landed and
	// more modules instantiated — is left standing rather than trimmed, because it was right and
	// then overtaken: it rose 356 → 441 → 8713 exactly as written, and then the arms arrived and
	// it fell by 8273. A forecast that came true twice and was then made obsolete by the work it
	// was forecasting is the record worth keeping.
	//
	// # Re-based 440 → 662 by the block family, and 232 of the rise is downstream reading
	//
	// `block`/`loop`/`if`/`select` on both sides (#7) instantiate modules whose exports are a
	// `loop` around an `if` — `memory_copy.wast`'s `checkRange` is the shape — so vectors that
	// could not be built before now run and read memory. The rise is legitimate under this
	// constant's own instruction, and the instruction is named: **+227 `memory_copy.wast` and +5
	// `memory_fill.wast` value mismatches**, +7 `10 call`, +9 `fc 0b`, −26 `0f return`.
	//
	// It partitions to exactly 662, read off the buckets rather than reasoned toward:
	//
	//	309  opcode arms this phase does not have, `no arm for opcode` and nothing else: `10
	//	     call` 81, the `fc 00`–`fc 07` saturating conversions 180, `fc 0a`/`fc 0b` 35, `23
	//	     global.get` 9, `fc 09`/`fc 10` 4.
	//	280  `assert_return value mismatch`, every one downstream of a setup invoke that failed
	//	     — see below, where that is measured rather than claimed.
	//	 52  `memory %d is imported, and linking is not implemented (contract §3)` — the
	//	     ErrUnsupported category, naming linking rather than blaming a table.
	//	 21  the encoder's frontier charged here because instantiation is where it is met: 20
	//	     `(elem …)` and 1 `(start …)`, both #8's.
	//
	// **That is a partition by *cause*, and it is not the bucket list the board prints**, which
	// is a distinction worth stating because reading one against the other looks like a
	// discrepancy and is not. 34 of the 662 arrive under `assert_trap (module) expected: …`
	// keys — the vector wanted a trap from a bare module form, so the *key* names the trap it
	// wanted and the cause is in `Got`. Unfolded: the 16 + 2 + 2 under those keys are `(elem …)`
	// encoder reds, the 7 + 2 + 1 + 1 are imported memories, and 2 are `23 global.get`, which is
	// why that opcode reads 7 by key and 9 by cause. A census that had quoted the keys would
	// have reported four buckets short and one opcode light.
	//
	// **All 280 value mismatches stand behind a setup `(invoke "test")` that itself fails**, and
	// that is measured rather than asserted: a probe grouped every mismatch under its preceding
	// module head and asked whether that module's own top-level invoke succeeded. 280 of 280
	// downstream, 0 not. A vector whose setup traps on `no arm for opcode fc 0a` then reads a
	// memory nobody copied into — the same downstream shape the 48 above already had, four times
	// larger because more of those modules now build.
	//
	// The "0 not" was interrogated rather than quoted, because *exactly zero* on an agreement is
	// the cleanest tell there is: the classifier was falsified by making it record no setup
	// failures, and it flipped all 280 to "not downstream". It can distinguish both ways.
	//
	// **`0f return`'s 26 went to zero, and three of them are grave #135 rather than a new arm.**
	// The bucket drained because the arm arrived — that part is ordinary. What is not: the arm's
	// first version did not truncate the stack to the function's result arity, where
	// `eval.ml:1069` is `take n vs0`, so `(i32.const 1) (return (i32.const 2))` left two values
	// and `Invoke`'s arity check rejected a **valid** module as unvalidated. Three `comments.wast`
	// vectors failed exactly that way — a new head, `module reached the interpreter unvalidated`,
	// which is how it was found: the census printed it, and a *layering-debt* error on a module
	// the reference accepts is not a debt. An accept-direction defect the suite scores green by
	// construction everywhere else, and its comment asserted the property the code lacked.
	// **Re-based 662 → 670 by `br_table`, and the +8 is the legitimate rise this constant's own
	// message licenses**: #8 unblocked modules that now instantiate and run, so they reach an
	// opcode this engine has no arm for. The instruction is named, as that message requires —
	// all 8 are `interp: no arm for opcode 10`, which is `call`. Measured as a bucket-set diff
	// against the pre-PR board rather than inferred from the total: three deltas, and this is the
	// only one that lands here.
	//
	// A rise is not automatically legitimate, which is why the partition is the evidence: had the
	// 8 arrived under a value-mismatch or an `unvalidated` head, the same +8 would have been a
	// regression in an arm that used to answer, and the total alone cannot tell those apart.
	// # Re-based 670 → 1139 by `call_indirect`, and the +469 is a net of two movements
	//
	// The largest single rise this constant has taken, and legitimate under its own message: the
	// `call_indirect` arms mean `table_copy.wast` and `table_copy64.wast` instantiate for the
	// first time, so those two files' 938 encoder reds became 394 passes and 536 exec reds. A
	// ceiling that rises because a *front end* stopped blocking the interpreter is this constant
	// working as documented — but "legitimate" is a claim about the partition, not about the
	// direction, so the partition is what is recorded.
	//
	// **+469 net = +565 arriving in 9 files, −96 leaving 10.** Both halves are stated because the
	// total hides the second: quoting +469 as "the new work's reds" would credit this PR with a
	// 96-vector improvement it also earned and never mention it.
	//
	//	+272  table_copy.wast     encode 469 → 0, exec 0 → 272 — the file went 52/521 to 249/521
	//	+264  table_copy64.wast   the same module set with i64 tables, 52/521 → 249/521
	//	 +16  bulk.wast           encode 47 → 31, exec 31 → 47 — the 16 `call_indirect` reds moved
	//	  +4  imports.wast        encode 6 → 0, exec 19 → 23
	//	  +9  five table_*.wast   table_set 2, table_get 2, table_init 2, table_init64 2, table_grow 1
	//	 −68  endianness.wast     exec 68 → 0, all of them `no arm for opcode 10` — plain `call`
	//	 −28  nine others         func 8, elem 6, forward 4, fac 3, linking 2, memory_trap 2, three at 1
	//
	// And by *cause*, which is a different partition from the board's bucket keys (see the note
	// above on why reading one against the other looks like a discrepancy). The three classes
	// that were zero before this PR:
	//
	//	420  `table slot N names function N, which is an import, and linking is not implemented`
	//	     — **the honest §3 decline, and the reason the two table_copy files are 249 and not
	//	     521.** Their modules import five functions from a registered module and put them in
	//	     tables, so every `call_indirect` through those slots meets linking. Not a defect and
	//	     not this PR's to fix; it is #7's linking work, and it is the single largest class in
	//	     the exec column now.
	//	 47  `no arm for opcode fc 0e` — `table.copy`, which those files' setup functions call.
	//	 57  `trap: uninitialized element N` — **downstream of the 47, not of the 420**, and that
	//	     distinction was checked rather than assumed: the vectors are `assert_return`s whose
	//	     setup is `(invoke "test")`, and "test" is a `table.copy` that traps on the missing
	//	     arm. So the slots it should have filled read empty and the call traps. Read at
	//	     table_copy.wast:590 and bulk.wast:307 with the failures printed beside them, because
	//	     "uninitialized element" next to 420 linking declines is exactly the coincidence that
	//	     invites the wrong parent.
	//
	// The remaining classes, and the whole table sums rather than gestures — every nonzero delta,
	// with nothing rolled into an "and others":
	//
	//	+420  §3 linking, table slot          −89  no arm for opcode 10 (`call`, the arm landed)
	//	 +57  trap: uninitialized element N   −18  encoder frontier at instantiation, 21 → 3
	//	 +47  no arm for opcode fc 0e          +4  no arm for opcode 25 (`table.get`)
	//	 +28  value mismatch, 280 → 308        +4  no arm for opcode fc 0d (`table.init`)
	//	 +11  §3 linking, table                +1  no arm for opcode d2 (`ref.func`)
	//	  +3  §3 linking, function             +1  the elem-expr `0x23` decline
	//
	// 420 + 57 + 47 + 28 + 11 + 3 + 4 + 4 + 1 + 1 − 89 − 18 = **+469**, against a measured
	// 670 → 1139. The value-mismatch rise is +28 in the two table_copy files and +4 in bulk, less
	// −4 elsewhere, and all of it downstream of the same missing `fc 0e`.
	//
	// A first draft of that sum was assembled from the per-file column by hand and came out
	// wrong in three places at once — it omitted the +11 and +3 linking classes entirely, called
	// `0x25` two instead of four, and still printed a total of 469 because the total was the one
	// figure carried over rather than recomputed. A sum that closes because its bottom line was
	// copied is not a partition, and the only reason it did not stand is that the diff was re-run
	// against the class table instead of proofread.
	//
	// The per-file and per-cause figures were read off `Failure.Stratum` and `Failure.Got` by a
	// throwaway probe, not off the printed bucket keys. A first pass had used the `no instance:`
	// prefix as a proxy for StratumEncode and came out 6 short — the prefix is a rendering
	// convention and the stratum is the classifier's own field, so the proxy disagreed on six
	// vectors whose message shape does not match their charge. Grave #129's rule, earning its
	// keep: measure with the instrument, never with a regexp over its output.
	// **Section 6 and `ref.null` (#8): 1139 → 1215, +76, and every one of them is an arrival with
	// *zero departures*** — set-differenced on `(file, line)` keys through `Failure.Stratum`, not
	// inferred from the total. That asymmetry is the signature of a frontier draining rather than of
	// an interpreter regressing: modules that could not be encoded now reach the interpreter and stop
	// at the first opcode with no arm, which is a red moving from `encode` to `exec` and is exactly
	// what a ceiling on this column must not read as a defect.
	//
	//	+30  no arm for opcode d0 (`ref.null` — the instruction this PR taught the *encoder* to
	//	     write, which is the tidiest illustration of the class: writing it is what let the
	//	     interpreter be asked about it, and the interpreter has no arm)
	//	+20  no arm for opcode fc 10 (`table.size`)
	//	+14  no arm for opcode 24 (`global.set`)
	//	 +6  no arm for opcode d2 (`ref.func`)
	//	 +6  no arm for opcode 23 (`global.get`)
	//
	// 30 + 20 + 14 + 6 + 6 = **+76**, and it lands in three files above all others: 36 in
	// `table_size.wast`, 14 in `table_grow.wast`, 10 in `ref_func.wast`. Those are files whose
	// modules are almost entirely globals and table operations, so the global section is the whole
	// of what was blocking them.
	//
	// **The `global.get`/`global.set` arrivals name the next interpreter work, and naming it is the
	// point of measuring per-opcode**: 20 vectors are now blocked on two arms this PR deliberately
	// did not write, in a column that used to be blocked on the encoder instead.
	//
	// **`global.get`/`global.set` (#7): 1215 → 1199, −16, and the two buckets are gone rather than
	// smaller** — `no arm for opcode 23` (15) and `opcode 24` (14) are both **absent** from the
	// after-state, which is a bucket reaching zero and therefore this PR's measure of done.
	//
	// The −16 is smaller than the 29 those buckets held, and the 13-vector difference is the
	// interesting half: those vectors are **still fails, at the same (file, line), for a different
	// reason**. A key-difference cannot see that — it reports 16 departures and 0 arrivals and would
	// let a reader conclude 29 − 16 = 13 vectors vanished from the board. So the surviving keys were
	// joined on (file, line) and their causes compared pairwise, and the two buckets partition
	// differently:
	//
	//	opcode 24 (14):  all 14 departed. `global.set`'s vectors had nothing behind them.
	//	opcode 23 (15):   2 departed, **13 changed cause** — `global.get` is reached by modules
	//	                  with further unimplemented ground beyond it:
	//	                    9  ⇒ §3 linking, `global N is imported` (globals 0..6, one import chain)
	//	                    3  ⇒ no arm for opcode 26 — `table_set`, read out of optable.go rather
	//	                       than guessed from the byte's neighbourhood, a first draft of this
	//	                       line having called it `local.set` on nothing but proximity
	//	                    1  ⇒ no arm for opcode d1 — `ref_is_null`
	//
	// 16 departed + 13 changed cause = 29, which closes against the two buckets exactly. **The
	// in-place cause change is the reading the key-difference is blind to**, and it is the mirror of
	// #151's pure-arrival signature: there, arrivals with zero departures meant a frontier draining;
	// here, departures with zero arrivals *plus* thirteen silent re-causings means an arm landed and
	// the queue behind it was deeper than one bucket key showed. Quoting only the −16 would have
	// implied 29 vectors' worth of progress for 16 vectors' worth.
	//
	// Nothing moved in the other strata: encode held at 1353 in both revisions, and arrivals were
	// **zero** — so no vector regressed into the exec column to pay for the gain.
	//
	// **The saturating truncations (#7): 1199 → 1019, −180, and all eight buckets are absent rather
	// than smaller.** `fc 00`..`fc 07` held 22/21/22/25/24/19/24/23 = 180 before and none of the
	// eight appears in the after-state, so this is eight buckets reaching zero in one PR.
	//
	// The three-way account is the flattest one this taxonomy can produce, and that is the finding
	// rather than a formality: **180 departed, 0 arrived, 0 same-key-new-reason.** 2552 − 180 + 0 =
	// 2372, which closes against the measured column exactly.
	//
	// **The zero in the third category was interrogated, not quoted** (#106): a perfect zero is
	// where an instrument reports its own blindness, and this is the same comparison that found 13
	// re-causings one PR ago. It joined **2372 pairs** — every surviving key — and found all 2372
	// causes byte-identical; run against a synthetic pair with one altered cause it reports the
	// change. So the comparison is live and the zero is a fact about `trunc_sat`, not about the
	// probe. The fact it states is a topological one: these vectors have **nothing behind them**.
	// `conversions.wast`'s trunc_sat functions are one instruction over a parameter, so the arm was
	// the last thing missing — where `global.get` sat in modules with linking and further opcodes
	// beyond it, which is why that PR's 29 held only 16.
	//
	// All 180 are in **one file**, `conversions.wast`, which goes 347/527 → **527/527, zero fail**.
	// That is the exact inverse of #152's file-level signature ("the file named after the feature is
	// not where the feature paid off") and worth stating in the same words: here the feature's own
	// file is the *only* place it paid off. A per-opcode arm's blast radius is a fact about how the
	// suite groups vectors, not a rule, and the two PRs bracket both ends of it.
	//
	// Encode held at 1353 across both revisions again, so nothing was borrowed from another stratum.
	//
	// # 1019 → 608: the bulk trio, and 411 out of 82
	//
	// `fc 0e` table.copy, `fc 0a` memory.copy and `fc 0b` memory.fill — the three buckets held
	// **82** vectors between them (47 + 22 + 13), and the arms moved **411**. A 5:1 multiplier, the
	// largest this column has recorded, and the mechanism is the corpus rather than the engine:
	// `memory_copy.wast` and `table_copy.wast` are generated (`;; Generated by
	// ../meta/generate_table_copy.js`), so each bulk instruction sits in its own module followed by
	// up to forty `call_indirect` or `i32.load` read-backs. Those read-backs were the other 329,
	// carried in the `assert_return value mismatch` and `trap: uninitialized element N` buckets —
	// which is why they were invisible in the three `no arm for opcode` counts that named the work.
	//
	// **So a bucket named after the missing opcode understates an arm whose absence corrupts state
	// that later vectors read.** *Bucket size estimates the reward, not the job* has a mirror image:
	// here the bucket understated the reward by 5×, because the board keys on the *expected* string
	// and a read-back's expected string names a value, not an instruction. Both directions have now
	// been paid for, and the shared remedy is the same one — partition the bucket by mechanism
	// before quoting it as a forecast, then quote the measurement after.
	//
	// Third distinct blast-radius shape in three PRs — #152 diffuse (+16 over eleven files, none of
	// them the feature's own), #154 concentrated (all 180 in `conversions.wast`), this one
	// multiplied by a generator's read-back convention. Three shapes in three PRs is the evidence
	// that per-arm forecasting from a bucket count does not work at all.
	//
	// Encode held at 1353 for the fourth consecutive revision, so none of the 411 was borrowed.
	// See passFloor for the 60 vectors that stayed red under a new cause — the arm relocated them
	// to the linking frontier rather than answering them.
	// # 608 → 626, +18, and the rise is a legitimate one: it is what admitting a Kind buys
	//
	// The `assert_trap (invoke …)` Kind (#157) put 4876 previously-unasked commands into the run
	// loop, and 1440 of them reached a verdict of fail — but only **18** land in this stratum,
	// because the other 1422 never reach an instance and are therefore encode-column vectors. Set-
	// differenced on `(file, line)` keys, the account is the pure-arrival signature: **18 arrived,
	// 0 departed, 0 same-key-new-reason**, so nothing regressed to pay for the drain, and all 18
	// carry `KindAssertTrapAction` — no pre-existing vector changed column.
	//
	// Where they went names the next frontier rather than a defect: 9 are `interp: no arm for
	// opcode 25` (`table.get`, in `table_get.wast` and `table_grow.wast`), and 9 are contract §3
	// linking — 6 `table 0 is imported`, 3 `memory 0 is imported`, all in `imports.wast` and
	// `imports2.wast`. So two thirds of this rise is queued behind work the project has already
	// declined for this phase (linking) or has a bucket for (the fc/25 arms).
	//
	// # 626 → 705, +79, and this is the reclassification the ceiling's own message anticipates
	//
	// `table.init`/`memory.init` on both sides (#8, #7) drained 1781 vectors out of the encode
	// column, and vectors that used to die *before* reaching an instance now reach the
	// interpreter — so a stratum whose members are "vectors that got as far as exec" grows by
	// construction when the encoder stops refusing. The ceiling's message licenses exactly this
	// and asks for the instruction named; the accounting is the instruction plus a set difference,
	// because a rise is where a regression would hide most comfortably:
	//
	//	+84  arrivals, **all one reason**: `table slot N names function M, which is an import,
	//	     and linking is not implemented (contract §3)`, 42 in `table_init.wast` and 42 in
	//	     `table_init64.wast`. These modules import functions and put them in a table's
	//	     element segments, so instantiation now gets far enough to meet §3 — the frontier the
	//	     project has already declined for v0 (no host-linking at v0), not a defect.
	//	 −5  departures, and they are precisely the two drop arms this PR adds: `fc 09`
	//	     (`memory_init.wast:209`) and `fc 0d` (`table_init.wast:435`, `:507`,
	//	     `table_init64.wast:620`, `:692`), all quoting `no arm for opcode`.
	//
	// 84 − 5 = 79, and **0 same-key-new-reason**: no vector already in this column changed why it
	// is red, so nothing regressed under cover of the rise. The column's largest reason is now
	// the §3 table-slot frontier at 540 of 705 — which is the next bucket this stratum offers and
	// is not takeable at v0.
	//
	// # 705 → 248, the largest fall this column has taken, and its own arrivals need the account
	//
	// The registry (0017 Q1) drains the §3 frontier the note above called untakeable: **624 → 13**
	// on the sentinel bucket, because a script-level `register` supplies from *another module in
	// the same script* rather than from a Go host, which is the whole content of 0017's measured
	// negative. The 13 that remain are gate-declined suppliers whose registers bind nothing.
	//
	// **The four sections above quote `linking is not implemented`, and that string is retired**
	// — stated here rather than edited there, because those sections are the board as it read at
	// the time and a re-based ceiling's history is the part worth keeping verbatim. The engine
	// now says `is an import nothing supplied (contract §3)` at all four sites, the swap being
	// forced by grave #36: an engine with a linker cannot testify that it has none. Recorded so
	// that a reader grepping the retired text finds a resolved rewording rather than a bucket key
	// that silently stopped matching, which is the failure mode a rewording mid-drain has.
	// TestUnsatisfiedImportKeepsItsSentinel pins the four sites to one wording.
	//
	// Flows, not the net, because both directions moved: **621 departures, 209 arrivals** at the
	// Q1 measurement, then a second motion of **46 departures, 1 arrival** for the link-error
	// wording below. Arrivals partition 167 `assert_unlinkable` + 37 named assert_return + 3
	// register + 2 named assert_trap — every one a Kind that did not reach this stratum before,
	// so nothing pre-existing changed column, and the ceiling's own message is satisfied on the
	// terms it states.
	//
	// **23 of the arriving assert_returns are `assert_return value mismatch`, and they are a real
	// defect rather than benign churn.** Probed rather than assumed: a supplier exports a table
	// whose slot 0 funcrefs its own func returning 11, an importer imports that table with a decoy
	// func 0 returning 99 and `call_indirect`s slot 0 — **got 99, want 11**. A table slot's funcref
	// carries a bare module-local index, so a cross-instance call resolves in the *importer*. That
	// is 0017's Q2 exactly, now reachable for the first time because nothing could hold another
	// instance's table until Q1 landed, and it is an accept-direction defect (§9 G-3) that scores
	// green whenever the two instances happen to agree. Filed as **#163** with both reproducers
	// rather than fixed here: widening `ref` is Q2's PR, and the seam is where the ADR put it.
	//
	// **#164 landed: 248 → 211, and the 124-vector `assert_unlinkable` bucket is 86.** The decoder
	// now retains a non-func import's table type, memory limits or global type/mutability
	// (`binary.Import`'s Table/Memory/GlobalType/GlobalMutable fields) instead of reading and
	// dropping them, and `Instance.link` compares them — `sameFuncType` for a func import's own
	// signature, `matchLimits` (the reference's `match_limits`, min/max in opposite directions) for
	// table and memory, byte equality for a global's type and mutability.
	//
	// **The 124 has a full account, measured by joining the pre- and post-fix bucket dumps rather
	// than read off either board alone** (a bare diff of totals cannot distinguish a conversion
	// from a co-incidental re-key): of the 124, **42** are this fix's target and **38 of those 42
	// convert to pass**. The other **4** stay in the bucket — `type-subtyping.wast`'s `rec`/`sub`
	// rows, where a func import's declared type is a *nominal subtype* rather than a plain
	// signature, and `sameFuncType`'s structural-equality reduction cannot see that (its own doc
	// comment already declares the gap; GC's gate gates the construct that would exercise it more
	// than this one file does). The remaining **82** were never reachable by this fix: **35** are
	// blocked by an EH-gated register target (`imports.wast`'s `test` module exports a `(tag …)`,
	// so it never decodes with the gate off and every import from it reads `unknown import` rather
	// than `incompatible import type`), **14** by a GC-gated register target (`Mref_ex`/
	// `Mtable_ex`/`M`, same mechanism, GC's gate), **4** by a Memory64-gated register target
	// (`test-table64-*`/`test-memory64-*`), and **29** by a GC-gated *import descriptor* — the
	// importing module's own declared type is a reftype the encoder cannot yet emit, so these fail
	// to encode rather than to link. **38 converted + 4 still-failing-in-scope + 35 + 14 + 4 + 29 =
	// 124**, closed with no residual.
	//
	// **`table_grow.wast` moves independently of the 124, by +1 net, and it is not a regression.**
	// Two of its rows (:124, :130) were `interp: no arm for opcode d0`/`fc 10` and re-key to two
	// new import-type-mismatch buckets — the same underlying cause read through a sharper
	// instrument, since `table.grow`'s absence means the table these vectors chain through never
	// actually grows, so the *next* module's declared size genuinely mismatches the actual one. A
	// third row (:123) is a brand-new fail one command earlier in the same chain: the `register`
	// commands's own module fails to link for the identical reason, caught one step sooner than
	// line 124 caught it. Verified by joining the two boards' full bucket dumps (40 departures, 3
	// arrivals, net −37, matching the board's 1265→1228 exactly) rather than trusting either
	// board's printed total alone.
	//
	// One genuine defect found in the making: `memory.grow` reallocated a memory's bytes without
	// updating its retained `Limits.Min`, so a grown memory re-exported for another instance to
	// import reported its stale pre-growth minimum — `imports4.wast:19-37` pins the corpus's own
	// vector for exactly this, in its own comment ("imported memory limits should match, because
	// external memory size is 2 now"). Fixed alongside #164 because the new type check is what
	// made it observable; `table.grow`'s absence means the table side of the same defect cannot be
	// measured yet.
	//
	// **#163 landed: 211 → 196.** 0017 Q2 (grave #163): `ref` gained an `Inst *Instance` field
	// naming the instance a funcref's index space belongs to, and `call_indirect` resolves
	// through it rather than through the caller — `mismatch_test.go`'s `expectedMismatches`
	// carries the per-row account (15 rows removed, all in that one bucket, the other 7 named
	// mismatches unmoved and unrelated: 5 multi-memory, 2 `#8`'s missing `(start …)` field).
	//
	// **#7's opcode-arm stream: 196 → 118.** Six arms this stream had been missing —
	// `table.get`/`table.set` (0x25/0x26), `table.size`/`table.grow`/`table.fill` (`fc 10`/`0f`/
	// `0x11`), `ref.null`/`ref.is_null`/`ref.func` (0xd0/0xd1/0xd2) — closed `opTableFC`'s region
	// completely (all 18 sub-opcodes now answered) and retired the main switch's three
	// reference-family gaps. `TestUnhandledFCSubOpcodeStaysOnTheWorkList`'s subject dissolved
	// rather than moved this time: with the region drained, the tripwire re-points to a direct
	// `execFC` call naming a sub-opcode the decoder itself can never admit, since there is no
	// nineteenth unhandled row left on the corpus to name.
	//
	// **A second genuine defect found in the making, upstream of any table opcode**: `invoke`'s
	// post-call arity check counted only `st.num`'s delta against `len(ft.Results)`, so *every*
	// function returning a ref-typed value reported "declares 1 results and left 0 values on the
	// stack" — right for a numeric-returning callee, wrong unconditionally for a ref-returning
	// one, because a ref result lands on `st.refs` and the check never looked there. Unreachable
	// before this PR: nothing produced a ref-typed function result through plain `call`/
	// `call_indirect` until `table.get`/`ref.func` had arms to be called *through*.
	// `table_get.wast`'s `is_null-funcref` (`ref.is_null (call $f3 …)`, `$f3` returning
	// `funcref`) is the corpus's own specimen. Fixed by counting `ft.Results` per kind and
	// checking each stack against its own count — `TestCallCheckesArityPerStack` pins both
	// directions, falsified by reverting to the shared counter and watching the exact error
	// message reappear.
	//
	// Two rows re-key rather than pay, both already-registered causes gaining a table.get/
	// table.grow-shaped member: `table_get.wast:30` and the two `table_grow.wast` rows are all
	// the harness's own `readConst` being unable to parse a `ref.null`/`ref.extern` action
	// argument, so the setup invoke that would write the table never runs and the later read is
	// honestly reporting the un-written state.
	//
	// **118 → 89, decision 0021's encoder-side implementation — -29 departures, 0 arrivals,
	// entirely a downstream effect and not this stratum's own frontier moving.** A module that
	// used to refuse to encode reached `run`/`register`/`invoke` for the first time and, for 29
	// vectors, the *link-time* check now runs and produces `assert_unlinkable expected:
	// incompatible import type` correctly (the encoder's frontier no longer masks the module's
	// actual type mismatch) — 27 in the default lane, +2 the all-gates-on lane's own denominator
	// carries. This column's frontiers (the opcode-arm work list above) are untouched.
	// **89 → 243, the SIMD gate's default-on flip (#227/ADR 0025, contract §9 G-1 amended).**
	// A legitimate rise, not a regression: 154 vectors that used to decline for `simd: feature
	// gate disabled` (Gated, invisible to this ceiling) now reach the interpreter and either
	// pass or land in one of exec's own named buckets — chiefly the 161 `#9`-orthogonal
	// `module reached the interpreter unvalidated` fails ADR 0025's carve-out names, plus the 7
	// already-registered `assert_return value mismatch` rows and a handful of encoder/linking
	// fails unrelated to SIMD's own arms. None of it is new engine wrongness: #223 and #229
	// closed every genuine defect the flip's own forecast surfaced, confirmed on both
	// architectures, before this flip landed.
	// **243 → 81, and none of the 162 is this PR's** — it is drift the slack-0 grave hid (see
	// boardbound_test.go's package comment). Attributed by walking this stratum line across every
	// merge from the SIMD flip to HEAD, one checkout per commit, rather than reasoned about:
	//
	//	d79c5f9 … 3b3a4b7   exec 243
	//	5174810             exec 106   one constant-expression evaluator, four call sites (#241/#244)
	//	23b886f … 30e76e9   exec 106
	//	83cbecf             exec  81   a v128 lost a slot at a branch and at select (#242/#269)
	//	efce8a7 … 052b45b   exec  81
	//
	// −137 and −25, residual zero. Both are legitimate progress in the direction this ceiling
	// constrains, and both should have re-based it in their own PR; neither could, because
	// `boardBound` was returning before the staleness check on every bound that passed slack 0. So
	// the two merges are not at fault — the exemption is, which is why the fix is in the helper and
	// the re-base is here rather than a rule about remembering.
	//
	// This PR moves this column by **nothing**: `assert_invalid` vectors are answered before
	// execution, so the validator's arrival cannot touch it. Stated because a 243 → 81 re-base
	// landing in a PR that also adds 1211 fails elsewhere is exactly the shape a laundered drift
	// would have.
	// **81 → 15, and 66 of the 66 are a reclassification rather than an engine getting anything
	// right** — decision 0037, issue #366. The registry carried no gated state, so a `register`
	// whose module was gate-declined left the name unbound and every later import against it
	// reported `unknown import`: true about the resolver, a lie about the cause, and 62 of this
	// column's 81 rows. Four more had a different `Got` and the same cause. They are now `gated`,
	// which is the third verdict landing where it belongs and **not** progress in the direction
	// this ceiling constrains.
	//
	// Stated in those words because a −66 on the interpreter's own column is exactly the shape a
	// laundered figure would have, and the pre-registered forecast (0037, written before the
	// mechanism existed) said 19: the 4-row miss is the forecast's, from filtering on the
	// `unknown import` message instead of on the cause.
	//
	// What is left is the interpreter: 7 `assert_return value mismatch`, 2 `trap: uninitialized
	// element 0`, 3 `assert_trap (module) expected: unreachable`, 3 `register: no module named
	// $I/$I1/$I2`. That is the work plan this column exists to be, and it could not be read as one
	// while three quarters of it named the wrong component.
	// **15 → 8, and all 7 are the start section landing (#413) — the first time this column has moved
	// because the *interpreter* gained something rather than because a reclassification found its
	// column.** The seven were the three `assert_trap (module) expected: unreachable` rows
	// (`linking.wast:592`, `linking3.wast:65`, `start.wast:97`), the two `assert_return value mismatch`
	// rows the sibling registry has just retired, and the two `trap: uninitialized element 0` rows —
	// and the four below the three `assert_trap`s were *downstream* of them, each asserting against a
	// different, healthy instance whose table or memory the refused module's start function was
	// supposed to write.
	//
	// Which is the reading worth keeping: **not one of the seven was the interpreter computing a wrong
	// answer.** They were fail rows in the exec stratum because a module the *encoder* could not write
	// produced no instance, and the stratum ledger charges the row where it lands. That extends
	// Scott's own withdrawal of the "exec 81 means the interpreter gets answers wrong" premise (#409)
	// from the 66 to seven more of the 15 — leaving the residue this column now names: 5 `assert_return
	// value mismatch` (all `load1.wast`, all #414's gate-decline collateral) and 3 `register: no module
	// named $I/$I1/$I2`.
	// **8 → 5, and the three are the `register: no module named $I/$I1/$I2` rows the entry above named
	// as half of what was left.** They were never about the interpreter or the registry: `instance.wast`
	// registers `$I1`, `$I2` and `$I`, which are `(module instance …)` names, and the harness had no
	// case for that form — so the names were genuinely unbound and the resolver's complaint was true
	// about itself and wrong about the cause. #426 gives the form an arm, and with the EH gate off the
	// three registers are `gated` (the definitions carry `(tag …)`), which is the third verdict landing
	// where it belongs rather than a fourth red disappearing.
	//
	// So this is a reclassification, in the words the 66 got: **not one of the three was the
	// interpreter computing a wrong answer**, and the entry above had already said as much when it
	// listed them beside the five `load1.wast` rows without claiming a defect. The residue is now
	// exactly those five — `assert_return value mismatch`, all `load1.wast`, all #414's gate-decline
	// collateral — which makes this column a single-subject work plan for the first time.
	// **5 → 0, and it is the fifth consecutive reclassification rather than the first repair.** Decision
	// 0038: the five `load1.wast` rows the entry above named as the whole residue are a side effect a
	// gate-declined module was supposed to have on a *third* instance — `$M`, which instantiated cleanly
	// and honestly reports the zeros its memory still holds. `sideEffectOfDecline` gates them, so this
	// column empties for the reason the previous four entries emptied their share: the row was charged to
	// the interpreter and belonged to a gate that is off by design.
	//
	// **Not one of the five was the interpreter computing a wrong answer**, and with that the whole
	// chain above resolves to the same sentence: this column has never once named an interpreter defect
	// since the stratum ledger was built. Worth stating exactly there, because five reclassifications in
	// a row is the point at which "the exec column is the interpreter's work plan" stops being a premise
	// and becomes a claim the board has falsified — the premise Scott withdrew on #409, now spent.
	//
	// **A ceiling at 0 is the most sensitive this bound has ever been**, which is the one thing to say
	// against the reflex that an emptied column is a lost instrument. It was, at 243, unable to notice
	// anything smaller than a 162-row drift; at 0 with slack 0 any single new exec fail anywhere in the
	// corpus fails it and has to be named. The column stops being a work plan and becomes a tripwire,
	// and the work plan moves to the all-gates-on lane's 38 (see allOnFail…, and decision 0038's
	// consequences section, which names the nine files).
	const execFailCeiling = 0
	boardBound(t, "execFailCeiling", execFail, execFailCeiling, 0, ceilingBound,
		"the interpreter answered fewer vectors than it did: either an opcode arm regressed or "+
			"a value comparison started disagreeing. A *rise* caused by #8 unblocking more "+
			"modules is legitimate and gets this constant re-based with the instruction named")

	// The validator's own column, arriving with #9's first slice, and it is **the largest
	// annotated stratum this board has admitted in one move** — which is why the number is
	// pre-registered here rather than read off a green run.
	//
	// **1201 is a work plan, not a defect count**, the shape textFailCeiling had at 391 and
	// encodeFailCeiling still has. It decomposed into exactly two populations at slice 1 — three
	// from slice 2 on, the third having been 0 here — and **each gets its own bound, because they
	// are separate instruments and not halves of one**:
	//
	//   - **1059 declines.** Slice 1 met an instruction whose rules belong to a later slice and
	//     said so, `validate.ErrUnsupported` naming the opcode. This number falls when a slice
	//     lands; the bucket keys *are* the work plan, one key per rule to write.
	//   - **142 admissions.** Modules this validator declared *valid* that the suite says are
	//     not — limits, duplicate export names, constant expressions, alignment: rules attached
	//     to things the code-section walk never visits, so there is no instruction to decline.
	//     This number falls when a *rule* lands, and it is the accept direction, which no other
	//     bound on this board can see (contract §9 G-3).
	//
	// One constant over both would let a decline converting into an admission read as no change,
	// which is the worst movement this column can make: a vector that used to be refused with a
	// named gap is now *accepted*, and the total is identical. That is binaryFailCeiling's own
	// argument about not sharing a column with the text one, arriving inside a single stratum.
	//
	// **The 0 that was not here is the one worth naming: wrong-message failures.** At slice 1, 2697
	// vectors were scored and *no* vector reached the type checker, was refused, and got a message
	// the corpus disagreed with. That is a suspiciously clean number and it was treated as one: it
	// was 0 because six message defects were fixed before that slice landed — the invented `global
	// is immutable`, all nine `unknown <category>` formats, `pushFrame(opFuncBody, nil, …)`, the
	// duplicated result-arity check, `expectEmptyFrame` discarding leftovers, and `brTable`
	// comparing its arms to the default — each of which was a non-zero row in this account until it
	// was fixed. Four were comments asserting the property the code lacked.
	//
	// That paragraph ended "a future non-zero here is a regression with no ceiling to hide under,
	// which is the point of not giving it one" — and **slice 2 is the future it was talking about,
	// which is the part worth reading before the constants below.** It went to 4 there and back to 0
	// with #306. What the sentence got right is that the rise had to be *explained*; what it got
	// wrong is that a population with
	// no bound was being watched at all. It was not being watched: `validateAdmitCeiling` was
	// `validateFail − validateDeclined`, so the four new wrong-message rows landed *inside the
	// accept-direction constant* and the only reason they were noticed is that the destination
	// ledger counts by bucket key and disagreed by exactly 4. The population now has a bound of its
	// own (`validateMismatchCeiling`), which is the third-verdict rule arriving one stratum down: a
	// third outcome needs a structural bound and not a comment promising to look.
	//
	// Slack 0 on all three, matching every other fail ceiling: these drain by rules landing, and a
	// slice that lands re-bases the constant with the rule named.
	//
	// # Slice 2 (#305), the 0xFD region: 1201 → 553, and the parts moved in opposite directions
	//
	// **668 declines drained; 648 became passes; 16 landed in the admission stratum and 4 in the
	// wrong-message one.** The arithmetic closes exactly (−668 + 16 + 4 = −648, against a +648 pass
	// delta), which is what makes the account below a ledger rather than a summary. This is the
	// sub-partition earning its keep on its first real test: a single constant over the stratum would
	// have shown 1201 → 553 and said nothing about the twenty vectors that went the wrong way.
	//
	// It is also the sub-partition's *own* first real test, and it took the finding: two counters
	// were enough for a stratum whose third population was empty, and the 20 arrived split 16/4
	// across a boundary the arithmetic could not see. See `Failure.Accepted`.
	//
	// **Every one of the 20 is the alignment gap, and it is one cause with two faces.** Established
	// by diffing the bucket keys before and after rather than reasoned about — the only admitted
	// bucket that changed is `alignment must not be larger than natural`, 38 → 54, and the only
	// *new* key is the bare form's `expected:` bucket for `alignment must not be larger than
	// natural` at 4 — spelled `assert_invalid expected: …` in the era this was measured, and
	// `assert_invalid (module) expected: …` since #364 renamed the Kind
	// (`simd_store{8,16,32,64}_lane.wast`, one each):
	//
	//   - **+16 accepted-but-invalid.** `decodeMemop` drops the memarg's alignment, deliberately
	//     (its own comment: keeping it would store a fact only #9 reads), so a vector whose *only*
	//     defect is an over-aligned SIMD access is now typed successfully and accepted. Before this
	//     slice the instruction was declined and the module refused — for the wrong reason, but
	//     refused. Typing the region removes that accidental cover.
	//   - **+4 wrong-message refusals.** Those four modules carry two defects: over-alignment
	//     *and* an `(result v128)` whose body produces nothing. The reference reports the alignment
	//     error from inside `check_memop`; this package reports `type mismatch` at the body's
	//     `end`. An honest refusal with the wrong message per 0003 — and the population that was
	//     **exactly 0** three paragraphs above, which is why this note names it rather than letting
	//     the total absorb it. It is no longer 0, the cause is known, and it is one cause.
	//
	// **The trade was measured, not argued.** Declining the four memory families instead of typing
	// them yields `validate 603 (461 declined + 142 admitted + 0 wrong-message)` and pass 60280 — so
	// typing them buys 50 passes and costs these 20. Measured by running it: the four `Vec*` memory
	// arms returning `errNoVecSignature` produces exactly that board, which is also the falsification
	// of the ledger's self-retirement property below.
	//
	// **Ruled: take the admission, not the decline** (Scott, PR #307). The reason is not the 50
	// passes — it is that the decline channel means *"not yet in vocabulary"* and that is false here.
	// The validator knows how to type these; it cannot see the alignment because the decoder threw it
	// away. Declaring a decline for a reason that is not out-of-vocabulary puts two different facts in
	// one column, which is the error this board's whole sub-partition exists to refuse, and it would
	// buy that by leaving `v128.load`/`v128.store` untyped for the interpreter — paying 50 passes for
	// *worse information*. The condition attached to the ruling was a named ledger, since retired —
	// see the note at `validateAdmitCeiling` for what it was and what replaced it.
	//
	// The alignment slice was filed with a tripwire of its own (#306): at 54 it was the largest
	// admitted bucket, and a debt is discharged by a tripwire and never by an intention.
	//
	// # Slice 2's twenty, drained: 553 → 495, and the ledger retired itself as designed
	//
	// #306 landed the memarg's alignment through the decoder and `check_memop`'s first `require`
	// through the validator, and **both faces of the one cause closed together**: the 54 admissions
	// are 0 and the 4 wrong-message refusals are 0. The second half is the part worth stating,
	// because it was a *forecast* — those four modules carry two defects, and the claim that the
	// reference's step order (`check_memop` before the body's `end`) is what picks the message could
	// only be settled by running it. It was run. 54 + 4 = 58, against a 553 → 495 stratum delta.
	//
	// **The ledger retired itself with no edit to the arithmetic**, which is property 3 below
	// arriving on schedule: the alignment message stopped being reachable, its measured set emptied,
	// and the vacuity check fired with the instruction to delete the ledger. That instruction was
	// itself wrong by 38 — it said "re-base this ceiling to 142", and 142 was slice 1's *base*, the
	// figure with 38 alignment members already inside it. The correct residual is 104. A
	// self-retiring control that names its own successor value has to compute that value the same way
	// the live path does, or the last thing it says is a number nobody re-derived.
	//
	// # `select t`, drained: 495 → 493, and this one is the decline channel doing its job
	//
	// #294 retained `select`'s result-type annotation and typed it, and the two vectors it converts
	// are **declines**, not admissions: `select.wast:368` and `:373` were refused, correctly, by an
	// arm that said `instruction not in this slice: select_t` — which was *true*, and is the decline
	// channel's actual meaning as the ruling three paragraphs up defines it. Slice 3's two vectors
	// were the other case, the one that ruling calls false: rules the validator knew and could not
	// reach for want of an operand. The same PR landed both, so this stratum's two sub-ceilings fall
	// for opposite reasons in one diff, and the reason each is quoted separately is that only the
	// *decline* fall is evidence about vocabulary. `validateDeclineCeiling` 391 → 389 was that fall;
	// `validateAdmitCeiling` was unmoved at 104 and `validateMismatchCeiling` at 0.
	//
	// # 493 → 492 and 104 → 103: the offset bound (#310), and these bounds are *not* discretionary
	//
	// `check_memop`'s third `require` landed, and `align.wast:1004` moved from the admission census
	// to a pass — one vector, the whole reward, the other three expecting `offset out of range`
	// being `module quote` forms that sit in the unsupported column.
	//
	// **Both of these fired, and the pre-registration on #310 predicted they would not.** That
	// prediction was written by analogy to `allOnPassFloor`, which carries a slack of 250 and so says
	// nothing about a distance of 1 — and the analogy was false, because these two carry a slack of
	// **0** and are exact re-bases. The correction belongs beside the numbers and not only on the
	// issue: the all-on lane's silence is a property of *that* lane's one loose bound rather than of
	// board bounds generally, and reading one lane's instrument as the other's is the mistake #315
	// exists to remove. A prediction that a control will stay quiet is a claim about the control, and
	// this one got checked by being wrong.
	// # 492 → 134 and 389 → 31: slice 5, and the stratum's largest single fall
	//
	// The index-space family — 0xFC's ten bulk operations plus the two plain memory operators
	// `memory.size`/`memory.grow` — types 358 vectors that were declined, and **all 358 come out of
	// `validateDeclined`**. `validateAdmitCeiling` is unmoved at 103 and `validateMismatchCeiling` at
	// 0, which is the row worth reading twice: a slice this size converting nothing into an admission
	// means no module it newly types is *accepted* that the spec refuses, and converting nothing into
	// a wrong-message refusal means every one of the 358 refuses with the string its vector names. Two
	// unmoved ceilings are the evidence here, and neither would have moved on its own if the slice had
	// simply been right — they move when a rule is *nearly* right, which is what makes their silence
	// informative rather than empty.
	//
	// The forecast was derivable before the code existed and was pre-registered on #322 with all four
	// rows (declines −350, admissions 0, wrong-message 0, pass +350); the widening to `memory.size`
	// and `memory.grow` was taken during the work and accounts for the difference between 350 and 358.
	// So the pre-registered figure held on the population it was written about, and the extra 8 are
	// named as a scope decision rather than folded into a delta that would then have matched nothing.
	//
	// # 74 → 62 and 43 → 31: check_global, and the *admissions* drain alone
	//
	// `is_const`'s GlobalGet arm (valid.ml:1037) over the four const-expression sites this engine's
	// internal form can hold — a global's own initializer, a data segment's offset, an element
	// segment's offset, and each expression-form element. Twelve vectors, all twelve out of
	// `validateAdmitted`, with `validateDeclineCeiling` unmoved at 31 and `validateMismatchCeiling`
	// at 0.
	//
	// **This is the exact complement of slice 5's signature, and the pair is worth reading together.**
	// Slice 5 drained `declined` alone and left `admitted` unmoved: a slice that adds vocabulary
	// converts refusals-to-answer into answers, and the unmoved admission bound is the evidence that
	// every new answer was the right one. This slice drains `admitted` alone and leaves `declined`
	// unmoved: a slice that adds a *rule* converts wrong acceptances into refusals, and the unmoved
	// decline bound is the evidence that it did so without withdrawing from any question the engine
	// already answered. The two shapes are what a vocabulary slice and a rule slice look like from the
	// stratum's side, and neither is derivable from the pass delta, which reads +12 either way.
	//
	// The twelve were pre-registered by `file:line` — derived from *module contents* rather than from
	// file location, which is Scott's condition on this slice and not a stylistic preference: only 1
	// of the 5 `constant expression required` rows and 3 of the 7 `unknown global` rows live in
	// `global.wast`, so a file-keyed forecast would have named four rows and missed eight. A
	// thirteenth family member, `global.wast:674`, was pre-registered as *not* converting and did not:
	// it is charged to the encode stratum, needing a `(table …)` field the emitter cannot yet write
	// (#8).
	// # 62 → 86 and 31 → 55: #341, and the *rise* is the assertion arriving rather than the engine
	// losing ground
	//
	// Module definitions ask the validator now (Scott's semantics ruling on #341: a
	// `KindModuleText` command means the module decodes **and** validates), and 24 of the 2150 are
	// declined — `ref_null`, `ref_func`, `ref_is_null`, `table_get`, `table_set` and eight relaxed-SIMD
	// operators, all of them rules a later slice owes. All 24 come out of `validateDeclined`; the
	// admission bound is unmoved at 31 and the wrong-message bound at 0.
	//
	// **This is the one movement on this stratum that is neither of the two signatures above**, and
	// reading it as either would be wrong. A vocabulary slice drains `declined`; a rule slice drains
	// `admitted`; this is a *question* slice — it changes what the corpus is allowed to ask — so the
	// column rises on both lanes and nothing about the validator changed. What makes the rise honest
	// rather than a regression is that it was pre-registered on #341 with its row list and its cause
	// before the arm was touched, and the two neighbouring bounds staying still is what says the
	// engine did not move: had this slice broken a rule, the new questions would have arrived as
	// over-rejections instead, in the fourth bound below.
	//
	// The 24 are the *default* lane's figure. The all-on lane declines 129, which is the same
	// population without the gates hiding four fifths of it, and that lane's fail count is printed
	// rather than bounded (TestAllGatesOnLeavesNothingGated bounds `Gated` only).
	//
	// # 86 → 39 and 55 → 8: the reference-type slice (#359), and #341's residue closes on its own list
	//
	// The five `ref.*`/`table.*` rules land, and **47 of the 55 declines convert to passes** with
	// `validateAdmitCeiling` unmoved at 31 and `validateMismatchCeiling` at 0 — slice 5's signature
	// again, at a seventh of the size: a vocabulary slice drains `declined` alone, and the two unmoved
	// neighbours are the evidence that every rule it newly typed types correctly rather than nearly so.
	//
	// **The 47 split 31 + 16 across two populations this bound cannot see, and the split is checked
	// elsewhere rather than asserted here.** 31 are `assert_invalid` heads and are pinned per
	// destination by TestAssertInvalidDestinationLedgerCloses, whose `declined` column reaches zero in
	// this diff. The other 16 are `module text` commands — #341's own contribution to this bound — and
	// they are 16 of the 24 that entry names. So this stratum's fall is the sum of a ledgered figure
	// and a pre-registered row list, neither derived from the other.
	//
	// **The residue of 8 is the rest of #341's list, closing two PRs after it was written**: the eight
	// relaxed-SIMD operators, whose gate is its own event (ADR 0025), so they are a *structural* residue
	// and not a slice's leftovers. #341 named them in the same breath as the five this slice types, and
	// the reason that matters is that a residue nobody predicted is indistinguishable from a rule that
	// was missed. This one was predicted, by name, before either PR existed.
	//
	// The all-on lane's figure is 258 → 196, and its shape is *not* this one: 107 rows drain from the
	// five mnemonics and 45 of them re-decline one instruction later on a GC or tail-call operator the
	// default lane never decodes, netting the 62 that lane's pass delta reads. A decline moving within
	// the column rather than out of it is invisible to any single figure, which is why the composition
	// was diffed rather than the total quoted — and it is why the #359 forecast's all-on prediction
	// (192) missed by 4 while its default prediction (8) was exact.
	// # 39 → 38, slice 9 (ADR 0035), and the one row that moved is the row the forecast said could not
	//
	// **The tail-call arms contribute nothing to this lane and the ADR said so correctly** — `TailCall`
	// is absent from `DefaultFeatures()`, so `return_call` and `return_call_indirect` are refused at
	// *decode* here and never reach a validator column. What the ADR then said, and got wrong, is that
	// the default lane would therefore be byte-identical: the slice also repaired grave #390, the
	// element-type `require` missing from `call_indirect`, and **`call_indirect` is MVP core**. So
	// `call_indirect.wast:994` — a `(table 10 externref)` module this validator was accepting — converts
	// on the default lane too, and it lands in `validateAdmitCeiling` (31 → 30) rather than in
	// `declined`, taking this partition's total with it.
	//
	// The shape worth keeping: a gated slice's *forecast* was reasoned from the gate map, which is sound
	// for the instructions the slice ports and silent about a grave the port happens to expose. **A
	// repair rides the lane its subject ships on, not the lane its discoverer was working in** — and the
	// discovery direction here was the reverse of the usual one, since reading `ReturnCallIndirect` is
	// what showed `CallIndirect` was short a require. Pre-registration cannot cover what the work has not
	// found yet; what it can do is make the miss a stated one, which is why this paragraph exists rather
	// than a re-based constant.
	//
	// `validateDeclineCeiling` is unmoved at 8, the eight relaxed-SIMD operators, for the reason its own
	// paragraph gives: a structural residue whose gate is its own event.
	//
	// # 38 → 36 with slice 10 (ADR 0036), and the delta was forecast from the fix's own call site
	//
	// The whole of it comes out of `validateAdmitCeiling` (30 → 28), so the account is there. What
	// belongs *here* is that the figure was **pre-registered before the measurement**, on the lesson the
	// paragraph above this one had to learn the hard way: slice 9's forecast reasoned from the gate map,
	// which is sound about the opcodes a gated slice ports and silent about anything else the PR carries.
	// Slice 10 carries #391 — `check_elem`'s missing `RefFunc` resolution, a rule about `(elem …)` and
	// not about exception handling — and `(table funcref (elem 0 0))` is MVP core, so the two rows it
	// converts were forecast to land in this lane rather than discovered in it afterwards.
	//
	// A forecast from the fix's location and not from the work's: that is the transferable half, and it
	// is the same sentence grave #390 produced read one step earlier in the process.
	//
	// # 36 → 8 with #328, and this partition is now `validateDeclineCeiling` alone
	//
	// The whole of the 28 comes out of `validateAdmitCeiling` (28 → 0), so the account is there. What
	// belongs here is what the identity between the two constants now means: this bound sums declines
	// and admissions, the admissions are zero, and the eight declines are the eight relaxed-SIMD
	// operators whose gate is its own event. So **this bound and `validateDeclineCeiling` have become
	// the same number, and they are two bounds rather than one for the reason they always were** — one
	// counts a structural residue and the other counts it plus a population that can rise. Collapsing
	// them because they agree today would delete the only instrument that can report the rise, which
	// is the movement `validateAdmitCeiling`'s own header calls the one that makes the engine less
	// correct. The agreement is asserted rather than assumed: the arm-flag reconciliation below fires
	// if the two paths ever describe different populations.
	//
	// # 8 → 0, and the word "structural" in the paragraph above is grave #427
	//
	// The eight were the relaxed-SIMD operators and they are typed now (#427). Nothing about them was
	// structural: `DefaultFeatures()` had carried `RelaxedSIMD: true` since `7315b57` (#275/ADR 0028),
	// so the decoder was admitting `fd 0x100..0x12f` and the *validator* was declining for want of a
	// rule — and `internal/validate/vecfamily.go` already held all 20 rows, in families whose arms were
	// already written, because that table's domain is the whole region rather than what its file
	// happens to type. The repair was the deletion of a guard.
	//
	// **The sentence above cost eight rows of unworked engine, and it cost them by foreclosing.** It
	// did not merely record a wrong number — a wrong number invites a re-measurement. It told the next
	// reader there was no work here, and it was written on 2026-08-17, *three days after* the flip that
	// falsified it. Its sibling in `internal/validate/vec.go` ("unreachable while the gate is off — the
	// decoder refuses these opcodes first") was written one day after. Neither was ever true.
	//
	// The word is left standing above rather than edited out, because the paragraph is testimony and a
	// corrected transcript is a worse record than an annotated one. What replaces the *practice* is
	// `internal/testenv`'s foreclosing-word sweep, which reads this file among others: a bound account
	// may say a residue is structural only where it names the mechanism that makes it so, and a claim
	// resting on a gate is checked against `DefaultFeatures()` rather than believed.
	//
	// Both lanes moved by exactly 8 — default 60914 → 60922, all-on 64985 → 64993 — and the *equality*
	// is this repair's own check, in the direction grave #130's inequality was: a validator decline
	// reads no gate, so a fix inside one must move the two lanes identically. All six figures were
	// pre-registered before the run.
	const validateFailCeiling = 0
	const validateDeclineCeiling = 0
	boardBound(t, "validateDeclineCeiling", validateDeclined, validateDeclineCeiling, 0, ceilingBound,
		"slice 1 declined more instructions than it did — either an opcode left the signature "+
			"table or a later slice's rule regressed into a decline")
	// 142 → 158 → **104**, and the middle number is the one with a stamp on it. The rise was the +16
	// the account above attributes to one cause, and a *rise* on this particular bound is the one
	// board movement that makes the engine less correct, so it was flagged for a principal rather
	// than taken: **the stamp is Scott's on PR #307**, conditioned on a named ledger. The interval it
	// spent raised-but-unauthorized was one PR wide. The fall to 104 needs no stamp — #306 landing
	// the rule is the whole event, and a ceiling that drops is a bound catching up to an engine that
	// got more correct.
	//
	// **158 and not 162, and the 4 between them are the whole reason `Failure.Accepted` exists.**
	// This bound read `validateFail − validateDeclined` and was re-based to 162 on that reading,
	// which put four *refusals* inside the constant whose documented subject is acceptance — a bound
	// measuring a population 2.5% larger than the one it names, in the direction that makes the
	// engine look worse rather than better, but wrong either way. It now counts the arm's own flag,
	// and the four have since drained: see `validateMismatchCeiling`.
	//
	// # The named ledger, and what it cost to have had one
	//
	// It is gone, and the note stays because the *shape* is the reusable part. Scott's condition on
	// taking the admission rather than the decline (#307) was that the sixteen be named, on the
	// argument that **`158` is satisfied by any sixteen vectors, including a different sixteen**: a
	// count cannot tell "the dropped alignment immediate admitted these" from "eight were fixed and
	// eight others arrived", both readings satisfy the same constant, and the second is a silent
	// regression wearing the first one's number. So `alignmentAdmissions` named all 54 sites by
	// `file:line`, the ceiling was *computed* from how many were still live, and the measured set was
	// checked in both directions. Three properties, of which the third is why that shape:
	//
	//  1. **A membership change fails.** A measured admission whose site was not in the ledger was an
	//     admission nobody had accounted for, whatever the total said.
	//  2. **A drain produced exactly one loud signal, never silence.** A slice-1 site draining left
	//     the ceiling stale against a smaller actual; a slice-2 site draining lowered the ceiling with
	//     the actual, so the three-way sum fired instead — the correct place, a drain being a stratum
	//     re-base and not an admission-bound event.
	//  3. **It retired itself.** Which is what happened: #306 made the message unreachable, the
	//     measured set emptied, the vacuity check fired, and the ledger was deleted rather than
	//     amended. The self-retiring shape 0025 used for its own carve-out — named to one numbered
	//     blocker, attribution read off the engine's own output, repealed by the blocker landing.
	//
	// What replaced it is the admission census `TestPhase1Files` prints, not a second named set. That
	// is Scott's residual flag answered — "a base that has stopped having a documented majority is a
	// count agreeing with any 104" (Note: Scott, PR #307) — by a decomposition the board regenerates
	// rather than by hand-written sites, which is the smaller instrument for the same question. Naming
	// them per site is available and is a decision to flag, not to take.
	//
	// # The decomposition was also *quoted* here, and the quote is deleted rather than re-based
	//
	// This paragraph used to carry the census inline — fifteen "N reason" rows in the board's own
	// order. It drifted twice without anything noticing, in the two ways a copied measurement drifts:
	// **silently by one** (it summed to 104 against a ceiling of 103, still listing an
	// `offset out of range` row that had already drained), and then **loudly by eight** when the
	// 17-head slice re-based the ceiling to 111 and left the list at 104. Neither drift could fail
	// anything, because the list's only reader was a human and its only check was that a reader might
	// add it up.
	//
	// So it is deleted, on the rule that a measured figure in prose is generated or it is gone
	// (Scott's rider, ADR 0029). The board prints this census on every run, in size-then-reason order,
	// and the printed copy cannot go stale against itself. A comment that restates an instrument's
	// output is a second instrument with no falsification, and this one demonstrated the failure mode
	// twice before it was removed:
	//
	//	go test ./internal/spec/ -run TestPhase1Files -v   # the `admitted N expected: …` rows
	//
	// What stays here is the *shape* claim, which is not a measurement and does not rot: the census's
	// membership is read from each bucket's key, which is the vector's own expected message, and never
	// from a filename prefix.
	//
	// A `simd_`-prefix predicate would have been a claim about the current sample rather than the
	// space, and an under-matching trigger fails silently by construction.
	// **31 → 30 with slice 9 (ADR 0035), and a fall on this bound is the rarest movement the board has.**
	// Every other slice in this campaign has drained `declined`; this is the first to convert an
	// *admission* — a module the validator was calling valid and the corpus calls invalid — and it did so
	// on the default lane, where the slice's own opcodes are not even decoded. The row is
	// `call_indirect.wast:994`, `(table 10 externref)` with a `call_indirect` through it, accepted since
	// the arm landed because the arm read the table's address type and never its element type (grave
	// #390, `valid.ml:563`).
	//
	// Two things about it are worth keeping. It was found by porting the *sibling* opcode, so **the
	// authority for a landed arm was read while implementing a new one** — a call site corrected on one
	// axis (#343 cause 2's address-type repair) was still wrong on another, and nothing in the campaign
	// would have re-read it on its own account. And it was invisible to every fail column by
	// construction: an over-*acceptance* produces no message to bucket, which is the whole reason this
	// bound is a ceiling rather than a floor.
	//
	// # 30 → 28 with slice 10 (ADR 0036), and it is the second consecutive fall on this bound
	//
	// Two rows, both `(module (table funcref (elem 0 0)))` in shape, accepted since the element slice
	// landed because `check_elem`'s `check_const` walk resolved a segment's *globals* and never the
	// function indices its `ref.func` initialisers name (#391, `valid.ml:1097-1101`). They convert out of
	// this bound and into `pass` with `declined`, `mismatch`, `gated` and `total` all unmoved, which is
	// this table's signature for a correctness gain: two rules became right and none became known.
	//
	// **The slice's own subject contributed nothing to this lane**, exactly as slice 9's did not: the
	// exception-handling gate is off by default, so `throw`/`throw_ref`/`try_table` and the tag section
	// are refused at decode here and the family's whole reward is in the all-on lane (25 declines and 4
	// admissions, ADR 0036's criterion). What moves the default lane is the rider, and the rider was
	// named in the forecast rather than found in the diff — see `validateFailCeiling` above for why that
	// ordering is the lesson and not the arithmetic.
	//
	// Two consecutive slices whose *default-lane* delta is entirely a rider's is now a pattern worth
	// stating: a gated slice's default-lane forecast is a forecast about **everything else in the PR**,
	// and reading it off the gate map returns zero every time.
	//
	// # 28 → 0 with #328, and a bound that reaches zero has to say where its subject went
	//
	// The const-expression typing slice converts all 28 at once, which is the largest single-destination
	// correctness gain this campaign has recorded and the last one available on this lane. Four rules,
	// each a reference line this engine did not have: `check_const`'s typing half (23 rows),
	// `check_import`'s `ExternFuncT` arm (2), `check_elemmode`'s `match_reftype` (2), and `ref.func`
	// resolution inside a const expr (1). Forecast at 28 → 0 before the rules were written and landed
	// there exactly.
	//
	// **A ceiling at 0 is a real assertion and not a lost instrument, but it is no longer a work plan**,
	// which is the distinction `declined`'s own drain to zero (the reference-type slice) had to make and
	// this bound now inherits. As an assertion it is the strongest form this bound has ever taken: any
	// rise at all is a regression, where at 28 a rise of 1 was indistinguishable from a slice not having
	// landed yet. As a work plan it has no subject — and the subject moved to the **all-on lane**, where
	// the same census reads 28 after this slice took it from 66. That number is measured on both sides
	// against `main`, not inferred: 66 → 28 there, all 38 out of `accepted`, with `allOnPassFloor`'s +38
	// and the all-on fail count's −38 closing the arithmetic from two other directions.
	//
	// The 28-and-28 is a coincidence of two disjoint populations and is stated as one because the
	// numbers invite the other reading — that this lane's 28 simply moved lanes. They did not: the
	// all-on residue is 14 `unknown type`, 9 `type mismatch`, 4 `uninitialized local` and 1 `constant
	// expression required`, none of which is a vector this lane can ask, since every one of them needs a
	// gated feature to decode. An identical count on two lanes is exactly the shape a mis-scoped census
	// produces, so it was measured rather than trusted.
	const validateAdmitCeiling = 0
	boardBound(t, "validateAdmitCeiling", validateAdmitted, validateAdmitCeiling, 0,
		ceilingBound,
		"the validator accepted an invalid module it used to refuse. This is the accept direction: "+
			"a rise here is the one board movement that makes the engine *less* correct while the "+
			"total holds still")
	// The third population: 0 → 4 with slice 2, and **back to 0 with #306**.
	//
	// **The judgement call that made this a ceiling rather than an equality is what let the drain
	// land as a re-base**, and it is worth reading now that the drain has happened. An equality would
	// have been the stronger instrument on the rise and would also have fired on the fix, which is the
	// direction this bound wants to encourage; a ceiling with slack 0 catches the rise — a right
	// refusal turning into a wrong message is a regression in testimony (0003) whether or not the
	// verdict moved — and reports the drain as staleness.
	//
	// **At 0 it is a bound with nothing under it, which is the state its own history says to be
	// careful about.** The paragraph above `validateFailCeiling` records that this population was
	// exactly 0 at slice 1 and was *not being watched*: the four rows landed inside the
	// accept-direction constant, and only the destination ledger's per-key count disagreed. That is
	// why the bound stays at 0 rather than being deleted for want of a subject — the third verdict
	// needs a structural bound, and a population that has been empty before and refilled is precisely
	// the one to keep an instrument pointed at.
	const validateMismatchCeiling = 0
	boardBound(t, "validateMismatchCeiling", validateMismatched, validateMismatchCeiling, 0,
		ceilingBound,
		"the validator refused a module with a message the corpus disagrees with. The verdict is "+
			"right and the testimony is not, which is the failure a pass/fail column cannot show: "+
			"these rows are fails, and a rise here can be a *message* regression on a vector that "+
			"was already refused correctly")
	// The fourth population, and it is **0 in this lane while being 13 in the other** — which is the
	// one thing about this bound a reader has to hold in mind before trusting its silence.
	//
	// An over-rejection is the accept direction's own defect: validation ran to completion and refused
	// a module the corpus asserts is valid. `validateAdmitCeiling` above watches the mirror
	// (accepted-and-shouldn't-be) and has watched it since slice 1; nothing watched this side at all
	// until #341, because no command asked the validator a question whose right answer was yes.
	//
	// **The 0 here is honest and it is not the finding.** All 13 rows #341 measured need features the
	// default lane gates off — GC's recursive type equivalence, memory64's 64-bit table index type at
	// `call_indirect`, `(ref null none) <: (ref null any)`, element-type subtyping at `table.copy` —
	// so they are invisible in this lane by construction and not by the instrument being blind. The
	// all-on lane is where they live, and TestModuleDefinitionsAskTheValidator is what pins them by
	// row: a bound at 0 whose population is elsewhere would otherwise be the unasserted distance,
	// running and agreeing and saying nothing.
	//
	// A rise *here* therefore means something sharper than a rise in the all-on figure: it means the
	// validator began refusing a module built only from instructions this engine ships on by default.
	const validateOverRejectCeiling = 0
	boardBound(t, "validateOverRejectCeiling", validateOverRejected, validateOverRejectCeiling, 0,
		ceilingBound,
		"the validator refused a module the corpus says is valid, built from default-lane features "+
			"only. This is the accept direction's own defect and the reason #341 exists: an "+
			"over-rejection produces no error for anyone to bucket, so it is invisible until a "+
			"module definition is scored on the validator's answer")
	// **Five constants now, where four of them used to be two plus a derived quantity.** While the
	// alignment ledger existed, `validateAdmitCeiling` was computed from its live members, and this
	// identity was the check that fired when one drained — the intended loud signal, landing in the
	// right place, a drain being a stratum re-base and not an admission-bound event. The ledger has
	// retired, so all sides are written down by a person again, and the identity is back to its
	// plainer job: it catches a re-base of one part that forgot the others.
	//
	// Do not make `validateFailCeiling` derived to save the arithmetic. A partition whose every part
	// is computed from the measurement is a tautology, and this identity's whole value is that both
	// sides were asserted independently.
	if validateDeclineCeiling+validateAdmitCeiling+validateMismatchCeiling+
		validateOverRejectCeiling != validateFailCeiling {
		t.Errorf("the four validator ceilings (%d declined + %d admitted + %d wrong-message + "+
			"%d over-rejected) sum to %d but the stratum's own is %d; one was re-based without the "+
			"others, so the sub-partition no longer decomposes the column it claims to",
			validateDeclineCeiling, validateAdmitCeiling, validateMismatchCeiling,
			validateOverRejectCeiling,
			validateDeclineCeiling+validateAdmitCeiling+validateMismatchCeiling+
				validateOverRejectCeiling, validateFailCeiling)
	}
	boardBound(t, "validateFailCeiling", validateFail, validateFailCeiling, 0, ceilingBound,
		"the validator answered fewer assert_invalid vectors than it did; the three bounds above "+
			"say which part moved, and this one exists so that a movement between them cannot "+
			"net out")

	// Pass floor over the whole board, the counterpart to TestBinaryWast's per-file
	// floor.
	//
	// **1419 = 783 + 636, and the 636 is the forecast reconciled rather than absorbed.**
	// 783 is the pre-reader board: 764 from the byte-string corpus plus the 19 the
	// derived selector newly reaches, unmoved by the quote admission because admitting a
	// corpus earns no verdicts. The 636 the reader earns is 629 vectors answered through
	// an error whose text matches, plus **7 bare `(module quote ...)` forms that lex
	// clean and are unearned** — six in annotations.wast, one in comments.wast. None of
	// the seven turns on lexing; they are valid modules whose validity the lexer cannot
	// assess, and they pass because the parser's absence means nothing contradicts them.
	// Named here rather than netted out of the total: they are overfitting arrived at by
	// omission, and they will stop being free the moment #8 can disagree with them. They
	// are pinned individually by TestBareQuoteFormsPassUnearned so the seven cannot grow
	// quietly into a habit.
	//
	// **1628 = 1419 + 210 − 1 after #62's module-field parser**, and the arithmetic is the
	// point: 210 vectors answered on the merits, and **one pass given back** —
	// `comments.wast:83`, an unearned pass the parser retired by reaching it and declaring
	// the boundary. A floor is normally raised by only counting what was won; this one
	// records the withdrawal in the same expression, because netting it out would let a
	// green claim credit for a vector the board no longer answers. The unearned six are
	// still six, still named, and still not netted out. See textFailCeiling above for the
	// itemized reconciliation against #62's 221–225 forecast.
	//
	// **1922 = 1628 + 294 after #63's flat instruction grammar**, and this time the two columns
	// move by the same number in opposite directions: 294 answered, nothing withdrawn. The
	// per-bucket account is at textFailCeiling above.
	//
	// The seven unearned quote forms are **seven again, not six**, and that is a real change
	// rather than a rounding of the sentence above. `comments.wast:83` went back onto the
	// unearned list: #62 retired it by stopping at the instruction body, #63 reads that body, so
	// the vector is accepted again — and a bare `(module quote ...)` asserts *validity*, which
	// wants a typechecker. So the pass is unearned once more rather than earned, the withdrawal
	// recorded in #62's `− 1` is handed back, and the 294 is a gross figure that needs no
	// netting. TestBareQuoteFormsPassUnearned holds the sum at seven and has the argument.
	//
	// **1941 = 1922 + 17 + 2 after the flat block family**, and the split matters more than the sum:
	//
	//	17  blockinstr and the block family — #63's own Scope, mis-assigned to #64 in the
	//	    reconciliation above until the flat/folded classification was measured
	//	 2  try_table.wast:366/:371, found by a control rather than by the board — the boundary
	//	    was claiming a handler clause in instruction position, which #70 generalizes
	//
	// Nothing withdrawn either time, so this is gross like the 294. The two-from-a-control rows are
	// worth naming separately: they are vectors the *suite* had all along and the *board* could not
	// point at, because a bucket keyed on the expected string cannot distinguish "we have not
	// written that reader" from "we are wrong about which reader would answer it".
	// **1953 = 1941 + 12 after the derived instruction boundary (#70)**, gross again — nothing
	// withdrawn, checked per file. All 12 are func.wast field-ordering vectors, itemized by line
	// at textFailCeiling above along with why the "zero vectors" forecast was wrong.
	//
	// Worth recording as a *measurement* rather than as a sum: the 12 were found by patching the
	// boundary and reading the board, after a hand-probe of five spellings said none existed.
	// Two claims of mine were falsified by running the check instead of reasoning about it, and
	// this is the second — the first was in #63's label readers. The board is the instrument.
	// **1992 = 1953 + 39 after the folded/sugar stratum (#64, first half)**, gross again — nothing
	// withdrawn, and this time the per-file check is the *whole* evidence rather than a footnote,
	// because a stratum that touches five files at once is exactly where a quiet withdrawal in a
	// sixth would hide. Diffed file by file against the pre-change board: five lines moved, all
	// upward, summing to 39. The itemization and the forecast's two-vector error are at
	// textFailCeiling above.
	//
	// The 39 are the folded spellings of a grammar #63 had already made correct — `blockSignature`
	// and its bindidx rejection were landed then, so the folded arms mostly needed *routing* to the
	// existing reader rather than new rules. That is why the fall is one PR rather than three, and
	// why the controls that matter here are agreement controls (TestFoldedAndFlatSignaturesAgree)
	// rather than verdict controls: the risk was a second implementation, not a wrong one.
	// **4122 = 1992 + 2130 after the bare-module admission (#69).** The largest single move
	// this floor has made, and it is *earned coverage rather than earned correctness*: not
	// one line of the reader changed: 2130 modules the parser already accepted became
	// *scored* instead of invisible. The board did not get better, it got honest.
	//
	// Decomposed, because a floor is only an assertion if it knows what it is bounding:
	// within the 68 files already on the board, pass rose 1992 → 3106 (+1114 of the 1119
	// bare modules there, the other 5 failing); the 185 newly-admitted files bring 1016 more.
	// 1114 + 1016 = 2130.
	//
	// This is the number the #64-second-half work will be measured against, and that is the
	// point of doing #69 first. Resolution is a **rejector**: it can only turn passes into
	// fails. Installing one while the accept oracle was 7 vectors would have made
	// over-rejection invisible by construction — the overfitting law (§9 G-3) at its purest,
	// since the cheap wrong check and the correct one score identically on a corpus that
	// asks nothing. Against 2130 must-succeed modules, an over-eager resolver fails loudly.
	//
	// **4147 = 4122 + 25 after typeuse resolution and functype equality (#64, second half)**,
	// gross — nothing withdrawn, checked file by file: six files moved, all upward, summing to
	// 25, and the per-file decomposition is at textFailCeiling above.
	//
	// **The floor is where #69's argument gets paid off, and it did its job.** Resolution is a
	// rejector, so the interesting column here was never the 25 — it was the 2130 bare modules
	// that had to keep passing. They did, and the falsification pass proves the check has teeth:
	// making `resolveTypeIdx` run at the typeuse instead of in `runDeferred` — the single most
	// natural way to write this wrong, and the way that reads more simply — drops this count to
	// 4145 on `imports.wast:62`'s forward reference. Under the 7-vector accept oracle that
	// preceded #69 the same defect would have been a silent green. One vector of 2130 is a thin
	// margin, and it is a real one.
	//
	// **4156 = 4147 + 9 after lane_imms (#76)**, and this floor is where that fix is *certified*
	// rather than merely observed: all nine vectors are must-succeed, so the entire finding lives
	// in this column and none of it in the fail bucket's key. The board cannot distinguish a
	// correct `lane_imms` from one that stopped reading a memory index altogether — eight of the
	// nine files write only the bare arm — which is why the arm-by-arm control in
	// `internal/text/instr_test.go` is the actual evidence and this number is the receipt that it
	// did not cost anything elsewhere.
	//
	// **4159 = 4156 + 3 after elem_list (#75)**, and all three are must-succeed modules, so — as
	// with #76 — the whole finding lives in this column. The pattern across both graves is worth
	// naming: **every defect #69's admission surfaced is an over-rejection**, which is the class a
	// reject-direction corpus is structurally blind to. Two graves, twelve vectors, and not one of
	// them would have been visible on the 7-module accept oracle that preceded it.
	//
	// **4161 = 4159 + 2 after symbolic label resolution (#80)**, and this is the *third* rejector
	// installed against this floor, so #69's argument earns its keep once more: the two vectors that
	// move are must-fail, and everything the change could break is must-succeed. It broke none.
	//
	// The accept direction is where the label work is actually at risk, and the floor is most of the
	// evidence: the reference binds a label at four sites this parser has to mirror (an anonymous one
	// at each unnamed block, one at every `func`, a *cleared* space at `enter_func`, and `catch`'s
	// target resolved in the **outer** context), and getting any of them wrong rejects legal modules
	// while still scoring 2/2 on the vectors that named the feature. Which is why the mechanism
	// controls in `internal/text/label_test.go` are scoped to those four facts and not to the two
	// vectors: the two vectors are the reward, the 2130 bare modules are the job.
	//
	// **How sharp each of the four is was measured, and the first draft of this paragraph got it
	// wrong.** It said `foldedBlock`'s push was the sharpest case because dropping it "costs nothing
	// in the fail bucket and shows up here" — an inference from the two vectors' spelling, not a
	// reading. Dropping it moves this count 4161 → 4077 and the *fail* bucket 2 → 86, all 84 landing
	// in `(module <wat body>) must read`: a rejector that over-rejects turns must-succeed modules into
	// failures, so it is loud in **both** columns, and the "invisible in the fail bucket" half was
	// simply false. The board is the instrument, again — the third time on this floor that reasoning
	// about a check was corrected by running it.
	//
	// The probe's other three arms are the finding worth keeping. `catch`-in-the-outer-context is
	// package-visible only (6 reject rows in label_test.go, nothing on the board). And **two of the
	// four are not falsifiable at all**: `funcField`'s label reset and its anonymous push can each be
	// dropped with the board at 4161/2 and `./internal/text/` green, because every push site pops
	// under `defer` and a func has no enclosing scope to leak into. Recorded here rather than only at
	// the definition site, because a floor that claims to be the evidence for four facts is
	// overclaiming when it is the evidence for one; the argument for keeping the unfalsifiable two is
	// in `funcField`'s header, where it can be read next to the lines it defends.
	//
	// **4161 → 4162 with grave #83**, and the one vector it adds is the whole `annotations.wast:1`
	// module: `scanAnnotBody` carried `token`'s bare-`(@` error arm (lexer.mll:829), which the
	// `annot` rule does not have (:850 and :855 are its only two), so `(@)` nested in a body was
	// rejected and the file's leading must-succeed module went with it. Same over-rejection shape
	// as the `foldedBlock` measurement two paragraphs up, and the same reason it is visible here:
	// one rejected legal module is one pass, and rejecting legal input is what this floor watches.
	// # 4162 → 17923, and 13761 of those are values the engine computed
	//
	// **The first movement on this floor that is not about a front end.** Every raise above was
	// a rejector installed or an over-rejection repaired; this one is 13761 `assert_return`
	// vectors where a function ran and returned the bits the suite asked for. The floor's
	// character changes with it: up to here it watched for over-rejection, and it now watches
	// for that *plus* an interpreter that starts computing a different answer.
	//
	// The number is not decomposed further because the decomposition that matters is the
	// **fail** side's — an interpreter that returns a wrong value produces a member of
	// `assert_return value mismatch`, and there are **0** of those. That zero is the sharper
	// claim: 13761 vectors compared bit-for-bit against expectations read by an independent
	// literal reader (see value.go for why the reader is independent, and grave #106 for the
	// echo it exists to avoid), and not one disagreed.
	//
	// Falsified by perturbing `binI32`'s 0x6a arm to `a + b + 1`, and the measurement corrected
	// this paragraph's first draft. It predicted the bucket would fill "while this floor barely
	// moves" — reasoning from the 13761-vector slack rather than from the mechanism, and wrong,
	// because a floor fails on *any* drop regardless of slack. What actually ran: 10 vectors
	// into `assert_return value mismatch`, `execFail` 356 → 366 tripping execFailCeiling, and
	// this floor 17923 → 17913, failing. Three instruments, three fails, and the sharpest is
	// the ceiling — 10 against a bound of 356 versus 10 against a floor of 17923. Stated
	// because a floor that claims to be the evidence for a property it is the weakest witness
	// to is overclaiming.
	// # Re-based 17923 → 26307 by #7's memory, and the +8384 is one family
	//
	// Load and store arms, `memory.size`, `memory.grow`, memarg addressing, OOB traps and
	// instantiation-time data-segment copying: one family, and the pass column's largest single
	// jump. The account is on the exec ceiling below, which fell 8713 → 440 in the same motion —
	// the two are the same 8,000 vectors seen from opposite sides, which is why a rise here and a
	// fall there is one fact and not two.
	//
	// **The gain was measured rather than forecast, on Scott's rider, and the census was generous
	// by 289 in one direction and blind by 136 in the other.** Pre-registered: ~8,424 payable,
	// 289 residue. Delivered: exec 8713 → 440, the 289 residue exact — and two buckets the census
	// had not predicted at all, 95 value mismatches and 41 memory-index errors, where 95 had been
	// **zero** before this PR. Quoting the drop without them would have been the flattering half
	// of a measurement. Partitioned: 22 were imported memories shifting the memory index space
	// (fixed, class now 0), 73 were the harness never running a bare top-level `(invoke …)` — the
	// engine right, the jig not having set the state up — and the 48 that remain are all
	// downstream of the missing `fc 0a`/`fc 0b` arms and say so.
	// # Re-based 26307 → 26567 by the block family, and +3 of the 260 are a grave
	//
	// `block`/`loop`/`if`/`br`/`br_if`/`return`/`select` on both sides (#7). **+257 from the
	// family and +3 from grave #135**, which is stated apart because the two are different kinds
	// of gain: the 257 are vectors that could not be built or could not be run before, and the 3
	// are `comments.wast` modules this engine was *rejecting* — a `return` that did not truncate
	// the value stack left extra values behind, and `Invoke`'s arity check then called a valid
	// module unvalidated. Accept-direction, so no `assert_invalid` vector could have found it;
	// the reject-direction board could not either, since it scored them as fails under a
	// perfectly honest-looking layering-debt string.
	//
	// The other side of the same motion is the exec ceiling below, 440 → 662: vectors that now
	// instantiate and run reach value comparisons they could not reach before. A rise here and a
	// rise there is one fact this time rather than opposite views of one — the family unblocked
	// both the vectors that agree and the vectors whose remaining gap is an opcode.
	// # Re-based 26567 → 26833 by `br_table`, and the +266 is a fifth of what the bucket held
	//
	// `br_table` end to end (#8, decision 0016): the label vector retained in `Func.Labels`, the
	// interpreter's `0x0e` arm, and the encoder's three text-to-wire transformations. The bucket
	// `cannot yet encode the br_table instruction's immediates (#8)` held **1330** and is now
	// **0** — and 266 of those became passes, which is the number worth stating precisely because
	// it is not 1330.
	//
	// **The other 1064 are accounted for individually rather than described**, a full bucket-set
	// diff showing exactly three nonzero deltas: **+1006** to `cannot yet encode the
	// call_indirect instruction (#8)`, **+8** to `interp: no arm for opcode 10`, and +50 to
	// `gated` (see TestGatedVectors' new `align0`/`align64` entries, whose causes were read off
	// the engine's own decline strings). 266 + 1006 + 8 + 50 = 1330.
	//
	// That is the **dependency closure**, and it is why this landed as its own PR: `br_table.wast`'s
	// own module calls `call_indirect`, so the file is still 1/147 with all 146 fails re-keyed to
	// the instruction it now waits on. A forecast that quoted 1330 for this PR would have been
	// counting vectors blocked on the *next* one — the bucket estimated the reward, and the
	// mechanism partition estimated the job.
	// # Re-based 26833 → 27451 by `call_indirect`, and 618 of a 2255-vector bucket
	//
	// `call_indirect` and `return_call_indirect` end to end (#8, decision 0016) plus the element
	// section the indirect call needs to have anything to dispatch through. The two buckets
	// `cannot yet encode the call_indirect instruction (#8)` (2213) and `…return_call_indirect…`
	// (42) are both **0**; 618 of those 2255 became passes.
	//
	// **The other 1637 are accounted for individually**, a full cause diff rather than a
	// description: +469 to the exec column (partitioned on execFailCeiling above, where the
	// largest class is 420 honest §3 linking declines in the two table_copy files), +996 re-keyed
	// to `(global …)` — the next unwritten section, since the encoder's frontier message names
	// the sections it *does* write and the vectors move to the first one it does not — +190 to
	// `ref.null`'s immediates, and the rest across smaller #8 frontiers. 618 + 469 + 996 + 190 =
	// 2273, which overshoots 2255 by 18 because 18 of the arrivals came from *other* buckets
	// draining in the same motion, not from this one.
	//
	// That last clause is the reason the arithmetic is written out rather than summarized: a
	// bucket-to-bucket flow does not conserve, because more than one bucket moved. The columns
	// are what conserve — fail 4319 → 3682 (−637) against pass +618 and gated +19, and 618 + 19 =
	// 637 exactly. When a per-bucket sum and a per-column sum disagree, the column is the
	// conserved quantity and the bucket flow is a story about it.
	//
	// The file-level shape, since 618 across 16 files is not 618 across the board: +197 each in
	// `table_copy.wast` and `table_copy64.wast`, +95 `left-to-right.wast`, +68 `endianness.wast`,
	// +16 `func_ptrs.wast`, +12 `func.wast`, +11 `elem.wast`, +7 `call_indirect.wast`, and 15
	// spread over eight more. `call_indirect.wast` itself is 21/128 — its own file is the *least*
	// improved of the large ones, because its remaining 107 reds are `(global …)` and `ref.null`
	// frontiers rather than the instruction it is named for. A PR that quoted its own file's
	// gain would have reported 7.
	// **Section 6 plus `ref.null`'s heaptype (#8): 27451 → 28398, +947.** The columns conserve and
	// that is the check the bucket flow is a story about: fail 3682 → 2568 (−1114) against pass +947
	// and gated +167, and 947 + 167 = 1114 exactly.
	//
	// The −1114 is *the whole* of two buckets leaving the board, set-differenced on `(file, line)`
	// keys rather than netted out of the total: **966 `(global …)` field** and **148 `ref.null`
	// immediates**, 966 + 148 = 1114. Both buckets go to zero; nothing partial remains of either.
	//
	// The predecessor PR (#148) forecast these at 996 and 190 while re-keying them, and both were
	// **over** — by 30 and 42. Worth recording rather than quietly correcting, because the direction
	// is informative: a bucket keyed on a module's *first* refusal is an upper bound on what draining
	// it pays, since some of its members have a second unencodable construct behind the first. Here
	// the residue is the GC-parameterized cases — 39 `heap type N as ref.null's immediate` and 6
	// `global N: type (ref N)` — which stay red under the default lane's gates and are now charged
	// to *their own* buckets rather than to these two.
	//
	// The +167 gated is not a side effect to wave at: those are vectors that got far enough to meet
	// a declined feature, which is a gate reporting honestly on a module it can finally see.
	//
	// The file-level shape, since 947 across 16 files is not 947 across the board: +121 `if.wast`,
	// +110 `select.wast`, +106 `call_indirect.wast`, +87 `br_if.wast`, +80 `nop.wast`, +77
	// `loop.wast`, +76 `br.wast`, +68 `call.wast`, +63 `return.wast`, +54 `local_tee.wast`, +51
	// `block.wast`, +36 `load.wast`, and 18 over four more. `call_indirect.wast` gaining 106 in a PR
	// about the global section is the same lesson #148 recorded from the other side: its remaining
	// reds were never about `call_indirect`.
	//
	// **`global.get`/`global.set` (#7): 28398 → 28414, +16, and all sixteen are exec→pass.** The
	// gain equals the exec column's fall exactly (1215 → 1199) with encode unmoved at 1353, so no
	// vector paid for another: this is the arithmetic that distinguishes an arm landing from a
	// reclassification. Set-differenced on `(file, line)` keys, arrivals were **zero**.
	//
	// The +16 lands thinly across eleven files — 3 `nop.wast`, 2 each in `stack.wast`, `select.wast`
	// and `if.wast`, 1 each in seven more — which is the signature of an opcode used *incidentally*
	// rather than of a feature file draining. `global.wast` itself gains none of the sixteen: it
	// reaches 16/18 with its last two fails on the encoder's `(table …)` field, a different
	// frontier, so the file named after the feature is not where the feature's arm paid off. See
	// execFailCeiling for the other half of this account — 13 further vectors changed cause without
	// changing verdict, which the pass column cannot show.
	//
	// **The saturating truncations (#7): 28414 → 28594, +180, all of it exec→pass.** The gain equals
	// the exec column's fall exactly (1199 → 1019) with encode unmoved at 1353, and unlike the
	// globals PR the set-difference needs no third category to close: 180 departed, 0 arrived, 0
	// re-caused. See execFailCeiling for why the flat account is itself the finding.
	//
	// All 180 land in `conversions.wast`, which reaches **527/527**. One file, one bucket family,
	// eight buckets to zero — the concentrated case, where #152 was the diffuse one (+16 across
	// eleven files, none of them `global.wast`).
	//
	// **The bulk trio (#7): 28594 → 29005, +411, all of it exec→pass.** Set-differenced on
	// `(file, line)` keys against the same tree with the three arms removed — both revisions
	// measured in one working tree rather than against a commit, so the corpus and the encoder are
	// held fixed by construction. **411 departed, 0 arrived, 60 same-key-new-reason**, and encode
	// held at 1353.
	//
	// The 60 is the account's substance and it is a *single* finding: every one is in
	// `table_copy.wast` (30) or `table_copy64.wast` (30), and every one moves from `assert_return
	// value mismatch` or `trap: uninitialized element N` to `table slot N names function M, which
	// is an import`. Before the arm, those read-backs found an untouched slot; now the copy happens,
	// moves a slot that holds an *imported* function reference, and `call_indirect` meets the
	// linking frontier (contract §3). So the arm did not fail to help them — it moved them one layer
	// down, and the number that will collect them is #7's linking work, not this one's.
	//
	// Worth stating in the taxonomy's own terms: a re-caused vector is neither a win nor a loss,
	// and a PR that reported +411 without the 60 would be honest about its column and silent about
	// having relocated a frontier. The distribution says which — 60 of 60 in the two table_copy
	// files, zero in the six memory files, because memory has no reference slots to relocate.
	// **The `assert_trap (invoke …)` Kind (#157): 29005 → 31898, +2893, and none of it is an arm.**
	// The Kind admits 4876 commands the classifier used to decline; they partition 2893 pass + 1440
	// fail + 543 gated, closing against the unsupported drain exactly (see unsupportedCeiling).
	// This is the *admission* signature rather than the arm signature — the exec column rose by 18
	// instead of falling, because a question that was never asked cannot have been failing.
	//
	// Why an admission converts a majority to pass here, where the quote-form admission converted
	// almost none: a trapping call needs no value comparison. The vector asserts that the engine
	// traps and that the trap's text contains a phrase, so every module the encoder can already
	// build and every opcode the interpreter already has answers on the merits immediately. The
	// 1440 that fail are 1422 encode-column vectors (no instance) plus the 18 in execFailCeiling.
	//
	// **Zero of the 2893 passed by text coincidence, and that is now a property of the harness
	// rather than of today's engine.** A non-trap error whose message happened to contain the
	// expected phrase would score green invisibly — contract §9 G-3, the accept direction the suite
	// cannot falsify — so the run loop asks `Engine.IsTrap` *before* the substring match. The
	// recon measured the coincidence count at zero against this engine; injecting the predicate is
	// what makes it zero against every future one.
	//
	// **The bulk-segment pair, `table.init`/`memory.init` end to end (#8, #7): 31898 → 33356,
	// +1458.** Encoder emission for both instructions plus their `elem.drop`/`data.drop` arms, so
	// the columns move in the encode-drain shape rather than the arm shape: **fail 3401 → 1699
	// (−1702)** against **pass +1458 and gated +244**, and 1458 + 244 = 1702 exactly.
	//
	// The +244 gated is the largest single batch this board has admitted, and it is not the pass
	// column's leftovers: every one of the 244 was a *fail* at the parent — 151 quoting `cannot yet
	// encode memory.init (#8)` and 93 `cannot yet encode table.init (#8)` — so these are modules
	// the encoder can now build well enough for a declined feature to be reached and reported.
	// Probed rather than inferred from filenames: 232 are memory64, 12 multi-memory. See
	// TestGatedVectors for the per-file account and for why `memory_init0.wast` is a multi-memory
	// file despite its name.
	//
	// The 1458 splits 1453 out of the encode column and 5 out of exec, which is the whole of the
	// drop arms' contribution — see encodeFailCeiling for why that five is written separately
	// rather than netted, and execFailCeiling for the 84 arrivals it is entangled with.
	//
	// **`unsupportedCeiling` is unmoved at 27501**, in that word, and the reason is structural
	// rather than a shortfall: every vector this PR touched was already being *asked* and already
	// answering `fail`. An encoder frontier is a fail, not an unsupported — the classifier declined
	// nothing new and admitted nothing new — so a PR that drains 1702 fails can leave the
	// unsupported column exactly where it was. This is product work by the phase rule (an
	// interpreter arm and its encoder), so the column being flat is a fact about *which* column
	// measures this kind of progress, not a confession of overhead.
	//
	// # 33356 → 34097, +741, and this time the unsupported column moves with it
	//
	// The registry (0017 Q1) is an **admission** and a **conversion** in one PR, which is the
	// vocabulary Total() defines and the reason both columns move: 402 commands the classifier
	// declined now exist as questions (`unsupportedCeiling`, −402), and the linker answers vectors
	// that were already being asked (`execFailCeiling`, 705 → 248).
	//
	// The +741 is 696 from Q1 plus 45 from the link-error wording, and the second figure is the
	// one worth stating separately: the linker's two failure messages were **invented strings where
	// the spec has two of its own**, `unknown import` and `incompatible import type` (`eval.ml`'s
	// two `Link.error` cases). `assert_unlinkable` is the arm where that is *oracle-covered* — its
	// expected text is the whole sentinel, not a prefix — so 29 vectors were failing on the wording
	// alone while the verdict underneath was right. Grave #36's fabricated evidence in the one
	// place the suite can see it (#38's refinement), and the fix is 46 departures against 1
	// arrival.
	//
	// **The single arrival is a coincidental pass becoming an honest fail, and it is the more
	// interesting half.** `linking3.wast:39` expects `out of bounds table access` from a module
	// importing `"Mm" "mem1"`; `$Mm` is gate-declined on this board, so the import is unsatisfied.
	// The old code left the slot nil and instantiated anyway, reached the element segment, and
	// trapped out of bounds — the expected string, arrived at through a module the spec says is
	// unlinkable. Refusing at link time converts it to a named fail. A pass given back rather than
	// netted out, for the reason the `− 1` above records: a green earned by the engine being wrong
	// twice is not a green.
	//
	// 66 of the resulting `unknown import` fails are **cascades** — the name *is* registered in the
	// script, but the register bound nothing — and zero are genuine unknowns, which is the register
	// arm's predicted cluster now arriving with an attributable cause instead of silently. 49
	// cascade from an encoder frontier (35 from `imports.wast`'s `(tag …)` fields alone) and **17
	// from a gate-declined supplier**. The 17 stay honest fails rather than being gated: verified
	// against the structural control, the all-on lane reports **0** `unknown import` fails across
	// `linking{1,2,3}.wast` and `memory64-imports.wast`, so each of the 17 is simultaneously
	// answered on the merits and cannot become a disappearance — which is exactly what
	// TestAllGatesOnLeavesNothingGated is for and why the deferral did not need a second mechanism.
	// **34308 → 58429, the SIMD gate's default-on flip (#227/ADR 0025).** Pre-registered in
	// #227's own redistribution forecast and reconciled to the actual post-flip board, both
	// architectures: pass +24121, fail +161, gated −24282, unsupported +0, summing to zero
	// against the fixed 65022 total. The forecast's own fail delta (161) is exactly the
	// #9-orthogonal population ADR 0025's carve-out names — see execFailCeiling's own comment
	// two tests up for the fail-side accounting.
	// **58429 → 58659, the relaxed-SIMD gate's default-on flip.** Pre-registered in the flip
	// PR's redistribution forecast and reconciled to the actual post-flip board on both
	// architectures: pass +69, fail +0, gated −69, unsupported +0, summing to zero against the
	// fixed 65021 total. The 69 are the whole of `wholeFileGated`'s six relaxed entries
	// (2+5+10+11+17+24), which drain to an empty map at the same commit.
	//
	// **Raised even though it would have passed**, which is the part worth recording: 58659 −
	// 58429 is a distance of 230 against a `boardBoundSlack` of 250, so the stale-check had 20
	// to spare and would have stayed silent. A bound that survives a jump by 20 is the 798-
	// against-4178 shape at an earlier stage, and the slack is a tolerance for drift between
	// PRs, never a licence for the PR that *causes* the jump to skip its own accounting.
	//
	// **58659 → 59682, #9 slice 1's `assert_invalid` arm**, and the +1023 needs its two halves
	// stated separately because one of them is not a conversion:
	//
	//   - **900 conversions** off `unsupported`: vectors the harness already saw, now answered.
	//   - **123 admissions**: two files join the board (`unreached-invalid.wast`,
	//     `memory_size3.wast`, 254 → 256 files) because neither held a scorable command before
	//     this arm existed. Their commands *begin* to exist, so `Total()` rises and no column
	//     falls — 0015's data1.wast case again, and the reason the difference is named rather
	//     than netted: `unsupported` falls 2574 while pass+fail rises 2234, and the 340 gap is
	//     the gated 463 minus the 123 admissions.
	//
	// **18 of the 1023 are answered above the validator, and they are named rather than
	// absorbed.** An `assert_invalid` whose module the encoder or the decoder refuses still
	// reaches the substring match, and 18 such refusals quote the string the vector expected: 17
	// `constant expression required` from the decoder's own init-expression check, and one
	// `unknown global` from the wat encoder's symbol table. The verdicts are right and the
	// messages are the reference's, so these are passes on the merits — but they come from a layer
	// the reference validates in, which makes them evidence about where a rule currently lives.
	// Pinned by TestAssertInvalidPassesFromAboveTheValidator, and the encoder one is why this PR's
	// `encodeFail` forecast of +11 landed at +10.
	// **59682 → 60330, #9 slice 2's 0xFD region (#305): +648, all of them conversions.**
	//
	// No new files and no admissions this time — every one of the 648 is a vector already on the
	// board as a `validate` decline that is now answered, which is why this delta needs no
	// decomposition where slice 1's needed three. The mirror figure is `validateDeclineCeiling`
	// falling 1059 → 391, and the two are the same 648 seen from either side plus the 20 that went
	// the other way; the ledger is at that constant.
	//
	// `unsupported` is **unmoved** at 83, and structurally so: a validator decline is scored `fail`
	// with a named cause, never `unsupported`, so this campaign cannot touch that column however
	// much capability it lands. The reward figure for slice 2 is this floor's +648, and it is stated
	// here so the PR's Board section is quoting an instrument rather than describing one.
	//
	// **60330 → 60388, #9 slice 3's alignment rule (#306): +58, and this one has no decline
	// component at all.** Slice 2's mirror figure was `validateDeclineCeiling` falling; slice 3's is
	// two *other* sub-ceilings falling — `validateAdmitCeiling` 158 → 104 and
	// `validateMismatchCeiling` 4 → 0 — because the vectors it converts were never declined. They
	// were accepted, or refused with the wrong message, which is the whole reason the sub-partition
	// exists: a +58 with `declined` untouched says the reward is 58 rules newly *right* rather than
	// 58 opcodes newly *known*. `unsupported` is unmoved again and for the same structural reason.
	//
	// **60388 → 60390, `select t`'s annotation (#294): +2, and both are `select.wast`'s.** The
	// smallest delta this campaign has landed, and it is in the same PR as slice 3 because it is the
	// same class of defect — information the decoder read and dropped, which no rule in this package
	// could ask for. Slice 3's operand was the alignment exponent; this one's is the result-type
	// vector. The mirror figure is `validateDeclineCeiling` falling 391 → 389, which makes this a
	// slice-2-shaped delta (opcodes newly *known*) sitting beside a slice-3-shaped one (rules newly
	// *right*) — the two mirrors move in one PR, and they are quoted separately rather than summed
	// because a +60 with one mirror would be a different claim about the engine than a +60 with two.
	//
	// The two vectors are `select.wast:368` (`(select (result))`, arity 0) and `:373`
	// (`(select (result i32 i32))`, arity 2), both expecting `invalid result arity`. The all-on lane
	// takes a third — `ref.wast:78`'s `(select (result (ref 1)))` expecting `unknown type`, which
	// needs the GC gate to decode at all — and that asymmetry is at `allOnPassFloor`.
	// **60390 → 60749, #9 slice 5, the index-space family: +358, and it is this campaign's largest
	// single move.** The mirror is `validateDeclineCeiling` 389 → 31, so the delta is entirely
	// slice-2-shaped: 358 opcodes newly *known*, none newly *right*, with `validateAdmitCeiling` and
	// `validateMismatchCeiling` both unmoved. `unsupported` is unmoved at 83 for the fourth time and
	// for the structural reason stated above — a decline is a `fail` with a named cause, so no amount
	// of typing capability can reach that column. This floor's +358 is the reward figure the PR quotes.
	//
	// The 358 are 350 from the 0xFC region and 8 from the widening to `memory.size`/`memory.grow`
	// (plain 0x3F/0x40), which are in this slice because they resolve `memory c x` — the criterion
	// that defines it — rather than because of a prefix byte they do not carry. That scope call is
	// named in the PR and at `internal/validate/bulk.go`'s header, and the two figures are quoted
	// apart so the pre-registered forecast on #322 (which was written about the region) is checked
	// against the population it actually named.
	//
	// The all-on lane takes 552 where this one takes 358, and the 194-vector gap is attributed per
	// file at `allOnPassFloor`.
	// **60836 → 60790, #341: −46, and a pass floor going *down* is the one movement on this bound
	// that has to argue for itself.**
	//
	// Every entry above this one is a rise, because every one of them landed capability. This one
	// landed an *assertion*: a module definition now means the module decodes **and** validates
	// (Scott's semantics ruling on #341), so 46 commands that used to pass on the reader's answer
	// alone are now judged on a question nobody was asking. 22 of them cannot reach the type checker
	// (`encodeFailCeiling`'s +22) and 24 are declined by it (`validateDeclineCeiling`'s +24). Not one
	// of the 46 is a vector this engine used to answer correctly and now answers wrongly — the
	// distinction this bound's own failure message cannot draw, which is why the delta is written down
	// here rather than left to a reader to reconstruct.
	//
	// **Pre-registered on #341 before the arm was touched, with the row list and both lanes' figures**,
	// which is the condition Scott set precisely because a slice that moves already-merged numbers
	// cannot be allowed to explain its movements afterwards. The forecast was `60836 → 60790` and it
	// landed exactly.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural**: `classify` is untouched, so
	// nothing the harness could not ask became askable. The reward figure with a subject is the fail
	// column, +46 here and +164 in the all-on lane, every row an assertion that was not being made.
	//
	// **60790 → 60837, the reference-type slice (#359): +47, and the whole delta is one stratum.**
	// `encodeFailCeiling` is unmoved at 68 and `execFailCeiling` at 81, so fail 235 → 188 is
	// `validateFailCeiling` 86 → 39 and nothing else — which is what a validator slice should look like
	// from here, and is not guaranteed: typing an instruction can unblock a module whose *later*
	// commands then fail in the exec stratum, and this one does not because #341's arm already
	// instantiated those modules.
	//
	// The forecast pre-registered on #359 read `pass +46, fail 235 → 189`, so the slice beat it by one
	// row. The over-prediction is named rather than enjoyed: it expected the `unknown type` vector to
	// stay red as #357's rec-group blocker, and that vector was already in the admission census rather
	// than the decline column, so the residue it forecast had been counted in the baseline twice. An
	// off-by-one in the reassuring direction is still an error in the account.
	//
	// `unsupported` is unmoved at 66 again, and the zero is **structural** for this entry's own reason
	// rather than the one above: `classify` is untouched here too, and a validator slice cannot change
	// what the harness is able to *ask*. The reward figure with a subject is the fail column, −47 here
	// and −62 in the all-on lane.
	// **60837 → 60838, slice 9 (ADR 0035): +1, and the whole of it is a grave rather than a slice.**
	// The tail-call arms are gated off here, so the slice's 34 declines never appear on this lane at all;
	// the single row is `call_indirect.wast:994`, the admission grave #390's repair converts, and
	// `validateAdmitCeiling` 31 → 30 is where it lands. `encodeFailCeiling` unmoved at 68,
	// `execFailCeiling` at 81, `validateDeclineCeiling` at 8 — so fail 188 → 187 decomposes to one
	// admission and nothing else.
	//
	// **The ADR forecast this lane as byte-identical and it was wrong by exactly this row**, for a reason
	// worth naming: the forecast reasoned from the gate map, which covers the instructions a slice
	// *ports* and says nothing about a landed arm the port happens to audit. The paragraph at
	// `validateAdmitCeiling` carries the finding; this entry carries the arithmetic. A +1 that a forecast
	// did not predict is a smaller error than a +35 that one did, and it is recorded at the same volume
	// because the direction of a miss is not what makes it a miss.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the third entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable.
	//
	// **60838 → 60840, slice 10 (ADR 0036): +2, and the whole of it is the rider again.**
	// The exception-handling gate is off here, so the family's 25 declines and 2 tag admissions are all in
	// the other lane; the two rows are #391's `(elem …)` `ref.func` resolution, and `validateAdmitCeiling`
	// 30 → 28 is where they land. `encodeFailCeiling` unmoved at 68, `execFailCeiling` at 81,
	// `validateDeclineCeiling` at 8 — so fail 187 → 185 decomposes to two admissions and nothing else.
	//
	// **This time the +2 was forecast**, which is the only difference between this entry and the one above
	// it and is the whole reason both are here: the entry above records a gated slice's forecast reasoning
	// from the gate map and missing a repair that rode along, and this one is that lesson applied one PR
	// later — the rider was located, its call site read (`check_elem` runs in every mode and `(table
	// funcref (elem 0 0))` needs no gate), and the figure written down before the board was run.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the fourth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable. Four consecutive
	// validator slices with a structural zero is not four confessions; it is the column having no
	// mechanism by which a validator rule could move it, which is why the sentence names the mechanism
	// each time rather than the number.
	//
	// # 60840 → 60868 with #328: +28, and for the first time in five entries the slice's own subject is
	// the whole of it
	//
	// Const-expression typing needs no gate — a global's initializer is MVP core — so `check_const`'s
	// typing half (23 rows), `check_import`'s `ExternFuncT` arm (2), `check_elemmode`'s `match_reftype`
	// (2) and `ref.func` resolution inside a const expr (1) all land here. `validateAdmitCeiling` 28 → 0
	// is where every one of them comes from; `encodeFailCeiling` unmoved at 68, `execFailCeiling` at 81,
	// `validateDeclineCeiling` at 8 — so fail 185 → 157 decomposes to 28 admissions and nothing else.
	// Forecast at +28 before the rules were written.
	//
	// **Two decode-direction graves rode along and neither one moved this figure**, which is the entry
	// worth reading twice. Grave #360 gave every index-form element segment `funcref` where the reference
	// gives `(ref func)` (decode.ml:1154-1163), and grave #401 gave the `(table rt (elem x…))` sugar
	// `elemkind`'s type where the reference gives the table's own (parser.mly:1215). Both were invisible
	// for as long as nothing compared an element segment's type against its table's — nullability is only
	// ever read by a subtype check — and `check_elemmode` is the first such comparison this engine has
	// had. So they surfaced as **over-rejections of four valid modules**, not as admissions: `elem.wast`
	// :453 and :487, `br_table.wast:3`, `type-subtyping.wast:373`. Fixed in this PR, and their net
	// contribution to this figure is zero, because a rule that over-rejects and a rule that is absent
	// score the same on a corpus of invalid modules.
	//
	// The transferable half: **a new accept-direction rule audits the decoders that feed it**, and the
	// audit is free only if the rule is landed before the pins are re-based. Had the four over-rejections
	// been read as "#328's rule is too strict" the repair would have gone into `matchRefType` and both
	// decoders would still be wrong — the validator was right about the types it was handed.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the fifth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable.
	// **60868 → 60863, and the −5 is a finding rather than a regression** — decision 0037's
	// pre-registration forecast this row unchanged and said in advance that any movement in it
	// would be a finding to report rather than a number to adjust. Five moved, and they were
	// **passes awarded by coincidence**: `assert_unlinkable` vectors expecting `unknown import`
	// for a name their target module genuinely lacks, which passed because the *whole target
	// module* was unbound after a gate-declined `register`. The right text for the wrong fact —
	// the engine was never asked whether `"test"` exports `unknown`, it was asked whether
	// `"test"` exists.
	//
	// **The five are derived, not spotted**, on Scott's condition for accepting the −66 (his
	// ruling on PR #409: *"5 pass→gated removes green, and that direction always gets named"*).
	// Neuter the declined-import gate and re-run: a line gated *with* the gate that was neither
	// gated nor failing *without* it was a pass the correction removed. Board-wide that set has
	// exactly five members, and the per-file pass deltas sum to the aggregate −5, so no file
	// gained a pass that masked a sixth loss — the size and the level agree, which is the check
	// a set derived from a diff needs before it is trusted.
	//
	//   imports.wast:136   (import "test" "unknown" (func))         — asserts the *export* is absent
	//   imports.wast:295   (import "test" "unknown" (global i32))   — same, global
	//   imports.wast:440   (import "test" "unknown" (table 10 funcref)) — same, table
	//   imports.wast:538   (import "test" "unknown" (memory 1))     — same, memory
	//   linking3.wast:14   (table (import "Mm" "tab") …)            — the corpus itself comments
	//                      `;; does not exist`, and the same module imports `"Mm" "mem1"`, which
	//                      *does*, so the vector is about the table alone
	//
	// `"test"` is exported by an auxiliary module carrying `(tag …)`, declined under EH-off; `$Mm`
	// declares three memories, declined under multi-memory-off. In both cases the reference has the
	// name bound and this engine does not, so every one of the five was asking a question about a
	// module that, here, did not exist.
	//
	// **The corpus supplies the control for free, and it is why "wrong fact" is a measurement and
	// not a reading.** Each of the four `imports.wast` vectors is immediately followed by the
	// identical assertion against `"spectest" "unknown"` (:140, :299, :444, :542) — same expected
	// text, same shape, a module name that is never declined. Those four still score verdicts,
	// unchanged. The discriminator is therefore the *module name*, not the expectation, which is
	// exactly the fact a substring match cannot see. No new control is owed for it either:
	// `gatedDeclinedRegistration["imports.wast"]` is pinned at slack 0, so a gate that widened to
	// the twins would fail that bound rather than pass quietly.
	//
	// **The published line citations were wrong by two, and the error has a shape worth naming.**
	// This entry, ADR 0037, the CHANGELOG, grave #408 and PR #409 all first cited :138/:297/:442/:540
	// — the `"unknown import"` *text* lines, which is where a reader's eye lands when confirming what
	// a vector expects. The harness records the command's opening line, so the numbers named a
	// neighbour of the thing they were about; `linking3.wast:14` was right only because that command
	// opens where it was read. Nothing caught it: `citecheck.sh` resolves issue and ADR tokens and
	// has no oracle for a `file:N`, and the one control that does check such a citation —
	// `TestFixtureProvenance`, against the suite's own bytes — has a domain of citations sharing a
	// line with a byte-slice literal, which no prose citation is. Filed as #412.
	//
	// So this floor's own column held five false greens of the accept-direction shape, and the
	// change that drained 66 mis-attributed fails converted them to honest gates in the same
	// motion. Grave #408; the lesson is at the fix site.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the sixth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable.
	// **60863 → 60890, +27 against a forecast of +35, and the miss is the entry worth reading.**
	//
	// #413 landed the start section across the three layers that lacked it, and 35 fail rows left the
	// fail column — 28 from the encode stratum (`encodeFailCeiling` 68 → 40) and 7 from exec
	// (`execFailCeiling` 15 → 8), with every other stratum unmoved, so nothing migrated between
	// columns on the way. The **fail** forecast was exact: 91 → 56, to the row. The **pass** forecast
	// was 60898 and the board delivered 60890.
	//
	// The eight are one file. `start0.wast` had 9 encode-stratum rows and scored 1 pass + 8 **gated**,
	// not 9 passes: the file declares three memories at :2, so once section 8 encodes, the next
	// blocker the module meets is a memarg carrying flags bit 6 and the multi-memory gate declines it.
	// `start.wast`'s 18 and `ref_func.wast:112` became the other 20 passes; exec's 7 became the rest.
	// 20 + 7 = 27.
	//
	// **So a first-blocker census over-predicts passes even when it is exactly right about fails**,
	// and this is the specimen. Both halves of the forecast were derived the same way — one blocker
	// per row, removed — and that reasoning is sound for the fail column, because a row with any
	// remaining blocker leaves it either way. It is unsound for the pass column, because the *second*
	// blocker can be a **gate**, which moves the row to a third verdict rather than back to fail. A
	// census that names first blockers can therefore only bound passes from above; the number it
	// yields is `fails removed`, and the gap is whatever share of them a gate catches next.
	//
	// **The attribution is proven by a second lane rather than asserted.** If the eight were engine
	// wrongness they would be absent in the all-gates-on lane too; if they are the multi-memory gate,
	// that lane must collect the full 35. It gained 35 (`allOnPassFloor` 64903 → 64938), which is the
	// check this reading needs before "it's a gate" is a measurement instead of a story.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the seventh entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable.
	// # 60890 → 60909, +19 against −33 fails, and the 14 is a gate again — the specimen above,
	// # reproduced one slice later
	//
	// #419 landed the table initializer across text, decode, validate and interp. The fail column fell
	// exactly 33 (56 → 23), all of it the encode stratum (`encodeFailCeiling` 40 → 7, residue identical
	// row-for-row), and the pass column rose 19. The 14 that did not become passes became **gated**:
	// 4132 → 4146, the third column moving by precisely the gap, which is what says the rows went
	// somewhere rather than vanishing.
	//
	// The gate is nameable and it is this slice's own doing. A table that *spells* an initializer
	// encodes to the `0x40` form, and `decodeTableForm` declines that form without GC (decision 0008),
	// so teaching the emitter the field turned "cannot encode this (table …) field" into "gc: feature
	// gate disabled" for every vector whose table needed the spelled form — a fail becoming a third
	// verdict, not a pass. Per file, measured by diffing the two boards' own lines rather than by
	// bucket arithmetic:
	//
	//	                 default passes   default gated
	//	elem.wast              +8              +1
	//	global.wast            +6              +1
	//	i31.wast               +1              +3
	//	table.wast             +4              +9
	//	                      ---             ---
	//	                       19              14   = the 33 that left the fail column
	//
	// **The fail forecast was pre-registered and the pass forecast was not**, which is the honest
	// version of "as predicted": the previous section committed to −33 with seven rows unchanged and
	// that is what landed, to the row. Nothing named a pass figure, and the entry above is why one
	// would have been wrong if it had: a first-blocker census bounds passes from above, and the share a
	// gate catches next is not derivable from the census. What *is* new here is that the gate was
	// identifiable in advance from the wire form — so the missing forecast was available and simply not
	// written, which is a smaller failure than #413's but the same one.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the eighth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable.
	// # 60909 → 60913, +4 against −4 fails, and this time the two columns are the *same four rows*
	//
	// #77 binds a typeuse's params into the local index space, and the delta is the smallest and
	// cleanest this ledger holds: every row that left the fail column arrived in the pass column, no
	// third verdict took a share, and `func.wast` went 170/174 to **174/174**. That one-to-one identity
	// is worth naming because the two entries above are both cases where it did *not* hold — #413 and
	// #419 each lost part of their fall to `gated`, and the reason this slice cannot is that no new wire
	// form is produced: the fix changes an index byte from 0 to 1 inside an image the emitter already
	// wrote, so there is no gate for the rows to fall into.
	//
	// The pass forecast was **not** pre-registered and the fail forecast was, which is the same
	// asymmetry the previous entry confessed and the same reason it is defensible: `encodeFailCeiling`
	// committed to −4 with the three `memory-multi.wast` rows unchanged, and that is what landed to the
	// row. What was *newly* available here and still not written is that this slice's fall could not be
	// gate-absorbed — see the paragraph above, which is derivable from the wire form before running
	// anything. So +4 was forecastable and was not forecast; noted rather than excused.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the ninth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable. #323 remains the
	// one open issue that moves this column.
	// # 60913 → 60914, +1 against −3 fails, and the missing two are the gate
	//
	// Grave #130's repair drains `encodeFailCeiling` to zero, and the three rows that leave the fail
	// column do **not** all arrive here: one becomes a pass and two become `gated`. `memory-multi.wast`
	// went 1/4 pass, 3 fail, 2 gated to 2/2 pass, 4 gated — pass 60913 → 60914, fail 19 → 16, gated
	// 4146 → 4148, and the three columns account for each other exactly.
	//
	// **This one *was* forecast, and the forecast is the previous entry's confession discharged.** The
	// section above admitted that #77's +4 had been derivable from the wire form and simply not
	// written; here the same derivation runs before the measurement. The rows are a module definition
	// and two `assert_return`s over a two-memory module: the module needs only an encode, so it can
	// become a pass, while the assertions need the *interpreter* to hold two memories, which is
	// multi-memory's gate and off. So +1 with +2 to `gated` was the prediction, and the split is the
	// same one #413 and #419 each paid — a fall in the fail column lands in two columns, and the gate
	// is why the rest didn't become passes.
	//
	// `unsupported` is unmoved at 66 and the zero is **structural** for the tenth entry running:
	// `classify` is untouched, so nothing the harness could not ask became askable. #323 remains the
	// one open issue that moves this column.
	//
	// # 60914 → 60922 with #427, and this is the fail column's last non-harness member
	//
	// +8, every row a relaxed-SIMD `module text declined`: the validator had no rule for
	// `fd 0x100..0x12f` while the *decoder* had admitted the range since the gate flipped, so the eight
	// were a typing gap wearing a gate's name. Pre-registered exactly, in both lanes (all-on 64985 →
	// 64993), and the equality between the lanes is the check — a validator decline reads no gate, so
	// an unequal move would have meant the repair touched something else. `fail` 16 → 8, and the 8 that
	// remain are both harness attribution holes, #414's five and #426's three: **no row in this column
	// is now the interpreter computing a wrong answer, and none of the eight is engine work.**
	//
	// `unsupported` unmoved at 66, and the zero is **structural because `classify` is untouched by this
	// PR** — the column moves only when what the harness *can ask* changes, and nothing here changed it.
	// The mechanism is stated here rather than by pointing at the earlier entry that holds it, on Scott's
	// ruling on this PR: a foreclosing word is admissible when the reason travels with it, and a pointer
	// to the paragraph holding the reason is not the reason. This is the first such line written after
	// the foreclosing-word sweep landed, so it is the one that sets the form for the entries after it —
	// and the form is now checked, by `TestForeclosingBoundAccountsCarryTheirReasonInline`, rather than
	// promised. A convention promised in prose is what failed four times over; the phrase this entry
	// avoids is the one that instrument bans, which is why it is described here instead of quoted.
	//
	// The reward figure that does have a subject is this one: the validate stratum reaches 0 across all
	// four of its partitions (`declined`, `admitted`, `over-rejected`, `wrong-message`), which is #9's
	// instruction vocabulary complete over the space the decoder can name under `DefaultFeatures`.
	//
	// **That is not #9 done, and the distance is worth stating where the figure is.** A drained stratum
	// says no *board row* is attributed to a validator shortfall; it does not say the validator refuses
	// everything it should. #111 is the cleanest witness — nine valtype positions accept
	// `(ref null $undefined)`, one shape, and **no suite vector fails on it** — so a negative-vector corpus
	// cannot falsify what a validator wrongly *accepts*, which is why this column reaching 0 leaves #9
	// open. #357, #358 and #296 are the rest of the open shortfall.
	//
	// **This paragraph has been wrong twice, in the same clause position, about the same thing — and the
	// second time was the repair of the first.** Both are kept, because the pair is the finding and a
	// reader who finds two reasons where one is false cannot tell which half of the conclusion survives.
	//
	// The first leg was *"alignment is not checked at all — `decodeMemop` drops the memarg"*. Alignment is
	// checked (`internal/validate/align.go`, 45 natural widths derived from `mnemonics.ml`) and has been
	// since #306 landed in #313, two weeks before that sentence was written. It was copied out of
	// `internal/validate/vec.go`'s non-goals section — a foreclosing paragraph #306 falsified and nobody
	// re-read — in the same PR whose subject was foreclosing prose resting on premises that had moved
	// (grave #431).
	//
	// The second leg was that repair: *"#328's 103 module-and-section vectors have no vocabulary yet, and
	// #328 carries it alone"*. **#328 closed as completed the day before that was written** — #403 supplied
	// the vocabulary, drained `accepted` to 0 and re-based `validateAdmitCeiling` **28 → 0**, which is the
	// same zero `validateAdmitCeiling`'s own entry above quotes as a *reached* bound. So the replacement
	// leg named as the live carrier of "#9 is open" the issue whose closure is the evidence sitting two
	// bounds up this file (grave #434).
	//
	// Neither failure is about alignment or about vocabulary. Both are **a premise sourced from a paragraph
	// when the tracker was one query away** — and the second was written into the repair of the first, with
	// the first's lesson stated in the paragraph being edited. The lesson had been applied to the *fact*
	// and not to the *sourcing*, so the shape outlived its own grave by one commit. That is the durable
	// part: **a stale-premise repair is itself a premise-sourcing event and inherits the whole risk it
	// exists to discharge**, and it gets less scrutiny than the original for being the fix rather than the
	// bug. The conclusion is unchanged and now rests on #111, which is checkable by reading #111.
	//
	// **Third leg, annotated by the PR that falsified it rather than by a later reader.** The list above —
	// *"#357, #358 and #296 are the rest of the open shortfall"* — was accurate when written and is
	// **two-thirds spent as of the `check_valtype` slice**: #357's rec-group scoping and #358's supertype
	// message both land there, and #357's premise was itself stale when filed (grave #469). What remains of
	// the shortfall named here is **#296** plus the two the same slice measured and did not take: **#111**'s
	// nine `(ref null $undefined)` positions, still with no suite vector failing on them, and **#452**'s
	// local-initialization rule, which is `decision-needed:scott` and deliberately untaken. This
	// annotation exists because the paragraph it sits under is a two-instance record of sourcing a premise
	// from prose, and a list of open issues inside it is the same premise class one level down: the tracker
	// is one query away and *nothing in this file re-queries it when an issue closes*.
	// # 60922 → 60928, +6, and the six are **the whole of what the default lane can gain here**
	//
	// #426's two script forms. Six commands convert from `unsupported` to `pass`: the four standalone
	// `(module definition …)` forms in `memory`/`memory64`/`table`/`table64`.wast, and `instance.wast`'s
	// two named definitions. Every one is a module the corpus asserts is valid and the harness had no
	// case for.
	//
	// The three `(module instance …)` forms and the three `register`s that name them convert to
	// **`gated`**, not to pass, because `instance.wast`'s definitions carry `(tag …)` and the EH gate
	// declines *instantiating* them. So the default lane's ceiling on this slice is 6, structurally,
	// and the other 15 conversions are in the all-on lane where `allOnPassFloor` +21 records them.
	//
	// **The forecast said 60924 and missed by 4, in one direction and for one reason.** Pre-registered
	// on #426: memory64's and table64's definitions predicted `gated` on their proposals, and
	// `instance.wast`'s two definitions predicted `gated` on their tags. All four passed. The error is
	// the same error four times — a decline near a command inferred to be a decline *of* it — where the
	// question the arm actually asks is which step declines: an `i64` index type reads and validates
	// fine and memory64's 50 declines are elsewhere in its file, and a `(tag …)` field reads and
	// validates fine because the EH gate sits at instantiation, which a definition form never reaches.
	// *Aboutness is not proximity*, applied to a forecast rather than to a sentence.
	//
	// The three figures the forecast got right are the ones derived rather than associated: `fail` −3
	// and `unsupported` −9 exactly, and 12 rows conserved. Recorded because a pre-registration that
	// half-misses is only useful if which half is said out loud.
	// # 60928 → 60943, +15, and the +15 is the whole of `unsupported`'s −15
	//
	// #440's `assert_exhaustion` arm. No gate participates and no command is added, so the conversion
	// is one column into another and the two figures are the same event counted twice: `unsupported`
	// 29 → 14 and this floor +15, with `gated` flat at 4187 and `fail` flat at 0. The account of what
	// the 15 are, and of the forecast that got the *split* wrong while getting the total exactly
	// right, lives on `unsupportedCeiling` above — the column that drained is where a drain's story
	// belongs, and repeating it here would give a reader two copies to keep in agreement.
	//
	// What is worth stating on *this* side is the direction the miss ran. The pre-registration
	// forecast 5 of the 15 reaching this floor and 10 landing in `fail`; all 15 landed here. A floor
	// forecast low and beaten is not the reassuring case it reads as: the 10 were predicted to fail
	// *by construction*, so beating the forecast means the construction argument was false, not that
	// the engine over-delivered. The +15 is banked because the engine answered 15 questions right,
	// which is checkable; the forecast is recorded as a miss because its reasoning was wrong, which
	// is a separate fact about the forecaster.
	// # 60943 → 60946, +3, and this floor is the wrong figure to read the slice's value off
	//
	// #459's annotation skip. Same shape as the entry above — `unsupported` 14 → 11 and this floor
	// +3 are one conversion counted from both sides, `gated` flat at 4187, `fail` flat at 0 — so the
	// account of what the three are lives on `unsupportedCeiling`, where the drain happened.
	//
	// What belongs here is the caveat, because a floor that moves reads as an engine that gained.
	// **These three modules were being decoded, validated and instantiated correctly before this
	// PR**; nothing in the engine changed, and the +3 measures the harness learning to classify a
	// form it had been dropping. `TestAnnotatedModulesInstantiate` is what makes that assertable
	// rather than assumed: this floor cannot distinguish "the engine started answering" from "the
	// harness started asking", and on this slice it is the second.
	// # 60946 → 60957, +11, and here the engine *did* gain a method — which changes nothing
	//
	// #323's `(get …)` rows. Same one-conversion-two-sides shape as the two entries above:
	// `unsupported` 11 → 0 and this floor +11, `gated` flat at 4187, `fail` flat at 0, with the
	// account of the rows on `unsupportedCeiling`.
	//
	// The caveat this side needs is the *opposite* of #459's and lands in the same place. That slice
	// added nothing to the engine, so the "+3 is not capability" note was easy; this one adds
	// `interp.Instance.Global` and `burroughs.Instance.Global`, so the +11 looks like capability and
	// is not. What the engine gained is a **read path**, not an answer: the globals were already
	// allocated, initialized and exported correctly, and every one of these 11 vectors would have
	// passed years ago had there been a way to ask. The distance between "the value is right" and
	// "the harness can obtain the value" is what this floor cannot see, which is why the claim is
	// made where it is checkable — `TestGlobalReadsWhatTheInterpreterHolds` reads the same globals
	// through `global.get` inside a body *and* through the new boundary and requires them equal, so
	// the new path is pinned against the old one rather than against the corpus alone.
	//
	// **A first pass on a new public method is the weakest evidence about that method**, since the
	// corpus population is 11 rows over 2 files and none of them is a `v128`, a reference, or an
	// immutable-versus-mutable distinction. The unit control above is what covers those; the board's
	// +11 is a fact about the harness's reach.
	const passFloor = 60957
	// Slack 0 as of #387's ruling, with `allOnPassFloor` and `unsupportedCeiling` — see
	// `boardbound_test.go`'s retirement section. Two entries in the ledger above record taking a
	// re-base *although the slack stayed silent* (58659 by a margin of 20, and the 416 that was four
	// PRs' drift); under an exact bound neither of those is discretionary any more, which is what the
	// ruling means by "re-base it with the lane".
	boardBound(t, "passFloor", totalPass, passFloor, 0, floorBound,
		"a regression in a grammar that used to answer, or the corpus moved")
}

// TestDenominatorExcludesUnaskedCommands pins Total()'s denominator: it counts what
// was asked, never what was skipped or declined.
//
// This control exists because the choice became load-bearing the moment the corpus
// was derived (#52). While the board ran eight byte-string files, Unsupported was
// zero and Gated was two, so folding either into the denominator would have been
// nearly invisible — the kind of decision a refactor makes silently because nothing
// fails. With ~1345 unsupported commands the same slip renders a green board as
// 783/2128 and reads as a collapse when nothing regressed, and worse, it makes the
// ratio improve when a *component* lands rather than when a *verdict* is earned.
//
// A comment cannot fail, so the invariant gets a test. Falsified while writing it:
// changing Total to Pass+Fail+Unsupported makes the second case below report 1/2.
func TestDenominatorExcludesUnaskedCommands(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want int
	}{
		{"pass and fail are the denominator", Result{Pass: 3, Fail: 2}, 5},
		{"unsupported is not asked, so not counted", Result{Pass: 1, Unsupported: 99}, 1},
		{"gated is declined, so not counted", Result{Pass: 1, Gated: 99}, 1},
		{"neither, together", Result{Pass: 2, Fail: 1, Unsupported: 40, Gated: 7}, 3},
		// The fourth verdict is excluded for the same reason as the other two, and at
		// 1236 vectors this is the largest of the three exclusions by far: folding it in
		// would render a 783/784 board as 783/2020 and read as a collapse on the day the
		// corpus was admitted.
		{"unimplemented is unanswered, so not counted", Result{Pass: 1, Unimplemented: 1236}, 1},
		{"all three exclusions at once", Result{Pass: 5, Fail: 2, Unsupported: 30, Gated: 4, Unimplemented: 99}, 7},
		// The degenerate case stated rather than implied: a board that asked nothing
		// has a denominator of zero, which is why TestBinaryWast checks Total() != 0
		// before trusting a ratio. A pass rate over an empty denominator is the
		// vacuity failure wearing arithmetic.
		{"nothing asked at all", Result{Unsupported: 500}, 0},
	}
	for _, c := range cases {
		if got := c.r.Total(); got != c.want {
			t.Errorf("%s: Total() = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestUnsupportedIsBucketedByCommand checks that the unsupported column names what
// each unrun vector waits on, rather than reporting a bare total.
//
// The scalar and the map are two records of the same fact, so they can drift — and a
// map that silently stopped being populated would leave the board printing a large
// number with no work plan beside it, which is the column reverting to exactly the
// thing #52's doctrine forbids. Both directions: the sum must equal the scalar, and
// the map must be non-empty when the scalar is.
func TestUnsupportedIsBucketedByCommand(t *testing.T) {
	requireSuite(t)
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		sum := 0
		for _, n := range r.UnsupportedByHead {
			sum += n
		}
		if sum != r.Unsupported {
			t.Errorf("%s: UnsupportedByHead sums to %d but Unsupported is %d — the column and "+
				"its breakdown disagree, so one of them is not being maintained", f, sum, r.Unsupported)
		}
		if r.Unsupported > 0 && len(r.UnsupportedByHead) == 0 {
			t.Errorf("%s: %d unsupported commands and no breakdown; the board would print a "+
				"number with no work plan beside it", f, r.Unsupported)
		}
		// Every key names a real command head, or the breakdown is decoration. An
		// empty key would print as a blank row, which is the unlabelled-entry
		// failure the "(no head atom)" placeholder exists to prevent.
		for h := range r.UnsupportedByHead {
			if h == "" {
				t.Errorf("%s: UnsupportedByHead has an empty key; a blank row in a work-plan "+
					"column is the entry nobody investigates", f)
			}
		}
	}
}

// TestParseEverySuiteFile is a parser-robustness sweep: the s-expression reader
// must survive all 257 upstream .wast files without a parse error, even the ones
// full of wat it cannot interpret. Parsing and understanding are separate
// concerns, and conflating them would hide the real unsupported count.
func TestParseEverySuiteFile(t *testing.T) {
	paths := suitePaths(t)
	var broken int
	for _, p := range paths {
		if _, err := ParseFile(p); err != nil {
			broken++
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
	t.Logf("parsed %d/%d .wast files", len(paths)-broken, len(paths))
}

// TestEveryNeededCapabilityIsRegistered is guard 2 of decision 0010: a vector may
// reach the fourth verdict only via a registered capability.
//
// The abuse the fourth verdict invites is the one the third verdict already had to be
// defended against (#27): a category that is neither pass nor fail is a lever for
// emptying a board by fiat. `gated` was fenced with a per-vector allowlist plus an
// all-on lane, and neither transfers here — 1236 vectors cannot be allowlisted
// individually, and "turn the capability on" is not a configuration change when the
// component does not exist.
//
// So the fence is the registry: classify may only ask for a capability that has an
// entry, and the entry carries the issue that closes it — plus, per guard 6, the
// condition under which it must be deleted. This test reads the whole corpus rather
// than the board, because an unregistered capability on an unadmitted file is still a
// classification defect. TestNoCapabilityOutlivesItsComponent is the other half: this
// one guards the entry's birth, that one its death.
//
// **What "registered" means was refined by the first retirement, not weakened.** The
// original invariant read *every needed capability has a registry entry*, which was
// exactly right while no capability had ever been retired and became false the moment
// one was: `wat-reader` is needed by 1236 commands, has no entry, and that is the
// *success* condition, not a hole. The real invariant is the one the entry existed to
// serve — **every needed capability is accounted for, as a tracked debt or as a
// declared component** — and it is stronger than the old reading rather than looser,
// because the two arms are exclusive: a capability both registered and declared is
// guard 6's other-half failure, and one that is neither is guard 2's. The retirement
// is what made the distinction observable; before it, the two readings agreed on every
// input.
func TestEveryNeededCapabilityIsRegistered(t *testing.T) {
	requireSuite(t)
	seen := map[Capability]int{}
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Needs == CapNone {
				continue
			}
			seen[c.Needs]++
			issue, registered := CapabilityIssue(c.Needs)
			declared := EngineHas(c.Needs)
			switch {
			case registered && declared:
				// Guard 6's first arm, and it is asserted here as well as in the death
				// test so that the birth guard cannot report "accounted for" on a
				// capability that is accounted for twice.
				t.Errorf("%s:%d needs capability %q, which is both registered as missing and "+
					"declared by the engine; retirement is one motion and this is the half "+
					"that was skipped", filepath.Base(p), c.Line, c.Needs)
			case declared:
				// Retired: the engine has it, so there is no debt to track. Nothing to
				// check here — TestNoCapabilityOutlivesItsComponent is what proves the
				// population drained, which is the claim this arm is standing on.
			case !registered:
				t.Errorf("%s:%d needs capability %q, which is neither registered nor declared "+
					"by the engine; an unaccounted capability is a fourth-verdict column with "+
					"no owner", filepath.Base(p), c.Line, c.Needs)
			default:
				if issue == "" {
					t.Errorf("capability %q is registered with an empty issue; the tracking "+
						"number is what makes it a debt rather than an intention", c.Needs)
				}
				if ret, _ := CapabilityRetirement(c.Needs); ret == "" {
					t.Errorf("capability %q is registered with no retirement condition; an entry "+
						"born without a death certificate is a squatter, and its column becomes "+
						"permanent by omission rather than by decision", c.Needs)
				}
			}
		}
	}

	// Vacuity floor. A classifier that stopped setting Needs would leave this test
	// comparing an empty set against a registry and agreeing perfectly — the
	// comparisons-need-a-vacuity-check rule, and the exact shape that let an empty
	// keyword extraction drift-check clean (0009).
	//
	// The floor is now the *only* thing keeping this test non-vacuous, which the
	// retirement is what changed: while the registry was non-empty, an emptied `seen`
	// would also have tripped the used-members loop below. With the registry empty that
	// loop iterates zero times and asserts nothing, so a count floor is load-bearing
	// where it used to be belt-and-braces — and *a floor below the list's own length is
	// decoration*, so it is the measured 1236 rather than a token 1.
	if len(seen) == 0 {
		t.Fatal("no command in the corpus needs any capability, so this test asserted " +
			"nothing; classify has stopped setting Needs and the fourth verdict is dead code")
	}
	if n := seen[CapWatReader]; n < 1200 {
		t.Errorf("only %d commands need %s, want >=1200; the classifier has stopped "+
			"recognizing quote forms, and every arm above would then be agreeing about "+
			"almost nothing", n, CapWatReader)
	}
	// And the registry's own members must be *used*, or an entry is a debt nobody owes:
	// a stale capability overstates what the engine is waiting on.
	for _, c := range RegisteredCapabilities() {
		if seen[c] == 0 {
			t.Errorf("capability %q is registered but no command needs it; remove the "+
				"entry or the registry overstates the engine's outstanding work", c)
		}
	}
	for c, n := range seen {
		t.Logf("capability %s: %d commands", c, n)
	}
}

// TestQuoteFormsHaveTheirReader is the drain tripwire (decision 0010), re-pointed at the
// case that is still wrong now that the reader has landed.
//
// It was TestQuoteFormsAwaitTheirReader, and it asserted two things: that an undeclared
// capability scored `unimplemented`, and that declaring CapWatReader with no reader wired
// panicked. The first half's subject **dissolved** — the capability is declared, so the
// gap it measured cannot exist for wat-reader any more — and the second half's *risk* did
// not: a caller can still declare the capability and hand the run loop a nil ReadTextFunc,
// which is the registry running ahead of the engine in the only form still available. *A
// tripwire whose subject dissolves is re-pointed, never closed* — closing this as "no
// longer applicable" would retire a live risk on a technicality.
//
// So the three properties below are the same obligation aimed at what exists now: the
// eleven vectors score, they score as *verdicts* rather than as the fourth column, and
// the declared-without-supplied combination still panics.
func TestQuoteFormsHaveTheirReader(t *testing.T) {
	requireSuite(t)
	s, err := ParseFile(filepath.Join(suiteDir, "obsolete-keywords.wast"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := run(s)
	// The drain, at the file that prompted the capability. Eleven quote vectors, each
	// naming a mnemonic the keyword table omits, and each one now answered.
	if r.Unimplemented != 0 {
		t.Errorf("%d vectors still unimplemented in obsolete-keywords.wast; the reader is "+
			"declared, so nothing in this file has an excuse left", r.Unimplemented)
	}
	if r.Pass != 11 {
		t.Errorf("obsolete-keywords.wast scored %d pass, want 11 — this is the file whose "+
			"eleven `unknown operator` vectors are the reject-direction contract (0009), and "+
			"a count below 11 means the keyword table is admitting a mnemonic the spec does "+
			"not\n%s", r.Pass, r.Board())
	}
	if r.Fail != 0 {
		t.Errorf("%d fail in obsolete-keywords.wast:\n%s", r.Fail, r.Board())
	}

	// The re-pointed half. Declaring a capability whose component was not supplied must
	// fail loudly rather than score against a nil entry point. Recovered deliberately:
	// the panic *is* the assertion.
	//
	// Falsified while re-pointing it, per *break the control to know its green is
	// falsifiable*: with the nil check removed the run loop dereferences nil and panics
	// anyway, which would have made this pass for the wrong reason — a nil-map read, not
	// a diagnosis. So the message is asserted, not merely the panic.
	func() {
		defer func() {
			switch v := recover(); {
			case v == nil:
				t.Error("declaring CapWatReader with no ReadTextFunc did not panic; the " +
					"registry is allowed to run ahead of the engine, so a vector could be " +
					"scored against a component that was never handed over")
			case !strings.Contains(fmt.Sprint(v), "no ReadTextFunc was supplied"):
				t.Errorf("panic does not name the missing component: %v", v)
			}
		}()
		_ = s.RunWith(Engine{Decode: decode, IsGated: isGated, Has: []Capability{CapWatReader}})
	}()
}

// TestNoCapabilityOutlivesItsComponent is the second structural control on the fourth
// verdict (ruling: chat-Claude, PR #58), and it is temporal where `gated`'s is spatial.
//
// `gated` gets its anti-dumping-ground guarantee from a lane: turn every gate on, and
// the gated count must be zero, so a vector parked in the third verdict is
// simultaneously being failed somewhere. That does not transfer, and the reason is
// exactly why this category exists — absence-by-construction has nothing to switch on.
// You cannot enable a component that has not been written.
//
// So the guarantee is delivered as a tripwire on the registry's *lifecycle* instead.
// Two directions, and both are failures:
//
//   - A capability the engine declares must not still be registered as missing. The
//     registry would be claiming the engine is waiting on something it has.
//   - A capability the engine declares must have drained its population to exactly
//     zero. Landing a component that leaves some of its vectors in the fourth column
//     is the disappearance the whole ruling exists to prevent: the component exists,
//     so those vectors have no excuse left, and they must be pass or fail.
//
// The two together are why retirement is a single motion — declare the capability,
// delete the entry, and the population is zero because nothing can produce it. Doing
// half of it fails here rather than being noticed at review time, which is what makes
// the entry's stated Retires condition an assertion rather than a promise.
func TestNoCapabilityOutlivesItsComponent(t *testing.T) {
	requireSuite(t)

	engine := EngineCapabilities()

	// Vacuity, and **the retirement moved which set has to be non-empty** — which is the
	// finding this floor recorded by failing. It read "the registry must be non-empty,
	// because the engine's set is empty by design", and after the first retirement both
	// halves of that sentence are false: the registry is empty by design and the engine's
	// set is what carries the content. Left as written it would have Fataled on the state
	// it exists to certify, which is a control asserting the absence of its own success.
	//
	// The invariant that survives the swap is the one to floor: **the control's two loops
	// iterate over `engine`, so `engine` is what must be non-empty.** A capability
	// registry emptied without any component landing would leave both sets empty, and
	// this catches it from the side that will keep being true as capabilities are added.
	if len(engine) == 0 {
		t.Fatal("the engine declares no capabilities, so both loops below iterate zero " +
			"times and this test asserted nothing; either the registry was emptied without a " +
			"component landing, or engineCapabilities lost a declaration")
	}

	for _, c := range engine {
		if _, stillRegistered := CapabilityIssue(c); stillRegistered {
			ret, _ := CapabilityRetirement(c)
			t.Errorf("the engine declares %q but it is still in capabilityIssues; retirement "+
				"is one motion, and this is the half that was skipped.\n  its stated condition: %s",
				c, ret)
		}
	}

	// The population check, over the whole corpus rather than the board: a declared
	// capability leaving vectors behind on an unadmitted file is the same defect.
	pop := map[Capability]int{}
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		r := run(s)
		for c, n := range r.UnimplementedByCapability {
			pop[c] += n
		}
	}
	for _, c := range engine {
		if n := pop[c]; n != 0 {
			t.Errorf("the engine declares %q, yet %d vectors are still scored unimplemented "+
				"against it; a component that lands without draining its column to zero has "+
				"converted a deferral into a disappearance", c, n)
		}
	}

	// And the corpus's own report, over the union rather than over the registry: after a
	// retirement the registry is the *shorter* list, and logging only its members would
	// stop reporting exactly the capability whose drain this test just certified.
	for _, c := range RegisteredCapabilities() {
		issue, _ := CapabilityIssue(c)
		t.Logf("registered %s (%s): %d vectors outstanding, engine has it: %v",
			c, issue, pop[c], EngineHas(c))
	}
	for _, c := range engine {
		t.Logf("declared %s: %d vectors outstanding (must be 0), still registered: %v",
			c, pop[c], func() bool { _, ok := CapabilityIssue(c); return ok }())
	}
}

// TestBareModuleSpansAreNonEmptyAndPlausible is #69's vacuity floor over the real corpus, and
// it is deliberately three assertions rather than one, because "the span mechanism works" can
// fail in three independent ways that a single count cannot separate.
//
// The rule being applied is *comparisons need a vacuity check*, at the scale that matters: the
// pass floor of 4122 rests on 2130 newly-scored bare modules, and a span mechanism that
// silently found **zero** of them would leave the harness classifying nothing as
// KindModuleText, every one of those commands back in `unsupported`, and this file's floors
// failing with a number that says "the reader regressed" — which would be a lie about which
// stratum broke. A global `> 0` is not enough either: `const.wast` alone holds 402, so a bug
// that lost every file but one would still be comfortably non-zero.
//
// So the three assertions are:
//
//  1. **A corpus total floor** — 2000 against 2146 measured. Bounds a wholesale loss.
//  2. **A file-count floor** — 230 against 242 files holding at least one. This is the one a
//     total cannot give: it bounds the *distribution*. Not asserted but measured — dropping
//     the 13 smallest files trips this floor at 229 while the total sits at **2130**, still
//     1.06× above (1). So (2) is not a weaker restatement of (1); there is a real regression
//     shape that only it sees, and the gap is 130 vectors wide rather than the "200 small
//     files" this comment first guessed at.
//  3. **A per-span emptiness check** — every retained span is non-empty and starts with `(`.
//     A `start == end` span is what an off-by-one in the wrong direction produces, and it
//     reaches the reader as an empty module rather than as a missing one.
//
// The 11 board files with zero bare modules are the byte-string corpus (binary*.wast,
// utf8-*.wast, custom.wast, obsolete-keywords.wast) — files whose every module is a
// `(module binary ...)` form, so zero is correct there and a floor demanding one per file
// would be wrong. That is why (2) floors the *count of files* rather than asserting a
// per-file minimum: the honest invariant is "most files have some", not "all do".
//
// Falsified three ways while writing it, each by introducing the defect it names and running
// the suite — and the third falsification corrected this comment rather than confirming it:
//
//   - `end: start` in parseNode's list arm → (3) fires, 2149 lines of it, first at
//     address.wast:3: "KindModuleText with an empty Source".
//   - `end: p.off - 1` (drop the closing paren) → TestNodeSpanIsExactSource fails on all four
//     spans and TestBareModuleSourceRoundTrips reports `unclosed list` on the re-parse. Not
//     caught here, and that is the right division of labour: this test bounds *how many*
//     spans exist, the sexpr tests bound *what they contain*.
//   - Classification loss — replacing the KindModuleText arm with KindUnsupported, which is
//     what a mis-scoped moduleFormKeyword would do — trips **boardFiles' own 240-file floor
//     first**, at 68. I had written that (1) and (2) catch this; they do, at 0 and 0, but only
//     once the outer floor is lowered out of the way. Recorded as measured rather than as
//     predicted, because "two floors catch it" and "one floor catches it and the other would
//     have" are different facts about the mechanism.
func TestBareModuleSpansAreNonEmptyAndPlausible(t *testing.T) {
	requireSuite(t)

	const (
		totalFloor = 2000 // measured 2146 (2143 before #459 gave the three annotated modules a head)
		// measured 242 of 256 board files. The denominator moved with #9's arm (254 → 256:
		// `memory_size3.wast` and `unreached-invalid.wast` are wholly `assert_invalid`, so they
		// were not board files at all before it) and the numerator did **not**, which is correct
		// and worth writing down — an `assert_invalid` module is not a bare `(module …)`, so a
		// file can enter the board without entering this distribution. The 14-file gap is the
		// byte-string corpus plus those two.
		filesFloor = 230
	)

	total, withAny := 0, 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		n := 0
		for _, c := range s.Commands {
			if c.Kind != KindModuleText {
				continue
			}
			n++
			// (3): the span is a real extent, not a degenerate one. Checked per command
			// rather than sampled — an empty span reaches text.ReadModule as a syntax
			// error attributed to the reader, so this is the assertion that keeps a
			// harness bug from being read as an engine bug.
			if len(c.Source) == 0 {
				t.Errorf("%s:%d: KindModuleText with an empty Source — a degenerate span",
					f, c.Line)
				continue
			}
			if c.Source[0] != '(' {
				t.Errorf("%s:%d: span starts with %q, want '(' — the extent is off its "+
					"opening paren", f, c.Line, c.Source[0])
			}
		}
		total += n
		if n > 0 {
			withAny++
		}
	}

	// vacuityBound, not floorBound: these two are *plausibility* bounds and their looseness
	// is the design (2000 against 2146, 230 against 242). Slack-checking them would fire on
	// a control working exactly as intended, and a gate that fires for reasons which are not
	// findings trains the reflex of scrolling past it. Routed through boardBound anyway, so
	// the exemption is named at one place rather than being an absence — TestEveryBoardBound-
	// IsChecked reads this call, and *a precondition that excuses a gate is licensed at one
	// place, or it is a hole*. (0013.)
	boardBound(t, "totalFloor", total, totalFloor, 0, vacuityBound,
		"the span mechanism is not retaining source, so commands the pass floor counts on are "+
			"back in the unsupported column")
	boardBound(t, "filesFloor", withAny, filesFloor, 0, vacuityBound,
		"a total floor cannot catch this: const.wast alone holds 402, so the distribution needs "+
			"its own bound")
}

// aggregateFiller is one table-filling function in the corpus whose body constructs aggregates —
// #236's population, derived rather than enumerated.
type aggregateFiller struct {
	File     string
	Line     int
	Families []string // sorted: which of struct/array/i31/convert this filler's own body constructs

	// Export is the name the filler is reachable by, or "" — six of the eleven declare one, and the
	// difference decides which assertion the filler can carry (see the premise check).
	Export string
}

// aggregateFamilies maps a GC construction family to the instruction mnemonics that build it.
// The *families* are the axis #172's ladder is cut on (rung 2 struct, rung 3 array, rung 4 i31,
// rung 5 slice 3 convert), so a filler's family set says which rung makes it executable.
//
// **`convert` is a family this map was missing, and the omission would have misdated five fillers.**
// `ref_cast.wast:13` writes slot 4 with `(any.convert_extern (local.get $x))`, so the filler needs
// `fb 1a` as surely as it needs `struct.new_default` — and with only three families named, widening
// `liveFamilies` at rung 4 would have declared it live while it still could not run, which is a
// premise check reporting a premise that is false. The same body is the specimen for slice 2's
// init-sequence lesson (a missing arm at write *k* leaves slots `k…N` at their defaults), and this is
// that lesson pointed at the *classifier* rather than at a forecast: a filler is only live when every
// family in its body is, so a family the vocabulary cannot see is a filler that looks readier than it
// is. Measured, not supposed: 5 of the 11 fillers use a conversion (`br_on_cast.wast:13`,
// `br_on_cast_fail.wast:13`, `extern.wast:14`, `ref_cast.wast:13`, `ref_test.wast:15`).
var aggregateFamilies = map[string][]string{
	"struct":  {"struct.new", "struct.new_default"},
	"array":   {"array.new", "array.new_default", "array.new_fixed", "array.new_data", "array.new_elem"},
	"i31":     {"ref.i31"},
	"convert": {"any.convert_extern", "extern.convert_any"},
}

// aggregateFillers derives #236's population: functions that write an aggregate into a *table*,
// so a failure to construct leaves storage at its default and every later vector silently asserts
// something other than what it was written to assert.
//
// # Why the population is per *function* and not per file, which measurement decided
//
// The first reader classified whole files and produced **6 files, all one class** — and #236's own
// text names other shapes, so a single-class partition was the trigger under-matching rather than
// a clean result (*a suspiciously clean result is a tell*). The mechanism: `ref_cast.wast` and its
// three siblings are **multi-module**, and the struct-only `$init` lives in the *second* module
// while the first module's filler also uses `array.new` and `ref.i31`. A file-level family union
// therefore reports every one of them as needing rungs 2-4, and the struct-only partition — the
// whole subject of this rung — comes out **empty**. Paren-matching each `(func …)` and classifying
// its own body separates them: **11 fillers in 3 classes**.
//
// That degradation is what the discrimination check below tests for, per #159: the struct-only
// partition being non-empty is a capability a file-level reader cannot exhibit at any count, and a
// floor alone cannot tell the two readers apart.
//
// The trigger is `(table.set` inside the body, and the *negative* is the load-bearing half: the
// eleven files that construct aggregates without being fillers — `struct.wast`, `array.wast`,
// `i31.wast`, `array_copy.wast` and the rest — are excluded on purpose, because their vectors *are*
// the constructions. A failed construction there fails the vector visibly, which is the ordinary
// case the board already reports. #236's parenthetical guessed `array.wast`/`array_copy.wast`/
// `i31.wast` into this population; measurement says they are not in it, and the reason is the
// mechanism rather than the count.
func aggregateFillers(t *testing.T) []aggregateFiller {
	t.Helper()

	var out []aggregateFiller
	for _, f := range boardFiles(t) {
		src, err := os.ReadFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s := string(src)
		for off, body := range wastFuncBodies(s) {
			if !strings.Contains(body, "(table.set") {
				continue
			}
			var fams []string
			for fam, mnemonics := range aggregateFamilies {
				for _, m := range mnemonics {
					if containsMnemonic(body, m) {
						fams = append(fams, fam)
						break
					}
				}
			}
			if len(fams) == 0 {
				continue
			}
			slices.Sort(fams)
			out = append(out, aggregateFiller{
				File:     f,
				Line:     strings.Count(s[:off], "\n") + 1,
				Families: fams,
				Export:   fillerExport(body),
			})
		}
	}
	slices.SortFunc(out, func(a, b aggregateFiller) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		return a.Line - b.Line
	})
	return out
}

// wastFuncBodies yields each `(func …)` form's byte offset and full text, by paren matching.
//
// Deliberately *not* line-oriented: the whole finding above is that a reader which cannot see a
// function's extent cannot tell a multi-module file's struct-only init from its mixed one. Strings
// are skipped so a `")"` inside a name cannot unbalance the count.
func wastFuncBodies(s string) map[int]string {
	out := map[int]string{}
	for i := 0; i+5 <= len(s); i++ {
		if s[i] != '(' || !strings.HasPrefix(s[i:], "(func") {
			continue
		}
		if c := s[i+5]; c != ' ' && c != '\t' && c != '\n' && c != '(' && c != ')' {
			continue // (func_something — not this form
		}
		depth, inStr := 0, false
		for j := i; j < len(s); j++ {
			if inStr {
				switch s[j] {
				case '\\':
					j++
				case '"':
					inStr = false
				}
				continue
			}
			switch s[j] {
			case '"':
				inStr = true
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					out[i] = s[i : j+1]
				}
			}
			if depth == 0 && j > i {
				break
			}
		}
	}
	return out
}

// fillerExport returns the name in the filler's own `(export "…")`, or "" if it has none.
//
// Hand-scanned rather than a regexp for the reason `containsMnemonic` below is: this package's
// readers are all written this way, and *lessons are indexed by shape* — the wrapped-arm grave (#105)
// was earned by re-deriving a pattern a sibling already had, so a one-use regexp import here would be
// a new shape to get wrong for no gain. A `(func …)` form cannot nest another `(export …)`, so the
// first occurrence in the body is the function's own.
func fillerExport(body string) string {
	const open = `(export "`
	k := strings.Index(body, open)
	if k < 0 {
		return ""
	}
	rest := body[k+len(open):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// containsMnemonic reports whether body uses exactly this instruction, not a longer one that
// starts with it — `array.new` must not match `array.new_default`, which is a different family
// member and, at rung 3, a different arm.
func containsMnemonic(body, m string) bool {
	for i := 0; ; {
		k := strings.Index(body[i:], m)
		if k < 0 {
			return false
		}
		k += i
		end := k + len(m)
		if end >= len(body) {
			return false
		}
		if c := body[end]; c == ' ' || c == '\t' || c == '\n' || c == ')' || c == '(' {
			return true
		}
		i = end
	}
}

// TestAggregateTableFillersRun is #236's live half: the premise check that the GC arms make the
// table fillers **execute**, asserted on the premise rather than on any pass count.
//
// # Why a pass count cannot answer this
//
// #236's specimen is `ref_cast.wast`, four of whose vectors passed *because* its table was
// all-null: `ref.cast (ref null $t0)` on a null slot succeeds, and on a populated slot of a
// subtype it also succeeds. The verdict is identical either way, so a pass that survives the change
// of its own premise is indistinguishable from a pass that was always right — and the file's fail
// count is unmoved for the same reason.
//
// # The refusal proxy dissolved when the region completed, so the assertion is re-pointed
//
// This test was born asserting **zero struct-family refusals**: while a filler cannot run, its file
// reports `no arm for opcode fb 00`, and once it runs that refusal is gone. With rung 5 slice 3 the
// 0xfb region is fully dispatched (`execFB`, 31 of 31) and `TestEveryFBSubOpcodeIsAnswered` now pins
// that directly — so the old assertion cannot fail for the reason it was written, and a control whose
// subject dissolved is **re-pointed, never retired**: the risk #236 names (a filler silently not
// running, so every later vector asserts something else) outlived the one symptom that used to
// betray it. A filler can still fail to run with every arm present — a validation refusal, a trap, an
// argument form the harness declines — and none of those emit `no arm`.
//
// So the premise is now read where it is actually decided: **the command that calls the filler must
// pass.** Six of the eleven fillers declare an export, `(invoke "init" …)` names it, and that command
// is scored — so a failure at its line, or a `Kind` that means the harness never asked, is the
// premise being false stated in one row. The refusal check is kept below for the five unexported
// fillers, whose callers this reader cannot resolve; it is the weaker instrument and says so.
//
// # Scoped to the space
//
// The population is derived (`aggregateFillers`) and partitioned by the *families a filler's own body
// constructs*, which is the axis the ladder is cut on. All four families now execute, so the live
// partition is the whole population — and note the widening was **not** just turning a knob: the
// vocabulary was missing `convert` entirely, so five fillers were classed as needing only rungs 2-4
// while their slot-4 write needed `fb 1a`. Turning the knob at rung 4 would have made this test green
// on a false premise, which is the failure mode it exists to catch, one level up.
//
// #236's *other* half — `ref_eq.wast`'s 69 fails must drain rather than invert — is discharged
// separately by `refeqwitness_test.go` (#260), which pins the witness pairs rather than the count:
// the inversion the issue fears is a pass *gain* with the wrong sign, and a count cannot see the
// sign.
func TestAggregateTableFillersRun(t *testing.T) {
	requireSuite(t)

	fillers := aggregateFillers(t)

	// Exact counts beside the floors, because both are knowable here and they are different
	// instruments: the floor catches a moved corpus or a reader that stopped matching, the exact
	// count catches a small silent loss (#105). A suite-pin bump updates these deliberately, with
	// the delta stated — it is not a number to relax when it disagrees.
	// wantExported is **7 and was first written 6**, which is worth keeping rather than quietly
	// correcting: the hand count summed the fillers exporting `"init"` and called that "the fillers
	// declaring an export", missing `array_new_elem.wast:89`'s `array-new-elem-contents` — a row that
	// was printed on screen in the same probe output the 6 came from. That is the print-don't-reason
	// law failing at the summing step rather than the measuring step, and the control caught it on its
	// first run, which is what an exact count beside a floor is for (#105).
	const wantFillers, wantExported, wantConverting = 11, 7, 5
	if len(fillers) < 8 {
		t.Fatalf("derived only %d aggregate table fillers, want >=8 — the population is empty or "+
			"the reader stopped matching; a comparison against an empty set agrees perfectly",
			len(fillers))
	}
	if len(fillers) != wantFillers {
		t.Errorf("derived %d aggregate table fillers, want %d — if the suite pin moved, update "+
			"this with the delta stated; if it did not, the reader lost rows silently", len(fillers), wantFillers)
	}

	// liveFamilies is what this engine can construct today. With rung 5 slice 3 that is all four,
	// so the partition below is expected to be total — which is exactly why the discrimination
	// checks that used to ride on a non-empty `pending` are re-pointed rather than kept.
	liveFamilies := map[string]bool{"struct": true, "array": true, "i31": true, "convert": true}
	var live, pending []aggregateFiller
	for _, f := range fillers {
		if allIn(f.Families, liveFamilies) {
			live = append(live, f)
		} else {
			pending = append(pending, f)
		}
	}
	if len(pending) != 0 {
		t.Errorf("%d fillers still classed as pending, but every GC construction family now has an "+
			"arm — either a family name below has drifted from the mnemonic list or the ladder is "+
			"less complete than `execFB`'s count claims: %v", len(pending), pending)
	}

	// **Discrimination, not a count** (#159), re-pointed. The capability under test is that this
	// reader is per-*function* and not per-file: the reader it replaced classified whole files and
	// produced an empty struct-only partition, because the multi-module files hold a mixed filler
	// beside a struct-only one. That was witnessed by a non-empty live/pending split, which the
	// completed ladder has now dissolved — so the witness moves to the property the split was only
	// ever a proxy for: **one file contributing two fillers with different family sets**, which a
	// per-file union cannot produce at any count.
	byFile := map[string]map[string]bool{}
	for _, f := range fillers {
		if byFile[f.File] == nil {
			byFile[f.File] = map[string]bool{}
		}
		byFile[f.File][strings.Join(f.Families, "+")] = true
	}
	split := 0
	for _, sets := range byFile {
		if len(sets) > 1 {
			split++
		}
	}
	if split == 0 {
		t.Errorf("no file contributes two fillers with different family sets — a per-file reader "+
			"unions them and cannot exhibit that, so this reader has degraded to a per-file one; "+
			"fillers=%v", fillers)
	}

	// The two sub-populations the assertions below split on, pinned exactly because each carries a
	// *different* strength of check and a silent drift between them would swap a premise assertion
	// for a refusal proxy without changing any count that is currently watched.
	exported, converting := 0, 0
	for _, f := range live {
		if f.Export != "" {
			exported++
		}
		if slices.Contains(f.Families, "convert") {
			converting++
		}
	}
	if exported != wantExported {
		t.Errorf("%d fillers declare an export, want %d — the premise check below only reaches "+
			"exported fillers, so this number is how much of the population carries the strong "+
			"assertion", exported, wantExported)
	}
	if converting != wantConverting {
		t.Errorf("%d fillers use a conversion, want %d — this is the family the vocabulary was "+
			"missing, and a zero here would mean the mnemonic list stopped matching and every one "+
			"of them silently re-classed as live-since-rung-4", converting, wantConverting)
	}

	// The GC refusal text, derived from the decoder's own table rather than typed, so a renamed or
	// renumbered opcode cannot leave this searching for a string nothing emits. Widened from the six
	// struct sub-opcodes to the whole region, because the population's fillers now need arms from four
	// families and a list stopping at `fb 05` would check the one family least likely to be missing.
	//
	// This is the **weak** half and is retained for the unexported fillers only, per the doc above:
	// with 31 of 31 dispatched no `no arm for opcode fb NN` can be emitted at all, so a green here is
	// currently guaranteed by `TestEveryFBSubOpcodeIsAnswered` rather than by any filler running. It
	// stays because the guarantee is contingent — a gate flip or a decoder widening re-opens it — and
	// a check that costs nothing to keep is not worth the argument to delete.
	var refusals []string
	for op := range uint32(0x1f) {
		if _, _, ok := binary.PrefixedOp(0xfb, op); !ok {
			continue
		}
		refusals = append(refusals, fmt.Sprintf("fb %02x", op))
	}
	if len(refusals) < 31 {
		t.Fatalf("derived %d refusal strings from the decoder's 0xfb table, want >=31 — the region "+
			"moved, and a short list would make every check below pass by not looking", len(refusals))
	}

	_, _, allOnEngine := allOnLane(t)
	for _, f := range live {
		s, err := ParseFile(filepath.Join(suiteDir, f.File))
		if err != nil {
			t.Fatalf("%s: parse: %v", f.File, err)
		}
		r := s.RunGated(allOnEngine())

		// Vacuity: a file contributing no scored vectors would satisfy the refusal check by
		// asking nothing (#236's own condition).
		scored := r.Pass
		for _, fs := range r.Buckets {
			scored += len(fs)
		}
		if scored == 0 {
			t.Errorf("%s scored 0 vectors, so its refusal check asserts nothing", f.File)
			continue
		}
		for key, fs := range r.Buckets {
			for _, refusal := range refusals {
				if strings.Contains(key, refusal) {
					t.Errorf("%s:%d is a table filler whose families all have arms, so it must run, "+
						"but %s still reports %d fails keyed %q — the filler did not execute and every "+
						"vector reading the table it fills is asserting something other than what it "+
						"was written to assert (#236)", f.File, f.Line, f.File, len(fs), key)
					break
				}
			}
		}

		// **The premise itself, for the fillers that can carry it.** The call is a scored command, so
		// "the init ran" is not a proxy here: a failure at its line says the table was left at its
		// defaults, and a `Kind` outside the invoke pair says the harness never asked — which is the
		// same premise being false, arriving in the column that does not read as a defect.
		if f.Export == "" {
			continue
		}
		// **The nearest call *after* the filler, not every call that shares its name.** Measured
		// requirement, not caution: `array_new_elem.wast` declares `array-new-elem-contents` in two
		// modules (`:38` and `:89`) with an `assert_return` for each (`:47`, `:103`), so a name-only
		// match would attribute the first module's verdict to the second module's filler — the
		// field-attribution law (#159), which is not first-match, here in its temporal form. Taking the
		// first matching command below the filler's line is the module-scoped reading, and it assumes a
		// script never re-declares an export *above* the module that fills the table; that assumption
		// holds across all eleven and is stated because it is an assumption.
		//
		// The kinds are the four that *call and score*: an `assert_return` is how six of these fillers
		// are reached, and restricting to bare `invoke` would have silently dropped
		// `array_new_elem.wast` from the strong check — the first draft did exactly that, and the row
		// it dropped is the one row whose filler is also its own assertion. Trap and exception kinds
		// are deliberately not accepted: a call expected to trap has not run to completion, so it
		// cannot witness a filled table, and a filler reachable only that way must surface here as
		// uncalled rather than pass on a technicality.
		callKinds := []Kind{KindInvoke, KindNamedInvoke, KindAssertReturn, KindNamedAssertReturn}
		var call *Command
		for i, c := range s.Commands {
			if c.Export != f.Export || c.Line <= f.Line || !slices.Contains(callKinds, c.Kind) {
				continue
			}
			call = &s.Commands[i]
			break
		}
		if call == nil {
			t.Errorf("%s:%d exports %q and no scored call below it names that export — either the "+
				"harness classed the call as unsupported, in which case the filler never ran and the "+
				"column says nothing, or this reader read the wrong export name", f.File, f.Line, f.Export)
			continue
		}
		for _, fs := range r.Buckets {
			for _, fail := range fs {
				if fail.Line == call.Line {
					t.Errorf("%s:%d calls the table filler at :%d and FAILED (%s) — expected %q, got "+
						"%q. Every vector reading that table is now asserting something other than what "+
						"it was written to assert, and the file's pass count cannot say so because a "+
						"null slot answers the same as a populated one (#236)",
						f.File, call.Line, f.Line, fail.Kind, fail.Expect, fail.Got)
				}
			}
		}
	}

	t.Logf("#236 population: %d fillers, all live (struct/array/i31/convert), %d exported and "+
		"carrying the premise check: %v", len(fillers), exported, fillers)
}

// allIn reports whether every family in fams is currently constructible.
func allIn(fams []string, live map[string]bool) bool {
	for _, f := range fams {
		if !live[f] {
			return false
		}
	}
	return true
}
