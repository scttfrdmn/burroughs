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
	// verdict. No per-vector allowlist — at 1236 vectors there could not be one.
	Needs Capability

	// Expect is the expected failure text for an assertion, e.g.
	// "integer too large". Matched by substring (decision 0003) — upstream
	// runners do the same, and it lets "alignment" work as a prefix of
	// "alignment must be a power of two" without special casing.
	Expect string
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
	// (module quote "...") source into a module. Tracked at #53; the keyword table it
	// will read is already in the tree (decision 0009).
	CapWatReader Capability = "wat-reader"
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
var capabilityIssues = map[Capability]capEntry{
	CapWatReader: {
		Issue: "#53",
		Retires: "when a wat reader is wired and engineCapabilities declares CapWatReader: " +
			"this entry is deleted in the same commit, and unimplemented(wat-reader) must " +
			"be 0 — every one of its vectors converted to pass or fail, none left behind",
	},
}

// engineCapabilities is what the engine actually has. Empty today, and stated
// explicitly rather than left to omission: guard 1 of decision 0010 says the
// classifier computes what a command needs and the engine declares what it has, so
// the engine's half has to be a declaration. Silence would be the same fact carried
// by an absence, and an absence cannot be read as a claim.
//
// Adding a member here is half of a retirement: the other half is deleting the
// matching capabilityIssues entry, and TestNoCapabilityOutlivesItsComponent fails if
// only one of the two happens.
var engineCapabilities = map[Capability]bool{}

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
		s.Commands = append(s.Commands, classify(n))
	}
	return s, nil
}

func classify(n node) Command {
	head := n.head()
	switch head {
	case "module":
		if img, ok := binaryModule(n); ok {
			return Command{Kind: KindModuleBinary, Line: n.line, Head: head, Module: img}
		}
		if src, ok := quoteModule(n); ok {
			return Command{
				Kind: KindModuleQuote, Line: n.line, Head: head,
				Source: src, Needs: CapWatReader,
			}
		}
		// (module ...) with a bare wat body — #8. Still unsupported rather than
		// unimplemented, and the difference is the one decision 0010 turns on: the
		// harness cannot *ask* about a wat body, because the s-expression reader has
		// parsed it into nodes rather than holding its source text. A quote form hands
		// over its source as a string literal; a bare body does not.
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}

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
			if src, ok := quoteModule(n.list[1]); ok {
				return Command{
					Kind:   KindAssertMalformedText,
					Line:   n.line,
					Head:   head,
					Source: src,
					Expect: string(n.list[2].str),
					Needs:  CapWatReader,
				}
			}
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}
	}
	return Command{Kind: KindUnsupported, Line: n.line, Head: head}
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
	// Why not Fail, which is where these 1236 vectors would otherwise land: today the
	// fail column means *defect*. The board's lone failure (binary-gc.wast:1) is
	// visible precisely because the column discriminates wrong-answer from not-built.
	// Scoring 1236 unread quote vectors as failures takes it to 1237, and a genuine
	// regression tomorrow arrives as 1238 — invisible. A column that cannot surface a
	// new defect has stopped being an instrument, which is the lint-wall failure
	// (decision 0005) wearing a board's clothes.
	//
	// Gated is the architectural precedent rather than the argument: it exists because
	// scoring an unanswered question as a failure marks correct behaviour red. Gated is
	// absence-by-configuration; this is absence-by-construction.
	//
	// The category exists to **drain**. When the wat reader lands these convert to
	// pass/fail in a movement the board shows, and decision 0004's version rule is what
	// enforces the draining: no minor bump while its milestone's Unimplemented is
	// nonzero, and v0.1.0 requires zero.
	Unimplemented int

	// UnimplementedByCapability counts the fourth verdict by the capability each
	// command waited on, so the column is a work plan for the same reason
	// UnsupportedByHead is: "1236 unimplemented" names nothing, while
	// "1236 wat-reader (#53)" names an issue.
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

	// Failures, bucketed by expected spec text. The bucket key names exactly
	// which check is missing or wrong, which makes the board a priority queue:
	// the biggest bucket is the next issue to take, and a bucket reaching zero
	// is a PR's measure of done (CLAUDE.md, Disciplines).
	Buckets map[string][]Failure
}

