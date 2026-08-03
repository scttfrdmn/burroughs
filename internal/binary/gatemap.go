package binary

// The proposal→opcode mapping: which gate governs which construct.
//
// # Why this is its own file
//
// This is hand-authored testimony, and decision 0008 keeps it out of `optable.go` for
// one reason: that file's header says `Code generated ... DO NOT EDIT` and names
// `decode.ml` at `bdd7164` as its authority, and every row in it is answerable by
// re-running the extractor. A `feature` column would be the one column that is not, so
// the header would make a claim true of 542 rows and false of 542 cells — a single
// artifact under two authorities, where a reader cannot tell by looking which half they
// are reading. *One file, one authority, one answer to "who says so"* (the tools/go.mod
// lesson from 0005, in a new costume).
//
// The reference interpreter cannot supply this. `decode.ml` records what an opcode's
// immediates are; it says nothing about which proposal introduced it, because the
// reference compiles the whole tracked union unconditionally. So this is a
// `constOps`-shaped fact — a predicate the authority does not encode — and it is the
// second and last one in this package.
//
// # Provenance: cited, and checked as far as a machine can reach
//
// Every entry cites a line in a vendored proposal document that enumerates the opcode
// (`third_party/spec/proposals/...`, same `bdd7164` snapshot the table was extracted
// from, so citations and table are one snapshot). Those are **cited** rows in the
// taxonomy of #37: a human read the document and wrote down what it said.
//
// What the machine checks is not the inference but the consistency, and there are two
// checks because there are two ways this file can be wrong (gatemap_test.go):
//
//   - Every opcode named here **exists in the generated table**. A mapping entry for an
//     opcode the reference does not have is a gate governing nothing, which reads as
//     coverage and is a hole.
//   - Every bool in `Features` **maps at least one construct**. A gate with no entries
//     cannot decline anything, which is exactly the #48 defect wearing a mapping file.
//
// Neither check can verify that GC really owns `0xfb02` — no machine here reads English.
// That half is reviewed by eyes, and the citation is what makes the review possible.
// Same split as `TestDerivedFixturesStateResolvablePremises`: the inference is human, the
// resolvability is mechanical.
//
// # Granularity is mixed, and each entry says which it is
//
// Three shapes, because the proposals have three shapes (#48: "mixed granularity is fine
// but should be stated"):
//
//   - **Whole region**: `0xfb` is entirely GC, so one entry covers 31 arms.
//   - **Sub-range of a region**: relaxed SIMD is `fd 0x100`–`fd 0x12f` *inside* the
//     region SIMD owns, so the two gates share a prefix and the range decides.
//   - **Individual opcodes**: exception handling is three single-byte opcodes, tail calls
//     two. No region to lean on.
//
// Constructs that are not opcodes at all (limits flags, section ids, valtypes) stay where
// they are checked, in sections.go. This file governs the instruction stream only, and
// `Features`' own comments name both halves.

// gateID identifies a Features field. It is a string rather than an index because an
// index into a struct is a fact that changes when a field is inserted, and the whole
// point of this file is that the mapping survives the table being regenerated.
type gateID string

const (
	gateExceptionHandling gateID = "ExceptionHandling"
	gateSIMD              gateID = "SIMD"
	gateGC                gateID = "GC"
	gateTailCall          gateID = "TailCall"
	gateRelaxedSIMD       gateID = "RelaxedSIMD"
	gateMultiMemory       gateID = "MultiMemory"
	gateThreads           gateID = "Threads"
	gateMemory64          gateID = "Memory64"
	gateExtendedConst     gateID = "ExtendedConst"
)

// gatedOpcode is one mapped construct: a prefix (0x00 for none), a sub-opcode range, the
// gate that governs it, and the citation.
//
// The range is inclusive on both ends and `lo == hi` for a single opcode, so one shape
// covers all three granularities rather than three types covering one idea each.
type gatedOpcode struct {
	prefix byte
	lo, hi uint32
	gate   gateID
	// cite is the vendored document and line enumerating these opcodes. Checked for
	// *resolvability* by TestGateMapCitationsResolve, not for meaning.
	cite string
	// what names the constructs in human terms, for the error message and the reader.
	what string
}

