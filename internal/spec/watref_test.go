// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// updateWatRef regenerates the committed reference corpus. Off by default: `wat2wasm` is an external,
// non-Go tool and the whole point of committing its output is that the runtime path never calls it.
//
// This is the **wabt precedent** (ruling, #67 review): commit what the oracle produced, with its version and a
// regeneration path recorded, and do not depend on the oracle at test time.
var updateWatRef = flag.Bool("update-watref", false, "regenerate internal/spec/testdata/watref from wat2wasm")

const (
	watRefDir      = "testdata/watref"
	watRefManifest = watRefDir + "/manifest.txt"
)

// watRefName is the reference file for one must-succeed module, keyed by the suite file and the module's line.
//
// Keyed by LINE and not by ordinal deliberately: an ordinal renumbers every entry below an inserted module when
// the corpus pin moves, so a one-module upstream addition would rewrite thousands of files and make the diff
// unreadable. A line also renumbers, but only within its own file, and it is the key the harness's own failures
// already carry (`Failure.Line`), so a mismatch names something a reader can open.
func watRefName(file string, line int) string {
	return fmt.Sprintf("%s.%d.wasm", strings.TrimSuffix(file, ".wast"), line)
}

// mustSucceedKind is the population: modules the suite expects to load.
//
// `KindAssertTrapModule` is excluded — that module is expected to trap at instantiation, so it is an
// expected-failure vector. `KindModuleBinary` is excluded because it never goes through the encoder, so it has
// no bridge to check. Both exclusions are the probe's, carried forward unchanged so the witness's population is
// the probe's population.
// **Exhaustive on purpose, and the linter asking for it is right.** With a default arm, a Kind added upstream
// would fall silently into "not must-succeed" and the control's population would shrink without anyone deciding
// it should — *derive the domain, do not list today's cases*. Listing every Kind makes the next addition a
// question somebody has to answer.
//
// **What enforces it is the `exhaustive` linter in the LINT GATE, not the compiler.** Go has no exhaustiveness
// check of its own, so `go build` and `go vet` will both accept an unlisted Kind here. A reader who expects the
// compiler to catch it would be wrong, and would be wrong in the direction of trusting a check that is not
// running.
func mustSucceedKind(k Kind) bool {
	switch k {
	// The three that carry text the encoder must bridge.
	case KindModuleText, KindModuleQuote, KindModuleDefinition:
		return true

	// A module, but not through the encoder: the binary form has no text to bridge, and an instance
	// instantiates a definition that was already checked under its own Kind.
	case KindModuleBinary, KindModuleInstance:
		return false

	// Modules the suite expects to FAIL. A mutation flipping one of these is half 1's question — does it
	// reject what it should — not half 2's. `KindAssertTrapModule` belongs here too: it is expected to trap
	// at instantiation, so it is an expected-failure vector and neither half reads cleanly from it.
	case KindAssertMalformed, KindAssertMalformedText,
		KindAssertInvalid, KindAssertInvalidBinary, KindAssertInvalidQuote,
		KindAssertUnlinkable, KindAssertTrapModule:
		return false

	// Not modules at all — assertions and actions over a module already instantiated.
	case KindAssertReturn, KindNamedAssertReturn,
		KindAssertTrapAction, KindNamedAssertTrap,
		KindInvoke, KindNamedInvoke,
		KindAssertException, KindAssertExhaustion,
		KindRegister, KindUnsupported:
		return false
	}
	return false
}

type watRefEntry struct {
	File   string
	Line   int
	Status string // "ok" or "no-reference: <reason>"
}

