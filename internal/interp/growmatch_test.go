// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// growMatchIters is the number of (grow ‖ link) pairs each control below runs. Each iteration
// builds its own supplier, so each pair is a fresh address with exactly one write and one read
// against it — which is what keeps the detector's shadow memory from having to retain an access
// across unrelated traffic for the report to be possible.
const growMatchIters = 200

// growMatchImporter decodes an importing module once, outside the loop, so that the racing pair is
// as close together as it can be made: `link1` would put a text encode, a decode and a validation
// pass between the goroutine's release and the matcher's read, which prices the fixed cost of the
// front end into the window rather than the field access.
func growMatchImporter(t *testing.T, src string) *binary.Module {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode importer: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode importer: %v", err)
	}
	return m
}

// TestImportMatchingDoesNotRaceAGrowingMemory is #663's memory arm: `grow` and import matching
// must not both reach one field with one of them writing.
//
// # The subject is a *duplicated* fact, not an unsynchronised one
//
// The control is named for the rule rather than for today's field, because the repair is a
// deletion: the published image's length is the authority for a memory's current size, and
// `limits.Min` was a second copy of it that `grow` kept current with a plain write while `link`
// read it holding nothing. A later reader who re-introduces a cached size — under a lock, under an
// atomic, or as a third copy — brings back the defect this control names even though the line it
// first fired on is gone. *A control names a risk, not a code shape.*
//
// # The oracle is `-race`, and `make check` does not run it
//
// Both accesses are single-word, so neither can produce a torn value on either architecture this
// engine targets: the wrongness is definitional — an unsynchronised read/write pair, undefined by
// the Go memory model — and only a detector can see it. So this control's verdict lives in CI's
// `race` step, inside the two-architecture `build` job, and a green `make check` says nothing about
// its subject. That is the channel `globaltear_test.go`'s controls name, for the same reason and
// with the same caveat stated rather than assumed.
//
// # Watched die
//
// With `m.limits.Min = newSize` at the end of `grow` — the shape `main` carried before this slice —
// `go test -race -run TestImportMatchingDoesNotRaceAGrowingMemory ./internal/interp/` reports
// `WARNING: DATA RACE` against the write in `internal/interp/memory.go:memory.grow`.
//
// **The arm has two read sites, not one**, which is a finding rather than a detail:
// `internal/interp/link.go:importTypeMismatch` reads the field once to *decide*
// (`matchMemoryType`, which inlines into it, so no frame names the matcher) and again to *render*
// the refusal through `externMemory`. Which of the two the detector names varies with how much work
// precedes them — an earlier shape of this control, going through `link1`, drew reports at both;
// this one consistently names the rendering read, being the more recent of the pair the detector
// retained. So a reader matching a report against this paragraph should expect one site of two and
// should not hunt for the matcher's own frame, which the compiler removed. Both sites are the
// repair's business; a fix that fed only the verdict from the image would leave the message racing.
//
// # Why there is no interleaving arm, which is the opposite of the sibling control's answer
//
// `globaltear_test.go`'s controls require the two agents to have *observably* overlapped, because a
// tear is a real-time event: a reader that ran entirely before the writer exercised nothing. Copying
// that arm here — counting iterations that observed size 1 against size 2 — was tried first and it
// **failed 0 accepted / 200 refused**, because a `link` does strictly more work before its read than
// a `memory.grow` does before its write, so the read wins every time. The same run under `-race`
// reported the race on every arm anyway, and the reason is that the detector's question is the
// absence of a happens-before edge, not an order of arrival: an unordered pair is a race whichever
// access lands first. So the interleaving arm asserted a stronger property than the oracle needs and
// would have made this control red for something that is not its subject. *Copying a control
// inherits its visible property, not its load-bearing one.*
//
// What vacuity means here instead is that **each side actually performed its access**, and both are
// checked: `grow` is required to return without error, and every iteration's link is required to
// have reached the matcher — which either verdict establishes, since an `incompatible import type`
// refusal *is* the matcher's answer and an acceptance is too. A link that failed earlier (an
// unresolved import, say) would have read nothing, and that is the `default` arm below rather than a
// silent pass. The accept direction — that a grown memory matches an importer against its *current*
// size — is not this control's business and has two witnesses of its own:
// `TestGrownMemoryReexportsItsCurrentSize` and `imports4.wast:19-37`.
func TestImportMatchingDoesNotRaceAGrowingMemory(t *testing.T) {
	importer := growMatchImporter(t, `(module (memory (import "s" "mem") 2))`)

	reached := 0
	for range growMatchIters {
		sup := supplier(t, `(module
			(memory (export "mem") 1)
			(func (export "grow") (result i32) (memory.grow (i32.const 1))))`)

		var wg sync.WaitGroup
		wg.Add(1)
		var growErr error
		go func() {
			defer wg.Done()
			_, growErr = sup.Invoke("grow")
		}()

		_, trap, err := InstantiateLinked(importer, exportsOf(sup))
		wg.Wait()

		if growErr != nil {
			t.Fatalf("grow agent: %v", growErr)
		}
		if trap != nil {
			t.Fatalf("importer trapped: %v", trap)
		}
		switch {
		case err == nil, strings.Contains(err.Error(), "incompatible import type"):
			reached++
		default:
			t.Fatalf("link: %v — neither an acceptance nor an incompatible-import-type refusal, so "+
				"this iteration failed before the matcher and never read the field under test", err)
		}
	}

	if reached != growMatchIters {
		t.Errorf("%d of %d iterations reached the matcher; the rest measured nothing",
			reached, growMatchIters)
	}
}

