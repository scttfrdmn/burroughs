package spec

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Command is one directive from a .wast script.
type Command struct {
	Kind Kind
	Line int

	// Head is the directive's head atom as written — "assert_return", "module",
	// "invoke". Recorded for *every* command including unsupported ones, which is
	// the point: with per-command corpus selection (#52) the unsupported column is
	// 1345 lines rather than zero, and a bare total is a number nobody can act on.
	// Bucketed by head it is a work plan — "504 assert_return" names the
	// interpreter, "110 assert_invalid" names the validator.
	//
	// Kind says what the harness can *do* with a command; Head says what the
	// command *is*. Deriving one from the other in either direction loses
	// information: several heads map to KindUnsupported today and will map
	// elsewhere as components land, which is exactly the movement the column
	// exists to show.
	Head string

	// Module is the raw module image for a (module binary ...) form, with the
	// quoted byte strings concatenated.
	Module []byte

	// Source is the wat source text for a (module quote ...) form, with the quoted
	// strings concatenated. Distinct from Module because the two are different
	// languages: one is a byte image the decoder eats, the other is text a lexer must
	// read first. A single field would make the harness's own type system unable to
	// say which.
	Source []byte

	// Needs names the engine capability this command requires, or "" when the
	// command needs nothing beyond the decoder. It is what makes the fourth verdict
	// *derived* rather than assigned (decision 0010, guard 1): classify computes what
	// a command needs, the run loop asks what the engine has, and the gap is the
	// verdict. No per-vector allowlist — the fourth verdict's one citizen was needed
	// by 1236 commands at its creation, and there could not have been one.
	Needs Capability

	// Expect is the expected failure text for an assertion, e.g.
	// "integer too large". Matched by substring (decision 0003) — upstream
	// runners do the same, and it lets "alignment" work as a prefix of
	// "alignment must be a power of two" without special casing.
	Expect string

	// Invoke names the exported function an `assert_return` calls, with Args as its
	// arguments and Results as the expected return values.
	//
	// Three fields rather than one action struct, because the harness reads exactly one
	// action shape — `(invoke "name" arg*)` — and a struct would be a place for the other
	// script actions (`get`, `invoke $M`) to be half-modelled. When one of those becomes
	// askable it arrives as its own Kind, which is where the classification decision is
	// visible.
	//
	// **That promise was redeemed rather than reinterpreted**, and the shape is Scott's
	// ruling on it: `(invoke $M …)` is KindNamedAction, read by its own function, and
	// `invokeAction` was not widened. The unnamed form's three arms are untouched, which is
	// what the ruling means by confining the blast radius. `(get …)` is still half-modelled
	// and therefore still absent — see the `get` arm in classify for its count and what it
	// waits on.
	//
	// Empty for every other Kind. Args is nil for a nullary call, which is the common
	// shape: 6560 of the answerable population take no arguments.
	Invoke  string
	Args    []Val
	Results []Val

	// Target is the script-level `$name` of the module an action selects, empty when the
	// action runs against the most recent module command.
	//
	// **Keyed by the script identifier, which is a different map from Register's** — one
	// module can have either, both, or neither (decision 0017, part 2). Merging them would
	// make `(register "a" $M)` mean that `(invoke $M …)` and `(invoke "a" …)` name the same
	// thing, and the second form does not exist.
	Target string

	// Name is the module's own `$name` as written, empty for the 2195 unnamed forms.
	//
	// Recorded on module commands rather than derived at use, because the run loop is where
	// the binding happens and the grammar is classify's business. 52 module forms carry one.
	Name string

	// Register is the export-name string a `(register "n" [$m])` command binds, and Target
	// carries the optional `$m`. Empty for every other Kind.
	//
	// Its own field rather than reusing Invoke, though both hold a string from the script:
	// `Invoke` is a name *inside* a module and this is a name *between* modules, so a shared
	// field would put two namespaces under one identifier — the same argument that keeps
	// Module and Source apart.
	Register string
}

// Kind classifies a directive. Phase 1 recognizes the module and malformed
// forms and records everything else as KindUnsupported rather than dropping it
// — an unrun test must be visible on the board.
type Kind int

const (
	KindModuleBinary        Kind = iota // (module binary "...")
	KindAssertMalformed                 // (assert_malformed (module binary ...) "text")
	KindModuleQuote                     // (module quote "...")
	KindAssertMalformedText             // (assert_malformed (module quote ...) "text")
	KindModuleText                      // (module <wat body>) — source retained, #69
	KindAssertReturn                    // (assert_return (invoke "f" arg*) result*) — #7
	KindInvoke                          // (invoke "f" arg*) at top level — a state mutation, #7
	KindAssertTrapModule                // (assert_trap (module …) "text") — instantiation traps, 0015
	KindAssertTrapAction                // (assert_trap (invoke "f" arg*) "text") — a trapping call, #157
	KindRegister                        // (register "n" [$m]) — binds a module into the registry, 0017
	KindAssertUnlinkable                // (assert_unlinkable (module …) "text") — linking must fail, 0017
	KindNamedAssertReturn               // (assert_return (invoke $M "f" arg*) result*) — 0017
	KindNamedAssertTrap                 // (assert_trap (invoke $M "f" arg*) "text") — 0017
	KindNamedInvoke                     // (invoke $M "f" arg*) at top level — 0017
	KindAssertException                 // (assert_exception (invoke "f" arg*)) — an uncaught exception, #201 rung 2a
	KindAssertInvalid                   // (assert_invalid (module …) "text") — the module must fail validation, #9
	KindAssertInvalidBinary             // (assert_invalid (module binary …) "text") — decodes, then must fail validation
	KindAssertInvalidQuote              // (assert_invalid (module quote …) "text") — assembles, then must fail validation
	KindUnsupported                     // anything phase 1 cannot execute
)

// String names the question the harness asked, **in the suite's words, plus any distinction the
// Kind adds** (ruling: Scott, PR #364).
//
// The board measures the corpus, so its rows name the corpus's terms: `assert_invalid` is checkable
// against the `.wast` files by anyone, where a Go identifier is checkable only against source the
// reader does not have open. And the collapsing hazard is the other half of the same rule — two
// Kinds sharing one head atom must not print the same label, which the distinctness pass in
// `TestAssertInvalidKindsAreExactlyTheAssertInvalidForms` enforces — load-bearing for the ledger's
// bucket keys, not for display.
//
// Five strings were wrong under that rule until #364 fixed them, and all five the same way: **a
// bare head atom in a family whose siblings discriminate**, so the string named the head atom of a
// group rather than the Kind's own form. `module` is how the corpus spells the *text* form, sitting
// beside two wrapper Kinds; `assert_malformed` has no unwrapped form at all, its two Kinds being
// the binary and quote wrappers, so the bare atom named neither; `assert_return`, `invoke` and
// `assert_invalid` each left the unwrapped arm bare while their siblings carried a discriminator.
//
// **The fifth was defended in this comment before the control was written, and the defence was
// wrong.** The argument for keeping a bare `assert_invalid` was that the Kind's own corpus spelling
// *is* unwrapped — true, and not the question. `assert_trap` in this same switch already
// discriminates both of its arms (`(module)` and `(invoke)`), so the three bare arms made one
// switch inconsistent about one question, and a board reader can check consistency between rows
// while having no way to recover which form the corpus happens to spell bare. Recorded rather than
// quietly corrected because the sequence is the lesson: prose asserting the property the code
// lacked, then a control derived from the rule instead of from the prose, disagreeing with it.
//
// The three arms that stay bare — `register`, `assert_unlinkable`, `assert_exception` — are bare
// because *no sibling Kind subdivides them*, which is the rule's own condition and not an
// exception to it. A second Kind arriving under any of those heads makes them defects the moment
// it lands, and `TestKindStringsSpeakTheSuitesVocabulary` fails at that moment rather than at the
// next reading of this comment.
//
// `module text` is the one string the corpus does not literally contain, and it is declared rather
// than checked: the text form is distinguished by the *absence* of a wrapper, so its faithful
// spelling is the bare `module` — which would leave a board reader unable to tell it from the head
// atom of three Kinds. The added word is legibility bought against fidelity, named here because
// `TestKindStringsSpeakTheSuitesVocabulary` declares it as the one string it cannot check.
func (k Kind) String() string {
	switch k {
	case KindModuleBinary:
		return "module binary"
	case KindAssertMalformed:
		return "assert_malformed (binary)"
	case KindModuleQuote:
		return "module quote"
	case KindAssertMalformedText:
		return "assert_malformed (quote)"
	case KindModuleText:
		return "module text"
	case KindAssertReturn:
		return "assert_return (invoke)"
	case KindInvoke:
		return `invoke "f"`
	case KindAssertTrapModule:
		return "assert_trap (module)"
	case KindAssertTrapAction:
		return "assert_trap (invoke)"
	case KindRegister:
		return "register"
	case KindAssertUnlinkable:
		return "assert_unlinkable"
	case KindNamedAssertReturn:
		return "assert_return (invoke $M)"
	case KindNamedAssertTrap:
		return "assert_trap (invoke $M)"
	case KindNamedInvoke:
		return "invoke $M"
	case KindAssertException:
		return "assert_exception"
	case KindAssertInvalid:
		return "assert_invalid (module)"
	case KindAssertInvalidBinary:
		return "assert_invalid (binary)"
	case KindAssertInvalidQuote:
		return "assert_invalid (quote)"
	default:
		// **Bracketed, because this Kind has no head atom and must not read as though it did.**
		// It is not a suite form — it is the harness saying it recognized nothing — and the corpus
		// contains no `(<…` anywhere, so the brackets are a spelling no `.wast` file can produce.
		// It rendered a bare `unsupported` until #364, which sat in a board row beside real atoms
		// and beside the board's own `unsupported` *column*, two different things wearing one word.
		return "<unsupported>"
	}
}

// isAssertInvalid reports whether k is one of the `assert_invalid` forms — the three Kinds that
// assert *validation must refuse this module with this text* and differ only in how the module
// comes into being.
//
// **A predicate and not an equality test, because there used to be one Kind and readers were
// written against it.** The destination ledger's domain was `c.Kind == KindAssertInvalid`, and the
// 17-head slice made that a *sample* of its own subject rather than the subject: two new forms
// landed and the ledger counted neither, so its rows held still while the population under them
// grew. That is coverage-is-a-claim in an instrument that was correct when written, which is the
// only way this failure ever arrives. `TestGatedVectors` carried the identical equality in its
// bulk arm and failed *loudly* there, because its per-line arm counts the complement — the pair,
// one silent and one noisy from one cause, is grave #330.
//
// Enumerated here rather than derived from the name, so the hot readers do no string work — and
// checked against a name-derived domain by TestAssertInvalidKindsAreExactlyTheAssertInvalidForms,
// so a fourth form cannot join the enum without joining this. Two mechanisms, neither vouching
// for itself.
func (k Kind) isAssertInvalid() bool {
	return k == KindAssertInvalid || k == KindAssertInvalidBinary || k == KindAssertInvalidQuote
}

// The three questions the run loop's shared action arm asks about a Kind, as predicates
// rather than as equality tests spelled at each site.
//
// **Two axes crossed, which is why there were six action Kinds and not three.** An action
// selects a module (unnamed, or `$M`) and carries an expectation (values, a trap, nothing),
// and those are independent facts — `(assert_trap (invoke $M "f") "…")` is real, and so is
// every other cell. Scott's ruling was that the named form arrives as its own Kind rather
// than widening the unnamed reader, and a Kind per cell is what that costs; the alternative
// was one `KindNamedAction` discriminated by which of Expect/Results happened to be empty,
// which is a flag by another route and ambiguous besides — `(assert_return (invoke $M "f"))`
// with zero expected results exists.
//
// **`KindAssertException` is the seventh, and it broke the pairing rather than extending
// it** (#201 rung 2a) — a third expectation crossed with only the unnamed axis, since the
// corpus has no named form to give it a partner. The two-axis count above is the shape at
// six; a reader wanting the current total counts the Kind declaration block, not this
// comment, for `selectsModule`'s own stated reason.
//
// Predicates rather than literals at the use sites because the arm asks each question in more
// than one place, and *one concept, one trigger*: an eighth action Kind is admitted by
// extending one of these, not by finding every `c.Kind ==` in the loop.
func (k Kind) selectsModule() bool {
	switch k {
	case KindNamedAssertReturn, KindNamedAssertTrap, KindNamedInvoke:
		return true
	default:
		// A real fallback rather than a shrug, which is the condition `.golangci.yml`'s
		// `default-signifies-exhaustive` attaches to writing one: *every* other Kind runs
		// against the current instance, including Kinds not yet invented, so the negative is
		// the honest default and enumerating the thirteen others here would be a list to forget
		// to extend. Contrast the run loop's own switch, where a missing Kind must be loud.
		return false
	}
}

func (k Kind) wantsTrap() bool {
	return k == KindAssertTrapAction || k == KindNamedAssertTrap
}

func (k Kind) wantsNothing() bool {
	return k == KindInvoke || k == KindNamedInvoke
}

// wantsException is wantsTrap's own question for exception handling's outcome. One member
// today, not two: **zero `assert_exception` vectors name a module** — measured over the
// whole corpus (`grep -rn 'assert_exception' | grep '\$'` finds nothing across all 9 files
// that use the directive, tracked and legacy proposals both) — so there is no
// `KindNamedAssertException` to pair it with, unlike wantsTrap's two. A predicate with one
// member is not a shortcut around the pairing; it is what the corpus actually has, stated
// the same way selectsModule's own doc comment states its cell count.
func (k Kind) wantsException() bool {
	return k == KindAssertException
}

// Capability is an engine component a command may require before it can be
// answered. The registry is closed: a command may only be scored `unimplemented`
// via a registered capability, so a gap the harness invented is a loud
// classification failure rather than a quietly larger column (decision 0010,
// guard 2).
type Capability string

const (
	// CapNone means the command needs nothing beyond the binary decoder.
	CapNone Capability = ""

	// CapWatReader is the wat text-format reader — the lexer and parser that turn
	// (module quote "...") source into a module. **Declared** (engineCapabilities) since
	// the lexer was wired, and therefore no longer in capabilityIssues: the constant
	// outlives its registry entry because classify still has to say what a command needs,
	// whether or not the engine has it. The parser half is #8; a quote vector whose
	// verdict waits on it fails with a bucket, which is the work plan, not a fourth
	// column.
	CapWatReader Capability = "wat-reader"

	// CapInterpreter is the execution loop — the thing that turns a decoded module and an
	// export name into values (#7).
	//
	// **Declared and never registered**, which is the opposite of CapWatReader's history and
	// deliberately so. wat-reader was *registered* while its component did not exist, held
	// 1236 vectors in the fourth column, and retired when the reader landed. This capability
	// is born on the day its component runs, so it goes straight to the declared side and the
	// fourth column never sees it — guard 6's two arms are exclusive, so declaring it and
	// registering it would be the retirement-skipped failure at birth rather than at
	// retirement.
	//
	// That is not a loophole in guard 2. Guard 2 requires a needed capability to be
	// *accounted for*, as a tracked debt or as a declared component; a capability that has
	// its component wants the second, and registering a debt that was never owed would make
	// the registry overstate the engine's outstanding work — which
	// TestEveryNeededCapabilityIsRegistered's used-members loop fails on directly.
	//
	// Its population is not the whole `assert_return` corpus: classify admits only the shapes
	// the loop can be *asked* about, and everything else stays unsupported with its head
	// recorded. That is the classification seam (see assertReturn), and it is why this
	// capability's arrival moves the unsupported column rather than creating a debt anywhere.
	CapInterpreter Capability = "interpreter"

	// CapValidator is the type oracle — the pass that decides whether a decoded module is
	// well-formed enough to run (#9, decision 0002 Q3).
	//
	// **Declared and never registered, by CapInterpreter's motion rather than
	// CapWatReader's**: `internal/validate` exists in the commit that names this constant, so
	// there is no interval to track and no debt to owe. See CapInterpreter's comment for why
	// those two histories are the only two shapes a capability may have.
	//
	// It is a *separate* capability from CapInterpreter even though both are reached through
	// the encoder, because they answer different questions and can be absent independently: an
	// engine can validate a module it cannot run, which is exactly the state this repo is in at
	// slice 1 (the interpreter runs unvalidated modules today — ADR 0025's carve-out). Folding
	// validation into CapInterpreter would make `assert_invalid` unanswerable in any run that
	// lacks an interpreter, and would put the fourth verdict's own accounting behind a
	// component the question does not need.
	CapValidator Capability = "validator"
)

// capEntry is a registry entry: what tracks the gap, and what ends it.
//
// Retires is the entry's own death certificate, written on the day it is born.
// `unimplemented` describes components that do not exist yet, so its guards cannot
// be spatial the way `gated`'s all-on lane is — there is nothing to switch on. They
// have to be temporal instead: an entry states the condition under which it must be
// deleted, and a test enforces that the condition, once met, was acted on. An entry
// may not outlive its component (ruling: chat-Claude, PR #58).
type capEntry struct {
	Issue   string
	Retires string
}

// capabilityIssues is the registry, and both the tracking issue and the retirement
// condition are part of the entry rather than comments beside it.
//
// An entry bearing its issue is the design-debt-needs-a-tripwire rule (0006)
// applied to a verdict: `unimplemented` is a debt, and a debt with no tracking
// number is an intention. The map is what TestEveryNeededCapabilityIsRegistered
// reads, so an unregistered capability cannot reach the board.
//
// **Empty, and that emptiness is a retirement rather than an absence.** It held exactly
// one entry — CapWatReader, #53 — from the fourth verdict's creation (0010) until the wat
// reader landed. Its stated condition was "when a wat reader is wired and
// engineCapabilities declares CapWatReader: this entry is deleted in the same commit, and
// unimplemented(wat-reader) must be 0 — every one of its vectors converted to pass or
// fail, none left behind". That is what happened, in one commit, and guard 6's control
// (TestNoCapabilityOutlivesItsComponent) is what makes the claim checkable rather than
// asserted here: it fails if a declared capability is still registered, and it fails if a
// declared capability left any of its population behind.
//
// This is the intended ending, and it is the same shape as the deadcode allowlist's:
// a deferral retired by a component landing, not by an entry outliving the reason for it.
var capabilityIssues = map[Capability]capEntry{}

// engineCapabilities is what the engine actually has, stated explicitly rather than left
// to omission: guard 1 of decision 0010 says the classifier computes what a command needs
// and the engine declares what it has, so the engine's half has to be a declaration.
// Silence would be the same fact carried by an absence, and an absence cannot be read as
// a claim.
//
// Adding a member here is half of a retirement: the other half is deleting the matching
// capabilityIssues entry, and TestNoCapabilityOutlivesItsComponent fails if only one of
// the two happens. CapWatReader arrived by exactly that motion.
//
// CapInterpreter arrived by the *other* motion, and the difference is worth having written
// down because it is the shape a capability should have from now on: it was never
// registered, because its component landed in the commit that named it. A registry entry is
// for a gap that has to be *tracked over time*, and there was no interval here to track — so
// the honest history is one line in this map and no line in the other, rather than an entry
// born and retired in the same breath.
//
// A declaration here is a claim about the *engine*, and the run loop refuses to honour a
// claim with nothing behind it: a declared capability whose component is not wired into
// the run panics rather than scoring, which is where TestQuoteFormsHaveTheirReader's
// tripwire was re-pointed when its original subject dissolved. (The rename *was* the
// re-pointing — it was TestQuoteFormsAwaitTheirReader — and this citation kept the old
// name, which is the drift a stale test-name citation causes: it reads as a second,
// missing control. Swept with #88.)
var engineCapabilities = map[Capability]bool{
	CapWatReader:   true,
	CapInterpreter: true,
	CapValidator:   true,
}

// EngineCapabilities returns the capabilities the engine has, sorted. Board runners
// pass this rather than nothing, so what the board scores is derived from a
// declaration instead of from a forgotten argument.
func EngineCapabilities() []Capability {
	return sortedCaps(len(engineCapabilities), func(yield func(Capability)) {
		for c := range engineCapabilities {
			yield(c)
		}
	})
}

// EngineHas reports whether the engine declares a capability.
func EngineHas(c Capability) bool { return engineCapabilities[c] }

// RegisteredCapabilities returns the registry's members, sorted. Derived from the
// map rather than listed, for the reason every domain in this package is derived:
// an enumeration is a sample, and a sample has a blind spot by construction.
func RegisteredCapabilities() []Capability {
	return sortedCaps(len(capabilityIssues), func(yield func(Capability)) {
		for c := range capabilityIssues {
			yield(c)
		}
	})
}

func sortedCaps(n int, each func(func(Capability))) []Capability {
	caps := make([]Capability, 0, n)
	each(func(c Capability) { caps = append(caps, c) })
	sort.Slice(caps, func(i, j int) bool { return caps[i] < caps[j] })
	return caps
}

