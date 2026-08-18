// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The vector region's authority controls — slice 2's half of authority_test.go's argument.
//
// # The chain has three links and no single file closes it
//
// `sig.go` derives a numeric signature from one authority, because `decode.ml` says which byte is
// `i32.add` and the name says the rest. The vector region cannot be read that way: `decode.ml`
// gives a **mnemonic**, `valid.ml` types a **constructor family**, and nothing in either file joins
// them. `syntax/mnemonics.ml` is the join — one `let` per mnemonic, whose body names the family —
// and `exec/v128.ml` supplies the two facts a family alone does not carry, a shape's lane count and
// its lane scalar type.
//
// So the checks below are a chain, and each link has its own failure:
//
//   - `TestVecFamiliesMatchTheReference` — `vecFamily`'s 256 rows against `mnemonics.ml`, both
//     directions. Catches a wrong family, a missing row, and a row upstream does not have.
//   - `TestVecSignaturesMatchTheReferencesArms` — each family's arity *and* the resolvable half of
//     its types against `valid.ml`'s own `[t; t] --> [t]` text, plus the line each arm is cited at.
//   - `TestLaneFactsMatchTheReference` — `laneType`/`numLanes` against `v128.ml`'s two six-row
//     functions, both directions.
//   - `TestPrefixSIMDIsTheRegionBinaryDispatches` — the two constants `vec.go` spells locally,
//     against `binary`'s own table rather than against inspection.
//
// **None of them can be satisfied by the corpus, and that is why they exist.** An `assert_invalid`
// vector is satisfied by any refusal (contract §9 G-3), so a family that types
// `i32x4.bitmask` as `(v128) -> v128` instead of `(v128) -> i32` makes this package *accept*
// modules the spec refuses, on a board that cannot say so.

// refVecBinding is one `let <mnemonic> … = Vec… ` binding as read out of mnemonics.ml.
type refVecBinding struct {
	family string
	shape  vecShape
	op     string
}

var (
	// A binding starts at `let` in column 0 and runs to the next one. Split on the *start* rather
	// than matching a whole binding, because **the bodies wrap** — every one of the 21 memory
	// bindings puts its constructor on the following line:
	//
	//	let v128_load x align offset =
	//	  VecLoad (x, {ty = V128T; align; offset; pack = None})
	//
	// The authoring script matched those only because Python's `\s` spans newlines, which is luck
	// rather than design; #78/#80/#105 are the graves for reading a wrapped OCaml arm as though it
	// were one line, and `mllex`/`opgen` already paid for the lesson. Handled explicitly here.
	refLetStart = regexp.MustCompile(`(?m)^let `)

	// The lane shape a constructor carries, when it carries one:
	// `VecBinary (V128 (I8x16 V128Op.Swizzle))`. The whole-register families spell it
	// `V128 V128Op.Not` — no parenthesis, so no match, so the empty shape.
	refVecShape = regexp.MustCompile(`V128 \((I8x16|I16x8|I32x4|I64x2|F32x4|F64x2)\b`)

	// The operator constructor. Both spellings occur: `V128Op.Swizzle` and the local-open form
	// `V128Op.(Extract (i, S))`.
	refVecOp = regexp.MustCompile(`V128Op\.\(?([A-Za-z0-9_]+)`)

	refIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_']*`)
)

// parseRefVecBindings reads every vector mnemonic's family out of mnemonics.ml.
func parseRefVecBindings(tb testing.TB) map[string]refVecBinding {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.RefMnemonicsML)

	out := map[string]refVecBinding{}
	starts := refLetStart.FindAllStringIndex(src, -1)
	for i, s := range starts {
		end := len(src)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		chunk := src[s[1]:end] // everything after `let `, wrapped body included

		name := refIdent.FindString(chunk)
		eq := strings.Index(chunk, "=")
		if name == "" || eq < 0 {
			continue
		}
		// The definition's `=` is the first one: the parameters are bare names. The record
		// literals inside the memory bodies (`{ty = V128T; …}`) contain more, which is exactly why
		// the *first* is taken rather than the last.
		body := strings.TrimLeft(chunk[eq+1:], " \t\r\n")
		fam := refIdent.FindString(body)
		if !strings.HasPrefix(fam, "Vec") {
			continue
		}

		b := refVecBinding{family: fam}
		if m := refVecShape.FindStringSubmatch(body); m != nil {
			b.shape = vecShape(m[1])
		}
		if m := refVecOp.FindStringSubmatch(body); m != nil {
			b.op = m[1]
		}
		if prev, dup := out[name]; dup {
			tb.Errorf("%s binds %q twice (%v then %v); the join key is the mnemonic, so a "+
				"duplicate makes it ambiguous", testenv.RefMnemonicsML, name, prev, b)
		}
		out[name] = b
	}
	return out
}

// TestVecFamiliesMatchTheReference is vecfamily.go's promised control, in both directions.
//
// The table is a cache of this parse. Three failures, three messages, because they are three
// different defects: a **wrong family** types the instruction by the wrong arm (accept-direction,
// invisible on the board); a **missing row** declines an instruction the region has; and a **row
// the reference does not have** is a mnemonic this package believes in and nothing produces.
func TestVecFamiliesMatchTheReference(t *testing.T) {
	ref := parseRefVecBindings(t)

	// Vacuity, pinned exactly rather than floored. A regex that stopped matching yields an empty
	// map, and an empty map agrees with every row below by construction — the
	// comparison-against-nothing shape. Exact because the reference is fetched at a pin: upstream
	// adding a vector instruction is a fact for a reader to record, not churn to absorb. This is
	// also the assertion that bounds the region's *size*, which the opcode scan in
	// TestPrefixSIMDIsTheRegionBinaryDispatches cannot do on its own.
	const wantBindings = 256
	if len(ref) != wantBindings {
		t.Fatalf("parsed %d Vec* bindings from %s, want %d; either the parse stopped matching — "+
			"in which case every comparison below is against nothing — or upstream changed the "+
			"vector region, which is a finding either way",
			len(ref), testenv.RefMnemonicsML, wantBindings)
	}

	for name, row := range vecFamily {
		r, ok := ref[name]
		if !ok {
			t.Errorf("vecFamily has a row for %q, which %s does not bind; a mnemonic this table "+
				"believes in and the authority does not have is a rule with no subject",
				name, testenv.RefMnemonicsML)
			continue
		}
		if row.family != r.family {
			t.Errorf("%q: table says family %q, %s says %q — the family decides which valid.ml "+
				"arm types it, so this is a wrong *signature*, and a wrong signature in the "+
				"accept direction is invisible on the board (§9 G-3)",
				name, row.family, testenv.RefMnemonicsML, r.family)
		}
		if row.shape != r.shape {
			t.Errorf("%q: table says shape %q, %s says %q — the shape decides the lane type and "+
				"the lane bound", name, row.shape, testenv.RefMnemonicsML, r.shape)
		}
		if row.op != r.op {
			t.Errorf("%q: table says op %q, %s says %q — `check_vec_binop`'s shuffle bound is "+
				"keyed on this", name, row.op, testenv.RefMnemonicsML, r.op)
		}
	}

	for name, r := range ref {
		if _, ok := vecFamily[name]; !ok {
			t.Errorf("%s binds %q as %s and vecFamily has no row for it, so vecSignature declines "+
				"an instruction the region contains", testenv.RefMnemonicsML, name, r.family)
		}
	}
}

