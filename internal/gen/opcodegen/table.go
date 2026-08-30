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

// checkFloors is condition 1 of decision 0007: the vacuity control, over the regions the
// *source itself declares*.
//
// Read the doc on Floors for why this is not a sanity check but the control on a
// specific failure the unrecognized-arm error is blind to, and for why the domain is
// Table.Regions rather than Floors' keys — the direction that was wrong, and what the other
// direction (checkFloorDomain) covers instead.
func (t *Table) checkFloors() error {
	counts := map[byte]int{}
	for _, a := range t.Arms {
		counts[a.Prefix]++
	}
	for _, prefix := range t.Regions {
		want, floored := Floors[prefix]
		if !floored {
			return fmt.Errorf("%w: region %#02x has no floor in Floors — a region discovered "+
				"generically and bounded by nothing is unbounded, so its arms could go to zero "+
				"and drift-check clean; add an entry beside the others with its measured count",
				ErrVacuous, prefix)
		}
		if got := counts[prefix]; got < want {
			return fmt.Errorf("%w: prefix %#02x yielded %d arms, floor is %d — "+
				"the extractor recognized implausibly little, which is what a moved file or a "+
				"changed layout upstream looks like; an empty table would otherwise drift-check "+
				"clean against an empty commit", ErrVacuous, prefix, got, want)
		}
	}
	return nil
}

// checkFloorDomain asserts the composed table's region set *equals* Floors' key set, and it
// is the direction checkFloors structurally cannot cover.
//
// checkFloors walks the regions a source declares, so a region that disappears upstream is
// not checked at all: no region, no floor lookup, no complaint, and the emitted table loses a
// whole sub-table quietly. Equality closes that — a floor with no region is as much an error
// as a region with no floor.
//
// Composed-only, because per source the inequality is *correct*: the threads pin has no 0xfb
// region and never did.
func (t *Table) checkFloorDomain() error {
	have := map[byte]bool{}
	for _, p := range t.Regions {
		have[p] = true
	}
	for _, prefix := range slices.Sorted(maps.Keys(Floors)) {
		if !have[prefix] {
			return fmt.Errorf("%w: Floors has a floor for region %#02x and the composed table has "+
				"no such region — either the authority stopped declaring it (which is the case this "+
				"check exists for: a region can vanish without any per-source floor firing) or it is "+
				"read from a pin nothing composes", ErrVacuous, prefix)
		}
	}
	// The other direction is checkFloors', which every composed table has already been
	// through via its sources; restated here so the equality is asserted by one function and
	// a reader does not have to know that.
	for _, p := range t.Regions {
		if _, floored := Floors[p]; !floored {
			return fmt.Errorf("%w: composed region %#02x has no floor in Floors", ErrVacuous, p)
		}
	}
	return nil
}

