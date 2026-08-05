package text

import (
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestParseNatMatchesTheReferenceGrammar pins parseNat against `of_string`'s three branches
// (ixx.ml:305-326) at each width `parser.mly:37-43` instantiates.
//
// Each reader is checked *on its own* rather than through the parser, per #63's DoD and #41's
// precedent: composition over the vectors the suite happens to have reaches fewer readers than
// the vocabulary has, and the accept direction has no vectors at all.
func TestParseNatMatchesTheReferenceGrammar(t *testing.T) {
	// Widths as `parser.mly` instantiates them: nat8 → I8 (bitwidth 8, i8.ml), nat32 → I32
	// (32, i32.ml), nat64 → I64 (64, i64.ml). The limit is unsigned-max at each width because
	// `of_string_u` (ixx.ml:349) requires an unsigned spelling and `of_string`'s final
	// `upper = zero || upper = mask || upper = sign` admits the whole unsigned range.
	for _, c := range []struct {
		name  string
		in    string
		bits  uint
		want  uint64
		ok    bool
		cites string
	}{
		// Decimal, the default branch. `parse_dec` is reached for everything without an "0x".
		{"zero", "0", 32, 0, true, "derived: of_string's parse_dec on a single digit"},
		{"decimal", "1234", 32, 1234, true, "derived: parse_dec"},

		// The two strconv divergences, which is why this reader is hand-written. Both are
		// accept-direction: no assert_malformed vector can express either, because the inputs
		// are legal wat and the correct verdicts are "accept, with this value".
		{
			"leading zero is decimal not octal", "010", 32, 10, true,
			"derived from int_literals.wast:10, which exports `i32.not_octal` asserting " +
				"(i32.const 010) == 10 via assert_return — the fact is the suite's, the " +
				"vector is unexecutable in v0",
		},
		{
			"leading zero, three digits", "0755", 32, 755, true,
			"derived: same premise; strconv.ParseUint(_, 0, _) reads this as 493",
		},
		{
			"08 is a legal nat", "08", 32, 8, true,
			"derived: `num = digit ('_'? digit)*` (lexer.mll:62) admits it and parse_dec " +
				"has no octal branch; strconv base 0 rejects it",
		},
		{"09 is a legal nat", "09", 32, 9, true, "derived: as above"},

		// Hex, the "0x"-prefixed branch.
		{"hex lower", "0xff", 32, 255, true, "derived: parse_hex"},
		{"hex upper digits", "0xFF", 32, 255, true, "derived: hex_digit's 'A'..'F' arm (ixx.ml:300)"},
		{"hex mixed", "0xdeadBEEF", 32, 0xdeadBEEF, true, "derived: hex_digit"},
		{
			"bare 0x is zero", "0x", 32, 0, true,
			"derived: parse_int's guard is `i + 2 <= len`, so the hex branch is selected and " +
				"parse_hex returns its accumulator on an empty digit run. Unreachable through " +
				"the lexer (`nat = num | \"0x\" hexnum` needs a digit), pinned because an " +
				"unreachable divergence is one no oracle will ever report",
		},

		// Underscores: admitted at this layer, positioned by the lexer. See the premise test.
		{"underscore between digits", "1_000", 32, 1000, true, "derived: parse_dec skips '_'"},
		{"underscore in hex", "0xdead_beef", 32, 0xdeadBEEF, true, "derived: parse_hex skips '_'"},

		// Width boundaries, both signs of each. The reference's bound is checked *before* the
		// shift/multiply, so these are the exact edges rather than approximations.
		{"nat8 max", "255", 8, 255, true, "derived: I8 bitwidth 8 (i8.ml), unsigned range"},
		{
			"nat8 overflow by one", "256", 8, 0, false,
			"cited in effect by simd_lane.wast's 15 `i8 constant out of range` vectors; the " +
				"boundary itself is derived from bitwidth 8",
		},
		{"nat8 max hex", "0xff", 8, 255, true, "derived: as above"},
		{"nat8 hex overflow", "0x100", 8, 0, false, "derived: parse_hex's pre-shift bound"},
		{"nat32 max", "4294967295", 32, 4294967295, true, "derived: I32 bitwidth 32"},
		{"nat32 overflow by one", "4294967296", 32, 0, false, "derived: as above"},
		{"nat32 max hex", "0xffffffff", 32, 4294967295, true, "derived: as above"},
		{"nat32 hex overflow", "0x100000000", 32, 0, false, "derived: as above"},
		{"nat64 max", "18446744073709551615", 64, ^uint64(0), true, "derived: I64 bitwidth 64"},
		{
			"nat64 overflow by one", "18446744073709551616", 64, 0, false,
			"derived: parse_dec's `lt_u num max_upper || num = max_upper && le_u digit " +
				"max_lower` (ixx.ml:319), which is why overflow is caught before the multiply",
		},
		{"nat64 max hex", "0xffffffffffffffff", 64, ^uint64(0), true, "derived: as above"},
		{"nat64 hex overflow", "0x10000000000000000", 64, 0, false, "derived: parse_hex's bound"},

		// Rejections. `require (len > 0)` (ixx.ml:326) and the digit functions' `_ -> failwith`.
		{"empty", "", 32, 0, false, "derived: require (len > 0), ixx.ml:326"},
		{"non-digit", "12a", 32, 0, false, "derived: dec_digit's failwith arm (ixx.ml:294)"},
		{"non-hexdigit", "0xg", 32, 0, false, "derived: hex_digit's failwith arm (ixx.ml:301)"},
		{
			"sign is not a nat", "+1", 32, 0, false,
			"derived: of_string_u requires s[0] is neither '+' nor '-' (ixx.ml:349), and the " +
				"lexer's `nat` has no sign arm either",
		},
		{"negative is not a nat", "-1", 32, 0, false, "derived: as above"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseNat(c.in, c.bits)
			if ok != c.ok {
				t.Fatalf("parseNat(%q, %d) ok = %v, want %v\n\tprovenance: %s",
					c.in, c.bits, ok, c.ok, c.cites)
			}
			if ok && got != c.want {
				t.Errorf("parseNat(%q, %d) = %d, want %d\n\tprovenance: %s",
					c.in, c.bits, got, c.want, c.cites)
			}
		})
	}
}

