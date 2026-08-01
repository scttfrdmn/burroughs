package text

import (
	"strings"
	"testing"
)

// The lexer's unit vectors, with provenance.
//
// Every row here is `cited`, `derived`, or `synthetic` in TestTextFixtureProvenance's
// sense (internal/spec) — the *text* arm of the same machinery that checks the binary
// fixtures' `binary.wast:N` comments. A cited row's `src` is compared against the
// `(module quote ...)` command at that line of the vendored suite, so a hand-typed
// transcription that drifts fails there rather than passing here.
//
// Why hand-typed rows at all, when the standing preference is deriving corpora from the
// suite at run time: these assert *which arm* fired and *what the message says*, which the
// suite does not record. The suite-derived count is the board's job and lands with the
// wiring (#53, PR B). These are the mechanism checks underneath it.

// lexVector is one input and the error substring it must produce, or "" for "must lex".
type lexVector struct {
	name string
	src  string
	want string // substring of the error; "" means the input must lex clean
}

// obsoleteMnemonics are the eleven vectors whose *message text* the suite reads back —
// `assert_malformed`'s expected string contains the lexeme, so message rendering is
// oracle-covered here (#38's refinement) rather than ours alone to keep honest. That
// makes them the highest-value rows in this file: they are the one place the invented-bits
// class has suite teeth.
var obsoleteMnemonics = []lexVector{
	{"current_memory", "(memory 1)(func (drop (current_memory)))", "unknown operator current_memory"},                             // obsolete-keywords.wast:2
	{"grow_memory", "(memory 1)(func (drop (grow_memory (i32.const 0))))", "unknown operator grow_memory"},                        // obsolete-keywords.wast:10
	{"get_local", "(func (local $i i32) (drop (get_local $i)))", "unknown operator get_local"},                                    // obsolete-keywords.wast:19
	{"set_local", "(func (local $i i32) (set_local $i (i32.const 0)))", "unknown operator set_local"},                             // obsolete-keywords.wast:26
	{"tee_local", "(func (local $i i32) (drop (tee_local $i (i32.const 0))))", "unknown operator tee_local"},                      // obsolete-keywords.wast:33
	{"anyfunc", "(global $g anyfunc (ref.null func))", "unknown operator anyfunc"},                                                // obsolete-keywords.wast:40
	{"get_global", "(global $g i32 (i32.const 0))(func (drop (get_global $g)))", "unknown operator get_global"},                   // obsolete-keywords.wast:47
	{"set_global", "(global $g (mut i32) (i32.const 0))(func (set_global $g (i32.const 0)))", "unknown operator set_global"},      // obsolete-keywords.wast:55
	{"wrap/i64", "(func (drop (i32.wrap/i64 (i64.const 0))))", "unknown operator i32.wrap/i64"},                                   // obsolete-keywords.wast:63
	{"trunc_s:sat", "(func (drop (i32.trunc_s:sat/f32 (f32.const 0))))", "unknown operator i32.trunc_s:sat/f32"},                  // obsolete-keywords.wast:70
	{"convert_s/i32x4", "(func (drop (f32x4.convert_s/i32x4 (v128.const i64x2 0 0))))", "unknown operator f32x4.convert_s/i32x4"}, // obsolete-keywords.wast:77
}

// TestObsoleteMnemonicsAreUnknownOperators is the 555-bucket's oracle-covered core.
//
// The mechanism under test is the keyword table's *absence* (decision 0009): none of these
// eleven is in keywords.go, so `emitKeyword`'s miss produces the error. That is the whole
// reject-direction contract, and keywords_test.go pins the absences at the artifact while
// this pins the behaviour at the consumer.
func TestObsoleteMnemonicsAreUnknownOperators(t *testing.T) {
	// A count floor, not a non-empty check: a table that lost rows would pass every
	// assertion below by iterating fewer of them, and the eleven are a closed set the
	// suite file defines. *A comparison against an empty set succeeds.*
	if len(obsoleteMnemonics) != 11 {
		t.Fatalf("obsoleteMnemonics holds %d rows; obsolete-keywords.wast has exactly 11 "+
			"assert_malformed forms and this table is meant to be all of them", len(obsoleteMnemonics))
	}
	for _, v := range obsoleteMnemonics {
		t.Run(v.name, func(t *testing.T) {
			_, err := LexAll([]byte(v.src))
			if err == nil {
				t.Fatalf("lexed clean, want error containing %q", v.want)
			}
			if !strings.Contains(err.Error(), v.want) {
				t.Fatalf("got %q, want a substring match on %q", err, v.want)
			}
		})
	}
}

