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
	KindModuleBinary    Kind = iota // (module binary "...")
	KindAssertMalformed             // (assert_malformed (module binary ...) "text")
	KindUnsupported                 // anything phase 1 cannot execute
)

func (k Kind) String() string {
	switch k {
	case KindModuleBinary:
		return "module"
	case KindAssertMalformed:
		return "assert_malformed"
	default:
		return "unsupported"
	}
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
		// (module ...) with a wat body, or (module quote ...) — #53 and #8.
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
		}
		return Command{Kind: KindUnsupported, Line: n.line, Head: head}
	}
	return Command{Kind: KindUnsupported, Line: n.line, Head: head}
}

// binaryModule extracts the image from (module [$name] binary "..." "..."),
// concatenating the byte strings. It reports false for any other module form,
// including (module quote ...) and wat bodies.
func binaryModule(n node) ([]byte, bool) {
	if n.head() != "module" {
		return nil, false
	}
	i := 1
	// Optional $name, as in (module $M1 binary "...").
	if i < len(n.list) && !n.list[i].isList() && !n.list[i].isS && strings.HasPrefix(n.list[i].atom, "$") {
		i++
	}
	if i >= len(n.list) || n.list[i].isList() || n.list[i].isS || n.list[i].atom != "binary" {
		return nil, false
	}
	i++
	// Everything after `binary` must be a string literal.
	img := []byte{}
	for ; i < len(n.list); i++ {
		if !n.list[i].isS {
			return nil, false
		}
		img = append(img, n.list[i].str...)
	}
	return img, true
}

// Result is the outcome of running one script.
type Result struct {
	Path string

	Pass        int
	Fail        int
	Unsupported int

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
// have. Unsupported is excluded for the same reason. The three exclusions and the
// two counted verdicts should always sum to the command count, which
// TestVerdictsPartitionCommands pins.
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
	if len(r.Buckets) > 0 {
		b.WriteString("\n  failures bucketed by expected spec text (largest first):")
		for _, k := range r.BucketsBySize() {
			fmt.Fprintf(&b, "\n    %3d  %s", len(r.Buckets[k]), k)
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
type runOpts struct {
	isGated GatedFunc
}

// Run executes a script's assertions against a decoder, scoring every gate as
// though it were on — no error is treated as a gate decline.
func (s *Script) Run(decode DecodeFunc) *Result {
	return s.run(decode, runOpts{})
}

// RunGated executes a script and separates gate declines from verdicts. isGated
// reports whether an error means the engine refused to answer because a feature
// gate is off; those vectors land in Result.Gated instead of Pass or Fail.
func (s *Script) RunGated(decode DecodeFunc, isGated GatedFunc) *Result {
	return s.run(decode, runOpts{isGated: isGated})
}

func (s *Script) run(decode DecodeFunc, opts runOpts) *Result {
	r := &Result{Path: s.Path, Buckets: map[string][]Failure{}, UnsupportedByHead: map[string]int{}}
	isGated := func(err error) bool {
		return err != nil && opts.isGated != nil && opts.isGated(err)
	}
	for _, c := range s.Commands {
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
