// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// Space is a component index space. The ordering rule runs across sorts on the definition stream, so a
// reference names a (Space, index) whose defining stream position must precede the referrer's; the
// per-sort slices cannot express that, which is the B.1 finding (dated, on #694).
type Space uint8

const (
	SpaceCoreFunc Space = iota
	SpaceCoreTable
	SpaceCoreMemory
	SpaceCoreGlobal
	SpaceCoreType
	SpaceCoreModule
	SpaceCoreInstance
	SpaceFunc
	SpaceValue
	SpaceType
	SpaceComponent
	SpaceInstance
)

// Def is one definition in stream order: the index space it adds an entry to, the section that defined
// it, and the item's position within that section's parsed slice. The engine walks Defs in order,
// growing each space, so a reference resolves against exactly the definitions that precede it.
type Def struct {
	Space   Space
	Section SectionKind
	Item    int
}

// def records a definition in the stream as it is parsed.
func (c *Component) def(space Space, section SectionKind, item int) {
	c.Defs = append(c.Defs, Def{Space: space, Section: section, Item: item})
}

// coreSortSpace maps a core:sort to its index space (for alias core-export targets and inline exports).
func coreSortSpace(s CoreSort) Space {
	switch s {
	case CoreSortFunc:
		return SpaceCoreFunc
	case CoreSortTable:
		return SpaceCoreTable
	case CoreSortMemory:
		return SpaceCoreMemory
	case CoreSortGlobal:
		return SpaceCoreGlobal
	case CoreSortType:
		return SpaceCoreType
	case CoreSortModule:
		return SpaceCoreModule
	default: // CoreSortInstance
		return SpaceCoreInstance
	}
}

// sortSpace maps a component sort to its index space (for alias export targets).
func sortSpace(s Sort) Space {
	switch s {
	case SortFunc:
		return SpaceFunc
	case SortValue:
		return SpaceValue
	case SortType:
		return SpaceType
	case SortComponent:
		return SpaceComponent
	default: // SortInstance, SortCore handled by the caller
		return SpaceInstance
	}
}

// The instantiation sections (Binary.md @ 2bed77e), parsed structurally here so PR B's engine can wire
// them. Slice 1 framed these; this parses their contents. An unmodeled form refuses at parse with the
// discriminant named, and every parser requires its body consumed exactly — the loader's discipline
// extended to the instantiation grammar.

// CoreSort is a core:sort byte (Binary.md core:sort): the kind a core sortidx or inline export names.
type CoreSort uint8

const (
	CoreSortFunc     CoreSort = 0x00
	CoreSortTable    CoreSort = 0x01
	CoreSortMemory   CoreSort = 0x02
	CoreSortGlobal   CoreSort = 0x03
	CoreSortTag      CoreSort = 0x04
	CoreSortType     CoreSort = 0x10
	CoreSortModule   CoreSort = 0x11
	CoreSortInstance CoreSort = 0x12
)

// CoreInstance is a core:instance: either an instantiation of a core module with named args, or a set
// of inline exports gathered into an instance.
type CoreInstance struct {
	Instantiate   bool // true: an (instantiate m arg*); false: inline exports
	ModuleIdx     uint32
	Args          []CoreInstantiateArg
	InlineExports []CoreInlineExport
}

// CoreInstantiateArg names a prior core instance supplied to an instantiation (the arg is always an
// instance — the fixed 0x12 tag).
type CoreInstantiateArg struct {
	Name        string
	InstanceIdx uint32
}

// CoreInlineExport is one export of an inline-exports core instance.
type CoreInlineExport struct {
	Name string
	Sort CoreSort
	Idx  uint32
}

// AliasKind is which of the three alias forms an Alias is.
type AliasKind uint8

const (
	AliasExport     AliasKind = iota // (alias export i n (s))
	AliasCoreExport                  // (alias core export i n (s))
	AliasOuter                       // (alias outer ct idx (s))
)

// Alias is an alias definition: an export of an instance, a core export of a core instance, or an outer
// definition. Sort is what is aliased.
type Alias struct {
	Sort        Sort
	CoreSort    CoreSort // meaningful when Sort is SortCore (a core-export alias): which core space
	Kind        AliasKind
	InstanceIdx uint32 // export / core-export
	Name        string // export / core-export
	Count       uint32 // outer
	Index       uint32 // outer
}

