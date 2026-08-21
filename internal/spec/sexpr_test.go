package spec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// Unit tests for the reader itself, so a parser bug is distinguishable from a
// decoder bug when the suite board moves.

// stubValidate and stubDeclined are fact 2 stubbed out for the probes in this file, in the shape
// their `Decode` and `ReadText` stubs already use: every module is valid, nothing is declined.
//
// #341 made a module definition assert that the module *validates*, so an Engine that reads text
// needs a validator or the arm panics — the registry-ahead-of-the-engine tripwire, one component
// over. A permissive stub is the right answer for a probe whose subject is the parser, for the same
// reason `Decode: func([]byte) error { return nil }` is: the component is being taken out of the
// picture rather than measured. It is emphatically *not* the right answer for the board, and the
// difference is the whole of #341 — a validator that says yes to everything is exactly what
// `internal/validate/global_test.go`'s M11 row measured surviving. The board's engine supplies the
// real one (`engine()` in spec_test.go), and TestModuleDefinitionsAskTheValidator is what asserts
// that a refusal there is scored.
var (
	stubValidate ValidateFunc = func(Command) (Stratum, error) { return StratumValidate, nil }
	stubDeclined DeclinedFunc = func(error) bool { return false }
)

// withFact2 supplies *and declares* the permissive validator on a stub engine whose script
// contains a top-level `(module binary …)`.
//
// #353 gave that form's arm fact 2, so its Kind carries `Needs: CapValidator` and a caller must do
// both halves: hand over the component, and say it has it. The two halves are separate on purpose
// (guard 1 of decision 0010 — the classifier derives what a command needs, the engine declares what
// it has), and supplying without declaring is what the gap check refuses to guess about. It is a
// named helper rather than a copy per site because the *reason* is what a reader needs: one place to
// read why a probe about traps or exceptions carries a validator at all, and a name that says the
// answer is "so the setup module can be defined", not "so validation is measured here". Its callers
// are its own call sites and are not counted here — a count in a comment schedules its own next
// increment, and `grep -c 'withFact2(Engine{'` is the instrument.
//
// It fills the fields only when they are empty, so a probe that supplies its own validator — the
// ones whose subject *is* fact 2 — keeps it and is not silently overridden.
func withFact2(e Engine) Engine {
	if e.Validate == nil {
		e.Validate = stubValidate
	}
	if e.IsDeclined == nil {
		e.IsDeclined = stubDeclined
	}
	if !slices.Contains(e.Has, CapValidator) {
		e.Has = append(e.Has, CapValidator)
	}
	return e
}

func TestParseStringEscapes(t *testing.T) {
	cases := []struct {
		src  string
		want []byte
	}{
		{`"\00asm"`, []byte{0x00, 'a', 's', 'm'}},
		{`"\01\00\00\00"`, []byte{0x01, 0x00, 0x00, 0x00}},
		{`"a custom section"`, []byte("a custom section")},
		{`"\ff"`, []byte{0xFF}}, // one raw byte, NOT a rune — utf8-*.wast depends on this
		{`"\t\n\r\"\'\\"`, []byte{'\t', '\n', '\r', '"', '\'', '\\'}},
		{`"\u{41}"`, []byte("A")},
		{`"\u{1F600}"`, []byte("\U0001F600")},
		{`""`, []byte{}},
	}
	for _, c := range cases {
		p := newParser([]byte(c.src))
		n, err := p.parseNode()
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !n.isS {
			t.Errorf("%s: not parsed as a string literal", c.src)
			continue
		}
		if !bytes.Equal(n.str, c.want) {
			t.Errorf("%s: got % x, want % x", c.src, n.str, c.want)
		}
	}
}

// TestEmptyStringIsNotNil pins the nil-vs-empty distinction found by
// FuzzWastLexer. `isS` reports "this node is a string"; `str` carries the bytes.
// If `str` were nil for `""`, the two would be entangled and any reader checking
// `str != nil` would misread `(module binary "")` — the empty image, which is
// the "unexpected end" boundary and the single most-exercised vector in
// binary.wast. Emptiness must be a length, never a nil.
func TestEmptyStringIsNotNil(t *testing.T) {
	for _, src := range []string{`""`, `(module binary "")`} {
		nodes, err := newParser([]byte(src)).parseAll()
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		var found bool
		var walk func(n node)
		walk = func(n node) {
			if n.isS {
				found = true
				if n.str == nil {
					t.Errorf("%s: empty string literal has nil bytes; want non-nil len 0", src)
				}
			}
			for _, c := range n.list {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
		if !found {
			t.Errorf("%s: no string literal parsed", src)
		}
	}
	// The extracted image must be non-nil too, so a caller can distinguish
	// "no binary form" (nil, false) from "the empty image" (non-nil, true).
	n, err := newParser([]byte(`(module binary "")`)).parseNode()
	if err != nil {
		t.Fatal(err)
	}
	img, ok := binaryModule(n)
	if !ok || img == nil || len(img) != 0 {
		t.Errorf("binaryModule of the empty image = %v, %v; want non-nil len 0, true", img, ok)
	}
}

func TestParseStringRejects(t *testing.T) {
	for _, src := range []string{
		`"unterminated`,
		`"bad escape \z"`,
		`"short hex \f"`,
		`"newline
in string"`,
		`"\u{}"`,
		`"\u41"`,
	} {
		p := newParser([]byte(src))
		if _, err := p.parseNode(); err == nil {
			t.Errorf("%q: expected a parse error, got none", src)
		}
	}
}

func TestParseComments(t *testing.T) {
	src := `
;; a line comment
(module binary "\00asm") ;; trailing
(; a block
   comment (; nested ;) still in it ;)
(module binary "\01")
`
	nodes, err := newParser([]byte(src)).parseAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d top-level forms, want 2: %v", len(nodes), nodes)
	}
	for _, n := range nodes {
		if n.head() != "module" {
			t.Errorf("got head %q, want module", n.head())
		}
	}
}

// TestParseAnnotationTokenSoup is a regression vector for the reader's third
// grave: a bare ';' inside a custom annotation. The line below is copied
// verbatim from annotations.wast:14, the one file out of 257 that the reader
// could not traverse. ';' is a delimiter, so the atom loop consumed zero bytes
// and errored on its own delimiter.
//
// The grave's own assertion is that this **parses**, because a parse error and a scored
// command are different numbers on the board and the reader must be able to traverse a file
// it does not interpret.
//
// **The verdict half was re-pointed by #69, not patched.** It used to read `r.Unsupported !=
// 1 || r.Total() != 0` — "nothing here classifies to a runnable command" — which was true
// while a bare `(module <wat body>)` had no retrievable source. It is now a scored
// KindModuleText, so the old assertion would have been a test asserting the absence of a
// feature that had arrived. The grave's subject (';' inside an annotation) is untouched; only
// what the harness *does* with the form afterwards changed, so the parse assertion stays
// verbatim and the verdict assertion follows the form's new kind.
//
// It runs through RunWith rather than Run: the form needs CapWatReader now, and Run declares
// nothing, so Run would panic here by design. That panic is guard 1 doing its job and is
// asserted directly in TestQuoteFormsHaveTheirReader, whose recovered panic *is* the
// assertion. (It said TestBareModuleNeedsWatReader, which has never existed — a citation
// nobody re-checks is a claim, and a test name is as checkable as a `.wast:N`. Swept with
// #88 by comparing every `Test*` mentioned in the tree against every `Test*` defined.)
func TestParseAnnotationTokenSoup(t *testing.T) {
	src := `
(module
  (@a , ; ] [ }} }x{ ({) ,{{};}] ;)
  (func (@name "f"))
)
`
	nodes, err := newParser([]byte(src)).parseAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 1 || nodes[0].head() != "module" {
		t.Fatalf("got %v, want one module form", nodes)
	}
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := s.RunWith(Engine{
		Decode:     func([]byte) error { return nil },
		ReadText:   func([]byte) error { return nil },
		Validate:   stubValidate,
		IsDeclined: stubDeclined,
		IsGated:    func(error) bool { return false },
		Has:        []Capability{CapWatReader},
	})
	if r.Pass != 1 || r.Unsupported != 0 {
		t.Errorf("got %d pass, %d unsupported; want 1/0 — the bare module form is scored "+
			"since #69", r.Pass, r.Unsupported)
	}
}

func TestBinaryModuleForms(t *testing.T) {
	cases := []struct {
		src  string
		ok   bool
		want []byte
	}{
		// Concatenated string arguments, as binary.wast writes them.
		{
			`(module binary "\00asm" "\01\00\00\00")`, true,
			[]byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00},
		},
		// Single string form.
		{
			`(module binary "\00asm\01\00\00\00")`, true,
			[]byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00},
		},
		// Named module: (module $M1 binary ...)
		{`(module $M1 binary "\00asm")`, true, []byte{0x00, 'a', 's', 'm'}},
		// Empty image is legal to extract — "" is a real test case.
		{`(module binary "")`, true, []byte{}},
		// Not binary forms.
		{`(module quote "(module)")`, false, nil},
		{`(module (func))`, false, nil},
		{`(module)`, false, nil},
	}
	for _, c := range cases {
		n, err := newParser([]byte(c.src)).parseNode()
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		got, ok := binaryModule(n)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, want %v", c.src, ok, c.ok)
			continue
		}
		if ok && !bytes.Equal(got, c.want) {
			t.Errorf("%s: got % x, want % x", c.src, got, c.want)
		}
	}
}

