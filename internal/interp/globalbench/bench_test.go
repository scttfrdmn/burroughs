// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package globalbench measures the cost of publishing a global's value on the three axes the two
// decisions split: [ADR 0063]'s numeric single atomic word and mutex-published v128 pair, and
// [ADR 0066]'s atomic-pointer reference slot.
//
// # The six rows are two pre-registrations' rows
//
// #573's first pre-registration names four forecasts and its second names four more about the reference
// arm; this package exists to answer them, one row each:
//
//	GetI64    F1 — within noise. An atomic load is a MOVQ on amd64, an LDAR on arm64.
//	SetI64    F2 — measurably slower. Direction only; a Store is a locked XCHG / an STLR.
//	GetV128   F3 — measurably slower. A lock pair appears on a read path that had neither.
//	SetV128   F4 — slower by roughly the same absolute amount as GetV128. **The discriminating row.**
//	GetRef    R1 — within the null arm's excursion. An atomic pointer load is a MOVQ and the 40-byte
//	               copy out happens in both arms, so the mechanism adds nothing to a read.
//	SetRef    R2/R3 — measurably slower, direction only, and **decomposed**: the store's own share is
//	               pre-registered at 0063's locked-XCHG figure, so the residual attributes to the
//	               allocation. R3 says the allocation is the majority of it, which is the forecast most
//	               likely to embarrass the mechanism and is registered in that direction deliberately.
//
// **Two of the four reference forecasts came back falsified, and the table above is the pre-registration
// rather than the result** — read [ADR 0066] for the board. Said here because the entries are written in
// the present tense and a reader arriving at `R1 — within the null arm's excursion` would otherwise take
// it for a finding: `GetRef` came in at **+17.19% (p=0.000)** against a null excursion of 0.05%, so a read
// under an atomic pointer is *cheaper than a mutex* rather than free, and R4's leading sentence
// (*"arm64 shows no `SetRef` effect"*) was falsified by the largest effect on either board. R2 and R3 hold,
// R3 by a grafted `-benchmem` arm rather than by subtracting the pre-registered store figure. The entries
// are left as written — a pre-registration edited after its measurement is not one — and this paragraph is
// the pointer that keeps them from reading as conclusions.
//
// F4 is the one that decides whether the ADR may say where the cost is. The identical lock/unlock pair
// is what was added to both v128 arms, so a v128 cost appearing on one side only is *not* the lock, and
// the mechanism would not be where decision 0063 puts it. F5 — that the i64 and v128 deltas differ in
// kind rather than degree — is read off the pair of pairs rather than measured by a fifth row. R4 is
// arm64's own bound and is read off the second architecture's board rather than from a seventh row.
//
// **The reference arm's base is the defect, not an alternative mechanism.** `main`'s plain 40-byte field
// is wrong, so a delta against it is a *cost of correctness*. The comparison decision 0066 actually made
// — atomic pointer against a mutex — is not a measured arm here, because
// [#618](https://github.com/scttfrdmn/burroughs/issues/618) records `ab.sh` cannot A/B two mechanisms
// inside one revision; the mutex comparator is 0063's decomposed `Lock`/`Unlock` figure, a **predicted**
// comparator stated as predicted.
//
// # On loopbench's shape, and why that is not a stylistic choice
//
// wat through `text.EncodeModule` → `binary.DecodeModule` → `Instantiate` → `Invoke`, so the timed path
// is the engine's own dispatch loop reaching the real `*global`. #515's pre-registration named
// `dropbench` as an effect arm on the strength of its header, and had to be narrowed on the issue when
// it turned out that package is `import "testing"` and nothing else and **never executes a wasm
// instruction** — so it could not have linked the code under test. *A pre-registration forecasts the
// instruments*, and the instrument is the premise most easily left unchecked.
//
// # The density is the number that makes a null readable, so it is stated and asserted
//
// A global access is a small fraction of one interpreter dispatch. A **sparse** loop therefore hides a
// real cost under dispatch overhead and prints a null that means nothing — it would be the instrument
// reporting its own blindness, not evidence of a free mechanism. So the arms are dense, by this much:
//
//	37 instructions per trip, of which 16 are the global access itself — 43%.
//
// Every arm has the identical shape and the identical trip count, differing only in *which* global
// instruction fills the 16 slots. `TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp` asserts both the
// density and the difference, because a generator is only as trustworthy as the last edit to it, and
// *a comparison needs a vacuity check*: arms that had drifted to the same body would still print a row
// of tidy percentages. Its population is `arms`, so the reference pair joined it by being declared and
// not by an edit to the control — which is the property #573's second pre-registration asked for when
// it said the control is extended to six arms so the new pair cannot collapse onto an existing one.
//
// A null on any row is read against that density **and** against `--null`'s own spread — *compare the
// floor to the bar*. Where the null arm's interval is no narrower than the effect being claimed, this
// board does not adjudicate that row, and saying so is the reading rather than a hedge.
//
// # What this package does not do
//
// It no longer omits the reference arm — the two rows above are it, and the sentence that stood here
// (*"`ref` is 40 bytes and out of #573's scope by the pre-registration"*) was true only while there was
// no mechanism to price. Decision 0066 built one, so the omission would now be a claim about scope that
// the tree contradicts. It does not compare a mutex against a seqlock — that would need two
// mechanisms in one revision, which is exactly the comparison
// [#618](https://github.com/scttfrdmn/burroughs/issues/618) records `ab.sh` cannot make. F3 *did* come
// back dominated by the acquire — `GetV128` +41.73% on native x86-64, all of it the `Lock`/`Unlock` pair
// — so the seqlock is filed as the named successor
// ([#625](https://github.com/scttfrdmn/burroughs/issues/625)) and the mutex stays. Recorded here in the
// past tense because the sentence this replaced was a conditional the board had already settled, and a
// conditional left standing after its condition is decided tells the next reader the question is open.
//
// [ADR 0063]: ../../../docs/decisions/0063-a-numeric-globals-single-word-goes-atomic-and-a-v128s-pair-goes-under-the-globals-own-mutex.md
// [ADR 0066]: ../../../docs/decisions/0066-a-reference-globals-forty-byte-value-is-published-through-an-atomic-pointer-because-reads-are-the-hot-direction-and-a-mutex-taxes-every-get.md
package globalbench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// trips is how many loop back-edges one Invoke executes.
//
// Sized for loopbench's reason rather than the usual one: `Invoke`'s fixed cost — a boundary crossing
// pair, a stack, an argument slice — is a *larger share of a shorter row*, so it does not dilute the
// four arms equally and would bias the very comparison F4 rests on. 100_000 trips puts each row in the
// millisecond range against a boundary crossing measured in microseconds.
const trips = 100_000