// regenerateWatRef rewrites the committed reference corpus from wat2wasm.
//
// **A helper called by the control, not a test of its own, and that shape is the gate-census precedent**
// (`TestGateCensusIsClassifiedArmByArm`: one test that writes under `-update-census` and compares otherwise). A
// separate generator test would have to SKIP on every ordinary run, and a skip is not a verdict — this tree has
// a control that refuses unlicensed skip sites, and it would be right to.
func regenerateWatRef(t *testing.T) {
	t.Helper()
	w2w, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Fatalf("wat2wasm is required to REGENERATE the reference (never to read it): %v", err)
	}
	verOut, _ := exec.Command(w2w, "--version").Output()
	version := strings.TrimSpace(string(verOut))

	if err := os.RemoveAll(watRefDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(watRefDir, 0o750); err != nil {
		t.Fatal(err)
	}

	var entries []watRefEntry
	for _, f := range boardFiles(t) {
		s, perr := ParseFile(filepath.Join(suiteDir, f))
		if perr != nil {
			continue
		}
		for _, c := range s.Commands {
			if !mustSucceedKind(c.Kind) || len(c.Source) == 0 {
				continue
			}
			tmp := filepath.Join(t.TempDir(), "m.wat")
			if werr := os.WriteFile(tmp, c.Source, 0o600); werr != nil {
				t.Fatal(werr)
			}
			out := filepath.Join(watRefDir, watRefName(f, c.Line))
			// **NOT `--enable-all`, and the reason is measured.** `--enable-all` switches on
			// `compact-imports`, which **re-encodes the import section** — wabt then cannot read back its
			// own output (`wasm2wat`: *"module uses compact imports, but feature not enabled"*), and our
			// decoder reported `malformed import kind: 0x7f` on 79 modules. That read exactly like a
			// decoder gap or a bridge defect and was neither: it was this flag.
			//
			// A broad flag is not a neutral "accept more" — it can change the artifact. So the set is
			// named: wabt's defaults already cover SIMD, bulk-memory, reference-types, multi-value,
			// tail-call, memory64, multi-memory, extended-const and relaxed-simd, and these three are the
			// off-by-default proposals this corpus actually uses. `compact-imports`, `code-metadata`,
			// `wide-arithmetic` and `custom-page-sizes` stay OFF: experimental, and the first three change
			// or add encodings. Anything needing them lands in the no-reference list with its reason.
			cmd := exec.Command(w2w,
				"--enable-threads", "--enable-function-references", "--enable-gc",
				"-o", out, tmp)
			if combined, rerr := cmd.CombinedOutput(); rerr != nil {
				reason := strings.TrimSpace(string(combined))
				if i := strings.IndexByte(reason, '\n'); i > 0 {
					reason = reason[:i]
				}
				reason = strings.ReplaceAll(reason, tmp, "<module>")
				entries = append(entries, watRefEntry{f, c.Line, "no-reference: " + reason})
				continue
			}
			entries = append(entries, watRefEntry{f, c.Line, "ok"})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].File != entries[j].File {
			return entries[i].File < entries[j].File
		}
		return entries[i].Line < entries[j].Line
	})

	var b strings.Builder
	ok, missing := 0, 0
	for _, e := range entries {
		if e.Status == "ok" {
			ok++
		} else {
			missing++
		}
	}
	// The manifest is the DENOMINATOR, committed. A module with no reference is listed by name with the
	// reason and counted — never skipped silently, because a selector that quietly drops what it cannot
	// encode produces a control whose population shrinks without anyone deciding it should.
	fmt.Fprintf(&b, "# wat2wasm reference corpus for the text->binary bridge (#67 half 2).\n")
	fmt.Fprintf(&b, "# Generated by `make watref`; DO NOT EDIT. The runtime path never calls wat2wasm:\n")
	fmt.Fprintf(&b, "# this file and the .wasm beside it ARE the oracle's reading, committed.\n")
	fmt.Fprintf(&b, "# tool: %s\n", version)
	fmt.Fprintf(&b, "# suite: %s\n", suitePin(t))
	fmt.Fprintf(&b, "# must-succeed modules: %d  with reference: %d  without: %d\n", len(entries), ok, missing)
	fmt.Fprintf(&b, "# columns: file<TAB>line<TAB>status\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\t%d\t%s\n", e.File, e.Line, e.Status)
	}
	if err := os.WriteFile(watRefManifest, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("WATREF: %d must-succeed modules, %d with a reference, %d without (tool %s)",
		len(entries), ok, missing, version)
}

