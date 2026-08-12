package binary

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// Native Go fuzzing over the decoder. The invariant a decoder fuzz target
// checks is not "no bug" — it is **total behaviour**: for any byte string
// whatsoever, DecodeModule returns a module or a declared error, and never
// panics, never hangs, and never reports a nil error alongside a nil module.
//
// The corpus is seeded from the spec suite rather than by hand (see
// seedFromSuite). That is deliberate and it is the structural fix for a real
// defect: hand-transcribed fixtures in this file had drifted from the suite
// vectors they claimed to copy, one of them truncated from 11 bytes to 8. A
// corpus read out of testdata/spec cannot drift, because there is no
// transcription step. TestFixtureProvenance (internal/spec) covers what remains
// hand-written; this covers the bulk.

// declaredErrors is every error the decoder is allowed to return. A fuzz find
// that produces something outside this set is either a new declared condition
// (add it here, deliberately) or a bug — and the failure message says which
// question to ask.
var declaredErrors = []error{
	ErrBadMagic,
	ErrBadVersion,
	ErrTruncated,
	ErrLEBTooLong,
	ErrLEBOverflow,
	ErrSectionOverrun,
	ErrTrailingData,
	ErrMalformedSectionID,
	ErrFuncCodeMismatch,
	ErrDataCountMismatch,

	// The payload grammars (#5). ErrPayloadEnd and ErrSectionSizeMismatch are the
	// three faces of the size mechanism; the rest are the malformed-form errors of
	// individual grammars.
	ErrPayloadEnd,
	ErrSectionSizeMismatch,
	ErrMalformedDefType,
	ErrMalformedStorageType,
	ErrMalformedNumType,
	ErrMalformedVecType,
	ErrMalformedRefType,
	ErrMalformedLimits,
	ErrMalformedMutability,
	ErrMalformedImportKind,
	ErrMalformedExportKind,

	// The name grammar (#26): a name's bytes must be well-formed UTF-8.
	ErrMalformedUTF8,

	// The constexpr production and the three sections that need it (#25).
	ErrMalformedElemSegKind,
	ErrMalformedElemKind,
	ErrMalformedDataSegKind,

	// The instruction grammar (#43/#39). ErrNonConstantExpr used to stand here as the
	// one error naming the *reader's* limit rather than a spec verdict, covering both
	// "no such opcode" and "real opcode, not constant" because that reader could not
	// tell them apart. The authority-derived table does, so the single entry became
	// three verdicts:
	//
	//	ErrIllegalOpcode      malformed — no arm in decode.ml, or one that only rejects
	//	ErrConstExprRequired  invalid   — a real instruction that is not constant
	//	ErrEndExpected        malformed — `end_ s` found something that is not END
	ErrIllegalOpcode,
	ErrConstExprRequired,
	ErrEndExpected,

	// The rest of the instruction grammar's malformed forms.
	//
	// ErrMalformedHeapType moved here from unreachability with #88: `immHeapType` reads
	// `heaptype` rather than `reftype`, so ref.null/ref.test/ref.cast can now surface it.
	// It was previously reachable only through reftype's parameterized prefixes, which no
	// section grammar the decoder descends into reaches.
	ErrMalformedMemopFlags,
	ErrMalformedCatch,
	ErrMalformedTypeIndex,
	ErrMalformedHeapType,
	ErrTooManyLocals,

	// ErrMalformedBrOnCastFlags — `require (flags land 0xfc = 0)` (decode.ml:642), a real
	// spec verdict with the reference's own message text, so it belongs in this set.
	//
	// **Enrolled by the fuzzer rather than by its author, which is the entry worth reading
	// twice.** Rung 5 slice 2 added the sentinel, its wrap, and two controls asserting
	// `errors.Is(err, ErrMalformedBrOnCastFlags)` — and did not add this line, so
	// `fuzz-smoke` went red on `fb 19` with flags `0x30` inside 41 seconds while `make check`
	// was green. The gap is structural and not a habit: `make check` runs no fuzz target, so
	// the *local* mirror of CI cannot see a new error string, and every control this PR wrote
	// for the condition asks whether the right sentinel came back — none of them asks whether
	// the vocabulary declares it. A control that names the sentinel it expects cannot notice
	// the sentinel is undeclared, because it supplies the very fact the allowlist is missing.
	// So the rule for a PR that introduces a decoder error is: run `make fuzz` before pushing,
	// or expect the enrollment to be found for you.
	ErrMalformedBrOnCastFlags,

	// ErrZeroByteExpected — `zero s = expect 0x00 s "zero byte expected"` (decode.ml:150),
	// and **the sibling the sweep found rather than the fuzzer** (grave #264). It was omitted
	// on the PR that introduced it and has never been observed, because all three of its call
	// sites (`decodeTag`, the 0x40 table form, #51) sit behind gated constructs and a gate-off
	// decoder declines before reaching the reserved byte. Reachable code, unreachable
	// verdict — so the fuzz target could not have found it and will not, until a flip makes
	// those sites live.
	//
	// Listed for exactly the reason ErrMisplacedOpcode below is listed while unreachable: the
	// set is what the decoder is *allowed* to return, and an entry becoming reachable should
	// not look like a fuzz find when it does. Left out, `gate:gc`'s flip would have turned a
	// correct verdict on a malformed module into a red board with no defect behind it — the
	// omission's other failure mode, and the one that arrives without a 41-second reproducer
	// to explain it.
	//
	// Note this is the *opposite* posture to errNoImmReader and errNotEmptyBlockType
	// (instr.go), which are deliberately undeclared. The discriminator is not reachability:
	// it is whether surfacing the error would be a verdict about the module or a bug in this
	// package. Both of those are the engine failing to read a field it was told about; this
	// is the reference's own message about a byte the module got wrong.
	ErrZeroByteExpected,

	// Declared and *unreachable at bdd7164*, which is a different claim from the rest
	// of this list and is stated rather than hidden: both of the authority's reason
	// arms are bytes `block` stops on, so the dispatch never sees them
	// (TestEveryReasonRowIsABlockDelimiter is the tripwire). Listed for the same
	// reason ErrDataCountRequired was listed before it was reachable — the set is what
	// the decoder is *allowed* to return, and an entry becoming reachable should not
	// look like a fuzz find when it does.
	ErrMisplacedOpcode,

	// ErrFeatureDisabled is a declared error but not a malformed-verdict: it means
	// the decoder declined to judge. Listed here because the fuzz target's question
	// is "is this error one the decoder is allowed to return", and it is.
	ErrFeatureDisabled,

	// ErrDataCountRequired became reachable with the code section's grammar (#22,
	// closed inside #39): a body using one of the four data-referencing opcodes in a
	// module with no data count section. It was declared here while unreachable, which
	// is what kept its arrival from looking like a fuzz find.
	ErrDataCountRequired,
}

