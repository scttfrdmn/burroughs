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
	// Empty for every other Kind. Args is nil for a nullary call, which is the common
	// shape: 6560 of the answerable population take no arguments.
	Invoke  string
	Args    []Val
	Results []Val
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
	KindUnsupported                     // anything phase 1 cannot execute
)

func (k Kind) String() string {
	switch k {
	case KindModuleBinary:
		return "module"
	case KindAssertMalformed:
		return "assert_malformed"
	case KindModuleQuote:
		return "module quote"
	case KindAssertMalformedText:
		return "assert_malformed (quote)"
	case KindModuleText:
		return "module text"
	case KindAssertReturn:
		return "assert_return"
	case KindInvoke:
		return "invoke"
	case KindAssertTrapModule:
		return "assert_trap (module)"
	default:
		return "unsupported"
	}
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
		if img, ok := binaryModule(n); ok {
			return Command{Kind: KindModuleBinary, Line: n.line, Head: head, Module: img}
		}
		// Named `quoted`, not `src`: the parameter above is the *script's* bytes and this is
		// the *module's*, and letting the second shadow the first would put two different
		// sources under one name in the one function that has to keep them apart.
		if quoted, ok := quoteModule(n); ok {
			return Command{
				Kind: KindModuleQuote, Line: n.line, Head: head,
				Source: quoted, Needs: CapWatReader,
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
		// They stay `unsupported` with the head recorded, so the column names them. Phase
		// v3 (component model) is where `definition`/`instance` become answerable.
		if kw := moduleFormKeyword(n); kw == "definition" || kw == "instance" {
			return Command{Kind: KindUnsupported, Line: n.line, Head: head}
		}
		return Command{
			Kind: KindModuleText, Line: n.line, Head: head,
			Source: n.span(src), Needs: CapWatReader,
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

	case "assert_return":
		if c, ok := assertReturn(n); ok {
			return c
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

	case "assert_trap":
		// **Only the module-wrapping shape, and the narrowness is the whole point.**
		// `assert_trap` has two populations: `(assert_trap (invoke …) "text")`, the great
		// majority, waiting on the interpreter's trapping paths, and `(assert_trap (module …)
		// "text")`, which is what 0015 was written for — a module that traps *while coming to
		// life*, with no invoke anywhere in the form. This arm admits the second and leaves
		// the first in the unsupported column where it is visible.
		//
		// Split this way because the two need different engine surfaces: the action shape
		// needs an instance and then a trapping call, the module shape needs instantiation
		// itself to be able to trap, which is the return-type change 0015 records. Admitting
		// both here would have made one Kind that two different components answer.
		//
		// **The module-wrapping population is 54, not the 14 this arm was written for**, and
		// the correction came from measuring the corpus rather than from reading `data1.wast`:
		// 14 in data1, 14 in data.wast, 12 in elem.wast, 13 across linking*.wast and
		// start.wast. 0015 cited data1's 14 because that is the file the design question came
		// from, and a premise measured over the same sample the reader looks at is an echo
		// (grave #106). The 40 outside data1 need element segments, linking, or a start
		// function, so they will not pass here yet — they *fail* honestly rather than sitting
		// unclassified, which is the point of admitting the shape rather than the file.
		if len(n.list) == 3 && n.list[1].isList() && n.list[2].isS &&
			n.list[1].head() == "module" {
			// A wat-bodied module only: all 54 are the bare `(module <fields>)` form —
			// measured, no `binary` or `quote` variant of this shape exists in the corpus.
			if kw := moduleFormKeyword(n.list[1]); kw == "" {
				return Command{
					Kind: KindAssertTrapModule, Line: n.line, Head: head,
					Source: n.list[1].span(src), Expect: string(n.list[2].str),
					Needs: CapInterpreter,
				}
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
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}
	}
	return Command{Kind: KindUnsupported, Line: n.line, Head: head}
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
//   - `(invoke $M "f" …)`, a named module: **0** in the answerable population, and admitting
//     it would need module-name state the run loop does not keep.
//   - `(either …)` results, the relaxed-SIMD non-determinism form: **0** answerable, all of
//     them in bulk and relaxed-SIMD files.
//   - `(get "g")` actions, v128 constants, reference constants: their own strata.
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
func assertReturn(n node) (Command, bool) {
	no := Command{Kind: KindUnsupported, Line: n.line, Head: n.head()}
	if len(n.list) < 2 || !n.list[1].isList() {
		return no, false
	}
	c, ok := invokeAction(n.list[1])
	if !ok {
		return no, false
	}
	c.Kind, c.Line, c.Head = KindAssertReturn, n.line, n.head()
	for _, e := range n.list[2:] {
		v, ok := readConst(e)
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
		if !ok || v.NaN != NaNNone {
			// A NaN *class* in an argument position is not a value that can be passed —
			// it is a predicate. The asymmetry is enforced here rather than in the
			// matcher, because it is a statement about which vectors are askable, and
			// that is this function's subject.
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

	// Failures, bucketed by expected spec text. The bucket key names exactly
	// which check is missing or wrong, which makes the board a priority queue:
	// the biggest bucket is the next issue to take, and a bucket reaching zero
	// is a PR's measure of done (CLAUDE.md, Disciplines).
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

// GatedFunc reports whether an engine error means "a feature gate is off"
// rather than a verdict on the module. The harness must not sniff error text for
// this — the engine names its own gate errors, and a substring test here would be
// the harness guessing at the taxonomy it is supposed to be checking.
type GatedFunc func(error) bool

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

	// Instantiate and Invoke are the interpreter's two halves, and they are separate
	// fields because they are separate obligations: an engine can decode a module without
	// being able to run it, which is exactly the state this repo was in until #7. Both are
	// required by CapInterpreter and the run loop checks both.
	Instantiate InstantiateFunc
	Invoke      InvokeFunc

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
	// cur is the instance the *most recent* module command produced, which is what an
	// `assert_return` runs against.
	//
	// **"Most recent" is measured, not assumed.** The reference's script semantics let an
	// action name a module — `(invoke $M "f" …)` — and a classifier ignoring the name would
	// invoke the wrong module and score whatever came out. There are **0** such actions in
	// the answerable population, and 7 answerable vectors sit after a `(register …)` in the
	// same file, which affects imports rather than which module an unnamed invoke selects. So
	// one slot is the whole state, and the day a named action becomes askable it arrives as
	// its own Kind, where the classification decision is visible.
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
			// A bare (module binary ...) at top level asserts the module is
			// *valid*. Phase 1 only decodes, so this is a decode-must-succeed
			// check — a weaker claim than the suite makes, and the honest thing
			// is to score it rather than skip it.
			err := opts.Decode(c.Module)
			if isGated(err) {
				// The instance is cleared on a gate decline too. An `assert_return`
				// following a declined module must not run against the *previous*
				// module's instance — it would score a real verdict from the wrong
				// program, which is worse than reporting no instance.
				cur, curErr, curStratum, curGated = nil, errNoInstance(c, "gate declined the module"), StratumBinary, true
				r.gate(c)
				continue
			}
			if err != nil {
				cur, curErr, curStratum, curGated = nil, err, StratumBinary, false
				r.Fail++
				const key = "(module binary ...) must decode"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: err.Error(), Kind: c.Kind,
					Stratum: StratumBinary,
				})
				continue
			}
			cur, curStratum, curErr = opts.instantiate(c)
			curGated = isGated(curErr)
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
				cur, curErr, curStratum, curGated = nil, errNoInstance(c, "gate declined the module"), StratumText, true
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
					cur, curErr, curStratum, curGated = nil, err, StratumText, false
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
				// **The module command keeps its pass and the decline is carried forward.**
				// A gate decline arriving from instantiation is not a verdict on the front
				// end — ReadModule accepted the source, which is the right answer and is
				// #124's ruling that the text front end stays gate-blind. Scoring the module
				// command as `gated` here would move a pass the reader earned into the third
				// verdict; scoring the downstream assert_return as `fail` would mark correct
				// behaviour red. So the front end is scored on its own answer and the
				// decline travels to the vector whose question it actually blocks.
				cur, curStratum, curErr = opts.instantiate(c)
				curGated = isGated(curErr)
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
			if opts.Instantiate == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no InstantiateFunc was "+
					"supplied; the capability registry is ahead of the engine", s.Path, c.Line))
			}
			_, _, err := opts.instantiate(c)
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

		case KindAssertReturn, KindInvoke:
			// **Two kinds, one arm, and the sharing is the point.** A bare `(invoke …)` is an
			// assert_return with no expectation: it needs the same instance, the same
			// no-instance accounting, the same gate handling, and the same error bucketing,
			// and it differs only in having nothing to compare afterwards. A second arm would
			// be a second copy of all of that, drifting from this one on the next change.
			//
			// Reachable only when a caller declares CapInterpreter, and the same
			// re-pointed tripwire the text kinds carry: a declaration with no component
			// is the registry ahead of the engine, and it must stop rather than score.
			// Both halves are named, because an engine that can instantiate and not
			// invoke is a real intermediate state and a nil deref is not a diagnosis.
			if opts.Instantiate == nil || opts.Invoke == nil {
				panic(fmt.Sprintf("%s:%d: CapInterpreter declared but no %s was supplied; "+
					"the capability registry is ahead of the engine", s.Path, c.Line,
					map[bool]string{true: "InstantiateFunc", false: "InvokeFunc"}[opts.Instantiate == nil]))
			}
			// No instance: the module command that should have produced one failed, was
			// gate-declined, or never appeared. **Its own bucket, never the invoke's** —
			// the module's failure is already counted where it happened, and charging the
			// same defect to the interpreter's column would make this layer's work plan
			// name a component that is not broken. *An error from the wrong layer is
			// evidence about where structure was lost*, and here the harness knows the
			// layer exactly, so it says so.
			if cur == nil && curGated {
				// The module this vector needs was declined for a feature, so the
				// question was never asked — the same reason the module arms above
				// count a decline rather than a failure, one command downstream.
				// Checked *before* the fail branch for the reason a gate decline is
				// checked before the substring match on the malformed arms: order is
				// what makes the collision impossible rather than merely unlikely.
				r.gate(c)
				continue
			}
			if cur == nil {
				r.Fail++
				got := "no preceding module command produced an instance"
				if curErr != nil {
					got = curErr.Error()
				}
				// **Keyed by the failing module's error, and charged to its stratum**, not
				// to a single "no instance" key charged to exec. That single key was
				// written first and measured 13991 — one bucket, naming nothing, blaming
				// the interpreter for the wat encoder's frontier. Keyed this way the same
				// population reads as the encoder's opcode work list, which is what a work
				// plan is for.
				st := curStratum
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
			out, err := opts.Invoke(cur, c.Invoke, c.Args)
			if isGated(err) {
				r.gate(c)
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
			if c.Kind == KindInvoke {
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
				// list or a string, as in annotations.wast's `(@custom ...)` forms.
				// Measured, not assumed: three such commands across the vendored suite,
				// all in annotations.wast, which the derived selector does not currently
				// put on the board. So this branch is live-but-unexercised by today's
				// corpus and stays because the corpus moves — TestUnsupportedIsBucketed-
				// ByCommand pins that no key is ever empty, which is the failure this
				// placeholder prevents: an unlabelled entry in a work-plan column is the
				// one nobody investigates.
				head = "(no head atom)"
			}
			r.UnsupportedByHead[head]++
		}
	}
	return r
}

// instantiate calls the caller's InstantiateFunc, or reports that there is none.
//
// Nil is legitimate and common: every caller that scores only module and malformed forms
// supplies no interpreter, and the module commands in those runs must still score. So a
// missing InstantiateFunc leaves `cur` nil with a stated reason rather than panicking — the
// panic belongs where a *declared* capability meets a nil component, which is the
// KindAssertReturn arm, because that is where the declaration is being relied on.
func (o runOpts) instantiate(c Command) (Instance, Stratum, error) {
	// The stratum for the harness's *own* failures to instantiate. StratumExec is right
	// here and wrong for the caller's errors: a missing InstantiateFunc is the interpreter
	// being absent, where an encoder frontier is the encoder's.
	if o.Instantiate == nil {
		return nil, StratumExec, errNoInstance(c, "this run supplied no InstantiateFunc")
	}
	in, st, err := o.Instantiate(c)
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
