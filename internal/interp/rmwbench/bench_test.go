// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package rmwbench measures the 0xFE region's read-modify-write family on the axis #559 turns on:
// whether an operator has a native `sync/atomic` primitive at its access width.
//
// **It exists because nothing in the tree could ask the question, and that absence is measured rather
// than asserted.** No benchmark here executes an atomic instruction: `membench` is plain loads and
// stores, `vecbench`'s `BenchmarkAtomicV128` is ADR 0054's aligned-access path rather than this region,
// and `dispatchbench`, `dropbench` and `scanbench` build modules with no atomics in them. So the claim
// #559 was filed against — *"in an interpreter, the difference between one `LOCK XADD` and a
// load-plus-CAS is lost in dispatch overhead"* — had no instrument that could confirm or refute it, which
// is what made it a *cheap is a grammar claim* rather than a measurement.
//
// # The population is derived from the opcode table, not from a list
//
// **49 rows: 42 read-modify-write and 7 compare-and-exchange.** Enumerated from
// `binary.PrefixedRegionOpcodes(0xfe)` at build time, because a hand-written arm list is a hand-written
// chance to omit the row that would have moved. #559's own body puts the family at *"six of the six at
// widths 4 and 8 … leaving only xor and the narrow widths"*, arithmetic that reads as 6 operators × 4
// widths = 24; the table says **7 rows per operator**, because the i64 slot carries `rmw8`, `rmw16` and
// `rmw32` forms as well as the natural width. Counting the table rather than the prose is the difference,
// and it is the whole reason this list is generated.
//
// The access width is what decides native eligibility, and the slot width is not — `i64.atomic.rmw32.add_u`
// is a 4-byte access into a 64-bit slot and takes the same whole-word path `i32.atomic.rmw.add` does. So
// the four regimes are by access width: 1 and 2 are narrow (a field inside a containing word), 4 and 8 are
// whole-word.
//
// # What each arm is for
//
//   - **Whole-word `add`/`sub`/`xchg`** (widths 4 and 8) are what a native `AddUint32`/`SwapUint32`
//     dispatch was written to speed up, and a regression in them falsifies it.
//   - **Narrow `and`/`or`** (widths 1 and 2) are native too, which #559's body does not claim: a field's
//     `and` is a full-word `AndUint32` with 1s outside the field, and its `or` is an `OrUint32` with 0s
//     there. So these rows are a second regime rather than part of the loop's remainder.
//   - **`xor` at every width, and narrow `add`/`sub`/`xchg`**, keep the compare-and-swap loop under every
//     outcome. They are the *complement*, and reading them is how the price of a dispatch on the rows it
//     does not serve gets measured at all. *An unmeasured complement is not an empty one.*
//   - **`cmpxchg` at all seven rows** keeps its loop for a reason that cannot change — Go's
//     `CompareAndSwapUint32` returns a bool where the spec needs the value that was observed.
//
// **Neither population is a control, and this doc claimed both were (grave #581).** The claim was: *"if
// they move, something other than the dispatch changed and the eligible rows are measuring that
// instead."* They are rows whose *semantics* the diff leaves alone, which is not the same thing: they
// share a binary and a schedule with the change. #559's three-arm run moved all seven `cmpxchg` rows by
// 2.5–5.8% (arm64) and 6.9–8.8% (amd64) across a diff that cannot reach `atomicCmpxchg`, so their
// flatness in the two-arm run before it had been luck — and it had already been spent, as the licence for
// a published attribution that had to be withdrawn. See #580 for what does move them.
//
// # The protocol a comparison here has to use (grave #581)
//
// This is prose rather than a control because it governs a run a human does across two machines, and a
// control that could assert it would have to run the benchmark it is checking.
//
//   - **A null arm.** A byte-identical copy of one arm, its sha256 asserted equal to the original and
//     distinct from the other arm, run as a third arm. Its true effect is zero *by construction*, so
//     whatever it shows is the instrument and nothing about the code under test can make it flat by
//     accident. A control chosen for semantic invariance cannot do this job.
//   - **Rotated slots.** With *k* arms and a round count that is a multiple of *k*, arm *i* takes slot
//     *(i+r) mod k* in round *r*, so each arm holds each slot equally often — which balances position and
//     elapsed time at once. Splitting output per (arm, slot) then makes the slot effect readable from the
//     same run for free. Fixed slots are grave #552's shape: a per-arm constant nobody varied.
//   - **A distributional floor.** The null arm's spread is the instrument's resolution, and it must be
//     summarised by a geomean or a quantile — **never by the largest per-row delta**. Over 49 rows at
//     benchstat's α=0.05, ~2.45 rows carry a verdict on a perfectly flat instrument, so the maximum *is*
//     the multiplicity artifact: taking it as the floor reproduces the very defect it looks like a repair
//     for. For the same reason a criterion of the form "every row is `~`" is failed by the truth ~92% of
//     the time here (1 − 0.95⁴⁹) and gets worse with more samples.
//   - **A criterion that states its row count**, since every threshold over this population is a
//     statement about 49 comparisons rather than about one.
//
// Bodies are straight-line rather than looped, so the figure is the access and not a guest loop's
// bookkeeping, and the whole module is built through the real front end — `text.EncodeModule` →
// `binary.Decoder` → `Instantiate` → `Invoke` — for `membench`'s stated reason: a hand-built
// `binary.Module` would let the measurement run against a module the decoder never produced (grave #125).
//
// # The gate
//
// The 0xFE region is behind `Features{Threads: true}`, so this package decodes with an explicit gate set
// exactly as `atomic_test.go`'s `atomicGated` does. The default policy is off and stays off; a benchmark
// that needed the default flipped to run would be a gate flip wearing an instrument's clothes.
package rmwbench

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// accesses is how many read-modify-write instructions one Invoke performs.
//
// Matched to `membench`'s and `dispatchbench`'s N so a reader comparing across bench packages is
// comparing the same trip count, and large enough that `Invoke`'s own fixed cost — a boundary crossing
// pair, a stack, an argument slice — is a small share of the row rather than the row itself.
const accesses = 1000

