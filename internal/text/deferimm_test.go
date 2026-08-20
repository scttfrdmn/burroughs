package text

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen/xcorpus"
)

// Grave #130's control: an immediate whose index name is bound *after* the instruction that uses
// it must encode, and must encode to the same image as the same module written the other way
// round.
//
// # The defect this exists to catch is invisible to the corpus
//
// `module_fields` admits any field order, so `(module (func (data.drop $d)) (data $d "x"))` is a
// valid module — the section order constrains the *image*, not the text. Resolving a symbolic
// index at the cursor rejected every such module and accepted its mirror, and the spec suite
// happens to write declarations first almost everywhere: `memory-multi.wast` is the corpus's
// single witness across all of these categories. A category with no board vector is certified by
// nothing but this file, which is why the shape of the check here matters more than its length.
//
// # Two legs, because arms of one mechanism share its faults
//
// The differential leg — decl-first versus use-first, byte-for-byte — is necessary and not
// sufficient. Both arms run the same encoder, so a single defect moves both and they agree
// *exactly* the way two empty sets agree (`docs/laws/controls.md`, the vacuity family's third
// specimen). This was not hypothetical: an earlier scratch patch wrote `fc 08 00` where the wire
// form is `fc 08 00 00`, and a both-orders probe reported "identical" over the truncated image in
// every row. So every row also carries an **absolute** leg: an expected image whose bytes this
// package did not produce, or whose meaning a foreign runtime confirmed.
//
// # The two authorities, and why there are two
//
//   - [authorityWabt] — the image is `wat2wasm --enable-all` output under the wabt pinned in
//     `testdata/xcorpus/manifest.json`, generated once and committed here, the same standing as a
//     committed fuzz crasher (ADR 0011's second appendix: wabt is a generator, never a gate).
//     Eight rows. The reference read the *use-first* source too, which is independent
//     confirmation that these modules are legal text and not merely bytes we like.
//
//   - [authorityWasmtime] — three rows are GC, and the pinned wabt has GC *types* but no GC
//     *instructions*: `wat2wasm --enable-gc` answers `unexpected token "struct.get"`. For those
//     the expected image is a pin of our own output, so the leg's independence is not in the
//     bytes' origin — it is in a recorded observation that the bytes denote the intended module.
//     Each of the three exports `f` returning `i32`, and `wasmtime run --invoke f` returned 7.
//
// **Scope, for those three rows: the leg certifies *meaning*, not byte-identity.** What is pinned is
// that these bytes are *a* correct encoding of the intended module, never that they are the
// canonical one — a foreign encoder choosing a different legal spelling is outside what wasmtime
// running the image can say. The eight wabt rows do assert byte-identity, and reading the three as
// if they did is the over-read this line exists to stop.
//
// **And the substitution is self-retiring** ([TestDeferredImmReferenceIsStillThePinnedWabt]): when
// the pin moves to a wabt that parses GC instructions, these three rows take the byte leg and
// `authorityWasmtime` goes. The mechanism is that the pin's own version is asserted here, so a
// toolchain bump — its own gated PR — cannot land without this file failing and naming the move.
// Carved out the way ADR 0025 carves out, and for its reason: a carve-out with no stated end
// condition becomes permanent by nobody deciding anything.
//
// The wasmtime leg is the weaker of the two and its weakness has a name: a pin preserves whatever
// error was present when it was taken. What bounds that is that the observation discriminates.
// Each GC row was mutated at the byte the deferral writes and re-run, and each mutant was caught:
// `struct.get`'s field index 01→00 returned 1 instead of 7; its type index 01→00 was rejected
// (`type mismatch: expected (ref null $type), found (ref $type)`); `br_on_cast`'s destination type
// index 00→01 was rejected (`expected (ref $type), found anyref`). A leg that cannot fail is not a
// leg, so that is the part that had to be watched rather than argued
// (`docs/laws/controls.md`, "a control isn't born until it's watched die").
const (
	authorityWabt     = "wabt 1.0.41, wat2wasm --enable-all"
	authorityWasmtime = "wasmtime 47.0.3, run --invoke f == 7"
)

// deferredImmRow is one index space's use-before-definition module, written both ways.
//
// `site` names the function whose deferral the row reaches, and it is a *claim* — a reader checks
// it, no mechanism here does. What is machine-checked is the other direction: that every
// deferring function in the package is named by some row
// ([TestDeferredImmediateSitesAreAllNamed]). Stating which half is derived matters, because the
// claim that a control covers a site is exactly the claim grave #130 got wrong.
type deferredImmRow struct {
	name      string
	site      string
	declFirst string
	useFirst  string
	wantHex   string
	authority string
}

