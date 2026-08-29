//go:build !burroughs_endtable

package binary

// This file is the default build's side of the block-pairing split — endtable.go's twin,
// and the thing that makes "the default build is byte-identical" a checkable claim rather
// than a hope.
//
// Everything here is empty or returns nil, and the two empty structs are what the tag is
// actually for: `moduleEnds` removes a 24-byte slice header from every `Module` (101184 B
// over 0048's corpus, and the largest term the default build would otherwise pay for a
// feature it does not use), and `decoderEnds` removes the per-body scratch. The four
// methods inline to nothing, so the decode path is unchanged instruction for instruction.
//
// What is *not* here, deliberately: `Func.EndsOff`. It is declared in every build because
// it costs nothing in any of them — it occupies the 4-byte hole after `TypeIndex`, which
// `TestEndsOffsetIsFreeInTheLayout` asserts rather than argues. Splitting it would have
// bought zero bytes and cost `Func` a second declaration of every field, to be kept in
// sync by hand, in the one struct whose layout this decision turns on.

// moduleEnds is empty in the default build. See endtable.go for the populated form.
// Embedded in `Module`, which `unused` does not count as a use for a type with no readable members
// — being unreadable is the whole point of this declaration.
//
//nolint:unused // embedded-only, and empty by design in this build; see the two lines above.
type moduleEnds struct{}

// decoderEnds is empty in the default build. See endtable.go for the populated form.
//
//nolint:unused // embedded-only and empty by design, exactly as moduleEnds above.
type decoderEnds struct{}

// openerAt reports that nothing is being paired. Returning a constant −1 is what lets the
// compiler drop the `pairEnd` call in `structural` entirely.
func (c *instrCtx) openerAt() int32 { return -1 }

// pairEnd does nothing in the default build.
func (c *instrCtx) pairEnd(int32) {}

// beginFuncEnds does nothing in the default build.
func (d *Decoder) beginFuncEnds() {}

// fileFuncEnds does nothing in the default build.
func (d *Decoder) fileFuncEnds() {}

// trimEnds does nothing in the default build.
func (d *Decoder) trimEnds() {}

// FuncEnds returns nil in the default build: no table is built, so every consumer takes the
// scanning path it took before #136 existed. This is the fallback the gated form's own
// bounds failure would degrade to, which is why the gated form refuses to degrade to it
// silently — see the note on the gated `FuncEnds`.
func (m *Module) FuncEnds(*Func) []int32 { return nil }
