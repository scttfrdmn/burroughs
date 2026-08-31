// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"maps"
	"math/bits"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The 0xFE region's controls, and the one thing that makes them different from every other region's.
//
// `atomic.go` derives 67 signatures from the mnemonic instead of tabulating them, for `sig.go`'s
// reason: a wrong row is an *accept* and no `assert_invalid` vector can see it (contract §9 G-3). So
// the derivation needs an authority to disagree with, and here there is only one pin that has these
// arms — the core pin's `valid.ml` contains no `Atomic` constructor at all. There is no second
// opinion to triangulate against.
//
// What stands in for it is that the threads pin says each thing **twice, in two files**, and the two
// statements are joined here rather than restated:
//
//   - `syntax/operators.ml` gives every mnemonic its constructor family and its `ty` — the *rows*.
//   - `valid/valid.ml` gives every constructor family its `[…] --> […]` — the *shapes*.
//
// Neither file mentions the other's fact, and `atomicSignature` reads both off one string. So the
// join is the check: 32 names on the left, 7 arms on the right, and the product is 67 opcodes' worth
// of types that no hand in this repository wrote down. The name join is exact in both directions and
// asserted to be — the table's 32 distinct mnemonics *are* the reference's 32 binding names, so the
// spelling is the reference's own and no normalization stands between them.
//
// # The rmw operator is why the counts differ, and it is the argument for deriving
//
// 67 opcodes, 32 mnemonics, 7 arms. The 42 rmw encodings collapse onto 14 constructors because
// `AtomicRmw (rmwop, atomicop)` carries the operator *beside* the memop, exactly as the generated
// table carries it in a separate `operator` field that `binary.PrefixedOp` does not return. A table
// of 67 rows would have spelled `add`, `sub`, `and`, `or`, `xor` and `xchg` forty-two times over a
// term that appears in none of the seven signatures.
//
// # The address operand is where these controls stop agreeing with the pin
//
// Every arm writes `NumType I32Type` for the address, because the threads baseline predates
// memory64. `atomicSignature` passes the memory's own address type instead — **ruled for by Scott on
// the #538 review**, as a snapshot artifact rather than a statement about atomics — and the two
// readings agree on every module with a 32-bit memory, which is every module these arms can be
// checked against. So `TestAtomicSignaturesMatchTheThreadsReference` builds a 32-bit memory and the
// agreement it reports is real but silent on the divergence.
//
// Two controls cover it, and they are two because they fail for unrelated reasons.
// `TestAtomicAddressTypeIsTheNamedMemorys` is the *helper*: which memory's type is read, when a module
// has several, checked by reading a `sig`. `TestAtomicAddressTypeIsObservableWithBothGatesOn` is the
// *path*: wat through the encoder, the decoder under an explicit feature set, and the validator, read
// as a verdict about a module. Both are hand-built because **no corpus vector meets the condition**,
// and the mutation that implements the losing reading leaves every board green — so these two are the
// whole witness set.

// atomicRegion returns the 0xFE region as the decoder actually presents it: sub-opcode to mnemonic.
//
// Derived by probing `binary.PrefixedOp`, not by reading `opTableFE` — the table is another package's
// private map, and what this package can be wrong about is the set of rows that *reach* it.
func atomicRegion(tb testing.TB) map[uint32]string {
	tb.Helper()
	const scanTo = 0xFFFF

	region := map[uint32]string{}
	for op := uint32(0); op <= scanTo; op++ {
		name, _, ok := binary.PrefixedOp(prefixAtomic, op)
		if !ok {
			continue
		}
		if name == "" {
			// The SIMD region has 19 of these — `illegal: true` holes that decode to a named row
			// with no name. This region has none, so one appearing is a table change this package
			// must hear about rather than a case to skip.
			tb.Errorf("%#02x %#02x resolves with an empty mnemonic; the atomics region has no "+
				"illegal holes, so a nameless row here is a decoder-side change",
				prefixAtomic, op)
			continue
		}
		region[op] = name
	}
	return region
}

