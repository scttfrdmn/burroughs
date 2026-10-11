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

// # The 🧵 family's refusal, witnessed on real bytes (#957)
//
// Before this slice an eleven-member family of canonical built-ins fell through `canon`'s `default` arm:
// `canon built-in 0x26 is not modeled (this slice: …)`, with no public sentinel, so the CLI classified a
// **well-formed** component as **exit 1** — a corrupt file's code. `threadbuiltins.go` records the three
// decisions behind the repair; these are the arms that hold it.
//
// # The fixture, and how it was produced
//
//	$ cat testdata/thread-index-synth.wat
//	(component
//	  (canon thread.index (core func $ti))
//	)
//	$ wasm-tools parse thread-index-synth.wat -o thread-index-synth.wasm     # wasm-tools 1.258.0
//	38 bytes
//
// Both files are committed, following the flat `*-synth` fixtures beside them: the toolchain is never on
// the test path. **38 bytes, not the 37 quoted on #957** — the same component with a one-character symbol
// name is 37, because `wasm-tools` emits a `component-name` custom section and `$ti` is a byte longer
// than `$x`. Measured with `cmp -l`, which puts the whole difference in that section; recorded because an
// unexplained byte in a committed artefact is worth one sentence.

// TestAThreadBuiltinIsRefusedByNameAsUnsupported drives the committed fixture through the loader.
//
// **Neuter, run and recorded because no arm can hold it.** Returning the `0x26`–`0x42` case arm to
// `canon`'s `default` wording restores `canon built-in 0x26 is not modeled (this slice: …)` with no
// sentinel. Measured: **three tests fail, not one** — this one on `errors.Is` at both gate settings,
// `TestTheThreadBuiltinNamesAgreeWithThePinnedTable` on its vacuity guard (*"no discriminant decodes to
// a thread-built-in refusal"*), and the public boundary's nine-cell table on the sentinel **and** the
// message, through all three doors. That spread is the evidence the repair is load-bearing at the
// boundary and not only here.
//
// The gate-setting loop is what shows the refusal is not a gate's doing: it is identical with
// `BURROUGHS_ASYNC` on and off, because no gate admits an UNMODELED production and the message says so.
//
// # The subject has moved twice, and will move again as each form lands
//
// It read `testdata/thread-index-synth.wasm` until ADR 0100's first build slice modelled `thread.index`
// (0x26), then `cmthreads/switch.wasm`'s first 🧵 canon, `thread.resume-later` (0x28), until the second
// slice modelled that and `thread.new-indirect` (0x27). Each time the test failed for one run with the
// right answer to a question it is not asking. **An UNMODELED production is what it is about**, and the
// decoder stops at the first one. It named `switch.wasm`'s `thread.suspend` (0x29) until the switching
// slice modelled the last five phase-1 forms, which left `switch.wasm` with no unmodelled canon at all.
//
// **This is the last move, as this comment predicted.** The three 🧵② forms are refused by name for good
// under ADR 0100's point 1, so the subject is now one of those, in a fixture of its own:
//
//	$ cat testdata/thread-ap-synth.wat
//	(component
//	  (canon thread.available_parallelism (core func $ap))
//	)
//	$ wasm-tools parse thread-ap-synth.wat -o thread-ap-synth.wasm     # wasm-tools 1.258.0, 38 bytes
//
// The text spelling is wasm-tools' underscore; the refusal spells it the pinned table's hyphenated way
// (`threadbuiltins.go` records the discrepancy). `wasm-tools validate --features all` accepts it, and
// wasmtime 49.0.2 refuses it as *"requires the shared-everything-threads proposal"* — the toolchain's own
// form of "no gate admits it".
func TestAThreadBuiltinIsRefusedByNameAsUnsupported(t *testing.T) {
	wasm, err := os.ReadFile("testdata/thread-ap-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing, so this witness has no subject: %v", err)
	}

	for _, gate := range []string{"0", "1"} {
		t.Run("BURROUGHS_ASYNC="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, gate)
			_, err := Instantiate(wasm)
			if err == nil {
				t.Fatal("the fixture was accepted; nothing in this engine executes thread.available-parallelism")
			}
			// The sentinel a caller branches on, and the one the public boundary maps to
			// ErrUnsupported. No new sentinel was added for this family.
			if !errors.Is(err, ErrUnsupportedForm) {
				t.Fatalf("errors.Is(err, ErrUnsupportedForm) is false, so this crosses as an "+
					"unclassified failure and the CLI falls to its catch-all code: %v", err)
			}
			msg := err.Error()
			// Four arms, one per thing that was wrong with the old refusal. Separate rather than one
			// combined match, so a failure says WHICH property regressed.
			if !strings.Contains(msg, "thread.available-parallelism") {
				t.Errorf("the refusal does not name the production, so a reader gets an opcode and "+
					"has to look it up: %v", err)
			}
			if !strings.Contains(msg, "0x42") {
				t.Errorf("the refusal does not carry the opcode, which is what a reader comparing "+
					"against Binary.md matches on: %v", err)
			}
			if !strings.Contains(msg, "shared-everything-threads") {
				t.Errorf("the refusal does not name the proposal, so it does not say which feature "+
					"is missing: %v", err)
			}
			if !strings.Contains(msg, "well-formed") || !strings.Contains(msg, "no gate admits") {
				t.Errorf("the refusal does not say the component is well-formed and ungated — the "+
					"two things the old message got wrong by implying a parse failure and, read "+
					"charitably, a switch somewhere: %v", err)
			}
			// The old wording, asserted absent. A repair that leaves the misleading sentence in
			// place beside a better one has not repaired the reading.
			if strings.Contains(msg, "is not modeled") {
				t.Errorf("the old decode-refusal wording is back, so #957 is returning: %v", err)
			}
		})
	}
}

