package dispatchbench

import "testing"

// 4. In-place with side tables (Wizard-style): the bytecode stays the
// authoritative program — pc is a bytecode offset, nothing is rewritten — but a
// sparse side table maps immediate-bearing pcs to pre-decoded values, so the
// hot path never re-decodes LEB.
type sideEntry struct {
	imm    int32
	nextPC int32 // pc after the immediate, so dispatch skips the LEB bytes
}

func buildSide(code []byte) []sideEntry {
	tbl := make([]sideEntry, len(code))
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
		at := pc
		op := code[pc]
		pc++
		switch op {
		case opLocalGet, opLocalSet, opConst, opBrIf:
			v := u32()
			tbl[at] = sideEntry{imm: int32(v), nextPC: int32(pc)}
		default:
			tbl[at] = sideEntry{nextPC: int32(pc)}
		}
	}
	return tbl
}

func runSide(code []byte, tbl []sideEntry, locals, stack []int32) int32 {
	pc, sp := 0, 0
	for {
		op := code[pc]
		e := tbl[pc]
		pc = int(e.nextPC)
		switch op {
		case opLocalGet:
			stack[sp] = locals[e.imm]
			sp++
		case opLocalSet:
			sp--
			locals[e.imm] = stack[sp]
		case opConst:
			stack[sp] = e.imm
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
				pc = int(e.imm)
			}
		case opEnd:
			return locals[0]
		}
	}
}

var side = buildSide(code)

func TestSideAgrees(t *testing.T) {
	want := int32(N * (N + 1) / 2)
	if got := runSide(code, side, []int32{0, 1}, make([]int32, 16)); got != want {
		t.Fatalf("side = %d, want %d", got, want)
	}
	t.Logf("side table: %d entries for %d bytecode bytes", len(side), len(code))
}

func BenchmarkInPlaceSideTable(b *testing.B) {
	st := make([]int32, 16)
	for range b.N {
		runSide(code, side, []int32{0, 1}, st)
	}
}