// TestEncodedModulesMatchTheReference is #67's half 2: for every must-succeed suite module with a committed
// reference, the bytes `text.EncodeModule` emits **decode to the same module** wat2wasm's bytes decode to.
//
// # Why decoded modules and not bytes
//
// Byte equality would flag legitimate encoding choices — LEB padding, section ordering latitude, a `memarg`
// alignment written differently — none of which changes the module. Comparing after `binary.DecodeModule`
// compares *what the bytes mean*, and the decoder is independently checked by the suite's own binary vectors, so
// it is not the thing under test here.
//
// # Why this exists when the board already runs every one of these modules
//
// The probe measured it. A changed constant went undetected in **75%** of the 1,342 modules it touched, and a
// flipped data byte in **92%** of 72 — behind vector counts of 6,127 and 32 that looked like saturation. The
// board is a **behavioural** comparator: it sees a wrong module only where an assertion happens to exercise the
// difference. Two mutation classes that preserve behaviour — a local's type, export order — were detected **0**
// times out of 19 and 7 modules.
func TestEncodedModulesMatchTheReference(t *testing.T) {
	requireSuite(t)
	if *updateWatRef {
		// `make watref`. Regenerating and then comparing in ONE run means the regeneration is never
		// trusted on its own: a reference nobody immediately compared against is a reference nobody has
		// checked.
		regenerateWatRef(t)
	}
	raw, err := os.ReadFile(watRefManifest)
	if err != nil {
		t.Fatalf("the committed reference manifest is missing: %v\n"+
			"Regenerate with `make watref` (needs wat2wasm). The control reads the committed bytes and "+
			"never calls the tool.", err)
	}

	type key struct {
		file string
		line int
	}
	status := map[key]string{}
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		parts := strings.Split(ln, "\t")
		if len(parts) != 3 {
			t.Fatalf("manifest line is not three tab-separated fields: %q", ln)
		}
		n, convErr := strconv.Atoi(parts[1])
		if convErr != nil {
			t.Fatalf("manifest line has a non-numeric line field: %q", ln)
		}
		status[key{parts[0], n}] = parts[2]
	}

	compared, mismatched, noRef, unseen := 0, 0, 0, 0
	feats := allFeaturesOn(t)

	for _, f := range boardFiles(t) {
		s, perr := ParseFile(filepath.Join(suiteDir, f))
		if perr != nil {
			continue
		}
		for _, c := range s.Commands {
			if !mustSucceedKind(c.Kind) || len(c.Source) == 0 {
				continue
			}
			st, known := status[key{f, c.Line}]
			if !known {
				// The manifest and the suite disagree about the population. That is a real failure, not a
				// skip: it means the corpus pin moved under a committed reference.
				unseen++
				continue
			}
			if st != "ok" {
				noRef++
				continue
			}
			refBytes, rerr := os.ReadFile(filepath.Join(watRefDir, watRefName(f, c.Line)))
			if rerr != nil {
				t.Errorf("%s:%d manifest says ok but the reference is unreadable: %v", f, c.Line, rerr)
				continue
			}
			ourBytes, eerr := text.EncodeModule(c.Source)
			if eerr != nil {
				t.Errorf("%s:%d our encoder refused a must-succeed module: %v", f, c.Line, eerr)
				continue
			}
			d := &binary.Decoder{Features: feats}
			refMod, derr := d.DecodeModule(refBytes)
			if derr != nil {
				// wat2wasm emitted something our decoder rejects. Reported, not silently dropped: it is
				// either a decoder gap or a reference we should not have recorded as ok.
				t.Errorf("%s:%d our decoder rejected the REFERENCE bytes: %v", f, c.Line, derr)
				continue
			}
			ourMod, derr2 := d.DecodeModule(ourBytes)
			if derr2 != nil {
				t.Errorf("%s:%d our decoder rejected OUR OWN bytes: %v", f, c.Line, derr2)
				continue
			}
			compared++
			if diff := diffModules(refMod, ourMod); diff != "" {
				mismatched++
				if mismatched <= 20 {
					t.Errorf("%s:%d our encoding denotes a DIFFERENT module than wat2wasm's: %s",
						f, c.Line, diff)
				}
			}
		}
	}

	if unseen > 0 {
		t.Errorf("%d must-succeed modules are in the suite but not in the manifest — the corpus pin moved "+
			"under the committed reference. Regenerate with `make watref`.", unseen)
	}
	// Vacuity: a comparison over an empty set agrees with anything. The floor sits BELOW the measured
	// population so adding suite modules is not a failure, and is moved in the PR that moves the corpus pin.
	const floor = 1800
	if compared < floor {
		t.Fatalf("compared only %d modules, want at least %d — the selector or the manifest has emptied, "+
			"and a structural comparison over nothing passes", compared, floor)
	}
	t.Logf("WATREF COMPARE: %d modules compared, %d mismatched, %d with no reference (listed in the manifest)",
		compared, mismatched, noRef)
}