// CapabilityIssue returns the tracking issue for a capability, and false if it is
// not registered.
func CapabilityIssue(c Capability) (string, bool) {
	e, ok := capabilityIssues[c]
	return e.Issue, ok
}

// CapabilityRetirement returns the condition under which a capability's registry
// entry must be deleted, and false if it is not registered.
func CapabilityRetirement(c Capability) (string, bool) {
	e, ok := capabilityIssues[c]
	return e.Retires, ok
}

// Script is a parsed .wast file.
type Script struct {
	Path     string
	Commands []Command
}

// ParseFile reads and parses a .wast script.
func ParseFile(path string) (*Script, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, src)
}

// Parse converts .wast source into a command list.
func Parse(path string, src []byte) (*Script, error) {
	nodes, err := newParser(src).parseAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s := &Script{Path: path}
	for _, n := range nodes {
		if !n.isList() {
			return nil, fmt.Errorf("%s:%d: top-level atom %q", path, n.line, n.atom)
		}
		s.Commands = append(s.Commands, classify(n, src))
	}
	return s, nil
}

// classify turns one top-level form into a Command.
//
// src is the source the nodes were parsed from, needed for the one kind whose payload is
// *text the reader consumed* rather than a string literal it carried: a bare
// `(module <wat body>)`, whose extent is n.start:n.end (#69).
func classify(n node, src []byte) Command {
	head := n.head()
	switch head {
	case "module":
		// The module's own `$name`, read once for all three forms: it is a property of the
		// `(module …)` head rather than of which language the body is in, which is why it is
		// read before the forms divide. Empty for the 2195 unnamed forms.
		name, _ := scriptName(nodeAt(n, 1))
		if img, ok := binaryModule(n); ok {
			return Command{
				Kind: KindModuleBinary, Line: n.line, Head: head, Module: img, Name: name,
				// **CapValidator since #353**, and it is the one capability this form needs
				// syntactically — the same call `assert_invalid (module binary …)` makes and for
				// the same stated reason: the decoder is the component the harness has
				// unconditionally and has no constant, so the validator is what is left to
				// declare. It was `CapNone` while the arm scored decode alone, which was accurate
				// then.
				//
				// The two text forms keep `CapWatReader` rather than gaining this, which is not
				// an inconsistency: `Needs` names the *distinguishing* capability, their arm
				// checks the validator with `requireValidator`, and a form whose payload is bytes
				// has no reader to distinguish it.
				//
				// **What this changes for a caller that does not declare the validator is which
				// panic it gets, not whether it gets one**, and the first draft of this comment
				// claimed the fourth verdict instead — corrected here rather than quietly, since a
				// comment asserting the property its code lacks makes review confirm the bug.
				// `CapValidator` was retired from `capabilityIssues` when the validator landed, so
				// the gap check has no entry to score against and panics by design (guard 6's
				// ending). The gain is that the panic now comes from the gap check, naming the
				// capability and the remedy — *use RunGated, which derives from the declaration* —
				// instead of from inside the arm, where `requireValidator` can only report that a
				// `ValidateFunc` was missing. `TestClassifyAndRun` was the caller that surfaced
				// this: it *supplies* `Validate` and had simply not listed the capability in `Has`.
				Needs: CapValidator,
			}
		}
		// Named `quoted`, not `src`: the parameter above is the *script's* bytes and this is
		// the *module's*, and letting the second shadow the first would put two different
		// sources under one name in the one function that has to keep them apart.
		if quoted, ok := quoteModule(n); ok {
			return Command{
				Kind: KindModuleQuote, Line: n.line, Head: head,
				Source: quoted, Needs: CapWatReader, Name: name,
			}
		}
		// (module ...) with a bare wat body. **Askable since #69**, and the sentence that
		// used to stand here is the reason it was not:
		//
		//	the harness cannot *ask* about a wat body, because the s-expression reader has
		//	parsed it into nodes rather than holding its source text. A quote form hands
		//	over its source as a string literal; a bare body does not.
		//
		// True when written, and it named a *harness* defect rather than an engine gap —
		// which is why the fix is a span on node, not a fourth-verdict entry. The form's
		// own extent is its source: n.span is the `(module …)` text verbatim, the exact
		// shape the reader's `module_` production takes (parser.mly:1389), so no
		// reconstruction is involved and nothing is re-lexed to find the closing paren.
		//
		// Scored, not skipped: a bare module form asserts its source is *valid* wat, so a
		// reader rejecting one is a fail in a named bucket. That is the largest silent
		// population on the board becoming a work plan — 1119 commands across 57 files at
		// #69's measurement, against the 7 reachable quote modules that were the whole
		// accept-direction oracle until now. *A parser that rejects valid modules is worse
		// than one that misses an invalid one*, and the reason that was hard to act on is
		// that 1119 valid modules could not say so.
		// **`definition` and `instance` are script-grammar forms, not wat bodies**, and
		// handing them to the wat reader manufactures a failure out of a classification
		// error. `script_module` is `LPAR MODULE definition_opt option(module_var)
		// module_fields RPAR` (parser.mly:1422) — `definition` sits *outside* `module_`,
		// which is the production `text.ReadModule` implements (:1389) — and `instance`
		// (:1439-1444) is a different production altogether, referencing a module by name
		// with no fields at all.
		//
		// Caught by measuring the reds rather than by reading the grammar first: 9 of the
		// first 22 failures were these, clustered so tightly (10 SIMD files at the same
		// line, 5 in instance.wast) that the shape was the tell. *Gates never manufacture
		// malformedness* generalizes past gates — a **harness** that asks the wrong reader
		// invents a red the same way, and it would have been indistinguishable on the
		// board from an engine defect. The wat reader is right to reject `(module
		// definition …)`; it was never asked a fair question.
		//
		// They stay `unsupported` with the head recorded, so the column names them.
		//
		// **The sentence that stood here parked them in the wrong phase**, and the reference's
		// own grammar is what refutes it. It read:
		//
		//	Phase v3 (component model) is where `definition`/`instance` become answerable.
		//
		// `definition_opt` is a modifier on the **core** `script_module` production
		// (parser.mly:1417-1428) and `instance` is a core production beside it (:1437-1444);
		// `instance.wast` is in `test/core/`, not in a proposal directory. Nor is the purpose
		// component-model: `memory.wast:8` is `(module definition (memory 65536))` sitting in a
		// run of nine ordinary `(module (memory …))` commands, and `table.wast:9` is
		// `(module definition (table 0xffff_ffff funcref))` — upstream writes `definition` there
		// exactly because the module must be asserted **valid without being instantiated**, a
		// 4 GiB memory and a 4 G-slot table being valid and unallocatable. That is
		// decode-plus-validate with no interpreter, which is v0 work and not v3's.
		//
		// So these 9 are a **harness widening**, tracked on #320 with the rest of the column's
		// census: a `definition` form validates and does not instantiate, an `instance` form
		// instantiates a named definition, and 0017's registry is where the second one's state
		// already lives. What the wrong premise cost was not the sentence — it was 9 vectors
		// filed three rungs up the ladder, where nothing in v0 would come looking. *Comments and
		// ADRs are testimony too, and where prose and the reference's executable disagree, the
		// executable outranks.*
		if kw := moduleFormKeyword(n); kw == "definition" || kw == "instance" {
			return Command{Kind: KindUnsupported, Line: n.line, Head: head}
		}
		return Command{
			Kind: KindModuleText, Line: n.line, Head: head,
			Source: n.span(src), Needs: CapWatReader, Name: name,
		}

	case "assert_malformed":
		// (assert_malformed <module> "expected text")
		if len(n.list) == 3 && n.list[1].isList() && n.list[2].isS {
			if img, ok := binaryModule(n.list[1]); ok {
				return Command{
					Kind:   KindAssertMalformed,
					Line:   n.line,
					Head:   head,
					Module: img,
					Expect: string(n.list[2].str),
				}
			}
			// `quoted`, matching the module arm above — and the shadow this avoids is the
			// one that arm's own comment already names ("letting the second shadow the
			// first would put two different sources under one name in the one function that
			// has to keep them apart"). It was written there and violated here, four
			// arms down, which is the defect-stated-as-the-rule shape at its cheapest:
			// review reads the warning and the violation as agreement. `govet`'s shadow
			// check is what noticed, not a reader.
			if quoted, ok := quoteModule(n.list[1]); ok {
				return Command{
					Kind:   KindAssertMalformedText,
					Line:   n.line,
					Head:   head,
					Source: quoted,
					Expect: string(n.list[2].str),
					Needs:  CapWatReader,
				}
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_invalid":
		// `(assert_invalid (module …) "text")`: the module is well-formed — it decodes — and
		// validation must refuse it with the spec's text. **2697 commands in the text form**, the
		// single largest classifiable population left on the board, and #9's own subject.
		//
		// **All three forms, as of the 17-head slice.** The deferral this comment used to carry was
		// explicit that the two non-text forms *would* convert "when the slice that wants them
		// arrives" and that each needed "their own Stratum attribution for a pre-validation
		// failure" — that slice is this one, and the attribution is the point rather than the cost.
		// 11 are `(module binary …)` and 6 are `(module quote …)`, the census the deferral named and
		// which still measures 11 and 6.
		//
		// **The three forms differ in what the precondition is, not in what the assertion is.** All
		// three say *validation must refuse this module with this text*; they disagree only about
		// how the module comes into being — a byte image (binary), wat source the assembler eats
		// (quote), or the inline body that is the same assembler on a different span (text). So the
		// Kinds split and the verdict logic does not, which is why the run loop's two new arms
		// delegate their tail to the same four-outcome switch the text form uses.
		//
		// `Source` rather than `Module`, for `assert_unlinkable`'s reason: the payload is wat, and
		// the encode-then-decode round trip that turns it into an image is the *caller's* three
		// steps, which is why ValidateFunc returns the Stratum instead of this arm deciding it.
		if len(n.list) == 3 && n.list[1].isList() && n.list[2].isS && n.list[1].head() == "module" {
			expect := string(n.list[2].str)
			// Each form binds its payload inside its own arm rather than to one name above the
			// switch, which is the malformed arm's shadow lesson taken as a *layout* rule instead
			// of as a warning to be careful: two different sources cannot end up under one name if
			// there is no shared name to end up under.
			switch moduleFormKeyword(n.list[1]) {
			case "":
				return Command{
					Kind: KindAssertInvalid, Line: n.line, Head: head,
					Source: n.list[1].span(src), Expect: expect,
					Needs: CapValidator,
				}
			case "binary":
				if img, ok := binaryModule(n.list[1]); ok {
					return Command{
						Kind: KindAssertInvalidBinary, Line: n.line, Head: head,
						Module: img, Expect: expect,
						// CapValidator and *not* a decoder capability, because there is no
						// such constant: the decoder is the one component the harness has
						// unconditionally, which is the whole reason the binary form was
						// described as needing nothing new.
						Needs: CapValidator,
					}
				}
			case "quote":
				if quoted, ok := quoteModule(n.list[1]); ok {
					return Command{
						Kind: KindAssertInvalidQuote, Line: n.line, Head: head,
						Source: quoted, Expect: expect,
						// CapValidator, matching the text form, which also assembles before it
						// validates and also declares only this. **`Needs` holds one
						// capability and this command genuinely has two** — the assembler and
						// the type checker — so the field names the distinguishing one and the
						// other rides implicitly. That is a real narrowing and it is recorded
						// on #296 as a fourth boundary-signature specimen rather than repaired
						// here: both capabilities are declared, so nothing lands in the fourth
						// column today and the loss is latent, not live.
						Needs: CapValidator,
					}
				}
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "register":
		// `(register "n" [$m])`: the module `$m` — or the most recent one, when the name is
		// omitted — becomes importable under the module name `"n"`. `runner.ml:314-355`'s
		// `registry`, and decision 0017 part 1.
		//
		// **A command with no verdict**, which no other Kind here is: it asserts nothing, so it
		// can neither pass nor fail. It was `unsupported` until now and that was honest while
		// nothing consumed it — 78 commands across 33 files, of which 54 omit the `$name` and 24
		// carry one. What made it stop being honest is that the *importing* modules downstream
		// are 605-and-counting fails whose bucket text names linking: an unrun `register` is a
		// skip whose cost is borne by other vectors, which is the bare-`invoke` lesson (#7)
		// exactly — *a harness that drops a state mutation is not neutral about the vectors
		// after it*.
		//
		// So it is scored as neither: the run loop performs it and counts nothing. See the arm.
		if c, ok := registerCommand(n); ok {
			// Kind/Line/Head stamped here rather than inside the reader, matching every other
			// arm — and **the first draft returned `c` bare**, which compiled, ran, and scored
			// all 78 as `KindModuleBinary` with a nil image, because `Kind`'s zero value is a
			// *valid member* rather than an unset marker. The board said `78 (module binary ...)
			// must decode` / `unexpected end`, an error naming a layer the input never reached.
			// See TestNoReaderLeavesKindAtItsZeroValue for the control that now catches the shape.
			c.Kind, c.Line, c.Head = KindRegister, n.line, head
			return c
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_unlinkable":
		// `(assert_unlinkable (module …) "text")`: the module is well-formed and valid, and
		// linking it must fail with the spec's text. 200 commands, of which **184 want
		// `incompatible import type` and 16 want `unknown import`** — measured over the corpus,
		// and the split is the whole reason this Kind can be scored at all: both verdicts come
		// out of the linker, so neither needs the validator.
		//
		// **It reads the module through the text path only**, because all 200 are the bare
		// `(module <fields>)` form — measured, no binary or quote variant of this shape exists —
		// which is the same measurement `KindAssertTrapModule` rests on and the same reason it
		// carries `Source` rather than `Module`.
		if len(n.list) == 3 && n.list[1].isList() && n.list[2].isS && n.list[1].head() == "module" {
			if kw := moduleFormKeyword(n.list[1]); kw == "" {
				return Command{
					Kind: KindAssertUnlinkable, Line: n.line, Head: head,
					Source: n.list[1].span(src), Expect: string(n.list[2].str),
					Needs: CapInterpreter,
				}
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_return":
		if c, ok := assertReturn(n); ok {
			return c
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_trap":
		// **Two populations, two Kinds, and the split is why there are two.** `assert_trap`
		// wraps either a module — `(assert_trap (module …) "text")`, a module that traps
		// *while coming to life*, which is what 0015 was written for — or an action,
		// `(assert_trap (invoke "f" arg*) "text")`, a call that traps once an instance
		// exists. They need different engine surfaces: the action shape needs an instance
		// and then a trapping call, the module shape needs instantiation itself to be able
		// to trap, which is the return-type change 0015 records. One Kind for both would be
		// one Kind two different components answer.
		//
		// The action shape sat in the unsupported column deliberately while the interpreter
		// had no trapping paths worth asking about, and this arm's comment said so. **That
		// sentence outlived its subject**: the trapping paths landed with the bulk trio, and
		// a recon (#157) put the population at 4903 commands of which 4876 are
		// classifiable under the rules already here and 2893 pass. A classification seam
		// left where a component used to be is a column that stops meaning "cannot ask" and
		// starts meaning "did not ask" — which is the disappearance decision 0010's guards
		// exist to prevent, arriving through the third column instead of the fourth.
		//
		// **The module-wrapping population is 54, not the 14 this arm was written for**, and
		// the correction came from measuring the corpus rather than from reading `data1.wast`:
		// 14 in data1, 14 in data.wast, 12 in elem.wast, 13 across linking*.wast and
		// start.wast. 0015 cited data1's 14 because that is the file the design question came
		// from, and a premise measured over the same sample the reader looks at is an echo
		// (grave #106). The 40 outside data1 need element segments, linking, or a start
		// function, so they will not pass here yet — they *fail* honestly rather than sitting
		// unclassified, which is the point of admitting the shape rather than the file.
		if len(n.list) == 3 && n.list[1].isList() && n.list[2].isS {
			if n.list[1].head() == "module" {
				// A wat-bodied module only: all 54 are the bare `(module <fields>)` form —
				// measured, no `binary` or `quote` variant of this shape exists in the corpus.
				if kw := moduleFormKeyword(n.list[1]); kw == "" {
					return Command{
						Kind: KindAssertTrapModule, Line: n.line, Head: head,
						Source: n.list[1].span(src), Expect: string(n.list[2].str),
						Needs: CapInterpreter,
					}
				}
				// No second `return` for a declined module form: `invokeAction` is keyed on
				// the head atom, so a `(module definition …)` falls through it and out the
				// bottom of this arm unsupported anyway. Written with the explicit return
				// first and **removed after the mutation that should have killed it did
				// not** — a branch no falsification can reach is a no-op wearing a guard's
				// clothes, which is the stillborn-control shape (#108) in engine code rather
				// than in a test.
			}
			// The action shape, read by the **same** `invokeAction` the `assert_return` and
			// bare-`invoke` arms use, so the three agree by construction about which actions
			// are askable rather than by three authors agreeing. The declines it carries are
			// decisions about the corpus — the module-selecting `(invoke $M …)` form, NaN-class
			// arguments — and a copy of them here would be a second answer to that question.
			// Graves #78, #105 and #106 are all one fact in two places; this arm is where the
			// fourth one would have gone.
			//
			// **47 fall out here, not the recon's 27, and the correction is the recon's own
			// blind spot rather than a change in the corpus.** Measured through this function —
			// `invokeAction` asked directly over every `assert_trap` node — the declines are 20
			// naming a module with `(invoke $M …)` and **27** carrying an argument `readConst`
			// cannot read (`ref.extern`, `ref.null`), against a population of **4923**. The
			// recon's population probe required element 1 to be a string, which is
			// `invokeAction`'s *own* accept condition, so it could not count the twenty forms
			// that fail on exactly that test: premise and subject shared an assumption, and both
			// halves of its 4903/27 were short by the same 20. That is grave #106's shape in a
			// census — *a premise measured over the same sample the code reads is an echo* — and
			// the figures here come from the code path, printed. They stay unsupported with the
			// head recorded.
			//
			// **No results to read**, which is the one field this shape does not share with
			// `assert_return`: a trapping call returns nothing, so the expectation is the trap
			// text in `Expect` and `Results` stays nil. That is also why the run loop's arm
			// cannot be shared with `KindAssertReturn` the way `KindInvoke`'s is — the two
			// disagree about whether an error is the answer or the failure.
			if c, ok := invokeAction(n.list[1]); ok {
				c.Kind, c.Line, c.Head = KindAssertTrapAction, n.line, head
				c.Expect = string(n.list[2].str)
				return c
			}
			// **The 20 module-naming declines that arm's comment measures, now admitted** — and
			// the shape is Scott's ruling: a *second reader*, not a widened first one. The
			// numbers above are the record of what this line converts, and they stay as written
			// rather than being edited down, because a comment that quietly loses the population
			// it once declined is how a measurement stops being checkable.
			if c, ok := namedInvokeAction(n.list[1]); ok {
				c.Kind, c.Line, c.Head = KindNamedAssertTrap, n.line, head
				c.Expect = string(n.list[2].str)
				return c
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_exception":
		// `(assert_exception (invoke "f" arg*))` — `parser.mly:1468-1469`'s
		// `LPAR ASSERT_EXCEPTION action RPAR`, one element shorter than `assert_trap`'s
		// grammar: there is no expected-text string at all (confirmed against the grammar
		// production, not inferred from a vector — `runner.ml:571-575`'s handler matches
		// only on *which exception* was raised, `AssertException act -> (match run_action
		// act with exception Exception (_, msg) -> () | _ -> Assert.error …)`, discarding
		// `msg`). So this arm has one fewer field to carry than KindAssertTrapAction: no
		// `Expect`, ever — a fact worth stating because every sibling arm in this switch
		// reads a trailing string and this is the one that structurally cannot.
		//
		// **Only the unnamed action shape is admitted, and that is the whole corpus, not a
		// declined remainder.** Measured directly against the grammar and against every
		// vector in both exception-handling proposals (tracked and legacy, 9 files, 41
		// uses of the directive): zero name a module. `invokeAction`'s own declines — a
		// NaN-class or unconvertible argument — still apply and still leave a vector
		// KindUnsupported with its head recorded, exactly as they do for `assert_trap`'s
		// unnamed half; there is no second reader to add here because there is no second
		// population to admit.
		if len(n.list) == 2 && n.list[1].isList() {
			if c, ok := invokeAction(n.list[1]); ok {
				c.Kind, c.Line, c.Head = KindAssertException, n.line, head
				return c
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "invoke":
		// A top-level `(invoke "f" arg*)`: a call made for its **effect**, with no
		// expectation attached. Scoring it unsupported was correct while nothing had
		// effects, and became a defect the moment memory did — the mutation silently did
		// not happen and the *following* `assert_return` read stale memory, so 73 vectors
		// across five files reported the interpreter computing a wrong value when it had
		// simply never been given the setup. `float_memory.wast:17` is the shape:
		// `(invoke "reset")` between two loads, where skipping it makes the second load
		// return the first one's NaN.
		//
		// That is *a skip is not a verdict* with the roles swapped — here the skip did not
		// pass by asking nothing, it made a neighbouring vector fail by answering a
		// question nobody had set up. A harness that drops a state mutation is not neutral
		// about the vectors after it.
		if c, ok := invokeAction(n); ok {
			c.Kind, c.Line, c.Head = KindInvoke, n.line, head
			return c
		}
		// The 2 top-level `(invoke $M …)` commands, which are the same state mutation aimed at a
		// named module. Small population, admitted for the reason the bare form was: an
		// unperformed mutation is charged to whatever vector reads the state next.
		if c, ok := namedInvokeAction(n); ok {
			c.Kind, c.Line, c.Head = KindNamedInvoke, n.line, head
			return c
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}
	}
	return Command{Kind: KindUnsupported, Line: n.line, Head: head}
}

// registerCommand reads `(register "n" [$m])`.
//
// Both shapes, and the optional `$m` is what the two maps of decision 0017 part 2 are for: the
// string is the *module name* an import will ask for, the `$m` is the *script identifier* of
// the module being bound. Written into different fields, because they are different namespaces
// — see Command.Register.
func registerCommand(n node) (Command, bool) {
	if n.head() != "register" || len(n.list) < 2 || len(n.list) > 3 || !n.list[1].isS {
		return Command{}, false
	}
	c := Command{Register: string(n.list[1].str), Needs: CapInterpreter}
	if len(n.list) == 3 {
		name, ok := scriptName(n.list[2])
		if !ok {
			return Command{}, false
		}
		c.Target = name
	}
	return c, true
}

// namedInvokeAction reads `(invoke $M "name" arg*)` into Target/Invoke/Args.
//
// **A second reader beside invokeAction rather than a widening of it, on Scott's ruling**, and
// the reasons he gave are worth keeping at the site because the cheaper-looking move is the one
// declined: widening would make one function answer two questions (does this action name a
// module, and is it askable), which is the one-authority law at function scale, and three arms
// share that reader so a new Kind confines the blast radius.
//
// **What it deliberately shares is the argument grammar**, by calling `readConst` under the same
// NaN-class rule — the fact that would be worth *nothing* duplicated, since which values are
// passable is one decision and graves #78/#105/#106 are all one fact in two places. So the split
// is at the shape and the join is at the values, which is the same seam `stringModule` cuts.
//
// Population, measured through this function rather than by grep: **132** `(invoke $M …)` forms,
// 110 under `assert_return`, 20 under `assert_trap`, 2 at top level — of which **124 classify**
// (102 / 20 / 2) and 8 do not. All 8 are in one twenty-line stretch of `elem.wast:1016-1030`, and
// all 8 decline on `ref.extern`: **2** on an argument, through this function's `readConst` loop,
// and **6** on an expected *result*, through `assertReturn`'s. Both counts are 0017's Q2 — a
// funcref or externref needs an instance to name — so this seam is where the split was drawn and
// the eight are the measurement that says the split is where the corpus puts it.
//
// **Both numbers, because keying the census on one of them undercounts.** The pair is quoted
// rather than the raw total for the reason the *previous* draft of this comment got it wrong: it
// said 130-of-132 args-readable and read as though 130 vectors were admitted, when the arguments
// are only half the gate. Expected and Got are different facts and a census keyed on either alone
// is short — here by exactly the 6 whose arguments were fine and whose results were not.
func namedInvokeAction(act node) (Command, bool) {
	if act.head() != "invoke" || len(act.list) < 3 {
		return Command{}, false
	}
	name, ok := scriptName(act.list[1])
	if !ok || !act.list[2].isS {
		return Command{}, false
	}
	c := Command{Target: name, Invoke: string(act.list[2].str), Needs: CapInterpreter}
	for _, a := range act.list[3:] {
		v, ok := readConst(a)
		if !ok || !v.isPassable() {
			return Command{}, false
		}
		c.Args = append(c.Args, v)
	}
	return c, true
}

// scriptName reads a `$name` atom, reporting false for anything else.
//
// **Checked positively — it must be an atom starting with `$` — where the unnamed readers check
// negatively**, that element 1 is not a string. The asymmetry is deliberate and it is the
// accept/reject direction again: a decline may safely be broad, because a shape nobody
// recognizes stays visible in the unsupported column with its head recorded; an *admission*
// must be narrow, because a wrong reading of an admitted shape produces a confident verdict
// about the wrong module. So the reader that says no uses the loose test and the reader that
// says yes uses the strict one.
func scriptName(n node) (string, bool) {
	if n.isList() || n.isS || !strings.HasPrefix(n.atom, "$") {
		return "", false
	}
	return n.atom, true
}

// nodeAt is n's i'th element, or a zero node when there is none.
//
// A helper rather than a bounds check at each site, because the alternative is
// `len(n.list) > 1 && …` repeated beside every optional-element read, and the zero node
// answers false to every predicate scriptName asks — an absent element and a wrong-shaped one
// are the same answer here, which is what makes collapsing them safe rather than convenient.
func nodeAt(n node, i int) node {
	if i >= len(n.list) {
		return node{}
	}
	return n.list[i]
}

// assertReturn reads `(assert_return (invoke "name" arg*) result*)`, reporting false for
// every other shape the head admits.
//
// # The classification seam
//
// This function is where the `assert_return` population divides, and the division is what
// makes the interpreter's arrival move the unsupported column instead of creating a debt in
// the fourth. What it admits is a *narrow* shape — one unnamed `invoke`, scalar constant
// arguments, scalar-or-NaN-class expected results — and everything else stays
// KindUnsupported with its head recorded, where the column names it. The forms deliberately
// left out, each measured over the corpus rather than guessed:
//
//   - `(either …)` results **are admitted now**, and the sentence they replace was wrong about
//     where they are: it read "**0** answerable, all of them in bulk and relaxed-SIMD files",
//     and the truth is **32 occurrences, all six of them relaxed-SIMD files, zero in bulk**.
//     Two errors in one clause. The count came from a scan of the answerable population at a
//     time when every relaxed vector was `gated` — so the 0 measured the gate, not the grammar
//     — and "in bulk" was an inference from the count rather than a measurement, bulk being
//     where unanswerable things usually are. A declined-shape list is a record of what was
//     measured when, so the wrong sentence is quoted rather than deleted; what it must not do is
//     stay in the present tense. `readResult` reads them (`Val.Alts`), and `Matches` is the
//     reference's `List.exists`.
//   - `(get "g")` actions, v128 constants, reference constants: their own strata.
//
// The list used to open with `(invoke $M "f" …)`, on the ground that it was **0** in the
// answerable population and "would need module-name state the run loop does not keep". Both
// halves were true and both have expired: the state is the registry (decision 0017), and the
// count was 0 only because *nothing after such a vector could run either*. It is **102 of 110**
// here now, admitted through a Kind of its own — the 8 short are `elem.wast:1016-1030`'s
// `ref.extern` forms, 6 of which decline on the results loop *below* rather than on the action
// reader, which is why the figure is stated as a pair. See namedInvokeAction for the breakdown.
// The sentence is quoted rather than deleted because a declined-shape list is a record of what was
// measured when, and an entry that vanishes leaves the reader unable to tell a resolved decline
// from an unnoticed one.
//
// A shape this declines is *not* a fail. The vector is valid; the harness simply cannot ask
// it yet, which is precisely the Unsupported/Fail distinction Result documents.
//
// # Why the whole form is read here rather than in the run loop
//
// Reading `(invoke …)` at classification time is what lets Needs be computed from the
// command — guard 1 of decision 0010 — and it is also what keeps the run loop free of
// grammar. A run loop that parsed nodes would be a second place that knows the constant
// grammar, and the readers in value.go are the first.
//
// The two action shapes are read by their own readers and the *results* by one loop below,
// which is where the seam falls: what differs between them is which module the call selects,
// and what is identical is what the call is expected to return.
func assertReturn(n node) (Command, bool) {
	no := Command{Kind: KindUnsupported, Line: n.line, Head: n.head()}
	if len(n.list) < 2 || !n.list[1].isList() {
		return no, false
	}
	c, ok := invokeAction(n.list[1])
	kind := KindAssertReturn
	if !ok {
		if c, ok = namedInvokeAction(n.list[1]); !ok {
			return no, false
		}
		kind = KindNamedAssertReturn
	}
	c.Kind, c.Line, c.Head = kind, n.line, n.head()
	for _, e := range n.list[2:] {
		// readResult, not readConst: a result position admits `(either …)` and an argument
		// position does not. See readResult's own comment for why that is two readers.
		v, ok := readResult(e)
		if !ok {
			return no, false
		}
		c.Results = append(c.Results, v)
	}
	return c, true
}

// invokeAction reads an `(invoke "name" arg*)` action into the Invoke/Args fields, leaving Kind,
// Line and Head to the caller.
//
// **One reader for the action, because a top-level `(invoke …)` is the same grammar.** It was
// split out of assertReturn when bare invokes became answerable, rather than copied: the arms
// that decline the module-selecting form and NaN-class arguments are decisions about which
// vectors are askable, and two copies of them would be two answers to that question. Same
// motive as `importedCount` — graves #78, #105 and #106 are all one fact in two places.
func invokeAction(act node) (Command, bool) {
	if act.head() != "invoke" {
		return Command{}, false
	}
	// `(invoke "name" arg*)`. A `$name` before the string is the module-selecting form,
	// declined — checked structurally (element 1 is not a string) rather than by looking
	// for a `$`, so any other shape upstream adds is declined too rather than misread.
	if len(act.list) < 2 || !act.list[1].isS {
		return Command{}, false
	}
	c := Command{Invoke: string(act.list[1].str), Needs: CapInterpreter}
	for _, a := range act.list[2:] {
		v, ok := readConst(a)
		if !ok || !v.isPassable() {
			// A NaN *class*, a bare `(ref.func)`/`(ref.extern)` type-pattern, or a bare
			// `(ref.null)` in an argument position is not a value that can be passed — each
			// is a predicate. The asymmetry is enforced here rather than in the matcher,
			// because it is a statement about which vectors are askable, and that is this
			// function's subject. See Val.isPassable.
			return Command{}, false
		}
		c.Args = append(c.Args, v)
	}
	return c, true
}

// binaryModule extracts the image from (module [$name] binary "..." "..."),
// concatenating the byte strings. It reports false for any other module form,
// including (module quote ...) and wat bodies.
func binaryModule(n node) ([]byte, bool) { return stringModule(n, "binary") }

// quoteModule extracts the wat source from (module [$name] quote "..." "..."),
// concatenating the strings. It reports false for any other module form.
//
// The suite's quote forms carry one source line per string literal with no
// separator, e.g. (module quote "(func (nop)" "(nop))") — concatenation is what the
// reference does, and the newlines the vectors rely on are inside the literals.
func quoteModule(n node) ([]byte, bool) { return stringModule(n, "quote") }

// moduleFormKeyword returns the bare keyword that follows `module` and an optional `$name`,
// or "" when the next element is a list, a string, or absent.
//
// It is how the script grammar's `definition_opt` (parser.mly:1417) and the `instance` form
// (:1439) are told apart from a wat body, whose next element is always a `(field …)` list.
// Deliberately *not* a keyword allowlist: it reports whatever atom is there, so the caller
// names the forms it knows and an unrecognized one falls through to the wat reader rather
// than being silently reclassified. An allowlist here would be a second place that has to
// learn every script-level keyword upstream adds.
func moduleFormKeyword(n node) string {
	if n.head() != "module" {
		return ""
	}
	i := 1
	if i < len(n.list) && !n.list[i].isList() && !n.list[i].isS && strings.HasPrefix(n.list[i].atom, "$") {
		i++
	}
	if i >= len(n.list) || n.list[i].isList() || n.list[i].isS {
		return ""
	}
	return n.list[i].atom
}

// stringModule reads the (module [$name] <keyword> "..." "...") shape shared by the
// binary and quote forms, which differ only in that keyword.
//
// Factored on arrival of the second caller rather than in advance — 0006's rule, and
// the seam is cut where the fact actually is. What the two forms do *not* share is
// what happens to the bytes afterwards, which is why Command keeps Module and Source
// as separate fields: the shape is common, the language is not.
func stringModule(n node, keyword string) ([]byte, bool) {
	if n.head() != "module" {
		return nil, false
	}
	i := 1
	// Optional $name, as in (module $M1 binary "...").
	if i < len(n.list) && !n.list[i].isList() && !n.list[i].isS && strings.HasPrefix(n.list[i].atom, "$") {
		i++
	}
	if i >= len(n.list) || n.list[i].isList() || n.list[i].isS || n.list[i].atom != keyword {
		return nil, false
	}
	i++
	// Everything after the keyword must be a string literal.
	out := []byte{}
	for ; i < len(n.list); i++ {
		if !n.list[i].isS {
			return nil, false
		}
		out = append(out, n.list[i].str...)
	}
	return out, true
}

// Result is the outcome of running one script.
type Result struct {
	Path string

	Pass        int
	Fail        int
	Unsupported int

	// Unimplemented counts commands the harness asked and the engine has no
	// registered component to answer — the fourth verdict (decision 0010).
	//
	// The distinction from Unsupported is the whole reason this field exists, and it
	// is stated here because if the sentence blurs the two categories merge back into
	// mush: **Unsupported means the harness cannot ask** — no Kind recognizes the
	// form, so there is no question. **Unimplemented means the harness asked and the
	// engine lacks a named capability to answer**, so the question exists, is
	// well-formed, and has a registered debt standing between it and a verdict.
	//
	// Why not Fail, argued when the column was born and 1236 quote vectors would
	// otherwise have landed there: the fail column means *defect*, and the board's lone
	// failure (binary-gc.wast:1) was visible precisely because the column discriminates
	// wrong-answer from not-built. Scoring 1236 unread vectors as failures took it to
	// 1237, and a genuine regression tomorrow arrives as 1238 — invisible. A column that
	// cannot surface a new defect has stopped being an instrument, which is the lint-wall
	// failure (decision 0005) wearing a board's clothes.
	//
	// Gated is the architectural precedent rather than the argument: it exists because
	// scoring an unanswered question as a failure marks correct behaviour red. Gated is
	// absence-by-configuration; this is absence-by-construction.
	//
	// The category exists to **drain**, and it has: the wat reader's arrival converted
	// all 1236 in one commit, leaving this field at 0 board-wide. Note *how* they
	// converted, because the distinction above is what made it legible — 636 to pass and
	// 600 to fail, and those 600 are a fail column that means "the parser is not written"
	// rather than a fourth column that means the same thing. Once a component exists, its
	// gaps are buckets: a named expected string per vector, ordered largest first, which
	// is a work plan the fourth verdict could not produce. So the drain is not just this
	// field reaching zero, it is the queue moving to the instrument that can schedule it.
	// Decision 0004's version rule enforced the draining: no minor bump while a
	// milestone's Unimplemented is nonzero, and v0.1.0 requires zero.
	Unimplemented int

	// UnimplementedByCapability counts the fourth verdict by the capability each
	// command waited on, so the column is a work plan for the same reason
	// UnsupportedByHead is: "1236 unimplemented" named nothing, while
	// "1236 wat-reader (#53)" named an issue.
	//
	// Empty board-wide since the retirement, and the map stays because the mechanism is
	// the general one, not wat-reader's: the next capability admitted will populate it
	// from classify without this type changing.
	UnimplementedByCapability map[Capability]int

	// UnsupportedByHead counts unsupported commands by their head atom, so the
	// column is diagnosable rather than merely large.
	//
	// Before per-command corpus selection (#52) the board's unsupported count was
	// zero, and that zero was a *property of the byte-string corpus* — not a law of
	// the board. Deriving the corpus makes it 1345, and the doctrine is that this is
	// the honest board now: commands the engine cannot answer yet, counted and
	// visible, shrinking monotonically as components land. The law was always the
	// underlying one — nothing hides behind a skip (#29).
	//
	// A bare total would satisfy that letter and miss its point: 1345 is not a work
	// plan, while "504 assert_return, 398 module, 110 assert_invalid" names the
	// interpreter, the text grammar, and the validator. Same reason failures are
	// bucketed by expected spec text rather than counted — a number you cannot act
	// on is a number nobody reads, and a column nobody reads is where a regression
	// goes to hide.
	UnsupportedByHead map[string]int

	// Gated counts vectors the engine declined because a feature gate is off.
	//
	// This is a third verdict, not a flavour of the other two, and it exists
	// because gates partition *acceptance* (CLAUDE.md, "gates never manufacture
	// malformedness"). A gate-off engine meeting a memory64 module must reject it,
	// so scoring that rejection as a failure marks correct behaviour red — while
	// scoring it as a pass would be worse, since the engine never answered the
	// question the vector asks. Neither: it was not asked.
	//
	// binary_leb128_64.wast is the whole reason. Both its vectors carry i64 limits
	// flags, and before the payload grammars existed the decoder ignored the flags
	// and "passed" one of them. That pass was never earned — it accepted a
	// memory64 module with the memory64 gate off — and this counter is what stops
	// the honest fix from looking like a regression.
	//
	// The obvious abuse is returning a gate error for anything inconvenient, which
	// would empty the board by fiat. Two things hold that line: gated is printed
	// on its own board line, never folded into pass, and TestGatedVectors pins
	// which vectors are allowed to land here.
	Gated int

	// GatedAt is the line of every vector counted in Gated.
	//
	// **The count alone made the per-vector control unable to reach its own population.**
	// TestGatedVectors had to re-derive the set by calling the decoder itself
	// (`isGated(decode(c.Module))`), which works only for vectors whose decline happens at
	// `c.Module` — and the interpreter added a second path, instantiation, where a *text*
	// module is declined and there is no `c.Module` to ask about. 17 of the 33 declines were
	// outside the trigger's reach, so the allowlist covered half its population and said
	// nothing about the other half. *Coverage is to a trigger what a vacuity check is to a
	// comparison* (grave #78), and a re-derived trigger is how a control comes to be pointed
	// at a different set than the one it claims.
	//
	// So the run loop records what it counted, and the control reads that instead of asking a
	// second oracle the same question. `len(GatedAt) == Gated` is pinned by TestGatedVectors,
	// as its first assertion before it reads the lines — a parallel count and a parallel list
	// are exactly the two-places-know-one-fact shape, so the agreement is asserted rather than
	// assumed.
	//
	// The sentence above named TestGatedLinesAccountForEveryDecline, which has never existed:
	// the assertion is real and lives in TestGatedVectors, so the *name* was invented while
	// describing a control correctly. Caught by TestEveryCitedTestNameResolves on this PR — the
	// #93 mechanism finding exactly the class it was widened for.
	GatedAt []int

	// AltChoices records, for every `assert_return` that passed against an `(either …)`
	// expectation, which alternative the engine's answer matched.
	//
	// **The column exists because a pass against a disjunction is a verdict with a hole in it.**
	// `either` is the corpus stating that more than one answer is legal — relaxed SIMD's whole
	// non-determinism form — so the vector passes whichever member the engine produced, and the
	// board cannot distinguish two lowerings that both stay inside the set. Decision 0028 d1
	// promises more than the spec requires: this engine's relaxed lowerings are deterministic
	// **and architecture-uniform**. A guarantee that exceeds the spec cannot be measured by an
	// instrument that only checks the spec, and this is the missing half.
	//
	// Recorded by the run loop rather than re-derived by the control, for GatedAt's reason
	// exactly: a control that re-invokes to find out what happened is asking a second oracle the
	// same question, and the two can be pointed at different sets without either being wrong.
	// Populated on the **pass** path only — a vector that matched nothing is already a failure
	// with a bucket, and an unmatched disjunction has no choice to report.
	AltChoices []AltChoice

	// Bound counts `(register "name" $M?)` commands that successfully bound a name.
	//
	// **A sixth term because a register asks no question, and "not scored" must not be
	// allowed to mean "not accounted".** A register is the one command whose successful
	// outcome is a *state change* rather than a verdict: it binds an export name to an
	// instance and asserts nothing, so pass and fail are both invented verdicts and
	// `Unsupported` is the same fiction pointed the other way (the harness *can* ask; there
	// is simply nothing to ask). Counting it nowhere was the first draft, and
	// TestVerdictsPartitionCommands caught it immediately — 45 commands, across 24 files,
	// summing short of the command count. That is the partition control doing exactly what
	// its comment says it exists for: *adding a verdict is a chance to lose vectors*, and a
	// sixth outcome is an added verdict whether or not it scores anything.
	//
	// Excluded from Total() for Gated's and Unsupported's reason — the denominator is over
	// questions asked — so a script full of registers cannot inflate a pass rate. The
	// **failing** register is a different fact and stays in Fail: it means a name the script
	// will import from later is bound to nothing, and the vectors that then report `unknown
	// import` are a cluster whose cause is that failure. See the KindRegister arm.
	Bound int

	// Failures, bucketed by expected spec text. The bucket key names exactly
	// which check is missing or wrong, which makes the board a priority queue:
	// the biggest bucket is the next issue to take, and a bucket reaching zero
	// is a PR's measure of done (docs/laws/boards-and-buckets.md).
	//
	// **A key can be a union of several refusals rather than one, and reading it whole is what
	// separates a forecast of pay from a forecast of unshadowing.** The `no instance` arm keys an
	// `assert_return` by the *failing module's* error (the `target == nil` branch of the
	// KindAssertReturn/KindInvoke arm below),
	// and `interp`'s `Instance.build` accumulates initializer failures into `in.deferred` with
	// `errors.Join`, whose `Error()` is newline-separated — so one key names every refusal that
	// module hit, at every site, and the terms are **sites and not distinct causes**: `i31.wast`'s
	// 60-vector key names `fb 1c` *twice*.
	//
	// Measured on the rung-3 tree (`2f9d50c`, all-on lane 61764 / 536 / 0): 101 distinct keys, of
	// which **2** are multi-term, holding **71** of the 536 fails; widest key 2 terms
	// (`extern.wast`, `fb 1b` + `fb 1a`). Era-stamped rather than asserted, because both figures
	// move with every arm that lands.
	//
	// Two consequences, and the second is a forecast rule paid for by a miss:
	//
	//  1. The newline is *inside* the key, so a line-oriented grep over board text splits one
	//     bucket into several and mis-sums the total. Sum with `run(s).Buckets`, never with a
	//     grep — #161's standing rule, and this is the mechanism behind it.
	//  2. The **co-blocking probe reads sole-blockedness off the whole key**. A single-term key is
	//     a sole blocker and its size forecasts what an arm pays; an N-term key is a vector
	//     blocked on N things, and that vector is *already counted* by a search for each of them
	//     — so clearing one term re-keys the vector without moving the count of keys naming the
	//     others. #249's forecast got the pay half exact (187 predicted, 187 delivered, read off
	//     whole keys) and the unshadowing half wrong: it predicted rung 3 would grow the `fb 1c`
	//     buckets and they read 60/6/5 before and after, because the co-blocked vectors' keys
	//     already named `fb 1c`. The error was inferring *a module needs two rungs* ⇒ *a vector
	//     is blocked on both*; the union key answers that directly when it is not truncated at
	//     its first term.
	//
	//     **That last clause is the grave (#380), and it names a precondition this stratum never
	//     meets.** The rule holds for the `no instance` form, whose key *is* an `errors.Join` union.
	//     A **validator decline** key is single-term by construction — the validator stops at the
	//     first offending instruction — so single-term-ness carries no information about the blocker
	//     set, and reading it as sole-blockedness is the probe reporting its own blind spot. ADR
	//     0032's `sole=81 co=0` was exact on the population and void on the `co`: 7 of the 81
	//     re-declined one instruction later on `ref_eq`/`ref.as_non_null`/`br_on_null`/
	//     `br_on_non_null`. `ref_eq.wast` read **7 fail before and 7 fail after** with one key
	//     changing underneath — a decline moving *within* the column, which no single figure sees.
	//     Third payout of the shape (#249, #359's miss of 4, #380's 7), and the magnitude tracks how
	//     much of a region the slice claims, which is what a systematic blind spot looks like.
	//
	//     The blind spot is **asymmetric by direction**, measured: slice 7's reject side came out 27
	//     of 27 exact and its accept side 47 of 54. An `assert_invalid` module is minimal by
	//     construction, so single-term really does mean sole-blocked; a `module text` definition is a
	//     working module that reaches for whatever else it needs. So a forecast states the reject
	//     count as a number and the accept count as a number **with an upper-bound reading**.
	//
	// (Sited here on Scott's ruling, PR #250: a fact about an instrument lives at the instrument,
	// per one-truth — not in CLAUDE.md. The miss itself stays marked in #249.)
	Buckets map[string][]Failure
}

// Stratum names the engine component a failure is charged to — the fail column's
// partition key.
//
// **It replaced Failure.Kind as that key, and the reason is a defect the interpreter
// exposed rather than a refactor.** Kind worked while every failure was caused by the
// command it was reported against, so "which layer" and "which command" were one question
// with one answer. An `assert_return` whose module failed to produce an instance breaks that
// identity: the *command* is an assert_return and the *defect* belongs to whatever front end
// could not build the module — 13991 of them at the interpreter's arrival, every one the
// text encoder's frontier (#8), and all of them would have been charged to the execution
// layer by a Kind-derived switch. That is *an error from the wrong layer is evidence about
// where structure was lost*, aimed at the instrument instead of at the engine, and it is the
// same shape as #69's accident where KindModuleText fell into a `default` arm and reported 13
// text reds as decoder reds.
//
// So the run loop states the stratum rather than letting a reader derive it, and there is no
// zero-value default: StratumUnset is a loud failure in the partition check, because a layer
// assigned by omission is exactly how the previous two mixups happened.
type Stratum byte

const (
	// StratumUnset is the zero value and is never valid. Its purpose is to fail loudly.
	StratumUnset Stratum = iota
	// StratumBinary is the binary decoder.
	StratumBinary
	// StratumText is the wat reader — ReadModule, the entry point the board scores for
	// module and assert_malformed forms.
	StratumText
	// StratumEncode is the wat *encoder* — EncodeModule, reached only through
	// instantiation. Separate from StratumText because they are separate entry points
	// with separate frontiers: ReadModule answers 254 files' module forms with 0 reds,
	// while EncodeModule cannot yet emit most instruction bodies. Folding them together
	// would raise the reader's ceiling by 13775 and destroy its value as a regression
	// detector — one instrument per component, or neither is an instrument.
	StratumEncode
	// StratumExec is the interpreter.
	StratumExec
	// StratumValidate is the type oracle — `internal/validate`, #9's own column.
	//
	// # The boundary is causal, not numeric and not per-file
	//
	// **A row belongs to this stratum when validation is the layer that decided it.** Not "a row
	// from an `assert_invalid` command", and not "a row in a file the validator campaign owns" —
	// the deciding layer is the only key that stays right when the layers move underneath it.
	// (Ruling: Scott, on the #291 recon.)
	//
	// Three populations that look like this one and are not:
	//
	//   - The **encoder frontier** reached on the way to the validator. 11 `assert_invalid`
	//     vectors cannot be encoded at all (`(table …)` and `(start …)` module fields, #8), so
	//     validation never ran; they are StratumEncode, in the ceiling that already holds 517
	//     of their siblings.
	//   - The **decoder answering first**. 17 vectors expecting `constant expression required`
	//     are refused by `binary.DecodeModule` before the code section is walked, with the
	//     spec's own text — a *pass*, earned by the decoder. When the constant-expression slice
	//     lands, the same rows will be decided twice by two layers that agree; they stay passes
	//     and their stratum is whichever one refused first, because that is what "decided" means.
	//   - `assert_return`s whose module never validated. Those fail in the *exec* column with
	//     the §3 sentinel today, and 0025's carve-out is the reason: the interpreter runs
	//     unvalidated modules. **The carve-out retires by migration, one slice at a time**, and
	//     each slice reports its own migration as a row of its own — a count moving from
	//     `execFail` to a pass rather than a count appearing here.
	//
	// The one case the harness genuinely cannot attribute is a *decode* failure on an image the
	// *encoder* produced: both components are ours, the bytes are ours, and the disagreement
	// names no layer. Charged to StratumEncode — the conservative direction, since the front end
	// wrote the bytes under test and the decoder's ceiling is a structural 0 that a wrong charge
	// would falsely break. Measured at 0 today, and its one historical member was in the
	// encoder, which is the only evidence there is: the laneidx grave (`internal/text`'s
	// `laneidx`, this slice's own sweep) put 15 vectors here before it was fixed.
	StratumValidate
)

func (s Stratum) String() string {
	switch s {
	case StratumBinary:
		return "binary"
	case StratumText:
		return "text"
	case StratumEncode:
		return "encode"
	case StratumExec:
		return "exec"
	case StratumValidate:
		return "validate"
	default:
		// StratumUnset, and anything a future Stratum adds before its arm lands. Spelled
		// as the default rather than as a named arm so that a new stratum renders *something*
		// in a failure message instead of an empty string — the board's own switch over
		// Stratum is the place that must be exhaustive, and it errors loudly on unset.
		return "unset"
	}
}

// Failure is one assertion that did not hold.
type Failure struct {
	Line   int
	Expect string
	Got    string // the engine's error text, or "" if it accepted the module

	// Kind is the command's Kind — what was being scored, not who is at fault. Retained
	// for reporting; Stratum is the partition key. See Stratum for why the two separated.
	Kind Kind

	// Stratum is the component the failure is charged to.
	Stratum Stratum

	// Declined marks a failure the engine *refused to answer* — the component reached the
	// vector, recognized what it was being asked, and reported that the rule deciding it is
	// unwritten. Set by the assert_invalid arms and, since #341, by the module-definition arm
	// (`validate.ErrUnsupported` either way), which is why it is a bool here rather than a second
	// Stratum: the stratum still says *which* component, and this says *whether the component
	// answered*.
	//
	// **It is a fail either way, and the field exists so that controls about something else can
	// say so.** A decline belongs in the fail column with its opcode in the bucket key — that is
	// the bucketed-failures discipline and the work plan for the next slice. But a control whose
	// subject is the reference-value boundary, asserting 0 fails in `table_get.wast`, is not
	// about whether slice 1 types `table.get`; without this field its only options are to trip on
	// a vector it has nothing to say about or to enumerate lines, and an enumerated exclusion
	// inherits today's sample. See the two boundary controls that filter on it, and the board's
	// own decline/admission split, which is a bidirectional check that this flag and the
	// stratum count agree.
	Declined bool

	// Accepted marks the opposite refusal: the component ran to completion and said **yes** to a
	// vector the corpus says is invalid. Today only the assert_invalid arm sets it, on the same
	// branch that keys the `assert_invalid accepted, expected:` bucket.
	//
	// **It is a bool for Declined's reason and it exists for a reason Declined did not have: the
	// arithmetic that used to identify this population stopped identifying it.** Slice 1 could read
	// the admission stratum off `validateFail − validateDeclined`, because the third thing a
	// validate-stratum failure can be — an honest refusal whose message the corpus disagrees with —
	// was **exactly 0** board-wide in that stratum, and a subtraction over a two-element partition
	// needs no third label. Slice 2 made it 4 (the alignment vectors carrying a second defect), and
	// the subtraction silently began reporting 162 for a population of 158, with four *refusals*
	// inside a constant whose whole documented purpose is the accept direction.
	//
	// So the arm states which of its outcomes it took instead of leaving it to be recovered by
	// subtraction from a partition whose size was an accident of the measurement. A count that is
	// right only while some other count is zero is not a count of anything.
	Accepted bool

	// OverRejected marks the accept direction's own defect: validation ran to completion and said
	// **no** to a module the corpus asserts is valid. Set only by the module-definition arm, which
	// is the only arm that asks the validator a question whose right answer is yes.
	//
	// **It is Accepted's mirror and it exists for Accepted's reason, applied before the arithmetic
	// could go wrong rather than after.** Without a flag of its own this population lands in the
	// `default` arm of the validate stratum's split — the wrong-message case, "an honest refusal
	// whose text the corpus disagrees with" — and that description is false of it twice over: there
	// is no expected text to disagree with, because a module definition states no expectation, and
	// the refusal is not honest, because the corpus says the module is valid. Folding it there
	// would also put 13 rows inside a constant standing at 0, so the first over-rejection would
	// read as a wrong-message regression in a population that has none.
	//
	// The distinction from Declined is *whether the validator claimed to know*: a decline says the
	// rule is unwritten (#9's next slice owes it), an over-rejection says the rule is written and
	// wrong. The two drain by different mechanisms, which is the same argument that separated
	// isDeclined from isGated one layer up.
	OverRejected bool
}

// AltChoice is one `(either …)` expectation and the alternative the engine's answer matched.
//
// `Text` carries the alternative's own printed form and is what a pin should assert against, in
// preference to `Alt`. The index is a position in the corpus's list, so an upstream reordering
// moves it without any lowering having changed; the text is the *answer*, which is what decision
// 0028 d1's guarantee is about. `Of` is beside them so a reader can see how wide the freedom was
// — and so a corpus that collapsed a disjunction to a single alternative cannot leave a pin
// looking satisfied while asserting nothing.
type AltChoice struct {
	Line   int    // the assert_return's line
	Result int    // which result of that command, for the multi-value case
	Alt    int    // the matching alternative's index in the corpus's list
	Of     int    // how many alternatives the expectation offered
	Text   string // the matching alternative's printed form — the pinned quantity
}

// Total is the number of assertions actually executed — the denominator of the
// pass rate.
//
// Gated is excluded on purpose: the engine returned no verdict on those vectors,
// so counting them would make the denominator claim coverage the run did not
// have. Unsupported and Unimplemented are excluded for the same reason — the
// question was never asked, or was asked of a component that does not exist. The
// three exclusions and the two counted verdicts should always sum to the command
// count, which TestVerdictsPartitionCommands pins.
//
// **This exclusion is load-bearing, not cosmetic** (#52, ruling recorded there).
// While the corpus was hand-listed byte-string files, Unsupported was zero and the
// choice of denominator could not be observed. Per-command selection makes it 1345,
// so folding Unsupported in would render a 783/791 board as 783/2136 and read as a
// collapse when nothing regressed — and, worse, would make the ratio improve
// whenever a *component* lands rather than when a *verdict* is earned. The
// denominator is over what was asked. TestDenominatorExcludesUnaskedCommands is
// the control that says so, because a comment cannot fail.
//
// # A conversion lowers the column; an admission raises the denominator
//
// The vocabulary for reading a movement of these numbers, and the two operations have
// **different honest signatures**, which is the whole reason to name them apart:
//
//   - A **conversion** takes a command the harness already saw and moves it between
//     columns. `Unsupported` (or `Gated`) falls, `Pass` or `Fail` rises, and `Total()`
//     rises by the same amount. The command count does not move.
//   - An **admission** makes a command *exist*. A form the parser did not emit, or a file
//     `boardFiles` did not select, arrives — so the command count rises, `Total()` rises,
//     and nothing falls anywhere.
//
// Both look like progress and only one drains a column, so a delta quoted without saying
// which it is cannot be checked. The distinction is not academic: it was minted by getting
// it wrong. 0015 made 54 `assert_trap`-wrapping-a-module forms scorable, 40 of which are a
// conversion off `Unsupported`; the remaining 14 are `data1.wast`, a file that held no
// scorable command and was therefore **not on the board at all**, so its vectors are an
// admission and the board's file count moved 253 → 254. `54 − 40 = 14` produces the right
// number by arithmetic while calling an admission a conversion, and the ceiling's comment
// said exactly that until the two sets were actually differenced.
//
// The practical test, since reasoning-by-subtraction is what fails here: **difference the
// command keys against the previous revision** rather than subtracting totals. Totals agree
// with both stories; the key sets do not.
func (r *Result) Total() int { return r.Pass + r.Fail }

// gate records a feature decline: the counter and the line, in one call.
//
// One method rather than two statements at five sites, because a count and a list of the
// things counted are one fact in two places and the fifth site is where they would have
// drifted. See Result.GatedAt.
func (r *Result) gate(c Command) {
	r.Gated++
	r.GatedAt = append(r.GatedAt, c.Line)
}

// UnimplementedByCapabilityBySize returns the capabilities blocking the fourth
// verdict, largest first — the same work-plan ordering as the other two columns.
func (r *Result) UnimplementedByCapabilityBySize() []Capability {
	keys := make([]Capability, 0, len(r.UnimplementedByCapability))
	for k := range r.UnimplementedByCapability {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if r.UnimplementedByCapability[keys[i]] != r.UnimplementedByCapability[keys[j]] {
			return r.UnimplementedByCapability[keys[i]] > r.UnimplementedByCapability[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// UnsupportedByHeadBySize returns unsupported head atoms ordered largest first —
// the component work plan, as BucketsBySize is the decoder's.
func (r *Result) UnsupportedByHeadBySize() []string {
	keys := make([]string, 0, len(r.UnsupportedByHead))
	for k := range r.UnsupportedByHead {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if r.UnsupportedByHead[keys[i]] != r.UnsupportedByHead[keys[j]] {
			return r.UnsupportedByHead[keys[i]] > r.UnsupportedByHead[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// BucketsBySize returns bucket keys ordered largest first — the work plan.
func (r *Result) BucketsBySize() []string {
	keys := make([]string, 0, len(r.Buckets))
	for k := range r.Buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(r.Buckets[keys[i]]) != len(r.Buckets[keys[j]]) {
			return len(r.Buckets[keys[i]]) > len(r.Buckets[keys[j]])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// Board renders the result in the PR Board-line format.
func (r *Result) Board() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d/%d pass", r.Path, r.Pass, r.Total())
	if r.Fail > 0 {
		fmt.Fprintf(&b, ", %d fail", r.Fail)
	}
	if r.Unsupported > 0 {
		fmt.Fprintf(&b, ", %d unsupported", r.Unsupported)
	}
	// Its own column, never folded into pass: a vector the engine declined to
	// judge is not a vector it judged correctly.
	if r.Gated > 0 {
		fmt.Fprintf(&b, ", %d gated", r.Gated)
	}
	// Likewise its own column, and specifically not folded into fail — that is the
	// whole point of decision 0010.
	if r.Unimplemented > 0 {
		fmt.Fprintf(&b, ", %d unimplemented", r.Unimplemented)
	}
	// Rendered so the board's own line sums to the command count, which is what
	// TestVerdictsPartitionCommands asserts and what an unrendered sixth term would
	// quietly break for a human reading a Board line. Called "bound" rather than a
	// verdict word because it is not one: N names now resolve.
	if r.Bound > 0 {
		fmt.Fprintf(&b, ", %d bound", r.Bound)
	}
	if len(r.Buckets) > 0 {
		b.WriteString("\n  failures bucketed by expected spec text (largest first):")
		for _, k := range r.BucketsBySize() {
			fmt.Fprintf(&b, "\n    %3d  %s", len(r.Buckets[k]), k)
		}
	}
	// The fourth verdict's work plan, keyed by capability and carrying the tracking
	// issue: a column that names an issue is a column someone can close.
	if len(r.UnimplementedByCapability) > 0 {
		b.WriteString("\n  unimplemented by capability (largest first):")
		for _, c := range r.UnimplementedByCapabilityBySize() {
			issue, ok := CapabilityIssue(c)
			if !ok {
				// Unreachable via the run loop, which rejects unregistered capabilities
				// before counting them — printed rather than hidden so that if it ever
				// happens the board says so instead of showing a bare name.
				issue = "UNREGISTERED"
			}
			fmt.Fprintf(&b, "\n    %5d  %s (%s)", r.UnimplementedByCapability[c], c, issue)
		}
	}
	// The unsupported column's own work plan, printed for the same reason the
	// failure buckets are: it names which component each unrun vector is waiting
	// on, so the number is actionable rather than merely honest.
	if len(r.UnsupportedByHead) > 0 {
		b.WriteString("\n  unsupported by command (largest first):")
		for _, k := range r.UnsupportedByHeadBySize() {
			fmt.Fprintf(&b, "\n    %5d  %s", r.UnsupportedByHead[k], k)
		}
	}
	return b.String()
}

// DecodeFunc is the engine entry point under test. It returns the error the
// engine produces for a module image, or nil if it accepts it.
type DecodeFunc func(image []byte) error

// ReadTextFunc is the engine's wat entry point. It returns the error the engine
// produces for wat source text, or nil if it accepts it.
//
// **Error-only, and it stayed that way after an attempt to change it failed a measurement.** The
// 17-head slice needed a boundary that hands back the module, and the first draft widened this one
// — which made the three Kinds it serves (`KindModuleQuote`, `KindModuleText`,
// `KindAssertMalformedText`) run the engine's *build*-mode assembler instead of its recognizer, and
// **58 vectors** regressed from pass to fail because the emitter cannot yet write `(table …)`,
// `(start …)` or #77's symbolic locals (#8). The module a build-mode parse cannot produce is not a
// module the recognizer needs, so the two are different questions and get different signatures —
// exactly the argument the next paragraph already made against a mode flag, arrived at a second
// time by breaking the board. AssembleFunc is the widened boundary; decision 0011's error-only
// surface stands here.
//
// Injected rather than called directly, for the reason DecodeFunc is: this package is
// the oracle, and an oracle that imports the engine it scores can no longer be read as
// neutral (contract §0). The harness holds `[]byte` of source and asks; what answers is
// the caller's business.
//
// Distinct from DecodeFunc because the two eat different languages — a byte image and a
// text source. One func with a flag would make the harness's own type system unable to
// say which question it asked, the same argument that separated Command.Module from
// Command.Source.
type ReadTextFunc func(src []byte) error

// AssembleFunc is the engine's wat *assembler*: it returns the module image built from wat source
// text, or the error it produces trying.
//
// **The boundary ReadTextFunc could not become, and the reason is a measurement rather than a
// preference.** A reader that answers only `error` can say a source is clean without saying what is
// clean, which leaves `KindAssertInvalidQuote`'s second fact — validation refuses *this module* —
// with nothing to be about. Widening ReadTextFunc in place cost 58 regressions (see its comment),
// because recognizing and building are different depths over the same language: the recognizer
// stops at well-formedness, the assembler must emit every field, and the emitter is behind (#8).
//
// So there are two, on `DecodeFunc`'s own stated ground: *one func with a flag would make the
// harness's own type system unable to say which question it asked*. Here the flag would have been
// invisible — both modes take `[]byte` and both return an error — which is the worst version of
// that hazard, since nothing in the signature would have disagreed with a caller who chose wrong.
//
// A `[]byte` and not a decoded module, for the reason this package injects everything: a
// `*binary.Module` in this signature would make the oracle import the engine it scores (contract
// §0).
type AssembleFunc func(src []byte) ([]byte, error)

// GatedFunc reports whether an engine error means "a feature gate is off"
// rather than a verdict on the module. The harness must not sniff error text for
// this — the engine names its own gate errors, and a substring test here would be
// the harness guessing at the taxonomy it is supposed to be checking.
type GatedFunc func(error) bool

// TrapFunc reports whether an engine error is a *trap* — the guest program dying the way
// the spec says it may — rather than a verdict, a gate decline, or a component the engine
// does not have yet.
//
// **Injected for GatedFunc's reason, and it is the same hazard one Kind over.** An
// `assert_trap` supplies the trap text it wants and the arm matches it as a substring, so a
// *non-trap* error quoting that phrase would score a pass the engine never earned. The
// malformed arms make that collision impossible by asking `isGated` before matching rather
// than by hoping feature names never collide with spec strings; this is the same move, and
// without it the collision is invisible on the board by construction — the vector is green,
// the trap never happened, and no expected string in the corpus reaches far enough to say
// so (contract §9 G-3).
//
// A predicate rather than a `trap: ` prefix test, because the prefix is the *engine's*
// rendering convention and this package may not know it (contract §0). The caller holds
// both type systems and answers with `errors.As`.
type TrapFunc func(error) bool

// ExceptionFunc reports whether an engine error is an *uncaught exception* escaping to the
// call boundary — exception handling's own control-transfer outcome (`runner.ml:571-575`'s
// `AssertException`, matched against `Eval.Exception` by `run_action`'s own exception
// clause), distinct from a trap, a verdict, and an engine gap.
//
// TrapFunc's own reasoning applies unchanged, one Kind over: the harness must not sniff
// error text for this, and the caller — which holds both type systems — answers with
// `errors.As`. Required by the assert_exception action arm and by nothing else, so it is
// nil for every caller that does not score exceptions; see the wantsException() panic this
// mirrors for what a missing predicate must never do (#201 rung 2a).
type ExceptionFunc func(error) bool

// ValidateFunc runs the engine's validator over a module command's payload and reports
// whether the module type-checks.
//
// **It takes the whole Command and returns a Stratum, for InstantiateFunc's reasons exactly.**
// Validating a text-form module is three steps — encode, decode, type-check — so a failure is
// not attributable from the Command's Kind, and deriving the layer here would assign it by
// omission. The caller performed the steps, so the caller says which one broke: an encoder or
// decoder failure on the harness's own image is StratumEncode, and only the type-checker's own
// refusal is StratumValidate. The value is ignored when err is nil.
//
// **A nil error is a verdict — "this module is valid" — and for `assert_invalid` that is the
// fail.** Which is why this cannot be a predicate: the arm needs the *message* to match the
// vector's expected string per 0003, and it needs to distinguish a refusal from a decline. Both
// live in the error, and a `bool` would flatten them into the one answer that hides both.
type ValidateFunc func(c Command) (Stratum, error)

// DeclinedFunc reports whether an engine error means "this component has not implemented the
// rule that would decide this module" rather than a verdict on the module.
//
// GatedFunc's argument, one component over and with a different subject: a gate decline says a
// *feature* is off by configuration, and a decline here says a *rule* is unwritten in the slice
// that is shipping. Both are the third verdict rather than the first two, and both must be asked
// **before** the substring match — because a decline whose text happens to contain the vector's
// expected string would otherwise score a pass the validator never earned, which is the exact
// collision the malformed arms ask `isGated` first to prevent (contract §9 G-3).
//
// It is a separate predicate rather than a widened GatedFunc, because the two answers are not
// interchangeable on the board: a gated vector converts when Scott flips a gate, and a declined
// one converts when the next slice lands. Merging them would put both populations in one column
// and lose which mechanism drains it.
//
// A predicate rather than a text test, for TrapFunc's reason: the sentinel is the engine's, and
// the caller — which holds both type systems — answers with `errors.Is`.
type DeclinedFunc func(error) bool

// Instance is a module the engine has made executable, opaque to the harness.
//
// `any`, and the emptiness is the neutrality rule (contract §0) rather than laziness: the
// harness's job is to hold the thing between a module command and the `assert_return`s that
// follow it, and to hold it without being able to look inside. An interface with methods
// would be this package describing the engine's shape, which is the import it is forbidden
// to make wearing a type's clothes.
type Instance any

// InstantiateFunc turns a module command into something InvokeFunc can be called on, or
// reports why it could not.
//
// It takes the whole Command rather than a payload, and that is the classification being
// *handed over* rather than re-derived: the Kind already says which language the bytes are,
// so the caller switches on a decision the harness made instead of on a flag the harness
// invented. It is also why a third module language would not change this signature — the
// argument against `func(image, src []byte)` is the same one that separated DecodeFunc from
// ReadTextFunc, and a two-payload func with one nil is that flag by another route.
//
// **Its error is not a verdict.** The module command has already been scored by Decode or
// ReadText; this call exists only to carry a handle forward. An error here means the
// following `assert_return`s have nothing to run against, and the run loop reports that as
// its own bucket rather than as a decode failure counted twice.
//
// **It returns the Stratum on the error path, and that is the caller's to state rather than
// the harness's to derive.** Instantiation is more than one step — a text module is encoded
// and then decoded — so which component failed is not a function of the Command's Kind, and
// deriving it here would assign a layer by omission. That is the mistake Stratum was
// introduced to stop making; the caller performed the steps, so the caller says which one
// broke. The value is ignored when err is nil.
type InstantiateFunc func(c Command) (Instance, Stratum, error)

// LinkedInstantiateFunc is InstantiateFunc with the script's registry supplied.
//
// **A second field rather than a widened first one**, and unlike the named-action Kinds this
// is not a ruling but the same argument one level down: an engine that can instantiate a
// closed module cannot necessarily link an open one, so they are separate obligations and a
// nil here is a caller that has the one and not the other. Every existing caller keeps
// working, which is the property that matters — `sexpr_test.go`'s four Engine literals score
// module and malformed forms and have no business acquiring a linker.
//
// # Why the registry crosses as a map of Instance and not as a resolver
//
// The engine's own resolver type is `func(module, name string) (Extern, bool)`, and `Extern`
// is the engine's type — naming it in this signature is the import this package is forbidden
// to make. So the harness hands over what it owns (names → opaque instances) and the caller,
// which legitimately knows both sides, builds the resolver. That is the same seam InvokeFunc
// cuts for values: *the conversion happens in the one place that knows both type systems*.
//
// Nothing is passed for "the most recent module": an unnamed import in a script resolves
// through the registry only, which is `runner.ml`'s behaviour and not a simplification.
//
// # Why a struct rather than the bare map it used to be
//
// Because a name can be *unbound for a reason*, and the reason changes the verdict. See Registry
// and decision 0037.
type LinkedInstantiateFunc func(c Command, reg Registry) (Instance, Stratum, error)

// Registry is what a script has bound for imports to resolve against, in the two states a name
// can be in that a caller must tell apart.
//
// `Instances` is decision 0017's map: module name → opaque instance, written by `register`.
// `Gated` is the names whose most recent `register` named a module the engine **declined**, and it
// is here because the registry was the one of the run loop's three state slots carrying no gate
// state at all — `cur` has `curGated`, `named` has `namedGated`, and this map had nothing
// (#366). The cost of the omission was measured: 62 of the default lane's 81 exec-stratum fails
// were a downstream import reporting `unknown import` — true about the resolver, and a lie about the
// cause — where the reference expects `incompatible import type`. A gate consequence in the
// interpreter's fail column, and invisible there, since nothing on the path carries a gated marker.
//
// **The two fields are mutually exclusive by construction and that is load-bearing.** A successful
// register binds the name and clears its `Gated` mark; a declined register marks the name *and
// deletes any binding under it*. The delete is not tidiness: `register` is last-register-wins in the
// reference (`runner.ml:314`), so a re-register whose new module was declined must not leave the
// **stale** instance resolvable — an import satisfied from it would award a pass for a program the
// reference replaced, which is worse than the mis-attributed fail that motivated the change and is
// in the direction no board can see.
//
// **What a caller is expected to do with `Gated`** is return an error that answers yes to
// Engine.IsGated when a module it is about to instantiate imports from one of these names, so the
// arms' existing gate paths classify it. The check is the *caller's* because reading a decoded
// module's import section needs a decoder, which this package does not have and must not acquire;
// the classification stays the harness's, exactly as it does for IsTrap. Decision 0037 records why
// the alternative — binding a sentinel Instance under the name — cannot be built here.
type Registry struct {
	Instances map[string]Instance
	Gated     map[string]bool
}

// bind records a successful register: the two fields are mutually exclusive, so binding a name
// clears any gated mark it carried from an earlier declined register.
func (reg Registry) bind(name string, in Instance) {
	reg.Instances[name] = in
	delete(reg.Gated, name)
}

// decline records a register whose module the engine refused to answer for. The delete is the half
// that is easy to omit and the half that could award a false pass — see Registry.
func (reg Registry) decline(name string) {
	reg.Gated[name] = true
	delete(reg.Instances, name)
}

// InvokeFunc calls an exported function and returns its results.
//
// Values cross as []Val — the harness's own type — for the reason ValKind is not
// binary.ValType: a signature naming the engine's value type would make this package
// depend on the engine's type system, and the whole point of the injection is that it does
// not. The caller converts, being the one place that legitimately knows both.
type InvokeFunc func(in Instance, name string, args []Val) ([]Val, error)

// Engine is the set of entry points the run loop calls, and the capabilities the caller
// declares on the engine's behalf.
//
// **A struct rather than parameters, and the reason is a hazard rather than a taste.**
// RunWith took `(DecodeFunc, ReadTextFunc, GatedFunc, ...Capability)`, and the first two are
// both `func([]byte) error`: transposing them compiles, runs, and scores every module
// command against the wrong language — a silent board rather than an error. Two more entry
// points arrived with the interpreter (#7), making four positional funcs of which three
// share a shape, so the argument list stopped being able to say what it meant. Named fields
// cannot be transposed.
//
// Zero values are meaningful and are the honest defaults. A nil entry point is a component
// the caller does not have, and Has empty means no capability beyond the decoder — so a
// command needing one is scored `unimplemented` rather than silently attempted. The failure
// mode this avoids is a new run entry point forgetting to declare and thereby converting the
// fourth verdict into a fail.
//
// A declared capability whose component is nil panics rather than scoring, because a
// declaration with nothing behind it is the registry running ahead of the engine.
type Engine struct {
	Decode   DecodeFunc
	ReadText ReadTextFunc
	IsGated  GatedFunc

	// Assemble is required by the assert_invalid (module quote ...) arm and by nothing else, so
	// it is nil-checked at that arm rather than here — IsTrap's discipline, one boundary over.
	Assemble AssembleFunc

	// IsTrap is required by the assert_trap action arm and by nothing else, so it is nil
	// for every caller that scores only module and malformed forms. A nil IsTrap makes the
	// arm score no pass at all rather than falling back to a text test: a harness that
	// cannot tell a trap from an error has not been given what it needs to judge a trap,
	// and quietly judging anyway is the accept-direction defect the field exists to close.
	IsTrap TrapFunc

	// IsException is IsTrap's own reasoning for exception handling's own outcome — required
	// by the assert_exception action arm alone, nil for every caller that does not score it
	// (#201 rung 2a).
	IsException ExceptionFunc

	// Validate and IsDeclined are the validator's halves, required together by CapValidator
	// and by the assert_invalid arm alone.
	//
	// **A nil IsDeclined alongside a non-nil Validate is the dangerous pairing, and it is the
	// one the loop panics on** — not because a missing predicate scores nothing, but because it
	// scores the wrong thing in the accept direction: every one of the 1059 declines would fall
	// through to the substring match, and the ones whose decline text quotes the vector's
	// expected phrase would land as passes. IsTrap's default (never award) is not available
	// here, because a decline is not the outcome being awarded — it is a third verdict that has
	// to be *removed* from the population before matching begins.
	Validate   ValidateFunc
	IsDeclined DeclinedFunc

	// Instantiate and Invoke are the interpreter's two halves, and they are separate
	// fields because they are separate obligations: an engine can decode a module without
	// being able to run it, which is exactly the state this repo was in until #7. Both are
	// required by CapInterpreter and the run loop checks both.
	Instantiate InstantiateFunc
	Invoke      InvokeFunc

	// InstantiateLinked is Instantiate with the registry, required by the `register` and
	// `assert_unlinkable` arms and by every module command in a script that has a registry.
	//
	// **A nil one is not a degradation to Instantiate, it is the linker being absent**, and the
	// run loop says so per arm rather than silently falling back: an `assert_unlinkable` scored
	// through the unlinked path would be asking whether a module instantiates *without* its
	// imports, which every one of the 200 does not, so the arm would report 200 passes it never
	// earned. That is the accept-direction defect this field exists to make impossible — the
	// same argument Engine.IsTrap makes about a missing trap predicate, and it lands on the
	// opposite default for the same reason: never award, and never quietly decline to notice.
	//
	// Module commands *do* fall back, and the difference is which way the error points: a
	// module instantiated without its registry reports the §3 sentinel and its dependents fail
	// in a named bucket, which is exactly today's board. So a caller with no linker sees the
	// board it had, and a caller with one drains it.
	InstantiateLinked LinkedInstantiateFunc

	// Has is what the engine declares. Board runners pass EngineCapabilities(); tests
	// pass a narrower set to exercise the gap.
	Has []Capability
}

// runOpts is Engine with Has resolved to a set.
type runOpts struct {
	Engine
	has map[Capability]bool
}

// Run executes a script's assertions against a decoder, scoring every gate as
// though it were on — no error is treated as a gate decline.
//
// It declares no capabilities and supplies no other component, so a script containing a
// quote form panics: the engine has CapWatReader, and a caller that neither declares
// it nor hands over a reader is asking the loop to score a vector against nothing. That
// is deliberate rather than a limitation — this is the minimal form for unit tests over
// synthetic byte-string scripts, and silently scoring such a vector `unimplemented`
// would resurrect a drained column from a forgotten argument.
func (s *Script) Run(decode DecodeFunc) *Result {
	return s.RunWith(Engine{Decode: decode})
}

// RunGated executes a script against the engine's *declared* capabilities, separating
// gate declines from verdicts. Engine.IsGated reports whether an error means the engine
// refused to answer because a feature gate is off; those vectors land in Result.Gated
// instead of Pass or Fail.
//
// Capabilities come from engineCapabilities and **any Has the caller set is overwritten**,
// not merged: this is the board's runner, and the board must score against what the engine
// declares rather than against what a call site remembered to pass. Adding the wat reader to
// that declaration moved the board without touching this function, which was the claim this
// comment made before it happened — and the interpreter's did it a second time.
//
// The components themselves are the caller's to supply, and a declared capability with a nil
// component panics in the run loop rather than scoring. So the derivation covers the half
// that gets forgotten (which capabilities count) and the loop covers the half a struct
// literal can omit silently (which components exist).
func (s *Script) RunGated(e Engine) *Result {
	e.Has = EngineCapabilities()
	return s.RunWith(e)
}

// RunWith executes a script against an explicitly described engine. A command whose Needs
// is not in e.Has is scored Unimplemented (decision 0010).
//
// This was the seam the wat reader arrived through, and the interpreter after it: each
// joined engineCapabilities and its population moved out of the fourth column. The default
// stays empty rather than "everything the harness knows about" — the latter would score
// vectors against components that do not exist, and the next capability will be in exactly
// the position wat-reader was.
//
// The explicit form stays for tests that need to declare a capability the engine cannot
// honour — a capability with no registered entry, or one whose component was left nil —
// which must panic rather than score.
func (s *Script) RunWith(e Engine) *Result {
	set := make(map[Capability]bool, len(e.Has))
	for _, c := range e.Has {
		set[c] = true
	}
	return s.run(runOpts{Engine: e, has: set})
}

func (s *Script) run(opts runOpts) *Result {
	r := &Result{
		Path: s.Path, Buckets: map[string][]Failure{},
		UnsupportedByHead:         map[string]int{},
		UnimplementedByCapability: map[Capability]int{},
	}
	isGated := func(err error) bool {
		return err != nil && opts.IsGated != nil && opts.IsGated(err)
	}
	// A nil IsTrap answers "no", so an assert_trap action scores a *fail* rather than a
	// pass — see Engine.IsTrap. Same defaulting as isGated above and the opposite
	// consequence, which is the honest direction in both cases: an absent predicate makes
	// the harness decline to award, never decline to notice.
	isTrap := func(err error) bool {
		return err != nil && opts.IsTrap != nil && opts.IsTrap(err)
	}
	// isTrap's own defaulting, one Kind over: an absent ExceptionFunc declines to award
	// rather than declines to notice.
	isException := func(err error) bool {
		return err != nil && opts.IsException != nil && opts.IsException(err)
	}
	// isDeclined's defaulting is isGated's, not isTrap's, and the difference matters here.
	// A nil predicate answering "no" makes a decline fall through to the substring match — the
	// accept-direction hole Engine.IsDeclined describes — so this is *not* the safe default and
	// the arm panics on the pairing rather than relying on it. The func exists for the nil-Validate
	// callers, where it is never reached.
	isDeclined := func(err error) bool {
		return err != nil && opts.IsDeclined != nil && opts.IsDeclined(err)
	}
	// scoreValidation is the `assert_invalid` verdict — the four outcomes in their fixed order —
	// shared by all three module forms.
	//
	// **Factored rather than copied, because the three forms differ only in their precondition.**
	// Each arm establishes that the module came into being (a byte image decodes, wat source
	// assembles, an inline body assembles) and then asks the identical question: did validation
	// refuse it, and with the right text. A third and fourth copy of this switch is how the two
	// new forms would drift from the 2697-vector one that already works — and the drift would be
	// invisible, since each copy passes its own vectors.
	//
	// **The bucket keys are prefixed by the Kind's own name, and derived from it rather than
	// passed in.** The three populations stay separate on the board — `KindModuleQuote`'s "two
	// keys, not one" ruling applied at birth instead of after, since merging the 8 new admissions
	// into message-keyed buckets that already carry text-form numbers would make a new red
	// indistinguishable from a regression in an old one.
	//
	// Derived from `c.Kind.String()` because the alternative is two places knowing one string: a
	// reader of these keys (the destination ledger) has to recover the form to classify the
	// marker after it, and a literal here would let the two drift into a `default` arm that
	// reports "the arm grew an outcome" when the arm merely renamed one.
	//
	// **That derivation was tested for real by #364**, which renamed `KindAssertInvalid` from
	// `assert_invalid` to `assert_invalid (module)`: every key on this side and every prefix strip
	// on the ledger's side moved together, because both come from the same call. What did *not*
	// move with them was the one literal that had been written out by hand — `admittedKeyPrefix` in
	// `spec_test.go`, now derived here's way. A rename is how you find out which readers were
	// deriving and which were copying; this comment used to end by asserting the keys were
	// byte-identical to their pre-split form, which stopped being true the moment the string did.
	//
	// The **stratum** figures are unaffected by the split because they come from
	// `Failure.Declined`/`Accepted`, the flags, not from these strings — so the partition is a
	// reporting choice and the accounting is not hostage to it.
	scoreValidation := func(c Command, st Stratum, err error) {
		form := c.Kind.String()
		got := ""
		if err != nil {
			got = err.Error()
		}
		switch {
		case isDeclined(err):
			r.Fail++
			// Keyed on the engine's own sentence, verbatim, matching the `register` arm's
			// `"register: " + got`: the declined opcode is *in* that sentence, so the buckets
			// partition by the rule the next slice has to write. A key this arm paraphrased
			// would be the harness inventing the taxonomy it is reporting on.
			key := form + " declined: " + got
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind, Stratum: st,
				Declined: true,
			})
		case err != nil && strings.Contains(got, c.Expect):
			r.Pass++
		case err != nil:
			r.Fail++
			key := form + " expected: " + c.Expect
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind, Stratum: st,
			})
		default:
			// Accepted. There is no error to key on and no Stratum for the caller to have
			// stated, so both are supplied here: `StratumValidate`, because the type-checker
			// is the layer that decided — it ran, it finished, and it said yes.
			r.Fail++
			key := form + " accepted, expected: " + c.Expect
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: c.Expect,
				Got:  "the module validated successfully",
				Kind: c.Kind, Stratum: StratumValidate,
				Accepted: true,
			})
		}
	}
	// requireValidator is the two-component tripwire the three assert_invalid arms share, in the
	// words the original single arm used. `Validate` because a declared CapValidator with nothing
	// behind it is the registry ahead of the engine; `IsDeclined` because its absence is not a
	// refusal to award but a refusal to *notice* — every slice decline would enter the substring
	// match, and any whose text quoted the expected phrase would score a pass the validator never
	// earned. That is the one asymmetry with IsTrap, and it is why this pairing is checked rather
	// than defaulted.
	//
	// **A fourth caller since #341, and the messages name the command rather than a form.** The
	// module-definition arm requires the validator too, on Scott's semantics ruling, and its
	// `Needs` stays `CapWatReader` — an arm requiring more than its own capability names is the
	// established shape here (`assert_invalid (module binary …)` needs a decoder and panics for it),
	// because `Needs` is what `classify` can state syntactically and the arm is where the rest is
	// checked. What the two callers would lose differs, so the second sentence of each message is
	// the one that stayed general: an unsupplied validator is a claim nobody asked, and an
	// unsupplied `IsDeclined` turns #9's frontier into unearned passes on the assert_invalid side
	// and into *over-rejections* on the module side, which is the very population the arm reports.
	requireValidator := func(c Command) {
		if opts.Validate == nil {
			panic(fmt.Sprintf("%s:%d: CapValidator declared but no ValidateFunc was supplied; "+
				"%v cannot be judged without one", s.Path, c.Line, c.Kind))
		}
		if opts.IsDeclined == nil {
			panic(fmt.Sprintf("%s:%d: CapValidator declared but no DeclinedFunc was supplied; "+
				"every slice decline would be scored as a verdict the validator never reached: an "+
				"assert_invalid whose expected text the decline happened to quote would pass, and a "+
				"module definition would be reported as over-rejected", s.Path, c.Line))
		}
	}
	// scoreModuleValidation is **fact 2 of a module definition**, shared by all three forms: the
	// module the corpus just defined must *validate*, not merely come into being.
	//
	// It is a closure and not a package function for `requireValidator`'s reason — both need
	// `opts` and `s.Path` — and it is extracted rather than duplicated because the thing it
	// encodes is what a definition *asserts*, which cannot be allowed to differ between the byte
	// image and the assembler. #341 landed it inline on the text arm; #353 added the binary arm as
	// its second caller, and two copies of a four-outcome switch is two chances for one form to
	// quietly stop asking. The forms still differ, and they differ in fact 1 — which is exactly
	// the split `classify`'s three Kinds already record.
	//
	// It returns the **stratum** as well as `scored`, because the caller needs it for the
	// two-decode-paths check that follows instantiation and re-deriving it would ask the validator
	// twice. It does **not** return the error, and the first draft did: `errcheck` flagged both
	// call sites discarding it, which was the right reading of a signature offering a caller
	// something to re-decide after this switch has already decided it. Every outcome the error
	// carries is bucketed here; a caller that consulted it would be a second opinion on a verdict
	// that is already recorded.
	//
	// **Gate declines keep their pass and are the one outcome this does not score.** #124's
	// ruling on a different axis: the front end stays gate-blind, so a decline arriving from the
	// validator's own decoder is not a verdict on the module, and scoring it here would move
	// passes the reader earned into the third verdict. The decline travels to the vector whose
	// question it actually blocks.
	scoreModuleValidation := func(c Command) (Stratum, bool) {
		requireValidator(c)
		vst, verr := opts.Validate(c)
		switch {
		case isGated(verr):
			// Unchanged by design — see above.
		case isDeclined(verr):
			// The sibling assert_invalid arms' treatment, verbatim including the key's shape
			// (see scoreValidation): a decline is a fail with the engine's own sentence as the
			// key, so the buckets partition by the rule #9's next slice has to write, and the
			// column drains as slices land rather than by anyone editing a number.
			r.Fail++
			key := c.Kind.String() + " declined: " + verr.Error()
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: key, Got: verr.Error(), Kind: c.Kind,
				Stratum: vst, Declined: true,
			})
			return vst, true
		case verr != nil && vst != StratumValidate:
			// **The module never reached the type checker**, so this is not evidence about the
			// validator: `Validate` assembles and decodes first, and both of those steps are the
			// harness reading its own output (see validateModule for why they are charged to
			// StratumEncode). Scored rather than skipped because an unrun vector is invisible,
			// and keyed apart from the validator's own answers so the encoder frontier drains in
			// the column that owns it (#8) instead of inflating #9's.
			r.Fail++
			key := c.Kind.String() + " must reach the validator"
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: key, Got: verr.Error(), Kind: c.Kind,
				Stratum: vst,
			})
			return vst, true
		case verr != nil:
			// **The finding this fact exists for**: the type checker ran, finished, and refused a
			// module the corpus asserts is valid. `OverRejected` rather than the stratum's
			// wrong-message arm, for the reason that field records.
			r.Fail++
			key := c.Kind.String() + " must validate"
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: key, Got: verr.Error(), Kind: c.Kind,
				Stratum: StratumValidate, OverRejected: true,
			})
			return vst, true
		}
		return vst, false
	}
	// cur is the instance the *most recent* module command produced, which is what an
	// unnamed `assert_return` runs against.
	//
	// **One slot is no longer the whole state, and the sentence that used to say so has been
	// redeemed rather than deleted.** It read:
	//
	//	There are **0** such actions in the answerable population … So one slot is the whole
	//	state, and the day a named action becomes askable it arrives as its own Kind, where
	//	the classification decision is visible.
	//
	// That day is this one. The 0 was true and was a *consequence* of the missing state rather
	// than evidence against needing it — with no registry, nothing after such a vector could
	// run either — and the instruction it left is the one that was followed: the named action
	// arrived as its own Kind (Scott's ruling), so `cur` keeps its meaning exactly and the two
	// maps below carry what it never could.
	//
	// curErr carries *why* there is no instance, so a run of assert_returns after a module
	// that failed to instantiate reports the cause once per vector rather than an anonymous
	// nil — and curStratum carries *whose* fault it was, which is what keeps 13775 encoder
	// frontiers out of the interpreter's ceiling. See Stratum.
	//
	// curGated is the third state a module command can leave behind, and it is *not*
	// derivable from the other two. A gate decline is not a defect, so an assert_return
	// after one is a question the engine was never asked — but the module command already
	// consumed its own decline into Result.Gated, and `cur == nil` cannot distinguish "the
	// module was declined" from "the module was broken". Without this flag the 17 memory64
	// vectors measured here would be *fails*, which is the third verdict leaking into the
	// fail column one command downstream: correct behaviour marked red, and marked red in
	// the interpreter's brand-new ceiling.
	var cur Instance
	var curErr error
	curStratum := StratumUnset
	curGated := false
	// The two maps of decision 0017, and they are two because they are keyed by two different
	// namespaces: `registry` by the *module name* an import asks for, written by `register`,
	// and `named` by the script `$name`, written by every module command that carries one. One
	// module may be in both, either, or neither. Merging them would make `(register "a" $M)`
	// imply that `(invoke $M …)` and `(invoke "a" …)` name the same thing, and the second form
	// does not exist in the grammar at all.
	//
	// `spectest` is in `registry` before the loop starts and is not special-cased anywhere
	// else — part 3 of the decision, and the reason the resolver has no builtin arm.
	//
	// It carries a second field as of decision 0037 — the names a gate decline left unbound — for
	// the reason the other two slots carry `curGated` and `namedGated`. See Registry.
	registry := opts.spectestRegistry(s.Path)
	named := map[string]Instance{}
	// A named action's failures carry *why* its module has no instance, for the reason `curErr`
	// exists: "no instance for $M" names nothing, and this run loop knows whether the module
	// failed, was declined, or never appeared.
	namedErr := map[string]error{}
	namedStratum := map[string]Stratum{}
	namedGated := map[string]bool{}
	// remember stamps a module command's outcome into whichever slots it owns. Every module
	// arm calls it exactly once, which is what keeps `cur` and `named` from disagreeing about
	// the same command — four arms each assigning five variables is the shape that drifts.
	remember := func(c Command, in Instance, st Stratum, err error, gated bool) {
		cur, curStratum, curErr, curGated = in, st, err, gated
		if c.Name == "" {
			return
		}
		named[c.Name], namedStratum[c.Name] = in, st
		namedErr[c.Name], namedGated[c.Name] = err, gated
	}
	for _, c := range s.Commands {
		// The capability gap, computed before the verdict switch and ahead of every
		// Kind: a command needing a component the engine lacks gets no verdict at all,
		// so asking the decoder about it would be asking the wrong question. Derived
		// from c.Needs rather than from the Kind, so a future Kind that needs the same
		// component inherits this for free (decision 0010, guard 1).
		if c.Needs != CapNone && !opts.has[c.Needs] {
			if e, ok := capabilityIssues[c.Needs]; !ok || e.Retires == "" {
				if ok {
					// Registered, but with no retirement condition — an entry with no way to
					// die is a squatter, and the column it feeds would be permanent by
					// omission rather than by decision.
					panic(fmt.Sprintf("%s:%d needs capability %q, whose registry entry states "+
						"no retirement condition; an entry that cannot die outlives its "+
						"component", s.Path, c.Line, c.Needs))
				}
				// Not registered — and there are now *two* ways to be unregistered, which the
				// first retirement is what discovered. Guard 2's message was written when the
				// only way was a classifier inventing a capability, and it told the reader to
				// register it; said to a caller under-declaring a capability the engine
				// **has**, that advice would reinstate a debt that was just paid. *A ruling
				// retroactively falsifies prose written before it* — here the ruling is a
				// retirement, and the orphaned sentence was three lines below it.
				if engineCapabilities[c.Needs] {
					panic(fmt.Sprintf("%s:%d needs capability %q, which the engine declares but "+
						"this caller did not: it was retired from the registry, so there is no "+
						"fourth-verdict entry left to score it against. Use RunGated, which "+
						"derives from the declaration", s.Path, c.Line, c.Needs))
				}
				// A capability the classifier invented. Counting it would let the fourth
				// verdict grow by fiat, which is the abuse guard 2 exists to make
				// impossible — so it is a hard stop, not a larger column.
				panic(fmt.Sprintf("%s:%d needs unregistered capability %q; "+
					"register it in capabilityIssues with a tracking issue or the fourth "+
					"verdict grows without an owner", s.Path, c.Line, c.Needs))
			}
			r.Unimplemented++
			r.UnimplementedByCapability[c.Needs]++
			continue
		}
		switch c.Kind {
		case KindAssertMalformed:
			err := opts.Decode(c.Module)
			got := ""
			if err != nil {
				got = err.Error()
			}
			// A gate decline is checked before the substring match, deliberately.
			// A gate error that happened to contain the expected text would
			// otherwise score a pass the engine did not earn — and since gate
			// errors are feature-named and spec strings are not, that collision is
			// unlikely rather than impossible. Order makes it impossible.
			if isGated(err) {
				r.gate(c)
				continue
			}
			// Substring matching, per decision 0003.
			if err != nil && strings.Contains(got, c.Expect) {
				r.Pass++
				continue
			}
			r.Fail++
			r.Buckets[c.Expect] = append(r.Buckets[c.Expect], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind,
				Stratum: StratumBinary,
			})

		case KindModuleBinary:
			// **Fact 1: the image decodes.** A bare `(module binary …)` at top level asserts
			// the module is *valid*, and this is the half of that claim the decoder can
			// answer. The sentence that used to finish this comment is gone, and its removal
			// is the point of #353:
			//
			//	Phase 1 only decodes, so this is a decode-must-succeed check — a weaker
			//	claim than the suite makes, and the honest thing is to score it rather
			//	than skip it.
			//
			// Accurate for as long as it stood, and it was a standing confession rather than
			// a deferral: the arm scored `Pass` on decode alone, so the 80 passes this form
			// contributes to the default board were decode-success wearing a validity claim.
			// #341 closed the same hole for the text and quote forms; #345 flagged this one
			// rather than widening it, because moving a second population's numbers inside
			// the PR that creates them has no pre-registration for that population.
			err := opts.Decode(c.Module)
			if isGated(err) {
				// The instance is cleared on a gate decline too. An `assert_return`
				// following a declined module must not run against the *previous*
				// module's instance — it would score a real verdict from the wrong
				// program, which is worse than reporting no instance.
				remember(c, nil, StratumBinary, errNoInstance(c, "gate declined the module"), true)
				r.gate(c)
				continue
			}
			if err != nil {
				remember(c, nil, StratumBinary, err, false)
				r.Fail++
				const key = "(module binary ...) must decode"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: err.Error(), Kind: c.Kind,
					Stratum: StratumBinary,
				})
				continue
			}
			// **Fact 2: the module validates**, in the words and the shape the text arm below
			// uses — see `scoreModuleValidation`, which both arms now call so that the two
			// module-definition forms cannot drift in what a definition *asserts*. The
			// per-form differences are entirely in fact 1 above (a byte image here, the
			// assembler there), which is the split `classify`'s three Kinds already record.
			//
			// **Fact 1 must run first, and not only for tidiness.** `ValidateFunc`
			// implementations assemble before they decode and charge a decode refusal to
			// `StratumEncode` — a claim that the image came out of the encoder, which is true
			// for the text and quote forms and false here, there being no encoder on this
			// path. The mis-charge is unreachable because the branch above has already
			// `continue`d on any decode failure, so this is statement order holding off a
			// wrong stratum rather than the stratum being right. Named because an ordering
			// constraint that is invisible in the code it constrains is one edit from a
			// silently mis-charged column (#353).
			//
			// **The pre-registered forecast for this arm is no board movement at all**: 88
			// commands, all 88 validating clean in both lanes, measured before the arm changed
			// (#353). So the assertion is prospective — an over-rejection produces no error for
			// any reject-direction bucket to catch, which is why a population that is clean
			// today does not make asking it idle — and the arm's green is worth nothing on its
			// own. `TestModuleDefinitionsAskTheValidator` carries the witness that it fires.
			vst, scored := scoreModuleValidation(c)
			in, st, ierr := opts.instantiate(c, registry)
			remember(c, in, st, ierr, isGated(ierr))
			if scored {
				continue
			}
			// **The two paths that decode this image must agree**, and here that is a strictly
			// narrower claim than the text arm's: both decode the *same bytes* under the same
			// features, with no encoder in between, so a disagreement asserts only that the
			// decoder is deterministic. Kept anyway, at one condition's cost, because the
			// weaker claim is still a real one and dropping it would make the two arms differ
			// for no reason a reader could recover.
			if ierr != nil && vst == StratumValidate &&
				(st == StratumEncode || st == StratumBinary) {
				r.Fail++
				key := c.Kind.String() + " two decode paths disagree"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: ierr.Error(), Kind: c.Kind,
					Stratum: st,
				})
				continue
			}
			r.Pass++

		case KindModuleQuote, KindModuleText, KindAssertMalformedText:
			// Reachable only when a caller declares CapWatReader, since the capability gap
			// above catches every other path.
			//
			// The tripwire that used to live here — panic, because no reader existed — has
			// not been deleted, it has been *narrowed to the case that is still wrong*: a
			// caller declaring the capability without supplying the component. The risk the
			// original named (the registry running ahead of the engine) survives its
			// original subject, so the control is re-pointed rather than retired.
			if opts.ReadText == nil {
				panic(fmt.Sprintf("%s:%d: CapWatReader declared but no ReadTextFunc was "+
					"supplied; the capability registry is ahead of the engine", s.Path, c.Line))
			}
			err := opts.ReadText(c.Source)
			if isGated(err) {
				// StratumText, not StratumBinary: the decline came out of ReadText. The
				// binary arm above stamps StratumBinary for the same reason, and the two
				// lines are otherwise identical, which is exactly the transposition hazard
				// that named the layer at the site instead of deriving it.
				remember(c, nil, StratumText, errNoInstance(c, "gate declined the module"), true)
				r.gate(c)
				continue
			}
			if c.Kind == KindModuleQuote || c.Kind == KindModuleText {
				// Both forms assert the source is *valid* wat, so both are must-read: an
				// error is a fail, no error is a pass. Scored rather than skipped for the
				// same reason KindModuleBinary is — an unrun vector is invisible.
				//
				// The sentence that used to qualify this as "the reader only lexes, so
				// this is a must-lex-clean check" is gone, and its removal is a fact
				// rather than an edit: the parser landed across #62/#63/#64, so a bare
				// module form that reads clean now has been through the whole module
				// grammar. The seven quote modules that used to be named *unearned* on
				// that ground (PR #61) are earned now for the same reason.
				//
				// **Two keys, not one**, and the split is the point. These are separate
				// populations with separate histories — 7 quote forms, 1119 bare bodies —
				// and merging them would put #69's admission into a bucket that already
				// had a number, making the new reds indistinguishable from a regression in
				// the old ones. *Bucketed failures are the work plan*, and a plan needs to
				// name which population it is about.
				if err != nil {
					remember(c, nil, StratumText, err, false)
					r.Fail++
					key := "(module quote ...) must read"
					if c.Kind == KindModuleText {
						key = "(module <wat body>) must read"
					}
					r.Buckets[key] = append(r.Buckets[key], Failure{
						Line: c.Line, Expect: key, Got: err.Error(), Kind: c.Kind,
						Stratum: StratumText,
					})
					continue
				}
				// **Fact 2: the module validates.** A module definition in a script asserts the
				// module *is valid*, so a harness scoring it on parsing alone is under-asserting
				// — and that under-assertion is precisely where an over-rejecting rule hides,
				// because an over-rejection produces no error anyone buckets. Every reward figure
				// quoted for a validator slice before this arm existed was resting on it:
				// `internal/validate/global_test.go`'s M11 row measured a `modulePre` refusing
				// *every* module and left all 2143 of these commands green. (Scott's semantics
				// ruling, #341: `KindModuleText` means the module decodes **and** validates.)
				//
				// **Gate declines are exempt and keep their pass, which is the one thing this
				// fact does not touch.** #124's ruling is a different axis: the front end stays
				// gate-blind, so a decline arriving from the validator's own decoder is not a
				// verdict on the module, and scoring it here would move 421 passes the reader
				// earned into the third verdict. The decline travels to the vector whose question
				// it actually blocks, exactly as an instantiation decline does.
				//
				// The four-outcome switch that used to stand inline here is
				// `scoreModuleValidation`, unchanged in behaviour and moved because #353 gave it a
				// second caller: the same fact 2, asked of `(module binary …)`. Its comments went
				// with it, including the two that were written as *this* arm's — "the reason
				// KindModuleBinary's arm is scored" now reads as the general statement it always was.
				vst, scored := scoreModuleValidation(c)
				// **Instantiation happens either way, and a validation refusal does not withhold
				// the instance.** Withholding is the tidier-looking choice and it is wrong here:
				// one missing subtyping rule would go red across every dependent vector of the
				// module, which is a hundred reds for one defect and the end of the bucket
				// resolution that *is* the work plan. Measurably the refusals are the validator's
				// error rather than the corpus's — 13 of 13 at the pre-registered revision — so
				// withholding would be punishing correct modules for an unwritten rule. 0025's
				// carve-out is about the other direction, where the corpus says invalid.
				//
				// **The module command keeps its pass on an instantiation decline and the decline
				// is carried forward**, which is #124's ruling stated where it applies: ReadModule
				// accepted the source, and scoring the downstream assert_return as `fail` would
				// mark correct behaviour red.
				in, st, ierr := opts.instantiate(c, registry)
				remember(c, in, st, ierr, isGated(ierr))
				if scored {
					continue
				}
				// **The two paths that decode this module must agree**, and this arm is where the
				// second one was introduced: `Validate` assembles and decodes, then `instantiate`
				// assembles and decodes again, which is #296's boundary-signature hazard created
				// by fact 2 above. Validation having reached the type checker means encode and
				// decode both succeeded, so a refusal from instantiation in a *pre-interpreter*
				// stratum means the two paths differ — a harness defect, not a verdict, and the
				// same assertion the `assert_invalid (module binary …)` arm makes for the same
				// reason. A gate decline is deliberately **not** excluded: one path declining on a
				// gate the other honours is exactly the lane-override defect `allOnLane` records
				// having twice nearly forgotten.
				if ierr != nil && vst == StratumValidate &&
					(st == StratumEncode || st == StratumBinary) {
					r.Fail++
					key := c.Kind.String() + " two decode paths disagree"
					r.Buckets[key] = append(r.Buckets[key], Failure{
						Line: c.Line, Expect: key, Got: ierr.Error(), Kind: c.Kind,
						Stratum: st,
					})
					continue
				}
				r.Pass++
				continue
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			// Substring matching, per decision 0003 — the same rule the binary side uses,
			// and here it is load-bearing in a way it is not there: eleven vectors read
			// the *lexeme* back out of the message (`unknown operator i32.wrap/i64`), so
			// for those the rendering is oracle-covered rather than ours alone (#38).
			if err != nil && strings.Contains(got, c.Expect) {
				r.Pass++
				continue
			}
			r.Fail++
			r.Buckets[c.Expect] = append(r.Buckets[c.Expect], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind,
				Stratum: StratumText,
			})

		case KindAssertTrapModule:
			// `(assert_trap (module …) "text")`: the module must come to life and die doing
			// it, with the spec's own trap string. 0015's Kind, and the reason instantiation
			// returns `*Trap` at all.
			//
			// **It does not touch `cur`, and that is deliberate.** A trapping module produces
			// no instance, and neither does one that fails to trap — the vector says nothing
			// about what the *next* command should run against. Assigning `cur = nil` here
			// would silently invalidate the surrounding script's state; leaving it alone means
			// a file that interleaves these with real modules keeps whichever instance it had.
			// Measured: all 54 of these forms stand alone, so the choice is invisible today
			// and would be a real defect the first time it was not.
			// **Either instantiation func satisfies this**, for the reason the action arm's
			// twin check states: they are two spellings of one capability, and asking only
			// about the plain one would panic on a caller that supplied the linked one — a
			// harness crash for a configuration that is strictly more capable.
			if opts.Instantiate == nil && opts.InstantiateLinked == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no InstantiateFunc was "+
					"supplied; the capability registry is ahead of the engine", s.Path, c.Line))
			}
			_, _, err := opts.instantiate(c, registry)
			if isGated(err) {
				r.gate(c)
				continue
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			// Substring matching, per decision 0003 — the same rule both malformed arms use.
			// The expected string here is the spec's trap text (`out of bounds memory
			// access`), which `Trap.Error` renders verbatim for exactly this reason: a second
			// spelling would be the engine's testimony disagreeing with itself.
			if err != nil && strings.Contains(got, c.Expect) {
				r.Pass++
				continue
			}
			r.Fail++
			// **Keyed by the expected text**, so the bucket names what the suite wanted rather
			// than what the engine said. The 40 of these outside `data1.wast` need element
			// segments, linking or a start function, and they will land in *this* key with the
			// front end's error as their Got — visible, and distinguishable from a real
			// disagreement about the trap by reading the Got.
			key := "assert_trap (module) expected: " + c.Expect
			if got == "" {
				got = "the module instantiated without trapping"
			}
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind,
				Stratum: StratumExec,
			})

		case KindRegister:
			// `(register "n" [$m])` binds a module into the registry, and it is **the one arm
			// whose success is a state change rather than a verdict**.
			//
			// Scoring it pass or fail would be an invented verdict either way: it asserts
			// nothing, so a pass is a pass nobody claimed and a fail is a defect nobody alleged.
			// Counting it as `unsupported` — which is what happened until this Kind existed — is
			// the same fiction pointed the other way, since the harness *can* ask and there is
			// nothing to ask. So the 45 registers that bind touch **no scoring** counter and the
			// denominators do not move.
			//
			// **They are nonetheless counted, in `Bound`, and that correction came from a
			// control rather than from review.** This comment's first draft said the arm "touches
			// no counter at all" and meant it as a virtue; TestVerdictsPartitionCommands then
			// failed across 24 files, because *unscored* and *unaccounted* are different things
			// and the partition asserts the second. A sixth outcome is an added verdict as far as
			// the arithmetic is concerned, whether or not it scores. See Result.Bound.
			//
			// The **`assert_unlinkable` arm is where a missing linker is caught**, not here: a
			// register with no linker binds an instance nobody will resolve against, which is
			// harmless, where an unlinkable vector with no linker would award a pass.
			if in, ok := registerTarget(c, cur, named); ok {
				registry.bind(c.Register, in)
				r.Bound++
				continue
			}
			// The module the register names produced no instance. **Not scored, and not silent
			// either** — this is a *state* failure whose cost lands on whichever import resolves
			// against the missing name later, and that vector will report `unknown import` with
			// no idea why. So the name is bound to nothing (the map keeps no entry) and the
			// bucket names the register itself, charged to the stratum of the module that failed.
			//
			// It is a fail rather than a no-op because the alternative was measured and is worse:
			// a register whose module failed silently produces a *cluster* of downstream
			// `unknown import` failures naming the wrong component, which is the wrong-layer
			// error the harness exists to avoid attributing.
			got := "no module command produced an instance to register"
			st := StratumExec
			gated := false
			if c.Target != "" {
				if err := namedErr[c.Target]; err != nil {
					got, st, gated = err.Error(), namedStratum[c.Target], namedGated[c.Target]
				} else {
					got = "no module named " + c.Target + " precedes this register"
				}
			} else if curErr != nil {
				got, st, gated = curErr.Error(), curStratum, curGated
			}
			// **A register whose module was *declined* is gated, not failed**, and the distinction
			// is the same one the module arms already draw: a decline is the engine refusing to
			// answer, so charging it to Fail would report a feature this build does not have as a
			// defect in this build. Measured — all 7 of these are exactly that: 3 in
			// `linking{1,2,3}.wast` on the multi-memory memarg bit and 4 in
			// `memory64-imports.wast` on memory64, each register naming a module the decoder
			// declined one command earlier, which the module command itself already scored `gated`.
			//
			// Found by the stratum ledger rather than by the total: `binary 7` against a ceiling of
			// 0 is what said so, and it said so *because* the binary column's ceiling is not shared
			// with the others. A shared column would have absorbed seven declines into an
			// encoder-sized number and reported nothing.
			//
			// **The decline is now *recorded* as well as scored, which is decision 0037**, and
			// until it was, this arm did half its job invisibly: it gated itself correctly and
			// left the name unbound, so every later import against it reported a plain, ungated
			// `unknown import` — a gate consequence in the interpreter's fail column, 62 of the
			// default lane's 81 exec fails. The line below is what makes the *downstream* vector's
			// verdict right rather than merely explicable. See Registry.
			if gated {
				registry.decline(c.Register)
				r.gate(c)
				continue
			}
			r.Fail++
			if st == StratumUnset {
				st = StratumExec
			}
			key := "register: " + got
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: "an instance to bind as module " + c.Register,
				Got: got, Kind: c.Kind, Stratum: st,
			})

		case KindAssertInvalid:
			// `(assert_invalid (module …) "text")`: the module decodes and **validation must
			// refuse it** with the spec's text. 2697 vectors, #9's own subject and the largest
			// classifiable population the board had left.
			//
			// **Four outcomes in a fixed order, and the order is the whole arm.** A gate decline,
			// then a slice decline, then the substring match, then acceptance — each earlier test
			// removing a population the later ones would otherwise misread:
			//
			//   1. `isGated` — 463 vectors, the decoder refusing a disabled proposal before the
			//      validator sees the module. The third verdict, already the house rule on every
			//      expected-text arm, and here for the same reason: a gate error's text can quote
			//      the vector's phrase.
			//   2. `isDeclined` — 1059 vectors whose rule belongs to a later slice. **Not a pass
			//      and not a gate**: it is a fail with the declined opcode in its bucket key, so
			//      the column drains as slices land and the work plan reads off the buckets.
			//      Asked before the match because `validator: instruction not in this slice:
			//      <mnemonic>` is a *sentence about an opcode*, and the corpus's expected strings
			//      are phrases about types — a collision is not hypothetical, it is one
			//      unluckily-named future sentinel away, and the check costs one branch.
			//   3. the substring match, per decision 0003.
			//   4. acceptance — **the admission stratum**, 142 vectors this validator declared
			//      valid and the suite says are not. They are fails with a named cause rather than
			//      passes, which is the accept-direction defect no board can otherwise see
			//      (contract §9 G-3): a module wrongly accepted produces no error to bucket, so
			//      the bucket is keyed on the *expectation* instead.
			//
			// **Both components are required and a nil one panics** — see requireValidator, which
			// holds the argument and is shared with the two forms below.
			requireValidator(c)
			// **`cur` is untouched**, for KindAssertUnlinkable's reason: an invalid module produces
			// no instance and says nothing about what the next command runs against.
			st, err := opts.Validate(c)
			if isGated(err) {
				r.gate(c)
				continue
			}
			scoreValidation(c, st, err)

		case KindAssertInvalidBinary:
			// `(assert_invalid (module binary …) "text")`: **three ordered facts** — the image
			// decodes, validation refuses the module, and the refusal names the spec's text. 11
			// vectors.
			//
			// **Decode success is asserted separately, and that separation is the whole arm**
			// (Scott's ruling, PR #327). A decode refusal here is not a pass, and it does not join
			// the validation fail bucket: it gets `StratumBinary` and a key of its own. The two
			// facts have different owners — a decoder refusing a module the spec says must decode
			// and *then* fail validation is a defect in the **decoder**, and burying it under a
			// validator fail makes the wrong team's work invisible. Same discipline as the
			// message-match one layer up: rejection alone is never the verdict, because
			// rejected-for-the-wrong-reason reads identically to rejected-for-the-right-one.
			//
			// The board is what makes that structural rather than tidy. `StratumBinary`'s ceiling
			// is **0** and is not shared with the others, so a decode refusal appearing here is a
			// red in the decoder's own column on the first run that produces one. Forecast at the
			// slice: zero decode refusals, two gate declines, 9 reaching validation.
			if opts.Decode == nil {
				panic(fmt.Sprintf("%s:%d: an assert_invalid (module binary ...) cannot assert that "+
					"its image decodes without a DecodeFunc; without this the decode precondition "+
					"would be silently folded into the validator's verdict", s.Path, c.Line))
			}
			requireValidator(c)
			// Fact 1. The gate check precedes the refusal check for the reason every expected-text
			// arm asks it first: a gate error is not a verdict on the module, and its text can
			// quote the vector's own phrase.
			if derr := opts.Decode(c.Module); derr != nil {
				if isGated(derr) {
					r.gate(c)
					continue
				}
				r.Fail++
				const key = "assert_invalid (binary) must decode before validation judges it"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: derr.Error(), Kind: c.Kind,
					Stratum: StratumBinary,
				})
				continue
			}
			// Facts 2 and 3.
			st, err := opts.Validate(c)
			if isGated(err) {
				r.gate(c)
				continue
			}
			// **The two decoders are asked the same question and must agree.** `Validate` decodes
			// again internally — the image is recoverable but only by repeating the step, which is
			// #296's boundary-signature channel — and a repeated step is a second chance to fail
			// that the caller's return type cannot attribute. So the disagreement is asserted
			// rather than hoped: fact 1 said this image decodes, and a pre-validation stratum
			// coming back from fact 2 means the two decode paths differ (a lane that swapped one
			// and not the other, most likely), which is a harness defect and not a verdict.
			if st == StratumBinary || st == StratumEncode {
				r.Fail++
				const key = "assert_invalid (binary) two decode paths disagree"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: "the same image to decode for DecodeFunc and ValidateFunc",
					Got:  fmt.Sprintf("DecodeFunc accepted it and ValidateFunc returned %v: %v", st, err),
					Kind: c.Kind, Stratum: st,
				})
				continue
			}
			scoreValidation(c, st, err)

		case KindAssertInvalidQuote:
			// `(assert_invalid (module quote …) "text")`: the same three ordered facts with
			// assembly as the precondition — the source assembles, validation refuses the result,
			// the refusal names the spec's text. 6 vectors.
			//
			// **This is the arm AssembleFunc exists for.** A reader that returns only an error can
			// say the source is clean and not say what is clean, so fact 1 would have to be
			// established by the same call that establishes facts 2 and 3 — which is the layer
			// collapse the binary arm above exists to avoid, one component over. With the image in
			// hand the harness asserts assembly on its own, then hands the image forward.
			//
			// Handing it forward is not an optimization: `Validate` prefers `Module` when it is
			// set, so the assembler runs **once** and the module facts 2 and 3 judge are
			// *provably* the module fact 1 accepted. The binary arm cannot have that property
			// (its `Validate` re-decodes, hence the disagreement check) and this one gets it for
			// free, which is worth naming as the asymmetry it is rather than leaving a reader to
			// wonder why only one arm carries the check.
			if opts.Assemble == nil {
				panic(fmt.Sprintf("%s:%d: an assert_invalid (module quote ...) cannot assert that "+
					"its source assembles without an AssembleFunc; CapValidator names the type "+
					"checker but this form needs the assembler first, and ReadTextFunc is the "+
					"recognizer rather than the assembler", s.Path, c.Line))
			}
			requireValidator(c)
			// Fact 1.
			img, aerr := opts.Assemble(c.Source)
			if aerr != nil {
				if isGated(aerr) {
					r.gate(c)
					continue
				}
				r.Fail++
				const key = "assert_invalid (quote) must assemble before validation judges it"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: aerr.Error(), Kind: c.Kind,
					// StratumText, not StratumEncode: the refusal came out of the wat reader, and
					// the reader's column is the one whose work a red here is about. The two lines
					// are otherwise identical, which is the transposition hazard that made the
					// module-quote arm name its layer at the site rather than derive it.
					Stratum: StratumText,
				})
				continue
			}
			// Facts 2 and 3, over the image fact 1 accepted.
			c2 := c
			c2.Module = img
			st, err := opts.Validate(c2)
			if isGated(err) {
				r.gate(c)
				continue
			}
			scoreValidation(c, st, err)

		case KindAssertUnlinkable:
			// `(assert_unlinkable (module …) "text")`: the module reads and validates, and
			// **linking it must fail** with the spec's text. 200 vectors, 184 wanting
			// `incompatible import type` and 16 wanting `unknown import`.
			//
			// **The linker's absence is a panic here and a fallback everywhere else**, which is
			// the one asymmetry in this file worth defending at its site. A module command with
			// no linker degrades honestly: it reports the §3 sentinel and its dependents fail in
			// a named bucket. This arm cannot degrade, because instantiating an unlinkable module
			// *without* its imports fails for a different reason and would satisfy nothing —
			// worse, a substring match against the engine's §3 text could coincide with the
			// expected string and award a pass the linker never earned. So the component is
			// required, in the same words the other three tripwires use.
			if opts.InstantiateLinked == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no LinkedInstantiateFunc "+
					"was supplied; an assert_unlinkable cannot be judged without one, and judging "+
					"it through the unlinked path would score a §3 refusal as a link failure",
					s.Path, c.Line))
			}
			// **`cur` is untouched**, for `KindAssertTrapModule`'s reason: an unlinkable module
			// produces no instance and says nothing about what the next command runs against.
			_, _, err := opts.instantiate(c, registry)
			if isGated(err) {
				r.gate(c)
				continue
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			// Substring matching per decision 0003, the same rule every expected-text arm uses.
			//
			// **Deliberately not asking whether the error is a link failure specifically**, which
			// is the opposite of what the assert_trap arm does with `isTrap` — and the asymmetry
			// is the corpus rather than an oversight. A trap and a non-trap error are both
			// *runtime* outcomes that a substring match cannot tell apart, so that arm needs a
			// predicate. Here the two expected strings (`incompatible import type`, `unknown
			// import`) are phrases only the linker produces, so the text *is* the discriminator.
			// Stated because the next reader will notice the missing predicate: if a vector ever
			// expects a string the engine can also produce elsewhere, this arm needs one.
			if err != nil && strings.Contains(got, c.Expect) {
				r.Pass++
				continue
			}
			r.Fail++
			key := "assert_unlinkable expected: " + c.Expect
			if got == "" {
				got = "the module linked and instantiated successfully"
			}
			r.Buckets[key] = append(r.Buckets[key], Failure{
				Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind,
				Stratum: StratumExec,
			})

		case KindAssertReturn, KindInvoke, KindAssertTrapAction,
			KindNamedAssertReturn, KindNamedInvoke, KindNamedAssertTrap, KindAssertException:
			// **Seven kinds, one arm, and the sharing is the point.** All seven call an exported
			// function on an instance, so they need the same instance lookup, the same
			// no-instance accounting, the same gate handling, and the same panic on a
			// declared-but-absent component. A bare `(invoke …)` is an assert_return with no
			// expectation; an `assert_trap` action is one whose expectation is an error;
			// `assert_exception` is a sibling expectation with no text to match; the three named
			// forms differ only in *which* instance. A separate arm would be a second copy of all
			// the state handling, drifting from this one on the next change.
			//
			// **`KindAssertException` joined rather than forked for `wantsTrap`'s own reason,
			// one Kind short of a pair.** Unlike the three named Kinds (which came in a matched
			// trio), there is no `KindNamedAssertException` — zero corpus vectors name a module
			// under this directive (classify's own doc comment measures it) — so this arm gains
			// one Kind, not two, and `selectsModule()`'s switch needs no new case.
			//
			// **It was three, and the named Kinds joining it rather than forking it is the
			// answer to the obvious objection to Scott's ruling.** Six Kinds sounds like six
			// arms; the ruling's cost is paid in `classify`, where the two *readers* are
			// genuinely separate, and it is refunded here, where the difference between naming
			// a module and not naming one is two lines. The one-authority law cuts both ways:
			// one reader per grammar, one arm per behaviour.
			//
			// They part company at exactly two places — which instance the call runs against,
			// and whether a non-nil error from Invoke is the answer or the failure — and both
			// branches are ahead of the accounting they affect, for the same reason the gate
			// check precedes the substring match on the malformed arms: order is what makes the
			// readings impossible to confuse rather than merely unlikely to be.
			//
			// Reachable only when a caller declares CapInterpreter, and the same
			// re-pointed tripwire the text kinds carry: a declaration with no component
			// is the registry ahead of the engine, and it must stop rather than score.
			// Both halves are named, because an engine that can instantiate and not
			// invoke is a real intermediate state and a nil deref is not a diagnosis.
			//
			// **Either instantiation entry point satisfies the first half**, which is the one
			// place the two fields are interchangeable: this check asks whether the engine can
			// build a module at all, and a caller with only the linked form can. Writing
			// `opts.Instantiate == nil` alone here would panic on a caller that supplied the
			// better of the two — a tripwire firing on the state it exists to certify.
			if (opts.Instantiate == nil && opts.InstantiateLinked == nil) || opts.Invoke == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no %s was supplied; "+
					"the capability registry is ahead of the engine", s.Path, c.Line,
					map[bool]string{true: "InstantiateFunc", false: "InvokeFunc"}[opts.Invoke != nil]))
			}
			// The third component, required by this Kind alone. **A panic rather than the
			// nil-predicate default**, which reads as a contradiction of Engine.IsTrap's
			// comment and is the resolution of it: the default keeps a *missing* predicate
			// from awarding passes, and this keeps a missing predicate from silently failing
			// 4876 vectors the engine can answer. Both are the same rule — the harness never
			// degrades quietly — and a caller who declares the interpreter and hands over no
			// trap predicate is in the registry-ahead-of-the-engine state the other two
			// halves already name. *Silent degradation is a skip one step quieter.*
			if c.Kind.wantsTrap() && opts.IsTrap == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no TrapFunc was supplied; "+
					"an assert_trap action cannot be judged without one, and judging it anyway "+
					"would score a non-trap error as a trap", s.Path, c.Line))
			}
			// TrapFunc's own tripwire, one Kind over: judging an assert_exception with no
			// ExceptionFunc would score *any* error — a missing arm, the layering debt — as the
			// exception the vector wants, which is the identical accept-direction hazard
			// wantsTrap's check exists to close.
			if c.Kind.wantsException() && opts.IsException == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no ExceptionFunc was supplied; "+
					"an assert_exception action cannot be judged without one, and judging it anyway "+
					"would score any error as the exception", s.Path, c.Line))
			}
			// **Which instance the action selects, which is the one thing the named Kinds
			// changed here.** An unnamed action runs against the most recent module command's
			// instance; a named one looks up its `$M`. The three state facts travel together
			// because all three arms below read all three — an instance, why there isn't one,
			// and whose fault that is.
			target, targetErr, targetStratum, targetGated := cur, curErr, curStratum, curGated
			if c.Kind.selectsModule() {
				target, targetGated = named[c.Target], namedGated[c.Target]
				targetErr, targetStratum = namedErr[c.Target], namedStratum[c.Target]
				if target == nil && targetErr == nil {
					// **No module of that name in this script**, which is a different fact from
					// "the module failed" and gets its own words: the first is a harness or
					// corpus problem, the second is an engine one, and a shared message would
					// make a script the harness mis-read look like an engine that cannot build
					// modules. Measured at 0 over the corpus, which is what makes it worth
					// stating rather than worth omitting — a branch that never fires and says
					// nothing when it does is how a misclassification hides.
					targetErr = fmt.Errorf("no module named %s precedes this action", c.Target)
				}
			}
			// No instance: the module command that should have produced one failed, was
			// gate-declined, or never appeared. **Its own bucket, never the invoke's** —
			// the module's failure is already counted where it happened, and charging the
			// same defect to the interpreter's column would make this layer's work plan
			// name a component that is not broken. *An error from the wrong layer is
			// evidence about where structure was lost*, and here the harness knows the
			// layer exactly, so it says so.
			if target == nil && targetGated {
				// The module this vector needs was declined for a feature, so the
				// question was never asked — the same reason the module arms above
				// count a decline rather than a failure, one command downstream.
				// Checked *before* the fail branch for the reason a gate decline is
				// checked before the substring match on the malformed arms: order is
				// what makes the collision impossible rather than merely unlikely.
				r.gate(c)
				continue
			}
			if target == nil {
				r.Fail++
				got := "no preceding module command produced an instance"
				if targetErr != nil {
					got = targetErr.Error()
				}
				// **Keyed by the failing module's error, and charged to its stratum**, not
				// to a single "no instance" key charged to exec. That single key was
				// written first and measured 13991 — one bucket, naming nothing, blaming
				// the interpreter for the wat encoder's frontier. Keyed this way the same
				// population reads as the encoder's opcode work list, which is what a work
				// plan is for.
				st := targetStratum
				if st == StratumUnset {
					// No module command at all preceded this vector: nothing failed, the
					// harness simply has nothing. Charged to exec as the layer that could
					// not answer, and it stays a fail rather than becoming unsupported —
					// classify already said the question is askable.
					st = StratumExec
				}
				key := "no instance: " + got
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: "an instance from the preceding module command",
					Got: got, Kind: c.Kind, Stratum: st,
				})
				continue
			}
			out, err := opts.Invoke(target, c.Invoke, c.Args)
			if isGated(err) {
				r.gate(c)
				continue
			}
			if c.Kind.wantsTrap() {
				// The trap *is* the expected result: an error is required, it must be a real
				// trap, and its text must contain the spec's expected string — matched as a
				// substring per decision 0003, the same rule every other expected-text arm
				// uses, against a string `Trap.Error` renders verbatim for exactly this
				// reason.
				//
				// **`isTrap` before the substring match**, which is the malformed arms' gate
				// ordering pointed at a different collision and is the whole reason TrapFunc
				// is injected. A non-trap error quoting the expected phrase would otherwise
				// score a pass the engine never earned, and no expected string in the corpus
				// reaches far enough to notice. The recon (#157) measured 0 such passes in
				// today's engine, which is a fact about today's engine; this line is what
				// makes it a fact about the harness.
				got := ""
				if err != nil {
					got = err.Error()
				}
				if err != nil && isTrap(err) && strings.Contains(got, c.Expect) {
					r.Pass++
					continue
				}
				r.Fail++
				// Three failure modes, three keys, because they are three different findings
				// and one key would make the column name none of them:
				//
				//   - **No error at all**: the call ran and returned values where the suite
				//     wants a trap. That is a *semantic* disagreement — the engine computed
				//     something the spec says is impossible — and it is the reading of this
				//     population most worth surfacing, so it is keyed by the expectation and
				//     carries the values in its Got.
				//   - **An error that is not a trap**: almost always a missing arm or the
				//     linking debt, and keyed by the engine's own text so it partitions into
				//     the opcode work list the way the assert_return arm's does.
				//   - **A trap with the wrong text**: keyed by the expectation, since what the
				//     suite wanted is the work plan and the engine's text is in the Got beside
				//     it. Distinguished from the first case by the key's suffix rather than
				//     folded into it, because "trapped wrongly" is a much smaller job than
				//     "did not trap".
				key := "assert_trap (invoke) expected: " + c.Expect
				switch {
				case err == nil:
					got = fmt.Sprintf("the call returned %d results (%s) without trapping",
						len(out), joinVals(out))
				case !isTrap(err):
					key = got
				default:
					key += " (trapped with other text)"
				}
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: c.Expect, Got: got, Kind: c.Kind,
					Stratum: StratumExec,
				})
				continue
			}
			if c.Kind.wantsException() {
				// **wantsTrap's own shape, one field shorter.** An exception *is* the expected
				// result, an error is required, and it must be a real uncaught exception — but
				// unlike assert_trap there is no expected text to match, because the grammar
				// carries none (classify's own doc comment cites `parser.mly:1468-1469` and
				// `runner.ml:571-575` for exactly this: `AssertException`'s handler discards the
				// exception's own message). So the substring step wantsTrap's arm needs simply
				// does not exist here — this is not that arm missing a check, it is one fewer
				// fact for the corpus to supply.
				//
				// **`isException` before scoring, for `isTrap`'s own reason.** A non-exception
				// error — a missing arm, the layering debt — must not score as the exception the
				// vector wants; that is the identical accept-direction collision wantsTrap's
				// ordering closes, one Kind over.
				got := ""
				if err != nil {
					got = err.Error()
				}
				if err != nil && isException(err) {
					r.Pass++
					continue
				}
				r.Fail++
				// Two failure modes, not wantsTrap's three, because there is no expected text
				// for a third mode ("right kind of error, wrong words") to exist:
				//
				//   - **No error at all**: the call ran and returned values where the suite wants
				//     an escaping exception — a semantic disagreement, keyed by the directive
				//     itself since there is no expected string to key by instead.
				//   - **An error that is not an exception**: almost always a missing arm, keyed
				//     by the engine's own text so it partitions into the opcode work list the
				//     same way assert_trap's non-trap branch does.
				const key0 = "assert_exception"
				key := key0
				if err == nil {
					got = fmt.Sprintf("the call returned %d results (%s) without raising an exception",
						len(out), joinVals(out))
				} else {
					key = got
				}
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: "an uncaught exception", Got: got, Kind: c.Kind,
					Stratum: StratumExec,
				})
				continue
			}
			if err != nil {
				r.Fail++
				// Bucketed by the *engine's* error text rather than by a fixed key, which
				// is the one place this arm's key differs from every other arm's and it is
				// deliberate. Elsewhere the suite supplies an expected string and that
				// string is the work plan; here the vector expects a *value*, so there is
				// no expected string to bucket by and a single key would produce one
				// bucket of thousands naming nothing. The engine's error is what
				// partitions the work — `unsupported opcode 6a` is an issue, "invoke
				// failed" is not.
				//
				// Untrimmed on purpose: `ErrUnsupportedOp` renders its own bytes, so the
				// bucket keys are per-opcode and the column reads as the opcode work list
				// exec.go's header promises.
				key := err.Error()
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: fmt.Sprintf("%s(%s) = %s", c.Invoke, joinVals(c.Args), joinVals(c.Results)),
					Got: key, Kind: c.Kind, Stratum: StratumExec,
				})
				continue
			}
			if c.Kind.wantsNothing() {
				// The call succeeded and there is nothing to compare. It counts as a pass
				// rather than being dropped, because the vector *was* asked and answered:
				// the suite writes a bare invoke to assert that the call completes without
				// trapping, so "it ran" is the whole expectation. Dropping it would leave
				// the denominator claiming the command was never asked, which is the
				// opposite of what happened.
				r.Pass++
				continue
			}
			// Arity first, and as its own bucket: a result-count mismatch is a different
			// defect from a wrong value, and folding it into the value comparison would
			// report "want i32 1, got nothing" where the truth is that the engine returned
			// two values.
			if len(out) != len(c.Results) {
				r.Fail++
				const key = "assert_return result arity"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line:    c.Line,
					Expect:  fmt.Sprintf("%d results (%s)", len(c.Results), joinVals(c.Results)),
					Got:     fmt.Sprintf("%d results (%s)", len(out), joinVals(out)),
					Kind:    c.Kind,
					Stratum: StratumExec,
				})
				continue
			}
			if bad := firstMismatch(c.Results, out); bad >= 0 {
				r.Fail++
				const key = "assert_return value mismatch"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line:    c.Line,
					Expect:  fmt.Sprintf("result %d of %s: %s", bad, c.Invoke, c.Results[bad]),
					Got:     out[bad].String(),
					Kind:    c.Kind,
					Stratum: StratumExec,
				})
				continue
			}
			// Every result matched, so the disjunctions among them have a choice to record.
			// Asked of MatchingAlt rather than of a second search, and asked here rather than
			// inside firstMismatch, whose subject is the *first* failure and which stops early
			// by design.
			for i, want := range c.Results {
				if want.Alts == nil {
					continue
				}
				alt, ok := want.MatchingAlt(out[i])
				if !ok {
					// Unreachable: firstMismatch returned -1, so every result matched, and
					// Matches delegates this exact search. Bucketed rather than skipped,
					// because a silent skip would quietly empty the column and the lowering
					// pin would then read as satisfied while asserting nothing.
					const key = "either expectation matched and then did not"
					r.Buckets[key] = append(r.Buckets[key], Failure{
						Line: c.Line, Expect: want.String(), Got: out[i].String(),
						Kind: c.Kind, Stratum: StratumExec,
					})
					continue
				}
				r.AltChoices = append(r.AltChoices, AltChoice{
					Line: c.Line, Result: i, Alt: alt,
					Of: len(want.Alts), Text: want.Alts[alt].String(),
				})
			}
			r.Pass++

		default:
			r.Unsupported++
			// Keyed by head rather than by Kind: every unsupported command has
			// KindUnsupported, so keying by Kind would produce one bucket of 1345 and
			// name nothing. The head is what says whether the vector waits on the
			// interpreter, the validator, or the text grammar.
			head := c.Head
			if head == "" {
				// A command with no head atom — a list whose first element is itself a
				// list or a string, as in annotations.wast's `((@a) module …)` forms,
				// where an annotation precedes the head.
				//
				// **Three of them, and they are on the board.** The clause that stood here
				// said otherwise:
				//
				//	all in annotations.wast, which the derived selector does not currently
				//	put on the board. So this branch is live-but-unexercised by today's
				//	corpus
				//
				// True when written and stale since: the board prints `annotations.wast:
				// 71/71 pass, 3 unsupported` with `3 (no head atom)`, so the selector does
				// put it on the board and this branch scores all three. Quoted rather than
				// deleted, because it was a coverage claim sitting in the arm whose own
				// coverage it described — and a stale one reads as a live measurement.
				// Counted in #320's census, drainable by teaching classify to skip
				// annotation nodes when it looks for the head.
				//
				// The placeholder stays regardless: TestUnsupportedIsBucketedByCommand
				// pins that no key is ever empty, which is the failure it prevents — an
				// unlabelled entry in a work-plan column is the one nobody investigates.
				head = "(no head atom)"
			}
			r.UnsupportedByHead[head]++
		}
	}
	return r
}