func TestClassifyAndRun(t *testing.T) {
	src := `
(module binary "\00asm\01\00\00\00")
(assert_malformed (module binary "") "unexpected end")
(assert_malformed (module binary "asm\00\01\00\00\00") "magic header not detected")
(assert_malformed (module quote "(module (func))") "unexpected token")
(assert_return (invoke "f") (i32.const 1))
`
	s, err := Parse("test.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(s.Commands) != 5 {
		t.Fatalf("got %d commands, want 5", len(s.Commands))
	}
	want := []Kind{
		KindModuleBinary,
		KindAssertMalformed,
		KindAssertMalformed,
		// The quote form is *recognized* now (decision 0010) and carries
		// Needs: CapWatReader. It was KindUnsupported until the admission, and the
		// change of expectation here is the point rather than a repair: the harness
		// can now ask this question, which is exactly what separates the fourth
		// verdict from the third.
		KindAssertMalformedText,
		// And the same sentence a second time, for the interpreter (#7). This was
		// KindUnsupported with the comment "assert_return is phase 2" from the harness's
		// first commit until an instruction executed; it is now the narrow invoke shape
		// assertReturn admits, carrying Needs: CapInterpreter. The expectation changing
		// here is the classification seam moving, which is what a capability landing looks
		// like from the classifier's side.
		KindAssertReturn,
	}
	for i, k := range want {
		if s.Commands[i].Kind != k {
			t.Errorf("command %d: got %v, want %v", i, s.Commands[i].Kind, k)
		}
	}
	// The capability is attached by the classifier, not by the run loop — the
	// derived-gap mechanism starts here, and it stays after the retirement: the
	// classifier's job is to say what a command needs, whether or not the engine
	// happens to have it.
	if got := s.Commands[3].Needs; got != CapWatReader {
		t.Errorf("quote command Needs = %q, want %q", got, CapWatReader)
	}
	if len(s.Commands[3].Source) == 0 {
		t.Error("quote command carried no Source; the run loop would have nothing to read")
	}
	if got := s.Commands[4].Needs; got != CapInterpreter {
		t.Errorf("assert_return Needs = %q, want %q", got, CapInterpreter)
	}
	// The action, read at classification time rather than at run time — which is what lets
	// Needs be *derived* from the command (guard 1) and keeps the run loop free of grammar.
	if got := s.Commands[4].Export; got != "f" {
		t.Errorf("assert_return Export = %q, want %q", got, "f")
	}
	if got := s.Commands[4].Results; len(got) != 1 || got[0].Kind != KindI32 || got[0].Bits != 1 {
		t.Errorf("assert_return Results = %v, want [i32 1]", got)
	}

	// A decoder that satisfies both malformed assertions and the valid module, a reader
	// that produces the expected text for the quote form, and a stub interpreter that
	// answers the invoke.
	//
	// Every component is *supplied* here where the pre-retirement version of this test used
	// the bare Run: a caller that declares a capability and hands over nothing panics rather
	// than scoring, which is the tripwire re-pointed at the case still available
	// (TestQuoteFormsHaveTheirReader asserts that panic directly).
	//
	// The interpreter is a stub rather than the real one, deliberately: this test's subject
	// is *classification and scoring*, and a real `interp.Instance` would make it fail
	// whenever the engine's opcode coverage moved. The board tests are where the real engine
	// is scored.
	r := s.RunWith(Engine{
		Decode: func(image []byte) error {
			if len(image) < 8 {
				return errString("unexpected end")
			}
			if !bytes.Equal(image[:4], []byte{0x00, 'a', 's', 'm'}) {
				return errString("magic header not detected")
			}
			return nil
		},
		ReadText:    func([]byte) error { return errString("unexpected token") },
		Validate:    stubValidate,
		IsDeclined:  stubDeclined,
		Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
		Invoke: func(_ Instance, name string, _ []Val) ([]Val, error) {
			if name != "f" {
				return nil, errString("unknown export " + name)
			}
			return []Val{{Kind: KindI32, Bits: 1}}, nil
		},
		// CapValidator joins the declaration with #353: the `(module binary …)` command above
		// carries `Needs: CapValidator` now that its arm asks fact 2, and this caller was already
		// supplying `Validate`/`IsDeclined` — it had simply not *declared* them. The two halves are
		// separate by design (guard 1: the classifier derives what a command needs, the engine
		// declares what it has), so supplying a component without declaring it is exactly the
		// under-declaration the gap check refuses to guess about.
		Has: []Capability{CapWatReader, CapInterpreter, CapValidator},
	})
	// 5 pass, 0 unsupported, 0 unimplemented. Two retirements are visible in that line:
	// the quote vector, which was `unimplemented` while no reader existed, and the
	// assert_return, which was `unsupported` until the interpreter landed. A component
	// landing without draining its column is the disappearance guard 6 exists to prevent
	// (decision 0010).
	if r.Pass != 5 || r.Fail != 0 || r.Unsupported != 0 || r.Unimplemented != 0 {
		t.Errorf("got %d pass, %d fail, %d unsupported, %d unimplemented; want 5/0/0/0\n%s",
			r.Pass, r.Fail, r.Unsupported, r.Unimplemented, r.Board())
	}
	if len(r.UnimplementedByCapability) != 0 {
		t.Errorf("fourth verdict populated after retirement: %v", r.UnimplementedByCapability)
	}
}

// TestBucketsAreAPriorityQueue pins the property that makes the board a work
// plan: buckets are ordered largest-first, so the top bucket is the next issue.
func TestBucketsAreAPriorityQueue(t *testing.T) {
	src := `
(assert_malformed (module binary "\01") "wanted A")
(assert_malformed (module binary "\02") "wanted A")
(assert_malformed (module binary "\03") "wanted A")
(assert_malformed (module binary "\04") "wanted B")
(assert_malformed (module binary "\05") "wanted C")
(assert_malformed (module binary "\06") "wanted C")
`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// A decoder that never produces the expected text — every assertion fails.
	r := s.Run(func([]byte) error { return errString("something else") })
	if r.Pass != 0 || r.Fail != 6 {
		t.Fatalf("got %d pass %d fail, want 0/6", r.Pass, r.Fail)
	}
	order := r.BucketsBySize()
	if len(order) != 3 || order[0] != "wanted A" || order[1] != "wanted C" || order[2] != "wanted B" {
		t.Errorf("bucket order %v, want [wanted A, wanted C, wanted B]", order)
	}
}

// TestSubstringMatching pins decision 0003's matching rule, including the
// "alignment" prefix case that motivated it.
func TestSubstringMatching(t *testing.T) {
	s, err := Parse("t.wast", []byte(`(assert_malformed (module binary "\01") "alignment")`))
	if err != nil {
		t.Fatal(err)
	}
	r := s.Run(func([]byte) error { return errString("alignment must be a power of two") })
	if r.Pass != 1 {
		t.Errorf("substring match failed: %d pass, %d fail", r.Pass, r.Fail)
	}
	// An engine that accepts a module the suite calls malformed must fail, not pass.
	r = s.Run(func([]byte) error { return nil })
	if r.Fail != 1 {
		t.Errorf("accepting a malformed module should fail: %d pass, %d fail", r.Pass, r.Fail)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestNodeSpanIsExactSource pins the span retention that made bare module forms askable
// (#69), and it pins it as an **equality against the source text**, not as a non-empty check.
//
// The distinction is the vacuity rule. A span that is merely non-empty, or merely contains the
// form, would satisfy a looser assertion while handing the wat reader a truncated module or one
// with a neighbour's trailing bytes — and the reader would then report a *syntax* error for a
// module the suite calls valid, which reads on the board as an engine defect. So each case
// states the exact bytes expected, and nested forms are checked too: a list's span has to close
// on its own paren rather than on the outermost one.
func TestNodeSpanIsExactSource(t *testing.T) {
	src := []byte("(module (func $f) (memory 1))\n(assert_return (invoke \"f\"))")
	nodes, err := newParser(src).parseAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d top-level forms, want 2", len(nodes))
	}
	if got, want := string(nodes[0].span(src)), "(module (func $f) (memory 1))"; got != want {
		t.Errorf("top-level span = %q, want %q", got, want)
	}
	if got, want := string(nodes[1].span(src)), `(assert_return (invoke "f"))`; got != want {
		t.Errorf("second form span = %q, want %q", got, want)
	}
	// Nested: each child closes on its own paren. This is the half a "span contains the
	// form" assertion would miss.
	kids := nodes[0].list
	if len(kids) != 3 {
		t.Fatalf("got %d children, want 3 (module, func, memory)", len(kids))
	}
	for i, want := range []string{"module", "(func $f)", "(memory 1)"} {
		if got := string(kids[i].span(src)); got != want {
			t.Errorf("child %d span = %q, want %q", i, got, want)
		}
	}
}

// TestBareModuleSourceRoundTrips is the control that matters for the board: the span handed to
// the wat reader must be a module the reader can actually read.
//
// A span off by one byte in either direction still *looks* like a module — `(module (func)` and
// `(module (func))` differ by one character — and the failure would surface as a syntax error
// attributed to the engine. So this asserts the retained source parses as wat, which is the
// property the 2130 newly-scored passes depend on.
func TestBareModuleSourceRoundTrips(t *testing.T) {
	src := []byte("(module (func $f (param i32) (result i32) (local.get 0)))\n(module (memory 1))")
	s, err := Parse("t.wast", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(s.Commands))
	}
	for i, c := range s.Commands {
		if c.Kind != KindModuleText {
			t.Errorf("command %d kind = %v, want KindModuleText", i, c.Kind)
			continue
		}
		if c.Needs != CapWatReader {
			t.Errorf("command %d needs %q, want %q", i, c.Needs, CapWatReader)
		}
		// Re-read the retained source as an s-expression. A truncated or over-long span
		// fails here, where a length check would not.
		re, err := newParser(c.Source).parseAll()
		if err != nil {
			t.Errorf("command %d source %q does not re-parse: %v", i, c.Source, err)
			continue
		}
		if len(re) != 1 || re[0].head() != "module" {
			t.Errorf("command %d source %q re-parsed to %d forms, want one module",
				i, c.Source, len(re))
		}
	}
}

