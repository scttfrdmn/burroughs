// Package dropbench measures grave #206's fix candidates — the hot-path cost decision 0023
// needs, on dispatchbench's own precedent (decision 0002): same access pattern, three
// implementations, benchstat decides.
//
// The question: `drop` (interp/exec.go, opcode 0x1a) pops the numeric stack unconditionally,
// with no signal for whether the logical top-of-stack is actually a reference. The obvious fix —
// a flat push-order log — was falsified against `branch`'s own independent-per-array truncation
// pattern before being built (see 0023): a block's branch keeps an arity-sized window from *each*
// array's own top and discards the rest, by position within that array, not by global recency —
// so a log recording "the last N pushes overall" cannot answer what a truncated log's surviving
// entries actually are. The sound alternative tags each stack slot with a push-order number,
// carried along by the same copy+reslice `branch`/`returnFrom`/`catchThrown` already do.
//
// **What is measured is the bookkeeping cost, not `drop`'s own decision cost.** `drop` runs once
// per drop instruction — rare relative to push/pop — while every push, pop, and branch pays the
// sequence-number bookkeeping on *every* call, whether or not a `drop` ever follows. So the
// benchmark's hot loop is push/pop/branch at 0002's own measured access pattern, and `drop`'s O(1)
// array-selection logic is checked once for correctness (TestDropPicksTheCorrectArray) rather
// than timed — timing a rare instruction would measure noise, not the fix's actual cost.
package dropbench

import "testing"

// The access pattern: dispatchbench's own sum-1..N-in-a-loop shape (decision 0002's measurement),
// extended with the operation this fix actually touches — a periodic "branch" that keeps an
// arity-sized window from each array's top and discards the rest by position, exactly `branch`'s
// own copy+reslice (control.go). N matches dispatchbench's own constant for the same reason it
// chose it: representative of one real loop body's trip count, not a stress-test extreme.
const N = 1000

// refEvery is how often a reference-typed value joins the numeric ones — matching exec.go's own
// measured population (0 of the 139-opcode numeric core's answerable corpus needs a reference,
// so pure-numeric is the overwhelmingly common case) while still exercising the ref array at all,
// since a benchmark that never touches refs cannot measure this fix's actual subject.
const refEvery = 97

// ---------- Baseline: today's shape, no order tracking, the bug as shipped ----------

type stackBase struct {
	num  []uint64
	refs []uint64 // a bare uint64 standing in for `ref` here — its internal shape is irrelevant
	// to this measurement, which is about array *bookkeeping* cost, not payload width.
}

func (s *stackBase) pushNum(v uint64) { s.num = append(s.num, v) }
func (s *stackBase) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	return v
}
func (s *stackBase) pushRef(v uint64) { s.refs = append(s.refs, v) }
func (s *stackBase) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	return v
}

// branchBase mirrors control.go's branch: keep the top `arity`/`refArity` values, truncate each
// array independently to height/refHeight, copy the kept window down.
func (s *stackBase) branch(height, arity, refHeight, refArity int) {
	if arity > 0 {
		src := len(s.num) - arity
		copy(s.num[height:], s.num[src:])
	}
	s.num = s.num[:height+arity]
	if refArity > 0 {
		src := len(s.refs) - refArity
		copy(s.refs[refHeight:], s.refs[src:])
	}
	s.refs = s.refs[:refHeight+refArity]
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
				// A block exit: keep 1 numeric result, discard the scratch below it.
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- Candidate A: per-slot uint64 sequence numbers ----------

type stackSeq64 struct {
	num    []uint64
	numSeq []uint64
	refs   []uint64
	refSeq []uint64
	next   uint64
}

func (s *stackSeq64) pushNum(v uint64) {
	s.num = append(s.num, v)
	s.numSeq = append(s.numSeq, s.next)
	s.next++
}

func (s *stackSeq64) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	s.numSeq = s.numSeq[:len(s.numSeq)-1]
	return v
}