// TestImportMatchingDoesNotRaceAGrowingTable is the table arm of the same defect, and it exists
// because **#663's body names only the memory copy**. `table.grow` ends with the same plain write to
// the same duplicated fact, read by `matchTableType` on another goroutine's `link`, so the issue's
// list was a registry of where the defect was noticed rather than an inventory of where it is.
// Everything the memory arm's comment says about the channel, the naming, and what vacuity means
// here holds unchanged.
//
// # Watched die
//
// `go test -race -run TestImportMatchingDoesNotRaceAGrowingTable ./internal/interp/` reports
// `WARNING: DATA RACE` against the write in `internal/interp/table.go:table.grow`. This arm has the
// same two read sites, and it can name the matcher: `internal/interp/link.go:matchTableType` does
// **not** inline, being four terms rather than one, so the earlier shape of this control drew a
// report naming it directly where the memory arm could only name its caller. This shape names the
// rendering read, `externTable`'s, for the memory arm's reason.
//
// The accept direction has no corpus witness at all on this side: no `.wast` file in the suite grows
// a table and then re-imports it, which is what `table.grow`'s own comment meant by the memory
// case's sibling being *"not yet measured"*. `TestGrownTableReexportsItsCurrentSize` is that
// witness, added in this slice, and it is what stops the deletion below from losing the fact the
// deleted line was keeping.
func TestImportMatchingDoesNotRaceAGrowingTable(t *testing.T) {
	importer := growMatchImporter(t, `(module (table (import "s" "tab") 2 funcref))`)

	reached := 0
	for range growMatchIters {
		sup := supplier(t, `(module
			(table (export "tab") 1 funcref)
			(func (export "grow") (result i32) (table.grow (ref.null func) (i32.const 1))))`)

		var wg sync.WaitGroup
		wg.Add(1)
		var growErr error
		go func() {
			defer wg.Done()
			_, growErr = sup.Invoke("grow")
		}()

		_, trap, err := InstantiateLinked(importer, exportsOf(sup))
		wg.Wait()

		if growErr != nil {
			t.Fatalf("grow agent: %v", growErr)
		}
		if trap != nil {
			t.Fatalf("importer trapped: %v", trap)
		}
		switch {
		case err == nil, strings.Contains(err.Error(), "incompatible import type"):
			reached++
		default:
			t.Fatalf("link: %v — neither an acceptance nor an incompatible-import-type refusal, so "+
				"this iteration failed before the matcher and never read the field under test", err)
		}
	}

	if reached != growMatchIters {
		t.Errorf("%d of %d iterations reached the matcher; the rest measured nothing",
			reached, growMatchIters)
	}
}

