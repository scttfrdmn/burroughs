// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// memRegime names which synchronisation regime a function's guest-memory accesses belong to.
type memRegime string

const (
	// regimeAtomic — every guest-byte access in the function goes through ADR 0054's atomic word
	// path (`atomicLoadWord`/`atomicStoreWord` at widths 4 and 8, `atomicCell` at 1 and 2).
	regimeAtomic memRegime = "atomic"
	// regimePlain — every access is plain at every alignment. This is ADR 0064's region.
	regimePlain memRegime = "plain"
	// regimeMixed — atomic on the aligned arm, plain on the unaligned head or tail. 0054's own
	// Consequences record that the unaligned path has no atomic mechanism at all, so this is a
	// stated residue rather than an oversight, and it is a third regime rather than a rounding of
	// the other two: calling it `atomic` would restate the over-claim #627 exists to correct.
	regimeMixed memRegime = "mixed"
	// regimeBounds — the view or read is taken for its length, or to make a bounds check happen,
	// and no guest byte is read or written through it.
	regimeBounds memRegime = "bounds"
)

// guestMemoryRegimes is the pinned enumeration ADR 0064 rests on, keyed `file.go:Receiver.Symbol`.
//
// **This table is the decision's extent, and a diff that changes it is changing the decision.** The
// seven sites [ADR 0064][0064] keeps plain map onto the `regimePlain` rows below, with `memory.write`
// and `memory.read` as the shared helpers three of the seven reach through:
//
//	memory.fill                  bulk.go:Instance.execMemoryFill    (the byte loop)
//	memory.copy                  bulk.go:Instance.execMemoryCopy    (plain copy, both sides)
//	memory.init                  bulk.go:Instance.execMemoryInit    (plain copy)
//	v128.store                   simd.go:Instance.vecStore          -> memory.go:memory.write
//	v128.store*_lane             simd.go:Instance.vecStoreLane      -> memory.go:memory.write
//	the SIMD reads               simd.go:Instance.vecLoad*          -> memory.go:memory.read
//	an active data segment       memory.go:Instance.runData         -> memory.go:memory.write
//
// The seventh is the one a hand-written list missed twice, because it was derived from
// guest-reachable instructions and `write`'s caller set is wider — *an issue's list is a registry, not
// an inventory*, which is why the domain below is derived from the AST and not typed out.
//
// [0064]: ../../docs/decisions/0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md
var guestMemoryRegimes = map[string]memRegime{
	// The two shared helpers. `write`'s `copy` is plain at every alignment and every one of its
	// callers inherits that; `read` hands a plain slice back and its callers read it plainly.
	"memory.go:memory.write": regimePlain,
	"memory.go:memory.read":  regimePlain,

	// The typed path, which is what 0054's mechanism actually reaches. Both are `mixed`: the
	// aligned arm is atomic and the unaligned tail is a plain byte loop.
	"memory.go:memory.writeNum":    regimeMixed,
	"memory.go:Instance.memAccess": regimeMixed,

	// The bulk family, `0xFC`, no gate at all.
	"bulk.go:Instance.execMemoryFill": regimePlain,
	"bulk.go:Instance.execMemoryCopy": regimePlain,
	"bulk.go:Instance.execMemoryInit": regimePlain,

	// The SIMD family, on a default-on gate since ADR 0025 and ADR 0028.
	"simd.go:Instance.vecStore":     regimePlain,
	"simd.go:Instance.vecStoreLane": regimePlain,
	"simd.go:Instance.vecLoad":      regimePlain,
	"simd.go:Instance.vecLoadLane":  regimePlain,

	// The seventh site: instantiation, not an instruction. Instantiation *looks* single-threaded
	// and is not necessarily — a second module importing an already-shared memory can be
	// instantiated while another instance's threads run on it.
	"memory.go:Instance.runData": regimePlain,

	// Bounds and length only: no guest byte crosses these. `atomicNotify`'s `read` is the clearest
	// case in the package — it discards both return values but the error, so the call is there to
	// make the trap happen and nothing is read.
	"memory.go:memory.size":           regimeBounds,
	"memory.go:memory.grow":           regimeBounds,
	"atomic.go:memory.cell":           regimeBounds,
	"atomic.go:Instance.atomicNotify": regimeBounds,
}

