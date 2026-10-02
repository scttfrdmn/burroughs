// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// # The equality witness over the Canonical ABI's built-in set (#771, ADR 0092)
//
// `testdata/canon-builtins.tsv` is the spec's own list, generated from `Binary.md`'s `canon` production at
// `CANON_PIN` and committed as the oracle's reading. `canonStatus` below is **Burroughs' claim** about each
// one. These two are compared **in both directions**, which is the part that makes the witness worth having:
//
//   - a production in the table with no authored status fails, so a pin bump that adds a built-in cannot land
//     unclassified;
//   - an authored status naming a production that no longer exists fails, so a pin bump that REMOVES one
//     cannot leave a dead claim behind.
//
// ## Why the status is authored and not generated
//
// A status derived from the engine would make the comparison compare the engine to itself, and would pass
// whatever the engine did. The statuses that make a claim about the engine — `built` and `refusedByName` —
// are therefore checked against `isBuiltAsyncBuiltin` as a third, independent reading, again in both
// directions. The classification cannot drift from the code without a failure, and the code cannot drift
// from the spec without a failure.

// canonBuiltinStatus is Burroughs' status for one canonical built-in. The set is closed: `statusOf` refuses
// a value outside it, because "unclassified" must be a failure rather than a bucket.
type canonBuiltinStatus string

const (
	// statusLifting is `canon lift` / `canon lower` — the lifting operations. Not built-ins at all: they
	// carry the `async` canonopt that gates the tier, but they are not members of the async built-in set,
	// and the demand set names one (`[async-lower]tick`) that no opcode in isBuiltAsyncBuiltin can cover.
	statusLifting canonBuiltinStatus = "lifting-operation"
	// statusResource is resource.new/drop/rep — modeled since ADR 0084, behind gate:components (on by
	// default). Decoded into their own CanonKinds, not as CanonAsyncBuiltin.
	statusResource canonBuiltinStatus = "resources"
	// statusAsyncDemandedBuilt: an async built-in the committed suspending guest imports, and which this
	// engine executes.
	statusAsyncDemandedBuilt canonBuiltinStatus = "async-demanded-built"
	// statusAsyncDemandedAbsent: an async built-in the committed guest imports and this engine does NOT
	// execute. This is the slice-2 work list, and it is deliberately a distinct status from "refused by
	// name": both refuse, but only this one blocks a guest that exists.
	statusAsyncDemandedAbsent canonBuiltinStatus = "async-demanded-absent"
	// statusAsyncBuiltUndemanded: executed by this engine for an earlier increment, but not imported by
	// the committed guest. Named rather than folded into "built" because the demand list's whole point is
	// that implemented and demanded are different questions.
	statusAsyncBuiltUndemanded canonBuiltinStatus = "async-built-undemanded"
	// statusAsyncRefusedByName: decoded into the graph as CanonAsyncBuiltin and refused at bind with
	// ErrAsyncNotImplemented. The gate is open; the mechanism is not there.
	statusAsyncRefusedByName canonBuiltinStatus = "async-refused-by-name"
	// statusErrorContext is the 📝 error-context family — a separate proposal from 🔀 async, refused as an
	// unmodeled kind at marshal rather than by the async tier.
	statusErrorContext canonBuiltinStatus = "error-context"
	// statusThreads is the 🧵 shared-everything-threads family, and statusThreadsPhase2 its ② subset. Both
	// are a DECODE refusal, not a gate refusal: `canon`'s default rejects them before any gate is asked.
	// Their gate is `gate:threads`, whose own state is unrelated to gate:async.
	statusThreads       canonBuiltinStatus = "threads"
	statusThreadsPhase2 canonBuiltinStatus = "threads-phase2"
)

