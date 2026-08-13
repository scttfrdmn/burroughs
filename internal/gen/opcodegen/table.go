package opcodegen

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// checkFloors is condition 1 of decision 0007: the vacuity control.
//
// Read the doc on Floors for why this is not a sanity check but the control on a
// specific failure the unrecognized-arm error is blind to.
func (t *Table) checkFloors() error {
	counts := map[byte]int{}
	for _, a := range t.Arms {
		counts[a.Prefix]++
	}
	for _, prefix := range slices.Sorted(maps.Keys(Floors)) {
		want := Floors[prefix]
		if got := counts[prefix]; got < want {
			return fmt.Errorf("%w: prefix %#02x yielded %d arms, floor is %d — "+
				"the extractor recognized implausibly little, which is what a moved file or a "+
				"changed layout upstream looks like; an empty table would otherwise drift-check "+
				"clean against an empty commit", ErrVacuous, prefix, got, want)
		}
	}
	return nil
}

// checkDuplicates catches an arm parsed twice, which would otherwise show up as a
// silently last-wins table entry.
//
// Cheap, and it is the control on the block-extent logic: if findPrefixBlocks got a
// region's end wrong, the prefixed arms inside it would also be read as single-byte
// arms and collide here rather than quietly overwriting.
func (t *Table) checkDuplicates() error {
	seen := map[[2]uint32]int{}
	for _, a := range t.Arms {
		k := [2]uint32{uint32(a.Prefix), a.Code}
		if prev, ok := seen[k]; ok {
			return fmt.Errorf("%w: opcode prefix=%#02x code=%#x appears at decode.ml:%d and :%d",
				errUnrecognized, a.Prefix, a.Code, prev, a.Line)
		}
		seen[k] = a.Line
	}
	return nil
}

func sortArms(arms []Arm) {
	slices.SortFunc(arms, func(a, b Arm) int {
		if c := cmp.Compare(a.Prefix, b.Prefix); c != 0 {
			return c
		}
		return cmp.Compare(a.Code, b.Code)
	})
}

var reErrorText = regexp.MustCompile(`error s pos "([^"]*)"`)

func errorText(rhs string) string {
	if m := reErrorText.FindStringSubmatch(rhs); m != nil {
		return m[1]
	}
	return ""
}

// reDispatchOnCode matches a constructor chosen by the opcode itself:
// `(if opcode = 0x18l then br_on_cast else br_on_cast_fail) x rt1 rt2` (decode.ml:646).
var reDispatchOnCode = regexp.MustCompile(
	`\(\s*if\s+\w+\s*=\s*0x([0-9a-f]+)l?\s+then\s+([a-z][a-z0-9_]*)\s+else\s+([a-z][a-z0-9_]*)\s*\)`)

// mnemonicOf recovers the reference's constructor name for the arm's given code.
//
// Not load-bearing — it exists so the generated table reads like the authority and so
// error messages can name an opcode. The immediates are the facts; this is the label.
// It takes the code because one arm can cover several opcodes with *different*
// constructors, so a single label per arm would be wrong by construction.
func mnemonicOf(rhs string, code uint32) string {
	// One arm, two mnemonics, selected by the opcode: `0x18l | 0x19l as opcode -> ...
	// (if opcode = 0x18l then br_on_cast else br_on_cast_fail) ...`. Handled before the
	// generic path, which reported the OCaml keyword `if` for both codes.
	if m := reDispatchOnCode.FindStringSubmatch(rhs); m != nil {
		if v, err := strconv.ParseUint(m[1], 16, 32); err == nil {
			if v == uint64(code) {
				return m[2]
			}
			return m[3]
		}
	}
	// The mnemonic is applied last: `let x = at idx s in call x` → `call`, and
	// `let x, a, o = memop s in v128_load x a o` → `v128_load`. Take the text after
	// the final `in`, then its first identifier.
	tail := rhs
	if i := strings.LastIndex(rhs, " in "); i >= 0 {
		tail = rhs[i+4:]
	}
	// ...but "last `in`" is not "last statement": the four structural arms end
	// `end_ s; block bt es'`, where the constructor follows a *sequence* separator
	// rather than a binding. Taking the text after the final `in` alone reported all
	// four as `end_` — the right verdict about which arm, quoting the wrong name.
	// Caught by printing what the code returns for the arms whose shape is known
	// (docs/laws/errors-and-testimony.md — print-don't-trust, and a label nobody checks
	// is a claim).
	if i := strings.LastIndex(tail, ";"); i >= 0 {
		tail = tail[i+1:]
	}
	tail = strings.TrimSpace(tail)
	// Strip a qualifying module (`Mnemonics.catch`) and any parenthesised prefix.
	tail = strings.TrimPrefix(tail, "(")
	if i := strings.LastIndex(tail, "."); i >= 0 && i < 20 {
		tail = tail[i+1:]
	}
	m := regexp.MustCompile(`^([a-z][a-z0-9_]*)`).FindStringSubmatch(tail)
	if m == nil {
		return ""
	}
	return m[1]
}