// checkReadersAreRecorded asserts every region's sub-opcode reader was read from its
// authority and matches what subOpcodeReaders records.
//
// The point is not that the recorded value is *used* — it deliberately is not, the decoder
// reading every region as a LEB — but that the divergence stays visible. If upstream changed
// 0xfe's head from `op` to `u32`, the choice this project made would have become free and
// nobody would know; if a new region arrived, its reader would be unrecorded and read on a
// guess. Both are failures of the record rather than of the table, which is why they are
// checked here and not by a floor.
func (t *Table) checkReadersAreRecorded() error {
	for _, p := range t.Regions {
		got, read := t.Readers[p]
		if !read || got == "" {
			return fmt.Errorf("%w: region %#02x declares a sub-table whose `match` head named no "+
				"recognized reader — reMatchHead's alternation is closed, so this is a reader whose "+
				"width nobody here has read", ErrVacuous, p)
		}
		if p == 0x00 {
			// The opcode byte itself, not a sub-opcode: recorded, and subOpcodeReaders
			// deliberately has no entry (see topLevelReader).
			continue
		}
		want, recorded := subOpcodeReaders[p]
		if !recorded {
			return fmt.Errorf("%w: region %#02x's sub-opcode reader is %q in the authority and is "+
				"not recorded in subOpcodeReaders — the record is what keeps this engine's choice of "+
				"reader from being an accident", ErrVacuous, p, got)
		}
		if got != want {
			return fmt.Errorf("%w: region %#02x's sub-opcode reader is %q in the authority, recorded "+
				"as %q — if upstream changed it, the record and the reasoning at subOpcodeReaders both "+
				"need re-reading, because that reasoning is about which of two readings to take",
				ErrVacuous, p, got, want)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(subOpcodeReaders)) {
		if !slices.Contains(t.Regions, p) {
			return fmt.Errorf("%w: subOpcodeReaders records region %#02x, which the composed table "+
				"does not have", ErrVacuous, p)
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

// multiEncodingJoinKeys is every join key that names more than one encoding, with its exact
// code count and the reason.
//
// # This started life as a uniqueness assertion and the core pin falsified it immediately
//
// The first version asserted that (mnemonic, operator) identifies one encoding. It does not,
// and never did: `select None` at 0x1b and `select (Some ts)` at 0x1c are two encodings of one
// instruction, distinguished by an argument this extractor drops exactly as it dropped the Rmw
// operator. `opgen`'s `OpsOf` returns a *slice* of codes per constructor for that reason, so
// the model was right and the new assertion was wrong — a control aimed at a property the tree
// deliberately does not have. (Found by running it: it failed on `select` before it ever saw
// the atomics region.)
//
// So the property worth asserting is not uniqueness but *accounting*: a join key naming several
// encodings is a fact someone has read, and an unread one is the failure. Keyed by content —
// `mnemonic/operator` — rather than by a count or a delta, so an entry cannot carry its
// justification onto a different subject.
//
// **It would have caught what motivated the whole detour.** Before `Arm.Operator`, the atomics
// region put 42 codes under 7 keys: seven new entries here, each claiming six encodings of what
// are six different instructions. Those seven would have been wrong to admit; the three below are
// not, and the difference is a criterion rather than a judgement call.
//
// # The admission rule: one *wat* name, or capture the discriminator
//
// An entry belongs here when the text grammar also has one name for the several encodings, so
// that the ambiguity is the format's and not this extractor's. All three entries pass it, and
// each carries its discriminator in an operand the text grammar reads separately:
//
//   - `REF_TEST reftype` / `REF_CAST reftype` (text/parser.mly:616-617) — one keyword, and the
//     *reftype's own nullability* picks between the two opcodes.
//   - `SELECT selectinstr_results_instr_list` (parser.mly:678) — one keyword, the optional
//     result list picking between them.
//
// The atomics 42 fail it: `i64.atomic.rmw32.xor_u` and `i64.atomic.rmw32.add_u` are two
// *keywords* in the lexer, so one join key answering to both was this extractor dropping a
// distinction the format makes. That is the case where the rule says capture, and Arm.Operator
// is the capture.
//
// Which is why the population here was measured rather than accumulated one error at a time: the
// check returns on the first offender, so writing an entry per failing run would have grown this
// map by whichever key sorts first and never asked how many there were. Three, in the core pin;
// one in the threads pin, which predates GC.
var multiEncodingJoinKeys = map[string]struct {
	codes int
	why   string
}{
	"select/": {2, "select None (0x1b) and select (Some ts) (0x1c) — one instruction, two " +
		"encodings, the second carrying an explicit type list (decode.ml:407-408); one wat " +
		"keyword, SELECT with an optional result list (text/parser.mly:678)"},
	"ref_test/": {2, "ref_test (NoNull, ht) at 0xfb/0x14 and (Null, ht) at 0xfb/0x15 " +
		"(decode.ml:636-637) — the nullability is the opcode, not an immediate; one wat " +
		"keyword, `REF_TEST reftype` (text/parser.mly:616)"},
	"ref_cast/": {2, "ref_cast (NoNull, ht) at 0xfb/0x16 and (Null, ht) at 0xfb/0x17 " +
		"(decode.ml:638-639) — same shape as ref_test; one wat keyword, `REF_CAST reftype` " +
		"(text/parser.mly:617)"},
}

// joinKey is decision 0014's key: the reference constructor plus the operator it is applied to.
func joinKey(a Arm) string { return a.Mnemonic + "/" + a.Operator }

// joinKeyCodes groups the table's arms by join key, skipping those that have none.
//
// Arms with no mnemonic are outside the domain rather than exceptions to it: an escape, an
// illegal arm, and a misplaced-END reporter carry no constructor, so they have no join key —
// the same reason `OpsOf` skips them.
func (t *Table) joinKeyCodes() map[string][]Arm {
	codes := map[string][]Arm{}
	for _, a := range t.Arms {
		if a.Mnemonic == "" {
			continue
		}
		codes[joinKey(a)] = append(codes[joinKey(a)], a)
	}
	return codes
}

// checkJoinKeysAreAccountedFor asserts every join key naming several encodings is a known one,
// over the keys the source *has*.
//
// One direction only, and the missing one is not an oversight: "every listed key exists" cannot
// be asked per source, because the threads pin's baseline predates GC and correctly has no
// `ref_test`. Asking it here would reproduce Floors' own bug — a map hand-scoped to the core pin,
// applied to whatever table it is handed — in the control written to replace the last one that
// made that mistake. checkJoinKeyDomain asks it where it is true.
func (t *Table) checkJoinKeysAreAccountedFor() error {
	codes := t.joinKeyCodes()
	for _, k := range slices.Sorted(maps.Keys(codes)) {
		arms := codes[k]
		known, listed := multiEncodingJoinKeys[k]
		switch {
		case len(arms) == 1 && listed:
			return fmt.Errorf("%w: join key %q is listed in multiEncodingJoinKeys as naming %d "+
				"encodings and now names 1 — the authority stopped distinguishing them, or this "+
				"extractor started; either way the listed reason is about a state of the world that "+
				"has changed (%s)", errUnrecognized, k, known.codes, known.why)
		case len(arms) == 1:
			// The ordinary case: one key, one encoding.
		case !listed:
			return fmt.Errorf("%w: join key %q names %d encodings and is not accounted for — "+
				"first at decode.ml:%d, last at :%d. That pair is decision 0014's join key, so this "+
				"is several answers to \"which opcode does this encode to\": the reference is "+
				"distinguishing them by something this extractor drops. Capture the discriminator, "+
				"or add the key to multiEncodingJoinKeys with the reason the encodings are one "+
				"instruction", errUnrecognized, k, len(arms), arms[0].Line, arms[len(arms)-1].Line)
		case len(arms) != known.codes:
			return fmt.Errorf("%w: join key %q names %d encodings, accounted for as %d (%s)",
				errUnrecognized, k, len(arms), known.codes, known.why)
		}
	}
	return nil
}

// checkJoinKeyDomain asserts every accounted-for join key is one the composed table actually has.
//
// The direction checkJoinKeysAreAccountedFor structurally cannot cover, and composed-only for
// checkFloorDomain's reason: per source the inequality is correct, since a pin whose baseline
// predates a proposal has none of its constructors. On the *composed* table there is no such
// excuse — an entry naming a key nothing produces is a justification for a collision that no
// longer happens, which is a stale reason left standing where a reader will take it for a
// current one.
func (t *Table) checkJoinKeyDomain() error {
	codes := t.joinKeyCodes()
	for _, k := range slices.Sorted(maps.Keys(multiEncodingJoinKeys)) {
		if len(codes[k]) == 0 {
			return fmt.Errorf("%w: multiEncodingJoinKeys accounts for join key %q, which the "+
				"composed table does not have — the constructor was renamed or removed upstream, and "+
				"the entry's stated reason is now about nothing", errUnrecognized, k)
		}
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

// reOperatorArg matches an operator constructor passed to an instruction constructor:
// `i64_atomic_rmw32_u (I64 I64Op.RmwXor) a o` (spec-threads/binary/decode.ml:840).
//
// The numeric-type prefix is spelled out rather than `\w+` because the shape being recognized
// is `Values`' tagged operator — the `let open Values in` the atomics region opens with — and a
// looser pattern would also claim any parenthesised qualified name, of which the reference has
// several that are not operators (`Mnemonics.catch`).
var reOperatorArg = regexp.MustCompile(`\(\s*(?:I32|I64|F32|F64)\s+(?:I32|I64|F32|F64)Op\.(\w+)\s*\)`)

// operatorOf returns the operator constructor an arm applies, or "" where it applies none.
//
// **Zero core arms have one** (counted at bdd7164), which is why nothing needed this until the
// atomics region arrived and why adding it moves no existing row in the generated table.
func operatorOf(rhs string) string {
	if m := reOperatorArg.FindStringSubmatch(rhs); m != nil {
		return m[1]
	}
	return ""
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
