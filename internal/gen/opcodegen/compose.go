package opcodegen

import (
	"errors"
	"fmt"
	"slices"
)

// ErrCompose reports a composition the licence does not cover.
//
// Its own error value rather than ErrVacuous's: a composition failure is not "the extractor
// read too little", it is "the two authorities are not in the relationship this code was
// written for". Same split as ErrVacuous versus errUnrecognized, and for the same reason —
// when two controls share an error value, `errors.Is` cannot tell a test which one fired.
var ErrCompose = errors.New("opcode table composition")

// Overlay is one consulted proposal pin and the single clause it is consulted for.
//
// # Why the clause is a field and not a discovery
//
// The obvious composition rule is *take whatever regions the base lacks*, and it is wrong in
// a way that only shows up under mutation. Gut the core pin's 0xfd region and that rule
// silently backfills SIMD from the threads pin — which has one, at a baseline three
// proposals old. The vacuity control would then pass, the drift check would pass, and the
// engine's SIMD table would be a stale snapshot nobody chose. A discovery rule cannot tell
// "core does not have this yet" from "core just lost this".
//
// So the clause is declared, and the declaration is checked in both directions: the base must
// *not* have the region (if core absorbs the proposal, this overlay is redundant and must go,
// loudly), and the overlay must have it. That is *consultation is clause-scoped, never
// wholesale* (contract §9 G-2, `docs/laws/gates.md`) expressed as a type — the same rule the
// wat keyword union landed under, where a symmetric read of this pin would have deleted 102
// core-only keywords.
type Overlay struct {
	// Name is the proposal: "threads".
	Name string
	// Path is the file read, repo-root-relative. In the type because both pins license a
	// file named `interpreter/binary/decode.ml` and the SHA alone does not say which.
	Path string
	// SHA is the pin's revision.
	SHA string
	// Region is the one prefix region this pin is consulted for. One, not a set: a pin
	// consulted for two clauses declares two overlays, so that each carries its own
	// citation and each is checked separately.
	Region byte
	// Clause cites where the licence is written down.
	Clause string
	// Table is the overlay pin's own extraction.
	Table *Table
}