// TestPrefixSIMDIsTheRegionBinaryDispatches checks the two constants vec.go spells locally.
//
// `prefixSIMD` and `relaxedSIMDFirst` are literals in this package because `binary` spells its
// prefix bytes as literals too (see prefixSIMD's comment for why that non-change is deliberate) —
// and two packages agreeing by inspection is what this test replaces.
//
// # The scan's bound is honest about what it does not prove
//
// `binary` exports no region enumerator, so the region is found by asking `PrefixedOp` for every
// sub-opcode up to 0xFFFF. That cannot prove there is nothing above it — `Op` is a uint32, which is
// grave #101's whole point. What bounds the region's size is the *other* direction:
// TestVecFamiliesMatchTheReference pins mnemonics.ml at exactly 256 vector bindings and requires a
// table row for each, so an opcode arriving above the scan's reach shows up there as a binding with
// no row. The scan is the join, not the census.
//
// # The region is 275 rows and 256 instructions, and the 19 are not an error
//
// The first version of this asserted `len(region) == len(vecFamily)` and failed at 275 against 256.
// The nineteen are `optable.go`'s `illegal: true` rows — 0xfd 0x9a, 0xa2, 0xa5 and sixteen more —
// sub-opcodes the reference's decoder explicitly rejects, each cited to its `decode.ml` line. They
// are *in* the table on purpose, because "rejected by the authority" and "unknown to the table" are
// different facts (that row type's own comment), and `PrefixedOp` reports both as `ok` with the
// mnemonic left empty. So this package sees exactly what `mnemonic()` already warns about: `ok`
// means there is a row, not that there is a name.
//
// The split is therefore asserted, both counts pinned, and the nineteen get the check that actually
// matters here — `vecSignature` **declines** them rather than falling through to a zero `sig`. The
// decoder refusing them first is `binary`'s guarantee and not this package's, so the decline is
// worth having even though nothing should reach it: it is the same reasoning the relaxed-SIMD arm
// states for itself.
func TestPrefixSIMDIsTheRegionBinaryDispatches(t *testing.T) {
	const scanTo = 0xFFFF

	region := map[uint32]string{}
	var unnamed []uint32
	for op := uint32(0); op <= scanTo; op++ {
		name, _, ok := binary.PrefixedOp(prefixSIMD, op)
		if !ok {
			continue
		}
		if name == "" {
			unnamed = append(unnamed, op)
			continue
		}
		region[op] = name
	}

	// Both halves pinned exactly. A named row losing its name would move one count into the other,
	// and only comparing both catches that: an instruction demoted to a hole is silently declined,
	// which reads on the board as a slice boundary rather than as a lost row.
	const wantNamed, wantIllegal = 256, 19
	if len(region) != wantNamed || len(unnamed) != wantIllegal {
		t.Fatalf("the %#02x region has %d named rows and %d unnamed (illegal) rows, want %d and "+
			"%d; the unnamed ones are optable.go's `illegal: true` holes, so a shift between the "+
			"two counts is an instruction becoming a hole or the reverse",
			prefixSIMD, len(region), len(unnamed), wantNamed, wantIllegal)
	}
	if len(region) != len(vecFamily) {
		t.Fatalf("prefix %#02x names %d sub-opcodes and vecFamily has %d rows; the table is keyed "+
			"by mnemonic and the region by opcode, so a size disagreement means the join has a "+
			"hole on one side", prefixSIMD, len(region), len(vecFamily))
	}

	for _, op := range unnamed {
		if _, err := vecSignature(nil, binary.Instr{Prefix: prefixSIMD, Op: op}); err == nil {
			t.Errorf("vecSignature typed %#02x %#02x, which is one of the reference's illegal "+
				"holes; a hole that types is a hole that *accepts*", prefixSIMD, op)
		}
	}

	// The maximum, pinned: it is the fact grave #101 was found by printing, and a region whose top
	// opcode moved is a decoder-side change this package must hear about.
	var maxOp uint32
	for op := range region {
		maxOp = max(maxOp, op)
	}
	const wantMaxOp = 0x113
	if maxOp != wantMaxOp {
		t.Errorf("the %#02x region's top sub-opcode is %#x, want %#x (grave #101's measurement); "+
			"a region that grew past the scan is a finding, and one that shrank is a lost row",
			prefixSIMD, maxOp, wantMaxOp)
	}

	// Forward: every opcode the decoder can produce in this region has a family row, so
	// `vecSignature` never falls through to "in no vector family" for a decodable instruction.
	for op, name := range region {
		if _, ok := vecFamily[name]; !ok {
			t.Errorf("%#02x %#02x decodes as %q and vecFamily has no row for it", prefixSIMD, op, name)
		}
	}

	// `prefixSIMD` names the *right* region, not merely a region that works. A vector mnemonic
	// resolving under another prefix would mean the constant is a coincidence.
	for p := 1; p <= 0xFF; p++ {
		if byte(p) == prefixSIMD {
			continue
		}
		for op := uint32(0); op <= scanTo; op++ {
			name, _, ok := binary.PrefixedOp(byte(p), op)
			if !ok {
				continue
			}
			if _, isVec := vecFamily[name]; isVec {
				t.Errorf("prefix %#02x %#02x also resolves the vector mnemonic %q, so prefixSIMD "+
					"does not identify the region uniquely", byte(p), op, name)
			}
		}
	}

	// `relaxedSIMDFirst` against the table's own names. The gate map is in `binary` and this
	// package cannot see it, but the mnemonics carry the proposal in their spelling, so the
	// boundary is checkable here in both directions: nothing below it is relaxed, and everything
	// above it is.
	relaxedByName, relaxedByOp := 0, 0
	for op, name := range region {
		isRelaxedName := strings.Contains(name, "_relaxed_")
		isRelaxedOp := op >= relaxedSIMDFirst
		if isRelaxedName {
			relaxedByName++
		}
		if isRelaxedOp {
			relaxedByOp++
		}
		if isRelaxedName != isRelaxedOp {
			t.Errorf("%#02x %#02x is %q: the name says relaxed=%v and relaxedSIMDFirst (%#x) says "+
				"%v — a boundary that disagrees with the table either declines a core "+
				"instruction or types a gated one", prefixSIMD, op, name, isRelaxedName,
				relaxedSIMDFirst, isRelaxedOp)
		}
	}
	// Vacuity on the boundary itself: with zero relaxed rows the agreement above is between two
	// empty sets, which every wrong constant satisfies.
	const wantRelaxed = 20
	if relaxedByName != wantRelaxed || relaxedByOp != wantRelaxed {
		t.Errorf("the region has %d relaxed rows by name and %d by opcode, want %d each; a "+
			"boundary check over an empty set agrees with any constant",
			relaxedByName, relaxedByOp, wantRelaxed)
	}
}

// refVecArm is one `| Vec… ->` arm of valid.ml's instruction typing.
type refVecArm struct {
	line    int      // 1-based, the line the arm's `|` is on — what vec.go cites
	params  []string // the reference's own tokens, left of `-->`
	results []string // and right of it
}

// parseRefVecArms reads valid.ml's vector arms: where each is, and what it types.
func parseRefVecArms(tb testing.TB) map[string]refVecArm {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.RefValidML)

	// Two-space indent and a leading `|` is a match arm. `VecT` at :134 matches that shape too and
	// is a *type* rather than a family — excluded by requiring a `-->` in the body, which is derived
	// from what an arm does rather than from a list of names to skip (*derive the domain, never
	// enumerate it*).
	//
	// **The split is on every arm, not on the Vec arms**, and that is what makes the `-->` filter
	// mean anything. Splitting Vec-to-Vec gave `VecT` a chunk running from :134 to :663 — five
	// hundred lines of unrelated arms, `-->`s included — so the filter admitted it and the parse
	// found 21 arms where valid.ml has 20. A filter reading a body has to be given that body's
	// actual extent; bounding a chunk by the next *matching* start makes the excluded cases the
	// widest ones. Caught by the vacuity pin below, which is the whole reason it is exact.
	armRe := regexp.MustCompile(`(?m)^  \| ([A-Za-z][A-Za-z0-9_']*)`)

	ms := armRe.FindAllStringSubmatchIndex(src, -1)
	out := map[string]refVecArm{}
	for i, m := range ms {
		end := len(src)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		fam := src[m[2]:m[3]]
		if !strings.HasPrefix(fam, "Vec") {
			continue
		}
		body := src[m[0]:end]
		arrow := strings.Index(body, "-->")
		if arrow < 0 {
			continue
		}

		params, okL := bracketedBefore(body[:arrow])
		results, okR := bracketedAfter(body[arrow+len("-->"):])
		if !okL || !okR {
			tb.Errorf("%s: the %s arm has a `-->` whose bracketed sides did not parse; the "+
				"signature text is the authority for arity, so an unparsed arm is a silent gap",
				testenv.RefValidML, fam)
			continue
		}
		out[fam] = refVecArm{
			line:    strings.Count(src[:m[0]], "\n") + 1,
			params:  splitTopLevel(params),
			results: splitTopLevel(results),
		}
	}
	return out
}