// instantiate calls the caller's instantiation entry point, or reports that there is none.
//
// Nil is legitimate and common: every caller that scores only module and malformed forms
// supplies no interpreter, and the module commands in those runs must still score. So a
// missing InstantiateFunc leaves `cur` nil with a stated reason rather than panicking — the
// panic belongs where a *declared* capability meets a nil component, which is the
// KindAssertReturn arm, because that is where the declaration is being relied on.
//
// **The linked entry point is preferred when the caller has one, and the fallback is not a
// degradation.** A module instantiated without the registry reports the §3 sentinel and its
// dependents fail in a named bucket, which is precisely the board before this change — so a
// caller with no linker keeps the board it had. The arms that must *not* fall back
// (`assert_unlinkable`) check the field themselves, because there the fallback would award
// passes: see the arm.
func (o runOpts) instantiate(c Command, reg Registry) (Instance, Stratum, error) {
	// The stratum for the harness's *own* failures to instantiate. StratumExec is right
	// here and wrong for the caller's errors: a missing InstantiateFunc is the interpreter
	// being absent, where an encoder frontier is the encoder's.
	if o.Instantiate == nil && o.InstantiateLinked == nil {
		return nil, StratumExec, errNoInstance(c, "this run supplied no InstantiateFunc")
	}
	in, st, err := o.instantiateRaw(c, reg)
	if err != nil {
		if st == StratumUnset {
			// A caller that returns an error without naming its layer gets StratumExec
			// rather than a silent zero, and the partition check names it. Charging it to
			// exec is deliberately the *conservative* direction: it lands in the newest
			// column, where an unexplained red is most likely to be looked at, rather than
			// in a front end whose ceiling is 0 and whose blame would read as a regression.
			st = StratumExec
		}
		return nil, st, err
	}
	if in == nil {
		// A nil instance with a nil error would make the no-instance bucket report
		// "instantiate succeeded" as the reason there is nothing to run — a lying
		// witness. Normalized to an error at the boundary, so the bucket's Got is
		// always a cause.
		return nil, StratumExec, errNoInstance(c, "InstantiateFunc returned a nil instance and a nil error")
	}
	return in, StratumUnset, nil
}