func FuzzDecodeModule(f *testing.F) {
	seedFromSuite(f)

	f.Fuzz(func(t *testing.T, image []byte) {
		m, err := DecodeModule(image)

		switch {
		case err == nil && m == nil:
			t.Fatalf("nil module with nil error for % x", image)

		case err != nil && m != nil:
			// Not wrong in principle, but this decoder does not do partial
			// results, and a silent change to that policy should be loud.
			t.Fatalf("both module and error (%v) for % x", err, image)

		case err != nil:
			// The error must be one the contract declares. errors.Is, not ==,
			// because wrapping is expected and errorlint enforces the habit.
			for _, want := range declaredErrors {
				if errors.Is(err, want) {
					return
				}
			}
			t.Fatalf("undeclared error %q for % x — add it to declaredErrors if it is a real condition, or fix the decoder", err, image)

		default:
			// Accepted. Payloads alias the input buffer by design (decision
			// 0002), so every section must point inside the image and the
			// spans must be consistent with what was consumed.
			for i, s := range m.Sections {
				if s.Payload == nil {
					continue
				}
				if len(s.Payload) > len(image) {
					t.Fatalf("section %d payload longer than the image it aliases (%d > %d)", i, len(s.Payload), len(image))
				}
			}
		}
	})
}

// FuzzULEB drives the width-parameterized reader directly at both widths. It
// exists because uleb is where graves #2, #3 and #36 all lived — three defects in
// one function, which is the argument for fuzzing it directly rather than only
// through module images.
//
// It originally cited #19 as its reason for exercising the 64-bit width, u64 having
// had no production caller. It has one now (limits min/max, #36), so the reason is
// the stronger one: the two widths are different code paths through the same budget
// arithmetic, and #36's defect was reachable at both.
func FuzzULEB(f *testing.F) {
	// Boundary seeds: the taxonomy's two failure classes at both widths, plus
	// the legal maxima. These are extracted integer fields, not module images,
	// so they carry no suite citation by construction — see TestLEBTaxonomy.
	seeds := [][]byte{
		{0x00},
		{0x7F},
		{0x80, 0x01},
		{0xE5, 0x8E, 0x26},
		{0xFF, 0xFF, 0xFF, 0xFF, 0x0F},       // u32 max, legal
		{0xFF, 0xFF, 0xFF, 0xFF, 0x1F},       // unused bits set
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, // continuation past the limit
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01},       // u64 max, legal
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}, // u64 too long
		{0x80},
	}
	for _, s := range seeds {
		for _, bits := range []uint{32, 64} {
			f.Add(s, bits)
		}
	}

	f.Fuzz(func(t *testing.T, b []byte, bitsIn uint) {
		// Only the two widths the format uses; uleb is not a general reader.
		bits := uint(32)
		if bitsIn%2 == 1 {
			bits = 64
		}

		r := &reader{b: b}
		v, err := r.uleb(bits)
		if err != nil {
			for _, want := range []error{ErrTruncated, ErrLEBTooLong, ErrLEBOverflow} {
				if errors.Is(err, want) {
					return
				}
			}
			t.Fatalf("undeclared uleb error %q for % x at %d bits", err, b, bits)
		}

		// The width invariant: a successful read never yields a value wider than
		// the width requested. This is the property the taxonomy grave violated
		// in its inverse form, and it is cheap to assert on every input.
		if bits == 32 && v > 0xFFFFFFFF {
			t.Fatalf("uleb(32) returned %#x, wider than 32 bits, for % x", v, b)
		}

		// Progress: a successful read consumed at least one byte and no more
		// than the width permits. A parser that reports success having consumed
		// nothing is the zero-progress bug that grave #18 wore in the lexer.
		maxBytes := int((bits + 6) / 7)
		if r.off < 1 || r.off > maxBytes {
			t.Fatalf("uleb(%d) consumed %d bytes for % x, want 1..%d", bits, r.off, b, maxBytes)
		}
	})
}