// gatedOpcodes is the mapping. Ordered by prefix then range for reading; lookup is a
// linear scan, which is 9 comparisons on a path that has already done a map lookup and
// is about to read immediates.
//
// The ranges do not overlap **within a prefix** except deliberately: SIMD owns all of
// 0xfd and relaxed SIMD owns 0x100–0x12f inside it. That overlap is why lookup returns
// the *most specific* match (narrowest range) rather than the first — see gateFor.
var gatedOpcodes = []gatedOpcode{
	// --- exception handling: individual opcodes -----------------------------------
	{
		prefix: 0x00, lo: 0x08, hi: 0x08, gate: gateExceptionHandling,
		cite: "proposals/exception-handling/Exceptions.md:461", what: "throw",
	},
	{
		prefix: 0x00, lo: 0x0a, hi: 0x0a, gate: gateExceptionHandling,
		cite: "proposals/exception-handling/Exceptions.md:462", what: "throw_ref",
	},
	{
		prefix: 0x00, lo: 0x1f, hi: 0x1f, gate: gateExceptionHandling,
		cite: "proposals/exception-handling/Exceptions.md:460", what: "try_table",
	},

	// --- tail calls: individual opcodes -------------------------------------------
	//
	// Both cited from prose rather than a table ("`return_call` is 0x12"), which is the
	// document tail-call has. A citation to prose is still a citation; what matters is
	// that the line resolves and says the number.
	{
		prefix: 0x00, lo: 0x12, hi: 0x12, gate: gateTailCall,
		cite: "proposals/tail-call/Overview.md:139", what: "return_call",
	},
	{
		prefix: 0x00, lo: 0x13, hi: 0x13, gate: gateTailCall,
		cite: "proposals/tail-call/Overview.md:140", what: "return_call_indirect",
	},

	// --- GC: a whole region, plus single-byte opcodes ------------------------------
	//
	// 0xfb is entirely GC at bdd7164 — 37 accepted arms, struct/array/i31/cast — so the
	// region is one entry, and a non-GC arm arriving in 0xfb upstream would otherwise
	// silently inherit this gate.
	//
	// **The control for that is TestGateCensusIsClassifiedArmByArm** (decision 0012):
	// a committed census of all 499 accepted arms and their gates, recomputed from this
	// file composed with the table, exact-compared. An arm arriving upstream inside this
	// range is a build failure demanding classification rather than a silent inheritance.
	// The census covers the *ungated* arms too — an arm arriving with no gate is #48
	// itself, not a cousin — which is why it is 499 rows and not 298.
	//
	// This comment previously cited `TestEveryTableOpcodeIsClassified` for that job, and
	// no such test had ever existed: it asserted the one direction nothing asserted, so
	// the gap was documented as closed. The nearby
	// TestEveryMappedOpcodeExistsInTheTable walks **this file** (does each range cover
	// something real?) — the opposite direction, blind by construction to an arm the
	// table *gained*. The two are complements; deleting either leaves a direction
	// unasserted. Found by sweeping cited-versus-defined test names (#88), fixed in #91:
	// *a test name is as checkable as a `.wast:N`.*
	{
		prefix: 0xfb, lo: 0x00, hi: 0xff, gate: gateGC,
		cite: "proposals/gc/MVP.md:809", what: "the 0xfb region (struct, array, i31, cast)",
	},
	{
		prefix: 0x00, lo: 0xd3, hi: 0xd3, gate: gateGC,
		cite: "proposals/gc/MVP.md:805", what: "ref.eq",
	},
	// The function-references five, mapped to the GC gate rather than to a bool of
	// their own — decision 0008: the proposal folded into 3.0 core alongside GC, which
	// is how the reference treats it, and a separate gate whose scope is a subset of
	// GC's is a fifth gate that can only be turned off in states nobody wants. Stated
	// here rather than omitted, because a construct with no gate is the #48 defect and
	// silence is how it got there the first time.
	{
		prefix: 0x00, lo: 0x14, hi: 0x15, gate: gateGC,
		cite: "proposals/function-references/Overview.md:323",
		what:  "call_ref, return_call_ref",
	},
	{
		prefix: 0x00, lo: 0xd4, hi: 0xd6, gate: gateGC,
		cite: "proposals/function-references/Overview.md:325",
		what:  "ref.as_non_null, br_on_null, br_on_non_null",
	},

	// --- SIMD and relaxed SIMD: a region and a sub-range of it ---------------------
	//
	// The overlap is the point. 0xfd is SIMD's region and relaxed SIMD lives *inside*
	// it at 0x100–0x12f, so an opcode in that window answers to the narrower gate.
	// gateFor resolves by narrowest range, and TestRelaxedSIMDIsInsideSIMDsRegion pins
	// the containment so a future edit cannot quietly make them siblings.
	{
		prefix: 0xfd, lo: 0x00, hi: 0xffffffff, gate: gateSIMD,
		cite: "proposals/simd/BinarySIMD.md:47", what: "the 0xfd region (v128)",
	},
	{
		prefix: 0xfd, lo: 0x100, hi: 0x12f, gate: gateRelaxedSIMD,
		cite: "proposals/relaxed-simd/Overview.md:312",
		what:  "the relaxed SIMD window fd 0x100..0x12f",
	},
}