// instantiateRaw picks the entry point. Split out so the normalization above — unset stratum,
// nil-instance-with-nil-error — applies identically to both, rather than being written twice
// and drifting on the next change.
func (o runOpts) instantiateRaw(c Command, reg Registry) (Instance, Stratum, error) {
	if o.InstantiateLinked != nil {
		return o.InstantiateLinked(c, reg)
	}
	return o.Instantiate(c)
}

// spectestBuiltin is the `spectest` module the suite's imports are resolved against, as wat
// source, transcribed from `interpreter/host/spectest.ml`.
//
// **Synthesized as wat and instantiated through the same path every other module takes**
// (decision 0017 part 3), rather than hand-built as an engine structure. Two reasons, and the
// second is the one that decided it: a hand-built instance would need the engine's internal
// types, which is the import this package cannot make, and — more to the point — a builtin
// that skips the front end is a builtin the board's own encoder and decoder never see. If
// `spectest` reaches the interpreter by a private door, then 174 import sites are resolved
// against a module the rest of the pipeline has never agreed to. This way it is scored by
// construction: a front end that cannot read this source fails loudly at the first script that
// imports it, rather than resolving against a phantom.
//
// **14 exports, and the count is asserted rather than described** — every value here is a
// constant off the reference, and the corpus asks for all 14 across 174 sites:
//
//   - four globals (`:13-16`): `global_i32`/`global_i64` hold **666**, `global_f32`/`global_f64`
//     hold **666.6**. Immutable, which is what `imports.wast`'s mutability vectors check.
//   - seven `print_*` (`:42-45`): each takes its named parameters and returns nothing. The
//     bodies are empty rather than printing, because the suite never reads the output — the
//     reference writes to stdout and no vector asserts on it.
//   - `table` and `table64`, 10..20 funcref (`:22-28`), all slots null.
//   - `memory`, 1..2 pages (`:30-32`).
//
// The corpus's fifteenth `spectest` name is `unknown`, asked for at 5 sites, and it is
// deliberately **absent**: those five are `assert_unlinkable` vectors whose expectation is
// `unknown import`, so exporting it would convert five passes into five failures. A missing
// export is load-bearing here, which is why it is stated — an absence nobody wrote down is an
// absence someone helpfully fixes.
//
// `table64` is in this list because the *count* was checked against the reference's `lookup`
// arms rather than against the corpus's import sites: a first draft had 13 exports and passed
// every board vector, because `table64` is imported exactly once. A floor is what says so
// (TestSpectestExportsEveryNameTheCorpusAsksFor).
//
// **And `table64` is why the export set is gate-dependent rather than constant**, which is the
// one thing about this fixture that a reader will not guess. Its `i64` index type *is* the
// memory64 proposal, so on the default board the fixture's own source does not decode —
// `memory64: feature gate disabled`, from the harness's own module, panicking every script that
// imports spectest. Found by running it, which is the only way this could have been found: no
// vector fails, the whole board does.
//
// What makes 13-on-default honest rather than a quiet loss is that **the absence is
// unobservable exactly where it occurs**: the corpus imports `spectest.table64` at one site,
// `table64.wast:13`, and that vector's *own* module declares an `i64` table, so it is declined
// at its own decode on the same gate. The export exists precisely when the feature that lets
// anything ask for it exists. That is a partition, not a fallback with better manners — and
// because the difference between "partition" and "fallback" is whether anyone checked, both
// branches are pinned by count and the unobservability is asserted rather than argued.
const spectestFields = `
	(global (export "global_i32") i32 (i32.const 666))
	(global (export "global_i64") i64 (i64.const 666))
	(global (export "global_f32") f32 (f32.const 666.6))
	(global (export "global_f64") f64 (f64.const 666.6))
	(table (export "table") 10 20 funcref)
	(memory (export "memory") 1 2)
	(func (export "print"))
	(func (export "print_i32") (param i32))
	(func (export "print_i64") (param i64))
	(func (export "print_f32") (param f32))
	(func (export "print_f64") (param f64))
	(func (export "print_i32_f32") (param i32 f32))
	(func (export "print_f64_f64") (param f64 f64))`

