// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// ref is a reference from one definition to a (space, index) it depends on.
type ref struct {
	space Space
	idx   uint32
}

// refsOf returns the index-space references a definition makes. Type references (canon lift's component
// type, a resource built-in's resource type) are omitted: this slice does not model the type spaces, so
// it neither counts them nor checks references into them (a later slice does). Outer aliases reference
// an enclosing component's scope, not this stream, and are omitted for the same reason.
func (c *Component) refsOf(d Def) []ref {
	switch d.Section {
	case SectionCoreInstance:
		ci := c.CoreInstances[d.Item]
		if ci.Instantiate {
			rs := []ref{{SpaceCoreModule, ci.ModuleIdx}}
			for _, a := range ci.Args {
				rs = append(rs, ref{SpaceCoreInstance, a.InstanceIdx})
			}
			return rs
		}
		rs := make([]ref, 0, len(ci.InlineExports))
		for _, e := range ci.InlineExports {
			rs = append(rs, ref{coreSortSpace(e.Sort), e.Idx})
		}
		return rs
	case SectionAlias:
		a := c.Aliases[d.Item]
		switch a.Kind {
		case AliasExport:
			return []ref{{SpaceInstance, a.InstanceIdx}}
		case AliasCoreExport:
			return []ref{{SpaceCoreInstance, a.InstanceIdx}}
		default: // AliasOuter — an enclosing scope, not this stream
			return nil
		}
	case SectionCanon:
		cn := c.Canons[d.Item]
		switch cn.Kind {
		case CanonLift:
			return []ref{{SpaceCoreFunc, cn.FuncIdx}} // + a type ref, not modeled here
		case CanonLower:
			return []ref{{SpaceFunc, cn.FuncIdx}}
		default: // resource built-ins reference a type, not modeled here
			return nil
		}
	case SectionInstance:
		in := c.Instances[d.Item]
		if in.Instantiate {
			rs := []ref{{SpaceComponent, in.ComponentIdx}}
			for _, a := range in.Args {
				rs = append(rs, argRef(a))
			}
			return rs
		}
		rs := make([]ref, 0, len(in.InlineExports))
		for _, e := range in.InlineExports {
			if e.Sort == SortCore {
				rs = append(rs, ref{coreSortSpace(e.CoreSort), e.Idx})
			} else {
				rs = append(rs, ref{sortSpace(e.Sort), e.Idx})
			}
		}
		return rs
	default: // core:module and import make no index-space references
		return nil
	}
}

// argRef is the space+index a component instantiate arg targets.
func argRef(a InstantiateArg) ref {
	if a.Sort == SortCore {
		return ref{coreSortSpace(a.CoreSort), a.Idx}
	}
	return ref{sortSpace(a.Sort), a.Idx}
}

// checkForwardRefs enforces the component grammar's ordering rule on the definition stream: a
// definition may reference only (space, index) pairs whose defining position precedes it. It walks the
// stream once, growing a per-space count, and refuses — by name — a reference into a space that has not
// yet reached that index. This is the cross-sort check the per-sort model could not express (the B.1
// finding): the counts advance in stream order across every sort at once.
func (c *Component) checkForwardRefs() error {
	count := map[Space]uint32{}
	for pos, d := range c.Defs {
		for _, r := range c.refsOf(d) {
			if r.idx >= count[r.space] {
				return fmt.Errorf("component: definition at stream position %d (%s) references %s index %d, defined at or after it — a forward reference",
					pos, d.Section, spaceName(r.space), r.idx)
			}
		}
		count[d.Space]++
	}
	return nil
}

func spaceName(s Space) string {
	switch s {
	case SpaceCoreFunc:
		return "core:func"
	case SpaceCoreModule:
		return "core:module"
	case SpaceCoreInstance:
		return "core:instance"
	case SpaceFunc:
		return "func"
	case SpaceInstance:
		return "instance"
	case SpaceComponent:
		return "component"
	default:
		return fmt.Sprintf("space(%d)", uint8(s))
	}
}