// bracketedBefore returns the contents of the last balanced `[...]` group in s.
func bracketedBefore(s string) (string, bool) {
	end := strings.LastIndex(s, "]")
	if end < 0 {
		return "", false
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case ']':
			depth++
		case '[':
			depth--
			if depth == 0 {
				return s[i+1 : end], true
			}
		}
	}
	return "", false
}

// bracketedAfter returns the contents of the first balanced `[...]` group in s.
func bracketedAfter(s string) (string, bool) {
	start := strings.Index(s, "[")
	if start < 0 {
		return "", false
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start+1 : i], true
			}
		}
	}
	return "", false
}

// splitTopLevel splits an OCaml list body on `;` at parenthesis depth zero.
func splitTopLevel(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth, last := 0, 0
	for i := range len(s) {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[last:i]))
				last = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[last:]))
}

// citedArmLines reads the `// valid.ml:NNN` citations off vec.go's own case arms.
//
// Reading the source rather than restating the numbers in a table here, because a second table is a
// second transcription: the thing to check is the citation a reader of vec.go follows, so that
// exact text is the input. Two of these were off by one when the check first ran.
func citedArmLines(tb testing.TB) map[string]int {
	tb.Helper()
	src, err := os.ReadFile("vec.go")
	if err != nil {
		tb.Fatalf("reading this package's own source: %v", err)
	}

	nameRe := regexp.MustCompile(`"(Vec[A-Za-z]+)"`)
	lineRe := regexp.MustCompile(`valid\.ml:(\d+(?:,\d+)*)`)

	out := map[string]int{}
	for _, l := range strings.Split(string(src), "\n") {
		if !strings.Contains(l, `case "Vec`) {
			continue
		}
		names := nameRe.FindAllStringSubmatch(l, -1)
		cite := lineRe.FindStringSubmatch(l)
		if cite == nil {
			tb.Errorf("vec.go has a vector case arm with no valid.ml citation: %q — every arm is "+
				"transcribed from the authority and says which line it came from", strings.TrimSpace(l))
			continue
		}
		nums := strings.Split(cite[1], ",")
		if len(names) != len(nums) {
			tb.Errorf("vec.go arm %q lists %d families and cites %d lines; the citation is "+
				"positional, so an unequal count means at least one family points at another's arm",
				strings.TrimSpace(l), len(names), len(nums))
			continue
		}
		for i, n := range names {
			v, convErr := strconv.Atoi(nums[i])
			if convErr != nil {
				tb.Errorf("vec.go cites %q as a valid.ml line: %v", nums[i], convErr)
				continue
			}
			out[n[1]] = v
		}
	}
	return out
}

// TestVecSignaturesMatchTheReferencesArms is the family-to-signature step's control.
//
// vec.go transcribes twenty arms by hand and argues that twenty is sound where 236 rows are not.
// This is the argument's other half: the transcription is checked, three ways.
//
//  1. **The arm set matches**, both directions — a family valid.ml types and vec.go has no case for
//     would be typed `() -> ()` and *accepted*, which is the one door a `switch` leaves open.
//  2. **The arity matches**, counted off the reference's own `[t; t] --> [t]` text and compared
//     against what `vecSignature` actually returns for a real opcode of that family.
//  3. **The types match, where the reference's tokens resolve** — which covers `VecCompare`
//     returning `t` rather than `i32` and `VecShift`'s `i32` count, the two arms vec.go names as
//     most likely to be written wrong from analogy.
//
// And the *citations* are checked, because a comment saying `valid.ml:952` when the arm is at 953
// sends a reader to the wrong rule, silently.
//
// # What (3) does not reach, stated rather than implied
//
// Seven of the type tokens are `t1`/`t2` in the three lane arms, where the reference binds them
// with `type_vec_lane` and the text alone cannot say which side is the lane. Those are
// TestLaneFactsMatchTheReference's subject and the witnesses'. Four more are VecTernary's, whose
// arm no input reaches — see the pin below. Both counts are exact: a floor would let the resolver
// quietly stop recognizing tokens, and *only an exact count sees a small silent loss*.
func TestVecSignaturesMatchTheReferencesArms(t *testing.T) {
	arms := parseRefVecArms(t)
	cited := citedArmLines(t)

	// Vacuity, and the count is the *authority's*: valid.ml has twenty vector arms, so a parse
	// that found fewer is comparing against nothing.
	const wantArms = 20
	if len(arms) != wantArms {
		t.Fatalf("parsed %d Vec* arms from %s, want %d; the parse is the input to every check "+
			"below", len(arms), testenv.RefValidML, wantArms)
	}

	// (1) Both directions over the arm set.
	for fam := range arms {
		if _, ok := cited[fam]; !ok {
			t.Errorf("%s types %s and vec.go has no case for it; the fall-through declines, so "+
				"this is a decline rather than a wrong accept — but it is a rule the region "+
				"needs", testenv.RefValidML, fam)
		}
	}
	for fam, line := range cited {
		arm, ok := arms[fam]
		if !ok {
			t.Errorf("vec.go types %s (citing %s:%d) and the reference has no such arm", fam,
				testenv.RefValidML, line)
			continue
		}
		if line != arm.line {
			t.Errorf("vec.go cites %s:%d for %s, whose arm is at :%d — a citation off by %d sends "+
				"a reader to a different rule and reads as checked", testenv.RefValidML, line,
				fam, arm.line, arm.line-line)
		}
	}

	// (2) and (3). One representative opcode per family: the smallest, which is a core opcode for
	// every family but one.
	//
	// **`VecTernary` has no core opcode at all**, and the first version of this assumed it did —
	// "every family with a relaxed member has a core member too", which is false and was caught by
	// asserting it rather than by relying on it. Its smallest opcode is 0x105, because the lanewise
	// ternary operators are *entirely* a relaxed-SIMD addition: core SIMD's only three-operand
	// instruction is `v128.bitselect`, which is `VecTernaryBits` and carries no shape. So vec.go's
	// VecTernary arm is transcribed correctly from valid.ml:902 and is **unreachable until the
	// relaxed gate flips** — `vecSignature`'s relaxed guard runs before the family lookup, so no
	// input reaches it.
	//
	// That is recorded here rather than treated as a defect, and the set is pinned literally so the
	// unreachability is *declared* instead of silent: a second family joining it would be a real
	// finding, and VecTernary leaving it means core SIMD grew a lanewise ternary operator.
	// The region's own top opcode, pinned by TestPrefixSIMDIsTheRegionBinaryDispatches rather than
	// spelled twice.
	const relaxedSIMDLast = 0x113

	rep := map[string]uint32{}
	for op := range uint32(relaxedSIMDLast + 1) {
		name, _, ok := binary.PrefixedOp(prefixSIMD, op)
		if !ok || name == "" {
			continue
		}
		fam := vecFamily[name].family
		if cur, seen := rep[fam]; !seen || op < cur {
			rep[fam] = op
		}
	}

	relaxedOnly := map[string]bool{}
	for fam, op := range rep {
		if op >= relaxedSIMDFirst {
			relaxedOnly[fam] = true
		}
	}
	if got, want := sortedKeys(relaxedOnly), []string{"VecTernary"}; !slices.Equal(got, want) {
		t.Errorf("the families whose only opcodes are relaxed SIMD are %v, want %v; a family "+
			"arriving here has an arm this slice cannot exercise, and one leaving means core SIMD "+
			"gained an instruction of that shape", got, want)
	}

	// A memory, because four of the twenty arms read the named memory's address type — memory 0
	// here, that being the only memory a one-memory module has and the index an unindexed memarg
	// names. Index type i32, which is what makes `NumT (numtype_of_addrtype at)` resolvable below.
	mod := &binary.Module{Memories: []binary.Memory{{}}}

	resolved, unresolved := 0, 0
	for fam, arm := range arms {
		op, ok := rep[fam]
		if !ok {
			t.Errorf("no opcode in the %#02x region has family %s, so its arm cannot be exercised",
				prefixSIMD, fam)
			continue
		}
		if relaxedOnly[fam] {
			// Declined by the gate arm before the family is even looked up, so there is no
			// signature to compare. Pinned as a set above; skipped here rather than read as a
			// missing rule.
			continue
		}

		// Zero immediates: lane 0 is in bounds for every shape and an all-zero shuffle mask
		// selects lane 0 sixteen times, so no rule fires and the signature is what is measured.
		s, err := vecSignature(mod, binary.Instr{Prefix: prefixSIMD, Op: op})
		if err != nil {
			t.Errorf("family %s (%#02x %#02x): vecSignature returned %v, but the reference types "+
				"it at %s:%d", fam, prefixSIMD, op, err, testenv.RefValidML, arm.line)
			continue
		}

		if len(s.params) != len(arm.params) || len(s.results) != len(arm.results) {
			t.Errorf("family %s: vecSignature is %d -> %d, %s:%d is %d -> %d (%v --> %v)",
				fam, len(s.params), len(s.results), testenv.RefValidML, arm.line,
				len(arm.params), len(arm.results), arm.params, arm.results)
			continue
		}

		for i, tok := range arm.params {
			want, ok := resolveRefType(tok)
			if !ok {
				unresolved++
				continue
			}
			resolved++
			if s.params[i] != want {
				t.Errorf("family %s param %d: vecSignature says %s, %s:%d says %q (%s)",
					fam, i, s.params[i], testenv.RefValidML, arm.line, tok, want)
			}
		}
		for i, tok := range arm.results {
			want, ok := resolveRefType(tok)
			if !ok {
				unresolved++
				continue
			}
			resolved++
			if s.results[i] != want {
				t.Errorf("family %s result %d: vecSignature says %s, %s:%d says %q (%s)",
					fam, i, s.results[i], testenv.RefValidML, arm.line, tok, want)
			}
		}
	}

	// Coverage, pinned exactly in both halves. `resolved` is what (3) actually checked and
	// `unresolved` is the declared blind spot; a resolver that stopped recognizing `t` would send
	// 42 tokens into the second bucket and pass every comparison it stopped making.
	const wantResolved, wantUnresolved = 38, 7
	if resolved != wantResolved || unresolved != wantUnresolved {
		t.Errorf("the type check resolved %d of the reference's tokens and could not resolve %d, "+
			"want %d and %d; an unexplained shift means the resolver's domain moved, and a "+
			"comparison it stopped making looks identical to one that agreed",
			resolved, unresolved, wantResolved, wantUnresolved)
	}
}