// TestDisambiguationIsLongestThenEarliest pins ocamllex's two-part rule, because the
// arm table in lexer.go is a transcription of an authority and reordering it silently is
// the defect this test exists to catch.
//
// Each case names *which half* of the rule it exercises, and the negative space is the
// point: `get_local` is included precisely because it cannot distinguish the orderings,
// which is why it is labelled that way rather than counted as coverage. *A test named for
// a partition must be checked against the partition, not against its own case labels*
// (grave #34) — so the two halves are asserted by their distinguishing observation (the
// message's lexeme, the token's kind), not by both merely erroring.
func TestDisambiguationIsLongestThenEarliest(t *testing.T) {
	t.Run("longest match wins", func(t *testing.T) {
		// `keyword` matches `i32.wrap` (8); `reserved` matches `i32.wrap/i64` (12),
		// because `/` is an idchar but is not in the keyword continuation set. The
		// discriminating observation is the *lexeme in the message*: a reader that
		// stopped at 8 would produce "unknown operator i32.wrap", also an error, also
		// plausible, and wrong on a vector that can see it. obsolete-keywords.wast:63
		_, err := LexAll([]byte("i32.wrap/i64"))
		if err == nil {
			t.Fatal("lexed clean")
		}
		if got := err.Error(); got != "unknown operator i32.wrap/i64" {
			t.Fatalf("got %q; the 12-byte reserved match must win over the 8-byte keyword match", got)
		}
	})

	t.Run("earliest arm breaks a tie", func(t *testing.T) {
		// `"offset="nat` and `reserved` both match all 8 bytes of `offset=0`. Longest
		// cannot separate them; arm position can, and `offset=` comes first. The
		// discriminating observation is the token *kind* — an implementation that let
		// `reserved` win would report "unknown operator offset=0" instead.
		//
		// derived from simd_address.wast:112 — the suite has `offset=-1` inside a quote
		// module expecting `unknown operator`, which establishes that `offset=` followed
		// by a *non-nat* falls through to reserved; the well-formed `offset=0` companion
		// is entailed by that arm existing at all and is not itself a vector.
		toks, err := LexAll([]byte("offset=0"))
		if err != nil {
			t.Fatalf("offset=0 must lex: %v", err)
		}
		if len(toks) != 1 || toks[0].Kind != OffsetEqNat {
			t.Fatalf("got %v, want one OffsetEqNat token; reserved matched the same 8 bytes "+
				"and only arm order separates them", toks)
		}
		// The negative half of the same tie-break, which is what makes the pair a
		// partition rather than two assertions: with a non-nat tail the `offset=` arm
		// does not match at all, so `reserved` wins on length and the message names the
		// whole lexeme. simd_address.wast:112
		if _, err := LexAll([]byte("offset=-1")); err == nil || !strings.Contains(err.Error(), "unknown operator offset=-1") {
			t.Fatalf("got %v, want `unknown operator offset=-1`", err)
		}
	})

	t.Run("tie the rule cannot be tested with", func(t *testing.T) {
		// `keyword` and `reserved` both match all 9 bytes of `get_local`, and both paths
		// produce the identical message. Recorded as a *non*-discriminating case so that
		// nobody later mistakes it for evidence about arm order. obsolete-keywords.wast:19
		_, err := LexAll([]byte("get_local"))
		if err == nil || err.Error() != "unknown operator get_local" {
			t.Fatalf("got %v, want `unknown operator get_local`", err)
		}
	})
}