// spectestTable64Field is spectest's fourteenth export, held apart because it is gated.
const spectestTable64Field = `
	(table (export "table64") i64 10 20 funcref)`

// spectestSource composes the fixture. Composed rather than written out twice: two full copies
// differing in one line is the drifted-fixture defect pre-installed, and the thing that must
// stay identical between the branches is the other thirteen exports.
func spectestSource(withTable64 bool) string {
	src := "(module" + spectestFields
	if withTable64 {
		src += spectestTable64Field
	}
	return src + ")"
}

// spectestRegistry is the registry a script starts with: `spectest` bound under its name, and
// nothing else.
//
// It returns an empty map when the caller has no linker, rather than skipping the
// instantiation and returning nil: a nil map reads correctly here (every lookup misses) but
// the *distinction* matters at the one place it does not — a caller with a linker whose
// spectest failed to build must not silently look like a caller with no registry, so that case
// panics. The registry running ahead of the engine is the shape those panics all have.
func (o runOpts) spectestRegistry(path string) Registry {
	reg := Registry{Instances: map[string]Instance{}, Gated: map[string]bool{}}
	if o.InstantiateLinked == nil {
		return reg
	}
	build := func(withTable64 bool) (Instance, error) {
		c := Command{
			Kind: KindModuleText, Head: "module",
			Source: []byte(spectestSource(withTable64)), Needs: CapWatReader,
		}
		// Instantiated with an *empty* registry rather than with `reg` itself: `spectest`
		// imports nothing, so passing the map it is about to be written into would be a cycle
		// waiting for its first typo. Measured off the reference — `spectest.ml` has no imports
		// at all.
		in, _, err := o.instantiate(c, Registry{
			Instances: map[string]Instance{}, Gated: map[string]bool{},
		})
		return in, err
	}
	exports := 14
	in, err := build(true)
	// **The gate is read off the engine's own answer, not guessed from the lane.** This function
	// has no view of `binary.Features` — that is the engine's business and importing it here is
	// the boundary this package exists to keep — so the question it can ask is the one it already
	// asks of every vector: was this a decline? A decline drops the gated export and requires the
	// thirteen to build; any other error is a defect in the fixture.
	if err != nil && o.IsGated != nil && o.IsGated(err) {
		exports = 13
		in, err = build(false)
	}
	if err != nil {
		// **A panic, and it is the registry-ahead-of-the-engine control again**, aimed at the
		// one input in this package the corpus does not supply. Every other module the loop
		// instantiates comes from a vector, so a failure is a board number; this one is *ours*,
		// so a failure is a defect in this file and scoring it would charge the harness's own
		// broken source to the engine's fail column — 174 import sites resolving against
		// nothing, reported as an engine that cannot link.
		//
		// **The message names which branch failed**, because the two are different defects: 14
		// failing means the fixture or the gated path is broken, 13 failing means the fixture is
		// broken outright and the gate was a red herring that would otherwise be the first thing
		// the reader chased. An error from a retry, reported without saying it was a retry, is the
		// wrong-layer tell one level up from the code that would have to diagnose it.
		panic(fmt.Sprintf("%s: the harness's own %d-export spectest module failed to instantiate: "+
			"%v; 174 import sites resolve against it, so this is a defect in spectestFields "+
			"rather than a board number", path, exports, err))
	}
	reg.bind("spectest", in)
	return reg
}

