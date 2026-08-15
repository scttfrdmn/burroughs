package spec

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// relaxedLoweringPins is every `(either …)` expectation in the corpus, with the alternative this
// engine's answer matches.
//
// **This table is the instrument decision 0028 d1's guarantee did not have.** 0028 promises more
// than the spec asks: relaxed-SIMD lowerings here are deterministic *and architecture-uniform*.
// The spec asks only that the answer be one of the listed alternatives, so every vector below
// passes under either lowering — which means the board, both lanes, and CI on both architectures
// are all structurally blind to the choice. #279's tripwire measured the size of that blindness
// on one opcode family and found the two f32 madd lowerings differ on 4 of 1000 triples; this
// table is the general form of that finding, and it is pre-flip work for the reason the flip
// makes it matter: after the flip these lowerings are what `DefaultFeatures()` ships.
//
// **What is pinned is the alternative's text, not its index.** An index is a position in the
// corpus's list, so an upstream reordering would move it with no lowering having changed; the
// text is the answer. The index is logged beside it for readability and is not asserted.
//
// **There is no oracle here and the table does not pretend to be one.** Where the corpus admits
// several answers there is by construction nothing that says which is right, so this is a *pin*:
// its whole claim is that the choice does not change silently. That is exactly what a refactor of
// the arms would do — every value below stays inside the permitted set, so a rewired helper, a
// different NaN preference, an unfused multiply-add, or a masked dot-product operand all keep the
// board green and move a row here.
//
// Read as a menu, the alternatives are informative in their own right, and the corpus says so
// outright: `relaxed_dot_product.wast:29-31` labels its three alternatives *signed × unsigned*,
// *signed × signed* and *unsigned × unsigned*, and the row below pins the middle one — which is
// `Convert.I16_.extend_i8_s` over both operands (0028 d2, and `simd_relaxed.go`'s own note that
// the `i7x16` in the mnemonic names a range and not a masking step).
//
// The values were **recorded, not derived** — there is no independent authority to derive them
// from — and the recording was made once, on arm64, then confirmed identical on amd64 under
// `--platform linux/amd64`. CI's two runners re-confirm it on every push, which is where the
// architecture-uniformity half of 0028 d1 is actually measured.
var relaxedLoweringPins = map[string]map[int]string{
	"i8x16_relaxed_swizzle.wast": {
		// Out-of-range indices give zero, which is `i8x16.swizzle`'s rule taken unchanged
		// (0028 d2, `eval_vec.ml:64`). The other alternative is the identity — what a
		// `PSHUFB` lowering produces, since it masks an index to its low four bits when the
		// high bit is clear.
		12: "v128 [i32 0, i32 1, i32 2, i32 3, i32 4, i32 5, i32 6, i32 7, i32 8, i32 9, i32 10, i32 11, i32 12, i32 13, i32 14, i32 15]",
		19: "v128 [i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0]",
		26: "v128 [i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0]",
	},
	"i16x8_relaxed_q15mulr_s.wast": {
		// The **second** alternative, and the one row in this table that does not pin index
		// 0: `-32768` is offered first and this engine gives `32767`, because 0x111 aliases
		// the saturating `i16x8.q15mulr_sat_s` (`eval_vec.ml:84`). A wrapping lowering would
		// give the first alternative and stay green.
		13: "v128 [i32 32767, i32 32767, i32 32766, i32 0, i32 0, i32 0, i32 0, i32 0]",
	},
	"relaxed_madd_nmadd.wast": {
		// The fused answers. Every one of these is a lane where fusing and not fusing differ
		// — that is what the vectors were written to expose — so this block is where an
		// `x*y + z` written as a bare expression would be caught, which is the shape decision
		// 0028 d3 forbids outright in this engine's floating-point paths.
		33:  "v128 [f32 0x7f7fffff (3.4028235e+38), f32 0x7f7fffff (3.4028235e+38), f32 0x7f7fffff (3.4028235e+38), f32 0x7f7fffff (3.4028235e+38)]",
		49:  "v128 [f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12)]",
		56:  "v128 [f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12)]",
		63:  "v128 [f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12), f32 0x2d000000 (7.275958e-12)]",
		75:  "v128 [f64 0x7fefffffffffffff (1.7976931348623157e+308), f64 0x7fefffffffffffff (1.7976931348623157e+308)]",
		91:  "v128 [f64 0x3ca0000000000000 (1.1102230246251565e-16), f64 0x3ca0000000000000 (1.1102230246251565e-16)]",
		98:  "v128 [f64 0x3ca0000000000000 (1.1102230246251565e-16), f64 0x3ca0000000000000 (1.1102230246251565e-16)]",
		105: "v128 [f64 0x3ca0000000000000 (1.1102230246251565e-16), f64 0x3ca0000000000000 (1.1102230246251565e-16)]",
	},
	"relaxed_min_max.wast": {
		// NaN propagation and the sign of zero, which are the two dimensions `MINPS`/`MAXPS`
		// differ from the reference on: the hardware forms return their second operand for a
		// NaN operand and for a zero pair. The four-wide alternatives here are the corpus
		// enumerating per-lane combinations of those two behaviours, and this engine takes
		// the reference's `f32x4.min`/`max` unchanged (0028 d2) — the first alternative in
		// every row.
		27:  "v128 [f32 nan:canonical, f32 nan:canonical, f32 nan:canonical, f32 nan:canonical]",
		35:  "v128 [f32 0x80000000 (-0), f32 0x80000000 (-0), f32 0x00000000 (0), f32 0x80000000 (-0)]",
		43:  "v128 [f32 nan:canonical, f32 nan:canonical, f32 nan:canonical, f32 nan:canonical]",
		51:  "v128 [f32 0x00000000 (0), f32 0x00000000 (0), f32 0x00000000 (0), f32 0x80000000 (-0)]",
		59:  "v128 [f64 nan:canonical, f64 nan:canonical]",
		67:  "v128 [f64 nan:canonical, f64 nan:canonical]",
		75:  "v128 [f64 0x8000000000000000 (-0), f64 0x8000000000000000 (-0)]",
		83:  "v128 [f64 0x0000000000000000 (0), f64 0x8000000000000000 (-0)]",
		91:  "v128 [f64 nan:canonical, f64 nan:canonical]",
		99:  "v128 [f64 nan:canonical, f64 nan:canonical]",
		107: "v128 [f64 0x0000000000000000 (0), f64 0x0000000000000000 (0)]",
		115: "v128 [f64 0x0000000000000000 (0), f64 0x8000000000000000 (-0)]",
	},
	"relaxed_laneselect.wast": {
		// Bit-wise selection — `v128.bitselect` unchanged, which is why `execFD` joins 0x52
		// with all four laneselect opcodes. The alternative each row rejects is the
		// per-lane-MSB blend a `PBLENDVB` lowering gives, and the corpus's own comment at
		// `:46` labels its alternative `;; bitselect`, naming the distinction outright.
		27: "v128 [i32 0, i32 17, i32 20, i32 50, i32 20, i32 21, i32 22, i32 23, i32 24, i32 25, i32 26, i32 27, i32 28, i32 29, i32 30, i32 31]",
		34: "v128 [i32 0, i32 9, i32 4728, i32 22068, i32 12, i32 13, i32 14, i32 15]",
		42: "v128 [i32 0, i32 9, i32 4728, i32 22136, i32 12, i32 13, i32 14, i32 15]",
		51: "v128 [i32 0, i32 5, i32 305419896, i32 1450709556]",
		58: "v128 [i64 0, i64 3]",
		65: "v128 [i64 1311693407469983352, i64 6230825158168547892]",
	},
	"relaxed_dot_product.wast": {
		// Signed × signed on both operands, the middle alternative of three at `:32` and the
		// third of four at `:62`. Neither is index 0, and that is the useful part: a lowering
		// that read the second operand as unsigned, or masked it to seven bits as the
		// mnemonic's `i7x16` invites, would land on a *different listed alternative* and the
		// vector would still pass.
		32: "v128 [i32 32512, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0, i32 0]",
		62: "v128 [i32 65025, i32 2, i32 3, i32 4]",
	},
}