// diffModules names the FIRST structural difference between two decoded modules, or "" when they denote the
// same module.
//
// Field-by-field with `reflect.DeepEqual` rather than one DeepEqual over the whole struct, for the reason a
// bare boolean comparison is weak evidence: a control that says only "they differ" sends the reader to diff two
// binaries by hand, and the field name is most of the diagnosis. `Sections` is deliberately EXCLUDED — it is
// the raw section layout, where legitimate encoding latitude lives (ordering, padding), and comparing it would
// reintroduce exactly the byte-equality problem this control exists to avoid.
func diffModules(a, b *binary.Module) string {
	for _, f := range []struct {
		name string
		x, y any
	}{
		{"Types", a.Types, b.Types},
		{"Imports", a.Imports, b.Imports},
		{"Funcs", canonFuncs(a), canonFuncs(b)},
		{"Tables", a.Tables, b.Tables},
		{"Memories", a.Memories, b.Memories},
		{"Globals", a.Globals, b.Globals},
		{"Exports", a.Exports, b.Exports},
		{"Tags", a.Tags, b.Tags},
		{"Elems", a.Elems, b.Elems},
		{"Datas", a.Datas, b.Datas},
		{"Start", [2]any{a.Start, a.HasStart}, [2]any{b.Start, b.HasStart}},
	} {
		if !reflect.DeepEqual(f.x, f.y) {
			return f.name + " differs (reference vs ours)"
		}
	}
	return ""
}

// canonFunc is a function in a form where **encoding latitude is removed and meaning is kept**, so the
// comparison is about the module rather than about how it was spelled.
//
// `EndsOff` is deliberately absent: it is the block-pairing arena offset the decoder assigns, i.e. derived
// build state rather than anything the module says, and comparing it would compare an implementation detail of
// whichever decode ran first.
type canonFunc struct {
	TypeIndex uint32
	Locals    []binary.LocalGroup
	Body      []canonInstr
}

// canonInstr keeps an instruction's opcode and immediates, except that a **blocktype becomes the signature it
// denotes**.
type canonInstr struct {
	Op         uint32
	Prefix     byte
	Imm0, Imm1 uint64
	Sig        string // set only for the four structural ops; "" elsewhere
}

// canonFuncs is why "compare decoded modules, not raw bytes" is necessary but **not sufficient**.
//
// Comparing decoded modules removes LEB padding and section-ordering latitude. It does not remove latitude the
// decoded form RETAINS — and a blocktype is exactly that. `block (result i32)` may be spelled as the
// single-valtype shorthand `0x7f` or as a type index naming a functype `[] -> [i32]`, and `instr.go`'s packing
// keeps the two apart: a type index is `(i, 0)`, empty is `blockTypeEmpty`, a valtype is `blockTypeValType|kind`.
//
// **Measured, not anticipated.** The first run of this control reported three mismatches — `block.wast:3`,
// `if.wast:3`, `loop.wast:3` — where wat2wasm used the **empty** and **i32** shorthands and our encoder used
// **type indices 1 and 2**. Same block types, different spellings, and the comparison called it a different
// module. That is the byte-equality false positive one level up, which is the whole reason this exists.
//
// Scope: the four ops that carry a blocktype — `block` 0x02, `loop` 0x03, `if` 0x04, `try_table` 0x1f, each at
// prefix 0, read from the committed gate census rather than assumed.
func canonFuncs(m *binary.Module) []canonFunc {
	out := make([]canonFunc, len(m.Funcs))
	for i, fn := range m.Funcs {
		cf := canonFunc{TypeIndex: fn.TypeIndex, Locals: fn.Locals, Body: make([]canonInstr, len(fn.Body))}
		for j, in := range fn.Body {
			ci := canonInstr{Op: in.Op, Prefix: in.Prefix, Imm0: in.Imm0, Imm1: in.Imm1}
			if in.Prefix == 0 && (in.Op == 0x02 || in.Op == 0x03 || in.Op == 0x04 || in.Op == 0x1f) {
				idx, vt, empty := binary.BlockType(in.Imm0, in.Imm1)
				var params, results []binary.ValType
				switch {
				case empty:
					// [] -> []
				case vt != (binary.ValType{}):
					// `BlockType` returns a zero `ValType` on both the empty and the type-index arms,
					// so a non-zero one is what identifies the single-valtype shorthand. Kind 0 is not
					// a valtype, which is why the zero value is free to mean "not this arm".
					results = []binary.ValType{vt}
				default:
					if int(idx) < len(m.Types) && m.Types[idx].Kind == binary.CompFunc {
						params = m.Types[idx].Func.Params
						results = m.Types[idx].Func.Results
					}
				}
				ci.Imm0, ci.Imm1, ci.Sig = 0, 0, sigKey(params, results)
			}
			cf.Body[j] = ci
		}
		out[i] = cf
	}
	return out
}

