package validate

import (
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestFBConstantsAgreeWithTheGeneratedTable is the control `gc.go`'s header promises when it says the
// 31 named sub-opcodes are "checked against the generated table by a control rather than trusted".
//
// A named constant is a readability win and a second copy of a wire-format fact, and the second half
// is why this exists. `fbArrayGetU = 0x0d` reads as documentation while being an assertion about the
// GC proposal's opcode assignment, and a validator whose constant disagrees with the decoder's table
// types the wrong instruction with a completely coherent-looking `switch`: the arm fires, the stack
// discipline is right for the rule it implements, and the module it accepts is the one nobody
// checked. That is the accept-direction failure (§9 G-3), reached by a typo.
//
// **The domain is derived in both directions**, which is 0006's law and also the only way this catches
// the case it is built for. Forward: every named `fb*` constant must be a sub-opcode
// `binary.PrefixedOp` admits. Reverse: every sub-opcode the table admits must have a named constant —
// so a 32nd entry arriving upstream fails here with a demand rather than being typed by whatever arm
// the `switch` happens to fall to. The reverse half is the one an enumeration cannot provide, and it
// is the direction `internal/interp`'s `TestEveryFBSubOpcodeIsAnswered` already covers for the
// executor; this is that control's typing-side twin, and the two together mean the region's frontier
// cannot move in one package without failing in the other.
//
// The scan window and its floor are borrowed from that test for its stated reasons: a sub-opcode is a
// u32 LEB so the space is unscannable and `window` is declared a window rather than described as the
// space, and the floor comes from the *authority* — the GC proposal defines `0x00`-`0x1e` — because a
// floor derived from what the scan returns is a certificate for the scan's own failure.
//
// # Watched die, four ways
//
// `fbArrayGetU = 0x0d → 0x1f` fails three of the checks at once and the triple is the interesting
// part: forward (a constant the table does not admit), reverse (`array_get_u` now unnamed), and the
// coverage figure (30 of 31 matched). Deleting the last constant trips the AST floor. Renaming
// `fbArrayNewFixed` to `fbArrayNewFixedLen` trips exact name agreement, and renaming `fbRefTestNull`
// to `fbCastTestNullable` trips the family check for the duplicated mnemonic — the two naming branches
// falsified separately, since a control with two branches has watched only one die until both have.
//
// **The fifth attempt is the one worth writing down, because it was the falsification that lied.**
// `sed 's/fbArrayNewFixed\b/…/'` reported success and changed nothing — BSD `sed` has no `\b` — so the
// suite came back green and the first reading was "the name-agreement branch does not fire". A
// falsification that silently fails to apply is indistinguishable from a control that fails to
// trigger, and it points the wrong way: it accuses the control. The tell was that the second attempt
// *did* fail, and the difference between them was the edit landing rather than anything about the
// check. So a falsification's own precondition gets verified — `grep` the edited line, or let the
// compiler do it, which is what renaming both sites accomplished. This is
// second-order-honesty applied to the instrument that certifies instruments.
func TestFBConstantsAgreeWithTheGeneratedTable(t *testing.T) {
	const (
		window = 0x1000
		floor  = 31
	)

	table := map[uint32]string{}
	for sub := range uint32(window) {
		if mnemonic, _, ok := binary.PrefixedOp(prefixGC, sub); ok {
			table[sub] = mnemonic
		}
	}
	if len(table) < floor {
		t.Fatalf("the decoder's %#02x table reports %d sub-opcodes in [0, %#x), want at least %d — "+
			"the GC proposal defines fb 00-fb 1e, so a smaller number means both directions below "+
			"are comparing against a shorter list than the table has, and the reverse direction in "+
			"particular would pass by having nothing to demand",
			prefixGC, len(table), window, floor)
	}

	named := fbConstants(t)
	if len(named) < floor {
		t.Fatalf("read %d `fb*` constants out of gc.go, want at least %d — an AST walk that stops "+
			"matching leaves the forward direction with nothing to check and reports it as agreement",
			len(named), floor)
	}

	// Forward: a named constant naming a sub-opcode the decoder does not admit. This is the typo's
	// most likely shape — a value one off the intended one, still inside the region.
	byValue := map[uint32]string{}
	for id, val := range named {
		if val > window {
			t.Errorf("%s = %#x, which is outside the scan window (%#x); either the region grew past "+
				"what this control looks at or the constant is not a sub-opcode at all", id, val, window)
			continue
		}
		if _, ok := table[uint32(val)]; !ok {
			t.Errorf("%s = %#x, which `binary.PrefixedOp` does not admit for prefix %#02x — the "+
				"validator would name an arm for an instruction the decoder never produces, and the "+
				"arm the decoder *does* produce for that byte would fall to the region's default",
				id, val, prefixGC)
			continue
		}
		if prev, dup := byValue[uint32(val)]; dup {
			t.Errorf("%s and %s both name sub-opcode %#x; two names for one opcode is how a "+
				"`switch` ends up with an arm it can never reach", prev, id, val)
			continue
		}
		byValue[uint32(val)] = id
	}

	// Reverse: a sub-opcode the table admits that no constant names. The half that makes this a
	// completeness control rather than a spellcheck.
	for sub, mnemonic := range table {
		if _, ok := byValue[sub]; !ok {
			t.Errorf("the decoder admits %#02x %#02x (%s) and no `fb*` constant names it — a "+
				"sub-opcode this slice cannot see by name is one it types by accident or not at all",
				prefixGC, sub, mnemonic)
		}
	}

	// Name agreement, where the table's mnemonic is unique enough to key on.
	//
	// **The two mnemonics that are not unique are the informative rows.** `ref_test` is the table's
	// name for both `0x14` and `0x15`, and `ref_cast` for both `0x16` and `0x17`, because the decoder
	// distinguishes them by the heap-type immediate's nullability and not by the mnemonic — the text
	// format writes `ref.test null $t`. The validator cannot collapse them the same way: nullability
	// is exactly what its arm branches on, so it needs two names. So a duplicated mnemonic is checked
	// as a *family* — both constants share the mnemonic's derived name as a prefix, and are told apart
	// by a suffix this control does not get to specify — and a mnemonic that is unique is checked
	// exactly. Insisting on an exact match everywhere would have made this control demand that the
	// validator lose the distinction it exists to make.
	perMnemonic := map[string][]uint32{}
	for sub, m := range table {
		perMnemonic[m] = append(perMnemonic[m], sub)
	}
	for sub, mnemonic := range table {
		id, ok := byValue[sub]
		if !ok {
			continue // already reported by the reverse direction
		}
		want := "fb" + camelMnemonic(mnemonic)
		switch {
		case len(perMnemonic[mnemonic]) == 1:
			if id != want {
				t.Errorf("sub-opcode %#02x is %q in the generated table but named %s here, want %s "+
					"— a constant whose name disagrees with the decoder's mnemonic reads as the arm "+
					"for a different instruction at every call site", sub, mnemonic, id, want)
			}
		case !strings.HasPrefix(id, want):
			t.Errorf("sub-opcode %#02x is %q in the generated table, a mnemonic the table gives to "+
				"%d opcodes, but %s does not start with %s — the variants may be distinguished by "+
				"any suffix, and must still be recognisable as that mnemonic's family",
				sub, mnemonic, len(perMnemonic[mnemonic]), id, want)
		}
	}

	// Vacuity, in the direction the loop above cannot see. Every check in it is inside a
	// `for sub := range table`, so a `byValue` that came back empty would report through the reverse
	// direction and this loop would silently check nothing.
	if len(byValue) != len(table) {
		t.Errorf("matched %d of the table's %d sub-opcodes to named constants; the name-agreement "+
			"loop only ran for the matched ones, so the figures must be equal for it to have "+
			"covered the region", len(byValue), len(table))
	}
}

// fbConstants reads gc.go's `fb*` constant block out of the AST rather than off a list here, so the
// control above has a derived domain on the package's side too. Keyed on the `fb` prefix and an
// integer literal: a constant with a computed value is not a wire-format transcription and has no
// business in this block, so failing to read it is the correct behaviour rather than a gap.
func fbConstants(t *testing.T) map[string]uint64 {
	t.Helper()

	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, "gc.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing gc.go: %v", err)
	}

	out := map[string]uint64{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, id := range vs.Names {
			if !strings.HasPrefix(id.Name, "fb") || i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				continue
			}
			v, err := strconv.ParseUint(lit.Value, 0, 32)
			if err != nil {
				t.Errorf("%s = %s does not parse as a sub-opcode: %v", id.Name, lit.Value, err)
				continue
			}
			out[id.Name] = v
		}
		return true
	})
	return out
}