// canonStatus is the authored classification: every production in the committed table, with exactly one
// status. Ordered as Binary.md orders them so a diff against the table reads straight.
//
// `thread.yield` (0x0c) is in the ASYNC family and not the threads one, which looks wrong and is not: the
// spec marks it 🔀, not 🧵. The marker is followed rather than the name, because the marker is what says
// which proposal owns the opcode.
var canonStatus = map[string]canonBuiltinStatus{
	"lift":          statusLifting,
	"lower":         statusLifting,
	"resource.new":  statusResource,
	"resource.drop": statusResource,
	"resource.rep":  statusResource,

	"backpressure.inc": statusAsyncRefusedByName,
	"backpressure.dec": statusAsyncRefusedByName,

	"task.return": statusAsyncDemandedBuilt,
	"task.cancel": statusAsyncDemandedAbsent, // the only demanded-and-absent one: slice 2's engine work
	"context.get": statusAsyncDemandedBuilt,
	"context.set": statusAsyncDemandedBuilt,

	"subtask.cancel": statusAsyncDemandedBuilt,
	"subtask.drop":   statusAsyncDemandedBuilt,

	"stream.new":           statusAsyncBuiltUndemanded,
	"stream.read":          statusAsyncRefusedByName,
	"stream.write":         statusAsyncBuiltUndemanded,
	"stream.cancel-read":   statusAsyncBuiltUndemanded,
	"stream.cancel-write":  statusAsyncBuiltUndemanded,
	"stream.drop-readable": statusAsyncBuiltUndemanded,
	"stream.drop-writable": statusAsyncBuiltUndemanded,

	"future.new":           statusAsyncBuiltUndemanded,
	"future.read":          statusAsyncBuiltUndemanded,
	"future.write":         statusAsyncBuiltUndemanded,
	"future.cancel-read":   statusAsyncRefusedByName,
	"future.cancel-write":  statusAsyncBuiltUndemanded,
	"future.drop-readable": statusAsyncBuiltUndemanded,
	"future.drop-writable": statusAsyncRefusedByName,

	"error-context.new":           statusErrorContext,
	"error-context.debug-message": statusErrorContext,
	"error-context.drop":          statusErrorContext,

	"waitable-set.new":  statusAsyncDemandedBuilt,
	"waitable-set.wait": statusAsyncBuiltUndemanded, // built; the committed guest POLLS rather than waits
	"waitable-set.poll": statusAsyncDemandedBuilt,
	"waitable-set.drop": statusAsyncDemandedBuilt,
	"waitable.join":     statusAsyncDemandedBuilt,

	"thread.index":                statusThreads,
	"thread.new-indirect":         statusThreads,
	"thread.resume-later":         statusThreads,
	"thread.suspend":              statusThreads,
	"thread.yield":                statusAsyncRefusedByName, // 🔀 in the spec, not 🧵
	"thread.suspend-then-resume":  statusThreads,
	"thread.yield-then-resume":    statusThreads,
	"thread.yield-then-promote":   statusThreads,
	"thread.suspend-then-promote": statusThreads,

	"thread.spawn-ref":             statusThreadsPhase2,
	"thread.spawn-indirect":        statusThreadsPhase2,
	"thread.available-parallelism": statusThreadsPhase2,
}

// claimsBuilt reports whether a status claims this engine EXECUTES the built-in. Only two statuses do, and
// they are the two compared against isBuiltAsyncBuiltin.
func (s canonBuiltinStatus) claimsBuilt() bool {
	return s == statusAsyncDemandedBuilt || s == statusAsyncBuiltUndemanded
}

// claimsDemanded reports whether a status claims the committed suspending guest imports the built-in.
func (s canonBuiltinStatus) claimsDemanded() bool {
	return s == statusAsyncDemandedBuilt || s == statusAsyncDemandedAbsent
}

// isAsyncFamily reports whether a status places the production in the 🔀 async family — the only family
// whose members isBuiltAsyncBuiltin is asked about at all.
func (s canonBuiltinStatus) isAsyncFamily() bool {
	switch s {
	case statusAsyncDemandedBuilt, statusAsyncDemandedAbsent, statusAsyncBuiltUndemanded,
		statusAsyncRefusedByName:
		return true
	case statusLifting, statusResource, statusErrorContext, statusThreads, statusThreadsPhase2:
		return false
	}
	return false
}