var deferredImmRows = []deferredImmRow{{
	// The one row that was already green before grave #130's repair: this deferral is #77's, and
	// the row is here as its regression guard and to reach `retainIdxIn`'s *other* arm. Said
	// plainly because a control run against the unfixed tree failed 10 of these 11, and a reader
	// who assumes all 11 witness #130 would over-read what the eleventh proves.
	name:      "local index behind a later typeuse",
	site:      "retainIdxIn",
	declFirst: `(module (type $t (func (param i32 i32))) (func (type $t) (local $v i32) (drop (local.get $v))))`,
	useFirst:  `(module (func (type $t) (local $v i32) (drop (local.get $v))) (type $t (func (param i32 i32))))`,
	wantHex:   "0061736d0100000001060160027f7f00030201000a09010701017f20021a0b",
	authority: authorityWabt,
}, {
	name:      "catch clause tag",
	site:      "handlerClauses",
	declFirst: `(module (tag $t) (func (try_table (catch $t 0))))`,
	useFirst:  `(module (func (try_table (catch $t 0))) (tag $t))`,
	wantHex:   "0061736d01000000010401600000030201000d030100000a0b0109001f40010000000b0b",
	authority: authorityWabt,
}, {
	name:      "memory.init sugar's data index",
	site:      "retainIdxIn",
	declFirst: `(module (memory 1) (data $d "x") (func (memory.init $d (i32.const 0) (i32.const 0) (i32.const 1))))`,
	useFirst:  `(module (memory 1) (func (memory.init $d (i32.const 0) (i32.const 0) (i32.const 1))) (data $d "x"))`,
	wantHex:   "0061736d010000000104016000000302010005030100010c01010a0e010c00410041004101fc0800000b0b0401010178",
	authority: authorityWabt,
}, {
	name:      "memarg memory index",
	site:      "retainMemarg",
	declFirst: `(module (memory $m 1) (func (drop (i32.load $m (i32.const 0)))))`,
	useFirst:  `(module (func (drop (i32.load $m (i32.const 0)))) (memory $m 1))`,
	wantHex:   "0061736d010000000104016000000302010005030100010a0a01080041002802001a0b",
	authority: authorityWabt,
}, {
	name:      "global index",
	site:      "retainIdxIn",
	declFirst: `(module (global $g i32 (i32.const 0)) (func (drop (global.get $g))))`,
	useFirst:  `(module (func (drop (global.get $g))) (global $g i32 (i32.const 0)))`,
	wantHex:   "0061736d01000000010401600000030201000606017f0041000b0a0701050023001a0b",
	authority: authorityWabt,
}, {
	name:      "table index",
	site:      "retainIdxIn",
	declFirst: `(module (table $t 1 funcref) (func (drop (table.get $t (i32.const 0)))))`,
	useFirst:  `(module (func (drop (table.get $t (i32.const 0)))) (table $t 1 funcref))`,
	wantHex:   "0061736d01000000010401600000030201000404017000010a09010700410025001a0b",
	authority: authorityWabt,
}, {
	name:      "elem index",
	site:      "retainIdxIn",
	declFirst: `(module (table 1 funcref) (elem $e func) (func (elem.drop $e)))`,
	useFirst:  `(module (table 1 funcref) (func (elem.drop $e)) (elem $e func))`,
	wantHex:   "0061736d01000000010401600000030201000404017000010904010100000a07010500fc0d000b",
	authority: authorityWabt,
}, {
	name:      "data index",
	site:      "retainIdxIn",
	declFirst: `(module (memory 1) (data $d "x") (func (data.drop $d)))`,
	useFirst:  `(module (memory 1) (func (data.drop $d)) (data $d "x"))`,
	wantHex:   "0061736d010000000104016000000302010005030100010c01010a07010500fc09000b0b0401010178",
	authority: authorityWabt,
}, {
	// Two struct types, so a type index resolved one slot off reads an i64 field where the
	// signature promises i32 — which is what made the mutant detectable rather than merely wrong.
	name:      "struct.get's type index",
	site:      "retainIdxIn",
	declFirst: `(module (type $r (struct (field i64))) (type $s (struct (field i32))) (func (export "f") (result i32) (struct.get $s 0 (struct.new $s (i32.const 7)))))`,
	useFirst:  `(module (func (export "f") (result i32) (struct.get $s 0 (struct.new $s (i32.const 7)))) (type $r (struct (field i64))) (type $s (struct (field i32))))`,
	wantHex:   "0061736d01000000010d035f017e005f017f006000017f03020102070501016600000a0d010b004107fb0001fb0201000b",
	authority: authorityWasmtime,
}, {
	// Two fields holding different values, for the same reason: the field index is the thing under
	// test, so the two slots must be distinguishable by the observation.
	name:      "struct.get's field name",
	site:      "structFieldPairRetained",
	declFirst: `(module (type $s (struct (field $x i32) (field $y i32))) (func (export "f") (result i32) (struct.get $s $y (struct.new $s (i32.const 1) (i32.const 7)))))`,
	useFirst:  `(module (func (export "f") (result i32) (struct.get $s $y (struct.new $s (i32.const 1) (i32.const 7)))) (type $s (struct (field $x i32) (field $y i32))))`,
	wantHex:   "0061736d01000000010b025f027f007f006000017f03020101070501016600000a0f010d0041014107fb0000fb0200010b",
	authority: authorityWasmtime,
}, {
	name:      "br_on_cast's heap type",
	site:      "retainHeapTypeImm",
	declFirst: `(module (type $s (struct (field i32))) (func (export "f") (result i32) (struct.get $s 0 (block $l (result (ref $s)) (br_on_cast $l anyref (ref $s) (struct.new $s (i32.const 7))) (unreachable)))))`,
	useFirst:  `(module (func (export "f") (result i32) (struct.get $s 0 (block $l (result (ref $s)) (br_on_cast $l anyref (ref $s) (struct.new $s (i32.const 7))) (unreachable)))) (type $s (struct (field i32))))`,
	wantHex:   "0061736d010000000109025f017f006000017f03020101070501016600000a180116000264004107fb0000fb1801006e00000bfb0200000b",
	authority: authorityWasmtime,
}}

