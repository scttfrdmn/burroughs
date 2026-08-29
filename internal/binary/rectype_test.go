package binary

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The type section's grammar is `rectype`, four levels deep, and this engine decoded
// `functype` — one arm of the third level — and called it the section (#86).
//
// **The suite has almost nothing to say about any of it**, which is why this file is
// mostly print-checks and one reference-derived comparison rather than a vector table:
//
//   - One vector in the whole corpus reaches a non-functype comptype:
//     `binary-gc.wast:1`, an `assert_malformed` on an array field's mutability byte. It
//     was the board's last remaining fail.
//   - **No vector** exercises `rec` (0x4e) or the two `sub` forms (0x50, 0x4f) in binary
//     form. Reverting the rectype descent entirely leaves the board at 4162/0 — measured,
//     not assumed, which is how this file's scope was decided.
//   - **No vector** asserts `malformed definition type` or `malformed storage type`. The
//     first was reported as `malformed function type` — a string the reference never emits
//     anywhere — and the board could not tell the invented sentinel from the real one.
//
// So the warrant here is agreement with the reference plus printed behaviour, in the
// proportion the oracle's silence forces. *Print-don't-predict* is not a style preference
// in this file; it is the only instrument that reaches three of the four defects.

// TestCompTypeFormsAreDecoded is the accept/decline pair for every comptype arm, in both
// lanes, and it is where `0x5e`/`0x5f` moved to from TestFuncTypeFormIsASignedLEB.
//
// They sat in that test's *malformedness* partition, labelled "array" and "struct" — named
// for what the spec calls them while being asserted as forms with no grammar. **A row whose
// label contradicts its assertion is grave #34 with the label telling the truth.**
//
// Both directions per form, because one wrong gate check passes half the pair: a missing
// gate accepts when it must decline (and *raises* the default board, which no floor can
// see — measured: removing the array arm's gate moves 4162 → 4163), and a gate on an
// ungated form declines a Wasm 1.0 module.
func TestCompTypeFormsAreDecoded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  []byte
		gated bool // needs Features.GC; if false it must decode with every gate off
	}{
		// The one Wasm 1.0 form. Ungated, and asserted as such: a gate here would reject
		// every module in the corpus.
		{"functype", []byte{0x01, 0x60, 0x00, 0x00}, false},

		// comptype's other two arms (decode.ml:255-258).
		{"arraytype of i8", []byte{0x01, 0x5E, 0x78, 0x00}, true},
		{"arraytype of i32", []byte{0x01, 0x5E, 0x7F, 0x01}, true},
		{"structtype, one field", []byte{0x01, 0x5F, 0x01, 0x78, 0x00}, true},
		{"structtype, no fields", []byte{0x01, 0x5F, 0x00}, true},

		// subtype's two explicit forms (decode.ml:262-271). Finality is not observable
		// to a decoder, so the two differ only in the discriminator byte.
		{"sub, no supertypes", []byte{0x01, 0x50, 0x00, 0x60, 0x00, 0x00}, true},
		{"sub final, no supertypes", []byte{0x01, 0x4F, 0x00, 0x60, 0x00, 0x00}, true},
		{"sub final, one supertype", []byte{0x01, 0x4F, 0x01, 0x00, 0x60, 0x00, 0x00}, true},

		// rectype's group form (decode.ml:274).
		{"rec group of one", []byte{0x01, 0x4E, 0x01, 0x60, 0x00, 0x00}, true},
		{"rec group of two", []byte{0x01, 0x4E, 0x02, 0x60, 0x00, 0x00, 0x60, 0x00, 0x00}, true},
		{"rec group, empty", []byte{0x01, 0x4E, 0x00}, true},

		// The nesting the four levels exist for: a rec group whose member is a sub of an
		// array. Nothing in the corpus reaches this shape.
		{"rec of sub of array", []byte{0x01, 0x4E, 0x01, 0x4F, 0x00, 0x5E, 0x78, 0x00}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := typeSection(tc.body)

			// Gate off.
			_, errOff := (&Decoder{}).DecodeModule(mod)
			if tc.gated {
				if !errors.Is(errOff, ErrFeatureDisabled) {
					t.Errorf("gate off: got %v, want a feature-named decline — *gates never "+
						"manufacture malformedness*: the form is Wasm 3.0 and well-formed, so the "+
						"engine's own configuration is what declines it and must say so", errOff)
				}
				if errOff != nil && !strings.Contains(errOff.Error(), "gc") {
					t.Errorf("gate off: %q does not name the feature", errOff)
				}
			} else if errOff != nil {
				t.Errorf("gate off: got %v, want accept — this form is not gated", errOff)
			}

			// Gate on: every form must decode.
			if _, err := (&Decoder{Features: Features{GC: true}}).DecodeModule(mod); err != nil {
				t.Errorf("GC on: got %v, want accept", err)
			}
		})
	}
}