// camelMnemonic turns the generated table's `array_new_fixed` into `ArrayNewFixed`, which is the
// naming convention the constants follow. Derived so that the check above compares the two spellings
// of one fact rather than comparing a list to itself.
func camelMnemonic(mnemonic string) string {
	var b strings.Builder
	for _, part := range strings.Split(mnemonic, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// TestGCDeclinesUnknownSubOpcode is the region's own default arm, and it is separate from the
// partition control in `bulk_test.go` because that one asks whether the *region* declines and this
// asks whether an unassigned sub-opcode *inside a claimed region* does.
//
// The distinction is the accept-direction one. Now that 0xFB is this package's, an opcode the
// dispatch hands to `gcInstr` no longer meets the prefix-level refusal, so `0xfb 0x1f` — one past the
// proposal's last assignment — reaches a `switch` with no arm for it. A `switch` whose default
// returns nil accepts it, and there is no vector for an opcode the decoder rejects, so nothing on any
// board would say so.
func TestGCDeclinesUnknownSubOpcode(t *testing.T) {
	v := &validator{mod: &binary.Module{}}
	v.frames = []frame{{}}

	const unassigned = 0x1f // one past fbI31GetU; the decoder rejects it, so only this reaches the arm
	if _, _, ok := binary.PrefixedOp(prefixGC, unassigned); ok {
		t.Fatalf("the decoder now admits %#02x %#02x, so this test's premise is gone: it asserts "+
			"the *validator's* refusal of a sub-opcode nothing decodes, and an assigned opcode "+
			"needs an arm instead", prefixGC, unassigned)
	}

	err := v.gcInstr(0, binary.Instr{Prefix: prefixGC, Op: unassigned})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want a decline (ErrUnsupported) for %#02x %#02x, got %v — a claimed region whose "+
			"default arm returns nil reports *valid* for an instruction it never typed",
			prefixGC, unassigned, err)
	}
	if want := fmt.Sprintf("%#02x %#02x", prefixGC, unassigned); !strings.Contains(err.Error(), want) {
		t.Errorf("the decline does not name what it declined (want %q): %v", want, err)
	}
}
