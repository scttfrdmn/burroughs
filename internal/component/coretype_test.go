package component

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAnI64NewIndirectFTIsRefusedAtLoad is #969's witness: the one place Burroughs accepted a component the
// reference toolchain refuses.
//
// `cmthreads/i64-ft.wasm` declares `ft = (i64) -> ()` over a table holding an `(i32) -> ()` function.
// `wasm-tools validate` and wasmtime 49.0.2 both reject it before anything runs — *"start function must
// take a single `i32` argument (currently)"*, recorded in `i64-ft.reading`. Before #969 Burroughs loaded
// it, ran it and returned 2, because it decoded no `core:type` section and checked only the callee.
//
// **Asserted against both gate settings**, because the refusal is a validity refusal at load and must not
// depend on whether `gate:cm-threads` would later admit the component.
//
// **Neuter, run:** deleting the `checkNewIndirectFT` call in `parseCanons` brings the defect back exactly —
// the component loads and `run` returns 2 — and fails this on its first assertion.
func TestAnI64NewIndirectFTIsRefusedAtLoad(t *testing.T) {
	wasm, err := os.ReadFile("testdata/cmthreads/i64-ft.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		t.Run("BURROUGHS_CM_THREADS="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			t.Setenv(asyncGateEnv, "1")
			_, err := Load(wasm)
			if err == nil {
				// Say what the defect does, not only that it is present: run it, as #969 did.
				ran := "did not instantiate"
				if in, ierr := Instantiate(wasm); ierr == nil {
					vals, cerr := in.CallValues("run", canon.U32(0))
					ran = fmt.Sprintf("ran, returned %v (err %v)", vals, cerr)
					in.Close()
				}
				t.Fatalf("the component LOADED and %s. wasm-tools and wasmtime both reject it as invalid, "+
					"so loading it is #969's defect — accepting input the reference toolchain refuses", ran)
			}
			if !errors.Is(err, ErrUnsupportedForm) {
				t.Errorf("refused, but not as ErrUnsupportedForm, so the public boundary does not "+
					"classify it the way it classifies the 🧵 decode refusals: %v", err)
			}
			// The same constraint the toolchain quotes, so a reader comparing outputs sees one rule.
			if !strings.Contains(err.Error(), "start function must take a single `i32` argument") {
				t.Errorf("the refusal does not quote the toolchain's constraint: %v", err)
			}
		})
	}
}

// TestTheCoreTypeDecoderCountsWhatItCannotDecode holds the decoder's one safety property: an entry it
// cannot decode is still COUNTED into the core type space, and is opaque rather than acceptable.
//
// A section of three entries — a plain `(i32) -> ()`, then a `0x4E` rec group the decoder does not read,
// then another plain functype it would read if it got there — must define three core types, the first
// decoded and the second and third opaque. If the decoder stopped counting at the rec group, every later
// index in the component would shift by two and name the wrong type.
func TestTheCoreTypeDecoderCountsWhatItCannotDecode(t *testing.T) {
	c := &Component{}
	body := []byte{
		0x03,                   // three entries
		0x60, 0x01, 0x7F, 0x00, // (func (param i32))
		0x4E, 0x00, // (rec) — an empty rec group: not a plain functype, so decoding stops here
		0x60, 0x00, 0x00, // (func) — never reached by the decoder, still counted
	}
	if err := c.parseCoreTypes(body); err != nil {
		t.Fatalf("parseCoreTypes: %v", err)
	}
	if len(c.CoreTypes) != 3 {
		t.Fatalf("%d core types defined; want 3 — every entry counts, decoded or not", len(c.CoreTypes))
	}
	if d, ok := c.coreTypeAt(0); !ok || !d.Func.isI32Entry() {
		t.Errorf("core type 0 is %+v, %v; want a decoded (i32) -> ()", d, ok)
	}
	for _, i := range []uint32{1, 2} {
		if d, _ := c.coreTypeAt(i); d.Func != nil {
			t.Errorf("core type %d decoded as %+v; past the first undecodable entry everything is opaque, "+
				"since the decoder cannot know where the next entry begins", i, d.Func)
		}
		if err := c.checkNewIndirectFT(i); !errors.Is(err, ErrUnsupportedForm) {
			t.Errorf("a new-indirect naming opaque core type %d was not refused: %v — opaque must never "+
				"be treated as acceptable", i, err)
		}
	}
	if err := c.checkNewIndirectFT(0); err != nil {
		t.Errorf("a new-indirect naming the decoded (i32) -> () was refused: %v", err)
	}
}

// TestANewIndirectFromASyncLiftMatchesTheReading is `sync-new.reading`'s differential (#970).
//
// `new-indirect` from a SYNC lift is valid, and wasmtime 49.0.2 runs `cmthreads/sync-new.wasm` and returns
// 2: the new Thread's index, its body never resumed, the sync lift returning. Burroughs refused it by name
// under ErrUnsupportedForm until #970 built the sync branch. This test asserted that refusal as a
// classification; the refusal is gone, so the test is now the reading's value.
//
// **Neuter, run:** restoring the refusal (`caller == nil` widened back to "no async lift task", i.e.
// `caller.task == nil`) fails this on the error.
func TestANewIndirectFromASyncLiftMatchesTheReading(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "sync-new.reading")
	wasm, err := os.ReadFile("testdata/cmthreads/sync-new.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %d; sync-new.reading records %d — the new Thread's index", got, want)
	}
}

// TestAnI64EntryUnderAnI32FTTraps is #969's other half: with `ft` checked at load, the callee check is the
// reference's `trap_if(f.t != ft)`, and an `(i64) -> ()` table entry is a plain mismatch.
//
// wasmtime 49.0.2 traps on `cmthreads/i64-entry.wasm` — *"start function does not match expected type"*,
// recorded in `i64-entry.reading`. Before #969 Burroughs refused it as an unsupported memory64 form,
// because without `ft` it could not tell a matching i64 pair from a mismatched one. That reason is gone,
// so the refusal must be too: a trap, and not an unsupported-form classification.
func TestAnI64EntryUnderAnI32FTTraps(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	wasm, err := os.ReadFile("testdata/cmthreads/i64-entry.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v — the component is valid; both toolchains load it", err)
	}
	defer in.Close()
	_, err = in.CallValues("run", canon.U32(0))
	var trap *interp.Trap
	if !errors.As(err, &trap) {
		t.Fatalf("not a trap: %v — wasmtime traps here, and the reference's check is trap_if(f.t != ft)", err)
	}
	if !strings.Contains(trap.Reason, "want (i32) -> ()") {
		t.Errorf("the trap does not name the expected entry type: %v", trap.Reason)
	}
	if errors.Is(err, interp.ErrUnsupportedOp) || errors.Is(err, ErrUnsupportedForm) {
		t.Errorf("classified as unsupported, which is the pre-#969 refusal: %v", err)
	}
}
