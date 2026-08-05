package spec

import (
	"bytes"
	"testing"
)

// Unit tests for the reader itself, so a parser bug is distinguishable from a
// decoder bug when the suite board moves.

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
		Decode:   func([]byte) error { return nil },
		ReadText: func([]byte) error { return nil },
		IsGated:  func(error) bool { return false },
		Has:      []Capability{CapWatReader},
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
	if got := s.Commands[4].Invoke; got != "f" {
		t.Errorf("assert_return Invoke = %q, want %q", got, "f")
	}
	if got := s.Commands[4].Results; len(got) != 1 || got[0] != (Val{Kind: KindI32, Bits: 1}) {
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
		Instantiate: func(Command) (Instance, Stratum, error) { return "stub", StratumUnset, nil },
		Invoke: func(_ Instance, name string, _ []Val) ([]Val, error) {
			if name != "f" {
				return nil, errString("unknown export " + name)
			}
			return []Val{{Kind: KindI32, Bits: 1}}, nil
		},
		Has: []Capability{CapWatReader, CapInterpreter},
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
func TestScriptModuleFormsAreNotWatBodies(t *testing.T) {
	cases := []struct {
		src  string
		want Kind
	}{
		{"(module definition (memory 65536))", KindUnsupported},
		{"(module definition $M (global (export \"g\") (mut i32) (i32.const 0)))", KindUnsupported},
		{"(module instance $I1 $M)", KindUnsupported},
		{"(module instance)", KindUnsupported},
		// The accept direction: an ordinary body, and one with a $name, stay scorable.
		{"(module (memory 1))", KindModuleText},
		{"(module $M (memory 1))", KindModuleText},
		// A quote form is still a quote form — the new keyword check must not shadow it.
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
