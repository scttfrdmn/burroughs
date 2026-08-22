package binary

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The controls on extended-const (#109), and the shape they are answering is unusual enough
// to be worth stating once here rather than three times below.
//
// Every other gate in this package governs something that **appears in the image**: a flags
// value, a section id, a value type, an opcode. Extended-const governs a *position* — six MVP
// instructions become legal inside a constant expression and are unchanged everywhere else —
// so nothing in the byte stream distinguishes the gated construct from the ungated one.
// `0x6a` in a function body is MVP; `0x6a` in a global's initialiser is this proposal.
//
// That is why the fix could not be a `gatedOpcodes` row and why these tests exist:
//
//   - The **positional** claim, both directions and both positions, because the failure mode
//     of the obvious implementation is not "the gate does nothing" but "the gate declines
//     `i32.add` in function bodies", which rejects valid modules — the accept-direction defect
//     #109 was itself filed for, reintroduced by its own fix. No `assert_malformed` vector can
//     see it (contract §9 G-3).
//   - The **join** between the proposal's six *names* and the six *bytes* the decoder holds,
//     because `extendedConstOps` was hand-written from mnemonics and a transcription slip
//     there is silent in one direction: an opcode wrongly admitted unconditionally passes
//     every board.
//   - The **error's voice**, because a gate-off decline must name the feature and must not
//     spoof `constant expression required`, which is a spec *invalid* string for a module the
//     spec calls both well-formed and valid (the #5 ruling).

// TestExtendedConstOpsAreTheProposalsSix is the join `extendedConstOps`' comment promises.
//
// The map's keys are bytes and the proposal's list is names, so the two authorities cannot be
// compared directly — which is exactly the gap a transcription slip lives in. The join key is
// the **generated table's mnemonic**: for each of the six names the proposal lists, find the
// single-byte opcode whose mnemonic is that instruction, and require the map to hold it.
//
// Both directions, because they fail differently and only one of them is visible on a board:
//
//   - A name in the proposal with no entry here means an extended-const module the engine
//     rejects with the gate *on*. A suite vector can catch that, and nine did (#109).
//   - An entry here that the proposal does not list means an opcode admitted into constant
//     expressions that the spec does not admit — accepted rather than rejected, so **no
//     assert_malformed and no assert_invalid vector can see it**. This direction is the reason
//     the test is worth writing; the other direction already had an oracle.
//
// The proposal's names are *read from the vendored document* rather than typed here. A list
// typed into this test would be a third copy of the same fact agreeing with the second by
// construction — the vacuity failure with an extra step — and the document is the authority
// the citation in `extendedConstOps` already claims.
func TestExtendedConstOpsAreTheProposalsSix(t *testing.T) {
	// The document's "New Instructions" list, one ``-quoted mnemonic per line. Licensed
	// through testenv: a join that skips when the document is absent reports agreement with
	// a file it never read, and BURROUGHS_NO_SKIP=1 revokes the license in CI.
	const doc = "proposals/extended-const/Overview.md"
	path := filepath.Join("..", "..", testenv.ProposalDoc(doc))
	names := proposalConstInstrs(t, testenv.RequireProposalDoc(t, path))

	// Vacuity floor on the *reading*, not just on the map. A regexp that stopped matching —
	// the document reformatted, the section renamed — yields an empty name list, and every
	// assertion below would then agree with nothing while the map went unchecked. Six is the
	// exact count the document lists at the pinned snapshot, so this is the exact-count
	// instrument beside the floor rather than a floor alone (#105's lesson: a floor bounds the
	// catastrophic case and cannot see a small silent loss).
	if len(names) != 6 {
		t.Fatalf("read %d instruction names from %s, want 6 (%s:41-46 lists i32/i64 add, sub, "+
			"mul): the join's own input is what a reformatted document breaks first, and an "+
			"empty list would let this test certify the map by comparing nothing",
			len(names), doc, doc)
	}

	// The mnemonic → byte index, built from the generated table. The table is the authority on
	// encodings (`decode.ml` at the pinned revision) and it is the *only* place in this
	// package that knows `i32.add` is 0x6a; deriving the expectation from it is what makes
	// this a join rather than a fourth transcription.
	//
	// Single-byte opcodes only. Every one of the six is in the MVP's single-byte space, and a
	// name resolving into a prefixed region would mean the proposal's subject moved — worth a
	// loud failure, not a silent widening of the search.
	byMnemonic := make(map[string]byte, len(opTable))
	for code, info := range opTable {
		if code > 0xff || info.illegal || info.escape || info.reason != "" {
			continue
		}
		// The generated table spells mnemonics with underscores (`i32_add`), the proposal with
		// dots (`i32.add`). Normalising here rather than in the reader keeps the document's
		// text quoted verbatim in failures.
		byMnemonic[strings.ReplaceAll(info.mnemonic, "_", ".")] = byte(code)
	}
	if len(byMnemonic) < 100 {
		t.Fatalf("indexed %d single-byte mnemonics from the generated table, want >=100: the "+
			"join key is this index, and an index this small means the table or its mnemonic "+
			"field is not being read", len(byMnemonic))
	}

	// Direction 1: every name the proposal lists is in the map, at the byte the table gives.
	want := make(map[byte]bool, len(names))
	for _, name := range names {
		b, ok := byMnemonic[name]
		if !ok {
			t.Errorf("%s lists %q as const-legal, and no single-byte arm of the generated table "+
				"has that mnemonic: the proposal's subject and the authority's vocabulary have "+
				"diverged, and the map cannot be checked against either", doc, name)
			continue
		}
		if !extendedConstOps[b] {
			t.Errorf("%s lists %q (%#02x) as const-legal but extendedConstOps omits it: with the "+
				"gate on, a module using it in a constant expression is rejected — the "+
				"accept-direction failure that made #109, and the one direction the suite can see",
				doc, name, b)
		}
		want[b] = true
	}

	// Direction 2: nothing else is in the map. This is the invisible direction.
	for b := range extendedConstOps {
		if want[b] {
			continue
		}
		info, ok := opTable[uint32(b)]
		mnemonic := "no arm in the generated table"
		if ok {
			mnemonic = info.mnemonic
		}
		t.Errorf("extendedConstOps holds %#02x (%s), which %s does not list: an opcode admitted "+
			"into constant expressions that the spec does not admit is *accepted*, so no "+
			"assert_malformed or assert_invalid vector can catch it (contract §9 G-3)",
			b, mnemonic, doc)
	}

	// And the near-miss the map's comment names, asserted rather than described. `0x41`
	// (i32.const) is unconditionally const-legal and `0x6a` (i32.add) is gated; a slip that
	// moved a member of `constOps` into this map would make an MVP opcode gate-dependent and
	// break every module ever written. `constLegalBytes` already rejects an overlap; this
	// pins the specific pair, because the two bytes are one bucket of the board apart and
	// the confusion is a plausible one rather than a hypothetical.
	if extendedConstOps[0x41] {
		t.Error("extendedConstOps holds 0x41 (i32.const), which is unconditionally const-legal: " +
			"gating it makes every MVP module with a global initialiser depend on a 3.0 feature")
	}
	if !extendedConstOps[0x6a] {
		t.Error("extendedConstOps lacks 0x6a (i32.add): the anchor of the proposal's list")
	}
}

// proposalConstInstrs reads the backtick-quoted mnemonics from the proposal's "New
// Instructions" list.
//
// Scoped to that section rather than to the whole document, because the file mentions
// instructions in prose too and a document-wide sweep would collect them — a trigger that
// over-matches is as wrong as one that under-matches, and here it would silently demand map
// entries the proposal never asked for. The section is delimited by its own heading and the
// next `##`, which is the structure the document actually has rather than a line range: a
// line range would be a citation that rots on the next upstream edit, and the assertion in
// the caller is what catches the section disappearing.
func proposalConstInstrs(tb testing.TB, body string) []string {
	tb.Helper()

	var out []string
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "##") {
			// The heading that opens the list, and any subsequent heading closes it.
			inSection = strings.Contains(trimmed, "New Instructions")
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "-") {
			continue
		}
		// ` - `i32.add`` — the mnemonic is between the first pair of backticks on a list item.
		open := strings.Index(trimmed, "`")
		if open < 0 {
			continue
		}
		rest := trimmed[open+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			continue
		}
		out = append(out, rest[:end])
	}
	return out
}

