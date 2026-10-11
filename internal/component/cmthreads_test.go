package component

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// # ADR 0100's first build slice: `gate:cm-threads`, the Thread table, and `thread.index`
//
// Four witnesses, and they fail for unrelated reasons, which is why they are four:
//
//   - the gate's three-way matrix — what an operator is told for each combination;
//   - the differential — what `thread.index` returns, against the committed wasmtime reading;
//   - the table's allocation policy — index 0 reserved, LIFO free list, as the reference specifies;
//   - the partition — every one of the eleven 🧵 productions is modeled or refused, never both and
//     never neither.

// TestTheGateMatrixSaysWhichGateIsMissing is the three-way matrix ADR 0100's point 2 describes.
//
// **The fixture uses `thread.index` and nothing else**, deliberately: `cmthreads/switch.wasm` uses all
// eight phase-1 forms, so `thread.resume-later` is still refused at *decode* and no gate ever speaks.
// A matrix built on that fixture would show the same decode refusal in all three cells and assert
// nothing about the gate — which is what the first run of this matrix did, before the fixture changed.
//
// **`BURROUGHS_ASYNC=0` is how the dependency cell is reached**, because `gate:async` is default-ON since
// the 2026-09-18 flip. Leaving both variables unset lands in "both on", not in the dependency cell.
func TestTheGateMatrixSaysWhichGateIsMissing(t *testing.T) {
	wasm, err := os.ReadFile("testdata/cmthreads/index-call.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing, so this matrix has no subject: %v", err)
	}

	for _, tc := range []struct {
		name       string
		cmThreads  string
		async      string
		wantErr    error
		wantPhrase string
	}{
		{
			name: "both_gates_off", cmThreads: "0", async: "0",
			wantErr:    ErrCMThreadsGated,
			wantPhrase: "set BURROUGHS_CM_THREADS=1",
		},
		{
			name: "cm_threads_on_async_off", cmThreads: "1", async: "0",
			wantErr: ErrCMThreadsNeedsAsync,
			// The remedy must not tell an operator to set the gate they already set. A first draft did,
			// by copying the gated arm's wording into this condition.
			wantPhrase: "Either unset BURROUGHS_ASYNC",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, tc.cmThreads)
			t.Setenv(asyncGateEnv, tc.async)
			_, err := Instantiate(wasm)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v; want %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantPhrase) {
				t.Errorf("the refusal does not carry %q, so it may be the right sentinel from the wrong "+
					"arm: %v", tc.wantPhrase, err)
			}
			// Every cell names the production. A gate refusal that does not say which built-in tripped
			// it sends an operator looking through their whole component.
			if !strings.Contains(err.Error(), "thread.index") || !strings.Contains(err.Error(), "0x26") {
				t.Errorf("the refusal names neither the production nor the opcode: %v", err)
			}
		})
	}

	t.Run("both_on_admits_it", func(t *testing.T) {
		t.Setenv(cmThreadsGateEnv, "1")
		t.Setenv(asyncGateEnv, "1")
		in, err := Instantiate(wasm)
		if err != nil {
			t.Fatalf("with both gates on the component must bind: %v", err)
		}
		in.Close()
	})
}