func sigKey(params, results []binary.ValType) string {
	var b strings.Builder
	for _, p := range params {
		fmt.Fprintf(&b, "%v,", p)
	}
	b.WriteByte('>')
	for _, r := range results {
		fmt.Fprintf(&b, "%v,", r)
	}
	return b.String()
}

// --- the control's falsification, committed ------------------------------------------------------------------

var (
	reOffsetM = regexp.MustCompile(`offset=(\d+)`)
	reConstM  = regexp.MustCompile(`(i32|i64)\.const (-?\d+)`)
	reLocalM  = regexp.MustCompile(`\(local i32\)`)
	reExportM = regexp.MustCompile(`\(export "([^"]*)" \(func ([^)]*)\)\)`)
	reDataM   = regexp.MustCompile(`\(data([^"]*)"([^"]+)"`)
)

// mutateSource applies one wrong-module mutation, keeping the text well-formed. Returns false when the class
// has no subject in this module — which is why the witness reports a denominator and not just a rate.
//
// These are the six classes the #67 probe used, carried over unchanged so the control is falsified against the
// same population that motivated it.
func mutateSource(class int, src []byte) ([]byte, bool) {
	s := string(src)
	switch class {
	case 1: // a called function returns another's result
		m := reExportM.FindAllStringSubmatchIndex(s, 2)
		if len(m) < 2 {
			return src, false
		}
		ga := reExportM.FindStringSubmatch(s[m[0][0]:m[0][1]])
		gb := reExportM.FindStringSubmatch(s[m[1][0]:m[1][1]])
		if ga[2] == gb[2] {
			return src, false
		}
		na := `(export "` + ga[1] + `" (func ` + gb[2] + `))`
		nb := `(export "` + gb[1] + `" (func ` + ga[2] + `))`
		return []byte(s[:m[0][0]] + na + s[m[0][1]:m[1][0]] + nb + s[m[1][1]:]), true
	case 2: // a different address is accessed
		loc := reOffsetM.FindStringSubmatchIndex(s)
		if loc == nil {
			return src, false
		}
		v, _ := strconv.Atoi(s[loc[2]:loc[3]])
		return []byte(s[:loc[2]] + strconv.Itoa(v+1) + s[loc[3]:]), true
	case 3: // a different value propagates
		loc := reConstM.FindStringSubmatchIndex(s)
		if loc == nil {
			return src, false
		}
		v, err := strconv.ParseInt(s[loc[4]:loc[5]], 10, 64)
		if err != nil {
			return src, false
		}
		return []byte(s[:loc[4]] + strconv.FormatInt(v+1, 10) + s[loc[5]:]), true
	case 4: // behaviour-PRESERVING: a local's type, where it stays valid
		loc := reLocalM.FindStringIndex(s)
		if loc == nil {
			return src, false
		}
		return []byte(s[:loc[0]] + `(local i64)` + s[loc[1]:]), true
	case 5: // a read of that address differs
		loc := reDataM.FindStringSubmatchIndex(s)
		if loc == nil {
			return src, false
		}
		body := s[loc[4]:loc[5]]
		if body == "" || strings.Contains(body, `\`) {
			return src, false
		}
		b := []byte(body)
		b[0] ^= 0x01
		return []byte(s[:loc[4]] + string(b) + s[loc[5]:]), true
	case 6: // behaviour-PRESERVING: export order
		m := reExportM.FindAllStringSubmatchIndex(s, 2)
		if len(m) < 2 {
			return src, false
		}
		a, b := s[m[0][0]:m[0][1]], s[m[1][0]:m[1][1]]
		if a == b {
			return src, false
		}
		return []byte(s[:m[0][0]] + b + s[m[0][1]:m[1][0]] + a + s[m[1][1]:]), true
	}
	return src, false
}

// TestWatRefControlDetectsWrongModules is the falsification of TestEncodedModulesMatchTheReference, and it is
// committed rather than run once: a control nobody can re-break is a control nobody can re-trust.
//
// # The bar, and where it came from
//
// **≥99% of mutated modules that have a reference must be detected, for every class** (ruling, #67 review). Set
// against what the board managed on the same classes, measured: a changed constant went undetected in **75%** of
// the 1,342 modules it touched, a flipped data byte in **92%** of 72, and the two behaviour-preserving classes
// were detected **0** times out of 19 and 7.
//
// **Classes 4 and 6 are the load-bearing ones.** They change structure and not behaviour, so the board could not
// see them in principle. If this control detects them, it is comparing structure — which is the whole claim.
func TestWatRefControlDetectsWrongModules(t *testing.T) {
	requireSuite(t)
	raw, err := os.ReadFile(watRefManifest)
	if err != nil {
		// Fatal, not Skip. The manifest is COMMITTED, so its absence is a broken tree rather than an
		// unavailable input — and a skip here would delete this control's falsification silently.
		t.Fatalf("the committed reference manifest is missing: %v — regenerate with `make watref`", err)
	}
	type key struct {
		file string
		line int
	}
	ok := map[key]bool{}
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		p := strings.Split(ln, "\t")
		if len(p) == 3 && p[2] == "ok" {
			n, _ := strconv.Atoi(p[1])
			ok[key{p[0], n}] = true
		}
	}
	feats := allFeaturesOn(t)
	files := boardFiles(t)

	for class := 1; class <= 6; class++ {
		mutated, detected := 0, 0
		for _, f := range files {
			s, perr := ParseFile(filepath.Join(suiteDir, f))
			if perr != nil {
				continue
			}
			for _, c := range s.Commands {
				if !mustSucceedKind(c.Kind) || len(c.Source) == 0 || !ok[key{f, c.Line}] {
					continue
				}
				src, changed := mutateSource(class, c.Source)
				if !changed {
					continue
				}
				ourBytes, eerr := text.EncodeModule(src)
				if eerr != nil {
					// The mutation made the text unencodable: no subject for half 2 here.
					continue
				}
				d := &binary.Decoder{Features: feats}
				ourMod, derr := d.DecodeModule(ourBytes)
				if derr != nil {
					// Ill-formed bytes are half 1's question, and half 1 is the board's already.
					continue
				}
				refBytes, rerr := os.ReadFile(filepath.Join(watRefDir, watRefName(f, c.Line)))
				if rerr != nil {
					continue
				}
				refMod, rderr := d.DecodeModule(refBytes)
				if rderr != nil {
					continue
				}
				mutated++
				if diffModules(refMod, ourMod) != "" {
					detected++
				}
			}
		}
		if mutated == 0 {
			t.Errorf("class %d mutated 0 modules — the class has no subject and its rate would be vacuous",
				class)
			continue
		}
		pct := float64(detected) / float64(mutated) * 100
		t.Logf("WATREF FALSIFY class=%d mutated=%d detected=%d (%.1f%%)", class, mutated, detected, pct)
		if pct < 99.0 {
			t.Errorf("class %d: %d/%d = %.1f%% detected, want >=99%% — a wrong module this control cannot "+
				"see is the gap #67 exists to close", class, detected, mutated, pct)
		}
	}
}
