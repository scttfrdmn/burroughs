package dispatchbench

import "testing"

// A loop summing 1..N, expressed three ways. Same logical program, same value
// stack discipline; only the dispatch/immediate strategy differs.
//
// Opcodes (wasm numbering where it exists):
//
//	0x20 local.get <u32>   0x21 local.set <u32>   0x41 i32.const <i32>
//	0x6a i32.add           0x48 i32.lt_s          0x0d br_if <abs pc>
//	0x0b end
const (
	opLocalGet = 0x20
	opLocalSet = 0x21
	opConst    = 0x41
	opAdd      = 0x6a
	opLtS      = 0x48
	opBrIf     = 0x0d
	opEnd      = 0x0b
)

const N = 1000

func leb(v uint32) []byte {
	var out []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, c|0x80)
			continue
		}
		out = append(out, c)
		return out
	}
}

// ---------- 1. in-place: LEB immediates decoded on every execution ----------

func buildInPlace() []byte {
	var p []byte
	e := func(b ...byte) { p = append(p, b...) }
	// loop start at index 0
	e(opLocalGet)
	p = append(p, leb(0)...) // acc
	e(opLocalGet)
	p = append(p, leb(1)...) // i
	e(opAdd)
	e(opLocalSet)
	p = append(p, leb(0)...) // acc += i
	e(opLocalGet)
	p = append(p, leb(1)...)
	e(opConst)
	p = append(p, leb(1)...)
	e(opAdd)
	e(opLocalSet)
	p = append(p, leb(1)...) // i++
	e(opLocalGet)
	p = append(p, leb(1)...)
	e(opConst)
	p = append(p, leb(N+1)...)
	e(opLtS)
	e(opBrIf)
	p = append(p, leb(0)...) // branch to 0
	e(opEnd)
	return p
}

func runInPlace(code []byte, locals, stack []int32) int32 {
	pc, sp := 0, 0
	u32 := func() uint32 {
		var v uint32
		var shift uint
		for {
			c := code[pc]
			pc++
			v |= uint32(c&0x7f) << shift
			if c&0x80 == 0 {
				return v
			}
			shift += 7
		}
	}
	for {
		op := code[pc]
		pc++
		switch op {
		case opLocalGet:
			stack[sp] = locals[u32()]
			sp++
		case opLocalSet:
			sp--
			locals[u32()] = stack[sp]
		case opConst:
			stack[sp] = int32(u32())
			sp++
		case opAdd:
			sp--
			stack[sp-1] += stack[sp]
		case opLtS:
			sp--
			if stack[sp-1] < stack[sp] {
				stack[sp-1] = 1
			} else {
				stack[sp-1] = 0
			}
		case opBrIf:
			t := u32()
			sp--
			if stack[sp] != 0 {
				pc = int(t)
			}
		case opEnd:
			return locals[0]
		}
	}
}

// ---------- 2. internal form: immediates pre-decoded into a struct ----------

type ins struct {
	op  byte
	imm int32
}

func buildRewrite(code []byte) []ins {
	var out []ins
	// map bytecode pc -> internal index, to fix up branch targets
	pcToIdx := map[int]int{}
	pc := 0
	u32 := func() uint32 {
		var v uint32
		var shift uint
		for {
			c := code[pc]
			pc++
			v |= uint32(c&0x7f) << shift
			if c&0x80 == 0 {
				return v
			}
			shift += 7
		}
	}
	for pc < len(code) {
		pcToIdx[pc] = len(out)
		op := code[pc]
		pc++
		switch op {
		case opLocalGet, opLocalSet, opConst, opBrIf:
			out = append(out, ins{op, int32(u32())})
		default:
			out = append(out, ins{op, 0})
		}
	}
	for i := range out {
		if out[i].op == opBrIf {
			out[i].imm = int32(pcToIdx[int(out[i].imm)])
		}
	}
	return out
}

func runRewrite(prog []ins, locals, stack []int32) int32 {
	pc, sp := 0, 0
	for {
		in := prog[pc]
		pc++
		switch in.op {
		case opLocalGet:
			stack[sp] = locals[in.imm]
			sp++
		case opLocalSet:
			sp--
			locals[in.imm] = stack[sp]
		case opConst:
			stack[sp] = in.imm
			sp++
		case opAdd:
			sp--
			stack[sp-1] += stack[sp]
		case opLtS:
			sp--
			if stack[sp-1] < stack[sp] {
				stack[sp-1] = 1
			} else {
				stack[sp-1] = 0
			}
		case opBrIf:
			sp--
			if stack[sp] != 0 {
				pc = int(in.imm)
			}
		case opEnd:
			return locals[0]
		}
	}
}

// ---------- 3. closure compilation ----------

type frame struct {
	locals []int32
	stack  []int32
	sp     int
	pc     int
}

type step func(*frame)

func buildClosures(prog []ins) []step {
	out := make([]step, len(prog))
	for i, in := range prog {
		imm := in.imm
		switch in.op {
		case opLocalGet:
			out[i] = func(f *frame) { f.stack[f.sp] = f.locals[imm]; f.sp++; f.pc++ }
		case opLocalSet:
			out[i] = func(f *frame) { f.sp--; f.locals[imm] = f.stack[f.sp]; f.pc++ }
		case opConst:
			out[i] = func(f *frame) { f.stack[f.sp] = imm; f.sp++; f.pc++ }
		case opAdd:
			out[i] = func(f *frame) { f.sp--; f.stack[f.sp-1] += f.stack[f.sp]; f.pc++ }
		case opLtS:
			out[i] = func(f *frame) {
				f.sp--
				if f.stack[f.sp-1] < f.stack[f.sp] {
					f.stack[f.sp-1] = 1
				} else {
					f.stack[f.sp-1] = 0
				}
				f.pc++
			}
		case opBrIf:
			out[i] = func(f *frame) {
				f.sp--
				if f.stack[f.sp] != 0 {
					f.pc = int(imm)
				} else {
					f.pc++
				}
			}
		case opEnd:
			out[i] = func(f *frame) { f.pc = -1 }
		}
	}
	return out
}

func runClosures(steps []step, locals, stack []int32) int32 {
	f := &frame{locals: locals, stack: stack}
	for f.pc >= 0 {
		steps[f.pc](f)
	}
	return f.locals[0]
}

var (
	code     = buildInPlace()
	prog     = buildRewrite(code)
	closures = buildClosures(prog)
)

func TestAllAgree(t *testing.T) {
	want := int32(N * (N + 1) / 2)
	for name, got := range map[string]int32{
		"inplace":  runInPlace(code, []int32{0, 1}, make([]int32, 16)),
		"rewrite":  runRewrite(prog, []int32{0, 1}, make([]int32, 16)),
		"closures": runClosures(closures, []int32{0, 1}, make([]int32, 16)),
	} {
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	t.Logf("all three agree on %d; %d bytecode bytes, %d internal ins", want, len(code), len(prog))
}

func BenchmarkInPlace(b *testing.B) {
	st := make([]int32, 16)
	for range b.N {
		runInPlace(code, []int32{0, 1}, st)
	}
}

func BenchmarkRewrite(b *testing.B) {
	st := make([]int32, 16)
	for range b.N {
		runRewrite(prog, []int32{0, 1}, st)
	}
}

func BenchmarkClosures(b *testing.B) {
	st := make([]int32, 16)
	for range b.N {
		runClosures(closures, []int32{0, 1}, st)
	}
}