// TestEveryArmCanWin is the arm table's vacuity check, scoped to the space rather than to
// today's vectors.
//
// A transcribed arm that is shadowed by an earlier one — matching, but never winning — is
// dead code that looks live, and no suite vector can see it: the token stream is right, so
// the board stays green while the reader has fewer arms than it claims. That is the
// unreachable-branch shape (grave 0003) inside a table whose whole correctness argument is
// "it is a faithful transcription".
//
// So the domain is derived: every arm in `arms` must be the winner for at least one input
// here, and the inputs are checked to cover the table rather than the table checked
// against the inputs. Adding an arm without a witness fails.
func TestEveryArmCanWin(t *testing.T) {
	witnesses := []string{
		"(", ")", // lpar, rpar
		"0", "42", "0x1f", // nat
		"+1", "-0x2", // int
		"1.5", "inf", "nan:0x1", "0x1p3", // float
		`"abc"`, `""`, // string
		`"abc`,              // unclosed string
		"\"a\x01b\"",        // control in string
		`"\q"`,              // illegal escape
		"module", "i32.add", // keyword
		"offset=0", "align=4", // offset=, align=
		"$x", "$a.b", // $id
		`$"n"`,                     // $string
		"$",                        // bare $
		"(@a x)", `(@"a" x)`, "(@", // annotations
		";; c\n",  // line comment
		"(; c ;)", // block comment
		" ", "\t", // space
		"\n", "\r", // newline
		",", "get_local", // reserved
		"\x01", // control
		"é",    // utf8enc, misplaced
		"\xff", // catch-all
	}

	won := map[string]int{}
	for _, w := range witnesses {
		b := []byte(w)
		best, bestArm := -1, -1
		for i := range arms {
			if n := arms[i].length(nil, b); n > best {
				best, bestArm = n, i
			}
		}
		if bestArm < 0 {
			t.Fatalf("no arm matched %q", w)
		}
		won[arms[bestArm].name]++
	}

	// The floor before the comparison, because comparing two nearly-empty sets agrees.
	if len(arms) < 20 {
		t.Fatalf("arms holds %d entries; the transcription of `rule token` is 20+ arms, so "+
			"this test would be checking almost nothing", len(arms))
	}
	for i := range arms {
		if won[arms[i].name] == 0 {
			t.Errorf("arm %q (index %d) never wins for any witness here.\n\t"+
				"Either it is shadowed by an earlier arm — dead code that looks live, which no "+
				"suite vector can see because the token stream stays right — or it needs a witness "+
				"added above. A transcription's correctness argument is that every arm is real.",
				arms[i].name, i)
		}
	}
}

// TestEscapesProduceBytesNotRunes pins the distinction the 186 parser-layer UTF-8 vectors
// depend on, one layer below where they are scored.
//
// `\ff` is the *byte* 0xFF, not U+00FF. A decoder that helpfully re-encoded it as UTF-8
// would make every one of those vectors well-formed and the bucket would go green while
// being wrong — buying pass count with a check that is wrong in general, which is the
// overfitting failure (§9 G-3) in its most tempting form, because the wrong answer is the
// friendlier one.
//
// The companion assertion is that `\u{...}` *does* encode: the two escapes are
// deliberately different and one test pins both, so a change that unified them fails here
// rather than showing up as a bucket moving for no reason.
func TestEscapesProduceBytesNotRunes(t *testing.T) {
	// synthetic — the suite has no vector asserting what `"\ff"` decodes *to*; it only
	// asserts that a name containing it is malformed. The byte-level claim is the
	// mechanism under that, and it is ours to state.
	toks, err := LexAll([]byte(`"\ff"`))
	if err != nil {
		t.Fatalf(`"\ff" must lex: %v`, err)
	}
	if len(toks) != 1 || len(toks[0].Value) != 1 || toks[0].Value[0] != 0xff {
		t.Fatalf(`"\ff" decoded to % x, want exactly one byte ff — a two-byte c3 bf would `+
			`mean the escape was treated as a rune, which turns 186 malformed vectors well-formed`, toks[0].Value)
	}

	// And the escape that does encode, so the pair is a partition.
	toks, err = LexAll([]byte(`"\u{ff}"`))
	if err != nil {
		t.Fatalf(`"\u{ff}" must lex: %v`, err)
	}
	if got := toks[0].Value; len(got) != 2 || got[0] != 0xc3 || got[1] != 0xbf {
		t.Fatalf(`"\u{ff}" decoded to % x, want c3 bf — \u{} names a scalar value and encodes it`, got)
	}
}