// opsPerTrip is how many global accesses fill one trip's body.
//
// Sixteen, unrolled rather than nested in an inner loop: an inner loop would add its own countdown to
// the instruction mix and dilute exactly the density this constant exists to raise.
const opsPerTrip = 16

// countdownInstrs is the instruction cost of one trip's countdown, derived from the body below rather
// than counted by hand: `local.get $n`, `i32.const 1`, `i32.sub`, `local.tee $n`, `br_if`.
const countdownInstrs = 5

// instrsPerOp is the instruction cost of one unrolled global access: the access itself plus the one
// instruction that keeps the stack balanced — a `drop` on the get arms, a `const` on the set arms.
//
// Two, and equal across all four arms on purpose. It is what makes the arms differ *only* in which
// global instruction runs, so a delta between them cannot be a difference in how much other work each
// one does.
const instrsPerOp = 2

// instrsPerTrip and globalShare are the density this package advertises, as arithmetic so the header's
// two numbers cannot drift from the generator.
const (
	instrsPerTrip = countdownInstrs + instrsPerOp*opsPerTrip
	globalShare   = 100 * opsPerTrip / instrsPerTrip
)

// arm names one benchmark row: the global it touches and the body that touches it.
type arm struct {
	name string // the exported function name, and the row's identity in the wat
	// op is one unrolled global access, rendered opsPerTrip times into the body. Exactly
	// instrsPerOp instructions, asserted by the control test rather than trusted here.
	op string
}