// TestPrefixAtomicIsTheRegionBinaryDispatches checks the constant, the region's extent, and that the
// two are about the same region.
//
// `prefixAtomic` is declared in this package because `internal/binary` spells its prefixes as
// literals; a local constant agreeing with another package's literal by inspection is the thing this
// replaces, following `TestPrefixSIMDIsTheRegionBinaryDispatches` one file over.
func TestPrefixAtomicIsTheRegionBinaryDispatches(t *testing.T) {
	region := atomicRegion(t)

	// Pinned exactly rather than floored: a floor catches a moved file and says nothing about six
	// rows a narrowed table quietly dropped, and a dropped row here is a *decline*, which reads on
	// the board as a slice boundary rather than as a lost instruction.
	const wantRows = 67
	if len(region) != wantRows {
		t.Fatalf("the %#02x region has %d named rows, want %d; the count is the threads proposal's "+
			"opcode set and a change to it is a decoder-side finding", prefixAtomic, len(region), wantRows)
	}

	// The extent, and the reserved run inside it. `atomic_fence` is 0x03 and the loads start at
	// 0x10, so 0x04–0x0f is twelve sub-opcodes the proposal never assigned — and unlike SIMD's holes
	// they are *absent* from the table rather than marked illegal, so the decoder declines them
	// outright. Both facts are pinned because they are the two ways the region's shape can move.
	var minOp, maxOp uint32 = ^uint32(0), 0
	for op := range region {
		minOp = min(minOp, op)
		maxOp = max(maxOp, op)
	}
	const wantMin, wantMax = 0x00, 0x4e
	if minOp != wantMin || maxOp != wantMax {
		t.Errorf("the %#02x region spans %#x..%#x, want %#x..%#x; a region that grew past the scan "+
			"is a finding and one that shrank is a lost row", prefixAtomic, minOp, maxOp, wantMin, wantMax)
	}

	var gap []uint32
	for op := minOp; op <= maxOp; op++ {
		if _, ok := region[op]; !ok {
			gap = append(gap, op)
		}
	}
	wantGap := []uint32{0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	if !slices.Equal(gap, wantGap) {
		t.Errorf("the region's unassigned sub-opcodes are %#x, want %#x; the run between "+
			"atomic_fence and the loads is the proposal's own, and a gap elsewhere means a row "+
			"vanished from the middle of an assigned range", gap, wantGap)
	}

	// A reserved sub-opcode must not type. It cannot be decoded, so this is the direction that
	// matters: were `atomicSignature` to answer for one, a table widened to admit it would acquire a
	// signature nobody derived rather than a decline in a named bucket.
	for _, op := range gap {
		if _, err := atomicSignature(nil, binary.Instr{Prefix: prefixAtomic, Op: op}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("atomicSignature typed the reserved %#02x %#02x with err %v, want a decline; a "+
				"hole that types is a hole that *accepts*", prefixAtomic, op, err)
		}
	}

	// `prefixAtomic` names the *right* region and not merely one that works: an atomic mnemonic
	// resolving under some other prefix would make the constant a coincidence.
	for p := 1; p <= 0xFF; p++ {
		if byte(p) == prefixAtomic {
			continue
		}
		for op := range uint32(0x10000) {
			name, _, ok := binary.PrefixedOp(byte(p), op)
			if !ok {
				continue
			}
			if _, _, isAtomic := atomicForm(name); isAtomic {
				t.Errorf("prefix %#02x %#02x also resolves %q, which atomicForm claims, so "+
					"prefixAtomic does not identify the region uniquely", byte(p), op, name)
			}
		}
	}
}

// TestEveryAtomicRowHasASignature walks the region's whole domain, which is what makes
// `atomicSignature`'s fall-through unreachable rather than merely unreached.
//
// `errNoAtomicSignature` at the end of that function is the `errNoSignature` posture: returned, not
// panicked, so a row from a widened table lands in a named bucket. This test is the claim that no
// row *today* lands there — and it asks the question through `atomicSignature` against a real module,
// not through `atomicForm` alone, because the family is only half of what a row needs.
func TestEveryAtomicRowHasASignature(t *testing.T) {
	region := atomicRegion(t)
	modes, forms := parseThreadsMemopModes(t)
	byName := refAtomicBindings(t, modes)

	m := &binary.Module{Memories: []binary.Memory{{}}}

	families := map[atomicFamily]int{}
	for op, name := range region {
		fam, ty, ok := atomicForm(name)
		if !ok || fam == atomicNone {
			t.Errorf("%#02x %#02x is %q and atomicForm puts it in no family, so the whole region's "+
				"claim to be derived from the mnemonic has a hole", prefixAtomic, op, name)
			continue
		}
		families[fam]++

		// The value type must be a numeric type this package has. A zero ValType would type an
		// operand as nothing at all and pop whatever was on the stack.
		if ty != binary.I32 && ty != binary.I64 && fam != atomicFence {
			t.Errorf("%q types its value operand %v, which is neither i32 nor i64; every atomic "+
				"constructor names one of the two", name, ty)
		}

		in := binary.Instr{Prefix: prefixAtomic, Op: op}
		if row, isMemarg := byName[name]; isMemarg && row.family != "" && binary.HasMemarg(prefixAtomic, op) {
			in.Imm1 = binary.StageMemarg(0, refAtomicAlignExp(t, row, forms))
		}
		if _, err := atomicSignature(m, in); err != nil {
			t.Errorf("atomicSignature(%q, %#02x %#02x) = %v, want a type; a decline here is a row "+
				"the validator cannot check, and an unchecked instruction in a module reported "+
				"valid is the accept direction", name, prefixAtomic, op, err)
		}
	}

	// Every one of the seven families is populated. Without this the walk above would pass on a
	// region that had collapsed onto one family — *the shape of what survives names the bug* — and
	// the four counts that are not 1 are the ones a mis-ordered `atomicForm` would redistribute.
	want := map[atomicFamily]int{
		atomicFence: 1, atomicNotify: 1, atomicWait: 2,
		atomicLoad: 7, atomicStore: 7, atomicRmw: 42, atomicCmpXchg: 7,
	}
	for fam, n := range want {
		if families[fam] != n {
			t.Errorf("family %d has %d rows, want %d; the cmpxchg-before-rmw order in atomicForm is "+
				"what keeps the last two apart, and getting it wrong moves seven rows between them",
				fam, families[fam], n)
		}
	}
	if len(families) != len(want) {
		t.Errorf("the region populates %d families and there are %d, so a family has no rows and "+
			"its arm is never taken", len(families), len(want))
	}
	t.Logf("%d rows over %d families, %d distinct mnemonics", len(region), len(families), len(byName))
}

// refAtomicArm is one of the threads pin's seven atomic arms.
type refAtomicArm struct {
	line    int      // 1-based, the arm's `|` — atomic.go's header cites the range these span
	params  []string // the reference's own tokens, left of `-->`
	results []string // and right of it
}

// atomicArmHeadRe matches the head of a top-level instruction arm in the threads pin's valid.ml.
//
// Two-space indent and a `|`, which is `parseRefVecArms`' pattern; the trailing `[ (]` is *not*
// required here because `| AtomicFence -> [] --> []` puts its whole body on the arm line.
var atomicArmHeadRe = regexp.MustCompile(`(?m)^  \| ([A-Za-z][A-Za-z0-9_']*)`)

// parseRefAtomicArms reads the seven arms out of the threads pin's `valid.ml`.
//
// # The family filter comes from the other file, and that is the point
//
// `parseRefVecArms` filters on the name prefix `Vec` and excludes non-arms by requiring a `-->` in
// the body. Neither move is safe here: `valid.ml:207` is `| Atomic ->`, an arm of `check_memop`'s
// *mode* match, which has the prefix and whose body — bounded by the next arm head, hundreds of
// lines below — contains other arms' `-->`s. That is the exact failure `parseRefVecArms`' comment
// records, where `VecT` swallowed five hundred lines and the parse found 21 arms in a file with 20.
//
// So the domain is the set of constructors `operators.ml` actually builds. A mode is not a
// constructor, so it cannot be admitted by a body-shaped filter that a wide chunk can satisfy; and
// the set is the join key these controls need anyway. With the domain closed, `-->` becomes an
// *assertion* rather than a filter: an arm in the set with no signature is a fatal error instead of a
// silent absence.
func parseRefAtomicArms(tb testing.TB, families map[string]bool) map[string]refAtomicArm {
	tb.Helper()
	src := testenv.RequireSpecRef(tb, testenv.ThreadsRefValidML)

	ms := atomicArmHeadRe.FindAllStringSubmatchIndex(src, -1)
	out := map[string]refAtomicArm{}
	for i, mi := range ms {
		fam := src[mi[2]:mi[3]]
		if !families[fam] {
			continue
		}
		end := len(src)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		body := src[mi[0]:end]
		line := strings.Count(src[:mi[0]], "\n") + 1

		arrow := strings.Index(body, "-->")
		if arrow < 0 {
			tb.Errorf("%s:%d: the %s arm has no `-->`, so the authority for its arity did not parse",
				testenv.ThreadsRefValidML, line, fam)
			continue
		}
		// The signature is on the arm's own line or within two of it — one `check_memop` line
		// between at most. Asserted rather than assumed because the chunk's *end* is the next arm
		// head and the last arm's chunk therefore runs to the end of the match, so a `-->` found far
		// away would belong to something else.
		if within := strings.Count(body[:arrow], "\n"); within > 2 {
			tb.Errorf("%s:%d: the %s arm's first `-->` is %d lines down, past its own body; the "+
				"chunk ran into a later arm and the signature read from it is not this arm's",
				testenv.ThreadsRefValidML, line, fam, within)
			continue
		}

		params, okL := bracketedBefore(body[:arrow])
		results, okR := bracketedAfter(body[arrow+len("-->"):])
		if !okL || !okR {
			tb.Errorf("%s:%d: the %s arm's bracketed sides did not parse; an unparsed arm is a "+
				"silent gap in the arity check", testenv.ThreadsRefValidML, line, fam)
			continue
		}
		out[fam] = refAtomicArm{
			line:    line,
			params:  splitTopLevel(params),
			results: splitTopLevel(results),
		}
	}
	return out
}

// atomicNullaryBindingRe matches a constructor binding that takes no memarg — `let atomic_fence =`
// over `AtomicFence`. The memarg-bearing ones are `refAtomicMemopRe`'s.
var atomicNullaryBindingRe = regexp.MustCompile(`(?m)^let (\w+) =\n\s+(\w+)\s*$`)

// refAtomicBindings maps every 0xFE mnemonic to the reference's row for it: family, `ty`, `pack`.
//
// Two shapes, because the reference has two — 31 memarg constructors and one that carries none — and
// the fence's absence from `parseRefAtomicMemops` is a fact about the instruction rather than a gap
// in the parse. `AtomicFence` has no `check_memop` call and so no mode, which is why the memop parse
// (which filters on mode `Atomic`) cannot see it and must not be widened to.
func refAtomicBindings(tb testing.TB, modes map[string]string) map[string]refMemop {
	tb.Helper()
	out := map[string]refMemop{}
	for _, row := range parseRefAtomicMemops(tb, modes) {
		out[row.name] = row
	}

	src := testenv.RequireSpecRef(tb, testenv.ThreadsRefOperatorsML)
	nullary := 0
	for _, m := range atomicNullaryBindingRe.FindAllStringSubmatch(src, -1) {
		if !strings.Contains(m[2], "Atomic") {
			continue
		}
		if _, dup := out[m[1]]; dup {
			tb.Errorf("%s binds %q twice, once with a memarg and once without", testenv.ThreadsRefOperatorsML, m[1])
		}
		out[m[1]] = refMemop{name: m[1], family: m[2]}
		nullary++
	}
	if nullary != 1 {
		tb.Errorf("parsed %d memarg-less atomic constructors from %s, want 1 (AtomicFence); the "+
			"fence is the one arm with no `check_memop`, and a second would be a row whose "+
			"alignment nothing checks", nullary, testenv.ThreadsRefOperatorsML)
	}
	return out
}

// refAtomicAlignExp is the alignment exponent the reference's own `check_memop` accepts for a row —
// the *only* one it accepts, since the atomic rule is an equality.
//
// Derived through `applyGetSz` and `refSizes`, the same two the width controls use, rather than
// through this package's `naturalWidth`. Using `naturalWidth` would let a wrong width make the
// signature controls below feed themselves a legal instruction that isn't one, and pass.
func refAtomicAlignExp(tb testing.TB, row refMemop, forms map[string]string) uint32 {
	tb.Helper()
	sz, hasSz, err := applyGetSz(forms[row.family], row.pack)
	if err != nil {
		tb.Fatalf("%s (%s): %v", row.name, row.family, err)
	}
	key := row.ty
	if hasSz {
		key = sz
	}
	width, ok := refSizes[key]
	if !ok {
		tb.Fatalf("%s: %q has no size in refSizes, so the proposal names a constructor this test "+
			"does not know; add it rather than defaulting", row.name, key)
	}
	if width == 0 || width&(width-1) != 0 {
		tb.Fatalf("%s: the reference's natural width is %d, which is not a power of two, so no "+
			"alignment exponent satisfies the equality rule", row.name, width)
	}
	return uint32(bits.TrailingZeros64(width))
}

// resolveThreadsRefType maps one of the threads pin's type tokens to this package's ValType.
//
// The threads baseline writes `NumType I32Type` where the core pin writes `NumT I32T`, so this is
// `resolveRefType`'s counterpart and not a call to it: two spellings kept apart for `refSizes`'
// reason — a rename upstream should arrive as an unresolved token here rather than be normalized
// away by this file's opinion that the two are the same type.
//
// `atomicop.ty` is the row's own value type, substituted from `operators.ml`. That substitution is
// the join: the arm says *which operand* is the value type and the binding says *which type it is*,
// and neither file says both.
func resolveThreadsRefType(tok, ty string) (binary.ValType, bool) {
	if tok == "NumType atomicop.ty" {
		tok = "NumType " + ty
	}
	switch tok {
	case "NumType I32Type":
		return binary.I32, true
	case "NumType I64Type":
		return binary.I64, true
	}
	return binary.ValType{}, false
}

// TestAtomicSignaturesMatchTheThreadsReference is the G-3 control for all 67 rows.
//
// For every opcode in the region it joins the two authority files — the binding's family and `ty`,
// the arm's operand tokens — and compares the result to `atomicSignature`'s. Nothing in this file
// states an atomic signature; a wrong arm in `atomic.go` disagrees with a shape parsed out of
// `valid.ml`, and a wrong type disagrees with one parsed out of `operators.ml`.
//
// The module carries a 32-bit memory, so the address operand's divergence is invisible *here* by
// construction and `TestAtomicAddressTypeIsTheNamedMemorys` is where it is checked. The memarg is
// staged at the reference's own natural alignment, because the Atomic-mode rule is an equality and
// any other exponent would make every row report an alignment error instead of a type.
func TestAtomicSignaturesMatchTheThreadsReference(t *testing.T) {
	region := atomicRegion(t)
	modes, forms := parseThreadsMemopModes(t)
	byName := refAtomicBindings(t, modes)

	// The name join, both directions, before anything is compared through it. This is the property
	// that lets the two files be joined by a bare string: the generated table's mnemonics *are* the
	// reference's binding names, and a normalization step here would hide a divergence rather than
	// report one.
	seen := map[string]bool{}
	for op, name := range region {
		if _, ok := byName[name]; !ok {
			t.Errorf("%#02x %#02x is %q and %s binds no constructor of that name, so its family and "+
				"value type have no authority", prefixAtomic, op, name, testenv.ThreadsRefOperatorsML)
		}
		seen[name] = true
	}
	for name := range byName {
		if !seen[name] {
			t.Errorf("%s binds %q and no opcode in the %#02x region decodes to it, so the reference "+
				"has an instruction this engine cannot express", testenv.ThreadsRefOperatorsML, name, prefixAtomic)
		}
	}
	const wantNames = 32
	if len(byName) != wantNames || len(seen) != wantNames {
		t.Fatalf("the join has %d reference bindings and %d distinct mnemonics, want %d each; the "+
			"42 rmw encodings collapse onto 14 constructors because the operator is a separate "+
			"immediate, and a changed count means that collapse changed",
			len(byName), len(seen), wantNames)
	}

	families := map[string]bool{}
	for _, row := range byName {
		families[row.family] = true
	}
	arms := parseRefAtomicArms(t, families)

	// Seven families, seven arms. The count is `valid.ml`'s and the vacuity check for the whole
	// control: an empty `arms` would make every comparison below run over nothing.
	const wantArms = 7
	if len(arms) != wantArms || len(families) != wantArms {
		t.Fatalf("parsed %d arms for %d constructor families, want %d each; a family with no arm is "+
			"a row this control silently skips", len(arms), len(families), wantArms)
	}

	// The arms' extent, which is the citation `atomic.go`'s header carries for them. Pinned as a
	// span rather than seven line numbers: what a reader follows is the range.
	lo, hi := 1<<30, 0
	for _, arm := range arms {
		lo, hi = min(lo, arm.line), max(hi, arm.line)
	}
	const wantLo, wantHi = 523, 539
	if lo != wantLo || hi != wantHi {
		t.Errorf("the seven atomic arms span %s:%d-%d, and atomic.go's header quotes them as "+
			"%d-%d (the last arm's signature is on the line after its head); a moved arm means the "+
			"quote in that header is of something else",
			testenv.ThreadsRefValidML, lo, hi, wantLo, wantHi)
	}

	// Every memarg family checks in Atomic mode, and the fence has no mode at all. Cross-checking
	// the two derivations — the mode map and the constructor set — is what says the fence's absence
	// from `parseRefAtomicMemops` is the reference's fact and not this parse's.
	for fam := range families {
		mode, hasMode := modes[fam]
		switch {
		case fam == "AtomicFence" && hasMode:
			t.Errorf("%s gives AtomicFence the mode %q; the fence carries no memarg, so an "+
				"alignment mode for it means the arm gained a `check_memop` call",
				testenv.ThreadsRefValidML, mode)
		case fam != "AtomicFence" && mode != "Atomic":
			t.Errorf("%s checks %s in mode %q, want Atomic; a memarg family that is not Atomic-mode "+
				"is one whose alignment this package bounds where the reference fixes it",
				testenv.ThreadsRefValidML, fam, mode)
		}
	}

	m := &binary.Module{Memories: []binary.Memory{{}}}

	for _, op := range slices.Sorted(maps.Keys(region)) {
		name := region[op]
		row, ok := byName[name]
		if !ok {
			continue // already reported above
		}
		arm, ok := arms[row.family]
		if !ok {
			continue // already reported above
		}

		in := binary.Instr{Prefix: prefixAtomic, Op: op}
		if binary.HasMemarg(prefixAtomic, op) {
			in.Imm1 = binary.StageMemarg(0, refAtomicAlignExp(t, row, forms))
		}

		want, ok := refAtomicSig(t, arm, row, name)
		if !ok {
			continue
		}
		got, err := atomicSignature(m, in)
		if err != nil {
			t.Errorf("atomicSignature(%q) = %v, and the reference types it %v --> %v",
				name, err, arm.params, arm.results)
			continue
		}
		if !slices.Equal(got.params, want.params) || !slices.Equal(got.results, want.results) {
			t.Errorf("%q (%#02x %#02x, %s): signature is %v --> %v, reference is %v --> %v "+
				"(from %s:%d, ty %s from %s).\nA wrong operand list accepts modules the spec "+
				"refuses, and no assert_invalid vector in this region can say so",
				name, prefixAtomic, op, row.family, got.params, got.results, want.params, want.results,
				testenv.ThreadsRefValidML, arm.line, row.ty, testenv.ThreadsRefOperatorsML)
		}
	}
}

// refAtomicSig resolves one arm's tokens against one row's value type.
func refAtomicSig(tb testing.TB, arm refAtomicArm, row refMemop, name string) (sig, bool) {
	tb.Helper()
	out := sig{}
	for _, side := range []struct {
		toks []string
		dst  *[]binary.ValType
	}{{arm.params, &out.params}, {arm.results, &out.results}} {
		for _, tok := range side.toks {
			t, ok := resolveThreadsRefType(tok, row.ty)
			if !ok {
				// Fatal to the row rather than to the run: an unresolved token is this file failing
				// to read the authority, and reporting it as a mismatch would blame `atomic.go`.
				tb.Errorf("%s: the %s arm names the type %q, which this file cannot resolve — the "+
					"reference's spelling moved and the comparison for this row is not made",
					name, row.family, tok)
				return sig{}, false
			}
			*side.dst = append(*side.dst, t)
		}
	}
	return out, true
}

// TestAtomicAddressTypeIsTheNamedMemorys is the *named memory* half of the divergence `atomic.go`'s
// header records: which memory's address type an atomic access takes when a module has several.
//
// The threads pin writes `NumType I32Type` for every atomic address operand. This engine passes the
// memory's own address type, on the grounds that the pin predates memory64 and its plain `Load` arm
// says the same thing about a family where this engine already declines to follow it. **The two
// readings agree on every module that exists**: the corpus's three atomic modules declare one 32-bit
// memory, and both conditions for a disagreement — the memory64 gate on, and an atomic access to an
// i64-indexed memory — are unmet by every vector. So the discriminating module is built here, in the
// pair `checkOffset`'s controls use: bug-compatibility flips one verdict and not the other, and a
// single case would be passed by an implementation that hardcoded either answer.
//
// **Ruled for this engine's reading** (Scott, on the #538 review): the pin's `I32Type` is a snapshot
// artifact from a revision predating memory64, the same shape as the sub-opcode's `op s` versus
// `u32`, and the standard outranks the snapshot — with the added grounds that unlike the sub-opcode
// case this reading is *strictly better* rather than merely defensible, being identical under i32
// memories and correct under memory64. The draft of this comment said the test was "the record of a
// decision that has not been made yet" and that it would fail if Scott ruled for the pin's literal
// reading. That sentence is now false in both halves and is corrected in place rather than deleted,
// because a sentence written before a ruling and left standing after it tells the next reader the
// tree is in a state it is not.
//
// **This control reads a signature, so it is the helper and not the path.** It hands
// `atomicSignature` a `binary.Module` with `Addr64` already set, which is a struct no decoder
// produces with the memory64 gate off — so it proves the rule reads the right field and says nothing
// about whether a real module reaches it. `TestAtomicAddressTypeIsObservableWithBothGatesOn` is the
// path, and the two are separate because they fail for unrelated reasons.
func TestAtomicAddressTypeIsTheNamedMemorys(t *testing.T) {
	op := atomicMemargRow(t, "i32_atomic_load")

	i32Mem := binary.Memory{}
	i64Mem := binary.Memory{Limits: binary.Limits{Addr64: true}}

	// Alignment exponent 2 — natural for this row, so the equality rule cannot stand in for the
	// verdict under test.
	in := binary.Instr{Prefix: prefixAtomic, Op: op, Imm1: binary.StageMemarg(1, 2)}

	tests := []struct {
		name string
		mems []binary.Memory
		want binary.ValType
		why  string
	}{
		{
			name: "named memory is i64, memory 0 is i32",
			mems: []binary.Memory{i32Mem, i64Mem},
			want: binary.I64,
			why: "memory 1 is what the instruction names and it is 64-bit, so its addresses are " +
				"i64. The pin's literal `NumType I32Type` types the operand i32 and rejects a " +
				"module that indexes a 64-bit memory with a 64-bit address — the direction that " +
				"costs a valid program",
		},
		{
			name: "named memory is i32, memory 0 is i64",
			mems: []binary.Memory{i64Mem, i32Mem},
			want: binary.I32,
			why: "memory 1 is 32-bit and the operand is i32, which is also what the pin says — so " +
				"this case is where the two readings agree, and it is here because a validator " +
				"that always answered i64 would pass the case above",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &binary.Module{Memories: tc.mems}
			got, err := atomicSignature(m, in)
			if err != nil {
				t.Fatalf("atomicSignature over memories %v: %v — the address type is the only rule "+
					"under test and something else refused first", tc.mems, err)
			}
			if len(got.params) == 0 {
				t.Fatalf("i32.atomic.load types with no operands at all; its address operand is "+
					"what this control reads.\n%s", tc.why)
			}
			if got.params[0] != tc.want {
				t.Errorf("address operand is %v, want %v.\n%s", got.params[0], tc.want, tc.why)
			}
		})
	}
}