// row is one arm: an instruction of the region's rmw family, its guest spelling, and its shape.
type row struct {
	// wat is the text mnemonic, which is also the export name and the sub-benchmark name.
	wat string

	// width is the bytes the access touches: 1, 2, 4 or 8. This is what decides whether the access
	// is a whole host word or a field inside one.
	width uint64

	// slot64 is whether the value slot is i64, which decides the operand and accumulator types.
	slot64 bool

	// cmpxchg rows take three operands rather than two.
	cmpxchg bool
}

// rows enumerates the rmw family from the generated opcode table.
//
// Sorted by mnemonic so the arm order is stable across runs and across the two revisions a comparison
// puts side by side — an unstable order would make `benchstat` pair rows by position and report a
// difference between two different instructions.
func rows() []row {
	var out []row
	for _, op := range binary.PrefixedRegionOpcodes(0xfe) {
		mnemonic, _, ok := binary.PrefixedOp(0xfe, op)
		if !ok || !strings.Contains(mnemonic, "_atomic_rmw") {
			continue
		}
		operator, _ := binary.PrefixedOperator(0xfe, op)
		out = append(out, parseRow(mnemonic, operator))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].wat < out[j].wat })
	return out
}

// parseRow derives an arm from a generated mnemonic and its operator.
//
// **The operator is not in the mnemonic**, which is the property `atomic.go`'s header states and this
// function depends on: `fe 1e` and `fe 25` are both `i32_atomic_rmw`, differing only in `RmwAdd` versus
// `RmwSub`. A derivation keyed on the mnemonic alone would give two arms the same guest spelling and
// silently measure one instruction twice.
//
// The spelling rule, in the order the suffixes have to come off, because `i64_atomic_rmw32_u_cmpxchg`
// carries both: strip `_cmpxchg`, strip `_u`, turn the remaining underscores into dots, append the
// operator word (or `cmpxchg`), then put `_u` back — which is where it goes in the text format even
// though the binary mnemonic spells it earlier.
func parseRow(mnemonic, operator string) row {
	base, isCmpxchg := strings.CutSuffix(mnemonic, "_cmpxchg")
	base, unsigned := strings.CutSuffix(base, "_u")

	slot, rest, _ := strings.Cut(base, "_atomic_")
	r := row{slot64: slot == "i64", cmpxchg: isCmpxchg}

	// The access width is the digits after `rmw`, and the slot's own width when there are none.
	switch strings.TrimPrefix(rest, "rmw") {
	case "8":
		r.width = 1
	case "16":
		r.width = 2
	case "32":
		r.width = 4
	default:
		if r.slot64 {
			r.width = 8
		} else {
			r.width = 4
		}
	}

	r.wat = strings.ReplaceAll(base, "_", ".")
	if isCmpxchg {
		r.wat += ".cmpxchg"
	} else {
		r.wat += "." + strings.ToLower(strings.TrimPrefix(operator, "Rmw"))
	}
	if unsigned {
		r.wat += "_u"
	}
	return r
}

