// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// typedStreamRows reads a stream-typed-*.reading: each `<export>: <reading>` row, with a `pin <export>:` row
// standing in for wasmtime's where the pin's answer is not wasmtime's (ADR 0102). Comment lines are skipped.
func typedStreamRows(t *testing.T, fixture string) (order []string, want map[string]string) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + fixture + ".reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	want = map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		pin := false
		if rest, ok := strings.CutPrefix(ln, "pin "); ok {
			ln, pin = rest, true
		}
		name, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("%s.reading: row %q has no `: `", fixture, ln)
		}
		if _, seen := want[name]; !seen {
			order = append(order, name)
		}
		if _, seen := want[name]; !seen || pin {
			want[name] = reading
		}
	}
	if len(order) == 0 {
		t.Fatalf("%s.reading has no rows", fixture)
	}
	return order, want
}

// runTypedStreamRow instantiates fixture and calls export, bounded.
func runTypedStreamRow(t *testing.T, b []byte, export string) ([]canon.Value, error) {
	t.Helper()
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() { r, e := in.CallValues(export); ch <- out{r, e} }()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s() did not return within 10s", export)
		return nil, nil
	}
}

// typedStreamTrapNeedles names, for each trap row, the phrase Burroughs' trap carries, so each row is held to its
// own check rather than to any trap. wasmtime's text is in the reading; these are this engine's.
var typedStreamTrapNeedles = map[string]string{
	"misaligned-read":        "not aligned",
	"misaligned-write":       "not aligned",
	"out-of-range-write":     "outside memory",
	"out-of-range-read":      "outside memory",
	"max-length-write":       "longer than",
	"mismatch-write":         "of a different type",
	"mismatch-read":          "of a different type",
	"mismatch-cancel-write":  "of a different type",
	"mismatch-drop-writable": "of a different type",
	"mismatch-drop-readable": "of a different type",
	"string-copy":            "non-numeric payload",
	"string-copy-read-first": "non-numeric payload",
	"own-copy":               "non-numeric payload",
}

// TestTypedStreamElementsMatchTheOracle is ADR 0124's witness (#1275): every row of stream-typed-copy.reading and
// stream-typed-traps.reading, wasmtime 49.0.3's (10 of 10 each), with the pin's answer where it is not wasmtime's,
// in both gate states. A copy strides by the element's size, both orderings; a float's NaN is canonicalised; and
// the pin's GuestBuffer, end-type and same-instance element traps each fire at their row, beside a control that
// does not.
//
// **Neuters, run** (each named at its site):
//   - the copy moving `n` bytes rather than `n * size`: u32-wf-w0 reads 0x00223344 and w1 reads 0, both orderings,
//     and the f32 and f64 rows read partial words;
//   - no NaN canonicalisation: f32-wf-w0, f32-rf-w0 and f64-wf-lo read wasmtime's preserved payload;
//   - no alignment check: misaligned-read and misaligned-write park, BLOCKED (wasmtime's reading);
//   - the range checked in bytes rather than elements: out-of-range-read and out-of-range-write park;
//   - no MAX_LENGTH check: max-length-write still traps, for its range, and fails on its phrase only;
//   - no end-type check: mismatch-write and mismatch-read park, mismatch-cancel-write returns CANCELLED, and the
//     drops succeed;
//   - no same-instance element check: string-copy, string-copy-read-first and own-copy complete, progress 1.
func TestTypedStreamElementsMatchTheOracle(t *testing.T) {
	for _, fixture := range []string{"stream-typed-copy", "stream-typed-traps"} {
		b, err := os.ReadFile("testdata/" + fixture + ".wasm")
		if err != nil {
			t.Fatalf("the committed fixture is missing: %v", err)
		}
		order, want := typedStreamRows(t, fixture)
		for _, gate := range []string{"0", "1"} {
			for _, export := range order {
				reading := want[export]
				t.Run(fixture+"/"+export+"/gate="+gate, func(t *testing.T) {
					t.Setenv(asyncGateEnv, "1")
					t.Setenv(cmThreadsGateEnv, gate)
					res, err := runTypedStreamRow(t, b, export)
					if strings.HasPrefix(reading, "trap") {
						needle, ok := typedStreamTrapNeedles[export]
						if !ok {
							t.Fatalf("%s is a trap row with no needle in typedStreamTrapNeedles", export)
						}
						var trap *interp.Trap
						if !errors.As(err, &trap) || !strings.Contains(err.Error(), needle) {
							t.Fatalf("%s() = %v, %v; want a trap containing %q (the oracle: %s)", export, res, err,
								needle, reading)
						}
						return
					}
					wantV, perr := strconv.ParseUint(reading, 10, 32)
					if perr != nil {
						t.Fatalf("%s: reading %q is neither a trap nor a u32", export, reading)
					}
					if err != nil {
						t.Fatalf("%s(): %v; the oracle reads %d", export, err, wantV)
					}
					if v, ok := res[0].U32(); !ok || uint64(v) != wantV {
						t.Fatalf("%s() = %v; the oracle reads %d", export, res, wantV)
					}
				})
			}
		}
	}
}

// TestAHostConsumerReadsOnlyAStreamOfBytes is the host sink's element check (ADR 0124): write-via-stream's `data`
// parameter is not checked at instantiate, so a guest's stream<u32> handed to it reaches the copy, which refuses it
// by name rather than handing its words to stdout as bytes. A stream<u8>, the control, reaches the sink.
//
// **Neuter, run:** drop the check (driveStreamCopyLocked's sink arm), and the stream<u32> write's 8 bytes reach the
// sink.
func TestAHostConsumerReadsOnlyAStreamOfBytes(t *testing.T) {
	words := &elemType{known: true, modeled: true, t: &canon.Type{Kind: canon.KindU32}, size: 4, align: 4, numeric: true}
	for _, c := range []struct {
		name    string
		elem    *elemType
		refused bool
	}{{"u8", byteElem, false}, {"u32", words, true}} {
		t.Run(c.name, func(t *testing.T) {
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			h := newAsyncHandles()
			ret, err := streamNew(h, c.elem)(cc, nil)
			if err != nil {
				t.Fatalf("stream.new: %v", err)
			}
			packed := uint64(ret[0].Int64())
			ri, wi := int32(packed&0xffffffff), int32(packed>>32)
			var out bytes.Buffer
			if _, werr := writeViaStream(&out)(h)(cc, []interp.Value{interp.I32(ri)}); werr != nil {
				t.Fatalf("write-via-stream: %v", werr)
			}
			_, err = streamWrite(h, c.elem)(cc, []interp.Value{interp.I32(wi), interp.I32(64), interp.I32(2)})
			switch {
			case c.refused && (!errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "stream<u8>")):
				t.Fatalf("stream.write of stream<u32> to write-via-stream: err = %v, sink got %d bytes; want "+
					"ErrUnsupportedForm naming the host's stream<u8>", err, out.Len())
			case c.refused && out.Len() != 0:
				t.Fatalf("the refused copy still reached the sink with %d bytes", out.Len())
			case !c.refused && (err != nil || out.Len() != 2):
				t.Fatalf("stream.write of stream<u8>: err = %v, sink got %d bytes; want 2", err, out.Len())
			}
		})
	}
}