// TestFieldTypeMutabilityIsTheSharedProduction is grave #83's shape, caught before it could
// become a second copy.
//
// `mutability` is one function in the reference (decode.ml:154-158), called from `fieldtype`
// (:244) and `globaltype` (:294). This engine had it at the global site only, so an array
// field's mutability byte was never read — `binary-gc.wast:1` was the board's last fail, and
// eight `global.wast` vectors scored the path that *did* work, which is exactly the
// configuration that makes a missing second call site invisible.
//
// Both positions asserted here, in one test, because **when two fields disagree about a
// value the suite has handed you a bidirectional control**: byte `0x02` is
// `malformed mutability` in both, so a check present at one site and absent at the other
// fails the two halves in opposite directions where either alone reads as plausible.
func TestFieldTypeMutabilityIsTheSharedProduction(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  []byte
		want string
	}{
		// The field position — the one the engine did not read. `binary-gc.wast:1` is the
		// vector, verbatim: array type, i8 storage, mutability 0x02.
		{"array field mut=0x02", typeSection([]byte{0x01, 0x5E, 0x78, 0x02}), "malformed mutability"},
		{"array field mut=0xff", typeSection([]byte{0x01, 0x5E, 0x78, 0xFF}), "malformed mutability"},
		{"struct field mut=0x02", typeSection([]byte{0x01, 0x5F, 0x01, 0x78, 0x02}), "malformed mutability"},

		// Legal at the same position, so the check is not simply rejecting the byte's
		// column: 0x00 const and 0x01 var both decode.
		{"array field mut=0x00", typeSection([]byte{0x01, 0x5E, 0x78, 0x00}), ""},
		{"array field mut=0x01", typeSection([]byte{0x01, 0x5E, 0x78, 0x01}), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// GC on, because the field positions are all inside GC constructs and a
			// gate-off run would decline before reaching the byte under test — which is
			// precisely why this vector reads `gated` on the default board.
			_, err := (&Decoder{Features: Features{GC: true}}).DecodeModule(tc.mod)
			if tc.want == "" {
				if err != nil {
					t.Errorf("got %v, want accept", err)
				}
				return
			}
			if !errors.Is(err, ErrMalformedMutability) {
				t.Errorf("got %v, want ErrMalformedMutability — the field position's mutability "+
					"byte is the *same* production as the global position's (decode.ml:154, called "+
					"from :244 and :294), and transcribing it at one call site is grave #83", err)
			}
		})
	}

	// The global position, unchanged by #86 and asserted alongside so the pair cannot drift
	// into being two different rules.
	//
	// Derived, not cited: the suite's four `malformed mutability` vectors are all in
	// `global.wast` (:395, :408, :424, :436) and each is a whole module image with a
	// five-byte LEB section size, so none of them is this fixture. This is the same
	// grammar reached by the smallest image that reaches it — the *global* section rather
	// than the import section, since :420's is the global-section form.
	//
	// Written by hand and therefore printed rather than predicted: my first attempt at it
	// declared a 7-byte payload and supplied 6, which failed at the size check having
	// never reached the mutability byte — the fixture asserting a mechanism it did not
	// mean to test. That is what the typeSection helper below exists to prevent, and this
	// literal does not get to use it.
	global := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x06, 0x06, 0x01, 0x7F, 0x04, 0x41, 0x00, 0x0B, // global section: i32, mut 4, i32.const 0, end
	} // derived from global.wast:420 (same grammar, minimal LEB widths)
	if _, err := DecodeModule(global); !errors.Is(err, ErrMalformedMutability) {
		t.Errorf("global position: got %v, want ErrMalformedMutability", err)
	}
}