// TestScriptModuleFormsAreNotWatBodies pins the classification error that manufactured 9 of
// #69's first 22 failures.
//
// `definition` and `instance` are *script* grammar (parser.mly:1417/:1439) — `definition` sits
// outside `module_`, and `instance` has no fields at all — so handing either to the wat reader
// invents a red out of a harness mistake. The reader is right to reject them; it was never
// asked a fair question. *Gates never manufacture malformedness* generalizes past gates.
//
// Scoped to both forms *and* to the plain body, because the risk runs both ways: a predicate
// that excluded too much would silently drop real modules back into `unsupported`, which is
// the invisibility this whole issue exists to end.
//
// # The four decline rows moved, and the reason they could is one token
//
// #426 gave both forms Kinds. The rows are **rebased, not deleted** — the discipline the
// `assert_trap` test below records twice: a decline row that becomes an accept row is what shows
// the classification was decided rather than drifted into. What this function's name asserts is
// unchanged and still true: **neither form's own text is a wat body**, and the fix is not to relax
// that.
//
// It is exact, not approximate. `script_module` is `LPAR MODULE definition_opt option(module_var)
// module_fields RPAR` (parser.mly:1417) and `module_` is the same production *without*
// `definition_opt` (:1389) — so a definition form's wat body is its own span with the one keyword
// excised, which is what `definitionSource` does. No reconstruction, no re-lexing, and the `$name`
// stays where the reader expects it. `instance` still has no body at all: it names a definition,
// so it carries no source and reaches the wat reader never.
//
// The one row that stays `KindUnsupported` for a *content* reason rather than a shape one is
// `(module definition binary "…")` — `definition_opt` composes with the string forms, so excising
// the keyword there would hand `binary "…"` to the wat reader and manufacture precisely the red
// this test is named after. Guarded, and it is a row here because a guard whose bypass is one
// missing `if` needs an assertion, not a comment.
func TestScriptModuleFormsAreNotWatBodies(t *testing.T) {
	cases := []struct {
		src  string
		want Kind
	}{
		// The two script forms, askable since #426. Their Kinds are the assertion; that their
		// *source* is not the raw span is asserted by TestDefinitionSourceExcisesTheKeyword.
		{"(module definition (memory 65536))", KindModuleDefinition},
		{"(module definition $M (global (export \"g\") (mut i32) (i32.const 0)))", KindModuleDefinition},
		{"(module instance $I1 $M)", KindModuleInstance},
		// Still declines, and each for a different reason — which is why they are four rows and
		// not one. Arity: `instance` needs both names, so a truncated form is not silently read as
		// something. Content: the string sub-forms compose with `definition` and are not wat.
		{"(module instance)", KindUnsupported},
		{"(module instance $I1)", KindUnsupported},
		{`(module definition binary "\00asm\01\00\00\00")`, KindUnsupported},
		{`(module definition quote "(func)")`, KindUnsupported},
		// The accept direction: an ordinary body, and one with a $name, stay scorable.
		{"(module (memory 1))", KindModuleText},
		{"(module $M (memory 1))", KindModuleText},
		// A quote form is still a quote form — the keyword check must not shadow it.
		{`(module quote "(func)")`, KindModuleQuote},
		{`(module binary "\00asm")`, KindModuleBinary},
	}
	for _, c := range cases {
		s, err := Parse("t.wast", []byte(c.src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		if len(s.Commands) != 1 {
			t.Fatalf("Parse(%q) gave %d commands, want 1", c.src, len(s.Commands))
		}
		if got := s.Commands[0].Kind; got != c.want {
			t.Errorf("%s\n  classified %v, want %v", c.src, got, c.want)
		}
	}
}

// TestDefinitionSourceExcisesTheKeyword is the other half of the row above, and it is a separate
// test because the two claims fail independently: a definition form can carry the right Kind and
// the wrong bytes, and that combination is worse than a decline — it reaches the reader and the
// reader's complaint is about text the corpus never wrote.
//
// **The assertion is on the payload, not on a status flag.** `classify` returning
// `KindModuleDefinition` says the arm ran; only reading `Source` back says what it will hand over.
// So each row states the exact bytes expected, and one row is a multi-line body with the keyword
// on the same line as `(module` — the shape `instance.wast:3` actually has — because a span-splice
// that got the offsets wrong would still look right on a single-line input.
func TestDefinitionSourceExcisesTheKeyword(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"(module definition (memory 65536))", "(module  (memory 65536))"},
		{"(module definition $M (memory 1))", "(module  $M (memory 1))"},
		{"(module definition\n  $M\n  (memory 1)\n)", "(module \n  $M\n  (memory 1)\n)"},
	}
	for _, c := range cases {
		s, err := Parse("t.wast", []byte(c.src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		got := string(s.Commands[0].Source)
		if got != c.want {
			t.Errorf("%q\n  Source = %q\n  want     %q", c.src, got, c.want)
		}
		// A second mechanism against the rows above, and its subject is *me*: the `want` strings
		// are hand-transcribed, so a splice that kept the keyword and a `want` that also kept it
		// would agree. This one derives nothing from them. It is not the wat reader's verdict —
		// that is the board's job, on the real corpus, and this file has no engine to ask.
		if strings.Contains(got, "definition") {
			t.Errorf("%q: Source still contains the keyword: %q", c.src, got)
		}
	}
}

// TestAssertTrapSplitsByWrappedForm pins the classifier's `assert_trap` partition: the two
// populations get the two Kinds, and every other shape stays unsupported.
//
// **Checked against the partition rather than against the case labels** (grave #34): the
// partition is "what does `assert_trap` wrap", so the accept direction needs both members —
// a module form and an action form — and the reject direction needs the shapes that resemble
// each. A test naming a partition and covering one side of it is the coverage defect that
// grave was filed for, and here the two sides are answered by *different Kinds*, so getting
// one arm right says nothing about the other.
//
// The `$M` and reference-argument rows are the 27 of 4903 the recon measured as declines.
// They are here because a decline is a claim about which vectors are askable, and the day one
// becomes askable this test is where the classification decision has to be made visible.
//
// **That day arrived twice, and each row moved rather than being deleted.** The registry
// (0017 Q1) makes `(assert_trap (invoke $M …) …)` askable, so `$M` is now `KindNamedAssertTrap`
// and the row asserts the new Kind — which is the sentence above being *collected* rather than
// merely vindicated: a decline row that becomes an accept row is the one artefact that proves
// the classification decision was made deliberately instead of drifting in.
//
// The reference-argument row moved the same way, later (#196/#197): `readConst` now names
// `ref.null func`, and the row's Kind changed from KindUnsupported to KindAssertTrapAction to
// say so — kept adjacent to the `$M` row for the same reason as before, so the two rows'
// history (two different declines, closed at two different times, by two different
// mechanisms) stays visible rather than collapsing into "always worked".
func TestAssertTrapSplitsByWrappedForm(t *testing.T) {
	cases := []struct {
		src    string
		want   Kind
		expect string // Expect, for the shapes that carry one
		invoke string
		target string // the module `$name` an action selects, empty for the current instance
	}{
		// The action population — 4876 commands, #157's by-product.
		{
			`(assert_trap (invoke "f") "integer divide by zero")`,
			KindAssertTrapAction, "integer divide by zero", "f", "",
		},
		{
			`(assert_trap (invoke "g" (i32.const 1) (i64.const 2)) "out of bounds memory access")`,
			KindAssertTrapAction, "out of bounds memory access", "g", "",
		},
		// The module population — 0015's Kind. Unchanged by the split, which is the point of
		// including it: the action arm must not shadow it.
		{
			`(assert_trap (module (memory 1) (data (i32.const 0) "x")) "out of bounds memory access")`,
			KindAssertTrapModule, "out of bounds memory access", "", "",
		},
		// The module-named population — 20 commands, and this row was a decline until the
		// registry gave the run loop script state to select an instance with.
		{
			`(assert_trap (invoke $M "f") "unreachable")`,
			KindNamedAssertTrap, "unreachable", "f", "$M",
		},
		// A reference-typed argument, askable since #196/#197 — readConst now names
		// `ref.null func`. Adjacent to the row above so the two rows' distinct histories stay
		// visible (see this function's own doc comment).
		{
			`(assert_trap (invoke "f" (ref.null func)) "unreachable")`,
			KindAssertTrapAction, "unreachable", "f", "",
		},
		// A `get` action is a different action grammar and its own stratum.
		{`(assert_trap (get "g") "unreachable")`, KindUnsupported, "", "", ""},
		// A module form the keyword check declines — the script grammar, not a wat body.
		{`(assert_trap (module definition (memory 1)) "unreachable")`, KindUnsupported, "", "", ""},
		// Arity: the form is `(assert_trap <action> "text")`, and neither a missing text nor
		// a missing action is that.
		{`(assert_trap (invoke "f"))`, KindUnsupported, "", "", ""},
		{`(assert_trap "integer divide by zero")`, KindUnsupported, "", "", ""},
	}
	for _, c := range cases {
		s, err := Parse("t.wast", []byte(c.src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		if len(s.Commands) != 1 {
			t.Fatalf("Parse(%q) gave %d commands, want 1", c.src, len(s.Commands))
		}
		got := s.Commands[0]
		if got.Kind != c.want {
			t.Errorf("%s\n  classified %v, want %v", c.src, got.Kind, c.want)
			continue
		}
		if got.Expect != c.expect {
			t.Errorf("%s\n  Expect = %q, want %q", c.src, got.Expect, c.expect)
		}
		if got.Export != c.invoke {
			t.Errorf("%s\n  Export = %q, want %q", c.src, got.Export, c.invoke)
		}
		// **Target is asserted, not just Kind, because Kind alone cannot fail on it.** A
		// reader that stamped KindNamedAssertTrap and dropped the `$M` would produce the
		// right Kind and run against whatever instance happened to be current — a wrong
		// answer from a right-looking classification, and the exact shape the six-Kind split
		// exists to keep separable. The unnamed rows assert it *empty* for the same reason:
		// a reader that filled Target unconditionally would silently redirect them.
		if got.Target != c.target {
			t.Errorf("%s\n  Target = %q, want %q", c.src, got.Target, c.target)
		}
		// Every admitted shape needs the interpreter, and the declines need nothing: a
		// decline that carried a capability would land in the fourth column, which is the
		// disappearance guard 6 forbids.
		wantNeeds := CapInterpreter
		if c.want == KindUnsupported {
			wantNeeds = CapNone
		}
		if got.Needs != wantNeeds {
			t.Errorf("%s\n  Needs = %q, want %q", c.src, got.Needs, wantNeeds)
		}
	}
}

// TestAssertTrapActionScoring falsifies the run loop's arm in **both directions**, which is
// the condition this Kind landed under: a wrong-text trap must not pass, and — the half no
// suite vector can see — an error that is not a trap must not pass either.
//
// The second half is contract §9 G-3 in miniature. The suite's expected string for one of
// these vectors stops at the trap text, so a harness that scored *any* error quoting that
// text would be green on all 2893 of them; the recon measured 0 such passes in today's
// engine, and this row is what makes that a property of the harness rather than a property
// of one afternoon's engine. The row was watched fail: with `isTrap(err)` removed from the
// pass condition, the `plausible imposter` case scores a pass and this test reports it.
func TestAssertTrapActionScoring(t *testing.T) {
	// A trap and a non-trap, distinguished by type rather than by text — the two error
	// values are deliberately spelled with the *same* text, because a difference in text
	// would let the arm pass this test by sniffing.
	type trapErr struct{ error }
	const text = "out of bounds memory access"
	trap := trapErr{errString(text)}
	imposter := errString(text)

	cases := []struct {
		name string
		err  error
		out  []Val
		pass bool
		key  string
	}{
		{"a real trap with the expected text", trap, nil, true, ""},
		{"a real trap wrapping the expected text", trapErr{errString("trap: " + text + " at 4")}, nil, true, ""},
		{
			// The accept-direction row. Same text, not a trap.
			"a plausible imposter: the right text, the wrong kind of error",
			imposter, nil, false, text,
		},
		{
			"a real trap with the wrong text",
			trapErr{errString("integer divide by zero")},
			nil, false,
			"assert_trap (invoke) expected: " + text + " (trapped with other text)",
		},
		{
			// The semantic disagreement: the engine computed a value where the spec says
			// the program dies.
			"no trap at all", nil,
			[]Val{{Kind: KindI32, Bits: 7}},
			false,
			"assert_trap (invoke) expected: " + text,
		},
	}
	for _, c := range cases {
		src := `(module binary "\00asm\01\00\00\00")` + "\n" +
			`(assert_trap (invoke "f") "` + text + `")`
		s, err := Parse("t.wast", []byte(src))
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		r := s.RunWith(withFact2(Engine{
			Decode:  func([]byte) error { return nil },
			IsGated: func(error) bool { return false },
			// errors.As rather than a type assertion, matching the board's own isTrap
			// (spec_test.go): the fake must discriminate the way the real predicate does,
			// or the row certifies an arm against a predicate no engine will supply.
			IsTrap:      func(e error) bool { var te trapErr; return errors.As(e, &te) },
			Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
			Invoke: func(Instance, string, []Val) ([]Val, error) {
				return c.out, c.err
			},
			Has: []Capability{CapInterpreter},
		}))
		// The module command is one pass in every case; the assertion is the second.
		if c.pass {
			if r.Pass != 2 || r.Fail != 0 {
				t.Errorf("%s: got %d pass / %d fail, want 2/0\n%s", c.name, r.Pass, r.Fail, r.Board())
			}
			continue
		}
		if r.Pass != 1 || r.Fail != 1 {
			t.Errorf("%s: got %d pass / %d fail, want 1/1 — this shape must not score a pass\n%s",
				c.name, r.Pass, r.Fail, r.Board())
			continue
		}
		// The bucket key, asserted rather than only the count: the three failure modes are
		// three different findings and a test that reads only the total cannot tell them
		// apart, which is the `errors.Is`-is-not-a-partition-check lesson (grave #34).
		if len(r.Buckets[c.key]) != 1 {
			t.Errorf("%s: no failure under key %q; got keys %v", c.name, c.key, r.BucketsBySize())
		}
	}
}

// TestAssertTrapActionNeedsATrapPredicate is the third component's tripwire, the same shape
// the other two carry: a caller that declares CapInterpreter and hands over no TrapFunc must
// stop rather than score.
//
// **The panic is the assertion, and the message is asserted with it.** Without the message
// check this would pass on the nil-predicate default silently failing the vector — a green
// from the wrong mechanism, which is exactly how TestQuoteFormsHaveTheirReader's sibling was
// nearly stillborn.
func TestAssertTrapActionNeedsATrapPredicate(t *testing.T) {
	src := `(module binary "\00asm\01\00\00\00")` + "\n" +
		`(assert_trap (invoke "f") "unreachable")`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer func() {
		switch v := recover(); {
		case v == nil:
			t.Error("declaring CapInterpreter with no TrapFunc did not panic; an assert_trap " +
				"action would be judged by a predicate that answers no to everything, which " +
				"fails 4876 vectors the engine can answer and says nothing about why")
		case !strings.Contains(fmt.Sprint(v), "no TrapFunc was supplied"):
			t.Errorf("panic does not name the missing component: %v", v)
		}
	}()
	// **withFact2 is load-bearing for this tripwire, not scaffolding for it.** The setup line is a
	// `(module binary …)`, so as of #353 it needs a validator — and without one the run panics
	// *there*, before the assert_trap arm is reached, leaving this test's `recover` holding a panic
	// about the wrong missing component. That is what happened when #353 landed the arm, and the
	// message check above is the only reason it was a red rather than a green test asserting nothing:
	// the panic arrived, `v != nil` was satisfied, and only *"no TrapFunc was supplied"* separated
	// this tripwire's own subject from an unrelated one. **A panic is not a verdict about which
	// panic** — the sibling of the lesson this test's doc comment already carries.
	_ = s.RunWith(withFact2(Engine{
		Decode:      func([]byte) error { return nil },
		Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
		Invoke:      func(Instance, string, []Val) ([]Val, error) { return nil, nil },
		Has:         []Capability{CapInterpreter},
	}))
}

// TestAssertExceptionClassification is TestAssertTrapSplitsByWrappedForm's own shape for
// assert_exception's narrower grammar (#201 rung 2a) — one fewer field per row, since there
// is no Expect to carry (classify's own doc comment: the grammar has no expected-text
// argument at all, unlike assert_trap's).
func TestAssertExceptionClassification(t *testing.T) {
	cases := []struct {
		src    string
		want   Kind
		invoke string
	}{
		{`(assert_exception (invoke "f"))`, KindAssertException, "f"},
		{`(assert_exception (invoke "g" (i32.const 1) (i64.const 2)))`, KindAssertException, "g"},
		// The module-naming form: declined, per classify's own measurement that zero corpus
		// vectors use it. Structurally indistinguishable from a real decline rather than a
		// found-and-rejected shape, because there is nothing in this proposal's grammar that
		// would admit it even if the corpus wanted it — `namedInvokeAction` is never called
		// from this arm at all, unlike assert_trap's.
		{`(assert_exception (invoke $M "f"))`, KindUnsupported, ""},
		// A `get` action is a different grammar entirely.
		{`(assert_exception (get "g"))`, KindUnsupported, ""},
		// Arity: `(assert_exception <action>)` takes exactly one element after the head, so a
		// bare atom or an extra trailing element are both declined. The trailing-element row
		// is the one that catches a check written as `len(n.list) >= 2` instead of `== 2` —
		// exactly the mutation that slipped through until this row existed, watched fail
		// against the mutation and pass against the correct code.
		{`(assert_exception "f")`, KindUnsupported, ""},
		{`(assert_exception (invoke "f") "text")`, KindUnsupported, ""},
	}
	for _, c := range cases {
		s, err := Parse("t.wast", []byte(c.src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		if len(s.Commands) != 1 {
			t.Fatalf("Parse(%q) gave %d commands, want 1", c.src, len(s.Commands))
		}
		got := s.Commands[0]
		if got.Kind != c.want {
			t.Errorf("%s\n  classified %v, want %v", c.src, got.Kind, c.want)
			continue
		}
		if got.Export != c.invoke {
			t.Errorf("%s\n  Export = %q, want %q", c.src, got.Export, c.invoke)
		}
		wantNeeds := CapInterpreter
		if c.want == KindUnsupported {
			wantNeeds = CapNone
		}
		if got.Needs != wantNeeds {
			t.Errorf("%s\n  Needs = %q, want %q", c.src, got.Needs, wantNeeds)
		}
	}
}

// TestAssertExceptionScoring is TestAssertTrapActionScoring's own shape, one failure mode
// short: there is no expected text to get wrong, so where assert_trap partitions its fails
// into three keys this partitions into two — no exception raised, and a real error that is
// not an exception. Both directions are falsified the same way #157's row was: the accept
// direction (any error scoring as the exception) is what IsException's injection exists to
// close, exactly as IsTrap's does one Kind over.
func TestAssertExceptionScoring(t *testing.T) {
	type excErr struct{ error }
	const text = "some engine-internal detail the vector does not name"
	exc := excErr{errString(text)}
	imposter := errString(text)

	cases := []struct {
		name string
		err  error
		out  []Val
		pass bool
		key  string
	}{
		{"a real exception", exc, nil, true, ""},
		{
			"a plausible imposter: an error, but not an exception",
			imposter, nil, false, text,
		},
		{
			"no exception at all: the call returned values",
			nil,
			[]Val{{Kind: KindI32, Bits: 7}},
			false, "assert_exception",
		},
	}
	for _, c := range cases {
		src := `(module binary "\00asm\01\00\00\00")` + "\n" +
			`(assert_exception (invoke "f"))`
		s, err := Parse("t.wast", []byte(src))
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		r := s.RunWith(withFact2(Engine{
			Decode:  func([]byte) error { return nil },
			IsGated: func(error) bool { return false },
			IsException: func(e error) bool {
				var ee excErr
				return errors.As(e, &ee)
			},
			Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
			Invoke: func(Instance, string, []Val) ([]Val, error) {
				return c.out, c.err
			},
			Has: []Capability{CapInterpreter},
		}))
		if c.pass {
			if r.Pass != 2 || r.Fail != 0 {
				t.Errorf("%s: got %d pass / %d fail, want 2/0\n%s", c.name, r.Pass, r.Fail, r.Board())
			}
			continue
		}
		if r.Pass != 1 || r.Fail != 1 {
			t.Errorf("%s: got %d pass / %d fail, want 1/1 — this shape must not score a pass\n%s",
				c.name, r.Pass, r.Fail, r.Board())
			continue
		}
		if len(r.Buckets[c.key]) != 1 {
			t.Errorf("%s: no failure under key %q; got keys %v", c.name, c.key, r.BucketsBySize())
		}
	}
}

// TestAssertExceptionNeedsAnExceptionPredicate is TestAssertTrapActionNeedsATrapPredicate's
// own shape: a caller that declares CapInterpreter and hands over no ExceptionFunc must stop
// rather than silently fail every assert_exception vector the engine can answer.
func TestAssertExceptionNeedsAnExceptionPredicate(t *testing.T) {
	src := `(module binary "\00asm\01\00\00\00")` + "\n" +
		`(assert_exception (invoke "f"))`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer func() {
		switch v := recover(); {
		case v == nil:
			t.Error("declaring CapInterpreter with no ExceptionFunc did not panic; an " +
				"assert_exception action would be judged by a predicate that answers no to " +
				"everything, which silently fails every vector the engine can answer")
		case !strings.Contains(fmt.Sprint(v), "no ExceptionFunc was supplied"):
			t.Errorf("panic does not name the missing component: %v", v)
		}
	}()
	// withFact2 for the reason its sibling in TestAssertTrapActionNeedsATrapPredicate carries in
	// full: without it the setup module's missing validator panics first, and this `recover` catches
	// a panic about the wrong component while still looking like a pass.
	_ = s.RunWith(withFact2(Engine{
		Decode:      func([]byte) error { return nil },
		Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
		Invoke:      func(Instance, string, []Val) ([]Val, error) { return nil, nil },
		Has:         []Capability{CapInterpreter},
	}))
}

// TestNoReaderLeavesKindAtItsZeroValue is the control the `register` arm's grave names: a reader
// that returns its Command bare, without stamping Kind, scores the vector as `KindModuleBinary`.
//
// **The defect it catches is a zero value that is a valid member.** `Kind` is an iota enum whose
// zero is `KindModuleBinary` rather than an unset marker, so a reader that forgets the stamp
// produces a Command that classifies, runs, and fails in the *decoder* — 78 registers reported as
// `(module binary ...) must decode` / `unexpected end`, an error naming a layer the input never
// reached. That is the wrong-layer tell, and no board number distinguishes it from a real decoder
// regression: the count and the bucket key both look like a decoder frontier.
//
// # Scoped to every classified Kind, not to the reader that had the bug
//
// A control scoped to today's sample inherits today's blind spot, and the sample here is
// "whichever arm was written last". So this asserts over the whole corpus and over the whole
// enum: **no command whose head is not `module` may carry `KindModuleBinary`**, which is the
// property the missed stamp violates for every arm at once, present and future. The complement
// is asserted with it — the commands that *are* binary module forms all carry a non-empty image —
// because a Kind stamped right and a payload read right are two facts, and the zero-value defect
// produces the first without the second.
//
// Falsified by removing the stamp from the `register` arm (`c.Kind, c.Line, c.Head = …` → `c.Line,
// c.Head = …`): 78 rows report `head "register" carries KindModuleBinary`, naming the file and
// line rather than the decoder.
func TestNoReaderLeavesKindAtItsZeroValue(t *testing.T) {
	requireSuite(t)

	// The heads a `KindModuleBinary` may legitimately have. One entry, and it is a set rather
	// than an equality test because `classify` keys on the head atom: a second module-form head
	// added upstream should widen this deliberately rather than fail mysteriously.
	binaryHeads := map[string]bool{"module": true}

	classified, binaries := 0, 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Kind == KindUnsupported {
				continue
			}
			classified++
			if c.Kind != KindModuleBinary {
				continue
			}
			binaries++
			if !binaryHeads[c.Head] {
				t.Errorf("%s:%d: head %q carries KindModuleBinary, which is Kind's *zero value* —\n"+
					"\tthe reader for this head returned its Command without stamping Kind, so the vector\n"+
					"\tis about to be scored as a binary module and fail in the decoder (see the register arm)",
					f, c.Line, c.Head)
			}
			// The payload half: a Kind stamped right with nothing read is the same defect one
			// field over, and a missing stamp produces exactly that — an image-less binary form.
			if len(c.Module) == 0 {
				t.Errorf("%s:%d: KindModuleBinary with an empty image; the head is %q",
					f, c.Line, c.Head)
			}
		}
	}
	// The vacuity guard, and it is two floors rather than one total: a corpus that parsed to
	// nothing would agree with every assertion above, and so would one whose binary forms all
	// vanished while the other Kinds stayed. Sized from the measured census (37965 classified,
	// 88 binary module forms) with room to move. Watched fire by ranging over
	// `boardFiles(t)[:0]`, which reports `0 classified commands and 0 binary module forms` —
	// breaking the assertion above could never have found this, which is the whole reason the
	// floors are here rather than a non-nil check.
	if classified < 30000 || binaries < 80 {
		t.Errorf("the loop saw %d classified commands and %d binary module forms, want >=30000 and >=80;\n"+
			"\ta run over an empty or truncated corpus agrees with every assertion above",
			classified, binaries)
	}
	t.Logf("%d classified commands, %d binary module forms, all under head %v", classified, binaries, "module")
}

// TestSpectestExportsEveryNameTheCorpusAsksFor is the floor the `spectestFields` comment names,
// and its subject is a *count* rather than an error.
//
// **The defect it exists for was met: a 13-export first draft passed every board vector.** The
// missing name was `table64`, imported at exactly one site — `table64.wast:13` — so the absence
// was worth one vector, and one vector is inside the noise of any board line. `err == nil` is not
// the assertion, because the fixture instantiating is not the claim; the claim is that the 174
// import sites in the corpus have something to resolve against.
//
// # 14 or 13, per gate state, both pinned
//
// The export set is gate-dependent, which is the one thing about this fixture a reader will not
// guess: `table64`'s `i64` index type *is* the memory64 proposal, so on the default board the
// fixture's own source does not decode. Both branches are asserted, because the difference between
// a partition and a fallback-with-better-manners is whether anyone checked:
//
//   - with memory64 on, 14 exports, `table64` among them;
//   - with it off, 13, and `table64` absent.
//
// **And the absence is asserted to be unobservable**, which is what makes 13 honest rather than a
// quiet loss: the corpus asks for `spectest.table64` at one site whose *own* module declares an
// i64 table, so it is declined at its own decode on the same gate. The export exists exactly when
// something can ask for it. That is measured here rather than argued — the site is looked up in
// the corpus and its module checked — because an unobservability nobody re-measured is a claim.
//
// # The names come from the corpus, not from a list
//
// Derive the domain, never enumerate it: the wanted set is read out of every `(import "spectest"
// "name" …)` in the vendored suite, so a fifteenth name added upstream fails this test instead of
// being silently unresolvable. The one name deliberately *not* supplied is `unknown`, asked for at
// 5 sites, all `assert_unlinkable` vectors expecting `unknown import` — exporting it would convert
// five passes into five failures, so it is excluded here with that reason rather than by omission.
//
// Falsified three ways, each watched fail:
//
//   - deleting `(table (export "table64") i64 10 20 funcref)` from `spectestTable64Field`:
//     `memory64 on: 13 exports, want 14` and `table64 is missing`;
//   - deleting `(func (export "print_i32_f32") (param i32 f32))` from `spectestFields`:
//     both branches report the count *and* `print_i32_f32 is asked for at 3 sites and not
//     exported`, which is the corpus-derived half doing the work a count alone cannot;
//   - adding `(func (export "unknown"))`: `unknown is exported, which converts 5
//     assert_unlinkable passes into failures`.
func TestSpectestExportsEveryNameTheCorpusAsksFor(t *testing.T) {
	requireSuite(t)

	// # What the corpus asks for, read through the package's own parser
	//
	// Through `newParser` rather than a grep, because the question is which imports *exist* and
	// that is a grammar question — a regexp measures text, the reader measures nodes. Every
	// `(import "spectest" "n" …)` at any depth, since the form sits inside module bodies.
	//
	// The population comes from `suitePaths` — one definition of which files are vectors (#340),
	// and it carries the vacuity floor this test's own glob did not have.
	wanted := map[string]int{}
	paths := suitePaths(t)
	table64Sites := []string{}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		nodes, err := newParser(src).parseAll()
		if err != nil {
			// A file this package cannot parse asks for nothing measurable, and it is a
			// different test's subject. Logged rather than skipped silently.
			t.Logf("%s: parse: %v (contributes no import sites)", filepath.Base(p), err)
			continue
		}
		for _, top := range nodes {
			forEachNode(top, func(n node) {
				if n.head() != "import" || len(n.list) < 3 || !n.list[1].isS || !n.list[2].isS {
					return
				}
				if string(n.list[1].str) != "spectest" {
					return
				}
				name := string(n.list[2].str)
				wanted[name]++
				if name == "table64" {
					table64Sites = append(table64Sites, fmt.Sprintf("%s:%d", filepath.Base(p), n.line))
				}
			})
		}
	}
	// The vacuity guard on the *derivation*: an empty wanted set would make every assertion
	// below agree with an empty fixture. Sized from the measured census — 174 sites over 15
	// names — and asserted on both, because one name asked for 174 times would satisfy a total.
	// Watched fire by ranging over `paths[:0]`: `0 spectest imports over 0 names`.
	sites := 0
	for _, n := range wanted {
		sites += n
	}
	if sites < 170 || len(wanted) < 15 {
		t.Fatalf("the corpus asks for %d spectest imports over %d names, want >=170 and >=15;\n"+
			"\tan empty derivation agrees with any fixture", sites, len(wanted))
	}

	// `unknown` is the one name whose absence is load-bearing. Excluded here with its reason, so
	// the loop below does not demand it and a future reader does not helpfully add it.
	const notSupplied = "unknown"
	if wanted[notSupplied] == 0 {
		t.Errorf("no vector imports spectest.%s any more; the deliberate omission in spectestFields "+
			"has lost its subject and should be re-pointed or dropped", notSupplied)
	}

	for _, lane := range []struct {
		what        string
		feat        binary.Features
		wantExports int
		wantTable64 bool
	}{
		{"memory64 on", allFeaturesOn(t), 14, true},
		{"memory64 off", binary.Features{}, 13, false},
	} {
		t.Run(lane.what, func(t *testing.T) {
			// The source is composed by the same function the registry uses, and which branch to
			// ask for is read off the *engine's* answer the way `spectestRegistry` reads it: try
			// 14, fall back to 13 on a decline. Hardcoding `spectestSource(lane.wantTable64)`
			// would assert the fixture against this test's own belief about the gate rather than
			// against the decoder's, which is the echo shape (grave #106).
			m, exports := decodeSpectest(t, lane.feat, true)
			if m == nil {
				m, exports = decodeSpectest(t, lane.feat, false)
			}
			if m == nil {
				t.Fatalf("neither the 14- nor the 13-export fixture decodes under %s", lane.what)
			}
			if exports != lane.wantExports {
				t.Errorf("%s: %d exports, want %d — spectestFields lost or gained a name, and a "+
					"missing one is worth as little as a single vector (table64's is worth exactly one)",
					lane.what, exports, lane.wantExports)
			}
			have := map[string]bool{}
			for _, e := range m.Exports {
				have[e.Name] = true
			}
			if have["table64"] != lane.wantTable64 {
				t.Errorf("%s: table64 exported = %v, want %v", lane.what, have["table64"], lane.wantTable64)
			}
			if have[notSupplied] {
				t.Errorf("%s: %s is exported, which converts %d assert_unlinkable passes into failures — "+
					"those vectors expect `unknown import`", lane.what, notSupplied, wanted[notSupplied])
			}
			// Every name the corpus asks for, except the two whose status is stated above.
			for name, n := range wanted {
				if name == notSupplied || name == "table64" {
					continue
				}
				if !have[name] {
					t.Errorf("%s: %s is asked for at %d sites and not exported; those imports resolve "+
						"against nothing", lane.what, name, n)
				}
			}
		})
	}

	// # The unobservability of the 13-export branch, measured
	//
	// The claim is that dropping `table64` costs nothing because the only vector that asks for it
	// is itself declined on the same gate. Checked rather than asserted: the site is located above
	// from the corpus, and its module must fail to decode with memory64 off.
	if len(table64Sites) != 1 {
		t.Errorf("spectest.table64 is imported at %d sites (%v), want 1; the 13-export branch's "+
			"unobservability argument was measured against one site and does not survive a second",
			len(table64Sites), table64Sites)
	}
	for _, f := range boardFiles(t) {
		if f != "table64.wast" {
			continue
		}
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Fatalf("%s: parse: %v", f, err)
		}
		found := false
		for _, c := range s.Commands {
			if c.Kind != KindModuleText || c.Line != 13 {
				continue
			}
			found = true
			// Through both front-end steps the board takes, since either could be the one that
			// declines and only the *pair* answers "can this vector ask for table64". The
			// encode failing is itself a decline for this purpose, which is why it is not a
			// fatal: a vector that cannot be emitted cannot import anything either.
			img, eerr := text.EncodeModule(c.Source)
			if eerr != nil {
				t.Logf("%s:%d does not encode (%v), so it cannot ask for spectest.table64 either", f, c.Line, eerr)
				continue
			}
			if _, derr := (&binary.Decoder{}).DecodeModule(img); derr == nil {
				t.Errorf("%s:%d decodes with memory64 off, so it *can* ask for spectest.table64 "+
					"while the 13-export fixture does not supply it — the export's absence is "+
					"observable and the branch is a silent loss rather than a partition", f, c.Line)
			}
		}
		if !found {
			t.Errorf("%s has no module command at line 13; the site this argument rests on has moved, "+
				"so the unobservability is unmeasured rather than false", f)
		}
	}
}

// decodeSpectest decodes one branch of the fixture under the given features, reporting a nil
// module when the branch is declined. Split out so the two lanes ask identically.
func decodeSpectest(t *testing.T, f binary.Features, withTable64 bool) (*binary.Module, int) {
	t.Helper()
	img, err := text.EncodeModule([]byte(spectestSource(withTable64)))
	if err != nil {
		return nil, 0
	}
	m, err := (&binary.Decoder{Features: f}).DecodeModule(img)
	if err != nil {
		return nil, 0
	}
	return m, len(m.Exports)
}

// forEachNode visits n and every node below it.
//
// Here rather than in sexpr.go because it exists for controls that census the corpus, and a
// traversal the run loop does not need is not the reader's API. Named for what it does rather
// than for the one caller, since the *next* census is the reason it is a function.
func forEachNode(n node, f func(node)) {
	f(n)
	for _, c := range n.list {
		forEachNode(c, f)
	}
}

// registryProbe is a stub engine that records the registry each instantiate call received.
//
// **The registry is state, and state is invisible to a board number** — which is the whole
// reason the controls below use a probe rather than a count. A script's registry is built
// command by command and read by whatever imports later; a run that scores 5/0 says nothing
// about *which name was bound to which instance when*, and every registry defect worth having
// a control for lives in that gap. So the probe records the map as it was at each call, and the
// assertions are about the sequence.
//
// Instances are strings, since `Instance` is `any` and a name is what the assertions compare.
type registryProbe struct {
	// seen is one entry per instantiate call: the line, and the registry's keys at that moment.
	seen []probeCall
	// fail names module sources that must not produce an instance, so a register whose target
	// failed can be exercised without needing a module the real engine rejects.
	fail map[string]error
}

type probeCall struct {
	line int
	keys []string
	// mods is the registry's *contents*, name → instance, because a key present with the wrong
	// instance behind it is a different defect from a key absent and the two must not share an
	// assertion (grave #34: errors.Is is not a partition check).
	mods map[string]string
}

func (p *registryProbe) engine() Engine {
	return Engine{
		Decode:     func([]byte) error { return nil },
		ReadText:   func([]byte) error { return nil },
		Validate:   stubValidate,
		IsDeclined: stubDeclined,
		IsGated:    func(error) bool { return false },
		IsTrap:     func(error) bool { return false },
		InstantiateLinked: func(c Command, reg Registry) (Instance, Stratum, error) {
			keys := make([]string, 0, len(reg.Instances))
			mods := map[string]string{}
			for k, v := range reg.Instances {
				keys = append(keys, k)
				mods[k], _ = v.(string)
			}
			slices.Sort(keys)
			p.seen = append(p.seen, probeCall{line: c.Line, keys: keys, mods: mods})
			if err := p.fail[string(c.Source)]; err != nil {
				return nil, StratumExec, err
			}
			// The instance *is* the module's own source, so an assertion can say which module
			// a name resolved to rather than only that something was there.
			return string(c.Source), StratumUnset, nil
		},
		Invoke: func(Instance, string, []Val) ([]Val, error) { return nil, nil },
		Has:    []Capability{CapWatReader, CapInterpreter},
	}
}

// scriptCalls returns the probe's calls with the `spectest` bootstrap dropped, and asserts the
// bootstrap's own property on the way past.
//
// **The bootstrap is a call on this path, not beside it** — 0017 part 3 synthesizes `spectest`
// as wat and instantiates it through the same door every vector's module takes, precisely so the
// front end sees it. So a probe over any script records `len(script modules) + 1` calls, and
// this is where that surprise is stated once rather than being absorbed by an off-by-one in
// three tests. Found by writing an accumulation test that expected 3 and got 4.
//
// The bootstrap is identified by its **empty registry** rather than by being first, because
// position is a coincidence and the empty map is the property `spectestRegistry` documents: it
// passes `map[string]Instance{}` rather than `reg` itself, so a typo cannot make the fixture
// import from the map it is about to be written into.
//
// **That assertion needs the cycle's other half to fire, which is worth stating rather than
// leaving as an apparent falsification.** Changing `o.instantiate(c, map[string]Instance{})` to
// `o.instantiate(c, reg)` alone does *not* fail this — `reg` is empty at that moment, so the two
// spellings are indistinguishable, and the mutation is honest about the code being currently
// equivalent. It fires when `reg` is also seeded (`{"spectest": "placeholder"}`), reporting
// `registry [spectest], want empty`. So this row guards the *pair*: it catches the day the map
// stops being empty at the call, which is the only day the cycle is reachable. Recorded because
// a mutation that passes is a fact about the control, and hiding it would leave a reader
// believing a single-line change is covered when it is the two together that are.
func (p *registryProbe) scriptCalls(t *testing.T) []probeCall {
	t.Helper()
	if len(p.seen) == 0 {
		t.Fatal("the probe recorded no instantiate calls at all; the run loop never reached a " +
			"module command, so every assertion below would agree with an empty script")
	}
	boot, rest := p.seen[0], p.seen[1:]
	if len(boot.keys) != 0 {
		t.Errorf("the spectest bootstrap was instantiated with registry %v, want empty: it is "+
			"built with a fresh map rather than the one it is written into, so a typo in its own "+
			"source cannot make it import from itself", boot.keys)
	}
	for _, c := range rest {
		if len(c.keys) == 0 {
			t.Errorf("a script module at line %d was instantiated with an empty registry; only the "+
				"spectest bootstrap may be, and it is the first call", c.line)
		}
	}
	return rest
}

// lastCall returns the registry as it stood at the final instantiate, which is what every
// "was the name bound by then" question is really asking.
func (p *registryProbe) lastCall(t *testing.T) probeCall {
	t.Helper()
	calls := p.scriptCalls(t)
	if len(calls) == 0 {
		t.Fatal("the probe recorded no script module commands, only the spectest bootstrap")
	}
	return calls[len(calls)-1]
}

// TestRegisterBindsUnderItsStringNameNotItsScriptName is the registry's central control, and its
// subject is the **two namespaces** decision 0017 part 2 separates.
//
// `(register "a" $M)` binds the *string* `"a"`, and `(invoke $M …)` names the *identifier* `$M`.
// One module can carry either, both, or neither, so merging the maps would make the two forms
// name the same thing — and the second form does not exist. A single-map harness passes every
// vector in which a module's `$name` and its registered string happen to be spelled alike, which
// is most of `linking.wast`, so this is an accept-direction defect that the corpus scores green
// by coincidence (§9 G-3).
//
// **Deliberately spelled apart in the fixture**: `$M` registers as `"other"`, so a harness
// keying the registry on `Name` binds `"M"` and the importer finds nothing. A fixture whose two
// names agree cannot fail this test, which is the shape-of-what-survives tell.
//
// Falsified by running both mutations, and **the numbers are quoted from the runs because the
// first prediction written here was wrong in both of them**:
//
//   - `registry[c.Register] = in` → `registry[c.Name] = in` reports the registry as
//     `[ spectest]` — the *empty string*, not `[M spectest]` as predicted, because a
//     `(register …)` command carries no `Name` at all: `Name` is a module command's field. So
//     the mutation binds `""` and every import of `"other"` misses. Both the key row and the
//     `mods` row fire.
//   - `registerTarget`'s `named[c.Target]` → `cur` **passed on the first attempt**, which is the
//     stillbirth the decoy above exists for: with `$M` immediately preceding its own register,
//     the two readings coincide. With the decoy between them the row reports
//     `registry["other"] is "(module $Decoy …)"` — and it is the `mods` assertion that catches
//     it, the keys being identical either way, which is why contents are asserted separately.
//
// `Bound` is asserted here but falsified elsewhere: dropping `r.Bound++` is
// TestVerdictsPartitionCommands' finding, since a register accounted nowhere breaks the
// partition, and that control already exists.
func TestRegisterBindsUnderItsStringNameNotItsScriptName(t *testing.T) {
	// **A decoy module stands between `$M` and its register, and it is load-bearing.** Without
	// it `cur` and `named["$M"]` are the same instance, so a harness reading the wrong one of
	// the two agrees on every assertion — measured, not supposed: the `registerTarget` mutation
	// below *passed* against a three-line fixture, which is a stillborn control, and the decoy
	// is what separates the two readings.
	const src = `(module $M (memory (export "m") 1))
(module $Decoy (memory (export "m") 3))
(register "other" $M)
(module (import "other" "m" (memory 1)))`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := &registryProbe{}
	r := s.RunWith(p.engine())
	if r.Bound != 1 {
		t.Errorf("Bound = %d, want 1: the register bound no name, so the importer below "+
			"resolved against an empty registry\n%s", r.Bound, r.Board())
	}
	// The importer's call is the one that matters: it is the only one that reads the binding.
	last := p.lastCall(t)
	if !slices.Contains(last.keys, "other") {
		t.Errorf("at the importer (line %d) the registry holds %v, want key %q — the register "+
			"bound under the module's script name instead of its export-name string, so every "+
			"import of %q resolves against nothing",
			last.line, last.keys, "other", "other")
	}
	if slices.Contains(last.keys, "M") {
		t.Errorf("the registry holds key %q: the script identifier leaked into the export-name "+
			"namespace, which makes (invoke $M …) and (invoke \"M\" …) name one thing", "M")
	}
	// The *contents*, not only the key: a register that bound the right name to the wrong
	// instance is a separate defect and a keys-only assertion cannot see it.
	if got := last.mods["other"]; !strings.Contains(got, "$M") {
		t.Errorf("registry[%q] is %q, want the source of $M — the register bound a name to some "+
			"other module's instance, so imports resolve to the wrong exports", "other", got)
	}
}

// TestRegisterWhoseModuleFailedBindsNothing pins the arm's failure path, and the property is an
// **absence**: a name whose module produced no instance must not appear in the registry at all.
//
// The alternative — binding the name to a nil instance — is what makes this worth a control
// rather than a comment. A nil in the map satisfies every `ok` from a map read, so the importer
// downstream gets `(nil, true)`, and the engine is handed an instance that is not one. That is
// the nil-instance-with-nil-error normalization at `instantiateRaw`, one layer out, and the two
// are separate because the map read is a different door.
//
// The register is also scored `Fail` rather than passed over, and both halves are asserted: the
// count says the harness noticed, the key says it named the register rather than blaming the
// import cluster that follows. *An error from the wrong layer is evidence about where structure
// was lost* — a silent register produces a cluster of `unknown import`s naming the linker.
//
// Falsified two ways:
//
//   - making the arm bind unconditionally (`registry[c.Register] = in` ahead of the `ok` check):
//     the registry holds `broken` and this reports it, where `Bound` alone stays 0 and says
//     nothing, since the failing path does not increment it either way.
//   - dropping the `r.Fail++` and its bucket: `Fail = 0, want 1` plus the missing key, and
//     TestVerdictsPartitionCommands fails alongside — the register would be accounted nowhere.
func TestRegisterWhoseModuleFailedBindsNothing(t *testing.T) {
	const bad = `(module $B (memory 1))`
	src := bad + "\n" + `(register "broken" $B)` + "\n" +
		`(module (import "broken" "m" (memory 1)))`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := &registryProbe{fail: map[string]error{bad: errString("this module was refused")}}
	r := s.RunWith(p.engine())
	if r.Bound != 0 {
		t.Errorf("Bound = %d, want 0: a register whose module failed counted as a binding", r.Bound)
	}
	if r.Fail != 1 {
		t.Errorf("Fail = %d, want 1 — the failing register, charged to itself\n%s", r.Fail, r.Board())
	}
	// Keyed by the register rather than by the import cluster it causes.
	const key = "register: this module was refused"
	if len(r.Buckets[key]) != 1 {
		t.Errorf("no failure under %q; got keys %v — the register failed without naming itself, "+
			"so the cost lands on whichever import resolves against the missing name next",
			key, r.BucketsBySize())
	}
	last := p.lastCall(t)
	if slices.Contains(last.keys, "broken") {
		t.Errorf("the registry holds %q after its module failed (keys %v); a name bound to a nil "+
			"instance reads as bound to every map lookup, so the importer is handed a non-instance "+
			"instead of reporting an unknown import", "broken", last.keys)
	}
}

// TestRegistryAccumulatesAcrossAScript pins the property `linking.wast` depends on and no single
// register can show: the registry a module is instantiated with holds **every name bound before
// it**, and nothing bound after.
//
// **Both directions, because a wrong one is plausible in each.** A harness that rebuilt the
// registry per command would hand the third module only the third name; one that pre-scanned the
// script would hand the first module a name registered below it, which is worse — it would score
// a forward reference as linkable, and the suite has vectors asserting that a module cannot
// import from a module registered later. The corpus registers as it goes (`linking.wast`), so
// only the *sequence* separates the two readings.
//
// Falsified by resetting the registry to `{"spectest": boot}` at the top of the run loop — the
// forget-earlier-bindings mutation — which reports both later rows: `at line 3 the registry holds
// [spectest], want [a spectest]` and the same at line 5. Note which mutation was *not* used:
// re-calling `opts.spectestRegistry` per command also fails, but it re-instantiates the fixture
// each time and so fails on the call *count* and on scriptCalls' bootstrap row instead — a red
// that names a different property than the one under test, which is no falsification at all.
func TestRegistryAccumulatesAcrossAScript(t *testing.T) {
	const src = `(module $A (memory (export "m") 1))
(register "a" $A)
(module $B (memory (export "m") 1))
(register "b" $B)
(module (import "a" "m" (memory 1)) (import "b" "m" (memory 1)))`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := &registryProbe{}
	if r := s.RunWith(p.engine()); r.Bound != 2 {
		t.Fatalf("Bound = %d, want 2\n%s", r.Bound, r.Board())
	}
	// Three module commands, and each one's registry is asserted — the middle one is the row
	// that separates accumulation from a pre-scan. `spectest` is in all three, bound before the
	// loop starts (0017 part 3); its own instantiate call is dropped and checked by scriptCalls.
	want := [][]string{
		{"spectest"},           // $A: nothing registered yet
		{"a", "spectest"},      // $B: only $A's name, never its own or a later one
		{"a", "b", "spectest"}, // the importer: both
	}
	calls := p.scriptCalls(t)
	if len(calls) != len(want) {
		t.Fatalf("the probe recorded %d script instantiate calls, want %d: %v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if !slices.Equal(calls[i].keys, w) {
			t.Errorf("at line %d the registry holds %v, want %v — a registry rebuilt per command "+
				"loses earlier names, and one pre-scanned from the whole script would let a module "+
				"import from a module registered below it",
				calls[i].line, calls[i].keys, w)
		}
	}
}

// TestNamedActionRunsAgainstItsTargetNotTheCurrentInstance is the named Kinds' control, and its
// property is the one thing the three of them added to the shared arm: **which instance**.
//
// A harness that stamped the Kind and ignored `Target` runs the action against `cur`, which is a
// wrong answer wearing a right classification. It is an accept-direction defect (§9 G-3) in the
// most literal way available: the vectors it breaks are `assert_return`s, so the suite scores it
// green whenever the named module and the current one happen to agree on the value — which is
// most of `linking.wast`, where the modules are near-copies of each other.
//
// **The fixture makes the two disagree by construction.** `$A` and `$B` export the same name and
// return different values, `$B` is current, and the action names `$A`. So an ignored `Target`
// answers 2 where the vector wants 1, and this control is exactly the difference between the two
// instances. A fixture whose modules agree cannot fail it.
//
// All three named Kinds are covered, since they reach the shared arm by three classify paths and
// share nothing before it — `assert_return` and `assert_trap` differ in whether an error is the
// answer, and a bare `invoke` in having no expectation at all, so getting one right says nothing
// about the others (the partition rule, grave #34).
//
// Falsified by neutering the selection — `if c.Kind.selectsModule()` → `if false` — which reports
// `3 pass / 2 fail` and names all three commands. Quoted from the run: the assert_return lands in
// `assert_return value mismatch` as `i32 1` wanted against `i32 2` got, the assert_trap in
// `assert_trap (invoke) expected: answered by $A`, and **the bare invoke is caught only by the
// probe** — it still scores a pass, having no expectation to disappoint, and the three `call N ran
// against "(module $B …)"` lines are the whole of its evidence. That is why `invoked` is asserted
// beside the count rather than instead of it: a Kind whose vectors assert nothing needs a witness
// that is not the board.
func TestNamedActionRunsAgainstItsTargetNotTheCurrentInstance(t *testing.T) {
	// A stub engine whose Invoke answers from the *instance*, which is what makes the target
	// observable: the instance is its module's source, so the value depends on which one was
	// selected. `$A` answers 1, `$B` answers 2, and `$B` is the current module.
	const src = `(module $A (func (export "f") (result i32) (i32.const 1)))
(module $B (func (export "f") (result i32) (i32.const 2)))
(assert_return (invoke $A "f") (i32.const 1))
(assert_trap (invoke $A "f") "answered by $A")
(invoke $A "f")`
	s, err := Parse("t.wast", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Kinds asserted before the run: a fixture misclassified into the *unnamed* Kinds would
	// select `cur` legitimately and score a green that says nothing.
	wantKinds := []Kind{
		KindModuleText, KindModuleText,
		KindNamedAssertReturn, KindNamedAssertTrap, KindNamedInvoke,
	}
	if len(s.Commands) != len(wantKinds) {
		t.Fatalf("got %d commands, want %d", len(s.Commands), len(wantKinds))
	}
	for i, k := range wantKinds {
		if s.Commands[i].Kind != k {
			t.Fatalf("command %d is %v, want %v — the fixture is not exercising the named arm",
				i, s.Commands[i].Kind, k)
		}
	}
	// invoked records which instance each call ran against, so the bare `(invoke $A …)` — which
	// asserts nothing and so cannot fail on a value — is still covered.
	var invoked []string
	answer := func(in Instance, _ string, _ []Val) ([]Val, error) {
		src, _ := in.(string)
		invoked = append(invoked, src)
		if strings.Contains(src, "$A") {
			return []Val{{Kind: KindI32, Bits: 1}}, nil
		}
		return []Val{{Kind: KindI32, Bits: 2}}, nil
	}
	p := &registryProbe{}
	e := p.engine()
	e.Invoke = answer
	// The assert_trap row wants an error, and the trap's text names the answering instance so a
	// wrong selection is a wrong *string* rather than only a wrong count.
	e.IsTrap = func(error) bool { return true }
	r := s.RunWith(e)
	// 2 modules + 3 actions. The assert_trap is the one deliberate fail: this stub does not trap,
	// so the vector cannot pass, and its *bucket* is what carries the finding.
	if r.Pass != 4 || r.Fail != 1 {
		t.Errorf("got %d pass / %d fail, want 4/1\n%s", r.Pass, r.Fail, r.Board())
	}
	if len(r.Buckets["assert_return value mismatch"]) != 0 {
		t.Errorf("the named assert_return answered the wrong value: %v — the action ran against "+
			"the current instance ($B) rather than its target ($A)",
			r.Buckets["assert_return value mismatch"])
	}
	// Every call ran against $A, which is the property stated directly rather than inferred from
	// the pass count: the bare invoke has no expectation, so nothing else can see its target.
	if len(invoked) != 3 {
		t.Fatalf("Invoke was called %d times, want 3: %v", len(invoked), invoked)
	}
	for i, got := range invoked {
		if !strings.Contains(got, "$A") {
			t.Errorf("call %d ran against %q, want $A's instance — a named action that ignores "+
				"Target runs against whatever module came last, which is a wrong answer from a "+
				"right-looking classification", i, got)
		}
	}
}

// TestAssertUnlinkableNeedsTheLinkerAndScoresBothWays is the unlinkable arm's control, and the
// two halves are separate defects.
//
// **The reject half is oracle-covered and the accept half is not.** 200 vectors assert that a
// module fails to link with a named string, so a harness that scored the *wrong* text as a pass
// would be caught by the board — but a harness that scored a module which linked *successfully*
// as a pass would not be, because no vector in the corpus is a successfully-linking module under
// `assert_unlinkable`. That is §9 G-3, and it is the row worth the fixture.
//
// **The panic is the third assertion, and it is the asymmetry the arm defends at its site.** Every
// other arm falls back to the unlinked entry point when there is no linker; this one cannot, since
// instantiating an unlinkable module *without* its imports fails for a different reason and its
// text could coincide with the expectation — 200 passes nobody earned. So a caller declaring
// CapInterpreter with a nil InstantiateLinked must stop rather than score, and the panic message
// is asserted with the panic: without the message check this would pass on any panic at all,
// which is how TestQuoteFormsHaveTheirReader's sibling was nearly stillborn.
//
// Falsified by running mutations, and **one of the four was a no-op, which is the finding worth
// keeping**: dropping the `err != nil` conjunct from `err != nil && strings.Contains(got,
// c.Expect)` changes nothing, because `got` is `""` exactly when `err` is nil and
// `strings.Contains("", "unknown import")` is already false. The conjunct is redundant for every
// vector in the corpus — all 200 expect a non-empty string — so it is *defensive against an empty
// `Expect`*, not the thing that rejects a successful link. Recorded because a comment claiming
// that mutation falsifies this row would be a citation to a run that says otherwise, and because
// a reader deleting the conjunct as dead weight should find out here that the board will not tell
// them. The three that do fire:
//
//   - `err != nil &&` → `err == nil ||`: the accept row reports `1 pass / 0 fail` and no bucket.
//     This is the accept-direction assertion, and the mutation is the shape a harness written
//     "an assertion about linking, so a clean link is the interesting case" would have.
//   - the whole conjunction → `err != nil`: the wrong-text row reports `1 pass / 0 fail`.
//   - removing the nil-linker panic: the fourth subtest reports no panic.
func TestAssertUnlinkableNeedsTheLinkerAndScoresBothWays(t *testing.T) {
	const mod = `(module (import "m" "f" (func)))`
	for _, c := range []struct {
		name string
		err  error
		pass bool
	}{
		{"the expected link failure", errString("unknown import: \"m\" \"f\""), true},
		{"a link failure with the wrong text", errString("incompatible import type"), false},
		// The accept-direction row: nothing in the corpus can produce it, so nothing but this
		// fixture will ever notice a harness that scores it green.
		{"the module linked successfully", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := Parse("t.wast", []byte(`(assert_unlinkable `+mod+` "unknown import")`))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := s.Commands[0].Kind; got != KindAssertUnlinkable {
				t.Fatalf("classified %v, want KindAssertUnlinkable", got)
			}
			r := s.RunWith(Engine{
				Decode:     func([]byte) error { return nil },
				ReadText:   func([]byte) error { return nil },
				Validate:   stubValidate,
				IsDeclined: stubDeclined,
				IsGated:    func(error) bool { return false },
				IsTrap:     func(error) bool { return false },
				InstantiateLinked: func(cmd Command, _ Registry) (Instance, Stratum, error) {
					// **The spectest bootstrap comes through this same door** (0017 part 3), and a
					// stub that failed every call would panic in spectestRegistry before the vector
					// was ever judged — which is how this row was first written, and the panic is
					// the harness protecting its own fixture exactly as documented. Discriminated
					// on the *source* rather than on call order, since a positional stub would
					// invert silently the day the bootstrap moves.
					if !bytes.Contains(cmd.Source, []byte(`"m" "f"`)) {
						return "spectest", StratumUnset, nil
					}
					if c.err != nil {
						return nil, StratumExec, c.err
					}
					return "linked", StratumUnset, nil
				},
				Invoke: func(Instance, string, []Val) ([]Val, error) { return nil, nil },
				Has:    []Capability{CapWatReader, CapInterpreter},
			})
			wantPass, wantFail := 0, 1
			if c.pass {
				wantPass, wantFail = 1, 0
			}
			if r.Pass != wantPass || r.Fail != wantFail {
				t.Errorf("got %d pass / %d fail, want %d/%d\n%s",
					r.Pass, r.Fail, wantPass, wantFail, r.Board())
			}
			if c.pass {
				return
			}
			// The bucket, because the two failing rows are two findings and a count cannot
			// tell them apart. The successful-link row's Got is asserted specifically: a
			// harness reporting an empty Got here would say "expected unknown import, got
			// nothing", which reads as a missing error rather than as a module that linked.
			const key = "assert_unlinkable expected: unknown import"
			b := r.Buckets[key]
			if len(b) != 1 {
				t.Fatalf("no failure under %q; got keys %v", key, r.BucketsBySize())
			}
			wantGot := "incompatible import type"
			if c.err == nil {
				wantGot = "the module linked and instantiated successfully"
			}
			if b[0].Got != wantGot {
				t.Errorf("Got = %q, want %q", b[0].Got, wantGot)
			}
		})
	}
	t.Run("a nil linker panics rather than scoring", func(t *testing.T) {
		s, err := Parse("t.wast", []byte(`(assert_unlinkable `+mod+` "unknown import")`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		defer func() {
			switch v := recover(); {
			case v == nil:
				t.Error("declaring CapInterpreter with no LinkedInstantiateFunc did not panic; the " +
					"200 unlinkable vectors would be judged through the unlinked path, where a §3 " +
					"refusal's text can coincide with the expectation and award a pass")
			case !strings.Contains(fmt.Sprint(v), "no LinkedInstantiateFunc"):
				t.Errorf("panic does not name the missing component: %v", v)
			}
		}()
		_ = s.RunWith(Engine{
			Decode:      func([]byte) error { return nil },
			ReadText:    func([]byte) error { return nil },
			Validate:    stubValidate,
			IsDeclined:  stubDeclined,
			IsGated:     func(error) bool { return false },
			IsTrap:      func(error) bool { return false },
			Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
			Invoke:      func(Instance, string, []Val) ([]Val, error) { return nil, nil },
			Has:         []Capability{CapWatReader, CapInterpreter},
		})
	})
}