// relaxedLoweringWidths is the census of how much freedom the corpus actually grants, keyed by
// alternative count.
//
// Pinned as an exact map rather than as a total, and that is this control's own falsification
// story: the sentence it replaces (`Val.Alts`' doc comment, PR #281) read "all 32 occurrences are
// two flat alternatives", which was a `grep -c` of `(either` *sites* written up as a claim about
// their *widths* — two different measurements, one taken. Grave #282. A total of 32 would have
// agreed with that wrong sentence; the distribution is what disagrees.
var relaxedLoweringWidths = map[int]int{2: 17, 3: 2, 4: 13}

// TestRelaxedLoweringChoicesArePinned is the pin: run every board file with all gates on, and
// require the recorded `(either …)` choices to be exactly the table above.
//
// **Both directions, and the second is the one that matters most.** A row in the table with no
// choice behind it means the vector stopped being asked — retired upstream, or newly failing, or
// gated in a lane this test thought it had turned on — and a pin over vectors nobody runs is the
// vacuum. A choice with no row means an `(either)` site arrived and nothing has decided what this
// engine answers there.
//
// The domain is `boardFiles`, not a list of relaxed-SIMD files: `(either …)` is a corpus-wide
// grammatical form and a proposal landing tomorrow may use it. Scoping to the six files that have
// one today would inherit today's blind spot, which is the control-scoping law this repo already
// has. The `wholeFileGated` tables elsewhere in this package are the counterexample kept
// deliberately: they name files because their subject *is* those files' gates.
func TestRelaxedLoweringChoicesArePinned(t *testing.T) {
	requireSuite(t)

	_, _, allOnEngine := allOnLane(t)

	got := map[string]map[int]string{}
	widths := map[int]int{}
	total := 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := s.RunGated(allOnEngine())
		for _, ac := range r.AltChoices {
			total++
			widths[ac.Of]++
			if ac.Of < 2 {
				t.Errorf("%s:%d offered %d alternative(s): an `either` of one is not a choice, "+
					"and a pin over it asserts nothing", f, ac.Line, ac.Of)
			}
			if got[f] == nil {
				got[f] = map[int]string{}
			}
			if prev, dup := got[f][ac.Line]; dup {
				// One line, two disjunctions — possible in principle for a multi-result
				// command, and the table is keyed by line, so it would silently keep one.
				// No corpus vector does this today; if one arrives the key needs the result
				// index and this says so rather than dropping the second.
				t.Errorf("%s:%d has two `either` results (%q and %q): this table is keyed by "+
					"line and would keep only one — key it by line and result index",
					f, ac.Line, prev, ac.Text)
			}
			got[f][ac.Line] = ac.Text
		}
	}

	// Vacuity before comparison: an empty run agrees with an empty table perfectly, and a
	// lane that failed to turn its gates on produces exactly that — every relaxed vector
	// scored `gated`, no choice recorded, both directions below silent.
	if total == 0 {
		t.Fatal("no `either` choices recorded across the whole board: with every gate on, the " +
			"six relaxed-SIMD files hold 32 of them, so zero means the lane did not run them " +
			"rather than that the corpus has none")
	}
	if diff := mapDiff(widths, relaxedLoweringWidths); diff != "" {
		t.Errorf("the alternative-width census moved: %s\n\tThis is the corpus's own grammar, not "+
			"this engine's behaviour, so a change here means the vendored suite moved and every "+
			"pinned answer below should be re-read rather than re-recorded.", diff)
	}

	for f, lines := range relaxedLoweringPins {
		for line, want := range lines {
			switch have, ok := got[f][line]; {
			case !ok:
				t.Errorf("%s:%d is pinned to %s and recorded no choice at all.\n"+
					"\tThe vector is not being asked: retired upstream, failing, or declined by "+
					"a gate this lane believes is on. A pin over a vector nobody runs is the "+
					"vacuum, so it fails here rather than passing quietly.", f, line, want)
			case have != want:
				t.Errorf(`%s:%d changed which permitted answer this engine gives.

    pinned   %s
    recorded %s

Both are legal — the vector lists them as alternatives, so the board stays green either way. What
moved is the *lowering*, and decision 0028 d1 makes the lowering a Burroughs guarantee: deterministic
and architecture-uniform, which is more than the spec asks. If this change is intended, the ADR is
what has to move first; if it is not, the arm is a defect no suite vector can see.`,
					f, line, want, have)
			}
		}
	}
	for f, lines := range got {
		for line, have := range lines {
			if _, ok := relaxedLoweringPins[f][line]; !ok {
				t.Errorf("%s:%d recorded the choice %s with no pin for it: an `(either …)` site "+
					"arrived and nothing has decided what this engine answers there. Record it "+
					"in relaxedLoweringPins with the lowering it identifies.", f, line, have)
			}
		}
	}

	t.Logf("%d `either` choices pinned on %s/%s, widths %s",
		total, runtime.GOOS, runtime.GOARCH, sortedCounts(widths))
}

// mapDiff renders the difference between two count maps, or "" when they agree.
func mapDiff(got, want map[int]int) string {
	keys := map[int]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var parts []string
	for k := range keys {
		if got[k] != want[k] {
			parts = append(parts, fmt.Sprintf("width %d: %d recorded, %d pinned", k, got[k], want[k]))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// sortedCounts renders a count map in key order, for a log line that reads the same every run —
// Go's map iteration is randomized, and a figure quoted in a board comment has to be quotable.
func sortedCounts(m map[int]int) string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d×%d-wide", m[k], k)
	}
	return strings.Join(parts, ", ")
}
