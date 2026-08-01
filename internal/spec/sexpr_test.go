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
// This file is unsupported for *execution* — nothing here classifies to a
// runnable command — but it must parse, because a parse error and an
// unsupported command are different numbers on the board.
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
	r := s.Run(func([]byte) error { return nil })
	if r.Unsupported != 1 || r.Total() != 0 {
		t.Errorf("got %d unsupported, %d executed; want 1/0", r.Unsupported, r.Total())
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
		KindUnsupported, // assert_return is phase 2
	}
	for i, k := range want {
		if s.Commands[i].Kind != k {
			t.Errorf("command %d: got %v, want %v", i, s.Commands[i].Kind, k)
		}
	}
	// The capability is attached by the classifier, not by the run loop — the
	// derived-gap mechanism starts here.
	if got := s.Commands[3].Needs; got != CapWatReader {
		t.Errorf("quote command Needs = %q, want %q", got, CapWatReader)
	}
	if len(s.Commands[3].Source) == 0 {
		t.Error("quote command carried no Source; the run loop would have nothing to read")
	}

	// A decoder that satisfies both malformed assertions and the valid module.
	r := s.Run(func(image []byte) error {
		if len(image) < 8 {
			return errString("unexpected end")
		}
		if !bytes.Equal(image[:4], []byte{0x00, 'a', 's', 'm'}) {
			return errString("magic header not detected")
		}
		return nil
	})
	// 3 pass, 1 unsupported (assert_return), 1 unimplemented (the quote form). The
	// quote vector is deliberately *not* a fail: no wat reader exists, and an unbuilt
	// component must not read as a wrong answer.
	if r.Pass != 3 || r.Fail != 0 || r.Unsupported != 1 || r.Unimplemented != 1 {
		t.Errorf("got %d pass, %d fail, %d unsupported, %d unimplemented; want 3/0/1/1",
			r.Pass, r.Fail, r.Unsupported, r.Unimplemented)
	}
	if got := r.UnimplementedByCapability[CapWatReader]; got != 1 {
		t.Errorf("unimplemented attributed to %s = %d, want 1", CapWatReader, got)
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