// TestNoGuestMemoryAccessSiteJoinsWithoutAClassification is [ADR 0064][0064]'s second conjunct.
//
// **The rule, and it is a rule rather than a property.** Scott's ruling on
// [#627](https://github.com/scttfrdmn/burroughs/issues/627): *"testimony alone, no — but an enumeration
// with a control that fails when a new site joins isn't testimony. It's a bounded, checkable claim.
// That distinction is what makes A admissible rather than a gap dressed up."* So this control exists to
// make the plain region's extent a machine-checked predicate, and its name asserts the rule by which
// the classification may change rather than any classification. Grave **#576** buried two tripwire
// names one proposal apart, each because the name asserted a code property the next proposal
// discharged; *no site joins without a classification* cannot be discharged by a proposal, because a
// proposal is the thing that has to classify its sites.
//
// **Why a region may be plain at all**, which is the other conjunct and lives in the corpus rather than
// here: [the guest's data races must not be the host's, except where the model permits the tear and the
// region is enumerable][law]. The threads proposal permits a racing `memory.fill` to be observed torn,
// so nothing below trades correctness — what the plain region costs is **report-freedom**, a property of
// the instruments.
//
// # What it checks, and which half is not tautological
//
// Two assertions, and only the first is a comparison against a table:
//
//  1. **The population.** Every function in `internal/interp`'s non-test files that calls
//     `(*memory).view`, `read` or `write` must appear in `guestMemoryRegimes`, and every row must
//     still name such a function. A new site fails as unclassified; a deleted or renamed one fails as
//     a stale row, which is the direction a bulk rename breaks.
//  2. **The regime, derived from the code.** A `regimePlain` row must call **no** synchronisation
//     helper, and an `atomic` or `mixed` row must call one. This is what stops 0064's option B from
//     landing quietly: routing `execMemoryFill` through `atomicCell` while leaving its row saying
//     `plain` fails here, and the failure message points at the obligation such a repair owes — #10's
//     `b-mm-2-sibling-field-after-wake` (landed as
//     `TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify`) takes its verdict from the race
//     detector's silence over *(this plain write, B's post-wake atomic load)*, and a detector needs one
//     non-atomic side to have anything to say. **If the region closes, that case needs a new plain side
//     in the same slice**, or it passes with nothing to detect.
//
// A "synchronisation helper" is a callee whose name begins `atomic` or is `cell` — a prefix rather
// than a fixed list, so a *new* helper (`atomicWordTouch`, say) is caught by the same predicate that
// catches today's. `cell` is named explicitly because it is the constructor for `atomicCell`, and every
// width-1 and width-2 atomic access reaches the regime through it.
//
// # Two soundness notes, because the scan is name-based
//
// **No type resolution.** `parser.ParseFile` gives no types, so `x.write(...)` matches on the selector
// name alone. Today that is exact, and the check is on the declarations rather than on the calls:
// `grep -n '^func (.*) \(view\|read\|write\)('` over the non-test files returns three lines and all
// three receivers are `*memory`, so no other type in this package can supply one of these selectors.
// (The call-site grep is the wrong instrument for this and is recorded here because it was tried
// first: `grep -n '\.read(\|\.write(\|\.view('` returns twenty hits, of which seventeen are calls and
// three are prose inside doc comments — *a grep measures text*.) If another type ever grows one of those names the
// control **over**-reports — a function joins the population that does not touch guest memory — and
// that is the safe direction: it fails, and the failure is discharged by classifying the row, not by
// widening an exception list.
//
// **The directory walk takes every `.go` file regardless of build tag**, `os.ReadDir` plus
// `parser.ParseFile` rather than the deprecated `parser.ParseDir`, for the reason
// `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` states: a plain access behind a build tag is
// still a plain access.
//
// # Watched die, all four arms, and two of them without being asked
//
// *A control isn't born until it's watched die*, and here the first two witnesses arrived from the
// tree rather than from a mutation — the table was written from the population #627 had derived by
// hand, and the first run refused it:
//
//   - **Unclassified site.** `atomic.go:Instance.atomicNotify` calls `mem.read` to make a bounds trap
//     happen, and no hand-derived list had it. So the *"a new site joined"* arm fired on a real site
//     that the enumeration this control exists to protect had missed, before any mutation was tried,
//     which is the same shape as the seventh site one level down.
//   - **Stale row.** Two rows written from the issue's prose named nothing — `atomicOp.check`, which
//     does not exist, and `vecLoadSplat`, which is not a function (`vecLoad` serves the splat
//     opcodes). Both failed as stale rows and were re-pointed to `atomicNotify` and dropped
//     respectively.
//   - **Option B landing quietly**, by mutation: `_ = atomicStoreWord(dst, uint64(k))` inserted into
//     `execMemoryFill`'s loop. The `plain`-row arm fires and names the carrier obligation.
//   - **0054's mechanism leaving a typed site**, by mutating the *table* rather than the code —
//     re-labelling `execMemoryFill` as `regimeMixed` — since that arm's subject is a row claiming
//     synchronisation the code does not have.
//
// [0064]: ../../docs/decisions/0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md
// [law]: ../../docs/laws/engine.md#the-guests-data-races-must-not-be-the-hosts-except-where-the-model-permits-the-tear-and-the-region-is-enumerable
func TestNoGuestMemoryAccessSiteJoinsWithoutAClassification(t *testing.T) {
	synchronised, files := scanGuestMemorySites(t)
	if files == 0 {
		t.Fatalf("scanned 0 non-test .go files in internal/interp: the walk found nothing, so every " +
			"assertion below would pass over an empty population")
	}
	if len(synchronised) == 0 {
		t.Fatalf("scanned %d files and found no caller of view/read/write: either the helpers were "+
			"renamed — in which case this control's predicate needs re-pointing, not deleting — or "+
			"the walk is looking at the wrong directory", files)
	}

	for _, site := range sortedKeys(synchronised) {
		regime, classified := guestMemoryRegimes[site]
		if !classified {
			t.Errorf("%s reaches guest memory and is in no regime.\n"+
				"A new site joined the population ADR 0064 rests on, and this control is the "+
				"enumeration that decision's admissibility depends on: Scott's ruling on #627 is "+
				"that an enumeration asserted by a control is a checkable claim where testimony is "+
				"not.\nClassify it in `guestMemoryRegimes` — %q if every access is plain at every "+
				"alignment, %q if it goes through ADR 0054's atomic word path, %q if the aligned arm "+
				"is atomic and the unaligned tail is not, %q if no guest byte crosses it — and if the "+
				"answer is %q, say so in ADR 0064, because widening that region is the decision's "+
				"business and not a diff's.",
				site, regimePlain, regimeAtomic, regimeMixed, regimeBounds, regimePlain)
		}
		if !classified {
			continue
		}
		switch regime {
		case regimePlain:
			if synchronised[site] {
				t.Errorf("%s is classified %q and calls a synchronisation helper.\n"+
					"This is ADR 0064's option B landing quietly, which is the one thing 0064 says "+
					"must not happen without its obligation being read: #10's "+
					"`b-mm-2-sibling-field-after-wake` — landed as "+
					"`TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify` — takes its verdict "+
					"from the race detector's silence over (this plain write, B's post-wake atomic "+
					"load), and a detector needs one non-atomic side to have anything to say. So a "+
					"slice that closes this region owes that case a new plain side IN THE SAME "+
					"SLICE, or the case keeps passing with nothing to detect. The only replacement "+
					"in hand is an unaligned typed store (0054 records that the unaligned path has "+
					"no atomic mechanism), which re-points the oracle rather than rescuing it.\n"+
					"If the repair is intended, amend ADR 0064 and re-classify this row; the fix is "+
					"not to delete this check.", site, regime)
			}
		case regimeAtomic, regimeMixed:
			if !synchronised[site] {
				t.Errorf("%s is classified %q and calls no synchronisation helper, so ADR 0054's "+
					"mechanism no longer reaches it.\nEither the atomic path was removed from this "+
					"function — in which case the plain region grew and ADR 0064's enumeration is "+
					"now wrong — or the access moved to a helper this predicate does not recognise, "+
					"in which case the predicate wants widening. A callee counts as "+
					"synchronisation when its name begins `atomic` or is `cell`.", site, regime)
			}
		case regimeBounds:
			// Deliberately unasserted in both directions. A bounds check needs no synchronisation
			// and is not wrong to have one — `memory.cell` is in this class and is the atomic
			// regime's own constructor — so neither arm above carries information here. What the
			// row asserts is the population membership, which is assertion 1.
		default:
			t.Errorf("%s carries regime %q, which is not one of the four", site, regime)
		}
	}

	for _, site := range sortedKeys(guestMemoryRegimes) {
		if _, live := synchronised[site]; !live {
			t.Errorf("%s is enumerated as regime %q and reaches guest memory nowhere in the tree.\n"+
				"A row that names nothing is the stale half of this control, and it is the half a "+
				"rename breaks: the site is still there under another name, unclassified, while the "+
				"table reads as covering it. Re-point the row to the current symbol — do not delete "+
				"it without checking that the access itself is gone.",
				site, guestMemoryRegimes[site])
		}
	}
}

// scanGuestMemorySites returns, for every function in this package's non-test files that reaches
// guest memory, whether it also calls a synchronisation helper, plus the number of files parsed.
func scanGuestMemorySites(t *testing.T) (map[string]bool, int) {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	sites := map[string]bool{}
	files := 0
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			reaches, sync := false, false
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := calleeName(call.Fun); callee {
				case "view", "read", "write":
					reaches = true
				case "cell":
					sync = true
				default:
					if strings.HasPrefix(callee, "atomic") {
						sync = true
					}
				}
				return true
			})
			if reaches {
				sites[name+":"+declName(fn)] = sync
			}
		}
	}
	return sites, files
}

// calleeName is the called function's own name — the identifier for a package-level call, the
// selector for a method call. Anything else (a call through a func-typed field, an immediately
// invoked literal) has no name and returns "".
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// declName is `Receiver.Symbol` for a method and `Symbol` for a function. The receiver is included
// because `read` and `write` are declared on `*memory` in a file that also declares `Instance`
// methods, and a bare symbol name would collide the moment a second type grows either.
func declName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return "?." + fn.Name.Name
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