// Compose returns the base table with each overlay's licensed region grafted on.
//
// What crosses from an overlay is exactly two things: the arms of its declared region, and
// the *escape arm* for that region in the single-byte space — the `| 0xfe ->` head itself,
// which Arm.Escape records and without which a decoder cannot tell "escape to a sub-table"
// from "no such opcode" (the third verdict Arm.Escape's doc describes going missing once
// already). Everything else in the overlay is dropped unread.
//
// **A zero contribution is an error, not a green.** A composition that comes out equal to its
// base drift-checks clean against a committed file generated the same wrong way, which is the
// same shape as an empty table agreeing with an empty commit — and it is the failure the
// keyword union met in this exact form: an overlay contributing nothing looks like a
// composition that worked.
func Compose(base *Table, overlays ...Overlay) (*Table, error) {
	if base == nil {
		return nil, fmt.Errorf("%w: nil base table", ErrCompose)
	}
	out := &Table{
		SourceSHA:  base.SourceSHA,
		SourcePath: base.SourcePath,
		Arms:       slices.Clone(base.Arms),
		Regions:    slices.Clone(base.Regions),
		Readers:    map[byte]string{},
	}
	for p, r := range base.Readers {
		out.Readers[p] = r
	}

	for _, ov := range overlays {
		if ov.Table == nil {
			return nil, fmt.Errorf("%w: overlay %q has no table", ErrCompose, ov.Name)
		}
		if slices.Contains(out.Regions, ov.Region) {
			return nil, fmt.Errorf("%w: overlay %q is consulted for region %#02x, which the base "+
				"already declares — if the core pin has absorbed the proposal, this overlay is "+
				"redundant and removing it is the change; backfilling a region the base has is how a "+
				"stale snapshot silently wins (%s)", ErrCompose, ov.Name, ov.Region, ov.Clause)
		}
		if !slices.Contains(ov.Table.Regions, ov.Region) {
			return nil, fmt.Errorf("%w: overlay %q does not declare region %#02x at %s — the clause "+
				"it is consulted for is not in the file it was read from (%s)",
				ErrCompose, ov.Name, ov.Region, ov.SHA, ov.Clause)
		}

		var taken, escapes int
		for _, a := range ov.Table.Arms {
			switch {
			case a.Prefix == ov.Region:
				out.Arms = append(out.Arms, a)
				taken++
			case a.Prefix == 0x00 && a.Escape && a.Code == uint32(ov.Region):
				out.Arms = append(out.Arms, a)
				escapes++
			}
		}
		if taken == 0 {
			return nil, fmt.Errorf("%w: overlay %q contributed 0 arms for region %#02x — a "+
				"composition equal to its base agrees with a committed table generated the same "+
				"way, so this is a clean drift check over an empty graft", ErrCompose, ov.Name, ov.Region)
		}
		if escapes != 1 {
			return nil, fmt.Errorf("%w: overlay %q contributed %d escape arms at single-byte %#02x, "+
				"want exactly 1 — without it the composed single-byte table calls the prefix unknown "+
				"and the whole region is unreachable (Arm.Escape)",
				ErrCompose, ov.Name, escapes, ov.Region)
		}

		out.Regions = append(out.Regions, ov.Region)
		out.Readers[ov.Region] = ov.Table.Readers[ov.Region]
		out.Overlays = append(out.Overlays, OverlayProvenance{
			Name: ov.Name, Path: ov.Path, SHA: ov.SHA, Region: ov.Region, Clause: ov.Clause,
		})
	}

	slices.Sort(out.Regions)
	sortArms(out.Arms)
	if err := out.checkDuplicates(); err != nil {
		return nil, err
	}
	if err := out.checkFloors(); err != nil {
		return nil, err
	}
	if err := out.checkFloorDomain(); err != nil {
		return nil, err
	}
	if err := out.checkReadersAreRecorded(); err != nil {
		return nil, err
	}
	// Re-checked on the composed table and not only per source, because this is the check a
	// composition can newly break: two authorities can each hold a unique join key and the union
	// hold a duplicate. That is not hypothetical — the atomics region needed Arm.Operator
	// precisely because 42 of its rows collided, and the collision was *within* the overlay, so
	// per-source would have caught that one. The cross-source case is the one only this call sees.
	if err := out.checkJoinKeysAreAccountedFor(); err != nil {
		return nil, err
	}
	if err := out.checkJoinKeyDomain(); err != nil {
		return nil, err
	}
	return out, nil
}

// Authority is the decoder that defined each region, repo-relative and keyed by prefix.
//
// # Why this is data and not only a header comment
//
// `overlayHeader` has stated the same fact since the composition landed — *region 0xfe /
// authority third_party/spec-threads/…/decode.ml* — and it states it in a comment, which
// means every control in package binary that wants to re-derive a fact from "the decoder"
// has had exactly one path available to it: the one it spelled itself. That is how
// `TestPrefixIllegalRenderingMatchesTheAuthority` came to search the *core* pin's decode.ml
// for the fallthrough arm of a region the *threads* pin defines, and report the region's
// rejection shape as underivable. The provenance was in the file and not in the language.
//
// Every region gets an entry, base regions included, so the consuming side has no default to
// apply. A rule of the form "unlisted means core" is the discovery rule Overlay's doc refuses,
// one level down: it answers confidently for a region whose authority moved.
func (t *Table) Authority() map[byte]string {
	out := make(map[byte]string, len(t.Regions))
	for _, p := range t.Regions {
		out[p] = t.SourcePath
	}
	for _, ov := range t.Overlays {
		out[ov.Region] = ov.Path
	}
	return out
}