// space is the index space an alias adds an entry to — the aliased sort, or its core sub-sort for a
// core-export.
func (a Alias) space() Space {
	if a.Sort == SortCore {
		return coreSortSpace(a.CoreSort)
	}
	return sortSpace(a.Sort)
}

// CanonOpts is a canonical function's options (Binary.md canonopt). StringEncoding defaults to "utf8"
// when no encoding option is present. Async/Callback are the async ABI markers (0x06/0x07): Async is the
// thing the async ABI turns on — the gate:async refusal keys on its presence in any lift or lower, read
// from the canon section (ADR 0086), not on a world name.
type CanonOpts struct {
	StringEncoding string
	Memory         *uint32
	Realloc        *uint32
	PostReturn     *uint32
	Async          bool    // the `async` canonopt (0x06): this lift/lower uses the async ABI
	Callback       *uint32 // the `callback` canonopt (0x07): a stackless-async lift's callback core func
}

// CanonKind is which canonical built-in a Canon is. lift, lower, and the resource built-ins are modeled;
// the async family (waitable-set / stream / future / task / subtask / context / backpressure /
// error-context, discriminants 0x05–0x25) is *recognized* — decoded into the graph as CanonAsyncBuiltin
// so the section survives to bind, where gate:async refuses it by name (ADR 0086; its semantics are
// slice 1's, not this shell's). The thread family (0x26–0x2d, 0x40–0x42) stays a decode refusal.
type CanonKind uint8

const (
	CanonLift CanonKind = iota
	CanonLower
	CanonResourceNew
	CanonResourceDrop
	CanonResourceRep
	CanonAsyncBuiltin
)

// Canon is a canonical function definition. FuncIdx/Opts apply to lift and lower; TypeIdx is the
// component function type (lift) or the resource type (resource built-ins); AsyncOp is the discriminant
// of a CanonAsyncBuiltin.
type Canon struct {
	Kind    CanonKind
	FuncIdx uint32
	Opts    CanonOpts
	TypeIdx uint32
	AsyncOp byte
}

// space is the index space a canon adds to: lift yields a component func; lower, the resource built-ins,
// and the async built-ins yield core funcs.
func (cn Canon) space() Space {
	if cn.Kind == CanonLift {
		return SpaceFunc
	}
	return SpaceCoreFunc
}

// isAsync reports whether this canon is async surface — a lift or lower carrying the `async` canonopt
// (the gate:async key), or an async canon built-in. The stream/future value types are async surface too,
// but they are caught in the type section (unmodeledValKind); this reports the canon-section markers.
func (cn Canon) isAsync() bool {
	return cn.Kind == CanonAsyncBuiltin || cn.Opts.Async
}

// externKindSpace maps a component import's extern kind to its index space.
func externKindSpace(k ExternKind) Space {
	switch k {
	case ExternFunc:
		return SpaceFunc
	case ExternValue:
		return SpaceValue
	case ExternType:
		return SpaceType
	case ExternComponent:
		return SpaceComponent
	case ExternInstance:
		return SpaceInstance
	default: // ExternCoreModule
		return SpaceCoreModule
	}
}

// InstantiateArg names a definition (by sortidx) supplied to a component instantiation.
type InstantiateArg struct {
	Name     string
	Sort     Sort
	CoreSort CoreSort
	Idx      uint32
}

// InlineExport is one export of an inline-exports component instance: a name and the
// definition it projects (by sortidx).
type InlineExport struct {
	Name     string
	Sort     Sort
	CoreSort CoreSort
	Idx      uint32
}

// Instance is a component instance: an instantiation of a component with named args, or inline exports.
type Instance struct {
	Instantiate   bool
	ComponentIdx  uint32
	Args          []InstantiateArg
	InlineExports []InlineExport
}

func (c *Component) parseCoreInstances(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: core:instance count: %w", err)
	}
	for i := range n {
		ci, err := r.coreInstance()
		if err != nil {
			return fmt.Errorf("component: core:instance %d: %w", i, err)
		}
		c.CoreInstances = append(c.CoreInstances, ci)
		c.def(SpaceCoreInstance, SectionCoreInstance, len(c.CoreInstances)-1)
	}
	return endOf("core:instance", r, len(body))
}

