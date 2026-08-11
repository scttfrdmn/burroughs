// Package vecbench measures decision 0024's stack-widening cost — the hot-path question the ADR
// needs, on dispatchbench/dropbench's own precedent (decisions 0002, 0023): same access pattern,
// competing implementations, benchstat decides.
//
// The question: a v128 value occupies two adjacent `stack.num` slots. Decision 0023 already gave
// every numeric slot a lazily-activated push sequence number so `drop` can tell which array holds
// the logical top with no validator to consult. Two independent `pushNum` calls for one v128
// value get two independent sequence numbers under 0023's mechanism exactly as written — grave
// #206's shape one layer up, since a `drop` or `branch` landing between the two halves would see
// two different ages for what is logically one value. The correctness-mandated fix is an atomic
// `pushV128`/`popV128` pair sharing one sequence number; what is measured here is what that
// atomicity costs over the naive (and rejected-on-correctness-grounds) two-independent-pushes
// alternative, and what either costs over a v128-free baseline.
//
// **What is measured is bookkeeping cost on the mixed access pattern, not any 0xfd opcode's own
// arithmetic** — no 0xfd arm exists yet (that is #212's ladder); this measures only the stack
// operations decision 0024 adds underneath them, exactly as dropbench measured 0023's tracking
// cost underneath drop rather than drop's own O(1) comparison.
package vecbench

import "testing"

// The access pattern: dropbench's own gated-stack shape (0023's Candidate C, the shipped design),
// extended with a periodic v128 push/pop — representative of a function body that occasionally
// carries a v128 local or block result alongside ordinary numeric traffic, not a stress-test
// extreme. N matches dispatchbench/dropbench's own constant for the same reason they chose it.
const N = 1000

// refEvery/v128Every are how often a reference or a v128 value joins the numeric ones, chosen to
// exercise both arrays without either dominating the loop — dropbench's own refEvery precedent
// (97, roughly one in a hundred) scaled down slightly for v128 to keep the two populations
// distinguishable in the profile rather than coinciding on the same iterations.
const (
	refEvery  = 97
	v128Every = 101
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- Baseline: 0023's shipped gated design, no v128 awareness at all ----------
//
// Copied from dropbench's own stackGated rather than imported: this package measures a
// *different* addition (v128 push/pop) layered on top of 0023's mechanism, and importing
// dropbench's unexported type would couple two benchmark packages' internals for a few dozen
// lines neither wants to expose. Same shape, same reasoning, restated rather than shared —
// dropbench's own header gives the identical argument for not sharing dispatchbench's harness.

type stackBase struct {
	num      []uint64
	numSeq   []uint64
	refs     []uint64
	refSeq   []uint64
	next     uint64
	tracking bool
}

func (s *stackBase) pushNum(v uint64) {
	s.num = append(s.num, v)
	if s.tracking {
		s.numSeq = append(s.numSeq, s.next)
		s.next++
	}
}

func (s *stackBase) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	if s.tracking {
		s.numSeq = s.numSeq[:len(s.numSeq)-1]
	}
	return v
}

func (s *stackBase) pushRef(v uint64) {
	if !s.tracking {
		s.tracking = true
		s.numSeq = make([]uint64, len(s.num))
		for i := range s.numSeq {
			s.numSeq[i] = s.next
			s.next++
		}
	}
	s.refs = append(s.refs, v)
	s.refSeq = append(s.refSeq, s.next)
	s.next++
}

func (s *stackBase) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return v
}

func (s *stackBase) branch(height, arity, refHeight, refArity int) {
	if arity > 0 {
		src := len(s.num) - arity
		copy(s.num[height:], s.num[src:])
		if s.tracking {
			copy(s.numSeq[height:], s.numSeq[src:])
		}
	}
	s.num = s.num[:height+arity]
	if s.tracking {
		s.numSeq = s.numSeq[:height+arity]
	}
	if refArity > 0 {
		src := len(s.refs) - refArity
		copy(s.refs[refHeight:], s.refs[src:])
		copy(s.refSeq[refHeight:], s.refSeq[src:])
	}
	s.refs = s.refs[:refHeight+refArity]
	s.refSeq = s.refSeq[:refHeight+refArity]
}

func runBase(iters int) uint64 {
	s := &stackBase{num: make([]uint64, 0, 64), refs: make([]uint64, 0, 8)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%refEvery == 0 {
				s.pushRef(uint64(i))
			}
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, max(len(s.refs)-1, 0), boolToInt(len(s.refs) > 0))
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
		for len(s.refs) > 0 {
			acc += s.popRef()
		}
	}
	return acc
}

// ---------- Candidate A: v128 as two independent pushNum calls — the rejected, unsafe shape ----------
//
// **Benched anyway, on the standing rule that a rejected option needs its number stated, not
// assumed.** This is exactly the mechanism 0024's own "Forced design question 1" rejects on
// correctness grounds (two independent sequence numbers for one logical value, reproducing grave
// #206's shape) — measured here only to answer "does the unsafe shape even buy anything worth
// weighing against the correctness cost," using stackBase unmodified: pushing a v128 is simply
// two ordinary pushNum calls, so this candidate's cost is definitionally identical to stackBase's
// per-slot cost and needs no separate implementation. Its own benchmark function below exists so
// the comparison is explicit in the recorded numbers rather than left as an inference.