// TestExtendedConstGateIsPositional is the control the whole design turns on, and it is the
// one a `gatedOpcodes` entry would fail.
//
// Four cells, because the claim is a conjunction of two independent facts and either alone is
// satisfied by a wrong implementation:
//
//	                      gate off                      gate on
//	constant expression   declined, by feature name     accepted
//	function body         accepted (MVP, ungated)       accepted
//
// The **bottom-left cell is the point**. A gate wired through `gatedOpcodes` passes the top
// row and fails there, declining `i32.add` in every function body — and the failure is
// invisible to the suite, because 4162 of 4162 green vectors are rejections and no board can
// see a decoder that wrongly rejects. The top row alone would certify that implementation.
//
// The right column is not filler either: without it, the decline in the top-left is
// indistinguishable from the decoder simply not supporting the instruction at all.
func TestExtendedConstGateIsPositional(t *testing.T) {
	// `i32.const 2` `i32.const 3` `i32.mul`, terminated. Legal in both positions with the
	// gate on; legal in one position with it off. The same bytes in all four cells,
	// deliberately — a probe that varied the bytes per cell would be four tests that cannot
	// be compared, and the whole claim is that the *position* decides.
	instrs := []byte{0x41, 0x02, 0x41, 0x03, 0x6c}

	space := constLegalOps(t) // vacuity: the const sets are non-empty and disjoint
	if !space[opKey(0x00, 0x6c)] {
		t.Fatalf("0x6c (i32.mul) is not in the const-legal space this probe assumes; the probe " +
			"would then be measuring an ordinary non-const verdict and agree with a gate that " +
			"does nothing")
	}

	for _, tc := range []struct {
		name      string
		on        bool
		constOnly bool
		wantErr   error // nil means "must decode clean"
	}{
		{
			name: "gate off, constant expression: declined by name", on: false, constOnly: true,
			wantErr: ErrFeatureDisabled,
		},
		{
			// The cell that fails if the gate is routed through the opcode map.
			name: "gate off, function body: accepted, because these are MVP instructions",
			on:   false, constOnly: false, wantErr: nil,
		},
		{name: "gate on, constant expression: accepted", on: true, constOnly: true, wantErr: nil},
		{name: "gate on, function body: accepted", on: true, constOnly: false, wantErr: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every *other* gate on in both rows, so a decline can only be credited to
			// ExtendedConst. Deriving from the struct rather than setting one field means a
			// tenth gate does not silently change what this test is holding fixed.
			f := featuresAllOn(t)
			f.ExtendedConst = tc.on

			d := &Decoder{Features: f}
			c := &instrCtx{d: d, constOnly: tc.constOnly}
			r := &reader{b: append(append([]byte{}, instrs...), opEnd), eof: ErrPayloadEnd}

			// The grammar must complete in all four cells: extended-const changes what is
			// *admissible*, never how bytes are read, and a probe that died in the grammar
			// would report the gate's verdict from a truncation.
			if err := c.block(r); err != nil {
				t.Fatalf("reading % x failed with %v; the probe must reach the gate check, "+
					"not die in the grammar", instrs, err)
			}
			if err := c.endTerminator(r); err != nil {
				t.Fatalf("the terminating END was not consumed: %v", err)
			}

			err := c.release()
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("% x declined with %v, want a clean read", instrs, err)
				}
				// A clean release is not enough for the const cells: the *const* verdict is
				// recorded separately and released after, so a reader that accepted the gate
				// and then reported `constant expression required` passes the line above.
				if tc.constOnly && c.nonConst.set {
					t.Errorf("% x released clean but recorded a non-const verdict at %s: with "+
						"the gate on these six are const-legal, and reporting them as non-const "+
						"rejects a valid module in the direction no vector can see",
						instrs, c.nonConst)
				}
				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("% x produced %v, want %v", instrs, err, tc.wantErr)
			}
			// The decline's *voice*, which is the #5 ruling: a gate-off engine must reject the
			// module, and must reject it in its own name. Two things it must not say.
			if strings.Contains(err.Error(), "malformed") {
				t.Errorf("error %q says malformed for a module Wasm 3.0 defines and the spec "+
					"calls well-formed — gates never manufacture malformedness (#5)", err)
			}
			if !strings.Contains(err.Error(), "extended-const") {
				t.Errorf("error %q does not name the feature: a decline that does not say which "+
					"gate is off is a verdict the reader cannot act on", err)
			}
			// And the const verdict must not have been armed in parallel. `constant expression
			// required` is a spec *invalid* string; the module here is valid, and the engine's
			// configuration is the only reason it is refused.
			if c.nonConst.set {
				t.Errorf("the gate declined *and* recorded a non-const verdict at %s: the "+
					"byte must be const-legal for reporting purposes either way, so that "+
					"turning the gate on cannot leave a second refusal behind", c.nonConst)
			}
		})
	}
}