// arms is the four rows, in the pre-registration's order. Declared as data rather than as four
// generator functions so that "the arms differ only in the global op" is visible in one place and
// checkable in another.
var arms = []arm{
	{name: "getI64", op: "(drop (global.get $gi64))"},
	{name: "setI64", op: "(global.set $gi64 (i64.const 1))"},
	{name: "getV128", op: "(drop (global.get $gv128))"},
	{name: "setV128", op: "(global.set $gv128 (v128.const i64x2 1 1))"},
	{name: "getRef", op: "(drop (global.get $gref))"},
	{name: "setRef", op: "(global.set $gref (ref.func $f))"},
}

// buildModule renders all four bodies into one module, so every arm runs against the same instance and
// no arm can differ by an instantiation.
//
// The countdown is written identically in every arm — `br_if` on a `local.tee`'d decrement — so "all
// four arms execute the same number of back-edges" is a property of this generator rather than a claim
// to be checked afterwards. The control test asserts it anyway.
func buildModule() string {
	var b strings.Builder
	b.WriteString("(module\n")
	// `$f` and its declare segment are the reference arm's payload: `ref.func` needs a *declared*
	// function, and an empty body is the right one — the arm prices publishing a reference, not calling
	// through it, and `setRef` never calls `$f`.
	b.WriteString("\t(func $f)\n")
	b.WriteString("\t(elem declare func $f)\n")
	b.WriteString("\t(global $gi64 (mut i64) (i64.const 0))\n")
	b.WriteString("\t(global $gv128 (mut v128) (v128.const i64x2 0 0))\n")
	b.WriteString("\t(global $gref (mut funcref) (ref.null func))\n")
	for _, a := range arms {
		fmt.Fprintf(&b, "\t(func (export %q) (param $n i32) (result i32)\n", a.name)
		b.WriteString("\t\t(loop $l\n")
		for range opsPerTrip {
			fmt.Fprintf(&b, "\t\t\t%s\n", a.op)
		}
		b.WriteString("\t\t\t(br_if $l (local.tee $n (i32.sub (local.get $n) (i32.const 1)))))\n")
		b.WriteString("\t\t(local.get $n))\n")
	}
	b.WriteString(")")
	return b.String()
}

// build takes wat through the whole front end. Duplicated from its siblings rather than shared for the
// reason the bench packages are separate at all: a helper shared across them would make one package's
// measurement depend on another package's edits.
func build(src string) (*interp.Instance, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, fmt.Errorf("instantiate: %w", trap)
	}
	return in, nil
}