func runNaiveV128(iters int) uint64 {
	s := &stackBase{num: make([]uint64, 0, 64), refs: make([]uint64, 0, 8)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%refEvery == 0 {
				s.pushRef(uint64(i))
			}
			if i%v128Every == 0 {
				// Two independent pushNum calls, two independent sequence numbers — the unsafe
				// shape. Popped as two independent popNum calls to match.
				s.pushNum(uint64(i))
				s.pushNum(uint64(i) ^ 0xff)
				acc += s.popNum()
				acc += s.popNum()
			}
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, max(len(s.refs)-1, 0), boolToInt(len(s.refs) > 0))
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
		for len(s.refs) > 0 {
			acc += s.popRef()
		}
	}
	return acc
}

// ---------- Candidate B: pushV128/popV128, atomic, one shared sequence number ----------
//
// The chosen shape (0024's Decision, point 1): both slots pushed as one operation, tagged with
// a single sequence number so `drop`'s comparison sees one age for the whole value, never two.

type stackAtomic struct {
	num      []uint64
	numSeq   []uint64
	refs     []uint64
	refSeq   []uint64
	next     uint64
	tracking bool
}

func (s *stackAtomic) pushNum(v uint64) {
	s.num = append(s.num, v)
	if s.tracking {
		s.numSeq = append(s.numSeq, s.next)
		s.next++
	}
}

func (s *stackAtomic) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	if s.tracking {
		s.numSeq = s.numSeq[:len(s.numSeq)-1]
	}
	return v
}

// pushV128 pushes both halves as one unit: two num slots, one shared sequence number recorded on
// each so a truncation or a drop's comparison reads the pair as a single age. Hi first, lo second
// — matching pop order (LIFO: lo pops before hi), so a plain two-call popNum popNum from the top
// yields (lo, hi) in that order without either caller needing to know this type exists.
func (s *stackAtomic) pushV128(hi, lo uint64) {
	seq := s.next
	if s.tracking {
		s.next++
	}
	s.num = append(s.num, hi, lo)
	if s.tracking {
		s.numSeq = append(s.numSeq, seq, seq)
	}
}

func (s *stackAtomic) popV128() (hi, lo uint64) {
	lo = s.popNum()
	hi = s.popNum()
	return hi, lo
}

func (s *stackAtomic) pushRef(v uint64) {
	if !s.tracking {
		s.tracking = true
		s.numSeq = make([]uint64, len(s.num))
		for i := range s.numSeq {
			s.numSeq[i] = s.next
			s.next++
		}
	}
	s.refs = append(s.refs, v)
	s.refSeq = append(s.refSeq, s.next)
	s.next++
}

func (s *stackAtomic) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return v
}

func (s *stackAtomic) branch(height, arity, refHeight, refArity int) {
	if arity > 0 {
		src := len(s.num) - arity
		copy(s.num[height:], s.num[src:])
		if s.tracking {
			copy(s.numSeq[height:], s.numSeq[src:])
		}
	}
	s.num = s.num[:height+arity]
	if s.tracking {
		s.numSeq = s.numSeq[:height+arity]
	}
	if refArity > 0 {
		src := len(s.refs) - refArity
		copy(s.refs[refHeight:], s.refs[src:])
		copy(s.refSeq[refHeight:], s.refSeq[src:])
	}
	s.refs = s.refs[:refHeight+refArity]
	s.refSeq = s.refSeq[:refHeight+refArity]
}

func runAtomicV128(iters int) uint64 {
	s := &stackAtomic{num: make([]uint64, 0, 64), refs: make([]uint64, 0, 8)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%refEvery == 0 {
				s.pushRef(uint64(i))
			}
			if i%v128Every == 0 {
				s.pushV128(uint64(i), uint64(i)^0xff)
				hi, lo := s.popV128()
				// Sum, matching runNaiveV128's two popNum() calls added together — the two
				// candidates must compute the identical accumulator, or TestAllAgree's job is
				// to say so before any timing number is trusted.
				acc += hi + lo
			}
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, max(len(s.refs)-1, 0), boolToInt(len(s.refs) > 0))
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
		for len(s.refs) > 0 {
			acc += s.popRef()
		}
	}
	return acc
}

// runBaseWithV128Frequency is stackBase run at the *same* iteration count and same non-v128
// operation shape as the two v128 candidates, but with the v128-carrying branch removed entirely
// — the "this workload never uses v128 at all" comparison, isolating what v128 support costs a
// function that never exercises it (should be ~0, since neither candidate touches the no-v128
// path differently from stackBase).
func runBaseWithV128Frequency(iters int) uint64 {
	return runBase(iters)
}