// gatedNonOpcodes records the constructs a gate governs that are **not** opcodes, with
// the site that checks each one.
//
// This exists because of what the "every tracked gate maps at least one construct" check
// would otherwise be: a walk over `gatedOpcodes` alone answers a narrower question than
// the ruling asked — *opcodes*, not *constructs* — and would then demand opcode entries
// for `Threads` and `Memory64`, which govern limits flags and no opcodes at all. Inventing
// entries to satisfy a check is how a control starts lying; widening the check to the
// thing it is actually about is the alternative, so the mapping carries both kinds.
//
// The entries are not dispatched from — sections.go checks these inline, and moving those
// checks here would be a refactor this change does not need. They exist so that "which
// gate governs nothing?" is a question with a complete answer in one file, and so a future
// gate that governs only a non-opcode construct is covered on arrival rather than looking
// like the #48 hole.
//
// A gate appearing in *neither* map is the defect this whole change is about, and
// TestEveryGateMapsAtLeastOneConstruct treats the two maps as one domain.
var gatedNonOpcodes = map[gateID]string{
	gateThreads:           "shared limits flags (2, 3) — sections.go decodeLimits",
	gateMemory64:          "64-bit limits flags (4..7) — sections.go decodeLimits",
	gateMultiMemory:       "memarg flags bit 6, an explicit memory index — decodeMemop",
	gateExceptionHandling: "tag section (id 13), import/export kind 4 — sections.go",
	gateSIMD:              "the v128 value type, including as a blocktype — decodeValType",
	gateGC: "the 0x40 table form with an initializer (function-references), and the twelve " +
		"GC reftypes — sections.go decodeTable, decodeRefType (#51)",
	// **The one entry here whose construct is a position rather than a syntactic form**, and the
	// distinction is load-bearing rather than pedantic. Every other entry in either map names
	// something that appears in the image and can be pointed at: a flags value, a section id, a
	// value type, an opcode. Extended-const names *where* six existing opcodes are allowed to
	// appear, so nothing in the byte stream distinguishes a gated construct from an ungated one —
	// `0x6a` in a function body is MVP, and `0x6a` in a global's initializer is this proposal.
	//
	// Which is precisely why it must not be in `gatedOpcodes`: `gateCheck` dispatches on the
	// opcode alone and cannot see the position, so an entry there would decline `i32.add`
	// everywhere. The check belongs where the position is known, and `instrCtx.constLegal` is the
	// only place that knows it — see extendedConstOps in instr.go.
	gateExtendedConst: "i32/i64 add, sub, mul in a constant expression — instr.go " +
		"extendedConstOps, checked in constLegal where the position is known (#109)",
}

// gateFor returns the gate governing one opcode, and whether one exists.
//
// Narrowest match wins, which is what makes the relaxed-SIMD-inside-SIMD overlap a
// feature rather than an ordering accident. A first-match rule would make the answer
// depend on slice order — a fact no reader of this file would think to check, and
// exactly the kind of load-bearing invisible ordering the blocktype branch order turned
// out to be.
func gateFor(prefix byte, sub uint32) (gatedOpcode, bool) {
	var best gatedOpcode
	found := false
	for _, g := range gatedOpcodes {
		if g.prefix != prefix || sub < g.lo || sub > g.hi {
			continue
		}
		if !found || g.hi-g.lo < best.hi-best.lo {
			best, found = g, true
		}
	}
	return best, found
}

// enabled reports whether a gate is on, reading the field by name.
//
// Reflection would be the obvious implementation and is wrong here: this runs per
// instruction, and a switch is both faster and a place the compiler can be asked whether
// every gate is handled. TestEveryFeatureFieldIsReadableByName is the control — it
// reflects over Features and requires each field to be reachable through this switch, so
// a ninth gate is a test failure rather than a silently unreadable bool.
func (f Features) enabled(g gateID) (on bool, known bool) {
	switch g {
	case gateExceptionHandling:
		return f.ExceptionHandling, true
	case gateSIMD:
		return f.SIMD, true
	case gateGC:
		return f.GC, true
	case gateTailCall:
		return f.TailCall, true
	case gateRelaxedSIMD:
		return f.RelaxedSIMD, true
	case gateMultiMemory:
		return f.MultiMemory, true
	case gateThreads:
		return f.Threads, true
	case gateMemory64:
		return f.Memory64, true
	case gateExtendedConst:
		return f.ExtendedConst, true
	}
	return false, false
}