// resolveRefType maps one of valid.ml's type tokens to this package's ValType, where it can.
//
// Partial by construction, and the `ok=false` cases are the point: `t1` and `t2` in the lane arms
// are bound by `type_vec_lane`, so the text says nothing about which of them is the lane. Returning
// false there is honest; guessing v128 would make the check agree with a wrong lane type.
func resolveRefType(tok string) (binary.ValType, bool) {
	switch tok {
	case "t", "VecT t":
		// `let t = VecT (type_vec …)` in every whole-register arm — bare `t` is the register.
		return binary.V128, true
	case "NumT I32T":
		return binary.I32, true
	case "NumT (numtype_of_addrtype at)":
		// Memory 0's index type. i32 for the module these checks build; a memory64 module would
		// make this i64, which is why `addrType` reads it rather than assuming.
		return binary.I32, true
	}
	return binary.ValType{}, false
}

// TestLaneFactsMatchTheReference checks laneType and numLanes against v128.ml.
//
// These two functions are the part of a vector signature that a family cannot supply: `VecSplat`'s
// operand is `NumT (type_vec_lane splatop)`, and only the shape says what that is. They are also
// the part most likely to be written wrong from memory — `type_of_lane` maps *three* shapes to
// `i32`, because a lane narrower than a machine word is extracted into one, and the plausible wrong
// version invents an `i8` Wasm does not have.
//
// The shape domain is derived from `vecFamily` rather than enumerated here, so the six constants in
// vec.go are not restated: the table is checked against mnemonics.ml, and this checks the table's
// shapes against v128.ml, which closes the loop without a third list to keep in step.
func TestLaneFactsMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefV128ML)

	lanesRe := regexp.MustCompile(`\|\s+([A-Za-z0-9]+) _ -> (\d+)`)
	refLanes := map[vecShape]uint64{}
	for _, m := range lanesRe.FindAllStringSubmatch(section(src, "let num_lanes"), -1) {
		n, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			t.Fatalf("%s: num_lanes arm %q has an unparseable count: %v", testenv.RefV128ML, m[0], err)
		}
		refLanes[vecShape(m[1])] = n
	}

	// `| I8x16 _ | I16x8 _ | I32x4 _ -> Types.I32T` — one arm, three shapes, which is the row a
	// reader gets wrong.
	typesRe := regexp.MustCompile(`(?m)^\s*\|\s*(.+?)\s*->\s*Types\.([A-Za-z0-9]+)\s*$`)
	refLaneType := map[vecShape]string{}
	for _, m := range typesRe.FindAllStringSubmatch(section(src, "let type_of_lane"), -1) {
		for _, pat := range strings.Split(m[1], "|") {
			shape := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(pat), "_"))
			if shape == "" {
				continue
			}
			refLaneType[vecShape(strings.TrimSpace(shape))] = m[2]
		}
	}

	const wantShapes = 6
	if len(refLanes) != wantShapes || len(refLaneType) != wantShapes {
		t.Fatalf("parsed %d num_lanes arms and %d type_of_lane shapes from %s, want %d each; "+
			"either parse stopping short makes the comparisons below vacuous",
			len(refLanes), len(refLaneType), testenv.RefV128ML, wantShapes)
	}

	// The domain, derived from the table this package already checks against the reference.
	mine := map[vecShape]bool{}
	for _, row := range vecFamily {
		if row.shape != shapeNone {
			mine[row.shape] = true
		}
	}
	if len(mine) != wantShapes {
		t.Fatalf("vecFamily carries %d distinct shapes, want %d — the domain of this check comes "+
			"from the table, so a table with fewer shapes narrows the check silently",
			len(mine), wantShapes)
	}

	for s := range mine {
		want, ok := refLanes[s]
		if !ok {
			t.Errorf("vecFamily carries shape %q and %s's num_lanes has no arm for it", s, testenv.RefV128ML)
			continue
		}
		got, ok := numLanes(s)
		if !ok {
			t.Errorf("numLanes(%q) has no answer, so every lane bound on that shape refuses "+
				"rather than bounding", s)
			continue
		}
		if got != want {
			t.Errorf("numLanes(%q) = %d, %s says %d — the lane bound is `lane < num_lanes`, so a "+
				"count that is too large accepts an out-of-range lane index", s, got,
				testenv.RefV128ML, want)
		}
	}

	for s := range mine {
		want, ok := refLaneType[s]
		if !ok {
			t.Errorf("vecFamily carries shape %q and %s's type_of_lane has no arm for it", s, testenv.RefV128ML)
			continue
		}
		got, ok := laneType(s)
		if !ok {
			t.Errorf("laneType(%q) has no answer, so the splat/extract/replace arms decline it", s)
			continue
		}
		// Derived rather than mapped: `I32T` is `i32` with the tag stripped and lowercased, which
		// is exactly what ValType prints. A transcribed `Types.I32T -> binary.I32` table here
		// would be a third place to be wrong in the same direction as the code it checks.
		if wantName := strings.ToLower(strings.TrimSuffix(want, "T")); got.String() != wantName {
			t.Errorf("laneType(%q) = %s, %s says %s (%s)", s, got, testenv.RefV128ML, wantName, want)
		}
	}

	for s := range refLanes {
		if !mine[s] {
			t.Errorf("%s has shape %q and no row of vecFamily carries it; a shape the authority "+
				"has and this package never sees is a region hole", testenv.RefV128ML, s)
		}
	}

	// The shape-less families must not get a lane fact. `VecUnaryBits` carries no shape, and a
	// `laneType(shapeNone)` that answered would give `v128.not` a lane type — which is how a
	// bound with nothing to bound against silently answers "in range".
	if _, ok := laneType(shapeNone); ok {
		t.Error("laneType(shapeNone) answered; the whole-register families carry no shape, so an " +
			"answer here is a fact invented for an instruction that has none")
	}
	if _, ok := numLanes(shapeNone); ok {
		t.Error("numLanes(shapeNone) answered; a lane count for a shape-less family bounds a lane " +
			"index that does not exist")
	}
}