// run is the timed body for every arm: one Invoke of `trips` back-edges.
//
// The first call is made outside the timed loop so a failure is reported as a failure rather than folded
// into the first iteration's time — a `b.Fatalf` inside the loop would report a broken arm as a fast one.
func run(b *testing.B, name string) {
	b.Helper()
	in, err := build(buildModule())
	if err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	arg := interp.Value{Type: binary.I32, Bits: trips}
	if _, err := in.Invoke(name, arg); err != nil {
		b.Fatalf("invoke %s: %v", name, err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke(name, arg); err != nil {
			b.Fatalf("invoke %s: %v", name, err)
		}
	}
}

func BenchmarkGetI64(b *testing.B)  { run(b, "getI64") }
func BenchmarkSetI64(b *testing.B)  { run(b, "setI64") }
func BenchmarkGetV128(b *testing.B) { run(b, "getV128") }
func BenchmarkSetV128(b *testing.B) { run(b, "setV128") }
func BenchmarkGetRef(b *testing.B)  { run(b, "getRef") }
func BenchmarkSetRef(b *testing.B)  { run(b, "setRef") }

// TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp is the instrument's own control, and it is a test rather
// than a benchmark because a benchmark cannot fail an assertion — `make bench` is not `make check`, so an
// arm that had stopped measuring its subject would go unreported until someone read the numbers and
// believed them.
//
// Three claims, each one a premise the four-row comparison rests on and none of them left to the
// generator:
//
//  1. **The density is what the header advertises.** A null row is readable only against it, so a body
//     that had drifted sparse would print the same tidy percentage and mean nothing.
//  2. **Every arm executes the same number of back-edges and the same op count.** Otherwise a delta
//     between rows is partly a fact about their shapes, and F4 — same absolute cost on both v128 sides
//     — becomes unreadable. R2's decomposition rests on the same premise from the other side: a
//     residual after subtracting a predicted store cost is only an allocation's if nothing else differs.
//  3. **The arms actually differ.** Bodies that had collapsed onto one global instruction would still
//     produce a row each and a p-value. *Assert the arms differ, not only that the null matches.*
func TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp(t *testing.T) {
	src := buildModule()

	// (1) The density, as arithmetic against the two figures the header quotes.
	if instrsPerTrip != 37 || globalShare != 43 {
		t.Errorf("the arms are %d instructions per trip with %d%% global accesses; the package header "+
			"and #573's pre-registration are written against 37 and 43%%. A null row is readable only "+
			"against a stated density, so correct the prose and the constants together — otherwise the "+
			"number a reader checks against is not the number being measured",
			instrsPerTrip, globalShare)
	}

	// (2) One countdown per arm, and every op rendered the advertised number of times.
	if got := strings.Count(src, "(br_if $l"); got != len(arms) {
		t.Errorf("the module contains %d `br_if $l`, want %d — one back-edge per arm. Any other number "+
			"means the arms are not the same loop, so the deltas between them are facts about their "+
			"shapes rather than about the global op", got, len(arms))
	}
	if got := strings.Count(src, "(i32.sub (local.get $n) (i32.const 1))"); got != len(arms) {
		t.Errorf("the module contains %d identical countdown decrements, want %d. The arms must execute "+
			"the same number of back-edges or the rows are not comparable", got, len(arms))
	}
	for _, a := range arms {
		if got := strings.Count(src, a.op); got != opsPerTrip {
			t.Errorf("arm %s renders its op %d times, want %d — its density is not the advertised %d%% "+
				"and its row cannot be compared against the others", a.name, got, opsPerTrip, globalShare)
		}
		// instrsPerOp is load-bearing in the density arithmetic above, so it is checked against each
		// op's actual shape rather than asserted once. Counted by open parens, which is the
		// instruction count for these fully-parenthesised folded forms.
		if got := strings.Count(a.op, "("); got != instrsPerOp {
			t.Errorf("arm %s's op %q is %d instructions, want %d — the arms no longer differ *only* in "+
				"which global instruction runs, so a delta between them can be a difference in how much "+
				"other work each does", a.name, a.op, got, instrsPerOp)
		}
	}

	// (3) The arms differ. Checked on the ops themselves rather than on the rendered source, because
	// two arms sharing an op would still render two distinct function bodies around it.
	seen := make(map[string]string, len(arms))
	for _, a := range arms {
		if prev, dup := seen[a.op]; dup {
			t.Errorf("arms %s and %s both run %q, so they are one measurement wearing two names — and "+
				"two identical arms agree perfectly, which is what an unchecked effect arm looks like "+
				"from the outside", prev, a.name, a.op)
		}
		seen[a.op] = a.name
	}

	// And every arm must run, which is the assertion that catches a body the validator rejects or a
	// gate that is off in this build. The countdown returns 0, so the answer pins that the loop
	// reached zero rather than exiting some other way.
	in, err := build(src)
	if err != nil {
		t.Fatalf("building the bench module: %v", err)
	}
	arg := interp.Value{Type: binary.I32, Bits: trips}
	for _, a := range arms {
		out, err := in.Invoke(a.name, arg)
		if err != nil {
			t.Fatalf("invoke %s: %v", a.name, err)
		}
		if len(out) != 1 || out[0].Bits != 0 {
			t.Errorf("arm %s returned %v, want a single 0 — the countdown did not reach zero, so the "+
				"loop ran a number of times this test cannot state", a.name, out)
		}
	}
}