func (r *reader) coreInstance() (CoreInstance, error) {
	disc, err := r.byte()
	if err != nil {
		return CoreInstance{}, err
	}
	switch disc {
	case 0x00:
		m, err := r.u32()
		if err != nil {
			return CoreInstance{}, err
		}
		n, err := r.u32()
		if err != nil {
			return CoreInstance{}, err
		}
		args := make([]CoreInstantiateArg, n)
		for i := range n {
			name, err := r.coreName()
			if err != nil {
				return CoreInstance{}, err
			}
			tag, err := r.byte()
			if err != nil {
				return CoreInstance{}, err
			}
			if tag != byte(CoreSortInstance) {
				return CoreInstance{}, fmt.Errorf("core:instantiatearg tag %#x is not instance (0x12)", tag)
			}
			idx, err := r.u32()
			if err != nil {
				return CoreInstance{}, err
			}
			args[i] = CoreInstantiateArg{Name: name, InstanceIdx: idx}
		}
		return CoreInstance{Instantiate: true, ModuleIdx: m, Args: args}, nil
	case 0x01:
		n, err := r.u32()
		if err != nil {
			return CoreInstance{}, err
		}
		exps := make([]CoreInlineExport, n)
		for i := range n {
			name, err := r.coreName()
			if err != nil {
				return CoreInstance{}, err
			}
			cs, idx, err := r.coreSortIdx()
			if err != nil {
				return CoreInstance{}, err
			}
			exps[i] = CoreInlineExport{Name: name, Sort: cs, Idx: idx}
		}
		return CoreInstance{InlineExports: exps}, nil
	default:
		return CoreInstance{}, fmt.Errorf("core:instanceexpr discriminant %#x is not 0x00/0x01", disc)
	}
}

func (c *Component) parseAliases(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: alias count: %w", err)
	}
	for i := range n {
		a, err := r.alias()
		if err != nil {
			return fmt.Errorf("component: alias %d: %w", i, err)
		}
		c.Aliases = append(c.Aliases, a)
		c.def(a.space(), SectionAlias, len(c.Aliases)-1)
	}
	return endOf("alias", r, len(body))
}

func (r *reader) alias() (Alias, error) {
	sort, cs, err := r.sort()
	if err != nil {
		return Alias{}, err
	}
	disc, err := r.byte()
	if err != nil {
		return Alias{}, err
	}
	switch disc {
	case 0x00, 0x01:
		idx, err := r.u32()
		if err != nil {
			return Alias{}, err
		}
		name, err := r.coreName()
		if err != nil {
			return Alias{}, err
		}
		kind := AliasExport
		if disc == 0x01 {
			kind = AliasCoreExport
		}
		return Alias{Sort: sort, CoreSort: cs, Kind: kind, InstanceIdx: idx, Name: name}, nil
	case 0x02:
		ct, err := r.u32()
		if err != nil {
			return Alias{}, err
		}
		idx, err := r.u32()
		if err != nil {
			return Alias{}, err
		}
		return Alias{Sort: sort, CoreSort: cs, Kind: AliasOuter, Count: ct, Index: idx}, nil
	default:
		return Alias{}, fmt.Errorf("alias discriminant %#x is not 0x00/0x01/0x02", disc)
	}
}

func (c *Component) parseCanons(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: canon count: %w", err)
	}
	for i := range n {
		cn, err := r.canon()
		if err != nil {
			return fmt.Errorf("component: canon %d: %w", i, err)
		}
		c.Canons = append(c.Canons, cn)
		c.def(cn.space(), SectionCanon, len(c.Canons)-1)
	}
	return endOf("canon", r, len(body))
}