// canonRow is one production as the committed table records it.
type canonRow struct {
	ops    []byte // every opcode byte of the form; ops[0] is what the engine keys on
	name   string
	marker string // "", "async", "error-context", "threads", "threads-phase2"
}

// readCanonTable parses the committed table, returning the pin and the productions.
func readCanonTable(t *testing.T) (string, []canonRow) {
	t.Helper()
	path := filepath.Join("testdata", "canon-builtins.tsv")
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("the committed spec table is missing, so this witness has no oracle: %v", err)
	}
	defer fh.Close()

	var pin string
	var rows []canonRow
	sc := bufio.NewScanner(fh)
	for ln := 1; sc.Scan(); ln++ {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if f[0] == "pin" {
			if len(f) != 2 {
				t.Fatalf("%s:%d: the pin line is malformed: %q", path, ln, line)
			}
			pin = f[1]
			continue
		}
		if len(f) != 3 {
			t.Fatalf("%s:%d: want 3 tab-separated fields, got %d: %q", path, ln, len(f), line)
		}
		var ops []byte
		for _, tok := range strings.Fields(f[0]) {
			v, convErr := strconv.ParseUint(strings.TrimPrefix(tok, "0x"), 16, 8)
			if convErr != nil {
				t.Fatalf("%s:%d: opcode %q is not a hex byte: %v", path, ln, tok, convErr)
			}
			ops = append(ops, byte(v))
		}
		if len(ops) == 0 {
			t.Fatalf("%s:%d: no opcode bytes: %q", path, ln, line)
		}
		rows = append(rows, canonRow{ops: ops, name: f[1], marker: f[2]})
	}
	if scanErr := sc.Err(); scanErr != nil {
		t.Fatalf("reading %s: %v", path, scanErr)
	}
	if pin == "" {
		t.Fatalf("%s records no pin; the table cannot be tied to a spec revision", path)
	}
	// A floor, stated as a floor: the table is generated and its real size is 47 at this pin, but a bound
	// here is about catching a truncated or empty file, not about pinning the population. The exact
	// population is pinned by the two-directional comparison against canonStatus, which is the instrument
	// for that question.
	if len(rows) < 40 {
		t.Fatalf("%s holds only %d productions; the generator produced a truncated table", path, len(rows))
	}
	return pin, rows
}

