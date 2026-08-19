package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/validate"
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

	// owner is the instance this extern belongs to, and therefore **the module its type
	// indices are read in**. Set for every kind.
	//
	// It began as `fnInst`, set only on the func arm, because a function needed its instance
	// to be *called*: fnIdx is a module-local index and nothing else can resolve it. The
	// widening to every kind is #368's, and the fact it carries is the same one under a
	// second reading — a table's element type, a global's value type and a tag's functype are
	// all `binary.ValType`s and type indices, so `match_externtype` cannot compare them
	// against an importer's declaration without knowing which type section each side's
	// indices name. Before the widening the linker compared the two modules' indices with
	// `==` and accepted four modules the spec refuses.
	//
	// **Q2's question in embryo, deliberately answered no further than this.** Decision
	// 0017 records that a `ref` holding a bare module-local index cannot name another
	// instance's function, and that widening `ref` is its own PR. This field is not that
	// widening: it carries an instance for an *import slot*, which is a name the module
	// resolves once at instantiation, where a table slot's funcref is a value that flows.
	// Keeping them apart is what stops this from pre-deciding Q2 in the load-bearing spot.
	owner *Instance
	fnIdx uint32
}

// typeSpace is the module this extern's type indices are read in.
//
// **Two sources, and which one applies is the re-export question.** A function's index space is
// the owner's, because `fnIdx` is an index into it. A table's, global's or tag's type indices
// belong to the module that *defined* the allocation, which is not the exporting instance when
// the export re-exports an import — `Instance.Export` hands back the shared allocation while
// `owner` is the re-exporter. So those three arms read the module off the allocation and the
// allocation is where it is stored (see `global.mod`).
//
// A memory has no type indices at all, so it falls through to the owner and nothing consults the
// answer. Nil is reachable only for the zero Extern, which `Export` only ever returns alongside
// `false`; importTypeMismatch reports it rather than dereferencing it.
func (e Extern) typeSpace() *binary.Module {
	switch e.Kind {
	case binary.ExternTable:
		if e.tab != nil {
			return e.tab.mod
		}
	case binary.ExternGlobal:
		if e.glob != nil {
			return e.glob.mod
		}
	case binary.ExternTag:
		if e.tag != nil {
			return e.tag.mod
		}
	case binary.ExternFunc, binary.ExternMemory:
		// Named rather than left to a default, so that a sixth `ExternKind` arrives here as a
		// lint failure and not as a silent owner's-module answer: whether a new sort's type
		// indices live in the exporter or the definer is exactly the question this function
		// exists to answer, and #368 is what getting it wrong costs. A function's `fnIdx` is
		// an index into the owner's space by construction, and a memory has no type indices at
		// all, so both take the fall-through below.
	}
	if e.owner == nil {
		return nil
	}
	return e.owner.mod
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
			return Extern{Kind: e.Kind, owner: in, fnIdx: e.Index}, true
		case binary.ExternMemory:
			if int(e.Index) >= len(in.mems) || in.mems[e.Index] == nil {
				// An export naming a slot this phase could not fill — an imported memory
				// re-exported, or one whose allocation failed. Reported as absent rather
				// than as a nil-carrying Extern, because a supplier handing over nothing
				// would make the *importer* fail with a confusing shape one layer later.
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, owner: in, mem: in.mems[e.Index]}, true
		case binary.ExternTable:
			if int(e.Index) >= len(in.tables) || in.tables[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, owner: in, tab: in.tables[e.Index]}, true
		case binary.ExternGlobal:
			if int(e.Index) >= len(in.globals) || in.globals[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, owner: in, glob: in.globals[e.Index]}, true
		case binary.ExternTag:
			if int(e.Index) >= len(in.tags) || in.tags[e.Index] == nil {
				return Extern{}, false
			}
			return Extern{Kind: e.Kind, owner: in, tag: in.tags[e.Index]}, true
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
// kind — `match_externtype`'s five kind-specific rules (match.ml:174-183), each ported here as its
// own arm and each routing its type comparisons through `internal/validate`'s relation.
//
// # Grave #368: every arm used to compare type *indices*
//
// A type index is an identity only relative to a type section, so `im.Index == …`, `im.GlobalType
// == ext.glob.typ` and `im.Table.ElemType == ext.tab.elemType` compared numbers drawn from two
// *different* modules' type spaces. The four witnesses, printed by the rows they produced:
//
//	expected func [(ref 4)] -> [], got func [(ref 3)] -> []              type-equivalence.wast:218
//	expected func [] -> [(ref 2)], got func [] -> [(ref 0)]              type-subtyping.wast:713
//	expected func [] -> [(ref 6)], got func [] -> [(ref 4)]              type-subtyping.wast:731
//	expected global const funcref, got global const (ref func)           linking.wast:112
//
// The first three are refusals of types that *are* equal — the same rolled type at different
// ordinals. The fourth is the global arm's `==` refusing `(ref func) <: (ref null func)`, which
// `match_globaltype`'s covariance for a const global admits.
//
// It is #343's cause 1 exactly one layer up. #363 repaired this reading inside the validator and
// the linker was not swept, which is what *sweep after a grave* and *lessons are indexed by shape,
// not by file* both exist for: the shape is "a type index used as a type identity", and this
// package held a second instance of it the whole time. The rows are invisible to the board because
// they are module *definitions*, whose instantiation the harness does not score (#367) — contract
// §9's G-3 accept-direction blind spot, found by a census rather than by a fail.
//
// The relation is `internal/validate`'s, *widened* to two type contexts rather than duplicated —
// ADR 0019's "widened, not a second comparator", applied to the linker. What stays here is what
// `match_externtype` has and `match_valtype` does not: limits, address types, mutability.
//
// Returns "" when the types match, and otherwise the spec's phrasing —
// "expected ..., got ..." — the wording eval.ml's Link.error uses, so the caller's sentinel plus
// this detail reproduces the reference's message rather than inventing a shape of its own. The two
// types are spelled by `speller`, each against *its own* module: a message printing indices at a
// reader who cannot resolve them was the same defect wearing its testimony clothes.
func (in *Instance) importTypeMismatch(im *binary.Import, ext Extern) string {
	gotMod := ext.typeSpace()
	if gotMod == nil {
		// The zero Extern, which Export only ever returns alongside `false`. A caller bug rather
		// than a link fact, stated as a reachable check rather than a panic (grave 0003) and
		// answered as a mismatch, since an extern whose types cannot be read cannot be said to
		// have matching ones.
		return "a supplier with no defining module"
	}
	want, got := speller{mod: in.mod}, speller{mod: gotMod}
	switch im.Kind {
	case binary.ExternFunc:
		// `ExternFuncT (Def dt1), ExternFuncT (Def dt2) -> match_deftype c dt1 dt2`.
		//
		// **Supplier first, importer second** — the reference's own argument order at this call
		// site (`Match.match_externtype [] xt' xt`, `eval.ml:1187`, with `xt'` the actual
		// export's type) so the declared-supertype walk climbs the *supplier's* chain, exactly as
		// `match_deftype`'s disjunct 3 does.
		fn, ok := gotMod.DefinedFunc(ext.fnIdx)
		if !ok {
			// A re-exported function import: Export hands back the re-exporter and the index it
			// used, and only resolveCall walks that indirection. Answered as a mismatch, and
			// flagged rather than silently widened — an over-rejection with no vector on either
			// board is a separate question from this one.
			return "a supplier that does not define its own export"
		}
		if validate.MatchDefType(gotMod, fn.TypeIndex, in.mod, im.Index) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s",
			want.externFunc(im.Index), got.externFunc(fn.TypeIndex))
	case binary.ExternMemory:
		// `match_memorytype c (MemoryT (at1, lim1)) (MemoryT (at2, lim2))` =
		// `at1 = at2 && match_limits c lim1 lim2`.
		if matchMemoryType(ext.mem.limits, im.Memory.Limits) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s",
			want.externMemory(im.Memory.Limits), got.externMemory(ext.mem.limits))
	case binary.ExternTable:
		// `match_tabletype` = `at1 = at2 && match_limits c lim1 lim2 && match_reftype c t1 t2 &&
		// match_reftype c t2 t1` — the element type **mutually**, so it is the subtype relation
		// used as an equality rather than an `==` on the representation.
		if matchTableType(gotMod, ext.tab, in.mod, im.Table) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s",
			want.externTable(im.Table.Limits, im.Table.ElemType),
			got.externTable(ext.tab.limits, ext.tab.elemType))
	case binary.ExternGlobal:
		// `match_globaltype` = `mut1 = mut2 && match_valtype c t1 t2 && (Cons -> true | Var ->
		// match_valtype c t2 t1)`: mutability invariant, a const global **covariant** in its value
		// type, a mutable one invariant. The covariance is the fourth witness above.
		if matchGlobalType(gotMod, ext.glob, in.mod, im.GlobalType, im.GlobalMutable) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s",
			want.externGlobal(im.GlobalMutable, im.GlobalType),
			got.externGlobal(ext.glob.mutable, ext.glob.typ))
	case binary.ExternTag:
		// `match_tagtype` = mutual `match_deftype` over the two *deftypes* (see matchTagType).
		// `im.Index` is the importer's declared type index, resolved against `in.mod.Types` the
		// way every other #9-layered type index in this package is; a bad index is the layering
		// debt, reported as a mismatch per declaredFuncType's own established reading — an import
		// this engine cannot even state a type for cannot be said to match.
		if int(im.Index) >= len(in.mod.Types) || in.mod.Types[im.Index].Kind != binary.CompFunc {
			return "an unresolvable declared type"
		}
		if matchTagType(gotMod, ext.tag.typeIdx, in.mod, im.Index) {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s",
			want.externTag(im.Index), got.externTag(ext.tag.typeIdx))
	}
	return ""
}