// TestLaneIndexCitationsResolveToTheReferencesSites checks the prose citations for one string.
//
// The twenty `case` arms are checked positionally by TestVecSignaturesMatchTheReferencesArms, and
// the lane-index bounds are not arms — they are cited in three doc comments, by hand, as bare line
// numbers. Both of those hand-written lists had an error: `checkLaneIndex` cited :952, which is the
// `let t2` *before* the bound, and ErrInvalidLaneIndex's list mixed a `require` line in with four
// string lines while describing itself as five sites producing one string.
//
// # Why this is checkable at all, and where it stops
//
// A line number in a comment is normally unfalsifiable — the checker would need to know what the
// line ought to say. Here it does: every one of these sites exists *because* it produces
// `invalid lane index`, so that string is the property, and the legal set of lines is derived from
// the reference rather than listed here. A wrapped `require` puts the string on the following line,
// so a citation to either half resolves; anything else does not.
//
// It does not check that the *right* site is cited — :677 where :684 was meant would pass. That is
// the honest limit of a string-keyed check on five sites producing the same string, which is
// ErrInvalidLaneIndex's own subject, and it is stated rather than papered over.
//
// # A point citation and a range citation are different claims, and the predicate says so
//
// Three earlier predicates over-matched, and they taught the same thing three times. Keying on the
// word "lane" per *line* claimed the four `case "Vec…Lane"` arms, which say "lane index bounded"
// beside their own arm's line. Keying on comment *blocks* then claimed `valid.ml:373-378`,
// `:906-908` and `:938-955` — the section and arm-rationale comments, which mention lanes and cite
// **ranges**. Distinguishing points from ranges fixed those two, and the third arrived with #306:
// ErrAlignmentTooLarge's block cites `check_memop`'s alignment `require` and mentions lanes twice,
// because it names ErrInvalidLaneIndex as the precedent for its own shared-sentinel shape — so a
// correct citation to a different rule was reported as a lane-index citation to the wrong line.
//
// A **topic word is not a subject**, which is what all three had in common: prose about a rule
// mentions its neighbours, so any keyword sitting in a block is evidence about vocabulary and not
// about what the block documents. The predicate is now derived instead —
// `messageKeyedPointCitations` reads each block's *subject* off the declaration below it and keys on
// the sentinel that subject produces. Nothing in this test names a word from the message; the
// message comes from `errors.New` and the legal lines come from the reference.
//
// The point/range distinction stays, and it is syntactic: a **range** cites an arm or a region, a
// **point** cites a statement, and only a point can be off by one the way :952 was. Trailing
// comments on code lines are the arm test's subject by construction — a doc block starts with `//`
// in the leading position and a `case … // valid.ml:897` does not.
func TestLaneIndexCitationsResolveToTheReferencesSites(t *testing.T) {
	// Nine legal lines: five carrying the string, plus the four wrapped `require`s above them.
	// **The pin was wrong the first time it ran** — 9, not 7: every `require` site wraps and only
	// :377's `error at` fits on one line. Guessing would have set it to whatever the parse
	// produced; asserting it made the parse state a fact worth knowing.
	//
	// Seven points: checkLaneIndex's 2 (:946,:953), checkPackedLaneIndex's 2 (:676,:683) and
	// ErrInvalidLaneIndex's 3 (:377,:947,:954).
	messageKeyedPointCitations(t, "invalid lane index", 9, 7)
}

// TestAlignmentCitationsResolveToTheReferencesSites is the same check for #306's rule.
//
// Its own test rather than a second message inside the one above, because the *name* of a control
// is a claim about its population: a test called "lane index citations" that also judged alignment
// citations would be checked against its own case labels instead of against the partition it names.
// Both bodies are one call, and the mechanism they share is where the generalization lives.
func TestAlignmentCitationsResolveToTheReferencesSites(t *testing.T) {
	// Two legal lines — `valid.ml:388`'s `require` and its wrapped string on `:389` — and one
	// point, ErrAlignmentTooLarge's `:388`. The `check_memop` and `check_pack` citations in
	// align.go are *not* here and cannot be: they point at a `let` and at a rule producing a
	// different string, so nothing keys them. That residue is stated in the helper's doc.
	messageKeyedPointCitations(t, "alignment must not be larger than natural", 2, 1)
}

// messageKeyedPointCitations checks every `valid.ml:NNN` point citation whose comment block
// documents something that produces `message`, against the reference's own lines for it.
//
// # What keys a block, and what the residue is
//
// A block's subject is the declaration immediately below it: a sentinel (`ErrX = errors.New("…")`)
// contributes its own string, and a `func` contributes the strings of every `Err…`/`err…` sentinel
// its body names. So the population is derived twice over — from Go's declaration order and from
// `errors.New` — and no word of the message appears in this file.
//
// **Not every point citation is keyable, and that is stated rather than hidden.** A block whose
// subject produces no reference message (`checkMemop`'s, whose citation is a `let`) or a file-level
// header block (align.go's, which cites four statements across three rules) has no message to key
// on, so its points are range-checked by the caller-independent well-formedness pass and no
// further. Coverage is a claim an instrument cannot check about itself; this one's domain is
// "citations inside a block whose subject produces a reference message", and the honest form of
// saying so is naming what falls outside it.
//
// wantSites and wantPoints are pinned in both directions. An empty site set would fail every
// citation rather than pass it, which is the safe direction — but a *short* set rejects a correct
// citation and reads as a citation defect, and zero points means the comments were reworded and
// this check is agreeing with nothing.
func messageKeyedPointCitations(tb testing.TB, message string, wantSites, wantPoints int) {
	tb.Helper()
	ref := testenv.RequireSpecRef(tb, testenv.RefValidML)
	lines := strings.Split(ref, "\n")

	// The legal set, derived: every line carrying the quoted string, plus the `require`/`error`
	// above it when the string is that statement's wrapped continuation.
	quoted := `"` + message + `"`
	sites := map[int]bool{}
	for i, l := range lines {
		if !strings.Contains(l, quoted) {
			continue
		}
		sites[i+1] = true
		if i > 0 && !strings.Contains(l, "require") && strings.Contains(lines[i-1], "require") {
			sites[i] = true
		}
	}
	if len(sites) != wantSites {
		tb.Fatalf("derived %d legal line numbers for %s from %s, want %d; the count is the string "+
			"lines plus the wrapped statements above them, and a different one means the "+
			"reference's sites moved", len(sites), quoted, testenv.RefValidML, wantSites)
	}

	points := 0
	for _, file := range citationFiles {
		for _, b := range docBlocks(tb, file) {
			if !slices.Contains(b.messages, message) {
				continue
			}
			for _, n := range b.points {
				points++
				if !sites[n] {
					tb.Errorf("%s:%d documents %q and cites %s:%d, and that line is %q — the "+
						"message is not produced there, so the citation points a reader at the "+
						"wrong statement while reading as checked",
						file, b.start, message, testenv.RefValidML, n, strings.TrimSpace(lines[n-1]))
				}
			}
		}
	}
	if points != wantPoints {
		tb.Errorf("checked %d point citation(s) for %q, want %d — recount and re-pin if a comment "+
			"was reworded, because a check over no citations passes without asserting anything",
			points, message, wantPoints)
	}
}