// TestCompTypeFormIsASignedLEB is the width claim, inherited from
// TestFuncTypeFormIsASignedLEB when the tag read moved to `comptype`.
//
// The tag is a *signed* LEB of width 7, and binary-leb128.wast:1067 is the vector that says
// so: `\e0\7f` is -0x20 in two bytes and the suite wants "integer representation too long"
// rather than a malformed-form error. sleb(7)'s width budget is one byte, so a continuation
// bit exhausts it — which is what "too long" means here.
func TestCompTypeFormIsASignedLEB(t *testing.T) {
	// binary-leb128.wast:1073 — -0x20 encoded in two bytes.
	overlong := typeSection([]byte{0x01, 0xE0, 0x7F, 0x00, 0x00})
	if _, err := DecodeModule(overlong); !errors.Is(err, ErrLEBTooLong) {
		t.Errorf("overlong comptype tag: got %v, want ErrLEBTooLong — a redundant encoding of "+
			"a legal tag is a LEB fault, not a definition-type fault", err)
	}

	// The three real constructors, each one byte on the wire despite being read as an s7.
	for _, tag := range []byte{0x60, 0x5F, 0x5E} {
		r := &reader{b: []byte{tag, 0x00, 0x00}, eof: ErrPayloadEnd}
		_ = (&Decoder{Features: Features{GC: true}}).decodeCompType(r)
		if r.off == 0 {
			t.Errorf("tag %#02x: consumed nothing — a zero-progress read", tag)
		}
	}
}