func (r *reader) canon() (Canon, error) {
	kind, err := r.byte()
	if err != nil {
		return Canon{}, err
	}
	switch kind {
	case 0x00: // canon lift: 0x00 func-sort, core func idx, opts, type idx
		return r.canonLiftLower(CanonLift, true)
	case 0x01: // canon lower: 0x00 func-sort, func idx, opts
		return r.canonLiftLower(CanonLower, false)
	case 0x02, 0x03, 0x04: // resource.new / resource.drop / resource.rep: a resource type index
		ti, err := r.u32()
		if err != nil {
			return Canon{}, err
		}
		return Canon{Kind: CanonResourceNew + CanonKind(kind-0x02), TypeIdx: ti}, nil
	case 0x05, 0x06, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20, 0x21, 0x22, 0x23,
		0x24, 0x25:
		// The async family (Binary.md @ 2bed77e, 🔀 + 📝): recognized, not modeled. The operands are read
		// so the section advances exactly; the built-in refuses by name at bind under gate:async (ADR 0086).
		return r.canonAsyncBuiltin(kind)
	default:
		return Canon{}, fmt.Errorf("canon built-in %#x is not modeled (this slice: lift, lower, resource.new/drop/rep, the async family)", kind)
	}
}

// canonAsyncBuiltin reads one async canon built-in's operands (advancing the reader exactly per Binary.md)
// and records it as CanonAsyncBuiltin. The semantics are slice 1's; this shell recognizes the shape so
// the canon section decodes to bind, where gate:async refuses it. The 🧵 thread family is not here — it
// stays a decode refusal (`canon`'s default), being out of the async tier's scope.
func (r *reader) canonAsyncBuiltin(op byte) (Canon, error) {
	c := Canon{Kind: CanonAsyncBuiltin, AsyncOp: op}
	// async? is a single byte (0x00 absent / 0x01 async); typeidx and memidx are u32; opts is a canonopt
	// vec; context.get/set carry a core:valtype byte + u32; task.return a resultlist + opts; the
	// waitable-set.wait/poll and thread.yield forms carry a fixed 0x00 before their operand.
	readAsyncQ := func() error { _, err := r.byte(); return err } // 0x00/0x01
	readIdx := func() error { _, err := r.u32(); return err }
	readOpts := func() error { _, err := r.canonOpts(); return err }
	switch op {
	case 0x05, 0x0d, 0x1e, 0x1f, 0x22, 0x23, 0x24, 0x25:
		// task.cancel, subtask.drop, error-context.drop, waitable-set.new/.drop, waitable.join,
		// backpressure.inc/.dec — no operands.
	case 0x06: // subtask.cancel: async?
		return c, readAsyncQ()
	case 0x09: // task.return: resultlist (0x00 valtype) + opts
		disc, err := r.byte()
		if err != nil {
			return Canon{}, err
		}
		if disc != 0x00 {
			return Canon{}, fmt.Errorf("canon task.return resultlist discriminant %#x is not 0x00", disc)
		}
		if _, err := r.valType(); err != nil {
			return Canon{}, err
		}
		return c, readOpts()
	case 0x0a, 0x0b: // context.get / context.set: core:valtype (one byte) + u32
		if _, err := r.byte(); err != nil {
			return Canon{}, err
		}
		return c, readIdx()
	case 0x0c: // thread.yield: a fixed 0x00 (the 🔀 form)
		b, err := r.byte()
		if err != nil {
			return Canon{}, err
		}
		if b != 0x00 {
			return Canon{}, fmt.Errorf("canon thread.yield not followed by 0x00 (got %#x)", b)
		}
	case 0x0e, 0x13, 0x14, 0x15, 0x1a, 0x1b: // stream/future .new/.drop-*: typeidx
		return c, readIdx()
	case 0x0f, 0x10, 0x16, 0x17: // stream/future .read/.write: typeidx + opts
		if err := readIdx(); err != nil {
			return Canon{}, err
		}
		return c, readOpts()
	case 0x11, 0x12, 0x18, 0x19: // stream/future .cancel-read/.cancel-write: typeidx + async?
		if err := readIdx(); err != nil {
			return Canon{}, err
		}
		return c, readAsyncQ()
	case 0x1c, 0x1d: // error-context.new / .debug-message: opts
		return c, readOpts()
	case 0x20, 0x21: // waitable-set.wait / .poll: a fixed 0x00 then a core:memoryidx
		b, err := r.byte()
		if err != nil {
			return Canon{}, err
		}
		if b != 0x00 {
			return Canon{}, fmt.Errorf("canon waitable-set.wait/poll not followed by 0x00 (got %#x)", b)
		}
		return c, readIdx()
	}
	return c, nil
}