// matchMemoryType is `match_memorytype` (match.ml:167-168): the address type by equality, then the
// limits.
//
// **The address type is the check that was missing**, and it was missing invisibly: `matchLimits`
// alone accepts an i64-addressed memory where an i32 one was declared, so eight
// `memory64-imports.wast` `assert_unlinkable` vectors were admissions on the all-gates-on board —
// "the module linked and instantiated successfully" where the spec says `incompatible import
// type`. A defect distinct from #368's, sharing only the site.
func matchMemoryType(got, want binary.Limits) bool {
	return got.Addr64 == want.Addr64 && matchLimits(got, want)
}

// matchTableType is `match_tabletype` (match.ml:170-172).
func matchTableType(gotMod *binary.Module, got *table, wantMod *binary.Module, want binary.TableType) bool {
	return got.limits.Addr64 == want.Limits.Addr64 &&
		matchLimits(got.limits, want.Limits) &&
		validate.MatchValType(gotMod, got.elemType, wantMod, want.ElemType) &&
		validate.MatchValType(wantMod, want.ElemType, gotMod, got.elemType)
}

// matchGlobalType is `match_globaltype` (match.ml:162-165).
func matchGlobalType(gotMod *binary.Module, got *global, wantMod *binary.Module, want binary.ValType, wantMutable bool) bool {
	if got.mutable != wantMutable {
		return false
	}
	if !validate.MatchValType(gotMod, got.typ, wantMod, want) {
		return false
	}
	if !wantMutable {
		return true
	}
	return validate.MatchValType(wantMod, want, gotMod, got.typ)
}