// TestDeferredImmediatesSurviveEitherFieldOrder runs both legs over every row.
func TestDeferredImmediatesSurviveEitherFieldOrder(t *testing.T) {
	// The exact row count, not a floor. A floor catches a deleted file and nothing smaller, and
	// what would go wrong here is one row quietly dropped while the rest keep the test green
	// (`docs/laws/evidence-and-instruments.md`, floors bound the catastrophic case only).
	if got := len(deferredImmRows); got != 11 {
		t.Fatalf("row count %d, want 11 — a row was added or dropped without moving this bound", got)
	}
	for _, row := range deferredImmRows {
		t.Run(row.name, func(t *testing.T) {
			want, err := hex.DecodeString(row.wantHex)
			if err != nil {
				t.Fatalf("wantHex does not decode: %v", err)
			}
			// The absolute leg is only a leg if it holds an image. A row whose reference is empty
			// would pass every comparison below against whatever this encoder happens to emit.
			if len(want) <= 8 {
				t.Fatalf("reference image is %d bytes, which is a header at most — the absolute leg is vacuous", len(want))
			}
			decl, declErr := EncodeModule([]byte(row.declFirst))
			use, useErr := EncodeModule([]byte(row.useFirst))
			if declErr != nil {
				t.Errorf("declaration-first form failed to encode: %v", declErr)
			}
			if useErr != nil {
				t.Errorf("use-before-definition form failed to encode: %v\n\tthis is grave #130's shape: a valid module refused for its field order", useErr)
			}
			if declErr != nil || useErr != nil {
				return
			}
			if !bytes.Equal(decl, use) {
				t.Errorf("the two field orders encode differently\n\tdecl-first % x\n\tuse-first  % x", decl, use)
			}
			// Against the reference last, and against *both* arms: an agreement between the arms
			// says only that they share a mechanism.
			if !bytes.Equal(use, want) {
				t.Errorf("use-before-definition image disagrees with %s\n\tgot  % x\n\twant % x", row.authority, use, want)
			}
			if !bytes.Equal(decl, want) {
				t.Errorf("declaration-first image disagrees with %s\n\tgot  % x\n\twant % x", row.authority, decl, want)
			}
		})
	}
}