// TestNothingWritesADeclaredTypeAfterConstruction is the static half of decision 0078's subject, and
// it exists because the two runtime instruments each see only part of the risk.
//
// The risk is a second copy of a memory's or a table's *current* size living in its *declared* type —
// what `grow` used to keep current with `m.limits.Min = newSize` (#663). Three instruments now bound
// it, and the reason there are three is that they fail for unrelated reasons:
//
//   - `TestImportMatchingDoesNotRaceAGrowingMemory` / `…Table` catch an **unsynchronised** write, under
//     `-race`, which `make check` does not run.
//   - `TestConcurrentGrowLosesNoPages`'s observer catches a write **under `growMu`** — the correctly
//     synchronised wrong answer — by asserting `limits.Min` is the declared minimum at every sample.
//     Memory only: there is no concurrent-grow twin for tables.
//   - This one catches **any assignment at all**, on either subject, in any function, in the default
//     lane with no detector and no concurrency. It is the only one whose domain is derived rather than
//     exercised: the other two catch a write on a path they happen to drive.
//
// **An AST scan and deliberately not a grep.** Four comments in this package quote
// `m.limits.Min = newSize` as the shape that was deleted, so a textual search reports its own
// documentation — *a grep measures text*, which is the trap #627 paid for. The walk looks at
// assignment statements only, so a sentence about the write is invisible to it and the record of why
// the write went can stay where a reader of `grow` will find it.
//
// **The field, not just the one sub-field.** `limits` as a whole is the declared type: `Max`,
// `HasMax` and the address width never change either, and a repair that moved the duplicate from
// `Min` to a sibling field would satisfy a `Min`-only check. So `x.limits = …` and `x.limits.Anything
// = …` both fail.
//
// # Watched die — three of the four assertions, and which one was not
//
// Both writes put back in one run, `m.limits.Min = newSize` before `memory.grow`'s return and
// `t.limits.Min = newSize` before `table.grow`'s: **both fired**, naming `memory.go:grow` with
// `m.limits.Min` and `table.go:grow` with `t.limits.Min`. One run reports both, which is the point of
// scanning the package rather than driving a path — *run the mutation over the whole package, not one
// fixture's path.*
//
// The two vacuity arms were watched fail separately, because they fail for different reasons and one
// hides the other:
//
//   - **The field name blinded** to something nothing declares: both subject checks fire, reporting
//     the subject *renamed* rather than the population empty. That is the failure mode a scan keyed to
//     an identifier has, and the clean write list underneath it means nothing.
//   - **The body walk blinded** (iterating no declarations): the assignment arm fires alone — the file
//     floor and both subject checks still pass, because the declarations are read by a separate walk
//     over the file. So a scan that reaches no function body reports a clean population and **the floor
//     cannot see it**, which is why the assignment count is asserted and not just the file count.
//
// **The file floor was not watched fail**, and it is a floor rather than a census on purpose: 37 is the
// measured count at the time of writing, not a figure above it, so falsifying it means deleting files
// from the package. What it catches is the walk's directory moving out from under it. *A floor bounds
// the catastrophic case only* — the blinded-body arm above is what covers the silent half.
func TestNothingWritesADeclaredTypeAfterConstruction(t *testing.T) {
	// The subjects, and the field name the scan is keyed to. Both are checked to still *declare* it
	// below, because a scan keyed to an identifier reports a clean population the moment the
	// identifier is renamed — the empty-domain failure a floor alone cannot tell from a pass.
	const field = "limits"
	subjects := map[string]bool{"memory": false, "table": false}

	// filesWhenWritten is a floor and not a census: it catches the package moving out from under this
	// scan, which is the way an AST walk over `os.ReadDir(".")` goes quietly vacuous. Measured by
	// running it, not forecast.
	const filesWhenWritten = 37

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	files, assignments := 0, 0
	type write struct {
		where string
		expr  string
	}
	var writes []write
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files++

		// The declarations first: a struct named in `subjects` must still have a field named
		// `field`, or this scan is looking for a write to something that no longer exists.
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			if _, want := subjects[ts.Name.Name]; !want {
				return true
			}
			for _, f := range st.Fields.List {
				for _, id := range f.Names {
					if id.Name == field {
						subjects[ts.Name.Name] = true
					}
				}
			}
			return true
		})

		// Then the writes. `IncDecStmt` is included because `x.limits.Min++` is an assignment the
		// `AssignStmt` arm does not see, and a bump is exactly how a size counter gets kept current.
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			where := name + ":" + fn.Name.Name
			flag := func(e ast.Expr) {
				sel, ok := e.(*ast.SelectorExpr)
				if !ok {
					return
				}
				// Either `x.limits` itself or a field of it: `x.limits.Min` is a selector whose own
				// X is the `x.limits` selector, so one unwrap covers both shapes and no deeper
				// nesting is possible — `binary.Limits`' fields are all scalars.
				target := sel
				if inner, ok := sel.X.(*ast.SelectorExpr); ok {
					target = inner
				}
				if target.Sel.Name != field {
					return
				}
				var buf strings.Builder
				if perr := printer.Fprint(&buf, fset, e); perr != nil {
					buf.WriteString("<unprintable>")
				}
				writes = append(writes, write{where: where, expr: buf.String()})
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch s := n.(type) {
				case *ast.AssignStmt:
					assignments++
					for _, lhs := range s.Lhs {
						flag(lhs)
					}
				case *ast.IncDecStmt:
					assignments++
					flag(s.X)
				}
				return true
			})
		}
	}

	if files < filesWhenWritten {
		t.Errorf("scanned %d non-test files, and there were %d when this control was written: below "+
			"that the walk has stopped seeing the package rather than the package having shrunk, and "+
			"every assertion here would pass by asking nothing", files, filesWhenWritten)
	}
	if assignments == 0 {
		t.Error("no assignment statement was visited at all: the walk is not reaching function " +
			"bodies, so a write to a declared type would be invisible to it")
	}
	for subject, found := range subjects {
		if !found {
			t.Errorf("no struct named %q with a field named %q: this control is keyed to that "+
				"identifier, so a rename empties its domain and the clean result below means "+
				"nothing. Re-point the scan at the new name rather than deleting it — the risk is a "+
				"second copy of the current size in the declared type, not the spelling of a field",
				subject, field)
		}
	}
	for _, w := range writes {
		t.Errorf("%s assigns to %s: a memory's and a table's `%s` is the **declared** type — what the "+
			"module asked for — and nothing may write it after construction (decision 0078, the "+
			"repair for #663). The current size is the published image's length; `typeOf` is how a "+
			"caller that needs the current type gets one. A second copy kept current here is the "+
			"defect whether or not it is locked: locked, it is a correctly synchronised wrong answer; "+
			"unlocked, it is the data race `growmatch_test.go`'s two `-race` controls fire on",
			w.where, w.expr, field)
	}
}