// TestEverySentinelIsTheReferencesOrIsDeclared is the control for the invented-sentinel
// defect, and it reads the authority because nothing else can.
//
// `malformed function type` was `comptype`'s fallthrough here and **appears nowhere in the
// reference**: its real message is `malformed definition type`. No suite vector asserts
// either string, so the board was blind by construction — grave #36's class one layer out,
// a fabricated *sentinel* rather than a fabricated byte. The defect that hides best is the
// one no oracle reads.
//
// # Scoped to the space, because the sample had three more
//
// The obvious version of this test pins the strings #86 touched. That is a control frozen
// at the moment of authorship, and it would have been *green while three siblings of the
// very defect it names sat in the same var block* — which is not a hypothetical: widening
// it to every sentinel is how `malformed value type`, `malformed element segment flags`,
// and `malformed data segment flags` were found, none of them in the reference and none
// asserted by any vector.
//
// So the domain is derived from `binary.go`'s declarations rather than listed here, and the
// permitted-absence set below is the *finding*, one entry per sentinel with its reason.
// Every engine error is then in one of two states, both explicit: it is a string the
// reference raises, or it is declared here as ours with a justification. Silence is not
// available. (#86.)
// # The authority is every pin's decoder, and it was the core pin's alone
//
// A sentinel is the reference's message or it is ours, and *which* reference stopped being a
// single file when the pin set went plural (ADR 0007's 2026-08-28 amendment). `ErrSharedTable`
// is `tables cannot be shared (yet)` verbatim from the threads pin's `table_type`, and against
// the core decoder alone it presented exactly as a fabricated sentinel does: absent from the
// authority, no vector asserting it. The two available answers were both wrong — flag it, and
// the test fabricates a finding about a correctly transcribed message; write it into `ours`
// with a plausible reason, and a *reference* message is recorded as this engine's invention,
// which is the laundering channel the `ours` map exists to make expensive.
//
// So the domain is the union of the pins' decoders, derived rather than listed
// (`refDecodersML`). The `ours` map keeps its meaning intact: absent from **every** reference
// this project pins.
func TestEverySentinelIsTheReferencesOrIsDeclared(t *testing.T) {
	decoders := refDecodersML()
	// Vacuity on the domain itself: a pin set that yielded one decoder would silently restore
	// the single-authority reading this test was just widened out of, and a set that yielded
	// none would call every sentinel invented.
	if len(decoders) < 2 {
		t.Fatalf("found %d pinned decoders, want >=2 — with one authority a message from the "+
			"other pin reads as fabricated, which is the false finding this widening fixed",
			len(decoders))
	}
	// **Every string literal in the file**, not the ones attached to a recognised raise
	// call. Getting here took two under-matching triggers, and both are #82's class:
	//
	//  1. Matching `error` alone found **21**. The reference raises through *three*
	//     functions — `error` directly, plus `require`/`expect`, which take a message and
	//     delegate (decode.ml:41-51). Adding those two gave 33.
	//  2. Matching `(?:error|require|expect)\b[^"\n]*"..."` still missed **five**, because
	//     `[^"\n]*` cannot cross a newline and four of the reference's `require`s wrap their
	//     message onto the next line (`:1295-1296`, `:1297-1298`, `:1299-1301`, `:350-351`).
	//     The fifth, `illegal opcode `, is *concatenated* at :52 and has no raise keyword
	//     adjacent to it at all. Those five presented exactly as the invented sentinels do —
	//     "absent from the reference" — which is a **false positive** wearing the same
	//     clothes as the real finding, and the only reason they were not written off into
	//     the `ours` map with plausible reasons is that each one was grepped for.
	//
	// So the trigger stops trying to recognise the *call* and matches the *population*: a
	// message can only be raised if it is a literal in this file, so over-collecting is
	// safe here (an extra string simply excuses a sentinel that would otherwise be flagged,
	// and the flag is the assertion) while under-collecting fabricates findings. **When a
	// trigger's two error directions are asymmetric, aim it at the direction that fails
	// loudly.** (#82; grave #78.)
	litRE := regexp.MustCompile(`"([^"\n]*)"`)
	emitted := map[string]bool{}
	// Vacuity floor, per *a comparison against an empty set succeeds*, and it is now **two**
	// floors because the domain is a union. Measured at the pinned revisions: 44 distinct
	// literals in the core decoder, 38 in the threads decoder, 47 in the union. Floored at 30
	// per pin and 40 on the union, leaving upstream room.
	//
	// The per-pin floor is the load-bearing half. A union floor alone is satisfied by the core
	// pin's 44 on its own, so a threads fetch that produced an empty or truncated decoder would
	// clear it while contributing nothing — the whole widening silently undone, and
	// `ErrSharedTable` back to reading as fabricated. *An unmeasured complement is not an empty
	// one*: the union figure cannot see which pin paid for it.
	//
	// The earlier draft of this floor was 40 chosen by feel, and the paragraph explaining it
	// claimed 89, also by feel, in the same breath as the sentence about measuring —
	// *second-order honesty* costing its usual amount. Every number above came out of a counter.
	perPin := map[string]map[string]bool{}
	for _, d := range decoders {
		mine := map[string]bool{}
		for _, m := range litRE.FindAllStringSubmatch(testenv.RequireSpecRef(t, d), -1) {
			mine[m[1]] = true
			emitted[m[1]] = true
		}
		if len(mine) < 30 {
			t.Fatalf("%s yielded only %d distinct string literals, want >=30 — a pin "+
				"contributing nothing restores the single-authority reading without failing "+
				"anything", d, len(mine))
		}
		perPin[d] = mine
	}

	// An empty map would mark every sentinel below "absent from the reference", which fails
	// loudly; the floor is what proves the *reader* worked rather than that the assertions
	// happened to hold.
	if len(emitted) < 40 {
		t.Fatalf("extracted only %d distinct string literals across %d pinned decoders, want "+
			">=40 (47 at the pinned revisions) — the reader has drifted, so this comparison is "+
			"asserting far less than it claims", len(emitted), len(decoders))
	}

	// Sentinels this engine raises that the reference does not, each with the reason it is
	// legitimately ours. An entry here is a claim reviewed by eyes; being *absent* from here
	// while also absent from the reference is what this test fails on.
	ours := map[string]string{
		// Structural, and the reference's own text — but reached via `require`/short forms
		// the regexp above does catch, so these are here only where the wording differs.
		"unexpected end": "the preamble-level short form; three custom.wast vectors expect " +
			"exactly it, and ErrPayloadEnd's longer text begins with it (see binary.go)",

		// This engine's own vocabulary, by design.
		"feature gate disabled": "the gates doctrine's whole point: a construct the spec " +
			"defines and this configuration declines gets a feature-named error, never a " +
			"spec malformed-string (#5)",
		"misplaced opcode": "declared-and-tracked (#6): the reference's two such arms carry " +
			"their own text after the colon, and both are unreachable here — see " +
			"TestEveryReasonRowIsABlockDelimiter",

		// Validation-layer strings, so decode.ml is the wrong authority for them.
		"constant expression required": "valid.ml's, not decode.ml's — a validation verdict " +
			"this decoder can reach from a const-expr position",

		// **The three #88 entries that used to sit here are gone, and their absence is this
		// map's most load-bearing property.** They were `malformed value type`,
		// `malformed element segment flags`, and `malformed data segment flags` — real
		// findings parked here as failing prose, with the issue's definition of done written
		// as "these three entries disappear". They disappeared by the engine adopting the
		// reference's strings, not by the reasons being improved (#88).
		//
		// Nothing marks the absence except this comment, which is the honest situation: a
		// map cannot assert what is not in it. What *does* assert it is the loop below,
		// which now has to find all three strings in decode.ml for the test to pass — so
		// re-introducing any of the invented spellings fails here rather than needing anyone
		// to remember the entries were removed.
	}

	// The domain: every sentinel binary.go declares. Derived, so a sentinel added tomorrow
	// is in scope tomorrow without anyone remembering this test exists.
	declRE := regexp.MustCompile(`(Err\w+)\s*=\s*errors\.New\("([^"]+)"\)`)
	binGo, err := os.ReadFile("binary.go")
	if err != nil {
		t.Fatalf("read binary.go: %v", err)
	}
	decls := declRE.FindAllStringSubmatch(string(binGo), -1)

	// Second vacuity floor, on the *other* input. A moved var block or a changed
	// declaration style yields zero sentinels and a green board asserting nothing at all —
	// the empty-set agreement, which breaking any assertion above would never reveal.
	//
	// **37**, measured by running this regexp and printing the count. This paragraph said
	// "30 at the pinned revision" when #86 wrote it, and 30 was wrong on the day: the real
	// figure was 36, and #88's two new sentinels make it 37. A third fabricated number in
	// the file whose subject is fabricated testimony, in the sentence next to the one about
	// measuring — *second-order honesty* is expensive precisely because the discipline does
	// not exempt its own prose. Floored at 30, below the measurement rather than at it, so
	// upstream trimming a sentinel is not a failure.
	if len(decls) < 30 {
		t.Fatalf("found only %d sentinel declarations in binary.go, want >=30 (37 measured) — "+
			"the extractor is reading past the declarations, so every assertion below is "+
			"vacuous", len(decls))
	}

	// # Retired spellings: what a union domain gives back, and what it takes away
	//
	// Widening the authority from one decoder to the pin set widens the *excusing* direction too,
	// and the first thing it excused was a defect #86 had already paid for. The threads pin
	// branched before upstream renamed `malformed function type` to `malformed definition type`
	// (`spec/binary/decode.ml:259` vs `spec-threads/binary/decode.ml:179`), so under the union an engine sentinel
	// carrying the old spelling is "the reference's message" and the loop below waves it through
	// — green, on the exact string whose removal was #86's definition of done.
	//
	// A pin set is a set of authorities *at different dates*, so the union answers "is this some
	// reference's message?" and cannot answer "is this the message at the site this engine reads?"
	// The clause-scoped rule the two limits masks taught, arriving at a control: consult the pin
	// that owns the clause, never the union, when the question is about a site.
	//
	// So retired spellings are named and refused ahead of the excuse. The value is the pin that
	// owns the site, and the assertion below runs against *that* pin's literals rather than the
	// union.
	retired := map[string]struct{ owner, why string }{
		"malformed function type": {
			owner: refDecodeML,
			why: "renamed to `malformed definition type` at comptype's fallthrough (#86); the " +
				"threads pin branched before the rename, so the union excuses the old spelling " +
				"and re-adopting it would be green",
		},
	}

	for _, d := range decls {
		name, msg := d[1], d[2]
		if r, dead := retired[msg]; dead {
			t.Errorf("%s = %q is a retired spelling: %s.\n\tIt is present in another pin, so "+
				"the union would have excused it — the authority for a site is the pin that "+
				"owns the site (%s)", name, msg, r.why, r.owner)
			continue
		}
		if emitted[msg] {
			continue
		}
		// The reference builds two messages by concatenation — `"illegal opcode " ^
		// string_of_byte b` (decode.ml:52-54) — so its literal carries a trailing space
		// where the sentinel does not, the byte being supplied by the format verb here
		// instead. A prefix match rather than an `ours` entry, because that is what the
		// relationship *is*: the same string, assembled differently. Writing it off as
		// "ours" would have been the third false positive this test's trigger produced.
		if emitted[msg+" "] {
			continue
		}
		if _, declared := ours[msg]; !declared {
			t.Errorf("%s = %q is absent from every pinned decoder (%v) and undeclared here — "+
				"either it is the reference's message spelled wrong (which is what "+
				"`malformed function type` was, green on every board because no vector "+
				"asserts it) or it is legitimately ours, and either way it says so out loud "+
				"in this test's `ours` map (#86).\n\tThe decoder list is derived from the pin "+
				"set, so a message from a proposal this project has not pinned reads exactly "+
				"like a fabricated one — pin the authority rather than writing the message off "+
				"as ours", name, msg, decoders)
		}
	}

	// Each retired spelling asserted absent **from the pin that owns its site**, so the fix
	// cannot silently un-fix itself: if that pin ever re-adopts the string, the engine should
	// adopt it where that pin uses it, rather than resurrecting it where #86 removed it.
	//
	// Reading the union here is what the widening broke — the threads pin has the string, so
	// this fired on a rename the core pin performed and the assertion pointed at the wrong
	// event entirely ("the reference now emits" it, when the reference that matters never
	// stopped). *A citation's authority is the clause it cites, not the corpus it sits in.*
	for msg, r := range retired {
		lits, ok := perPin[r.owner]
		if !ok {
			t.Errorf("retired spelling %q names owner %q, which is not a pinned decoder (%v) — "+
				"an absence asserted against a file nobody reads is asserted against nothing",
				msg, r.owner, decoders)
			continue
		}
		if lits[msg] {
			t.Errorf("%s now emits %q again; it did not when the engine dropped that spelling "+
				"(%s), so re-derive where it belongs rather than reverting", r.owner, msg, r.why)
		}
	}
}

