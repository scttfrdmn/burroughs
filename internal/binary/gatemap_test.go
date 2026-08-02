package binary

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The controls on the proposal→opcode mapping (decision 0008, #48).
//
// Four questions, and they are separate tests because they fail for separate reasons:
//
//   - Does every opcode the mapping names exist in the generated table? (a gate governing
//     nothing reads as coverage and is a hole)
//   - Does every gate govern at least one construct? (a gate that cannot decline is the
//     #48 defect wearing a mapping file)
//   - Do the citations resolve? (a citation nobody re-checks is a claim — #37)
//   - **Does every gate, turned off, actually decline something?** That is the inverse of
//     TestAllGatesOnLeavesNothingGated and the definition-of-done Scott's ruling attached
//     to #48. It is last here and first in importance.

// featuresAllOn is every gate on, derived by reflection.
//
// Duplicating internal/spec's allFeaturesOn is deliberate and is not the shared-fact
// problem: internal/spec imports internal/binary for its board, so the dependency cannot
// run the other way, and exporting a test helper from the engine to serve a test in
// another package would put a testing-only door in the public API. The two copies are
// kept honest by both being *derived* — neither enumerates the fields, so a ninth gate
// appears in both without an edit, which is the property that mattered.
func featuresAllOn(tb testing.TB) Features {
	tb.Helper()

	var f Features
	v := reflect.ValueOf(&f).Elem()
	for i := range v.NumField() {
		fld := v.Field(i)
		if fld.Kind() != reflect.Bool {
			tb.Fatalf("Features.%s is %s, not a bool: a gate this helper cannot turn on "+
				"would run as *off* while the test claims everything is on",
				v.Type().Field(i).Name, fld.Kind())
		}
		fld.SetBool(true)
	}
	return f
}

// constVerdictDecoder is a decoder for tests whose subject is *const-ness*: every gate on,
// so a gated construct cannot answer in a feature decline's voice.
//
// It exists because #48's fix made three existing tests fail, and they were right to fail.
// `throw_ref`, `return_call`, and `ref.eq` are all both non-const *and* gated, so on
// `&Decoder{}` — v0's posture — the feature decline now outranks the const verdict (0008's
// order) and a test asking "is this reported as non-const?" gets "your gate is off",
// which is a true answer to a different question.
//
// The tempting repair was to relax those assertions to accept either error. That would have
// been the overfitting failure applied to a control: the const verdict is a real
// obligation, and a test that accepts two verdicts pins neither. Turning the gates on
// instead keeps each test asking exactly what it was written to ask, and leaves the
// interaction between the two verdicts to TestGateDeclineYieldsToMalformed, which is
// *about* the interaction.
func constVerdictDecoder(tb testing.TB) *Decoder {
	tb.Helper()
	return &Decoder{Features: featuresAllOn(tb)}
}

// featureGateIDs returns every gate's ID, derived from the struct rather than from the
// gateID constants.
//
// The direction matters. Deriving from the constants would let a Features field with no
// constant go unnoticed — which is precisely #48's shape, a construct with no gate — so
// the domain is the struct, and the constants are what get checked against it.
func featureGateIDs(tb testing.TB) []gateID {
	tb.Helper()

	var f Features
	t := reflect.TypeOf(f)
	ids := make([]gateID, 0, t.NumField())
	for i := range t.NumField() {
		ids = append(ids, gateID(t.Field(i).Name))
	}
	if len(ids) < 4 {
		tb.Fatalf("derived %d gates from Features; the struct had 8 when 0008 was written, "+
			"and a domain this small means reflection found nothing to walk", len(ids))
	}
	return ids
}

// TestEveryFeatureFieldIsReadableByName is the control on `Features.enabled`'s switch.
//
// The switch is hand-written for speed (it runs per instruction), which makes it a place a
// ninth gate can be added to the struct and forgotten. An unreadable gate is not merely
// unhandled: `gateCheck` treats `known == false` as an internal error, so a forgotten gate
// turns every opcode it maps into a bug report. Reflection over the struct is the domain;
// the switch is the thing checked.
func TestEveryFeatureFieldIsReadableByName(t *testing.T) {
	on := featuresAllOn(t)
	var off Features

	for _, g := range featureGateIDs(t) {
		gotOn, known := on.enabled(g)
		if !known {
			t.Errorf("Features.%s has no arm in enabled()'s switch: gateCheck would report an "+
				"internal error for every opcode mapped to it", g)
			continue
		}
		// Both directions, because an arm returning a constant would pass one of them.
		// This is the partition-check lesson (#34): reading the same field twice with
		// different inputs is what distinguishes a wired arm from a hardcoded one.
		if !gotOn {
			t.Errorf("Features.%s: enabled() says off with every gate on — the arm reads the "+
				"wrong field", g)
		}
		if gotOff, _ := off.enabled(g); gotOff {
			t.Errorf("Features.%s: enabled() says on with every gate off — the arm reads the "+
				"wrong field", g)
		}
	}
}

// TestEveryMappedOpcodeExistsInTheTable is ruling 1's first machine-check.
//
// A mapping entry for an opcode the reference does not have is a gate that governs
// nothing while looking like coverage — the same failure as a stale allowlist entry, and
// invisible without this test because nothing else reads the mapping's ranges against the
// table.
//
// Ranges are checked by *membership*, not endpoint: `0xfb 0x00..0xff` is a legitimate
// region entry whose upper end is empty, so requiring both endpoints to exist would fail
// a correct entry. What is required is that each entry covers at least one real opcode,
// and — for single-opcode entries, where lo == hi — that the one it names is real.
//
// "Real" means **present and not illegal**, and that refinement is the falsification pass
// earning its keep. The first draft asked only for presence, and re-pointing an
// exception-handling entry from `0x0a` to `0xc6` left it green: `0xc6` is in the table with
// `illegal: true` — an arm the reference defines *in order to reject*. A gate governing a
// byte that is rejected anyway is a gate governing nothing, which is precisely the defect
// this test names, so presence was the wrong predicate and the control could not see the
// bug it was written for. Probing an *absent* byte (0xe0) did fire, which is what made the
// gap visible rather than the injection simply looking successful.
// Its complement is TestGateCensusIsClassifiedArmByArm (gatecensus_test.go, decision 0012):
// this walks the *mapping* and asks whether each entry covers a real arm; that walks the
// *table* and asks whether each arm's gate was classified rather than inherited from a
// range. Neither subsumes the other, and #91 was the direction this one cannot see.
func TestEveryMappedOpcodeExistsInTheTable(t *testing.T) {
	if len(gatedOpcodes) == 0 {
		t.Fatal("gatedOpcodes is empty: a walk over no entries agrees with any table")
	}

	total := 0
	for _, g := range gatedOpcodes {
		region, ok := prefixRegions[g.prefix]
		if !ok {
			t.Errorf("%s (%s): prefix %#02x is no region of the table", g.what, g.gate, g.prefix)
			continue
		}
		covered := 0
		for sub, info := range region {
			// Not `escape` either: a prefix byte is a dispatch, not a construct a gate
			// can accept or decline, and the region behind it carries its own entries.
			if sub >= g.lo && sub <= g.hi && !info.illegal && !info.escape {
				covered++
			}
		}
		if covered == 0 {
			t.Errorf("%s (%s): prefix %#02x range %#x..%#x covers no accepted opcode in the "+
				"table — a gate governing nothing, which reads as coverage",
				g.what, g.gate, g.prefix, g.lo, g.hi)
		}
		if g.lo == g.hi {
			switch info, ok := region[g.lo]; {
			case !ok:
				t.Errorf("%s (%s): %#02x %#x is named individually but is absent from the table",
					g.what, g.gate, g.prefix, g.lo)
			case info.illegal:
				t.Errorf("%s (%s): %#02x %#x is an arm the reference defines in order to "+
					"*reject*, so gating it governs a byte that is refused anyway",
					g.what, g.gate, g.prefix, g.lo)
			}
		}
		total += covered
	}

	// Vacuity with a plausible floor, not a non-zero check (the empty-comparison law).
	// The mapping covers **318** accepted arms at bdd7164 — stamped from this test's own
	// log, not reasoned. Two drafts got it wrong in different ways, which is why it is
	// printed: first an arithmetic slip, then 337, which was the count *before* illegal and
	// escape arms were excluded. A floor well under the real figure survives the table
	// growing and still fails a mapping that has quietly emptied.
	if total < 200 {
		t.Errorf("the mapping covers %d accepted table arms; it covered 318 at bdd7164, and a "+
			"count this low means the ranges stopped matching the regions", total)
	}
	t.Logf("mapping covers %d accepted table arms across %d entries", total, len(gatedOpcodes))
}

// TestEveryGateMapsAtLeastOneConstruct is ruling 1's second machine-check, and it is the
// one that would have caught #48 itself.
//
// A gate with no constructs cannot decline anything, so it is present, reflectable,
// turn-on-able, and inert — which is what `GC`, `TailCall`, `RelaxedSIMD`, and
// `MultiMemory` would have been had they been added as bools without a mapping.
//
// The domain is *constructs*, not opcodes: `Threads` and `Memory64` govern limits flags
// and no opcodes at all, so a check over `gatedOpcodes` alone would demand opcode entries
// for them and be satisfied only by inventing some. The two maps are one domain.
func TestEveryGateMapsAtLeastOneConstruct(t *testing.T) {
	withOpcodes := map[gateID]int{}
	for _, g := range gatedOpcodes {
		withOpcodes[g.gate]++
	}

	for _, g := range featureGateIDs(t) {
		if withOpcodes[g] > 0 || gatedNonOpcodes[g] != "" {
			continue
		}
		t.Errorf("Features.%s governs no construct in either map: it can be turned on and off "+
			"and decline nothing, which is #48 exactly — a gate that never fires passes the "+
			"all-gates-on lane trivially", g)
	}

	// The reverse: an entry naming a gate that is not a Features field. That would be a
	// mapping the dispatch can never consult, since enabled() would report it unknown.
	fields := map[gateID]bool{}
	for _, g := range featureGateIDs(t) {
		fields[g] = true
	}
	for _, g := range gatedOpcodes {
		if !fields[g.gate] {
			t.Errorf("%s maps to gate %q, which is no field of Features", g.what, g.gate)
		}
	}
	for g := range gatedNonOpcodes {
		if !fields[g] {
			t.Errorf("gatedNonOpcodes names gate %q, which is no field of Features", g)
		}
	}
}

// TestRelaxedSIMDIsInsideSIMDsRegion pins the one deliberate overlap.
//
// Relaxed SIMD lives at `fd 0x100..0x12f`, *inside* the region SIMD owns, and `gateFor`
// resolves it by narrowest range. Two things could break that silently: the ranges
// drifting apart until they are siblings rather than nested, and `gateFor` reverting to
// first-match — where the answer would depend on slice order, a fact no reader of
// gatemap.go would think to check.
//
// So both are asserted, and the second is asserted *through gateFor* rather than by
// reading the slice, because the behaviour is the claim.
func TestRelaxedSIMDIsInsideSIMDsRegion(t *testing.T) {
	var simd, relaxed gatedOpcode
	for _, g := range gatedOpcodes {
		switch g.gate {
		case gateSIMD:
			simd = g
		case gateRelaxedSIMD:
			relaxed = g
		default:
			// Every other gate. This is a selection, not a dispatch: only the two
			// overlapping entries are this test's subject.
		}
	}
	if simd.what == "" || relaxed.what == "" {
		t.Fatal("no SIMD or relaxed SIMD entry found: the containment claim has no subject")
	}
	if simd.prefix != relaxed.prefix {
		t.Fatalf("SIMD is prefix %#02x and relaxed SIMD is %#02x; the containment this test "+
			"pins requires one region", simd.prefix, relaxed.prefix)
	}
	if relaxed.lo < simd.lo || relaxed.hi > simd.hi {
		t.Errorf("relaxed SIMD %#x..%#x is not inside SIMD %#x..%#x: they have become siblings, "+
			"so an opcode in the window answers to whichever the scan reaches first",
			relaxed.lo, relaxed.hi, simd.lo, simd.hi)
	}

	// Narrowest-match, asserted through the lookup. An opcode in the window is relaxed
	// SIMD's; one outside it is SIMD's.
	if g, ok := gateFor(0xfd, 0x100); !ok || g.gate != gateRelaxedSIMD {
		t.Errorf("gateFor(fd, 0x100) = %q (found=%v), want %q: narrowest match must win, or the "+
			"answer depends on slice order", g.gate, ok, gateRelaxedSIMD)
	}
	if g, ok := gateFor(0xfd, 0x0c); !ok || g.gate != gateSIMD {
		t.Errorf("gateFor(fd, 0x0c) = %q (found=%v), want %q", g.gate, ok, gateSIMD)
	}
}

// TestGateMapCitationsResolve is *fixtures cite the suite, and the citations are checked*
// (#37) pointed at the mapping.
//
// Each entry cites a line in a vendored proposal document. The machine cannot verify that
// GC really owns `0xfb02` — nothing here reads English — but it can verify the cited line
// exists and is not blank, which is what catches the failure that actually happens: a
// document edited upstream, or an off-by-one. That is not a theoretical concern here. The
// first draft of gatemap.go cited `gc/MVP.md:806` for `ref.eq`; the row is at `:805`, and
// this check is what found it.
//
// Licensed through testenv like every other skip, because a citation check that skips
// when the documents are absent reports agreement with files it never read.
func TestGateMapCitationsResolve(t *testing.T) {
	if len(gatedOpcodes) == 0 {
		t.Fatal("gatedOpcodes is empty: a citation check over no entries verifies nothing")
	}

	checked := 0
	for _, g := range gatedOpcodes {
		file, line, ok := splitCitation(g.cite)
		if !ok {
			t.Errorf("%s (%s): citation %q is not file:line", g.what, g.gate, g.cite)
			continue
		}
		// Through the licensed door: a citation check that skips when the documents are
		// absent reports agreement with files it never read, and BURROUGHS_NO_SKIP=1
		// revokes the license so CI cannot pass by not asking.
		path := filepath.Join("..", "..", testenv.ProposalDoc(file))
		lines := strings.Split(testenv.RequireProposalDoc(t, path), "\n")
		if line < 1 || line > len(lines) {
			t.Errorf("%s (%s): %s has %d lines, so the citation does not resolve",
				g.what, g.gate, file, len(lines))
			continue
		}
		text := strings.TrimSpace(lines[line-1])
		if text == "" {
			t.Errorf("%s (%s): %s is blank — the document moved under the citation",
				g.what, g.gate, g.cite)
			continue
		}
		checked++
		t.Logf("%s -> %s", g.cite, text)
	}
	if checked != len(gatedOpcodes) {
		t.Errorf("resolved %d of %d citations; a partial pass is a coverage claim it cannot support",
			checked, len(gatedOpcodes))
	}
}

// splitCitation parses "path/to/doc.md:123".
func splitCitation(cite string) (file string, line int, ok bool) {
	i := strings.LastIndex(cite, ":")
	if i < 0 {
		return "", 0, false
	}
	n := 0
	digits := cite[i+1:]
	if digits == "" {
		return "", 0, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", 0, false
		}
		n = n*10 + int(r-'0')
	}
	return cite[:i], n, true
}

