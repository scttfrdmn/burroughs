package dispatchbench

import "testing"

// The benchmarks above use only single-byte LEB immediates — the *best* case for
// in-place decoding and the worst for side tables. Real modules have large local
// indices and large constants (3-5 byte LEBs). This variant forces multi-byte
// immediates by padding local indices and using a large constant, so the
// re-decode cost in-place pays is representative rather than free.
func buildWide() []byte {
	var p []byte
	// local indices 0 and 1 encoded non-minimally is malformed, so instead use
	// genuinely large indices: acc=128, i=129 (2-byte LEBs), const N+1 large.
	const acc, i = 128, 129
	e := func(b ...byte) { p = append(p, b...) }
	e(opLocalGet)
	p = append(p, leb(acc)...)
	e(opLocalGet)
	p = append(p, leb(i)...)
	e(opAdd)
	e(opLocalSet)
	p = append(p, leb(acc)...)
	e(opLocalGet)
	p = append(p, leb(i)...)
	e(opConst)
	p = append(p, leb(1)...)
	e(opAdd)
	e(opLocalSet)
	p = append(p, leb(i)...)
	e(opLocalGet)
	p = append(p, leb(i)...)
	e(opConst)
	p = append(p, leb(WideN+1)...)
	e(opLtS)
	e(opBrIf)
	p = append(p, leb(0)...)
	e(opEnd)
	return p
}

const WideN = 1000

var (
	wideCode = buildWide()
	wideProg = buildRewrite(wideCode)
	wideSide = buildSide(wideCode)
)

func wideLocals() []int32 { l := make([]int32, 200); l[129] = 1; return l }

func TestWideAgree(t *testing.T) {
	want := int32(WideN * (WideN + 1) / 2)
	a := runInPlace(wideCode, wideLocals(), make([]int32, 16))
	// runInPlace/runRewrite return locals[0]; for wide, acc is local 128.
	_ = a
	lb := wideLocals()
	runInPlace(wideCode, lb, make([]int32, 16))
	if lb[128] != want {
		t.Errorf("inplace wide acc = %d, want %d", lb[128], want)
	}
	lc := wideLocals()
	runRewrite(wideProg, lc, make([]int32, 16))
	if lc[128] != want {
		t.Errorf("rewrite wide acc = %d, want %d", lc[128], want)
	}
	ld := wideLocals()
	runSide(wideCode, wideSide, ld, make([]int32, 16))
	if ld[128] != want {
		t.Errorf("side wide acc = %d, want %d", ld[128], want)
	}
	t.Logf("wide: %d bytecode bytes (vs %d narrow), %d ins", len(wideCode), len(code), len(wideProg))
}

func BenchmarkWideInPlace(b *testing.B) {
	st := make([]int32, 16)
	l := wideLocals()
	for i := 0; i < b.N; i++ {
		l[128], l[129] = 0, 1
		runInPlace(wideCode, l, st)
	}
}
func BenchmarkWideRewrite(b *testing.B) {
	st := make([]int32, 16)
	l := wideLocals()
	for i := 0; i < b.N; i++ {
		l[128], l[129] = 0, 1
		runRewrite(wideProg, l, st)
	}
}
func BenchmarkWideSideTable(b *testing.B) {
	st := make([]int32, 16)
	l := wideLocals()
	for i := 0; i < b.N; i++ {
		l[128], l[129] = 0, 1
		runSide(wideCode, wideSide, l, st)
	}
}
