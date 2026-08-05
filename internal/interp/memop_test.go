package interp

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestMemopTableAgreesWithMnemonics cross-checks every `memops` row against the generated opcode
// table's mnemonic.
//
// **This is the accept-direction control for the load/store family** (§9 G-3, and the
// authority-for-accept-direction-facts rule). Every fact in a `memop` row — 1/2/4/8 bytes, signed
// or not, i32/i64/f32/f64 slot — is recoverable from the reference's own constructor name, so the
// name is a second authority that can disagree. It matters because a wrong row produces a
// *plausible value for a valid module*: `i64_load16_s` transcribed as unsigned reads 0x00FF where
// the spec says 0xFFFF…FF, which no assert_malformed vector can see and which the board scores
// green by construction.
//
// Derived from the space, not from today's subset: the loop walks all 256 single-byte opcodes and
// requires that a row exists exactly where the mnemonic says one should, so an opcode added to
// either side without the other is a failure rather than a silent omission.
func TestMemopTableAgreesWithMnemonics(t *testing.T) {
	checked := 0
	for op := range uint32(256) {
		name, ok := binary.OpMnemonic(op)
		if !ok {
			continue
		}
		want, isMemop := parseMemopMnemonic(name)
		got, haveRow := memops[op]
		switch {
		case isMemop && !haveRow:
			t.Errorf("opcode %02x is %q — a load/store by its mnemonic — and memops has no "+
				"row for it, so run's case list will treat it as an engine gap forever", op, name)
			continue
		case !isMemop && haveRow:
			t.Errorf("memops has a row for opcode %02x, whose mnemonic %q is not a load or "+
				"store; the table has drifted off the family it claims", op, name)
			continue
		case !isMemop:
			continue
		}
		checked++
		if got != want {
			t.Errorf("opcode %02x (%s): memops says %+v, the mnemonic says %+v", op, name, got, want)
		}
	}

	// Vacuity floor: a comparison against an empty set agrees perfectly. 23 is the exact
	// count (0x28-0x3e), and it is pinned exactly rather than as a floor because it is
	// *knowable* — a floor here would stay green through a 22-of-23 loss, which is grave
	// #105's lesson in this position.
	if checked != 23 {
		t.Errorf("cross-checked %d rows, want exactly 23 (0x28-0x3e); a mnemonic parser that "+
			"silently stopped matching would agree with everything it still read", checked)
	}
	if len(memops) != 23 {
		t.Errorf("memops has %d rows, want 23", len(memops))
	}
}

// parseMemopMnemonic derives a memop from a reference constructor name.
//
// The grammar is `<type>_(load|store)[<width>[_s|_u]]`: `i32_load`, `i64_load16_s`,
// `f64_store`, `i32_store8`. Anything else is not in the family — including `memory_size` and
// `memory_grow`, which take an index rather than a memarg and are handled as their own arms.
//
// **The parser is the independent authority, so it must not consult `memops`.** Deriving the
// answer from the thing under test is grave #106's shape: premise and implementation agreeing
// because they share an assumption. Everything here comes from the string.
func parseMemopMnemonic(name string) (memop, bool) {
	jj := strings.SplitN(name, "_", 2)
	if len(jj) != 2 {
		return memop{}, false
	}
	valType, rest := jj[0], jj[1]

	var m memop
	switch valType {
	case "i32":
	case "i64":
		m.is64 = true
	case "f32":
		m.isFloat = true
	case "f64":
		m.is64, m.isFloat = true, true
	default:
		return memop{}, false
	}

	// The slot's natural width, which a bare load/store touches.
	natural := uint64(4)
	if m.is64 {
		natural = 8
	}

	var suffix string
	switch {
	case strings.HasPrefix(rest, "load"):
		suffix = strings.TrimPrefix(rest, "load")
	case strings.HasPrefix(rest, "store"):
		suffix = strings.TrimPrefix(rest, "store")
	default:
		return memop{}, false
	}

	if suffix == "" {
		m.width = natural
		return m, true
	}
	// A packed access: digits, then optionally _s or _u. Floats have no packed forms, so a
	// suffix on one means this is not the family (there is no `f32_load8`).
	if m.isFloat {
		return memop{}, false
	}
	switch {
	case strings.HasSuffix(suffix, "_s"):
		m.signed = true
		suffix = strings.TrimSuffix(suffix, "_s")
	case strings.HasSuffix(suffix, "_u"):
		suffix = strings.TrimSuffix(suffix, "_u")
	}
	switch suffix {
	case "8":
		m.width = 1
	case "16":
		m.width = 2
	case "32":
		m.width = 4
	default:
		return memop{}, false
	}
	// A packed access must be narrower than its slot: `i32_load32_s` is not an instruction,
	// and admitting one would let a mis-parse look like a legitimate row.
	if m.width >= natural && !m.is64 {
		return memop{}, false
	}
	return m, true
}