// TestAtomicAddressTypeIsObservableWithBothGatesOn is the ruling made observable end to end, which is
// what Scott ordered on the #538 review: *"a test with Memory64 on and an i64-indexed memory
// asserting the atomic address operand is i64 turns 'unobservable' into 'tested' — exactly what the
// `0xfe 0xce 0x00` control did for sub-opcode width. Unobservable-today is what the last two of these
// turned out not to be."*
//
// The sibling above reads a `sig` off `atomicSignature` with a hand-built module. This one goes
// through the whole path — wat, encoder, decoder under an explicit feature set, validator — and reads
// a **verdict about a module**, because that is the only thing an implementation of the losing reading
// would disagree with. `got.params[0] != binary.I64` is a fact about an internal struct; *this module
// validates and that one does not* is the observable the ruling is about.
//
// # Two parts, and the first is why nothing on the board can see this
//
// Part 1 asserts the two gates are **each** necessary, over one image, by feature set. Both refuse at
// the *decoder* and each names itself, so the claim `atomic.go`'s header makes — that observability
// needs the memory64 gate on **and** an i64-indexed memory — is a printed reading rather than an
// argument. It also fixes which layer owns the edge: the validator never consults a gate, and by the
// time a module reaches it the address type has arrived as `Limits.Addr64` and the gate's whole job is
// already done (grave #427's lesson, one region over).
//
// Part 2 is the quad. Rows 1 and 2 are the discriminating pair and they flip in **opposite**
// directions under the pin's literal reading — it refuses row 1 and accepts row 2 — so an
// implementation that hardcoded `i32` cannot pass both, and neither can one that hardcoded `i64`.
// Rows 3 and 4 are where the two readings agree, and they are here for the reason the sibling's second
// case is: a validator that always answered i64 would pass rows 1 and 2's *shape* while breaking every
// 32-bit module in the corpus, and a control whose rows all disagree with the loser cannot see that.
//
// # Watched die
//
// Mutation: `atomicSignature`'s six memarg arms take `binary.I32` instead of `addr` — the pin's
// literal reading, implemented. What printed:
//
//	row                       result
//	--------------------------+---------------------------------------------------------------
//	i64 addr on an i64 memory   refused: requires [i32] but stack has [i64] — a valid module lost
//	i32 addr on an i64 memory   accepted, and this control's only witness that it should not be
//	i32 addr on an i32 memory   unchanged (the readings agree)
//	i64 addr on an i32 memory   unchanged (the readings agree)
//
// So the pair fires in both directions and the agreement rows stay quiet, which is the shape that
// distinguishes a discriminating control from one that merely has more rows.
//
// **The mutation was run over the whole tree, not just this test, and that is the reportable part:
// every board passes it.** The core suite's 256 files and the threads lane's four both score exactly
// as they do now with the losing reading compiled in, and the only failures anywhere are this control
// and its sibling above. So the two of them are the entire witness set for the ruling — not "the
// board plus these", *these*. That is the measurement behind the order to build this, and it is why
// the four gate rows are here too: they say the corpus's silence is structural rather than an
// accident of which vectors upstream happened to write.
func TestAtomicAddressTypeIsObservableWithBothGatesOn(t *testing.T) {
	const i64MemAtomic = `(module (memory i64 1 1 shared)
		(func (result i32) i64.const 0 i32.atomic.load))`

	// Part 1: one image, four feature sets. The image is fixed so the only variable is the gate.
	img, err := text.EncodeModule([]byte(i64MemAtomic))
	if err != nil {
		t.Fatalf("the wat encoder refused the module this whole control is about, so nothing below "+
			"says anything about the validator: %v", err)
	}

	gates := []struct {
		name           string
		threads, mem64 bool
		wantRefusal    string // "" means the decoder must accept
	}{
		{"both on", true, true, ""},
		// The two single-gate rows are the necessity claim. Each names the gate that refused,
		// because "decode failed" is compatible with the module being malformed for an unrelated
		// reason and would let this row pass while proving nothing about the gate.
		{"threads on, memory64 off", true, false, "memory64"},
		{"threads off, memory64 on", false, true, "threads"},
		// v0's default policy, which is the board's default lane: the row that says no corpus vector
		// can reach the rule under test even if one were written.
		{"both off — DefaultFeatures", false, false, "threads"},
	}
	for _, g := range gates {
		t.Run("gate/"+g.name, func(t *testing.T) {
			f := binary.DefaultFeatures()
			f.Threads, f.Memory64 = g.threads, g.mem64
			_, err := (&binary.Decoder{Features: f}).DecodeModule(img)
			switch {
			case g.wantRefusal == "" && err != nil:
				t.Fatalf("decoder refused a shared i64 memory with both gates on: %v — the address "+
					"type rule cannot be reached at all, so the ruling has no observable", err)
			case g.wantRefusal != "" && err == nil:
				t.Fatalf("decoder accepted a shared i64 memory with %s — the two-condition "+
					"observability claim in atomic.go's header is then false, and the corpus may be "+
					"able to reach this rule after all, which would change where it is tested",
					g.name)
			case g.wantRefusal != "" && !strings.Contains(err.Error(), g.wantRefusal):
				t.Errorf("decoder refused with %q, which does not name %q — a refusal for some other "+
					"reason would let this row pass while saying nothing about the gate",
					err, g.wantRefusal)
			}
		})
	}

	// Part 2: the quad, through the full path with both gates on.
	bothOn := func(f *binary.Features) { f.Threads, f.Memory64 = true, true }

	rows := []struct {
		name      string
		wat       string
		wantValid bool
		pinSays   string
	}{
		{
			name: "i64 address on an i64 memory validates",
			wat: `(module (memory i64 1 1 shared)
				(func (result i32) i64.const 0 i32.atomic.load))`,
			wantValid: true,
			pinSays: "the pin's literal `NumType I32Type` refuses this — a valid program lost, " +
				"which is the direction that costs",
		},
		{
			name: "i32 address on an i64 memory is refused",
			wat: `(module (memory i64 1 1 shared)
				(func (result i32) i32.const 0 i32.atomic.load))`,
			wantValid: false,
			pinSays: "the pin's literal reading accepts this, and it is the only row that witnesses " +
				"the accept direction — contract §9 G-3, where no assert_invalid vector can help",
		},
		{
			name: "i32 address on an i32 memory validates",
			wat: `(module (memory 1 1 shared)
				(func (result i32) i32.const 0 i32.atomic.load))`,
			wantValid: true,
			pinSays:   "both readings agree; this row is what a validator answering i64 always breaks",
		},
		{
			name: "i64 address on an i32 memory is refused",
			wat: `(module (memory 1 1 shared)
				(func (result i32) i64.const 0 i32.atomic.load))`,
			wantValid: false,
			pinSays:   "both readings agree, in the reject direction",
		},
	}
	for _, r := range rows {
		t.Run("path/"+r.name, func(t *testing.T) {
			_, err := validated(t, r.wat, bothOn)
			switch {
			case r.wantValid && err != nil:
				t.Errorf("valid module refused: %v\n%s\n%s", err, r.pinSays, r.wat)
			case !r.wantValid && err == nil:
				t.Errorf("invalid module accepted — the accept direction, which no board column "+
					"reports.\n%s\n%s", r.pinSays, r.wat)
			}
		})
	}
}