// TestEmptyStringValueIsNonNil — emptiness is a length, never a nil.
//
// A caller testing `Value != nil` to mean "has a string" misreads `""`, which is a real
// vector. This was a fuzz finding in the sibling wast reader and is asserted here rather
// than re-earned.
func TestEmptyStringValueIsNonNil(t *testing.T) {
	for _, src := range []string{`""`, `$"x"`} {
		toks, err := LexAll([]byte(src))
		if err != nil {
			t.Fatalf("%s must lex: %v", src, err)
		}
		if toks[0].Value == nil {
			t.Errorf("%s produced a nil Value; a caller distinguishing absent from empty "+
				"would misread it", src)
		}
	}
	toks, err := LexAll([]byte(`""`))
	if err != nil || len(toks[0].Value) != 0 {
		t.Fatalf(`"" must lex to a zero-length non-nil Value, got %v %v`, toks, err)
	}
}

// TestAnnotationBodyReservedIsAnAtom is the third `reserved` arm — the accept-direction
// half of the 555 bucket, and the half no vector can score.
//
// `reserved` appears three times in lexer.mll: `:809`'s keyword fallthrough and `:839`
// both produce `unknown operator`, but `:882` is in the `annot` scanner and produces
// `Annot.Atom`. A reader that treated all three alike would reject annotations the spec
// *accepts* — invisible on the board by construction, because the vectors that would catch
// it are the ones expected to lex clean and nothing counts those as a bucket.
//
// annotations.wast:14 is the vector, and it is the same file grave #18 came from: a bare
// `;` inside an annotation body, which is token soup one layer down from where the wast
// reader met it.
func TestAnnotationBodyReservedIsAnAtom(t *testing.T) {
	// annotations.wast:14
	const src = `(@a , ; ] [ }} }x{ ({) ,{{};}] ;)`
	if _, err := LexAll([]byte(src)); err != nil {
		t.Fatalf("annotation body must lex clean, got %v\n\t"+
			"the `annot` rule's reserved arm is an *atom*, not `unknown operator`; conflating it "+
			"with the two error-producing arms rejects annotations the spec accepts", err)
	}
}