// TestThreadIndexIsLoweredAndReturnsTheEntryThreadsIndex is the differential against the committed
// wasmtime reading.
//
// **The expected value is read out of `index-call.reading` rather than retyped**, so a reading that moves
// upstream cannot leave this asserting a stale number it also claims to check — the arrangement
// `cancel-wat.reading`'s consumers use.
//
// **It asserts the value and no execution order.** One Thread exists here so there is no order to assert,
// and `switch.reading` records that the reference schedules ready Threads with `random.choice`, so an
// order is not a property of a conforming implementation at any size.
//
// **Neuter, run: `0x26` back to the refusal arm fails THREE tests 3/3**, and the spread is the evidence
// the lowering is load-bearing in more than one place: this one on `Instantiate` (the refusal, not a
// value), the gate matrix on all three cells (a decode refusal precedes any gate), and the partition
// control on its explicit arm (`modeled set is ;` — 0 modeled, 11 refused).
func TestThreadIndexIsLoweredAndReturnsTheEntryThreadsIndex(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")

	want := readIndexCallReading(t)

	wasm, err := os.ReadFile("testdata/cmthreads/index-call.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()

	res, err := in.CallValues("idx")
	if err != nil {
		t.Fatalf("calling idx: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("idx returned %d values, want 1", len(res))
	}
	got, ok := res[0].U32()
	if !ok {
		t.Fatalf("idx returned a %v, not a u32", res[0].Type)
	}
	if got != want {
		t.Errorf("thread.index returned %d; the committed wasmtime reading records %d. The entry "+
			"Thread's index is the reference's reserved-zero Table policy showing through — `array` "+
			"starts as [None], so the first allocation is 1 — and a different number means this "+
			"engine's table does not reserve index 0", got, want)
	}
}

// readIndexCallReading pulls the RESULT line out of the committed reading.
//
// A parser that silently found nothing is the failure mode this guards: the result is returned with a
// found flag and the caller fatals, rather than defaulting to zero and asserting against it.
func readIndexCallReading(t *testing.T) uint32 {
	t.Helper()
	path := filepath.Join("testdata", "cmthreads", "index-call.reading")
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[0] == "RESULT" {
			v, perr := strconv.ParseUint(f[1], 10, 32)
			if perr != nil {
				t.Fatalf("%s: RESULT %q is not a u32: %v", path, f[1], perr)
			}
			return uint32(v)
		}
	}
	t.Fatalf("%s carries no RESULT line, so the oracle is empty", path)
	return 0
}

// TestThreadTableFollowsTheReferencesAllocationPolicy holds the table to `definitions.py`'s `Table`.
//
// **The committed readings do not discriminate the LIFO order** — `switch.reading` has exactly one slot
// free at each creation and `index-call.reading` never frees one — so this is a unit witness against the
// reference's source, not a differential. It is written because ADR 0100's point 4 requires the policy to
// be implemented exactly, and because the order becomes observable the moment two Threads are live and
// freed out of order, which is the fixture a later slice owes.
func TestThreadTableFollowsTheReferencesAllocationPolicy(t *testing.T) {
	tt := newThreadTable()

	// `array = [None]`: index 0 is reserved, so the first allocation is 1.
	if i := tt.add(&cmThread{}); i != 1 {
		t.Fatalf("the first allocation is %d; the reference reserves index 0, so it must be 1", i)
	}
	second := tt.add(&cmThread{})
	third := tt.add(&cmThread{})
	if second != 2 || third != 3 {
		t.Fatalf("appends gave %d and %d; want 2 and 3", second, third)
	}

	// `remove` appends to `free` and `add` pops the END, so reuse is LIFO: free 2 then 3, and the next
	// allocation is 3 — not 2, which a lowest-free policy would give.
	tt.remove(second)
	tt.remove(third)
	if i := tt.add(&cmThread{}); i != 3 {
		t.Errorf("after freeing 2 then 3 the next allocation is %d; the reference's free list is LIFO "+
			"(`remove` appends, `add` pops the end), so it must be 3. A lowest-free policy gives 2, and "+
			"this is the only arm that can tell them apart", i)
	}
	if i := tt.add(&cmThread{}); i != 2 {
		t.Errorf("the second reuse is %d; want 2, the remaining freed slot", i)
	}
	// The free list is empty again, so the next one appends.
	if i := tt.add(&cmThread{}); i != 4 {
		t.Errorf("with the free list drained the next allocation is %d; want 4, an append", i)
	}

	// A double remove must not push one index twice, which would hand it to two Threads.
	tt.remove(2)
	tt.remove(2)
	if i := tt.add(&cmThread{}); i != 2 {
		t.Fatalf("after a double remove of 2 the next allocation is %d; want 2", i)
	}
	if i := tt.add(&cmThread{}); i == 2 {
		t.Error("index 2 was handed out twice, so the double remove pushed it onto the free list twice")
	}

	// Index 0 can never be allocated or freed into circulation.
	tt.remove(0)
	for range 4 {
		if i := tt.add(&cmThread{}); i == 0 {
			t.Fatal("index 0 was allocated; the reference reserves it")
		}
	}
}

// TestEveryThreadProductionIsModeledOrRefusedNeverBoth is the partition control, and it replaces the
// third direction the names-map control used to carry.
//
// That direction asserted *"the map holds nothing the decoder does not refuse"*, which was true while all
// eleven were refused and becomes false the moment one is modeled — a modeled form is still named (the
// name table spells all eleven) but is no longer refused. Relaxing it would have left no total
// accounting, so it is replaced rather than dropped: every production in the pinned table's thread family
// is in exactly one of the two sets.
func TestEveryThreadProductionIsModeledOrRefusedNeverBoth(t *testing.T) {
	spec := readPinnedCanonNames(t)

	var modeled, refused, neither, both []byte
	for op, name := range spec {
		if !strings.HasPrefix(name, "thread.") || name == "thread.yield" {
			continue // thread.yield (0x0c) is 🔀 async per the pinned table, not 🧵
		}
		r := &reader{b: []byte{op}}
		cn, err := r.canon()
		isModeled := err == nil && cn.Kind == CanonThreadBuiltin
		isRefused := err != nil && errors.Is(err, ErrUnsupportedForm)
		switch {
		case isModeled && isRefused:
			both = append(both, op)
		case isModeled:
			modeled = append(modeled, op)
		case isRefused:
			refused = append(refused, op)
		default:
			neither = append(neither, op)
		}
	}

	if len(both) > 0 {
		t.Errorf("opcode(s) %#x are both modeled and refused, which is not a state the decoder can be "+
			"in — one of the two case arms has gained a duplicate", both)
	}
	if len(neither) > 0 {
		t.Errorf("opcode(s) %#x are neither modeled nor refused: the decoder falls through to its "+
			"default arm, which is #957's defect returning — a well-formed component reported as "+
			"malformed, with no sentinel and exit 1", neither)
	}
	// A derivation that found nothing would pass both checks above and assert nothing.
	if len(modeled)+len(refused) == 0 {
		t.Fatal("the pinned table yielded no 🧵 productions, so this control has no population")
	}
	t.Logf("partition: %d modeled %#x, %d refused %#x", len(modeled), modeled, len(refused), refused)

	// ADR 0100's point 4 in one assertion: a form is modeled only with a committed oracle, and this
	// slice lands exactly one. A second modeled form without its reading is what this catches.
	if len(modeled) != 1 || modeled[0] != 0x26 {
		t.Errorf("modeled set is %#x; ADR 0100's first build slice models exactly thread.index (0x26), "+
			"and a form joins it only when a wasmtime reading is committed under testdata", modeled)
	}
}