// TestStrconvWouldBeWrongHere is the *negative* half of parseNat's justification, and it is a
// test rather than a comment because a claim about a rejected alternative decays exactly like
// any other claim.
//
// The header of num.go says `strconv` is wrong in two measured directions. If a future Go
// release changed `ParseUint`'s base-0 handling, that paragraph would become false while the
// suite stayed green — the reader is hand-written and would not care. This asserts the
// divergence still exists, so the *reason* for the hand-written grammar is checked and not
// merely asserted. A rationale nobody re-checks is a claim, not a rationale.
func TestStrconvWouldBeWrongHere(t *testing.T) {
	// Direction 1: base 0 reads a leading zero as octal, giving a wrong *value* for a legal
	// input — the failure a reject-only suite cannot see at all.
	if n, err := strconv.ParseUint("0755", 0, 64); err != nil || n != 493 {
		t.Errorf("strconv.ParseUint(%q, 0, 64) = %d, %v; num.go's header claims it is 493 "+
			"(octal) against the reference's 755. If strconv changed, that paragraph needs "+
			"rewriting — but note parseNat is still right either way", "0755", n, err)
	}
	if n, _ := parseNat("0755", 64); n != 755 {
		t.Errorf("parseNat(%q) = %d, want 755 — the whole point of the hand-written grammar", "0755", n)
	}

	// Direction 2: base 0 *rejects* a legal wat nat. The accept-direction defect, and the one
	// that matters more: a decoder that rejects valid modules is worse than one that misses an
	// invalid one.
	for _, s := range []string{"08", "09"} {
		if _, err := strconv.ParseUint(s, 0, 64); err == nil {
			t.Errorf("strconv.ParseUint(%q, 0, 64) now succeeds; num.go's header claims it "+
				"rejects a legal wat nat", s)
		}
		if _, ok := parseNat(s, 64); !ok {
			t.Errorf("parseNat(%q) rejects a nat the lexer's `num` rule admits", s)
		}
	}

	// Direction 3: base 10, the obvious repair for direction 1, cannot read hex at all. Named
	// in the header as the reason there is no strconv mode that is the reference's grammar.
	if _, err := strconv.ParseUint("0xff", 10, 64); err == nil {
		t.Error("strconv.ParseUint(\"0xff\", 10, 64) now succeeds; num.go's header claims " +
			"base 10 cannot read hex, which is why neither base is the reference's grammar")
	}
}

