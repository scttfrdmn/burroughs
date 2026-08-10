package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// tagInst is one tag: an allocation, matching runtime/tag.ml's Tag.alloc exactly — identity is
// the allocation, never the declared type's structure, per #163's law applied here before any
// tag code shipped a wrong answer (#201's recon, 0022 §3). `Tag.alloc ty = {ty}` allocates one
// fresh record per declaration; this mirrors it with one fresh *tagInst per declaration, so two
// tags declared with structurally identical types in two different modules are two different
// tags — comparable only by pointer identity, never by re-deriving their type.
type tagInst struct {
	// typ is the tag's function type: its Params are the exception's payload shape, and its
	// Results are always empty ("non-empty tag result type" is #9's own already-cited
	// rejection, tag.wast:20-26 — a validation fact this package does not enforce, per the
	// declared layering debt every other #9 question in this package already carries).
	typ *binary.FuncType
}

// newTags allocates one tagInst per module-declared tag, in declaration order — `init_tag`'s
// whole content (`Tag.alloc tt'`, eval.ml:1200-1204), needing nothing but the type section
// already being populated: a tag's allocation is pure, from its declared type alone, no
// initializer to sequence against the global/table/memory chain the way those three need.
//
// Reports the layering debt when a tag names a type the module does not declare or names a
// non-function comptype — #9's verdict, arriving early exactly as `declaredFuncType` already
// states it for `call_indirect`'s type operand.
func (in *Instance) newTags() error {
	off := in.mod.ImportedTags()
	for i := range in.mod.Tags {
		idx := in.mod.Tags[i].TypeIndex
		if int(idx) >= len(in.mod.Types) {
			return fmt.Errorf("%w: tag %d declares type %d of %d",
				ErrNotValidated, off+i, idx, len(in.mod.Types))
		}
		ct := &in.mod.Types[idx]
		if ct.Kind != binary.CompFunc {
			return fmt.Errorf("%w: tag %d declares type %d, which is a %s",
				ErrNotValidated, off+i, idx, ct.Kind)
		}
		in.tags[off+i] = &tagInst{typ: &ct.Func}
	}
	return nil
}

// tagFor resolves a tag index to its allocation, reporting the layering debt for an index past
// the tag index space's end (#9's verdict, arriving early) and ErrUnsupported for an import
// nothing supplied (contract §3, matching every sibling index space's identical reasoning at
// its own resolution point — memoryFor, tableFor, globalFor).
func (in *Instance) tagFor(idx uint32) (*tagInst, error) {
	if int(idx) >= len(in.tags) {
		return nil, fmt.Errorf("%w: tag %d of %d", ErrNotValidated, idx, len(in.tags))
	}
	t := in.tags[idx]
	if t == nil {
		return nil, fmt.Errorf("%w: tag %d is an import nothing supplied (contract §3)",
			ErrUnsupported, idx)
	}
	return t, nil
}

// excObj is one thrown exception's payload: the tag that identifies it, and the values it
// carries — runtime/exn.ml's Exn(Tag.t, value list), one Go allocation per throw, matching this
// engine's own one-allocation-per-runtime-object precedent (0020's gcObj; newTable/newMemory/
// newGlobal before it). 0022's own decision, part 1.
type excObj struct {
	tag  *tagInst
	num  []uint64
	refs []ref
}

// Uncaught is an exception in flight, propagated as a Go error the way *Trap already is — the
// third control-transfer outcome (module-invalid layering debt, trap, exception), joining a
// taxonomy this package already has two members of rather than inventing a fourth channel.
// 0022's own decision, part 2.
//
// **Exported, unlike excObj/tagInst**, for `Trap`'s own reason: the harness's `isException`
// (internal/spec) needs an `errors.As` target at the package boundary the same way it already
// has one for `*interp.Trap`, and an unexported type cannot cross that boundary. The payload
// fields stay unexported — a caller asks "was this an exception" via the type, never "what was
// in it", matching the neutrality boundary Trap.Reason is the sole exception to.
type Uncaught struct {
	exc *excObj
}

// Error renders for the one case this ever reaches a human: an exception escaping every
// enclosing frame with nothing to catch it, surfacing through Invoke exactly as an uncaught
// Trap would. `Error` rather than a bespoke accessor, because every other control-transfer
// value in this package (error, *Trap) is read as an error and an escaping exception answering
// to a different interface would make the harness's own isException (spec_test.go) unable to
// treat it like its two siblings.
func (t *Uncaught) Error() string {
	return fmt.Sprintf("uncaught exception (tag with %d numeric, %d reference payload values)",
		len(t.exc.num), len(t.exc.refs))
}

// sameTagType is match_tagtype (match.ml:157-160) reduced the way sameFuncType already reduces
// match_deftype for MVP function types: `match_deftype c dt1 dt2 && match_deftype c dt2 dt1`,
// mutual containment, which for structurally-equal-or-not function types (no subtyping among
// tags themselves in this package's scope) is symmetric and reduces to one structural
// comparison rather than two. Used only for the *import* linking check (link.go's
// importTypeMismatch) — a thrown tag is matched against a catch clause by pointer identity
// (tagInst equality), never by type, which is #163's law and the entire reason tagInst exists
// as an allocation rather than a value.
func sameTagType(a, b *binary.FuncType) bool {
	return structFuncTypeEqual(a, b)
}