// TestExtendedConstDeclineYieldsToMalformed pins this gate into the release order the other
// gates already obey, and it is here rather than in gatemap_test.go because the ordering has a
// second obligation for a *positional* gate that the opcode-mapped ones do not have.
//
// The order is: malformed beats the feature decline beats the const verdict (0008, and
// binary.wast:112 is the vector that forced the first half). For extended-const the middle
// term has a neighbour — a module can be simultaneously gate-declined and genuinely
// non-const, and the two verdicts must not be able to trade places, because one names the
// engine's configuration and the other names a spec rule about the module.
func TestExtendedConstDeclineYieldsToMalformed(t *testing.T) {
	f := featuresAllOn(t)
	f.ExtendedConst = false
	d := &Decoder{Features: f}

	// `i32.const 2` `i32.const 3` `i32.mul` with **no END**: the gate is declined during the
	// read, then the grammar runs off the end. Malformed must win.
	c := &instrCtx{d: d, constOnly: true}
	r := &reader{b: []byte{0x41, 0x02, 0x41, 0x03, 0x6c}, eof: ErrPayloadEnd}
	if err := c.block(r); err != nil {
		t.Fatalf("block failed with %v; the truncation must be reported by the terminator, not "+
			"by the instruction read", err)
	}
	err := c.endTerminator(r)
	if !errors.Is(err, ErrPayloadEnd) {
		t.Errorf("an unterminated expression whose gate is off reported %v, want ErrPayloadEnd: "+
			"a gate decline that pre-empts a malformed verdict reports the wrong layer's answer, "+
			"and it also parks the vector in `gated` where the decline is pure artifact "+
			"(binary.wast:112's lesson, applied to a gate)", err)
	}
	// The decline was recorded — otherwise the assertion above is satisfied by a gate that
	// never fired at all, and this test would certify the ordering of one verdict.
	if c.release() == nil {
		t.Error("no feature decline was recorded, so the ordering above was never tested: the " +
			"probe must reach the gate check before the truncation")
	}
}