// TestAnnotationBodyIsNotAPermissiveRegion is the negative half of the test above, and it
// exists because the permissive version of that code passed the accept case.
//
// `scanAnnotBody` originally advanced one byte on anything it did not recognize, which
// lexed `(@a \00)` clean and silently converted 35 vectors the spec calls malformed into
// accepted input — measured, and it is what refuted this work's own forecast of 627. So
// the accept case and the reject case have to be asserted together: the accept case alone
// is satisfied by a scanner that accepts everything.
func TestAnnotationBodyIsNotAPermissiveRegion(t *testing.T) {
	for _, v := range []lexVector{
		{"nul", "(@a \x00)", "illegal character"},          // annotations.wast:23
		{"del", "(@a \x7f)", "illegal character"},          // annotations.wast:56
		{"empty id", "(@)", "empty annotation id"},         // annotations.wast:72
		{"unclosed", "(@x ", "unclosed annotation"},        // annotations.wast:81
		{"unclosed string", "(@x \"", "unclosed string"},   // annotations.wast:91
		{"empty $ id", "(func $(@a))", "empty identifier"}, // annotations.wast:95
	} {
		t.Run(v.name, func(t *testing.T) {
			_, err := LexAll([]byte(v.src))
			if err == nil {
				t.Fatalf("lexed clean, want %q", v.want)
			}
			if !strings.Contains(err.Error(), v.want) {
				t.Fatalf("got %q, want a substring match on %q", err, v.want)
			}
		})
	}

	// A *well-formed* multi-byte character in an annotation body is still an error: the
	// `annot` rule's tail is `| utf8 { "illegal character" } | _ { "malformed UTF-8" }`
	// and `utf8 = ascii | utf8enc`, so a valid é there is `illegal character`, not an
	// atom. Getting that order backwards accepted three vectors — the last three of the
	// four faithfulness bugs this file's subject went through.
	//
	// derived from annotations.wast:23,57 — the suite brackets it: :23 wants `illegal
	// character` for an ascii control and :57 wants `malformed UTF-8 encoding` for an
	// invalid byte, and a well-formed multi-byte char is in neither vector but is on the
	// `utf8` side of the same arm as :23 by the rule's own definition of `utf8`.
	if _, err := LexAll([]byte("(@a Heiße)")); err == nil ||
		!strings.Contains(err.Error(), "illegal character") {
		t.Fatalf("got %v, want `illegal character`: a well-formed multi-byte char in an "+
			"annotation body takes the `utf8` arm, not the atom arm", err)
	}
}

// TestUTF8RejectionBelongsToExactlyOneForm is the layer partition asserted at its seam,
// and it is the correction of a defect this file's author committed and then measured.
//
// The reference decodes a name's UTF-8 in exactly one place inside the lexer: `annot_id`
// (lexer.mll:51–54), which both decodes and rejects. Everywhere else the rejection is
// `parser.mly`'s — `name` at :46 for export names, `var` at :49 for `VAR` tokens, which
// includes `$"..."`. Both `'$'(string)` arms (`token`'s at :816, `annot`'s at :874) check
// *emptiness only*.
//
// The defect: `validUTF8` was added to `emitVarString` because the probe reported
// `(func $"\ef")` as "accepted but should reject". Adding it made the vector reject, and
// took the measured count from 629 to 630. Both true, and the check was wrong — that
// vector is answered in the reference by a layer this package does not implement, so the
// pass was bought from the wrong stratum: right on this vector, wrong in general,
// invisible on the board by construction (§9 G-3). It is the *inverse* of the wrong-layer
// tell — not an error leaking upward from a missing grammar, but a check reaching downward
// to claim a verdict that is not its layer's to give, and the tell is the same mismatch
// read the other way. Found by falsifying a control (a mutation survived), then reading
// the authority instead of the probe; `grep validUTF8` then found the identical mistake in
// `scanAnnotBody`.
//
// So all four forms are pinned in one test, because the discriminating fact is *which
// form*, not what the bytes are — identical `\ef` in four positions, two verdicts. A single
// form asserted alone would look plausible under any of the wrong readings.
func TestUTF8RejectionBelongsToExactlyOneForm(t *testing.T) {
	// The annotation id: the lexer's own, via annot_id. annotations.wast:79
	if _, err := LexAll([]byte(`(@"\ef")`)); err == nil ||
		!strings.Contains(err.Error(), "malformed UTF-8 encoding") {
		t.Fatalf("got %v, want `malformed UTF-8 encoding`: annot_id decodes in the lexer "+
			"(lexer.mll:51-54), and it is the only name form that does", err)
	}
	// The `$"..."` identifier: must lex clean here. id.wast:31 expects `malformed UTF-8
	// encoding`, and that vector is answered by `var` (parser.mly:49), not by this
	// package — so a green here is the *correct* behaviour, and the vector stays in the
	// fail column with a bucket until the parser lands.
	if _, err := LexAll([]byte(`(func $"\ef")`)); err != nil {
		t.Fatalf("`$\"\\ef\"` must lex clean, got %v\n\t"+
			"lexer.mll:816-818 checks emptiness only; the UTF-8 decode is parser.mly:49. "+
			"Rejecting it here answers id.wast:31 from the wrong stratum — a pass bought "+
			"with a check that is wrong in general.", err)
	}
	// Same form inside an annotation body: a different rule's arm (lexer.mll:874-878)
	// making the identical choice, which is why the sweep for siblings mattered.
	if _, err := LexAll([]byte(`(@a $"\ef")`)); err != nil {
		t.Fatalf("`$\"\\ef\"` in an annotation body must lex clean, got %v", err)
	}
	// The export name, the 176's form: parser-layer, so clean here.
	//
	// derived from names.wast — the suite's export-name UTF-8 vectors are all
	// `assert_malformed` at the *name* production; none asserts anything about the string
	// token, so the accept direction is entailed by the layer split rather than stated.
	if _, err := LexAll([]byte(`(func (export "\ef"))`)); err != nil {
		t.Fatalf("an export name must lex clean (%v); its rejection is parser.mly:46", err)
	}
	// And the legal companions, so the assertions above are not passing because the forms
	// are simply unsupported. synthetic.
	for _, src := range []string{`(func $"ok")`, `(@"ok")`, `(@a $"ok")`} {
		if _, err := LexAll([]byte(src)); err != nil {
			t.Fatalf("%s must lex: %v", src, err)
		}
	}
}