// matchLimits is match.ml's match_limits (match.ml:64-68): a supplier whose actual minimum is at
// least as generous as the declaration's and whose actual maximum is at least as tight — a
// supplier that promises *more* room than declared, never less. The direction imports.wast's
// accept vectors pin: a memory declared min 0 max unbounded accepts a supplier of min 2 max 4;
// the reverse does not.
//
// **The parameters are (got, want), which is the reference's order — and the previous version's
// doc comment claimed that while the code had them the other way round.** The behaviour was right
// and the testimony was wrong (`lim1.min >= lim2.min` with `lim1` the *actual*, per
// `eval.ml:1187`'s `match_externtype [] xt' xt`), so a reader checking this function against
// match.ml found the inequality reversed with no way to tell which half was the error. Reordered
// rather than re-commented, so it composes with the arms above — every one of which passes got
// first.
func matchLimits(got, want binary.Limits) bool {
	if got.Min < want.Min {
		return false
	}
	if !want.HasMax {
		return true
	}
	return got.HasMax && got.Max <= want.Max
}

// ErrLinkFailed is a link failure: an import the supplier answered with the wrong kind.
//
// Distinct from ErrUnsupported, which means "this engine has no linker at all" — the §3
// sentinel. Once a script *can* link, "I could not link this" and "I cannot link" are
// different facts and a caller matching on one must not catch the other.
var ErrLinkFailed = fmt.Errorf("interp: link failed")