func (s *stackSeq64) pushRef(v uint64) {
	s.refs = append(s.refs, v)
	s.refSeq = append(s.refSeq, s.next)
	s.next++
}

func (s *stackSeq64) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return v
}

func (s *stackSeq64) branch(height, arity, refHeight, refArity int) {
	if arity > 0 {
		src := len(s.num) - arity
		copy(s.num[height:], s.num[src:])
		copy(s.numSeq[height:], s.numSeq[src:])
	}
	s.num = s.num[:height+arity]
	s.numSeq = s.numSeq[:height+arity]
	if refArity > 0 {
		src := len(s.refs) - refArity
		copy(s.refs[refHeight:], s.refs[src:])
		copy(s.refSeq[refHeight:], s.refSeq[src:])
	}
	s.refs = s.refs[:refHeight+refArity]
	s.refSeq = s.refSeq[:refHeight+refArity]
}

// dropSeq64 is the payoff: drop's own fix, reading whichever array's top has the higher
// sequence number — an empty array reads as "older than everything".
func (s *stackSeq64) drop() {
	numTop, refTop := int64(-1), int64(-1)
	if len(s.numSeq) > 0 {
		numTop = int64(s.numSeq[len(s.numSeq)-1])
	}
	if len(s.refSeq) > 0 {
		refTop = int64(s.refSeq[len(s.refSeq)-1])
	}
	if refTop > numTop {
		s.popRef()
		return
	}
	s.popNum()
}

func runSeq64(iters int) uint64 {
	s := &stackSeq64{
		num: make([]uint64, 0, 64), numSeq: make([]uint64, 0, 64),
		refs: make([]uint64, 0, 8), refSeq: make([]uint64, 0, 8),
	}
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

// ---------- Candidate B: per-slot uint8 sequence numbers (narrower tag, same mechanism) ----------

type stackSeq8 struct {
	num    []uint64
	numSeq []uint8
	refs   []uint64
	refSeq []uint8
	next   uint8
}

func (s *stackSeq8) pushNum(v uint64) {
	s.num = append(s.num, v)
	s.numSeq = append(s.numSeq, s.next)
	s.next++
}

func (s *stackSeq8) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	s.numSeq = s.numSeq[:len(s.numSeq)-1]
	return v
}

func (s *stackSeq8) pushRef(v uint64) {
	s.refs = append(s.refs, v)
	s.refSeq = append(s.refSeq, s.next)
	s.next++
}

func (s *stackSeq8) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return v
}

func (s *stackSeq8) branch(height, arity, refHeight, refArity int) {
	if arity > 0 {
		src := len(s.num) - arity
		copy(s.num[height:], s.num[src:])
		copy(s.numSeq[height:], s.numSeq[src:])
	}
	s.num = s.num[:height+arity]
	s.numSeq = s.numSeq[:height+arity]
	if refArity > 0 {
		src := len(s.refs) - refArity
		copy(s.refs[refHeight:], s.refs[src:])
		copy(s.refSeq[refHeight:], s.refSeq[src:])
	}
	s.refs = s.refs[:refHeight+refArity]
	s.refSeq = s.refSeq[:refHeight+refArity]
}

// **The u8 counter wraps every 256 pushes — deliberately unsound as shown, and that unsoundness
// is the reason this variant is not the ADR's recommendation on its own.** A wraparound makes
// "higher sequence number" ambiguous once more than 255 pushes separate the two arrays' tops,
// which is common in a deep or long-lived function body. Measured anyway to answer whether width
// matters at all for this access pattern — and it does, roughly in half: two independent runs put
// Seq64 at +72–74% over baseline and Seq8 at +38–39%, so a correctness patch for the wraparound
// (a generation counter reset per call frame, or accepting u64) would give back real cost, not a
// rounding error. The width-versus-correctness tradeoff is therefore live, not moot.
func (s *stackSeq8) drop() {
	numTop, refTop := int16(-1), int16(-1)
	if len(s.numSeq) > 0 {
		numTop = int16(s.numSeq[len(s.numSeq)-1])
	}
	if len(s.refSeq) > 0 {
		refTop = int16(s.refSeq[len(s.refSeq)-1])
	}
	if refTop > numTop {
		s.popRef()
		return
	}
	s.popNum()
}