// citationFiles is the package's non-test source, which is the domain both citation checks walk.
//
// **It was a four-name list under that same sentence, and the sentence was false.** `align.go`,
// `sig.go`, `vec.go` and `validate.go` were named; `bulk.go`, `instr.go`, `stack.go` and — since the
// limits slice — `module.go` were not, so 26 of the package's 59 reference citations sat outside an
// instrument whose doc comment called its domain "the package's non-test source". The defence for
// enumerating was that "a new file arriving should fail the range count and get that reading rather
// than be swept in silently", and that is exactly what the enumeration cannot do: the count is
// summed *over the list*, so a file not on the list contributes nothing and moves no pin. A guard
// whose trigger predicate under-matches its space fails silently by construction, and this one
// under-matched by half. (Grave: #333.)
//
// Derived now, so the trigger is sound: a new file's citations join the totals, both pins move, and
// the read the old comment asked for is what re-pinning them requires. `packageSentinels` widens with
// it, which is its own repair — `module.go`'s three sentinels were invisible to every message-keyed
// check in this file for as long as the list omitted the file that declares them.
var citationFiles = func() []string {
	ents, err := filepath.Glob("*.go")
	if err != nil {
		panic(err)
	}
	var files []string
	for _, f := range ents {
		if !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	return files
}()

// docBlock is one leading-`//` comment run, its point citations, and the reference messages its
// subject declaration produces.
type docBlock struct {
	start    int
	points   []int
	ranges   [][2]int
	messages []string
}

var (
	pointRe    = regexp.MustCompile(`valid\.ml:(\d+(?:,\d+)*)(?:\b|$)`)
	rangeRe    = regexp.MustCompile(`valid\.ml:(\d+)-(\d+)`)
	sentinelRe = regexp.MustCompile(`^\s*(?:var\s+)?([Ee]rr\w*)\s*=\s*errors\.New\("([^"]*)"\)`)
	errIdentRe = regexp.MustCompile(`\b([Ee]rr[A-Z]\w*)\b`)
)

// docBlocks parses one file's comment blocks: where each is, which reference lines it cites as
// points, and which messages its subject produces.
func docBlocks(tb testing.TB, file string) []docBlock {
	tb.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		tb.Fatalf("reading %s: %v", file, err)
	}
	fileLines := strings.Split(string(src), "\n")
	sentinels := packageSentinels(fileLines)

	var blocks []docBlock
	for i := 0; i < len(fileLines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(fileLines[i]), "//") {
			continue
		}
		j := i
		for j < len(fileLines) && strings.HasPrefix(strings.TrimSpace(fileLines[j]), "//") {
			j++
		}
		b := docBlock{start: i + 1}
		joined := strings.Join(fileLines[i:j], "\n")
		// Ranges are stripped before points are read, so a range's leading number is never taken
		// for a point. They are *collected* first, because since #310 a range whose subject produces
		// a message is checkable for content and not only for well-formedness.
		for _, m := range rangeRe.FindAllStringSubmatch(joined, -1) {
			lo, loErr := strconv.Atoi(m[1])
			hi, hiErr := strconv.Atoi(m[2])
			if loErr != nil || hiErr != nil {
				tb.Errorf("%s:%d cites %q as a valid.ml range", file, b.start, m[0])
				continue
			}
			b.ranges = append(b.ranges, [2]int{lo, hi})
		}
		block := rangeRe.ReplaceAllString(joined, "")
		i = j - 1

		for _, m := range pointRe.FindAllStringSubmatch(block, -1) {
			for _, s := range strings.Split(m[1], ",") {
				n, convErr := strconv.Atoi(s)
				if convErr != nil {
					tb.Errorf("%s:%d cites %q as a valid.ml line: %v", file, b.start, s, convErr)
					continue
				}
				b.points = append(b.points, n)
			}
		}
		if len(b.points) > 0 || len(b.ranges) > 0 {
			b.messages = subjectMessages(fileLines, j, sentinels)
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// subjectMessages reads the messages produced by the declaration starting at line index `at`.
//
// A sentinel declaration contributes its own message; a `func` contributes every sentinel its body
// names, which is what puts `checkLaneIndex`'s doc block into ErrInvalidLaneIndex's population
// without the block having to mention the sentinel. Anything else — a `type`, a `const`, a package
// clause under a file header — contributes nothing, and its citations are the unkeyed residue.
func subjectMessages(fileLines []string, at int, sentinels map[string]string) []string {
	for ; at < len(fileLines); at++ {
		line := fileLines[at]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := sentinelRe.FindStringSubmatch(line); m != nil {
			return []string{m[2]}
		}
		if !strings.HasPrefix(line, "func ") {
			return nil
		}
		// The body, by brace balance from the `func` line. A signature spanning lines is fine:
		// the count only closes once the body does.
		var msgs []string
		depth := 0
		for ; at < len(fileLines); at++ {
			depth += strings.Count(fileLines[at], "{") - strings.Count(fileLines[at], "}")
			for _, id := range errIdentRe.FindAllStringSubmatch(fileLines[at], -1) {
				if msg, ok := sentinels[id[1]]; ok && !slices.Contains(msgs, msg) {
					msgs = append(msgs, msg)
				}
			}
			if depth == 0 && at > 0 && strings.Contains(fileLines[at], "}") {
				break
			}
		}
		return msgs
	}
	return nil
}

// packageSentinels maps sentinel identifiers to their messages, reading every `errors.New` in the
// package's non-test source rather than only in the file being walked — `vec.go`'s functions name
// sentinels `validate.go` declares.
func packageSentinels(_ []string) map[string]string {
	sentinels := map[string]string{}
	for _, file := range citationFiles {
		src, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			if m := sentinelRe.FindStringSubmatch(line); m != nil {
				sentinels[m[1]] = m[2]
			}
		}
	}
	return sentinels
}

// TestReferenceRangeCitationsAreWellFormed is the other half of the point/range split: a range
// cites an arm or a region, so what this check can say about it is that it is a range inside the
// file.
//
// **It bounds-checks and nothing more, and that is written down here because the difference was
// asked about.** A range citation whose target text is never read retargets in silence the day
// anything above it moves — the same defect class as an issue citation resolving to a real issue with
// the wrong title. Two other instruments cover what this one cannot, and neither is this:
// `TestReferenceRangeCitationsContainTheirSubjectsSite` below reads the *text* inside each range
// whose subject gives it a key, and `TestReferenceStillReadsMemoryZeroForTheOffsetBound`
// (`offset_test.go`) matches three literal patterns in the reference rather than any line number,
// which is why #310's ruling rests on that test and not on this one.
//
// Its count is pinned for the reason the point counts are — it is what keeps this from quietly
// becoming the branch that never runs — and the pin is what makes a new file in `citationFiles`
// arrive loudly rather than be swept in.
func TestReferenceRangeCitationsAreWellFormed(t *testing.T) {
	ref := testenv.RequireSpecRef(t, testenv.RefValidML)
	n := len(strings.Split(ref, "\n"))

	ranges := 0
	for _, file := range citationFiles {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range rangeRe.FindAllStringSubmatch(line, -1) {
				lo, _ := strconv.Atoi(m[1])
				hi, _ := strconv.Atoi(m[2])
				ranges++
				if lo < 1 || hi <= lo || hi > n {
					t.Errorf("%s:%d cites the range %s:%d-%d, which is not a range inside a "+
						"%d-line file", file, i+1, testenv.RefValidML, lo, hi, n)
				}
			}
		}
	}
	// Twenty-seven, by file, each read when the domain stopped being a four-name list:
	//
	//	align.go   :380-394 `check_memop`, :390-393 checkOffset's own bound (#310)
	//	bulk.go    :641-647 `TableInit`'s two element types, :618-651 the table arms as a region
	//	instr.go   :470-475 `BrTable`, :442-446 `Select (Some ts)`, :131-136 `check_valtype`
	//	module.go  :96-105 `check_limits`, :104-105 its shared min/max message, :200-208 and :210-218
	//	           the two type checks, :202-206 the memory ranges, :40-49 the `lookup` family and
	//	           :40-42 `lookup` itself, :1151-1164 `check_module`'s pre-body phases, :1073-1084
	//	           `check_datamode`, :1128-1137 `check_export`, :1142-1149 `check_names`
	//	sig.go     (none: its one citation is a point)
	//	stack.go   :966-972 `check_block`'s end-of-block check
	//	validate.go :41-42 `lookup`, :442-446 `Select (Some ts)` (slice 4/#294), :1168-1169 the
	//	           export phase's placement after every body
	//	vec.go     :885-937, :906-908, :938-955, :663-686 four section/rationale comments, :373-378
	//	           `check_vec_binop`
	//
	// Five more with #343's GC-subtyping slice, and note where they are *not*: `match.go` is a port of
	// `match.ml`, so almost every range in it cites that file and this instrument does not read it. The
	// five are the slice's `valid.ml` half —
	//
	//	match.go   :113-121 `check_typeuse`, named to say the index check is *not* the relation's
	//	module.go  :165-176 `check_subtype_sub`, :178-189 `check_rectype` twice (the rule and the
	//	           phase-order comment that places it)
	//	validate.go :170-174 the finality rule and the relation, at ErrSubType's sentinel
	//
	// Six more with #359's reference-type slice, all in `ref.go`: five rules and the declaration
	// sentinel, with the address-and-element rule cited once for its two opcodes. A seventh range in
	// that file cites `free.ml` and is invisible here by construction — the regexes are anchored on
	// the reference's validation module, so a citation to its free-variable pass is well-formed prose
	// that no instrument in this package reads. That is a **coverage claim this test cannot check
	// about itself**, recorded rather than fixed: widening the anchor is a second reference file's
	// worth of line-number churn, and the one citation that needs it is named in `declaredFuncs`.
	// Twenty-six more with slice 7's GC-instruction port, **all of them in `gc.go` and that measured
	// rather than assumed**: the per-file counts are 2/2/26/3/1/17/6/0/1/4/5/0 in `citationFiles`
	// order, which sums to the figure below and leaves the other eleven files unmoved. Worth stating
	// because the slice also *edited* two files in this list — `instr.go`'s dispatch comment and
	// `validate.go`'s declined-regions sentence, both of which quote the boundary they retired — and a
	// pin that moved by 26 while two unrelated files were being rewritten is exactly where an
	// attribution gets assumed. Neither edit added a range citation, and the per-file split is how
	// that is known instead of hoped.
	//
	// **Seven more with slice 8, every one of them in `ref.go`, measured per file the way slice 7's
	// were**: the counts are 2/2/26/3/1/17/13/0/1/4/5/0 in `citationFiles` order, so `ref.go` went 6 to
	// 13 and the other eleven files did not move — including `validate.go` and `stack.go`, which the
	// slice edited to retire a false boundary declaration and to name the second bottom. An eighth
	// citation *moved* rather than arrived, from `refIsNull`'s block into the new `peekRef`'s, and this
	// count cannot see it: a range that changes which doc block it sits in is the same range. It is
	// visible in `TestReferenceRangeCitationsContainTheirSubjectsSite`'s residue below, which counts
	// ranges *per block*, and naming it here is what keeps that pin's +3 from reading as an arithmetic
	// slip against this pin's +7.
	//
	// **Four more with slice 9, all in the new `tailcall.go`, and the per-file split is the whole
	// attribution**: 2/2/26/3/1/17/13/0/1/**4**/4/5/0 in `citationFiles` order — the list grew a
	// thirteenth entry rather than any existing count moving. That matters because the slice also
	// edited `instr.go` (two dispatch arms, and a rewritten `callIndirect` doc block carrying grave
	// #390) and `ref.go`'s header, both of which are in this domain: `instr.go` holds at 3 and `ref.go`
	// at 13, so the +4 is the new file's and not a rewrite's. The four are `:544-550` (`ReturnCall`),
	// `:560-565` (the element-type require #390 restores), `:560-570` (`ReturnCallIndirect` as a
	// region) and `:546-549` (the result-type require).
	//
	// A fifth range citation in that file is **invisible to this instrument by construction**, and it
	// is named for the reason the `free.ml` paragraph above names its own gap: `requireTailResults`
	// cites the reference's two textually identical requires as ``:546-549`, `:566-569``, and the second
	// is a bare continuation with no `valid.ml` prefix, so `rangeRe` does not see it. Nothing
	// bounds-checks it. The shape is worth a sentence because the abbreviation is idiomatic in this
	// package's prose and every use of it is a citation outside every sweep's domain.
	//
	// They are not enumerated here, and that is a deliberate break with the four paragraphs above.
	// Twenty-six lines of `file :n-m subject` would be a second copy of the citations themselves,
	// maintained by hand, drifting on the first renumbering — the fixed-point trap
	// `citation_subject_test.go`'s header walked into at 30-31-32, at a scale where it is certain
	// rather than likely. What reads them is `TestRangeCitationSubjectsAreReadFromTheReference`, which
	// resolves every range against the reference and needs no list here to do it.
	const wantRanges = 78
	if ranges != wantRanges {
		t.Errorf("checked %d range citation(s) across %v, want %d — recount and re-pin, and if a "+
			"file was added to citationFiles, read its point citations too",
			ranges, citationFiles, wantRanges)
	}
}