// TestCanonTablePinMatchesTheMakefile ties the committed table to the revision the Makefile fetches.
//
// Without this the table is a file with a date on it: CANON_PIN could move, `make canon-fixtures` could
// regenerate the ABI fixtures against a newer spec, and this table — with the status classification resting
// on it — would keep describing the old one with nothing failing. A pin bump must regenerate the table.
func TestCanonTablePinMatchesTheMakefile(t *testing.T) {
	pin, _ := readCanonTable(t)

	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("reading the Makefile, which holds the pin: %v", err)
	}
	m := regexp.MustCompile(`(?m)^CANON_PIN\s*:=\s*([0-9a-f]{40})\s*$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no CANON_PIN assignment found in the Makefile. If it was renamed, this witness must be " +
			"re-pointed: a control that cannot find its subject is not passing, it is blind.")
	}
	if got := string(m[1]); got != pin {
		t.Errorf("the committed table is at pin %s but the Makefile's CANON_PIN is %s.\n"+
			"Run `make canon-builtins` to regenerate the table, then re-classify any production the "+
			"bump added or removed — canonStatus must stay total over it.", pin, got)
	}
}

// TestEveryCanonProductionHasExactlyOneStatus is the totality control, in both directions.
func TestEveryCanonProductionHasExactlyOneStatus(t *testing.T) {
	_, rows := readCanonTable(t)

	// --- direction 1: every production in the spec has an authored status ---
	named := make(map[string]bool, len(rows))
	var unclassified []string
	for _, r := range rows {
		named[r.name] = true
		if _, ok := canonStatus[r.name]; !ok {
			unclassified = append(unclassified, fmt.Sprintf("%#x %s (%s)", r.ops[0], r.name, r.marker))
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("%d canon production(s) at the pin have NO status in canonStatus:\n  %s\n"+
			"Every built-in gets exactly one status. An unclassified production is a hole in the claim "+
			"ADR 0092 makes about this engine's coverage.", len(unclassified), strings.Join(unclassified, "\n  "))
	}

	// --- direction 2: every authored status names a production that exists ---
	var dead []string
	for name := range canonStatus {
		if !named[name] {
			dead = append(dead, name)
		}
	}
	if len(dead) > 0 {
		sort.Strings(dead)
		t.Errorf("%d authored status(es) name a production NOT in the spec at this pin:\n  %s\n"+
			"A pin bump that removes or renames a built-in must remove its status too; a claim about a "+
			"built-in that no longer exists reads as coverage and is not.", len(dead), strings.Join(dead, "\n  "))
	}

	// --- the status values themselves are a closed set ---
	for name, st := range canonStatus {
		if !st.isAsyncFamily() && st != statusLifting && st != statusResource && st != statusErrorContext &&
			st != statusThreads && st != statusThreadsPhase2 {
			t.Errorf("%s carries status %q, which is outside the closed set", name, st)
		}
	}

	// --- the marker a production carries must agree with the family its status puts it in ---
	// This is what stops a misreading of the spec from hiding inside a plausible-looking status: thread.yield
	// is 🔀 and classified async, and the only way to know that is the marker.
	for _, r := range rows {
		st, ok := canonStatus[r.name]
		if !ok {
			continue // already reported above
		}
		var want string
		switch {
		case st.isAsyncFamily():
			want = "async"
		case st == statusErrorContext:
			want = "error-context"
		case st == statusThreads:
			want = "threads"
		case st == statusThreadsPhase2:
			want = "threads-phase2"
		default:
			want = "" // lift/lower and the resource built-ins carry no proposal marker
		}
		if r.marker != want {
			t.Errorf("%s is marked %q in the spec but its status %q belongs to the %q family",
				r.name, r.marker, st, want)
		}
	}
}

// TestEngineBuiltSetEqualsTheClassification compares isBuiltAsyncBuiltin against the authored statuses, in
// both directions, over the whole async family.
//
// This is the arm that can actually catch an engine change: adding an opcode to the switch without
// reclassifying it fails here, and so does classifying one as built without implementing it.
func TestEngineBuiltSetEqualsTheClassification(t *testing.T) {
	_, rows := readCanonTable(t)

	var claimedBuiltNotExecuted, executedNotClaimed []string
	asyncSeen := 0
	for _, r := range rows {
		st, ok := canonStatus[r.name]
		if !ok || !st.isAsyncFamily() {
			continue
		}
		asyncSeen++
		engine := isBuiltAsyncBuiltin(r.ops[0])
		switch {
		case st.claimsBuilt() && !engine:
			claimedBuiltNotExecuted = append(claimedBuiltNotExecuted,
				fmt.Sprintf("%#x %s (status %s)", r.ops[0], r.name, st))
		case !st.claimsBuilt() && engine:
			executedNotClaimed = append(executedNotClaimed,
				fmt.Sprintf("%#x %s (status %s)", r.ops[0], r.name, st))
		}
	}
	// A vacuity check, because an empty async family would make both lists empty and the arms agree
	// perfectly about nothing.
	if asyncSeen == 0 {
		t.Fatal("no async-family productions were compared; the classification or the table is empty, " +
			"and two empty sets agree perfectly")
	}
	t.Logf("compared %d async-family productions against isBuiltAsyncBuiltin", asyncSeen)

	if len(claimedBuiltNotExecuted) > 0 {
		t.Errorf("classified as BUILT but isBuiltAsyncBuiltin says no:\n  %s",
			strings.Join(claimedBuiltNotExecuted, "\n  "))
	}
	if len(executedNotClaimed) > 0 {
		t.Errorf("isBuiltAsyncBuiltin executes these, but no status claims them built:\n  %s\n"+
			"An opcode added to the switch must be reclassified; otherwise ADR 0092's table understates "+
			"what the engine does, which is the direction nobody checks by hand.",
			strings.Join(executedNotClaimed, "\n  "))
	}

	// isBuiltAsyncBuiltin must not accept an opcode with no production at the pin at all. A stray byte in
	// the switch would otherwise sit there permitting something the spec does not define.
	known := make(map[byte]bool, len(rows))
	for _, r := range rows {
		known[r.ops[0]] = true
	}
	for op := range 256 {
		if isBuiltAsyncBuiltin(byte(op)) && !known[byte(op)] {
			t.Errorf("isBuiltAsyncBuiltin permits %#x, which is NOT a canon production at the pin", op)
		}
	}
}

// TestDemandSetEqualsTheClassification compares the authored `demanded` statuses against the committed
// guest's actual imports, in both directions.
//
// The demand set is read from the artefact — the committed `suspending/component.wasm`, decoded by this
// engine's own loader — rather than from a reading of the spec. That is the point of having committed the
// guest: ADR 0086 deferred the choice to "the first guest that actually lifts an async export", and a list
// of what that guest needs is only worth something if it comes from the guest.
func TestDemandSetEqualsTheClassification(t *testing.T) {
	_, rows := readCanonTable(t)

	path := filepath.Join("testdata", "asynclift", "suspending", "component.wasm")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed suspending guest is missing, so the demand set has no source: %v", err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("loading the committed guest with this engine's own loader: %v", err)
	}

	// The canonical intrinsics are imports of the guest's CORE modules, named in brackets — the component's
	// own import list holds the WIT-level imports instead.
	intrinsics := map[string]bool{}
	for _, m := range c.CoreModules {
		for _, imp := range m.Imports {
			if strings.HasPrefix(imp.Name, "[") {
				intrinsics[imp.Name] = true
			}
		}
	}
	if len(intrinsics) == 0 {
		t.Fatal("the committed guest imports no bracketed intrinsics at all. Either the loader stopped " +
			"reading core module imports or the guest was replaced: an empty demand set would agree with " +
			"an empty classification and prove nothing.")
	}

	// A bracketed intrinsic's name is the canon name with `.` spelled `-`, optionally followed by a
	// suffix: an export name (`[task-return]run`) or a context slot index (`[context-get-0]`). The dash is
	// ambiguous — `[waitable-set-drop]` is waitable-set.drop while `[waitable-join]` is waitable.join — so
	// the match runs the other way: for each production, derive the bracket spelling its name would have
	// and look for it. That way the spec's names drive the comparison and no hand-written map can be wrong.
	slotIdx := regexp.MustCompile(`-\d+$`)
	bracketKeys := map[string]bool{}
	for name := range intrinsics {
		key := name[1:]
		if i := strings.Index(key, "]"); i >= 0 {
			key = key[:i] // drop any export-name suffix after the closing bracket
		}
		bracketKeys[slotIdx.ReplaceAllString(key, "")] = true
	}

	var claimedNotImported, importedNotClaimed []string
	matched := map[string]bool{}
	for _, r := range rows {
		st, ok := canonStatus[r.name]
		if !ok {
			continue
		}
		// A lifting operation lowered or lifted with the `async` canonopt is spelled with that canonopt in
		// front: `canon lower ... async` imports as `[async-lower]<func>`. So a lifting operation has two
		// possible spellings and a built-in has one. Both are derived from the production's name, which
		// keeps the spec's names driving the comparison.
		spellings := []string{strings.ReplaceAll(r.name, ".", "-")}
		if st == statusLifting {
			spellings = append(spellings, "async-"+r.name)
		}
		imported := false
		for _, sp := range spellings {
			if bracketKeys[sp] {
				imported = true
				matched[sp] = true
			}
		}
		switch {
		case st.claimsDemanded() && !imported:
			claimedNotImported = append(claimedNotImported, fmt.Sprintf("%s (status %s)", r.name, st))
		case !st.claimsDemanded() && imported && st != statusLifting:
			// statusLifting is excluded by name, not by accident: the guest imports `[async-lower]tick`,
			// and `lower` is a lifting operation rather than a built-in, so no `demanded` status applies
			// to it. Any OTHER status that turns up imported is a real misclassification.
			importedNotClaimed = append(importedNotClaimed, fmt.Sprintf("%s (status %s)", r.name, st))
		}
	}
	t.Logf("the committed guest imports %d bracketed intrinsics; %d matched a production by name",
		len(intrinsics), len(matched))

	if len(claimedNotImported) > 0 {
		t.Errorf("classified as DEMANDED but the committed guest does not import them:\n  %s",
			strings.Join(claimedNotImported, "\n  "))
	}
	if len(importedNotClaimed) > 0 {
		t.Errorf("the committed guest imports these, but no status claims them demanded:\n  %s\n"+
			"Regenerating the guest can change its demand set; the classification must follow it.",
			strings.Join(importedNotClaimed, "\n  "))
	}

	// Every bracketed intrinsic must have matched some production. An unmatched one means the guest needs
	// something this table cannot even name — which is the case worth failing loudly on, because it is
	// invisible in both lists above.
	var unmatched []string
	for k := range bracketKeys {
		if !matched[k] {
			unmatched = append(unmatched, k)
		}
	}
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		t.Errorf("the guest imports %d intrinsic(s) matching NO canon production at the pin:\n  %s\n"+
			"Either the spelling rule changed or the guest uses a built-in the pin does not define.",
			len(unmatched), strings.Join(unmatched, "\n  "))
	}
}

// TestTheDemandedAbsentSetIsSliceTwosWorkList prints the engine work the committed guest is blocked on, and
// asserts the statuses are self-consistent about it.
//
// Not a tautology over the two tests above: they check the classification against the engine and against the
// guest separately. This asserts the INTERSECTION is what ADR 0092's demand list says it is — a guest import
// that this engine does not execute — and names it, so the slice-2 list is read off an instrument rather
// than off a sentence someone wrote.
func TestTheDemandedAbsentSetIsSliceTwosWorkList(t *testing.T) {
	_, rows := readCanonTable(t)

	var absent, built []string
	for _, r := range rows {
		switch canonStatus[r.name] {
		case statusAsyncDemandedAbsent:
			if isBuiltAsyncBuiltin(r.ops[0]) {
				t.Errorf("%s is classified demanded-ABSENT but the engine executes it", r.name)
			}
			absent = append(absent, fmt.Sprintf("%#x %s", r.ops[0], r.name))
		case statusAsyncDemandedBuilt:
			if !isBuiltAsyncBuiltin(r.ops[0]) {
				t.Errorf("%s is classified demanded-BUILT but the engine does not execute it", r.name)
			}
			built = append(built, fmt.Sprintf("%#x %s", r.ops[0], r.name))
		case statusLifting, statusResource, statusAsyncBuiltUndemanded, statusAsyncRefusedByName,
			statusErrorContext, statusThreads, statusThreadsPhase2:
			// Not part of the demand set, so not part of slice 2's list. Named rather than left to a
			// `default` so that a status added later lands here as a linter failure and gets a decision:
			// whether a new status is demanded is exactly the question this arm exists to answer.
		}
	}
	sort.Strings(absent)
	sort.Strings(built)
	if len(absent)+len(built) == 0 {
		t.Fatal("no demanded built-ins at all; the demand list is empty and says nothing")
	}
	t.Logf("demand set: %d built, %d absent", len(built), len(absent))
	t.Logf("  absent (slice 2's engine work): %s", strings.Join(absent, ", "))
}