// canonLiftLower reads the shared tail of lift and lower: a 0x00 func-sort byte, the func index, the
// options, and (lift only) the component function type index.
func (r *reader) canonLiftLower(kind CanonKind, lift bool) (Canon, error) {
	sortByte, err := r.byte()
	if err != nil {
		return Canon{}, err
	}
	if sortByte != 0x00 {
		return Canon{}, fmt.Errorf("canon lift/lower: sort byte %#x is not func (0x00)", sortByte)
	}
	f, err := r.u32()
	if err != nil {
		return Canon{}, err
	}
	opts, err := r.canonOpts()
	if err != nil {
		return Canon{}, err
	}
	c := Canon{Kind: kind, FuncIdx: f, Opts: opts}
	if lift {
		ti, err := r.u32()
		if err != nil {
			return Canon{}, err
		}
		c.TypeIdx = ti
	}
	return c, nil
}

func (r *reader) canonOpts() (CanonOpts, error) {
	n, err := r.u32()
	if err != nil {
		return CanonOpts{}, err
	}
	opts := CanonOpts{StringEncoding: "utf8"}
	for range n {
		disc, err := r.byte()
		if err != nil {
			return CanonOpts{}, err
		}
		switch disc {
		case 0x00:
			opts.StringEncoding = "utf8"
		case 0x01:
			opts.StringEncoding = "utf16"
		case 0x02:
			opts.StringEncoding = "latin1+utf16"
		case 0x03, 0x04, 0x05, 0x07:
			v, err := r.u32()
			if err != nil {
				return CanonOpts{}, err
			}
			switch disc {
			case 0x03:
				opts.Memory = &v
			case 0x04:
				opts.Realloc = &v
			case 0x05:
				opts.PostReturn = &v
			case 0x07: // callback (core func idx): a stackless-async lift's callback
				opts.Callback = &v
			}
		case 0x06: // async: this lift/lower uses the async ABI — gate:async's key (ADR 0086)
			opts.Async = true
		default:
			return CanonOpts{}, fmt.Errorf("canonopt discriminant %#x is undefined", disc)
		}
	}
	return opts, nil
}

func (c *Component) parseInstances(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: instance count: %w", err)
	}
	for i := range n {
		in, err := r.instance()
		if err != nil {
			return fmt.Errorf("component: instance %d: %w", i, err)
		}
		c.Instances = append(c.Instances, in)
		c.def(SpaceInstance, SectionInstance, len(c.Instances)-1)
	}
	return endOf("instance", r, len(body))
}

func (r *reader) instance() (Instance, error) {
	disc, err := r.byte()
	if err != nil {
		return Instance{}, err
	}
	switch disc {
	case 0x00:
		ci, err := r.u32()
		if err != nil {
			return Instance{}, err
		}
		n, err := r.u32()
		if err != nil {
			return Instance{}, err
		}
		args := make([]InstantiateArg, n)
		for i := range n {
			name, err := r.coreName()
			if err != nil {
				return Instance{}, err
			}
			sort, cs, idx, err := r.sortIdxFull()
			if err != nil {
				return Instance{}, err
			}
			args[i] = InstantiateArg{Name: name, Sort: sort, CoreSort: cs, Idx: idx}
		}
		return Instance{Instantiate: true, ComponentIdx: ci, Args: args}, nil
	case 0x01:
		n, err := r.u32()
		if err != nil {
			return Instance{}, err
		}
		exps := make([]InlineExport, n)
		for i := range n {
			name, err := r.name()
			if err != nil {
				return Instance{}, err
			}
			sort, cs, idx, err := r.sortIdxFull()
			if err != nil {
				return Instance{}, err
			}
			exps[i] = InlineExport{Name: name, Sort: sort, CoreSort: cs, Idx: idx}
		}
		return Instance{InlineExports: exps}, nil
	default:
		return Instance{}, fmt.Errorf("instanceexpr discriminant %#x is not 0x00/0x01", disc)
	}
}

// endOf requires a section's structured body to have been consumed exactly, turning a vec desync into a
// loud error rather than a silent mis-parse.
func endOf(what string, r *reader, n int) error {
	if r.pos != n {
		return fmt.Errorf("component: %s section: consumed %d of %d bytes", what, r.pos, n)
	}
	return nil
}
