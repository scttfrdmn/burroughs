package binary

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	ErrMalformedFuncType,
	ErrMalformedValType,
	ErrMalformedRefType,
	ErrMalformedLimits,
	ErrMalformedMutability,
	ErrMalformedImportKind,
	ErrMalformedExportKind,

	// ErrFeatureDisabled is a declared error but not a malformed-verdict: it means
	// the decoder declined to judge. Listed here because the fuzz target's question
	// is "is this error one the decoder is allowed to return", and it is.
	ErrFeatureDisabled,

	// ErrDataCountRequired is declared here though the decoder cannot yet reach
	// it (#22). Listing it now is the honest order: the set is what the decoder is
	// *allowed* to return, and an entry that becomes reachable later should not
	// look like a fuzz find when it does.
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
// exists because uleb is where graves #2 and #3 lived, and because u64 has no
// production caller yet (#19) — fuzzing is what keeps that width honest in the
// meantime.
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

// seedFromSuite adds every module image in the vendored spec suite to the
// corpus. When the suite is absent the target still runs — a fuzz target with no
// seeds is weaker, not broken — so a fresh clone needs no fetch to fuzz.
//
// This deliberately does not import internal/spec: that package imports this
// one for its board, and the cycle would be real. A minimal reader for the
// `(module binary "...")` form is the cheaper of the two fixes.
func seedFromSuite(f *testing.F) {
	f.Helper()

	const suiteDir = "../../testdata/spec"
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil || len(paths) == 0 {
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