// TestEveryGateOffDeclinesSomething is the inverse gate control, and it is #48's
// definition-of-done (ruling 2, Scott).
//
// TestAllGatesOnLeavesNothingGated bounds **over**-gating: under full features nothing may
// be declined. Nothing bounded under-gating, and that asymmetry is the whole reason #48
// was invisible on two boards — the default lane is assert_malformed-only, and a gate that
// never fires cannot park a vector in `gated`, so it passes the all-on lane trivially.
//
// This is the other half: every gate, turned off *with all others on*, must decline at
// least one construct with ErrFeatureDisabled. One gate at a time, so a decline cannot be
// credited to a different gate.
//
// It carries a second obligation, which is what decided landing it here rather than
// parking it red. The SIMD row's construct is a **blocktype** (`0x7b` as a block's result
// type), so this test also permanently pins the blocktype branch ordering: `decodeBlockType`
// is an `either`, and the valtype branch must be last or the alternation overwrites
// ErrFeatureDisabled with `malformed value type` — a gate manufacturing malformedness. Move
// the branch and this test goes red. *One control, two obligations.*
func TestEveryGateOffDeclinesSomething(t *testing.T) {
	// One probe per gate: bytes that use a construct that gate governs, and the form to
	// wrap them in. `body` is a function body's instructions (END supplied); `constExpr`
	// is an initialiser.
	probes := map[gateID]struct {
		instrs []byte
		what   string
	}{
		gateExceptionHandling: {[]byte{0x0a}, "throw_ref"},
		gateGC:                {[]byte{0xd3}, "ref.eq"},
		gateTailCall:          {[]byte{0x12, 0x00}, "return_call 0"},
		// fd 0x0c is v128.const: the prefix, a one-byte sub-opcode, 16 bytes of literal.
		gateSIMD: {append([]byte{0xfd, 0x0c}, make([]byte, 16)...), "v128.const"},
		// fd 0x100 is i8x16.relaxed_swizzle, whose sub-opcode is a two-byte LEB.
		gateRelaxedSIMD: {[]byte{0xfd, 0x80, 0x02}, "i8x16.relaxed_swizzle"},
		// i32.load with flags bit 6 set: align 0, an explicit memory index, offset 0.
		gateMultiMemory: {[]byte{0x28, 0x40, 0x00, 0x00}, "i32.load with an explicit memory index"},
	}

	gates := featureGateIDs(t)
	declined := 0
	for _, g := range gates {
		f := featuresAllOn(t)
		v := reflect.ValueOf(&f).Elem().FieldByName(string(g))
		if !v.IsValid() {
			t.Errorf("no Features field named %q", g)
			continue
		}
		v.SetBool(false)

		p, ok := probes[g]
		if !ok {
			// Threads and Memory64 govern limits flags, which are a *section* construct
			// and are covered by sections_test.go rather than by an instruction probe.
			// Named here rather than silently skipped: a gate with no probe and no note
			// is how under-gating hides, and this list is the licensed exception.
			switch g {
			case gateThreads, gateMemory64:
				if gatedNonOpcodes[g] == "" {
					t.Errorf("Features.%s has neither an instruction probe here nor a "+
						"gatedNonOpcodes entry: it is unpoliced in both directions", g)
				}
			default:
				// The default is the *failure*, deliberately, and this is the one place
				// in this file where `exhaustive`'s complaint would have been a real
				// finding rather than a shape question: a ninth gate added with no probe
				// lands here and fails, instead of being quietly omitted from the walk.
				t.Errorf("Features.%s has no probe: a gate this test cannot exercise is a "+
					"gate whose decline was never observed, which is #48's shape", g)
			}
			continue
		}

		d := &Decoder{Features: f}
		c := &instrCtx{d: d, nonConst: -1}
		r := &reader{b: append(append([]byte{}, p.instrs...), 0x0B), eof: ErrPayloadEnd}
		if err := c.block(r); err != nil {
			t.Errorf("Features.%s off: reading %s (% x) failed with %v; the probe must reach "+
				"the gate check, not die in the grammar", g, p.what, p.instrs, err)
			continue
		}
		err := c.release()
		if !errors.Is(err, ErrFeatureDisabled) {
			t.Errorf("Features.%s off: %s (% x) decoded with %v, want ErrFeatureDisabled\n\t"+
				"a gate-off engine meeting a gated construct must reject it — accept-and-ignore "+
				"silently breaks semantics (the #5 ruling's other direction, #48)",
				g, p.what, p.instrs, err)
			continue
		}
		// And it must not spoof a spec string, which is the #5 ruling's *first* direction.
		if strings.Contains(err.Error(), "malformed") {
			t.Errorf("Features.%s off: error %q says malformed for a construct Wasm 3.0 defines — "+
				"gates never manufacture malformedness", g, err)
		}
		// The same construct must be accepted with the gate on, or the decline above is
		// indistinguishable from the decoder simply not supporting it.
		on := &Decoder{Features: featuresAllOn(t)}
		onCtx := &instrCtx{d: on, nonConst: -1}
		onR := &reader{b: append(append([]byte{}, p.instrs...), 0x0B), eof: ErrPayloadEnd}
		if err := onCtx.block(onR); err != nil {
			t.Errorf("Features.%s on: %s (% x) failed with %v; a construct that fails either way "+
				"proves nothing about the gate", g, p.what, p.instrs, err)
		} else if err := onCtx.release(); err != nil {
			t.Errorf("Features.%s on: %s (% x) still declined with %v", g, p.what, p.instrs, err)
		}
		declined++
	}

	// Vacuity floor. Six of the eight gates have instruction probes; if the probe map is
	// emptied or the loop stops matching, this fails instead of passing by asking nothing.
	if declined < 6 {
		t.Errorf("only %d gates were observed declining a construct, want >=6 of %d: this test "+
			"passing while exercising nothing is the failure it exists to prevent", declined, len(gates))
	}
	t.Logf("%d of %d gates observed declining an instruction construct", declined, len(gates))
}