// Failure is one assertion that did not hold.
type Failure struct {
	Line   int
	Expect string
	Got    string // the engine's error text, or "" if it accepted the module
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

// GatedFunc reports whether an engine error means "a feature gate is off"
// rather than a verdict on the module. The harness must not sniff error text for
// this — the engine names its own gate errors, and a substring test here would be
// the harness guessing at the taxonomy it is supposed to be checking.
type GatedFunc func(error) bool

// gated is set by the caller via Script.RunGated. When nil, no error is gated
// and the board behaves exactly as before.
// has is the set of capabilities the engine declares. Empty is the honest default:
// a caller that says nothing about its components has none beyond the decoder, so a
// command needing one is scored `unimplemented` rather than silently attempted. The
// failure mode this avoids is a new run entry point forgetting to declare and
// thereby converting the fourth verdict into a fail.
type runOpts struct {
	isGated GatedFunc
	has     map[Capability]bool
}

// Run executes a script's assertions against a decoder, scoring every gate as
// though it were on — no error is treated as a gate decline.
func (s *Script) Run(decode DecodeFunc) *Result {
	return s.run(decode, runOpts{})
}

// RunGated executes a script and separates gate declines from verdicts. isGated
// reports whether an error means the engine refused to answer because a feature
// gate is off; those vectors land in Result.Gated instead of Pass or Fail.
//
// Capabilities come from engineCapabilities, not from the caller: this is the board's
// runner, and the board must score against what the engine declares rather than
// against what a call site remembered to pass. When the wat reader lands, adding it
// to that declaration moves the board without touching this function.
func (s *Script) RunGated(decode DecodeFunc, isGated GatedFunc) *Result {
	return s.RunWith(decode, isGated, EngineCapabilities()...)
}

// RunWith executes a script with an explicit set of engine capabilities. A command
// whose Needs is not in have is scored Unimplemented (decision 0010).
//
// This is the seam the wat reader will arrive through: when it exists, it joins
// engineCapabilities and 1236 vectors move out of the fourth column into pass or
// fail. Until then nothing declares it, which is why the default is empty rather
// than "everything the harness knows about" — the latter would score vectors against
// components that do not exist.
//
// The explicit-argument form stays for tests that need to declare a capability the
// engine does not have, which must panic rather than score.
func (s *Script) RunWith(decode DecodeFunc, isGated GatedFunc, have ...Capability) *Result {
	set := make(map[Capability]bool, len(have))
	for _, c := range have {
		set[c] = true
	}
	return s.run(decode, runOpts{isGated: isGated, has: set})
}

func (s *Script) run(decode DecodeFunc, opts runOpts) *Result {
	r := &Result{
		Path: s.Path, Buckets: map[string][]Failure{},
		UnsupportedByHead:         map[string]int{},
		UnimplementedByCapability: map[Capability]int{},
	}
	isGated := func(err error) bool {
		return err != nil && opts.isGated != nil && opts.isGated(err)
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
			err := decode(c.Module)
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
				r.Gated++
				continue
			}
			// Substring matching, per decision 0003.
			if err != nil && strings.Contains(got, c.Expect) {
				r.Pass++
				continue
			}
			r.Fail++
			r.Buckets[c.Expect] = append(r.Buckets[c.Expect], Failure{
				Line: c.Line, Expect: c.Expect, Got: got,
			})

		case KindModuleBinary:
			// A bare (module binary ...) at top level asserts the module is
			// *valid*. Phase 1 only decodes, so this is a decode-must-succeed
			// check — a weaker claim than the suite makes, and the honest thing
			// is to score it rather than skip it.
			err := decode(c.Module)
			if isGated(err) {
				r.Gated++
				continue
			}
			if err != nil {
				r.Fail++
				const key = "(module binary ...) must decode"
				r.Buckets[key] = append(r.Buckets[key], Failure{
					Line: c.Line, Expect: key, Got: err.Error(),
				})
				continue
			}
			r.Pass++

		case KindModuleQuote, KindAssertMalformedText:
			// Reachable only when a caller declares CapWatReader, since the capability
			// gap above catches every other path. No reader exists yet, so there is
			// nothing honest to do here: scoring would invent a verdict and falling
			// through to `unsupported` would contradict the classification that just
			// said the harness *can* ask.
			//
			// Declared rather than silent (#6): the deferral is named at its site with
			// the issue that closes it. When #53's reader lands, this is where it is
			// called, and TestQuoteFormsAwaitTheirReader is the tripwire that fails if a
			// caller declares the capability before this branch can honour it.
			panic(fmt.Sprintf("%s:%d: CapWatReader declared but no wat reader is wired "+
				"(#53); the capability registry is ahead of the engine", s.Path, c.Line))

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
