package text

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestFloatConstBitsMatchTheSuitesBitPatterns is the accept-direction control on floatConstBits,
// and the oracle is `float_literals.wast` read as a table of bit patterns.
//
// # Why this needed finding rather than inventing
//
// The converter's whole output is a bit pattern, and three of its four arms (`inf`, `nan`,
// `nan:0x…`) have no float value in the reference at all — `of_signless_string` returns
// `Rep`-level constants, and negation is `Rep.logxor x Rep.min_int` on the pattern rather than
// arithmetic. A plausible float-valued implementation compiles, passes every range check, and
// encodes a *different module*: every NaN payload collapses to one quiet NaN and `-0` comes out
// as `+0`. Nothing in the reject direction can see that, because the module is well-formed either
// way. So the class is accept-direction (§9 G-3) and needs an authority.
//
// The authority was assumed not to exist. It does, and it is unusually direct: `float_literals.wast`
// is built as `(func (export "n") (result i32) (i32.reinterpret_f32 (f32.const LIT)))` paired with
// `(assert_return (invoke "n") (i32.const PATTERN))`, and **reinterpret is the identity on bits**,
// so each pair states outright which pattern a spelling denotes. That makes the vectors readable
// *without an interpreter* — the `assert_return` is being used here as a literal table, not as an
// execution assertion, which is why this control can exist before the execution loop does.
//
// # The three row shapes, and what each is worth
//
//   - **absolute** (78 rows): the reinterpret pairs above. The strongest form — an expected pattern
//     written in the vector, compared against ours. This is what pins the NaN payloads, both
//     signed zeroes, and the subnormal boundaries.
//   - **equivalence** (21 rows): `(assert_return (invoke "f32-dec-sep1") (f32.const 1000000))`,
//     where the *expected* side is itself a float literal. Both sides go through our converter, so
//     this cannot pin an absolute pattern — it pins that two spellings agree, which is exactly and
//     only what the vector claims. Stated as the weaker check it is rather than counted with the
//     others; it is what catches an underscore stripped in one path and not the other.
//   - **rounding** (300 rows, from `const.wast`): `(module (func (export "f") (result f32)
//     (f32.const LONG)))` on one line, `(assert_return (invoke "f") (f32.const SHORT))` on the
//     next, where SHORT is exactly representable at the width and LONG has more digits than the
//     width can hold. This is the partition that pins **rounding**, and it is here because the
//     first three did not: `float_literals.wast` contains no double-rounding-sensitive literal, so
//     a converter that parsed at 64 bits and narrowed passed all 99 of its rows. Measured over the
//     corpus, 24 of 1295 parseable distinct `f32.const` spellings are sensitive, and every one is
//     in `const.wast` — e.g. `+0x1.00000100000000001p-50`, which correctly rounds to
//     `+0x1.000002p-50` (`const.wast:444`/`:445`) and double-rounds to `+0x1.000000p-50`.
//     Keyed by line adjacency rather than by export name, because all 300 modules export `"f"`.
//   - **skipped**: the file's `assert_malformed` rows (`(f32.const _100)` → "unknown operator") are
//     lexer verdicts, not converter ones — a leading underscore never reaches floatConstBits
//     because `parseNat`'s caller rejects the token first. Named here so the omission is a
//     classification and not a gap; `TestUnderscorePlacementIsTheLexersJob` owns them.
//
// # Two anti-echo choices
//
// The expected pattern is parsed with `strconv.ParseUint`, **not** with our `parseNat`, because
// `signlessFloatBits` calls `parseNat` for the NaN payload — using it on both sides would let one
// bug agree with itself (grave #106: a premise measured with the subject's own instrument is an
// echo). The vectors' patterns are plain hex or plain decimal with no underscores, verified by the
// row regexp being anchored and by the parse-failure branch below being a hard error rather than a
// skip: a row this test cannot read is a row it must not silently drop.
//
// Rows are derived from the file at run time rather than transcribed, so there is no hand-typed
// fixture to drift and nothing for TestFixtureProvenance to check.
func TestFloatConstBitsMatchTheSuitesBitPatterns(t *testing.T) {
	src := string(testenv.RequireSuiteFile(t, "float_literals.wast"))

	// The reinterpret form, captured as (export name, width, literal). Anchoring on the whole
	// shape rather than on `f32.const` alone is deliberate: a func whose body is *not* a bare
	// reinterpret of a const has no pattern stated for it, and matching it loosely would compare
	// our bits against an expectation about something else.
	absDef := regexp.MustCompile(
		`\(func \(export "([^"]+)"\) \(result i(?:32|64)\) \(i(?:32|64)\.reinterpret_f(32|64) \(f(?:32|64)\.const ([^)]*)\)\)\)`)
	// The pattern side. `0x…` or decimal; ParseUint with base 0 reads both.
	absWant := regexp.MustCompile(`\(assert_return \(invoke "([^"]+)"\) \(i(?:32|64)\.const ([^)]*)\)\)`)
	// The separator-equivalence form: expected side is a float literal, not a pattern.
	eqvDef := regexp.MustCompile(`\(func \(export "([^"]+)"\) \(result f(32|64)\) \(f(?:32|64)\.const ([^)]*)\)\)`)
	eqvWant := regexp.MustCompile(`\(assert_return \(invoke "([^"]+)"\) \(f(?:32|64)\.const ([^)]*)\)\)`)

	// Line numbers, so a failure cites the vector the way a hand-written fixture would.
	lineOf := func(off int) int { return 1 + strings.Count(src[:off], "\n") }

	type row struct {
		bits uint
		lit  string
		line int
	}
	collect := func(re *regexp.Regexp) map[string]row {
		out := map[string]row{}
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			bits, err := strconv.Atoi(src[m[4]:m[5]])
			if err != nil {
				t.Fatalf("width %q is not a number: %v", src[m[4]:m[5]], err)
			}
			out[name] = row{bits: uint(bits), lit: src[m[6]:m[7]], line: lineOf(m[0])}
		}
		return out
	}

	absolute := collect(absDef)
	equivalence := collect(eqvDef)

	var checkedAbs, checkedEqv int
	joinedAbs := map[string]bool{}

	for _, m := range absWant.FindAllStringSubmatchIndex(src, -1) {
		name, wantText, line := src[m[2]:m[3]], src[m[4]:m[5]], lineOf(m[0])
		def, ok := absolute[name]
		if !ok {
			continue // an invoke whose func is not the bare-reinterpret shape; not this test's row
		}
		want, err := strconv.ParseUint(wantText, 0, 64)
		if err != nil {
			t.Errorf("float_literals.wast:%d: cannot read expected pattern %q: %v — a row this "+
				"test cannot parse must fail, not be dropped", line, wantText, err)
			continue
		}
		got, ok := floatConstBits(def.lit, def.bits)
		if !ok {
			t.Errorf("float_literals.wast:%d: floatConstBits(%q, %d) rejected a literal the suite "+
				"asserts a value for (defined at :%d)", line, def.lit, def.bits, def.line)
			continue
		}
		if got != want {
			t.Errorf("float_literals.wast:%d: f%d.const %s → %#x, suite says %#x (defined at :%d)",
				line, def.bits, def.lit, got, want, def.line)
		}
		checkedAbs++
		joinedAbs[name] = true
	}

	for _, m := range eqvWant.FindAllStringSubmatchIndex(src, -1) {
		name, wantLit, line := src[m[2]:m[3]], src[m[4]:m[5]], lineOf(m[0])
		def, ok := equivalence[name]
		if !ok {
			continue
		}
		got, gotOK := floatConstBits(def.lit, def.bits)
		want, wantOK := floatConstBits(wantLit, def.bits)
		if !gotOK || !wantOK {
			t.Errorf("float_literals.wast:%d: floatConstBits rejected a spelling the suite asserts "+
				"is legal: %q ok=%v, %q ok=%v", line, def.lit, gotOK, wantLit, wantOK)
			continue
		}
		if got != want {
			t.Errorf("float_literals.wast:%d: f%d.const %s → %#x but the equivalent spelling %s → "+
				"%#x (defined at :%d)", line, def.bits, def.lit, got, wantLit, want, def.line)
		}
		checkedEqv++
	}

	checkedRound := roundingPairs(t)

	// Vacuity floors, per partition — *a comparison against an empty set succeeds*, and one full
	// half absorbing an empty one is that law with a partner to hide behind (grave #105). The
	// figures are the measured counts at authorship: 78 absolute rows (39 f32 + 39 f64) and 21
	// equivalence rows. Floored slightly under so an upstream addition does not fail the build,
	// but *per shape*, because the absolute rows are the ones carrying the NaN payloads and an
	// equivalence-only green would say nothing about them.
	if checkedAbs < 70 {
		t.Errorf("checked %d absolute pattern rows, want >=70: the row regexps have drifted from "+
			"float_literals.wast and this test is comparing almost nothing", checkedAbs)
	}
	if checkedEqv < 18 {
		t.Errorf("checked %d equivalence rows, want >=18: the separator block's shape has drifted",
			checkedEqv)
	}
	if checkedRound < 280 {
		t.Errorf("checked %d rounding pairs, want >=280: const.wast's one-line module/assert_return "+
			"shape has drifted, and this is the only partition that can see a rounding defect",
			checkedRound)
	}
	t.Logf("float_literals.wast: %d absolute bit patterns, %d spelling equivalences; "+
		"const.wast: %d rounding pairs", checkedAbs, checkedEqv, checkedRound)

	// The absolute half is the accept-direction claim, so print its coverage of the *file* rather
	// than only of the rows this test joined: a def whose assert_return never matched contributes
	// nothing and would be invisible in the count above. This is the trigger-coverage measurement
	// grave #78 paid for, applied to a join instead of to a regexp.
	var unjoined []string
	for name, def := range absolute {
		if !joinedAbs[name] {
			unjoined = append(unjoined, fmt.Sprintf("%s (:%d)", name, def.line))
		}
	}
	if len(unjoined) > 0 {
		sort.Strings(unjoined)
		t.Errorf("%d funcs match the reinterpret shape but %d were never joined to an expected "+
			"pattern, so their spellings are asserted by nothing: %s",
			len(absolute), len(unjoined), strings.Join(unjoined, ", "))
	}
}