// registerTarget resolves which instance a `(register "n" [$m])` binds.
//
// The two shapes read from the two maps' worth of state: a named register takes the instance
// of that `$name`, an unnamed one takes the most recent module command's. `runner.ml:314`'s
// `register` is `let x = match x_opt with Some x -> x | None -> !last_module`, which is this.
//
// A separate function rather than four lines in the arm because the arm's *failure* path needs
// to say which of the two cases it was in, and a reader following that path should be able to
// see the success case in one piece.
func registerTarget(c Command, cur Instance, named map[string]Instance) (Instance, bool) {
	if c.Target == "" {
		return cur, cur != nil
	}
	in, ok := named[c.Target]
	return in, ok && in != nil
}

func errNoInstance(c Command, why string) error {
	return fmt.Errorf("no instance from the %s at line %d: %s", c.Kind, c.Line, why)
}

// firstMismatch returns the index of the first result that does not satisfy its
// expectation, or -1.
//
// The *index* rather than a bool, so the failure names which result differed. A vector
// returning several values with the third wrong is common in `const.wast`, and "want 1 2 3,
// got 1 2 4" makes the reader do the diff the harness already did.
func firstMismatch(want, got []Val) int {
	for i := range want {
		if !want[i].Matches(got[i]) {
			return i
		}
	}
	return -1
}

func joinVals(vs []Val) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.String()
	}
	return strings.Join(parts, ", ")
}