// TestTheThreadBuiltinNamesAgreeWithThePinnedTable is the name map's control, in both directions.
//
// The map exists because the engine cannot read the spec table at run time, and a hand-kept list beside a
// case arm is correct exactly once — grave #850, repaired by ADR 0092's two-directional arrangement for
// `isBuiltAsyncBuiltin`. The same shape here:
//
//  1. every discriminant the decoder refuses as a thread built-in has a name, so none can be added to the
//     case arm and print as an unnamed opcode;
//  2. every name equals the pinned table's production for that opcode, so none can drift from the spec;
//  3. the map holds nothing the decoder does not refuse, so a stale entry cannot sit here unnoticed.
//
// `thread.yield` (0x0c) is deliberately **not** a member and the third direction is what would catch it
// being added: the pinned table marks it 🔀 async, so it decodes as a CanonAsyncBuiltin and `gateAsync`
// refuses it (#952).
func TestTheThreadBuiltinNamesAgreeWithThePinnedTable(t *testing.T) {
	// Direction 1 and 3 together: the decoder's own answer for every byte, against the map's keys.
	refused := map[byte]bool{}
	for op := range 0x100 {
		b := byte(op)
		r := &reader{b: []byte{b}}
		_, err := r.canon()
		if err != nil && errors.Is(err, ErrUnsupportedForm) {
			refused[b] = true
		}
	}
	if len(refused) == 0 {
		t.Fatal("no discriminant decodes to a thread-built-in refusal, so this control asserts " +
			"nothing — the case arm is gone or no longer returns ErrUnsupportedForm")
	}
	for op := range refused {
		if _, ok := threadBuiltinNames[op]; !ok {
			t.Errorf("the decoder refuses %#x as a thread built-in but threadBuiltinNames has no "+
				"name for it, so its refusal prints an unnamed opcode", op)
		}
	}
	// **The stale-entry direction is gone, replaced rather than relaxed.** It asserted *"the map holds
	// nothing the decoder does not refuse"*, which was true while all eleven productions were refused
	// and became false the moment ADR 0100 modeled one: `thread.index` is still **named** — the map
	// spells all eleven, because a modeled form's name is what the gate's refusal prints — and is no
	// longer **refused**. Weakening it would have left no total accounting of the eleven, so
	// `TestEveryThreadProductionIsModeledOrRefusedNeverBoth` carries that instead: every production is
	// in exactly one of the two sets, never both and never neither. The vacuity guard above and
	// direction 2 below are unaffected.

	// Direction 2: every name against the pinned spec table.
	spec := readPinnedCanonNames(t)
	for op, name := range threadBuiltinNames {
		got, ok := spec[op]
		if !ok {
			t.Errorf("%#x (%q) is not a production in the pinned table, so the map names something "+
				"the spec does not have at this pin", op, name)
			continue
		}
		if got != name {
			t.Errorf("%#x: the map says %q, the pinned table says %q", op, name, got)
		}
	}
}

// readPinnedCanonNames reads opcode -> production from the committed spec table. A thin reader rather
// than a reuse of canonbuiltins_test.go's: that one builds the richer canonRow the status witness needs,
// and this control wants only the two columns it compares.
//
// The table's ops column can carry more than one byte for a form with a fixed trailing operand
// (`0x29 0x00` for thread.suspend). The first byte is what the decoder keys on, so that is what is read —
// and that this is the first byte rather than the whole form is the thing to notice when a two-byte
// production is ever given a distinct meaning by its second byte.
func readPinnedCanonNames(t *testing.T) map[byte]string {
	t.Helper()
	path := filepath.Join("testdata", "canon-builtins.tsv")
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("the pinned spec table is missing, so this control has no oracle: %v", err)
	}
	defer fh.Close()

	out := map[byte]string{}
	sc := bufio.NewScanner(fh)
	for ln := 1; sc.Scan(); ln++ {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 || f[0] == "pin" {
			continue
		}
		first, _, _ := strings.Cut(f[0], " ")
		v, perr := strconv.ParseUint(strings.TrimPrefix(first, "0x"), 16, 8)
		if perr != nil {
			t.Fatalf("%s:%d: the ops column is not a hex byte: %q", path, ln, f[0])
		}
		out[byte(v)] = f[1]
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// A parse that silently found nothing would make every comparison above vacuous.
	if len(out) == 0 {
		t.Fatalf("%s parsed to zero productions, so the oracle is empty", path)
	}
	return out
}