// FuzzConstExprProgress drives the const-expression reader directly, because it is
// the decoder's only *unbounded* loop and therefore the only place the zero-progress
// shape (grave #18) can appear here.
//
// The loop's exit predicate (END) and its error predicate (byte not in the table)
// are deliberately different expressions, so progress is structural — but "the code
// looks right" is what grave #18 also looked like, and the discipline asks for a
// target rather than an argument. Two properties, and the second is the one the
// suite cannot ask about:
//
//  1. Termination and progress: return means at least one byte consumed. A reader
//     that returns nil having consumed nothing would make the caller's vec loop spin.
//     Sharper now that the grammar recurses: `block` calls `instr` calls `structural`
//     calls `block`, so an arm consuming nothing before it recurses would not merely
//     spin the caller's vec loop, it would recurse until the stack ran out.
//  2. Declared errors only, and the invalid verdict never wearing a malformed string.
//     This half **inverted** with the dissolution (#43/#39). It used to assert the
//     error contained *neither* "illegal opcode" *nor* "constant expression required",
//     on the grounds that the reader could not distinguish malformed from invalid and
//     so must claim neither. It can now, so both strings are legitimate — and what is
//     left to police is the direction no suite vector can reach: an *invalid* verdict
//     must not say "malformed", because that slanders a module the spec accepts.
//
// Seeded from valid encodings and from every rejection class, including the ones the
// dissolution made reachable. The corpus for this target is small by construction:
// these are extracted expression bytes, not module images, so there is nothing in the
// suite to derive them from — which is why TestConstExprExtentIsDiscovered carries the
// width assertions instead.
func FuzzConstExprProgress(f *testing.F) {
	for _, s := range [][]byte{
		{0x0B},             // bare end
		{0x41, 0x00, 0x0B}, // i32.const 0
		{0x41, 0x7F, 0x0B}, // i32.const -1
		{0x42, 0x80, 0x80, 0x80, 0x80, 0x78, 0x0B}, // i64.const, multibyte sleb
		{0x43, 0x00, 0x00, 0x80, 0x3F, 0x0B},       // f32.const
		{0x44, 0, 0, 0, 0, 0, 0, 0xF0, 0x3F, 0x0B}, // f64.const
		{0x23, 0x00, 0x0B},                         // global.get
		{0xD0, 0x70, 0x0B},                         // ref.null funcref
		{0xD2, 0x00, 0x0B},                         // ref.func
		{0x41, 0x01, 0x41, 0x02, 0x0B},             // two instructions
		{0x92, 0x0B},                               // f32.add: real opcode, not const
		{0xF3, 0x0B},                               // no such opcode
		{0x41},                                     // truncated immediate
		{},                                         // empty: no END at all

		// The structural arms, which are what the progress property is really about now.
		// A mutator reaches these from the flat seeds only by inventing a matching END,
		// so they are seeded rather than hoped for.
		{0x02, 0x40, 0x0B, 0x0B},             // block (empty result), empty body
		{0x03, 0x40, 0x0B, 0x0B},             // loop
		{0x04, 0x40, 0x0B, 0x0B},             // if with no else
		{0x04, 0x40, 0x05, 0x0B, 0x0B},       // if with an else arm
		{0x02, 0x02, 0x40, 0x0B, 0x0B, 0x0B}, // nested block
		{0x02, 0x40},                         // block with no END at all
		{0x1F, 0x40, 0x00, 0x0B},             // try_table, empty handler vec

		// The prefix escapes: a u32 sub-opcode, not a byte.
		{0xFC, 0x08, 0x00, 0x00, 0x0B},                   // memory.init — sets sawDataRef
		{0xFC, 0x87, 0x80, 0x80, 0x80, 0x80, 0x00, 0x0B}, // over-wide sub-opcode LEB
		{0xFD, 0x0C, 0x00, 0x0B},                         // the SIMD region
		{0xFB, 0x00, 0x00, 0x0B},                         // the GC region
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		d := &Decoder{}
		r := &reader{b: b, eof: ErrPayloadEnd}
		err := d.decodeConstExpr(r)

		if r.off == 0 {
			// Zero bytes consumed is only defensible on an empty input, where there
			// was nothing to consume. Anything else is the zero-progress bug.
			if len(b) != 0 {
				t.Fatalf("consumed 0 of %d bytes (err=%v) for % x — a reader that neither progresses nor fails is the shape of grave #18", len(b), err, b)
			}
			return
		}

		if err == nil {
			// Success means an END was reached, so the last byte consumed is one.
			if b[r.off-1] != opEnd {
				t.Fatalf("accepted % x consuming %d bytes, but byte %d is %#02x, not END", b, r.off, r.off-1, b[r.off-1])
			}
			return
		}

		// Every error the instruction grammar is allowed to produce. The list grew
		// fivefold with the dissolution, and the reasons are worth the space:
		//
		//	reader errors          truncation and the two LEB budget failures
		//	ErrIllegalOpcode       no arm in the authority, or one that only rejects
		//	ErrConstExprRequired   a real instruction that is not constant (invalid)
		//	ErrEndExpected         `end_ s` found a byte that is not END
		//	ErrMisplacedOpcode     unreachable; listed so its arrival is not a fuzz find
		//	numtype/vectype        valtype's first two `either` branches
		//	reftype                valtype's last branch, so the message a byte that is no
		//	                       valtype at all receives — including at a blocktype
		//	heaptype               ref.null / ref.test / ref.cast's immediate (#88)
		//	ErrMalformedTypeIndex  blocktype's and heaptype's negative-s33 branches
		//	ErrMalformedCatch      try_table's handler kind byte
		//	ErrMalformedMemopFlags a memarg whose flags field is >= 0x80
		for _, want := range []error{
			ErrTruncated, ErrPayloadEnd, ErrLEBTooLong, ErrLEBOverflow,
			ErrIllegalOpcode, ErrConstExprRequired, ErrEndExpected, ErrMisplacedOpcode,
			ErrMalformedNumType, ErrMalformedVecType, ErrMalformedRefType,
			ErrMalformedHeapType, ErrMalformedTypeIndex,
			ErrMalformedCatch, ErrMalformedMemopFlags, ErrFeatureDisabled,
		} {
			if !errors.Is(err, want) {
				continue
			}
			// The inverted spoof check, on every input rather than the four in the unit
			// test. An invalid verdict must not claim the module is malformed: the
			// accept-direction failure §9 G-3 names, which no assert_malformed vector can
			// see, the modules in question being well-formed.
			if errors.Is(err, ErrConstExprRequired) && strings.Contains(err.Error(), "malformed") {
				t.Fatalf("error %q for % x is the invalid verdict wearing a malformed string — "+
					"the module is well-formed and the engine is calling it broken", err, b)
			}
			// And the malformed verdict must not borrow the invalid one's string, which
			// is the same confusion pointed the other way.
			if errors.Is(err, ErrIllegalOpcode) && strings.Contains(err.Error(), "constant expression required") {
				t.Fatalf("error %q for % x claims both verdicts at once", err, b)
			}
			return
		}
		t.Fatalf("undeclared constexpr error %q for % x", err, b)
	})
}