// roundingPairs checks const.wast's rounding partition and returns the number of pairs compared.
//
// The rows are `(module (func (export "f") (result fN) (fN.const LONG)))` immediately followed by
// `(assert_return (invoke "f") (fN.const SHORT))`, both on their own line. The join is **positional**
// — consecutive lines — because every one of the 300 modules exports the same name `"f"`, so there
// is no key to join on and a name-keyed map would silently keep only the last pair. A positional
// join is fragile in exactly one way, an inserted line between the two, so the pairing requires the
// assert to be on `line+1` and the width to agree; a pair that fails either is not counted, and the
// floor at the call site is what reports it if the shape moves.
//
// Both sides go through floatConstBits, as in the equivalence partition — but unlike that one this
// pins rounding, because SHORT is exactly representable at the width and LONG is not. `float32` is
// applied to the *result* of neither side: the width is passed to the converter, which is the thing
// under test.
func roundingPairs(t *testing.T) int {
	t.Helper()

	src := string(testenv.RequireSuiteFile(t, "const.wast"))
	def := regexp.MustCompile(`^\(module \(func \(export "f"\) \(result f(32|64)\) \(f(?:32|64)\.const ([^)]*)\)\)\)$`)
	want := regexp.MustCompile(`^\(assert_return \(invoke "f"\) \(f(32|64)\.const ([^)]*)\)\)$`)

	lines := strings.Split(src, "\n")
	checked := 0
	for i, line := range lines {
		dm := def.FindStringSubmatch(line)
		if dm == nil || i+1 >= len(lines) {
			continue
		}
		wm := want.FindStringSubmatch(lines[i+1])
		if wm == nil || wm[1] != dm[1] {
			continue
		}
		bits, err := strconv.Atoi(dm[1])
		if err != nil {
			t.Fatalf("const.wast:%d: width %q is not a number: %v", i+1, dm[1], err)
		}
		got, gotOK := floatConstBits(dm[2], uint(bits))
		exp, expOK := floatConstBits(wm[2], uint(bits))
		if !gotOK || !expOK {
			t.Errorf("const.wast:%d: floatConstBits rejected a literal the suite asserts a value "+
				"for: %q ok=%v, %q ok=%v", i+1, dm[2], gotOK, wm[2], expOK)
			continue
		}
		if got != exp {
			t.Errorf("const.wast:%d: f%d.const %s → %#x, suite rounds it to %s = %#x (:%d)",
				i+1, bits, dm[2], got, wm[2], exp, i+2)
		}
		checked++
	}
	return checked
}