// TestEmptyDecodeIsRejectedInEveryFormThatChecksIt exists because a mutation survived.
//
// Falsifying the controls one at a time turned up a check with no test: gutting
// `emitVarString`'s `len(v) == 0` guard left every test green. Three arms make the same
// emptiness check in the reference — `token`'s `'$'(string)` at lexer.mll:816, `annot`'s at
// :874, and `annot_id` at :51 — and only one had a control, so the other two were *green
// surviving the bug they name*. Scoped to the arms rather than to the one vector that
// exists, because a control scoped to the current sample inherits the current blind spot.
//
// Why this is a separate function rather than a line in the test above: emptiness and UTF-8
// are two independent properties of a decoded name, checked by *different* subsets of the
// arms, and the file previously asserted their union as if it were one rule. That is what
// let a wrong stratum and a missing control sit in the same six lines.
func TestEmptyDecodeIsRejectedInEveryFormThatChecksIt(t *testing.T) {
	// derived from annotations.wast:72,95 — :95 (`(func $(@a))`) establishes the `empty
	// identifier` message for the `$` form and :72 establishes `empty annotation id` for
	// the annotation; the string-decode spelling of each is entailed by the arms making
	// their check on the *decoded* value, which is what `string s` in the reference means,
	// and the suite has no vector spelling it that way.
	for _, v := range []lexVector{
		{"$ empty decode", `(func $"")`, "empty identifier"},        // token's arm, lexer.mll:816
		{"$ empty decode in annot", `(@a $"")`, "empty identifier"}, // annot's arm, lexer.mll:874
		{"annot id empty decode", `(@"")`, "empty annotation id"},   // annot_id, lexer.mll:51
		{"bare $", `$`, "empty identifier"},                         // lexer.mll:819, synthetic
		{"bare $ in annot", `(@a $)`, "empty identifier"},           // lexer.mll:879, synthetic
		{"bare (@", `(@)`, "empty annotation id"},                   // annotations.wast:72
	} {
		t.Run(v.name, func(t *testing.T) {
			_, err := LexAll([]byte(v.src))
			if err == nil {
				t.Fatalf("lexed clean, want %q — an arm that decodes a name and does not "+
					"check the empty result is a check with no control", v.want)
			}
			if !strings.Contains(err.Error(), v.want) {
				t.Fatalf("got %q, want %q", err, v.want)
			}
		})
	}
}