// TestUnderscorePlacementIsTheLexersJob pins num.go's cross-layer premise.
//
// parseNat skips `_` wherever it appears, exactly as `of_string` does, and is therefore *not*
// the layer that rejects `1__0`, `_1`, `1_`, `0x_1`. That is only correct while the lexer's
// `matchNum` implements `num = digit ('_'? digit)*` (lexer.mll:62). A refactor there could
// quietly widen what reaches this function, and nothing in num.go would fail — the two halves
// of one rule live in two files, which is the drift shape a tripwire exists for.
//
// So the premise is asserted from both ends: the lexer rejects these lexemes today, and the
// reference's own rule still has the shape the claim quotes.
func TestUnderscorePlacementIsTheLexersJob(t *testing.T) {
	// End 1: parseNat is permissive by design, and the test says so rather than leaving a
	// reader to infer it from an absence.
	for _, s := range []string{"1__0", "0x_1"} {
		if _, ok := parseNat(s, 64); !ok {
			t.Errorf("parseNat(%q) rejects; this reader is *meant* to be permissive about "+
				"underscore placement, because the reference's of_string is (it skips '_' "+
				"unconditionally, ixx.ml:308/:316). Placement is the lexer's rule, and moving "+
				"it here would put one rule in two files", s)
		}
	}

	// End 2: the lexer is the layer that rejects them, so no such lexeme reaches parseNat from
	// a real module. Measured through the lexer's own entry point rather than by reading
	// match.go — *measure with the instrument, not a regex*.
	for _, lexeme := range []string{"1__0", "_1", "1_", "0x1_", "0x_1"} {
		src := "(module (func i32.const " + lexeme + "))"
		err := ReadModule([]byte(src))
		if err == nil {
			t.Errorf("the lexer accepted %q as a nat; num.go's premise is that `num = digit "+
				"('_'? digit)*` keeps malformed underscore placement away from parseNat, and "+
				"if that is no longer true the rejection has to move into this package", lexeme)
			continue
		}
		// The verdict is the premise; the *message* is what says which layer produced it.
		// `unknown operator` is the lexer's, and a different error here means the lexeme got
		// past the lexer and was rejected somewhere else — which is the premise failing in the
		// way that still looks green if only the verdict is checked.
		if !strings.Contains(err.Error(), "unknown operator") {
			t.Errorf("%q was rejected as %v, not by the lexer's `unknown operator`; the "+
				"premise is specifically that the *lexer* owns underscore placement, so a "+
				"rejection from another layer means the premise moved even though the vector "+
				"still fails", lexeme, err)
		}
	}

	// End 2b: the accept direction, which end 2 alone cannot supply. Every lexeme above is
	// rejected — and a lexer that rejected *every* nat would pass end 2 perfectly, because a
	// reject-only control cannot tell "the placement rule works" from "nothing lexes". So the
	// legal placements are lexed too, and must get *past* the lexer: `unimplemented` is #63's
	// own not-yet, and reaching it proves the lexeme was accepted.
	//
	// This is the vacuity cousin of *a comparison against an empty set succeeds*: the discriminating
	// evidence is the pair, not either half. Printed both ways before it was trusted — the reject
	// half yields `unknown operator <lexeme>`, the accept half `unimplemented: instruction body`.
	for _, lexeme := range []string{"1_0", "10", "0xff", "0xdead_beef"} {
		src := "(module (func i32.const " + lexeme + "))"
		err := ReadModule([]byte(src))
		if err != nil && strings.Contains(err.Error(), "unknown operator") {
			t.Errorf("the lexer rejected %q as %v; this is a legal nat under `num = digit "+
				"('_'? digit)*`, and a lexer rejecting valid placements would satisfy the "+
				"reject half above while breaking the premise in the direction that matters "+
				"more — a reader that rejects valid input is worse than one that misses "+
				"invalid input", lexeme, err)
		}
	}

	// End 3: the authority still writes the rule the premise quotes. Without this, the two ends
	// above could agree with each other while both diverging from upstream.
	src := testenv.RequireSpecRef(t, testenv.RefLexerMLL)
	for _, rule := range []string{
		"let num = digit ('_'? digit)*",
		"let hexnum = hexdigit ('_'? hexdigit)*",
	} {
		if !strings.Contains(src, rule) {
			t.Errorf("lexer.mll no longer contains %q; num.go's premise cites this rule as the "+
				"reason underscore placement is not checked here, and a moved rule means the "+
				"premise must be re-derived rather than the test relaxed", rule)
		}
	}
}