func TestAllAgree(t *testing.T) {
	const iters = 5
	// The two v128-carrying candidates must compute the identical accumulator to the digit —
	// same pushes, same pops, same arithmetic — or one of them has a bug the timing comparison
	// would otherwise silently launder into a "cost" that is actually a wrong answer.
	if got, want := runAtomicV128(iters), runNaiveV128(iters); got != want {
		t.Errorf("AtomicV128 = %d, want %d (must match NaiveV128 — same values pushed and popped, "+
			"only the sequence-number bookkeeping differs)", got, want)
	}
}

// TestPushV128PopOrderMatchesNumOrder falsifies the one thing this candidate's correctness
// actually depends on: popV128 must return the halves in the order they were logically pushed
// (hi, lo), not in stack-pop order reversed incorrectly. A swapped return would compute a
// different v128 value than the one pushed, silently, on every SIMD arm this design underlies.
func TestPushV128PopOrderMatchesNumOrder(t *testing.T) {
	s := &stackAtomic{}
	s.pushV128(0xAAAA, 0xBBBB)
	hi, lo := s.popV128()
	if hi != 0xAAAA || lo != 0xBBBB {
		t.Errorf("popV128() = (%#x, %#x), want (0xaaaa, 0xbbbb) — pushV128(hi, lo) must round-trip "+
			"through popV128 as the identical (hi, lo) pair", hi, lo)
	}
	if len(s.num) != 0 {
		t.Errorf("stack has %d slots left after one push/pop pair, want 0", len(s.num))
	}
}

// TestV128SharesOneSequenceNumberAcrossBothSlots is the falsifiable control 0024 pre-registers:
// a v128's two slots must carry the *same* sequence number, or a drop landing between them (which
// pushV128/popV128's atomicity should make impossible to observe, but a future direct-append bug
// could reintroduce) would see two different ages for one logical value — grave #206's shape.
//
// Falsified by reverting pushV128 to two independent pushNum calls (this test's own mutation
// target): the two slots then carry consecutive-but-different sequence numbers, and this
// assertion catches it before any 0xfd arm exists to hide the defect behind real arithmetic.
func TestV128SharesOneSequenceNumberAcrossBothSlots(t *testing.T) {
	s := &stackAtomic{tracking: true}
	s.pushV128(1, 2)
	if len(s.numSeq) != 2 {
		t.Fatalf("numSeq has %d entries after one v128 push, want 2", len(s.numSeq))
	}
	if s.numSeq[0] != s.numSeq[1] {
		t.Errorf("v128's two slots carry sequence numbers %d and %d, want identical — a drop or "+
			"branch that could observe them as different ages would misjudge which slot is the "+
			"logical top, reproducing grave #206's shape one layer up", s.numSeq[0], s.numSeq[1])
	}
}

func BenchmarkBaseWithV128Frequency(b *testing.B) {
	for range b.N {
		runBaseWithV128Frequency(iters)
	}
}

func BenchmarkNaiveV128(b *testing.B) {
	for range b.N {
		runNaiveV128(iters)
	}
}

func BenchmarkAtomicV128(b *testing.B) {
	for range b.N {
		runAtomicV128(iters)
	}
}

// ---------- Every-iteration v128 traffic — isolating the push/pop cost from the sparse-frequency
// dilution the three benchmarks above carry ----------
//
// v128Every=101 (roughly 1% of iterations) is representative of ordinary mixed code, but it
// dilutes any real per-operation cost difference into noise at this workload's other traffic
// volume — a v128 push/pop each costs some number of nanoseconds, and if that number is small
// relative to the other 99% of the loop's work, no benchtime short of extreme values resolves it.
// These variants push and pop a v128 (or the naive two-independent-pushNum equivalent) on *every*
// iteration instead, isolating the two candidates' own cost from the surrounding traffic — the
// worst-case-frequency reading, useful precisely because it is not the realistic one.

func runNaiveV128AllIters(iters int) uint64 {
	s := &stackBase{num: make([]uint64, 0, 64)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			s.pushNum(uint64(i) ^ 0xff)
			acc += s.popNum()
			acc += s.popNum()
		}
	}
	return acc
}

func runAtomicV128AllIters(iters int) uint64 {
	s := &stackAtomic{num: make([]uint64, 0, 64)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushV128(uint64(i), uint64(i)^0xff)
			hi, lo := s.popV128()
			acc += hi + lo
		}
	}
	return acc
}

func TestAllAgreeAllIters(t *testing.T) {
	const testIters = 5
	if got, want := runAtomicV128AllIters(testIters), runNaiveV128AllIters(testIters); got != want {
		t.Errorf("AtomicV128AllIters = %d, want %d", got, want)
	}
}

func BenchmarkNaiveV128AllIters(b *testing.B) {
	for range b.N {
		runNaiveV128AllIters(iters)
	}
}

func BenchmarkAtomicV128AllIters(b *testing.B) {
	for range b.N {
		runAtomicV128AllIters(iters)
	}
}

const iters = 100
