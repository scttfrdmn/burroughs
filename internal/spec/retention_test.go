package spec

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// endOp is END's opcode, spelled here because `binary.opEnd` is unexported and this is a
// different package.
//
// A literal rather than a new export: the byte is fixed by the spec's binary format, so a
// duplicate cannot drift the way a duplicated *derivation* can (#78's one-concept-one-trigger
// is about triggers, not about constants the format pins). Exporting it would widen the
// engine's API surface for a test's convenience.
const endOp = 0x0B

// The accept-direction control on the retained form (#7, 0002).
//
// # Why this is product work and not an instrument
//
// Every one of the decoder's 4162 green vectors is a **rejection**. The internal form is
// only built on the accepting path, so the entire conformance record the retention was
// grown out of says nothing whatever about it: a decoder that recognizes every malformed
// module correctly and retains *nothing* scores exactly 4162 too. That is contract §9 G-3
// in its purest form — the suite cannot see this, by construction — and the disciplines
// name this case as the one where a control *is* product work rather than overhead on it.
//
// So the assertions here are about what the form contains, checked against the modules the
// suite expects to be accepted, over the path that has the conformance record. The
// alternative — hand-typed images — would be a corpus with no provenance testing a producer
// with no consumer.
//
// # The population, measured rather than assumed
//
// The binary path's *accept* population is small and that is a fact about the suite, not a
// gap here: **74 accepted modules across 253 files, carrying 4 function bodies and 18
// instructions total** (measured, printed below every run). The corpus's binary modules are
// overwhelmingly `assert_malformed` fodder — deliberately broken images — and its executable
// content is in text modules. That asymmetry is why 0011's ruling sequences the internal
// form before the text encoder, and why the interpreter's real oracle arrives with the text
// path: exactly **one** ungated `assert_return` in the whole corpus sits behind a binary
// module (float_literals.wast:233; the other six are SIMD-gated).
//
// A population of 74 is therefore what there is to work with, and a control over it is worth
// more than its size suggests — those 74 are the only modules in the corpus whose acceptance
// the engine has *already proven*, so a retention defect visible in them is a retention
// defect proven against the suite rather than against my own fixtures.
func TestRetainedFormOverAcceptedModules(t *testing.T) {
	requireSuite(t)

	var (
		accepted                     int
		withTypes, withFuncs         int
		withGlobals, withExports     int
		withImports, withBodies      int
		instrs, sections, boundFuncs int
	)
	for _, path := range suitePaths(t) {
		s, err := ParseFile(path)
		if err != nil {
			// Not this control's subject: TestParseEverySuiteFile owns the parse
			// direction, and duplicating its verdict here would report one defect twice.
			continue
		}
		for _, c := range s.Commands {
			if c.Kind != KindModuleBinary {
				continue
			}
			m, err := binary.DecodeModule(c.Module)
			if err != nil {
				// A `(module binary ...)` the decoder rejects is a *fail* on the board, and
				// the board reports it. Here it is simply not a member of the population —
				// there is no retained form to check.
				continue
			}
			accepted++
			sections += len(m.Sections)

			// Every retained slice's length must agree with what the module declared, and
			// the check that makes that non-trivial is the *function* one below: the type
			// indices and the bodies come from two different sections, so their pairing is
			// the one piece of retention that can silently half-happen.
			if len(m.Types) > 0 {
				withTypes++
			}
			if len(m.Globals) > 0 {
				withGlobals++
			}
			if len(m.Exports) > 0 {
				withExports++
			}
			if len(m.Imports) > 0 {
				withImports++
			}
			if len(m.Funcs) > 0 {
				withFuncs++
			}

			for i := range m.Funcs {
				f := &m.Funcs[i]
				instrs += len(f.Body)
				if len(f.Body) > 0 {
					withBodies++
				}
				// **A body is never empty, and this assertion found the reason it was.**
				// `expr` is terminated by an END, so even `(func)` retains one
				// instruction — *if* the terminator is kept. It was not: `expectEnd`
				// judged the byte and dropped it at all three call sites, so 23 of the
				// 27 bound functions decoded to a zero-length body and the header
				// comment on `structural` claimed a retention that no code performed.
				// The fix is `endTerminator`; this is the control that named it.
				//
				// Kept as an assertion rather than relaxed, because a zero-length body
				// means the descent recognized a function and retained nothing from it —
				// retention half-happening, which is exactly the failure mode a
				// rejection-only conformance record cannot see.
				if len(f.Body) == 0 {
					t.Errorf("%s: function %d decoded with an empty body; every `expr` ends "+
						"with an END the retention keeps, so an empty body means the "+
						"instruction grammar ran and retained nothing", path, i)
					continue
				}
				// The terminator, checked positionally rather than merely counted: a body
				// whose last instruction is not END means either the delimiter was dropped
				// again or something was appended past it, and the interpreter reads the
				// extent off this byte.
				if last := f.Body[len(f.Body)-1]; last.Op != endOp || last.Prefix != 0x00 {
					t.Errorf("%s: function %d ends with opcode %#02x (prefix %#02x), want "+
						"END (%#02x); a block's extent is not derivable from its header, so "+
						"a dropped terminator is structure the interpreter cannot recompute",
						path, i, last.Op, last.Prefix, endOp)
				}
				// The type index must name a declared type. Not a *validation* claim — #9
				// owns whether indices resolve — but a claim about the **zip**: finishFuncs
				// pairs the function section's indices with the code section's bodies, and a
				// pairing that slipped by one would produce indices that are individually
				// plausible and collectively wrong. An out-of-range index here means the
				// zip lost alignment, since every module in this population was *accepted*
				// and checkCounts already required the two counts to agree.
				if int(f.TypeIndex) >= len(m.Types) {
					t.Errorf("%s: function %d has type index %d with %d types declared; "+
						"finishFuncs pairs two sections and an out-of-range index in an "+
						"accepted module means that pairing slipped",
						path, i, f.TypeIndex, len(m.Types))
					continue
				}
				if k := m.Types[f.TypeIndex].Kind; k != binary.CompFunc {
					t.Errorf("%s: function %d names type %d, which is a %s and not a func; "+
						"comptypes share the type index space, so this is index alignment "+
						"being wrong rather than a type being wrong",
						path, i, f.TypeIndex, k)
				}
				boundFuncs++
			}

			// The function index space, which is imports-then-defined. Checked through
			// DefinedFunc rather than by indexing m.Funcs, because that offset is the
			// accessor's whole reason to exist and an offset nobody exercises is an
			// assumption.
			off := m.ImportedFuncs()
			for i := range m.Funcs {
				idx := uint32(off + i)
				got, ok := m.DefinedFunc(idx)
				if !ok || got != &m.Funcs[i] {
					t.Errorf("%s: DefinedFunc(%d) did not return defined function %d "+
						"(imports: %d); the function index space is imports-then-defined "+
						"and this is the accessor that knows it", path, idx, i, off)
				}
			}
			// The imported range must *not* resolve to a defined function. The other half
			// of the same fact, and the half that would silently return function 0 if the
			// offset were dropped — a call to an import dispatching to the module's own
			// first function is the worst shape this defect could take.
			if off > 0 {
				if _, ok := m.DefinedFunc(0); ok {
					t.Errorf("%s: DefinedFunc(0) resolved with %d function imports; index 0 "+
						"names an import, and resolving it to a defined body would "+
						"dispatch a call to the wrong function entirely", path, off)
				}
			}

			for gi, g := range m.Globals {
				// A global's initializer is a constant expression, which is an instruction
				// sequence terminated by END — so it is never empty for the same reason a
				// body is not, and it was empty for the same reason too until
				// `endTerminator` landed. `constExprBody` shares the terminator call with
				// `decodeFuncBody`, which is why one defect had two faces.
				if len(g.Init) == 0 {
					t.Errorf("%s: global %d retained an empty initializer; a constexpr ends "+
						"with an END the retention keeps", path, gi)
				} else if last := g.Init[len(g.Init)-1]; last.Op != endOp || last.Prefix != 0x00 {
					t.Errorf("%s: global %d's initializer ends with opcode %#02x, want END "+
						"(%#02x)", path, gi, last.Op, endOp)
				}
				if g.Type == binary.NoValType {
					t.Errorf("%s: global %d retained NoValType as its type; that sentinel "+
						"means *unrepresentable* (the twelve GC forms), and a global whose "+
						"type the decoder accepted must have a representable one under "+
						"v0's gate posture", path, gi)
				}
			}

			for ti, tt := range m.Types {
				if tt.Kind != binary.CompFunc {
					continue
				}
				// The out-parameter's hazard, checked: decodeValType writes to a field on
				// the Decoder, and the one way that goes wrong is a backtracked `either`
				// branch leaving a type the module never contained. NoValType in an
				// accepted functype is that failure's signature.
				for _, vt := range append(append([]binary.ValType{}, tt.Func.Params...), tt.Func.Results...) {
					if vt == binary.NoValType {
						t.Errorf("%s: type %d has an unrepresentable valtype in its "+
							"signature; decodeValType writes its out-parameter only before "+
							"a successful return, so this means a value survived a "+
							"backtracked branch", path, ti)
					}
				}
			}

			// A module with a start section names a function. Retained as an index plus a
			// presence bool rather than as a sentinel index, because 0 is a *legal* start
			// index — the invented-evidence class (grave #36) if the two were merged.
			if m.HasStart && len(m.Sections) == 0 {
				t.Errorf("%s: HasStart with no sections retained at all", path)
			}
		}
	}

	t.Logf("retained form over %d accepted binary modules: %d sections, %d with types, "+
		"%d with funcs (%d bound), %d with bodies, %d instructions, %d with globals, "+
		"%d with exports, %d with imports",
		accepted, sections, withTypes, withFuncs, boundFuncs, withBodies, instrs,
		withGlobals, withExports, withImports)

	// # The vacuity floors, and why there are four of them
	//
	// Every assertion above is inside two loops, so all of them are vacuous together if the
	// population is empty — the empty-set agreement (#29), which is what a selector change
	// or a decoder regression would produce. A single "accepted > 0" floor is not enough,
	// because the *interesting* assertions are the ones about functions, bodies and
	// instructions, and those have their own much smaller populations: 74 accepted modules
	// with zero bodies would pass an accepted-count floor while the body, zip and
	// index-space checks all said nothing.
	//
	// So each stratum the control makes a claim about carries its own floor, at the measured
	// value with room for upstream churn. These are floors rather than equalities: the
	// numbers rise as the text path lands and as gates flip, and a floor that tracks the
	// measurement is the honest form (0013).
	if accepted < 70 {
		t.Fatalf("only %d accepted binary modules, want ≥70 (measured 74): every assertion "+
			"in this control is inside that loop, so a smaller population means the control "+
			"is agreeing with an empty set rather than checking a form", accepted)
	}
	if withTypes < 30 {
		t.Errorf("only %d modules retained a type section, want ≥30 (measured 32): the "+
			"type-index and signature claims above have no subject below this", withTypes)
	}
	if boundFuncs < 20 {
		t.Errorf("only %d functions were bound to a type index, want ≥20 (measured 26): the "+
			"zip claim — the one piece of retention that spans two sections — is what this "+
			"floor protects", boundFuncs)
	}
	if instrs < 15 {
		t.Errorf("only %d instructions retained across every accepted module, want ≥15 "+
			"(measured 18): the instruction-level retention is the largest part of the form "+
			"and the easiest to have built nothing at all", instrs)
	}
}