// TestGateDeclineYieldsToMalformed pins the release *order*, which no vector reaches.
//
// The deferred decline is released after each of its two grammar-completion points, and
// "after" is doing work in both:
//
//   - In a function body, after the size reconciliation. A body that is both gated and
//     mis-sized reports the size mismatch — the answer that does not depend on the
//     engine's configuration.
//   - In a const expression, after the grammar completes. binary.wast:112 is the vector
//     that forces this one and the suite does cover it: a gated opcode reached by
//     over-reading an unterminated initialiser must not turn a malformed module into a
//     gate decline.
//
// Both directions are asserted, because a release placed at either extreme satisfies one
// of them.
func TestGateDeclineYieldsToMalformed(t *testing.T) {
	var off Features // every gate off: v0's posture

	// A const expression whose grammar never completes, over a gated opcode. throw_ref
	// (0x0a) is binary.wast:112's byte.
	d := &Decoder{Features: off}
	r := &reader{b: []byte{0x0a}, eof: ErrPayloadEnd}
	if err := d.decodeConstExpr(r); !errors.Is(err, ErrPayloadEnd) {
		t.Errorf("gated opcode then truncation: got %v, want ErrPayloadEnd — a gate decline that "+
			"pre-empts a malformed verdict reports the wrong layer's answer, and would also park "+
			"binary.wast:112 in `gated` where TestGatedVectors demands an allowlist entry for a "+
			"decline that is pure artifact", err)
	}

	// A function body that is both gated and mis-sized. The size mismatch wins.
	img := []byte("\x00asm\x01\x00\x00\x00")
	img = append(img, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00) // type: 1 functype
	img = append(img, 0x03, 0x02, 0x01, 0x00)             // function: 1 function
	img = append(img, 0x0A, 0x05, 0x01,                   // code: 5 bytes, 1 body
		0x03,       // body declares 3 bytes, but the grammar consumes 4
		0x00,       // no locals
		0x0a,       // throw_ref — gated, with every gate off
		0x1a, 0x0B) // drop, END
	dm := &Decoder{Features: off}
	_, err := dm.DecodeModule(img)
	if !errors.Is(err, ErrSectionSizeMismatch) {
		t.Errorf("gated opcode in a mis-sized body: got %v, want ErrSectionSizeMismatch — "+
			"malformed outranks a feature decline, and the size check is this layer's "+
			"malformed verdict", err)
	}

	// The control on the control: the same body, correctly sized, *does* decline. Without
	// this, the assertion above is satisfied by a decoder that never declines at all.
	img2 := []byte("\x00asm\x01\x00\x00\x00")
	img2 = append(img2, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)
	img2 = append(img2, 0x03, 0x02, 0x01, 0x00)
	img2 = append(img2, 0x0A, 0x06, 0x01,
		0x04, // 4 bytes, which is what the grammar consumes
		0x00, 0x0a, 0x1a, 0x0B)
	if _, err := dm.DecodeModule(img2); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("gated opcode in a well-sized body: got %v, want ErrFeatureDisabled — if this "+
			"passes, the size-mismatch assertion above proves nothing about ordering", err)
	}
}