// TestParseAlignAppliesThePowerOfTwoCheck pins `align` (parser.mly:532) and the seam its doc
// comment names: this reader owns "must be a power of two", and the validator owns "must not be
// larger than natural".
//
// **The exponent column is asserted too, and it is the half no vector covers** (#128). The bytes
// this value reaches are a flags field a decoder accepts whatever it holds, so `align=4` writing 4
// instead of 2 encodes a legal image denoting a different access width — the accept-direction class,
// and `log2_unsigned` is the one line of arithmetic between the text and the field. `align=0x10`
// earns its place twice over here: the hex branch and the exponent 4 are independent, and a reader
// that returned the *value* rather than its log2 would pass every boolean above.
func TestParseAlignAppliesThePowerOfTwoCheck(t *testing.T) {
	for _, c := range []struct {
		in        string
		wantAlign int
		wantOK    bool
		wantIsNat bool
		cites     string
	}{
		{"align=1", 0, true, true, "derived: 1 land 0 = 0, lib.ml:331; log2 1 = 0"},
		{"align=2", 1, true, true, "derived: as above"},
		{"align=4", 2, true, true, "derived: as above"},
		{"align=0x10", 4, true, true, "derived: nat64 first, so the hex branch applies; log2 16 = 4"},
		{"align=9223372036854775808", 63, true, true, "derived: 2^63, the largest power of two in 64 bits"},
		// align=0 is a power-of-two failure, not a width failure — `n <> 0` is the first
		// conjunct of is_power_of_two_unsigned. Worth its own case because "zero is invalid"
		// could plausibly be either check, and only one of them is this reader's. The exponent
		// is 0 and meaningless: `bits.Len64(0)-1` is -1, so the reader returns before computing
		// it and the verdict is what guards the value.
		{"align=0", 0, false, true, "derived: `n <> 0 &&` in lib.ml:331; align.wast has vectors for it"},
		{"align=3", 0, false, true, "derived: 3 land 2 = 2"},
		{"align=6", 0, false, true, "derived: 6 land 5 = 4"},
		// A nat64 overflow: isNat false, so the caller reports the width error rather than the
		// power-of-two one. Two error strings behind one lexeme, which is why parseAlign
		// returns two booleans instead of one.
		{"align=18446744073709551616", 0, false, false, "derived: nat64 runs first (parser.mly:532)"},
		{"align=0x10000000000000000", 0, false, false, "derived: as above, hex branch"},
	} {
		t.Run(c.in, func(t *testing.T) {
			align, ok, isNat := parseAlign(c.in)
			if isNat != c.wantIsNat {
				t.Fatalf("parseAlign(%q) isNat = %v, want %v\n\tprovenance: %s",
					c.in, isNat, c.wantIsNat, c.cites)
			}
			if ok != c.wantOK {
				t.Errorf("parseAlign(%q) ok = %v, want %v\n\tprovenance: %s",
					c.in, ok, c.wantOK, c.cites)
			}
			if align != c.wantAlign {
				t.Errorf("parseAlign(%q) align = %d, want %d — this is the exponent the flags "+
					"field holds, and a wrong one encodes a legal image with the wrong access "+
					"width\n\tprovenance: %s", c.in, align, c.wantAlign, c.cites)
			}
		})
	}
}

// TestAlignmentCheckIsTheParsersAndNaturalIsTheValidators pins the *seam* parseAlign's doc
// comment states, in the authority, so the deferral of 54 vectors is checked rather than
// asserted.
//
// The claim has two halves and they live in different files: the power-of-two rejection is
// `parser.mly`'s, and "must not be larger than natural" is `valid.ml`'s. #63 reports those 54
// as waiting on the validator rather than netting them out, and that report is only honest if
// the layer assignment is true. If upstream moved either check, the reconciliation's excuse
// would be wrong and nothing else would notice.
func TestAlignmentCheckIsTheParsersAndNaturalIsTheValidators(t *testing.T) {
	parser := testenv.RequireSpecRef(t, testenv.RefParserMLY)

	if !strings.Contains(parser, `"alignment must be a power of two"`) {
		t.Error(`parser.mly no longer errors "alignment must be a power of two"; parseAlign's ` +
			`doc comment claims this check is the parser's and that its message contains the ` +
			`bare "alignment" the 46 align.wast vectors match by substring`)
	}
	// The substring relation the two buckets depend on. Not luck and not an accident of
	// wording: decision 0003 records prefix matching as the reason one check answers both
	// buckets, so if the messages stopped nesting, 46 vectors would need their own answer.
	if !strings.Contains("alignment must be a power of two", "alignment") {
		t.Error("the shorter expectation is no longer a substring of the longer message")
	}
	// And the negative half: the natural-width check is *not* in the parser. This is the
	// assertion that makes #63's deferral of the 54 a layer fact rather than a convenience.
	if strings.Contains(parser, "alignment must not be larger than natural") {
		t.Error(`parser.mly now contains "alignment must not be larger than natural"; ` +
			`parseAlign's comment defers those 54 vectors to the validator (valid.ml:389) on ` +
			`the grounds that the parser does not know each instruction's natural width. If ` +
			`the check moved into the parser, that deferral is no longer licensed`)
	}
}
