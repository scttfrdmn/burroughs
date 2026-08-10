package interp

import (
	"fmt"
	"strconv"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Extern is one thing an instance exports and another can import: a memory, a table, a
// global, or a function belonging to some instance.
//
// **It is the export side and the import side of the same fact**, which is why one type
// serves both — `Instance.Export` produces it and `InstantiateLinked` consumes it, so a
// mismatch between what an instance offers and what a supplier can supply is a compile
// error rather than a conversion. The reference's shape: `externtype` is func/table/memory/
// global/tag (`ast.ml`), and `module_export` hands back an `externinst` keyed by name
// (`instance.ml`).
//
// Exactly one field is non-nil, selected by Kind. A struct rather than an interface for the
// reason `binary.Import` carries its kind byte: the taxonomy is the wire format's and is
// closed, so a type switch here would be a second spelling of the same enum.
type Extern struct {
	Kind binary.ExternKind

	mem  *memory
	tab  *table
	glob *global
	tag  *tagInst

	// fn is a function *belonging to another instance*, which is why the instance travels
	// with the index rather than the index alone.
	//
	// **Q2's question in embryo, deliberately answered no further than this.** Decision
	// 0017 records that a `ref` holding a bare module-local index cannot name another
	// instance's function, and that widening `ref` is its own PR. This field is not that
	// widening: it carries an instance for an *import slot*, which is a name the module
	// resolves once at instantiation, where a table slot's funcref is a value that flows.
	// Keeping them apart is what stops this from pre-deciding Q2 in the load-bearing spot.
	fnInst *Instance
	fnIdx  uint32
}

// Export looks up one of an instance's exports by name.
//
// **Linear, matching exportedFunc's posture and for the same recorded reason**: a per-instance
// map would be paid by every module and read by the handful the harness links. The difference
// from exportedFunc is only that this one does not filter by kind, because an importer asks
// for a name and then checks the kind it got.
//
// A missing name reports false rather than an error, because "this module does not export
// that" is the *linker's* verdict to phrase (`assert_unlinkable` wants "unknown import"), not
// this function's.
func (in *Instance) Export(name string) (Extern, bool) {
	for _, e := range in.mod.Exports {
		if e.Name != name {
			continue
		}
		switch e.Kind {
		case binary.ExternFunc:
			return Extern{Kind: e.Kind, fnInst: in, fnIdx: e.Index}, true
		case binary.ExternMemory:
			if int(e.Index) >= len(in.mems) || in.mems[e.Index] == nil {
				// An export naming a slot this phase could not fill — an imported memory
				// re-exported, or one whose allocation failed. Reported as absent rather
				// than as a nil-carrying Extern, because a supplier handing over nothing
				// would make the *importer* fail with a confusing shape one layer later.
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, mem: in.mems[e.Index]}, true
		case binary.ExternTable:
			if int(e.Index) >= len(in.tables) || in.tables[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, tab: in.tables[e.Index]}, true
		case binary.ExternGlobal:
			if int(e.Index) >= len(in.globals) || in.globals[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, glob: in.globals[e.Index]}, true
		case binary.ExternTag:
			if int(e.Index) >= len(in.tags) || in.tags[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, tag: in.tags[e.Index]}, true
		}
		return Extern{}, false
	}
	return Extern{}, false
}

// Imports is what a caller supplies to InstantiateLinked: the (module, name) pairs an
// importing module asks for, resolved to externs.
//
// A func rather than a map, because resolution is the *script's* semantics and not this
// package's — the reference passes a `lookup` closure for exactly this reason
// (`runner.ml`'s `register_virtual`), and a map here would make the engine hold the
// registry the harness owns. It reports false for a name nothing supplies, which is what
// makes `unknown import` the linker's verdict to phrase.
type Imports func(module, name string) (Extern, bool)

// InstantiateLinked instantiates a module with its imports supplied.
//
// **It is Instantiate with the nil-reserved slots filled, which is the whole shape of this
// change.** The import slots were already reserved rather than omitted — `mems`, `tables`
// and `globals` are sized `imported + defined` and the import range is nil, a convention
// paid for by the 22 vectors where `memory.size $mem1` returned $mem3's page count. So
// supplying an import is *filling* a slot, never restructuring an index space, and every
// downstream index calculation is untouched.
//
// Returns `(*Instance, *Trap, error)`: the trap channel is 0015's and carries "the module
// came to life and died doing it", the error channel carries a link failure, which is
// neither a trap nor a verdict on the module's grammar. Two channels because a link failure
// must not be reportable as a trap — `assert_unlinkable` and `assert_trap` want different
// words, and a single channel would let one score the other's vectors.
//
// A nil `imp` means "supply nothing", which is exactly Instantiate's behaviour, so
// Instantiate is this function with a nil resolver rather than a second body.
func InstantiateLinked(m *binary.Module, imp Imports) (*Instance, *Trap, error) {
	memOff, tabOff := m.ImportedMems(), m.ImportedTables()
	globOff, tagOff := m.ImportedGlobals(), m.ImportedTags()
	in := &Instance{
		mod:     m,
		mems:    make([]*memory, memOff+len(m.Memories)),
		tables:  make([]*table, tabOff+len(m.Tables)),
		globals: make([]*global, globOff+len(m.Globals)),
		elems:   make([]*elemInstance, len(m.Elems)),
		datas:   make([]*dataInstance, len(m.Datas)),
		funcs:   make([]*Extern, m.ImportedFuncs()),
		// tags is sized like mems/tables/globals (imports + definitions, reserved not
		// omitted, 0022 §3) and unlike funcs (imports only) — a defined tag needs a runtime
		// allocation the way a defined memory does, where a defined function needs none.
		tags: make([]*tagInst, tagOff+len(m.Tags)),
	}
	// **Imports are resolved before anything is allocated or evaluated**, and the position is
	// forced rather than chosen: a global's initializer may read an imported global, and an
	// element or data segment's offset may too, so an unfilled import slot during those passes
	// would make `globalFor` report a declared-but-uninitialized global for something that was
	// merely not linked yet. The reference resolves the whole import list first for the same
	// reason (`eval.ml`'s `init` takes `externinst list` as an argument — resolution happens
	// in the caller, before instantiation begins at all).
	if err := in.link(imp); err != nil {
		return nil, nil, err
	}
	if t := in.build(); t != nil {
		return nil, t, nil
	}
	return in, nil, nil
}

// link fills the reserved import slots from the caller's resolver.
//
// **Per-kind index counters, not one shared cursor.** Each extern kind has its own index
// space, so the Nth *memory* import is at memory index N regardless of how many function or
// table imports precede it in the import section. A single counter over `m.Imports` would
// put the second memory import at index 3 in a module that imports two functions first —
// wrong, and wrong in the accept direction, which is the class of defect a rejection corpus
// scores green (contract §9 G-3). This is `importedCount`'s lesson arriving on the filling
// side: the same per-kind arithmetic that sizes the slices has to place into them.
func (in *Instance) link(imp Imports) error {
	var memIdx, tabIdx, globIdx, fnIdx, tagIdx int
	for i := range in.mod.Imports {
		im := &in.mod.Imports[i]
		// **A counter per kind, and the index is the count of imports of that kind before it.**
		// The reserved-not-omitted convention says an import occupies its index; this is where
		// the *which* index is computed, and the plausible wrong answer is a single cursor over
		// `in.mod.Imports`, which puts the second memory import at index 3 in a module that
		// imports two functions first. Wrong, and wrong in the accept direction
		// (TestImportSlotIndicesAreCountedPerKind).
		//
		// **The claim-before-resolve ordering was load-bearing and is no longer**, which is
		// worth stating because the code still has that shape. It said: an unsatisfied import
		// still occupies its index, so the counter advances whether or not anything fills the
		// slot, and incrementing only on success would place the second satisfied memory import
		// at index 0 when the first was unsatisfied. True of the draft that left an unsatisfied
		// slot nil and continued; the `unknown import` arm below returns instead, so there is no
		// longer an unsatisfied-and-still-running case for the ordering to get right. Its
		// control had to be re-pointed for the same reason — it was named
		// TestUnsatisfiedImportDoesNotShiftLaterIndices and could no longer fail.
		slot := -1
		switch im.Kind {
		case binary.ExternMemory:
			slot, memIdx = memIdx, memIdx+1
		case binary.ExternTable:
			slot, tabIdx = tabIdx, tabIdx+1
		case binary.ExternGlobal:
			slot, globIdx = globIdx, globIdx+1
		case binary.ExternFunc:
			slot, fnIdx = fnIdx, fnIdx+1
		case binary.ExternTag:
			slot, tagIdx = tagIdx, tagIdx+1
		}
		var ext Extern
		var ok bool
		if imp != nil {
			ext, ok = imp(im.Module, im.Name)
		}
		if imp == nil {
			// **No resolver at all is the unlinked path, and it degrades rather than refuses.**
			// Leaving every slot nil preserves the §3 reporting exactly — `memoryFor` and friends
			// discriminate on the import offset and name §3 — so `Instantiate` stays this function
			// with a nil resolver rather than a second body. Distinguished from a resolver that
			// *answers no* below, which is a different fact: the script was asked and had nothing.
			continue
		}
		if !ok {
			// `unknown import` — `link` in `eval.ml`, whose `Link.error` for a name the registry
			// does not hold is `"unknown import"`. 16 vectors expect this string.
			//
			// **A refusal, where an earlier draft of this function left the slot nil and continued.**
			// That draft's argument was bucket preservation: refusing would regress the 624 failures
			// whose text names the §3 frontier, which is the work plan this change exists to drain.
			// The argument was sound and is now *discharged* — the drain measured 624 → 13, so the
			// bucket has served its purpose, and the 13 that remain are gate-declined modules whose
			// registers bind nothing rather than a phase without a linker.
			//
			// What replaces it is the accept direction, which is the half a rejection corpus cannot
			// score (§9 G-3): a module whose import nothing supplies is **unlinkable**, so an engine
			// that instantiates it anyway and only complains if the import is *touched* accepts a
			// module the spec refuses. Measured rather than argued: the change converts 15
			// `assert_unlinkable` vectors from fail to pass and moves zero vectors out of pass.
			return fmt.Errorf("%w: unknown import: %q %q", ErrLinkFailed, im.Module, im.Name)
		}
		if ext.Kind != im.Kind {
			// `incompatible import type` — `eval.ml`'s other `Link.error`, for a name that resolves
			// to the wrong sort of thing. 184 vectors expect this string.
			//
			// **The sentinel is the spec's and the detail is ours, in that order.** An earlier
			// draft wrote only the detail — `import "spectest" "print_i32" is a memory but the
			// supplier offers a func` — a right verdict phrased in a string the spec does not have,
			// which is grave #36's fabricated evidence with the aggravating feature that this arm is
			// *oracle-covered*: `assert_unlinkable`'s expected text is the whole sentinel, so 29
			// vectors were failing on the wording alone. #38's refinement in the one place it bites.
			return fmt.Errorf("%w: incompatible import type: %q %q is a %s but the supplier offers a %s",
				ErrLinkFailed, im.Module, im.Name, im.Kind, ext.Kind)
		}
		if detail := in.importTypeMismatch(im, ext); detail != "" {
			// `incompatible import type` again, for a name that resolves to the *right kind*
			// with the wrong signature, limits or mutability — `match_externtype`'s other
			// failure mode (match.ml), and the one #164 exists for: a matching kind was
			// already enough to pass every vector above, so 124 modules the spec refuses
			// were being accepted (§9 G-3's accept-direction blind spot, closed rather than
			// left for the corpus to never ask about).
			return fmt.Errorf("%w: incompatible import type: %q %q %s",
				ErrLinkFailed, im.Module, im.Name, detail)
		}
		switch im.Kind {
		case binary.ExternMemory:
			in.mems[slot] = ext.mem
		case binary.ExternTable:
			in.tables[slot] = ext.tab
		case binary.ExternGlobal:
			in.globals[slot] = ext.glob
		case binary.ExternFunc:
			e := ext
			in.funcs[slot] = &e
		case binary.ExternTag:
			in.tags[slot] = ext.tag
		}
	}
	return nil
}

// importTypeMismatch reports why im and ext disagree on their *type*, once they already agree on
// kind — `match_externtype`'s four kind-specific rules (match.ml). The func case now calls
// `sameFuncType`'s widened `match_deftype` reduction (0019's own named gap, #164's remaining 4
// vectors) — the declared-supertype walk, restricted to the scope `sameFuncType`'s own doc
// comment states. The other three kinds stay `ValType`'s field-wise `==` (0018) rather than
// reftype subtyping: right for the two ungated Wasm 2.0 forms, and for GC forms (a table's or
// global's reftype) it is *equality* where the reference wants *subtyping*, a real but
// unwidened gap — `match_reftype`/`match_heaptype` over table and global element types is 0019's
// *other* consumer, not yet built, since ref.test/ref.cast are what motivated it and neither
// exists as an interpreter arm yet.
//
// Returns "" when the types match, and otherwise the spec's phrasing —
// "expected ..., got ..." — the wording eval.ml's Link.error uses, so the caller's sentinel plus
// this detail reproduces the reference's message rather than inventing a shape of its own.
func (in *Instance) importTypeMismatch(im *binary.Import, ext Extern) string {
	switch im.Kind {
	case binary.ExternFunc:
		want, err := in.declaredFuncType(uint64(im.Index))
		if err != nil {
			// #9's layering debt, not a link fact — an import naming a type the decoder
			// accepted but the validator would not have. Reported as a mismatch anyway: an
			// import this engine cannot even state a type for cannot be said to match.
			return "an unresolvable declared type: " + err.Error()
		}
		fn, ok := ext.fnInst.mod.DefinedFunc(ext.fnIdx)
		if !ok {
			// Unreachable while Export resolves re-exported imports through to their
			// definer (call.go's identical check on the call_indirect path states the
			// invariant at length). Stated as a reachable check rather than a panic, per
			// grave 0003.
			return "a supplier that does not define its own export"
		}
		got, err := ext.fnInst.funcType(fn)
		if err != nil {
			return "an unresolvable supplied type: " + err.Error()
		}
		// **`ext.fnInst.mod`/`fn.TypeIndex` first, `in.mod`/`im.Index` second** — matching the
		// reference's own argument order at this call site (`match_deftype c dt1 dt2` with
		// `dt1` the actual export's type, `dt2` the import's declared type, `eval.ml:1186-1187`)
		// so the declared-supertype walk climbs the *supplier's* chain, exactly as
		// `match_deftype`'s disjunct 3 does.
		if sameFuncType(ext.fnInst.mod, fn.TypeIndex, in.mod, im.Index) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s", funcTypeString(want), funcTypeString(got))
	case binary.ExternMemory:
		if matchLimits(im.Memory.Limits, ext.mem.limits) {
			return ""
		}
		return fmt.Sprintf("expected memory %s, got %s",
			limitsString(im.Memory.Limits), limitsString(ext.mem.limits))
	case binary.ExternTable:
		if im.Table.ElemType == ext.tab.elemType && matchLimits(im.Table.Limits, ext.tab.limits) {
			return ""
		}
		return fmt.Sprintf("expected table %s %s, got table %s %s",
			limitsString(im.Table.Limits), im.Table.ElemType,
			limitsString(ext.tab.limits), ext.tab.elemType)
	case binary.ExternGlobal:
		if im.GlobalType == ext.glob.typ && im.GlobalMutable == ext.glob.mutable {
			return ""
		}
		return fmt.Sprintf("expected global %s %s, got global %s %s",
			mutString(im.GlobalMutable), im.GlobalType, mutString(ext.glob.mutable), ext.glob.typ)
	case binary.ExternTag:
		// **`sameTagType`, not `sameFuncType`'s subtyping walk** — `match_tagtype`
		// (match.ml:157-160) is mutual `match_deftype` containment, which for MVP function
		// types with no declared supertypes reduces to one structural comparison (see
		// sameTagType's own doc comment). `im.Index` is the importer's declared type,
		// resolved against `in.mod.Types` the way every other #9-layered type index in this
		// package is; a bad index is the layering debt, reported as a mismatch per
		// declaredFuncType's own established reading — an import this engine cannot even
		// state a type for cannot be said to match.
		if int(im.Index) >= len(in.mod.Types) || in.mod.Types[im.Index].Kind != binary.CompFunc {
			return "an unresolvable declared type"
		}
		want := &in.mod.Types[im.Index].Func
		if sameTagType(want, ext.tag.typ) {
			return ""
		}
		return fmt.Sprintf("expected tag %s, got tag %s",
			funcTypeString(want), funcTypeString(ext.tag.typ))
	}
	return ""
}

// matchLimits is match.ml's match_limits: an importer's declared bound is satisfied by a
// supplier whose actual minimum is at least as generous and whose actual maximum is at least as
// tight — a supplier that promises *more* room than declared, never less. lim1 is the importer's
// declaration, lim2 the supplier's actual limits, matching the reference's parameter order and
// the direction imports.wast's accept vectors pin (a memory declared min 0 max unbounded accepts
// a supplier of min 2 max 4; the reverse does not).
func matchLimits(lim1, lim2 binary.Limits) bool {
	if lim1.Min > lim2.Min {
		return false
	}
	if !lim1.HasMax {
		return true
	}
	return lim2.HasMax && lim2.Max <= lim1.Max
}

// limitsString and mutString spell a limits pair and a mutability flag the way the reference's
// string_of_limits and string_of_mut do, for the same reason funcTypeString exists: the detail
// half of a message the suite does not read past its sentinel is still ours to keep honest.
func limitsString(lim binary.Limits) string {
	if lim.HasMax {
		return fmt.Sprintf("%d %d", lim.Min, lim.Max)
	}
	return strconv.FormatUint(lim.Min, 10)
}

func mutString(mutable bool) string {
	if mutable {
		return "mut"
	}
	return "const"
}

// ErrLinkFailed is a link failure: an import the supplier answered with the wrong kind.
//
// Distinct from ErrUnsupported, which means "this engine has no linker at all" — the §3
// sentinel. Once a script *can* link, "I could not link this" and "I cannot link" are
// different facts and a caller matching on one must not catch the other.
var ErrLinkFailed = fmt.Errorf("interp: link failed")