func runSeq8(iters int) uint64 {
	s := &stackSeq8{
		num: make([]uint64, 0, 64), numSeq: make([]uint8, 0, 64),
		refs: make([]uint64, 0, 8), refSeq: make([]uint8, 0, 8),
	}
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

// TestAllAgree is the judge-needs-a-judge control (dispatchbench's own precedent, contract §9
// G-4): all three variants must compute the identical accumulator before any timing means
// anything — a benchmark comparing implementations that disagree is measuring nothing.
func TestAllAgree(t *testing.T) {
	const iters = 5
	base := runBase(iters)
	seq64 := runSeq64(iters)
	seq8 := runSeq8(iters)
	for name, got := range map[string]uint64{"Seq64": seq64, "Seq8": seq8} {
		if got != base {
			t.Errorf("%s = %d, want %d (Base)", name, got, base)
		}
	}
	t.Logf("all three agree on %d over %d iterations of N=%d", base, iters, N)
}

// TestDropPicksTheCorrectArray is the correctness half no benchmark can substitute for:
// stackSeq64.drop/stackSeq8.drop must actually identify the logical top correctly, exercising
// exactly the shape grave #206 got wrong — a ref pushed after the most recent numeric value must
// be the one `drop` removes.
func TestDropPicksTheCorrectArray(t *testing.T) {
	s64 := &stackSeq64{}
	s64.pushNum(1)
	s64.pushRef(99)
	s64.drop()
	if len(s64.refs) != 0 || len(s64.num) != 1 {
		t.Errorf("Seq64: drop popped the wrong array: num=%v refs=%v", s64.num, s64.refs)
	}

	s8 := &stackSeq8{}
	s8.pushNum(1)
	s8.pushRef(99)
	s8.drop()
	if len(s8.refs) != 0 || len(s8.num) != 1 {
		t.Errorf("Seq8: drop popped the wrong array: num=%v refs=%v", s8.num, s8.refs)
	}
}

const iters = 200

func BenchmarkBase(b *testing.B) {
	for range b.N {
		runBase(iters)
	}
}

func BenchmarkSeq64(b *testing.B) {
	for range b.N {
		runSeq64(iters)
	}
}

func BenchmarkSeq8(b *testing.B) {
	for range b.N {
		runSeq8(iters)
	}
}

// ---------- Pure-numeric variant: same three implementations, zero references ever pushed ----------
//
// **The strongest argument for gating this mechanism per-function, measured rather than assumed.**
// 0 of the numeric core's 13671 answerable corpus vectors need a reference at all (exec.go's own
// header) — so the realistic cost of an *ungated* fix is not the refEvery=97 mixed workload above,
// it is this: bookkeeping paid on every numeric push/pop by a function that never touches a
// reference, mirroring `frame`'s own lazy refs/isRef allocation precedent (newFrame, value.go) —
// which pays nothing for a function with no reference-typed param or local. If the ungated cost
// here is close to the mixed-workload cost above, gating buys back nearly the whole regression for
// the common case; if it is not, gating buys back less than expected and the ADR needs to say so.

func runBaseNoRefs(iters int) uint64 {
	s := &stackBase{num: make([]uint64, 0, 64)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, 0, 0)
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
	}
	return acc
}

func runSeq64NoRefs(iters int) uint64 {
	s := &stackSeq64{num: make([]uint64, 0, 64), numSeq: make([]uint64, 0, 64)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, 0, 0)
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
	}
	return acc
}

func TestAllAgreeNoRefs(t *testing.T) {
	const iters = 5
	base := runBaseNoRefs(iters)
	seq64 := runSeq64NoRefs(iters)
	if seq64 != base {
		t.Errorf("Seq64NoRefs = %d, want %d (BaseNoRefs)", seq64, base)
	}
}