// TestEmptyIdentifierAndEmptyAnnotationIDAreDistinct — the two `empty` messages are
// different strings for different forms, and both are in the 1236.
//
// The per-arm assertions are above; what *this* adds is that the two messages do not
// collapse. **When a partition's members share an error value, that is not a partition
// check** (grave #34) — here they share a *shape*, so the discriminating assertion is the
// message text, and swapping the two would pass any test that only demanded an error.
func TestEmptyIdentifierAndEmptyAnnotationIDAreDistinct(t *testing.T) {
	// annotations.wast:95 and :72
	idErr, annotErr := lexErr(t, `(func $(@a))`), lexErr(t, "(@)")
	if idErr == annotErr {
		t.Fatalf("both forms produced %q; the reference has two distinct messages "+
			"(lexer.mll:819 vs :52) and collapsing them loses which form was wrong", idErr)
	}
	if idErr != "empty identifier" || annotErr != "empty annotation id" {
		t.Fatalf("got (%q, %q), want (`empty identifier`, `empty annotation id`)", idErr, annotErr)
	}
}

func lexErr(t *testing.T, src string) string {
	t.Helper()
	_, err := LexAll([]byte(src))
	if err == nil {
		t.Fatalf("%q lexed clean, want an error", src)
	}
	return err.Error()
}

// TestSuiteQuoteFormsLexClean is the accept direction, and it is deliberately named as a
// weak spot rather than as coverage.
//
// These seven forms are the suite's bare `(module quote ...)` commands: no expected error,
// so they must lex. A lexer-only reader passes them because they *lex*, not because they
// are well-formed — right on these vectors, wrong in general. That is overfitting to the
// oracle arrived at by omission (§9 G-3), and it is stated here and in PR B's account as a
// known unearned pass rather than counted as progress.
func TestSuiteQuoteFormsLexClean(t *testing.T) {
	for _, v := range []lexVector{
		{"tab in annot", "(@a \t)", ""},          // annotations.wast:32
		{"newline in annot", "(@a \n)", ""},      // annotations.wast:33
		{"cr in annot", "(@a \r)", ""},           // annotations.wast:36
		{"space in annot", "(@a  )", ""},         // annotations.wast:55
		{"annot before func", "(@a) (func)", ""}, // annotations.wast:206
		{"annot after func", "(func) (@a)", ""},  // annotations.wast:207
	} {
		t.Run(v.name, func(t *testing.T) {
			if _, err := LexAll([]byte(v.src)); err != nil {
				t.Fatalf("must lex clean: %v", err)
			}
		})
	}
}

// TestNextMakesProgress is the unit-level form of the property FuzzLexerProgress explores.
//
// *Parsers prove progress, they don't assume it* (grave #18). A loop whose exit condition
// and error condition are the same predicate surfaces as an error only when the offending
// byte happens to be a delimiter and hangs otherwise, so the assertion is on the offset,
// not on the verdict. Here over the awkward inputs by hand; the fuzz target covers the
// shapes nobody thought of.
func TestNextMakesProgress(t *testing.T) {
	inputs := []string{
		"", "(", ")", ";", ",", "$", "(@", "(;", ";;", "\x00", "\xff", "\x7f",
		`"`, `"\`, `"\q"`, "0x", "offset=", "align=", "nan:", "(@a", "(@a \x00)",
		"i32.wrap/i64", "é", "(@a Heiße Würstchen)", "(; (; ;)", "\n\r", "\r\n",
	}
	if len(inputs) < 20 {
		t.Fatalf("only %d inputs; this control is a sweep and a short list makes it decoration", len(inputs))
	}
	for _, src := range inputs {
		l := NewLexer([]byte(src))
		for range len(src) + 2 {
			before := l.Offset()
			tok, err := l.Next()
			if err != nil {
				break
			}
			if tok.Kind == EOF {
				break
			}
			if l.Offset() <= before {
				t.Fatalf("Next made no progress at offset %d in %q (token %v)", before, src, tok.Kind)
			}
		}
	}
}