// seedFromSuite adds every module image in the vendored spec suite to the
// corpus. When the suite is absent the target still runs — a fuzz target with no
// seeds is weaker, not broken — so a fresh clone needs no fetch to fuzz. That
// license is revoked by BURROUGHS_NO_SKIP=1, which CI sets: silently seeding from
// two boundary literals instead of 809 suite images is a downgrade no exit code
// reports.
//
// This deliberately does not import internal/spec: that package imports this
// one for its board, and the cycle would be real. A minimal reader for the
// `(module binary "...")` form is the cheaper of the two fixes. internal/testenv
// is safe to import from both — it depends on neither.
func seedFromSuite(f *testing.F) {
	f.Helper()

	const suiteDir = "../../testdata/spec"
	paths := testenv.SuiteFiles(f, suiteDir)
	if len(paths) == 0 {
		f.Log("spec suite not vendored; fuzzing with boundary seeds only (run: make spec-tests)")
		f.Add([]byte{})
		f.Add([]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00})
		return
	}

	var n int
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, img := range binaryModuleImages(string(src)) {
			f.Add(img)
			n++
		}
	}
	f.Logf("seeded %d module images from %d suite files", n, len(paths))
}

// binaryModuleImages extracts the images from `(module binary "..." ...)` forms.
// Intentionally simple: it scans for the literal `(module binary` (and the
// `(module $name binary` variant), then decodes the string literals that follow
// until a non-literal token ends the form. A miss costs a seed, not a wrong
// test, which is why an approximate scanner is acceptable here and would not be
// in internal/spec.
//
// Note the empty image: `(module binary "")` is a real suite vector, so an
// extracted image of length zero is a legitimate result and must be emitted.
// An earlier version deduplicated seeds by comparing &img[0], which panicked on
// exactly that case — the empty corpus entry is the one the harness most wants,
// since it is the "unexpected end" boundary.
func binaryModuleImages(src string) [][]byte {
	var out [][]byte
	rest := src
	for {
		j := strings.Index(rest, "(module")
		if j < 0 {
			return out
		}
		rest = rest[j+len("(module"):]

		// Skip an optional $name, then require the `binary` keyword.
		tail := strings.TrimLeft(rest, " \t\n\r")
		if strings.HasPrefix(tail, "$") {
			if k := strings.IndexAny(tail, " \t\n\r"); k > 0 {
				tail = strings.TrimLeft(tail[k:], " \t\n\r")
			}
		}
		if !strings.HasPrefix(tail, "binary") {
			continue
		}
		tail = tail[len("binary"):]

		img := []byte{} // non-nil: the empty image is a real vector
		sawString := false
		for len(tail) > 0 {
			switch tail[0] {
			case ' ', '\t', '\n', '\r':
				tail = tail[1:]
				continue
			case '"':
				lit, adv := scanWastString(tail)
				if adv == 0 {
					tail = "" // unterminated; abandon this form
					continue
				}
				img = append(img, lit...)
				sawString = true
				tail = tail[adv:]
				continue
			}
			break // a non-literal token closes the run
		}
		if sawString {
			out = append(out, img)
		}
		rest = tail
	}
}

// scanWastString decodes one `"..."` literal, returning the bytes and how many
// source bytes it spanned. Only \hh and the simple escapes appear in the
// byte-string corpus; anything unrecognized is passed through as a literal byte,
// because a mis-decoded seed is still a legal fuzz input.
func scanWastString(s string) ([]byte, int) {
	if len(s) == 0 || s[0] != '"' {
		return nil, 0
	}
	var out []byte
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '"':
			return out, i + 1
		case '\\':
			if i+2 < len(s) {
				hi, ok1 := hexDigit(s[i+1])
				lo, ok2 := hexDigit(s[i+2])
				if ok1 && ok2 {
					out = append(out, hi<<4|lo)
					i += 2
					continue
				}
			}
			if i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					out = append(out, '\n')
				case 't':
					out = append(out, '\t')
				case 'r':
					out = append(out, '\r')
				default:
					out = append(out, s[i+1])
				}
				i++
			}
		default:
			out = append(out, s[i])
		}
	}
	return nil, 0 // unterminated
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