func BenchmarkBaseNoRefs(b *testing.B) {
	for range b.N {
		runBaseNoRefs(iters)
	}
}

func BenchmarkSeq64NoRefs(b *testing.B) {
	for range b.N {
		runSeq64NoRefs(iters)
	}
}

// ---------- Candidate C: lazily-gated tracking, active only after the first pushRef ----------
//
// **Whether a branch-gated skip recovers the no-ref cost — measured, and the answer is "mostly,
// for the case that matters, and not at all for the case that doesn't".** Unlike Seq64/Seq8
// (which always append to numSeq), this variant checks a bool before deciding whether to track at
// all — mirroring `frame`'s own lazy refs/isRef allocation (newFrame, value.go). Two independent
// runs each: gated-no-refs costs **+27–29%** over ungated-no-refs' own baseline (down from
// Seq64's +72–75% when references are never involved at all — most of the regression bought
// back), but gated-mixed costs **+73%**, statistically indistinguishable from Seq64/Seq8's own
// always-on mixed cost — once a function pushes even one reference, tracking activates and stays
// active for the rest of that function's execution, so gating buys nothing for a function that
// genuinely uses references. The gate is a real win exactly for the population exec.go's own
// header names (0 of the 139-opcode numeric core's corpus needs a reference at all) and no win
// beyond it.
type stackGated struct {
	num      []uint64
	numSeq   []uint64
	refs     []uint64
	refSeq   []uint64
	next     uint64
	tracking bool
}

func (s *stackGated) pushNum(v uint64) {
	s.num = append(s.num, v)
	if s.tracking {
		s.numSeq = append(s.numSeq, s.next)
		s.next++
	}
}

func (s *stackGated) popNum() uint64 {
	v := s.num[len(s.num)-1]
	s.num = s.num[:len(s.num)-1]
	if s.tracking {
		s.numSeq = s.numSeq[:len(s.numSeq)-1]
	}
	return v
}

func (s *stackGated) pushRef(v uint64) {
	if !s.tracking {
		// Activate and backfill: every already-pushed numeric slot needs a sequence number
		// too, or the invariant (every slot has one) breaks the moment drop is asked about a
		// stack that mixes pre- and post-activation slots. Backfilling with ascending numbers
		// starting below `next` preserves relative order correctly since nothing was popped
		// between push order and now.
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

func (s *stackGated) popRef() uint64 {
	v := s.refs[len(s.refs)-1]
	s.refs = s.refs[:len(s.refs)-1]
	s.refSeq = s.refSeq[:len(s.refSeq)-1]
	return v
}

func (s *stackGated) branch(height, arity, refHeight, refArity int) {
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

func runGatedNoRefs(iters int) uint64 {
	s := &stackGated{num: make([]uint64, 0, 64)}
	var acc uint64
	for range iters {
		for i := range N {
			s.pushNum(uint64(i))
			if i%37 == 0 && len(s.num) > 4 {
				s.branch(len(s.num)-4, 1, 0, 0)
			}
			acc += s.popNum()
			s.pushNum(acc)
		}
	}
	return acc
}

func runGatedMixed(iters int) uint64 {
	s := &stackGated{num: make([]uint64, 0, 64), refs: make([]uint64, 0, 8)}
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

func TestAllAgreeGated(t *testing.T) {
	const iters = 5
	if got, want := runGatedNoRefs(iters), runBaseNoRefs(iters); got != want {
		t.Errorf("GatedNoRefs = %d, want %d", got, want)
	}
	if got, want := runGatedMixed(iters), runBase(iters); got != want {
		t.Errorf("GatedMixed = %d, want %d", got, want)
	}
}

func BenchmarkGatedNoRefs(b *testing.B) {
	for range b.N {
		runGatedNoRefs(iters)
	}
}

func BenchmarkGatedMixed(b *testing.B) {
	for range b.N {
		runGatedMixed(iters)
	}
}