// TestStorageTypeEitherOrderMatchesTheReference pins the branch order of `storagetype`'s
// `either` (decode.ml:236-241), which decides the *message* for a byte that is neither.
//
// The reference tries valtype first and packtype second, so packtype's `malformed storage
// type` (:234) is the message that stands. Swapping them yields `malformed value type` for
// the same input — printed and confirmed: `0x66` reports storage-type in the correct order
// and value-type in the swapped one. No vector reaches this position, so *print-don't-
// predict* is the whole cover, exactly as it is for decodeBlockType's ordering.
func TestStorageTypeEitherOrderMatchesTheReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  []byte
		want error
	}{
		// Both branches, so the ordering is not accidentally satisfied by one arm doing
		// all the work.
		{"i8 packed", typeSection([]byte{0x01, 0x5E, 0x78, 0x00}), nil},
		{"i16 packed", typeSection([]byte{0x01, 0x5E, 0x77, 0x00}), nil},
		{"i32 valtype", typeSection([]byte{0x01, 0x5E, 0x7F, 0x00}), nil},
		{"v128 valtype", typeSection([]byte{0x01, 0x5E, 0x7B, 0x00}), nil}, // SIMD too, below

		// Neither: the last branch's message stands, and that branch is packtype's.
		{"0x66 is neither", typeSection([]byte{0x01, 0x5E, 0x66, 0x00}), ErrMalformedStorageType},
		{"0x00 is neither", typeSection([]byte{0x01, 0x5E, 0x00, 0x00}), ErrMalformedStorageType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// SIMD on as well, since one row's storage type is v128 and a gate-off run
			// would decline it for the wrong reason — which would make this test pass
			// while asserting nothing about the ordering.
			d := &Decoder{Features: Features{GC: true, SIMD: true}}
			_, err := d.DecodeModule(tc.mod)
			if tc.want == nil {
				if err != nil {
					t.Errorf("got %v, want accept", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v — the reference tries valtype first and packtype "+
					"second (decode.ml:236-241), so packtype's message is the one that stands; "+
					"swapping the branches reports `malformed value type` for this input",
					err, tc.want)
			}
		})
	}
}