// ty is the guest type of the arm's value slot.
func (r row) ty() string {
	if r.slot64 {
		return "i64"
	}
	return "i32"
}

// body renders one exported function: `accesses` instructions, each accumulating the old value the
// instruction returns.
//
// The result is accumulated and returned rather than dropped so that no arm can be optimised away at
// either level — the interpreter has no dead-instruction elimination today, and a body whose value went
// nowhere would stop measuring the moment it acquired one.
//
// Addresses are `i * width`, so every access is naturally aligned and none traps: an unaligned atomic
// traps before it reaches the mechanism under measurement, which would turn the arm into a benchmark of
// `checkAlign`. The whole window is `accesses * width` bytes — at most 8000, inside the single page the
// module declares.
func (r row) body() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\t(func (export %q) (result %s) (local %s)\n", r.wat, r.ty(), r.ty())
	for i := range uint64(accesses) {
		operands := fmt.Sprintf("(%s.const 1)", r.ty())
		if r.cmpxchg {
			// expected 0 and replacement 1: the first pass over a fresh cell swaps, and every
			// later pass finds 1 and does not. Both outcomes exercise the loop's read, and the
			// mixture is deliberate — an arm that only ever mismatched would never reach the CAS.
			operands = fmt.Sprintf("(%s.const 0) (%s.const 1)", r.ty(), r.ty())
		}
		fmt.Fprintf(&b, "\t\t(local.set 0 (%s.add (local.get 0) (%s (i32.const %d) %s)))\n",
			r.ty(), r.wat, i*r.width, operands)
	}
	b.WriteString("\t\t(local.get 0))\n")
	return b.String()
}

// buildModule renders every arm into one module, so no arm can differ by an instantiation.
func buildModule() string {
	var b strings.Builder
	b.WriteString("(module (memory 1)\n")
	for _, r := range rows() {
		b.WriteString(r.body())
	}
	b.WriteString(")")
	return b.String()
}

// build takes wat through the whole front end under the threads gate.
func build(src string) (*interp.Instance, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, fmt.Errorf("instantiate: %w", trap)
	}
	if err := in.Deferred(); err != nil {
		return nil, fmt.Errorf("instantiate fell short: %w", err)
	}
	return in, nil
}

// BenchmarkRmw runs every arm as a sub-benchmark named by its guest mnemonic.
//
// One instance for all 49 arms, built outside the timed region: `benchstat` compares a row against the
// same row on the other revision, so what matters is that an arm is identical across builds rather than
// that the arms are isolated from each other.
func BenchmarkRmw(b *testing.B) {
	in, err := build(buildModule())
	if err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	for _, r := range rows() {
		b.Run(r.wat, func(b *testing.B) {
			// One call outside the loop, so a failure is reported as a failure rather than
			// folded into the first iteration's time.
			if _, err := in.Invoke(r.wat); err != nil {
				b.Fatalf("invoke %s: %v", r.wat, err)
			}
			b.ResetTimer()
			for range b.N {
				if _, err := in.Invoke(r.wat); err != nil {
					b.Fatalf("invoke %s: %v", r.wat, err)
				}
			}
		})
	}
}

// renderedAddresses reads the addresses one arm actually emits, in order.
//
// It reads the rendered text rather than recomputing `i * width`, because a check that recomputes the
// body's own arithmetic asserts that arithmetic against itself. What it can catch: a stride that is not
// the access width, a body that stops early, and a count that drifted from `accesses`.
func renderedAddresses(src, wat string) []uint64 {
	prefix := "(" + wat + " (i32.const "
	var out []uint64
	for rest := src; ; {
		_, after, found := strings.Cut(rest, prefix)
		if !found {
			return out
		}
		digits, remainder, _ := strings.Cut(after, ")")
		var addr uint64
		if _, err := fmt.Sscanf(digits, "%d", &addr); err == nil {
			out = append(out, addr)
		}
		rest = remainder
	}
}