// TestRetainedFormIsPerDecode asserts a reused Decoder does not carry one module's form
// into the next.
//
// The stateful-instrument law (#28) pointed at the retention: `Decoder.m` is per-decode
// state exactly as `sawDataRef` is, and the reset lives at the top of DecodeModule rather
// than at the bottom so the zero-value path is covered too. Nothing in the suite exercises
// this — a rejection-only record cannot — and the failure it prevents is a module reporting
// a *previous* module's functions, which is grave #36's class at whole-module scale.
//
// Two real images from the corpus rather than fixtures, so the vectors have provenance: the
// first two distinct accepted modules the suite offers, decoded through one Decoder in
// sequence and then each through its own.
func TestRetainedFormIsPerDecode(t *testing.T) {
	requireSuite(t)

	var images [][]byte
	for _, path := range suitePaths(t) {
		s, err := ParseFile(path)
		if err != nil {
			continue
		}
		for _, c := range s.Commands {
			if c.Kind != KindModuleBinary {
				continue
			}
			m, err := binary.DecodeModule(c.Module)
			// Want modules with *content*, since a pair of empty ones would agree
			// perfectly whether or not the state resets — the vacuity class again.
			if err != nil || len(m.Funcs) == 0 {
				continue
			}
			images = append(images, c.Module)
			if len(images) == 2 {
				break
			}
		}
		if len(images) == 2 {
			break
		}
	}
	if len(images) < 2 {
		t.Fatalf("found %d accepted binary modules with functions, need 2: a reuse test "+
			"needs two decodes to compare", len(images))
	}

	// Fresh decoders, one image each: the reference readings.
	var want []int
	for _, img := range images {
		m, err := binary.DecodeModule(img)
		if err != nil {
			t.Fatalf("re-decoding an accepted image failed: %v", err)
		}
		want = append(want, len(m.Funcs))
	}

	// One decoder, both images in sequence. The counts must match the fresh readings
	// exactly — a form that accumulated would give the second decode both modules'
	// functions, and one that leaked would give it the first's.
	d := &binary.Decoder{}
	for i, img := range images {
		m, err := d.DecodeModule(img)
		if err != nil {
			t.Fatalf("image %d failed on a reused decoder: %v", i, err)
		}
		if got := len(m.Funcs); got != want[i] {
			t.Errorf("image %d on a reused decoder retained %d functions, want %d: the "+
				"retained form is per-decode state and a decode that reports another "+
				"module's functions is the engine lying about its input",
				i, got, want[i])
		}
	}
}