// TestOffsetCitationsResolveToTheReferencesSites is #310's rule joining the message-keyed check.
//
// Its own test rather than a third message inside either of the two above, for the reason stated at
// `TestAlignmentCitationsResolveToTheReferencesSites`: a control's name is a claim about its
// population, and a test judging three messages under one rule's name would be checked against its
// own case labels instead of the partition it names.
//
// **The point that needed checking was `ErrOffsetOutOfRange`'s.** The rule's line citations went in
// unread by any instrument — the range pin bounds-checks, and no message-keyed test existed for this
// string — which is the gap that let the range citation land as `:390-392` when the quoted code block
// above `checkOffset` runs to `:393`. Corrected in the same PR that added this.
func TestOffsetCitationsResolveToTheReferencesSites(t *testing.T) {
	// Two legal lines — `valid.ml:392`'s `require` and its wrapped string on `:393` — and one point,
	// ErrOffsetOutOfRange's `:392`. `checkOffset`'s own block cites the *range* and so is judged by
	// the range check below; `align.go`'s file header writes its citation as a bare `:392` with no
	// `valid.ml:` prefix, which is not citation-shaped to `pointRe` and is unkeyable anyway.
	messageKeyedPointCitations(t, "offset out of range", 2, 1)
}

// TestReferenceRangeCitationsContainTheirSubjectsSite reads inside each range citation whose subject
// hands it a key, which is the half `TestReferenceRangeCitationsAreWellFormed` cannot do.
//
// # Why a range can be keyed at all
//
// The point/range split was written as "a range has no string to key on", and that is true of a range
// in isolation. It is not true of a range in a *doc block whose subject produces a reference message*:
// `checkOffset` cites `valid.ml:390-393` and its body names `ErrOffsetOutOfRange`, so the reference's
// own site for `offset out of range` is a line this range claims to contain. The key comes from
// `errors.New` and the site from the reference, exactly as in the point check — no word of any message
// appears here, and no per-file list says which range means what.
//
// # What it catches that the bounds check does not
//
// Upstream inserting ten lines above `check_memop` leaves `390-393` a perfectly well-formed range
// inside the file, pointing at whatever moved into it. That is silent under a bounds check and loud
// here. It does *not* catch a range whose end is short by a line while still covering the site —
// which is how `:390-392` survived long enough to be corrected by hand — so containment is the floor
// and not the whole claim. Stated because *an instrument's domain is an assertion it cannot check
// about itself*.
//
// # A constructed message is unkeyable, and that is counted rather than excused
//
// **The first run of this check failed on a correct citation**, which is how the distinction below
// got written. `validate.go`'s index-space block cites `valid.ml:41-42` and its subject produces
// `unknown type` — and the reference never writes that string: `lookup` builds it as
// `"unknown " ^ category ^ " " ^ …`, so there is no literal site to be inside any range. The
// citation is right and the instrument was wrong.
//
// The repair is not to skip that block. A message with no verbatim site anywhere in the reference is
// **residue**, and the residue is *counted and pinned* beside the keyed population — so upstream
// renaming a message does not quietly demote a checked range to an unchecked one. Both pins move,
// and the test says which way. Skipping silently would have been the accept-direction version of
// this fix, and it is the one that reads identically to working.
func TestReferenceRangeCitationsContainTheirSubjectsSite(t *testing.T) {
	ref := testenv.RequireSpecRef(t, testenv.RefValidML)
	lines := strings.Split(ref, "\n")

	// sites returns the reference's own line numbers for a message, empty when the reference builds
	// the string rather than writing it.
	sites := func(msg string) []int {
		var at []int
		for i, l := range lines {
			if strings.Contains(l, `"`+msg+`"`) {
				at = append(at, i+1)
			}
		}
		return at
	}

	keyed, residue := 0, 0
	for _, file := range citationFiles {
		for _, b := range docBlocks(t, file) {
			if len(b.ranges) == 0 || len(b.messages) == 0 {
				continue
			}
			// Only the messages the reference writes verbatim can locate anything. The rest are
			// residue: counted below, never silently dropped.
			var located []int
			for _, msg := range b.messages {
				located = append(located, sites(msg)...)
			}
			if len(located) == 0 {
				residue += len(b.ranges)
				continue
			}
			for _, r := range b.ranges {
				// A range is answerable if any one of the subject's sites is inside it. Any rather
				// than all: a subject naming several sentinels is not claiming that one arm's range
				// contains the others.
				ok := false
				for _, n := range located {
					if n >= r[0] && n <= r[1] {
						ok = true
					}
				}
				keyed++
				if !ok {
					t.Errorf("%s:%d documents %v and cites %s:%d-%d, and no line in that range "+
						"carries any of those messages (they are at %v) — the range has retargeted "+
						"or the subject changed, and either way the citation reads as checked while "+
						"pointing elsewhere",
						file, b.start, b.messages, testenv.RefValidML, r[0], r[1], located)
				}
			}
		}
	}

	// Both pinned, for the reason every count in this file is — a check over no ranges passes without
	// asserting anything — and pinned *separately* so a message the reference stops writing verbatim
	// moves a range from the checked column into the excused one loudly.
	// Five keyed and eight residue once the domain became the whole package. The keyed ones are
	// align.go:148 (`offset out of range` inside :390-393), instr.go:454 (select's arity message inside
	// :442-446), vec.go:316 (`invalid lane index` inside :373-378), and module.go's two — both blocks
	// naming `size minimum must not be greater than maximum`, which the reference does write verbatim
	// at :104-105.
	//
	// **The residue is where the newly-covered files landed, and it is a fact about the reference, not
	// a gap.** `memory size must be at most `, `table size must be at most `, `duplicate export name "`
	// and every `unknown <category> ` are *built* — the reference concatenates a head with a range text,
	// a quoted name, or a category, so no line carries the sentinel's string as a complete literal and
	// no range can be said to contain it. Those messages are not thereby unchecked: the head of each is
	// read straight out of the reference by `TestLimitsRangesMatchTheReference` and by the message-keyed
	// point checks, which is the stronger instrument. Residue is counted so that a message the reference
	// *starts* or *stops* writing verbatim moves a range between the columns loudly.
	//
	// **#343's five ranges all landed in residue, and for a third reason** — neither "built" nor
	// "unchecked", but *the reference's own literal not being in the range the comment points at*.
	// `sub type` and `forward use of type` are written verbatim by the reference, so the sentinels are
	// message-keyed elsewhere; what these ranges cite is the rule's *shape* — the three `require`s in
	// order, the one-group-at-a-time context, the finality arm — and a range cited for its structure
	// contains the structure and not necessarily the string. Counted so that stays visible: if one of
	// them ever starts containing its message verbatim it moves into the keyed column loudly.
	// **#359 moved the residue by five against six new ranges, and the sixth is the informative
	// one.** Five of the slice's blocks name a sentinel — three build a message from a category or an
	// index, two are internal-channel errors the reference has no counterpart for — so all five land
	// excused, for the "built" reason this header already gives. The sixth block names no sentinel at
	// all: its rule rejects only through the shared operand-pop helpers, so it has no message of its
	// own, and a block with ranges and no messages is skipped by the loop above rather than counted in
	// either column. So this pin's two figures do not sum to the range count and were never going to —
	// which is worth stating, because `wantRanges` next door counts six and this counts five, and the
	// discrepancy reads as an arithmetic slip until the third category is named.
	// **Slice 7 moved keyed by ten and residue by eight against twenty-six new ranges, and the eight
	// missing from the sum are the third category doing its job again.** Ten of the slice's blocks
	// cite a rule whose message the reference writes verbatim — `immutable field`, `immutable array`,
	// `array types do not match` and their neighbours are literals in `valid.ml`, so the range that
	// cites the rule contains the string and keys on it. Eight land excused for the "built" reason
	// this header gives: `unknown field 3` composes a category with an index, and `field is packed` is
	// assembled by a conditional in the reference itself (`"field is " ^ (if exto = None then …)`), so
	// neither is a complete literal on any reference line even though the reference emits both
	// verbatim. The remaining eight blocks name no sentinel at all — arms that
	// reject only through the shared operand-pop helpers, and the region's constants and its
	// parameterized-constructor argument, which cite the reference's structure and raise nothing.
	//
	// So the two figures still do not sum to `wantRanges`, by 28 rather than by the 26 they missed by
	// before, and the discrepancy is the same fact each time rather than an accumulating error. This
	// is the paragraph the previous slice asked for when it noted the sum "reads as an arithmetic slip
	// until the third category is named" — it is now named twice, at two different magnitudes, which
	// is stronger evidence that the category is real than either statement alone.
	//
	// **Slice 8 moved residue by three and keyed by nothing, against seven new ranges.** The three are
	// `peekRef`'s two and `brOnNonNull`'s and `returnCallRef`'s one each, less one that left
	// `refIsNull`'s block when the null-bit citation moved into `peekRef`'s — a range whose text did
	// not change at all, which is why the sibling `wantRanges` pin cannot see the move and this one
	// can. All four name `type mismatch`, and the reference *builds* that message rather than writing
	// it — the head is concatenated with the expected and actual types — so they land excused for the
	// oldest reason in this header. The other four new ranges name no sentinel of their own: they
	// reject through `popExpect` and the block-arity check, so the loop above skips their blocks
	// entirely, and the two figures miss `wantRanges` by 32 rather than 28 for that reason and no
	// other.
	// **Slice 9 moved residue by two and keyed by nothing, against four new ranges, and the split is
	// along the two-versus-third-category line the paragraphs above spent three slices naming.** The
	// two are `requireTailResults`' `:546-549` and `indirectTarget`'s `:560-565`: both subjects raise
	// `ErrTypeMismatch` directly, and the reference *builds* both messages — the result-type require
	// concatenates two type lists onto its head, and the element-type require concatenates
	// `string_of_reftype t` — so they are excused for the oldest reason in this header even though the
	// reference emits each one verbatim at run time. `returnCall`'s `:544-550` and
	// `returnCallIndirect`'s `:560-570` name no sentinel at all: both arms refuse only through
	// `requireTailResults`, `indirectTarget` and the operand-pop helpers, so `subjectMessages` answers
	// nil for them and the loop above skips their blocks. The two figures now miss `wantRanges` by 34.
	//
	// Which makes this the fourth consecutive slice where the miss grew by exactly the count of new
	// no-message blocks — 26→28→32→34 — and that regularity is the useful part: the gap is a stable
	// property of how arms delegate their refusals, not an error accumulating in either pin.
	const wantKeyed, wantResidue = 15, 29
	if keyed != wantKeyed || residue != wantResidue {
		t.Errorf("checked %d keyed range citation(s) and excused %d as constructed-message residue "+
			"across %v, want %d and %d — recount and re-pin. A range becomes keyable when its "+
			"subject starts producing a message the reference writes verbatim, so either figure "+
			"moving means a doc block's subject changed or a reference string did",
			keyed, residue, citationFiles, wantKeyed, wantResidue)
	}
}

// section returns the text from a top-level `let` binding to the next one.
func section(src, let string) string {
	i := strings.Index(src, let)
	if i < 0 {
		return ""
	}
	rest := src[i+len(let):]
	if j := regexp.MustCompile(`(?m)^let `).FindStringIndex(rest); j != nil {
		return rest[:j[0]]
	}
	return rest
}