// TestEitherDoesNotBacktrackAFeatureDecline is the control for the gate/alternation hazard,
// and it exists because the first version of #86 had the bug.
//
// `decodeStorageType` is an `either` whose first branch is `decodeValType`, which holds the
// SIMD gate. A v128 array field with SIMD off therefore produced `malformed storage type:
// 0x7b` — the alternation rewound past a *configuration* answer and let the last branch's
// *grammar* answer stand, which is a gate manufacturing malformedness (#5) in the one form
// the ordering remedy cannot reach: the reference puts valtype first, and that order is
// simultaneously load-bearing for the neither-case message
// (TestStorageTypeEitherOrderMatchesTheReference above).
//
// **Reverting the fix leaves every other test in this package green**, which is the reason
// this one is here and is stated rather than implied: measured, not assumed. No vector
// reaches a v128 storage type, so the board cannot defend it either.
//
// Scoped to the alternation sites and to every gate, rather than to the SIMD/storagetype
// pair that was broken — the pair is today's sample and the property is about the mechanism.
func TestEitherDoesNotBacktrackAFeatureDecline(t *testing.T) {
	// Every gate on except one, so a decline can only be credited to the gate under test.
	// Same construction as TestEveryGateOffDeclinesSomething, for the same reason.
	all := Features{
		ExceptionHandling: true, SIMD: true, Threads: true, Memory64: true,
		GC: true, TailCall: true, RelaxedSIMD: true, MultiMemory: true,
	}
	withoutSIMD := all
	withoutSIMD.SIMD = false
	withoutGC := all
	withoutGC.GC = false

	for _, tc := range []struct {
		name  string
		mod   []byte
		feats Features
		want  string // the feature name the decline must carry
	}{
		// storagetype = either(valtype, packtype): the site the fix was found at. The gate
		// is in the *first* branch and packtype is last, so a backtracked decline surfaces
		// as `malformed storage type`.
		{"v128 array field, SIMD off", typeSection([]byte{0x01, 0x5E, 0x7B, 0x00}), withoutSIMD, "simd"},
		{"v128 struct field, SIMD off", typeSection([]byte{0x01, 0x5F, 0x01, 0x7B, 0x00}), withoutSIMD, "simd"},

		// A functype param is a bare valtype with no alternation above it, so this row is
		// the control's control: it must decline identically, which shows the fix did not
		// merely relabel the storagetype path.
		{"v128 functype param, SIMD off", typeSection([]byte{0x01, 0x60, 0x01, 0x7B, 0x00}), withoutSIMD, "simd"},

		// The other alternation site, with the gated branch last — where ordering was
		// already sufficient. Asserted anyway: if a future edit reorders it, this row
		// fails here rather than silently in the board's blind spot.
		{"GC storage type, GC off", typeSection([]byte{0x01, 0x5E, 0x78, 0x00}), withoutGC, "gc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Decoder{Features: tc.feats}).DecodeModule(tc.mod)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("got %v, want ErrFeatureDisabled — `either` backtracked a feature "+
					"decline and let a later branch's spec malformed-string stand, which reports "+
					"the engine's own configuration as a property of the module (#5, #86)", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %q, want a decline naming %q", err, tc.want)
			}
		})
	}
}

// typeSection wraps a type-section payload in a minimal module image.
//
// A helper rather than a literal per case: the size byte is derived from the payload, so a
// case cannot silently declare the wrong length and test the size mechanism instead of the
// grammar. That mistake is available in every hand-written image in this package and this
// file has twenty-odd of them.
func typeSection(payload []byte) []byte {
	mod := append([]byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, byte(len(payload)),
	}, payload...)
	return mod
}