// TestTheArmsAreMatched is the instrument's own control, and it is a test rather than a benchmark
// because a benchmark cannot fail an assertion — and because *a benchmark's `b.Fatalf` is invisible
// until someone runs `make bench`*.
//
// **The comparison this package makes is only as good as the claim that the arms are matched.** Four
// things are asserted, each catching a different way a tidy percentage could be a fact about the
// instrument instead of about the engine:
//
//   - The population is the table's, at the size the table gives. A count asserted against a literal
//     would go stale silently when the region is regenerated; a count asserted against a *floor* would
//     sit safely below its population and read as a census. So this compares against the table's own
//     rmw-family count, re-derived here by a different filter than `rows()` uses.
//   - Every arm has `accesses` instructions. Two arms that had drifted into different lengths would
//     still produce a ratio, and it would be a fact about their sizes.
//   - Every arm's addresses are naturally aligned, checked at both ends of the window. An unaligned
//     address traps, and a trapping arm measures the trap.
//   - Every arm actually runs. This is what catches a wat spelling the derivation got wrong: the
//     encoder is the authority for a text mnemonic, so a name that does not encode fails here rather
//     than in a benchmark nobody watched.
func TestTheArmsAreMatched(t *testing.T) {
	got := rows()

	// The population, re-derived by a filter that shares no code with `rows()`: count the region's
	// opcodes whose mnemonic mentions the family, without parsing any of them.
	want := 0
	for _, op := range binary.PrefixedRegionOpcodes(0xfe) {
		if mnemonic, _, ok := binary.PrefixedOp(0xfe, op); ok && strings.Contains(mnemonic, "_atomic_rmw") {
			want++
		}
	}
	if len(got) != want {
		t.Errorf("the module has %d arms and the 0xfe table has %d rmw-family rows: the benchmark's "+
			"population is not the region's, so a row could change with no arm to notice", len(got), want)
	}
	if want == 0 {
		t.Fatal("the 0xfe table reports no rmw-family rows at all, so every assertion below would " +
			"pass by asking nothing")
	}

	// Distinct mnemonics, which is what would fail if the operator were dropped from the derivation:
	// six operators share `i32_atomic_rmw`, so a mnemonic-only spelling would collide six ways.
	seen := make(map[string]bool, len(got))
	for _, r := range got {
		if seen[r.wat] {
			t.Errorf("two arms are both spelled %q, so one instruction is measured twice and "+
				"another not at all", r.wat)
		}
		seen[r.wat] = true
	}

	src := buildModule()
	for _, r := range got {
		// **The addresses are read back out of the rendered source, not re-derived from `width`.**
		// The first version of this loop checked `(accesses-1)*r.width % r.width`, which is zero for
		// every width there has ever been: an analytic zero is not a measurement, and it would have
		// passed against a body that addressed every arm at stride 1.
		addrs := renderedAddresses(src, r.wat)
		if len(addrs) != accesses {
			t.Errorf("arm %s has %d instructions, want %d — two arms of different lengths still "+
				"produce a ratio, and it is a fact about their sizes", r.wat, len(addrs), accesses)
		}
		for _, addr := range addrs {
			if addr%r.width != 0 {
				t.Errorf("arm %s accesses %d, which is not %d-aligned: that access traps before it "+
					"reaches the mechanism, so the arm measures checkAlign", r.wat, addr, r.width)
				break
			}
		}
		// And the window is the whole one, so a body that stopped early is not read as aligned
		// because everything it did emit happened to be.
		if len(addrs) > 0 && addrs[len(addrs)-1] != (accesses-1)*r.width {
			t.Errorf("arm %s's last access is %d, want %d", r.wat, addrs[len(addrs)-1], (accesses-1)*r.width)
		}
	}

	in, err := build(src)
	if err != nil {
		t.Fatalf("building the bench module: %v", err)
	}
	for _, r := range got {
		if _, err := in.Invoke(r.wat); err != nil {
			t.Errorf("invoke %s: %v", r.wat, err)
		}
	}
}