// TestDeferredImmReferenceIsStillThePinnedWabt is the wasmtime substitution's end condition, made a
// mechanism instead of an intention.
//
// Three rows carry a semantic leg only because wabt 1.0.41 cannot parse GC instructions. That is a
// fact about a *pinned tool version*, so it expires, and a carve-out whose expiry nobody watches is
// how a stopgap becomes the design. The pin lives in the committed corpus's manifest, which is also
// what the eight byte-leg images were generated against — so asserting it here couples both legs to
// one recorded version and makes the toolchain bump the trigger.
//
// `wat2wasm` is **not** run: wabt is a generator, never a gate (ADR 0011), and a test that shelled
// out would skip wherever wabt is absent — a skip is not a verdict. The version string is read from
// the artifact instead, which is present in every clone.
func TestDeferredImmReferenceIsStillThePinnedWabt(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "..", xcorpus.Dir, xcorpus.ManifestFile))
	if err != nil {
		t.Fatalf("reading the committed corpus manifest, which is where the reference's version is recorded: %v", err)
	}
	var manifest struct {
		WabtVersion string `json:"wabt_version"`
	}
	if err := json.Unmarshal(blob, &manifest); err != nil {
		t.Fatalf("parsing the manifest: %v", err)
	}
	// Vacuity: an absent or renamed field unmarshals to "" and would compare unequal for the wrong
	// reason, reporting a bump that did not happen. Distinguish the two before comparing.
	if manifest.WabtVersion == "" {
		t.Fatalf("the manifest records no wabt version — the field moved, and this check is reading nothing")
	}
	const pinned = "1.0.41"
	if !strings.Contains(authorityWabt, pinned) {
		t.Fatalf("authorityWabt %q no longer names %s; the two must agree or this check compares the manifest against nothing", authorityWabt, pinned)
	}
	if manifest.WabtVersion != pinned {
		t.Errorf("the committed corpus was generated by wabt %s, not the pinned %s.\n"+
			"\tThis test is the wasmtime substitution's end condition. Three rows in this file "+
			"(`struct.get`'s type index, `struct.get`'s field name, `br_on_cast`'s heap type) carry a "+
			"recorded `wasmtime --invoke` observation instead of a reference image, for one reason: "+
			"wabt %s parses GC types but not GC instructions.\n"+
			"\tSo: try `wat2wasm --enable-all` on those three rows' use-first sources. If the new wabt "+
			"reads them, regenerate the three wantHex values from its output, move them to "+
			"authorityWabt, and delete authorityWasmtime together with this test. If it still refuses "+
			"them, re-pin the constant above and say which version was tried.",
			manifest.WabtVersion, pinned, pinned)
	}
}

// TestDeferredImmediateSitesAreAllNamed derives the control's domain from the package rather than
// from the list of categories anyone thought of.
//
// #130's cost was an enumeration: four categories were named as refusals and three more deferral
// sites were live in the same shape, found only by sweeping the tree by hand. So the domain here
// is every function that builds a deferred immediate — read out of the source, not listed — and a
// new one that no row reaches fails this test rather than landing uncertified
// (`docs/laws/controls.md`, derive the domain, never enumerate it).
//
// A deferral is built in exactly two ways, and both are recognised: a call to `deferImm`, and an
// `immPart` literal with a `later:` field, which is how `handlerClauses` appends into a clause
// vector instead of into `p.immParts`. That second form is the one an enumeration of `deferImm`
// callers would have missed, which is the argument for looking for both.
func TestDeferredImmediateSitesAreAllNamed(t *testing.T) {
	named := map[string]bool{}
	for _, row := range deferredImmRows {
		named[row.site] = true
	}
	// File by file rather than `parser.ParseDir`, which is deprecated for not reading build tags —
	// and the replacement it points at is `x/tools/go/packages`, which this module cannot have
	// (`go.mod` stays dependency-free). Nothing here needs package association: the domain is every
	// function in the package's own non-test source, and a `_test.go` filter is the whole selection.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var found []string
	sites := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// `deferImm` is the mechanism, not a site: it holds the only `immPart{later:}`
			// literal that is not itself a deferral.
			if fn.Name.Name == "deferImm" {
				continue
			}
			if n := deferredImmSites(fn.Body); n > 0 {
				found = append(found, fn.Name.Name)
				sites += n
			}
		}
	}
	sort.Strings(found)
	// Vacuity: a scan that matched nothing agrees with any row table, including an empty one. Both
	// counts are exact for the same reason as the row count, and there are two of them because the
	// domain this scan can see is *functions* while the thing at risk is a *deferral*:
	// `retainIdxIn` holds two, the locals-offset arm and the eight-space arm, and a third added
	// inside it would leave the function count untouched. The row table distinguishes the two arms
	// where the scan cannot, so the site count is what stands in for that granularity.
	if len(found) != 5 {
		t.Fatalf("found %d deferring functions %v, want 5 — either a deferral site moved or this scan stopped seeing them", len(found), found)
	}
	if sites != 6 {
		t.Errorf("found %d deferrals across %v, want 6 — a deferral was added or removed inside a function that already had one", sites, found)
	}
	for _, site := range found {
		if !named[site] {
			t.Errorf("%s defers an immediate and no row exercises it — add a row, with a reference image beside it", site)
		}
	}
	for site := range named {
		if !contains(found, site) {
			t.Errorf("rows name %s as a deferral site and the scan finds no deferral there — a stale citation in the control's own table", site)
		}
	}
}

// deferredImmSites counts the places in a function body that defer part of an immediate.
func deferredImmSites(body *ast.BlockStmt) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "deferImm" {
				n++
			}
		case *ast.CompositeLit:
			ident, ok := node.Type.(*ast.Ident)
			if !ok || ident.Name != "immPart" {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "later" {
					n++
				}
			}
		}
		return true
	})
	return n
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
