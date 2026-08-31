// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The threads lane's 41 failures are all of one kind: **the engine answers on a current authority
// and the vector asks in a superseded form.** Not one is an engine defect, which is a claim that
// needs a per-row record rather than a sentence, and this file is that record.
//
// # Why per row, and not a column
//
// The first proposal was a `superseded` verdict column beside pass/fail. Scott ruled it out on this
// project's own grounds: *"an aggregate 'superseded' column is a bucket where a real regression
// hides."* A column would score 41 today and 41 the day an atomic starts trapping wrongly, because
// nothing about the aggregate says which 41. So each row is named, and two constraints came with the
// ruling — every reason **cites its authority**, and every reason **names which suite the corpus's
// reading conflicts with**, so the mutual exclusivity is visible rather than implied. Where that
// conflict has a price, the price is measured and quoted here, because *cheap is a grammar claim*
// and so is expensive.
//
// # What this asserts that the lane's own pins do not
//
// `threadsLane` pins `Fail: 30` and `Fail: 11` per file. That is a count, and a count cannot tell a
// fixed row from a newly broken one — swap any failure for any other and both pins still hold. This
// control pins the **identity** of every failing row and refuses two ways: a failure with no entry
// is a new defect, and an entry with no failure is a stale list. The board column is deliberately
// left reading 41: these vectors *do* fail against their own text, and laundering them into passes
// would be the dishonest board that [engine.md] forbids. What is recorded here is why each one is
// not the engine's to fix.
//
// # The five findings, with the measurements behind them
//
// Five findings, **seven register entries** — the count differs on purpose, and the two splits are
// stated where they happen: the cascade rows are their own entry so a reader counting defects does
// not meet 22 of them, and multiple-tables and multiple-memories are separate because their costs were
// measured separately.
//
// Every figure below was printed by an instrument before it was written down. The two rule costs
// come from *neutering*: implementing the rule the corpus asks for and reading the core board,
// because arguing from absence about what a rule would break is the thing this project does not do.
//
//   - **The bare-index segment forms** (22 rows). `(data 0 …)` and `(elem 0 …)` are Wasm 1.0 syntax.
//     `Tmemuse_` and `Ttableuse_` in the normative wasm-latest grammar have exactly one production
//     each — the parenthesized `(memory x)` / `(table x)` — and both pins' `parser.mly` agree
//     (`memoryuse : LPAR MEMORY idx RPAR`, `bindidx : VAR`, so a bare `0` is neither the index nor a
//     binding). Only wabt 1.0.41 accepts the form, as legacy compatibility. 4 module rows, and 18
//     `invoke` rows that cascade from them.
//   - **Multiple memories / multiple tables** (8 rows). Rules Wasm 2.0 and 3.0 removed. The core
//     suite asserts neither — its only mentions of the phrase are the comments `;; No multiple
//     memories yet.` Implementing them costs **33** and **138** core-board failures respectively,
//     measured separately because a joint figure attributed to two rows is the partition trap; note
//     33+138 is 171 against 169 for both at once, so two modules are claimed by whichever rule
//     fires first and the split is not additive.
//   - **The max-pages message** (6 rows). Ours is verbatim the current pin, `2^16 pages (4 GiB) for
//     i32` (`spec/valid.ml:205`); the vector wants the threads pin's older `65536 pages (4GiB)`
//     (`spec-threads/valid.ml:603`). Aligning to the vector would be adjudicating for the snapshot,
//     which is what [ADR 0049] and Scott's #546 ruling forbid: the standard outranks the snapshot.
//   - **Malformed versus invalid** (3 rows). The current spec moved this rejection from the parser to
//     the validator when limits became u64 — `decodeLimits`' own comment states the layering (grave
//     #36). The core suite carries the *same module text* as `assert_invalid` + `"memory size"` and
//     we pass every one. Satisfying the threads reading breaks 9 of them.
//   - **`spectest.shared_memory`** (2 rows). No pinned reference exports it. The threads pin
//     *defines* the value at `spectest.ml:25` and never adds a `lookup'` arm, so it is dead code
//     upstream; the core pin has no such value at all. This is the one group where the vector asks
//     for something no authority provides, rather than something a newer authority changed.
//
// [engine.md]: ../../docs/laws/engine.md
// [ADR 0049]: ../../docs/decisions/0049-atomic-alignment-is-checked-on-the-effective-address-because-the-proposals-normative-prose-outranks-its-own-reference-interpreter.md

// supersededReason is one finding, and the board rows it accounts for.
//
// The three prose fields are all required, checked below rather than trusted: an entry added with an
// empty Conflict would satisfy a count pin while recording nothing, and *an exemption inherits none
// of the trigger's lessons* — the allow-list side of a control is written later and more casually
// than the side that fires.
type supersededReason struct {
	// What the engine does, and why that is the right answer.
	What string
	// Authority is the pinned artifact that says so. A path, so it resolves.
	Authority string
	// Conflict names the suite whose vectors contradict this one and what satisfying the corpus
	// would cost — or says plainly that no vector conflicts and names what does.
	Conflict string
	// Rows are the board rows, `file:line`, one entry per failing row.
	Rows []string
}

// threadsSupersededTotal is the lane's whole failure population, pinned so a new row cannot join
// silently — the second of the two constraints the ruling carried. It is the sum of the Rows
// lengths below and is asserted against both the table and the measured board.
const threadsSupersededTotal = 41

var threadsSuperseded = []supersededReason{
	{
		What: "The module is refused because `(data 0 …)` / `(elem 0 …)` puts the memory or table " +
			"index in a bare position that current syntax does not have.",
		Authority: "third_party/spec/spectec/test-frontend/TEST.md, `grammar Tmemuse_` and " +
			"`grammar Ttableuse_` (one production each, the parenthesized use); " +
			"third_party/spec/interpreter/text/parser.mly:1095-1110 and " +
			"third_party/spec-threads/interpreter/text/parser.mly `data`/`memory_use`",
		Conflict: "No vector conflicts: the form appears nowhere in the core suite, so nothing " +
			"would break. The conflict is with the normative grammar itself — accepting it means " +
			"parsing a form three current authorities refuse, for the benefit of one superseded " +
			"file. wabt 1.0.41 does accept it, as Wasm 1.0 legacy.",
		Rows: []string{
			"imports.wast:271", "imports.wast:290", "imports.wast:381", "imports.wast:393",
		},
	},
	{
		What: "No instance exists to invoke, because the preceding module command is one of the " +
			"four bare-index modules above and never parsed.",
		Authority: "Cascade only — same authority as the entry above; these rows have no " +
			"independent verdict and would pass or fail with it.",
		Conflict: "Inherited. Recorded as its own reason rather than folded in, because 18 of the " +
			"22 rows in that finding are consequences and a reader counting engine defects should " +
			"not meet them as 22 separate ones.",
		Rows: []string{
			"imports.wast:283", "imports.wast:284", "imports.wast:285", "imports.wast:286",
			"imports.wast:287", "imports.wast:302", "imports.wast:303", "imports.wast:304",
			"imports.wast:305", "imports.wast:306", "imports.wast:388", "imports.wast:389",
			"imports.wast:390", "imports.wast:391", "imports.wast:399", "imports.wast:400",
			"imports.wast:401", "imports.wast:402",
		},
	},
	{
		What: "The module validates, because a module may declare more than one table since the " +
			"reference-types proposal shipped in Wasm 2.0.",
		Authority: "third_party/spec/interpreter/valid/valid.ml has no table-count require; the " +
			"core suite asserts no such rule",
		Conflict: "Directly with the core suite: implementing `at most one table` scores **138** " +
			"failures on the core board (60957 pass/0 fail becomes 60819/138), measured by adding " +
			"the rule and reading the board.",
		Rows: []string{"imports.wast:309", "imports.wast:313", "imports.wast:317"},
	},
	{
		What: "The module validates, because a module may declare more than one memory in Wasm 3.0.",
		Authority: "third_party/spec/interpreter/valid/valid.ml has no memory-count require; the " +
			"core suite's only mentions of the phrase are the comments `;; No multiple memories " +
			"yet.` at testdata/spec/exports.wast:192 and :226",
		Conflict: "Directly with the core suite: implementing `at most one memory` scores **33** " +
			"failures on the core board (60957/0 becomes 60924/33), measured the same way.",
		Rows: []string{
			"imports.wast:404", "imports.wast:408", "imports.wast:412",
			"memory.wast:14", "memory.wast:15",
		},
	},
	{
		What: "The module is rejected, correctly, with the current reference's own wording — " +
			"`memory size must be at most 2^16 pages (4 GiB) for i32`. The vector wants the " +
			"threads pin's older `65536 pages (4GiB)`, so the two disagree on the string and not " +
			"on the verdict.",
		Authority: "third_party/spec/interpreter/valid/valid.ml:205 (`I32AT -> 0x1_0000L, \"2^16 " +
			"pages (4 GiB) for i32\"`) against " +
			"third_party/spec-threads/interpreter/valid/valid.ml:603",
		Conflict: "No vector conflicts — the core suite has no max-pages expectation at all, so " +
			"this engine's wording had never been asked by any suite until this lane existed. The " +
			"conflict is between the two pins, and ADR 0049 settles that direction: the standard " +
			"outranks the snapshot. Aligning to the vector would adjudicate for the snapshot.",
		Rows: []string{
			"memory.wast:58", "memory.wast:62", "memory.wast:66",
			"memory.wast:70", "memory.wast:74", "memory.wast:78",
		},
	},
	{
		What: "The limit parses and the validator rejects it. The vector wants the *parser* to " +
			"refuse it as malformed, which is where the rule lived before limits became u64.",
		Authority: "internal/binary/sections.go decodeLimits states the layering and its witness " +
			"(grave #36): reading the field narrowly here would be the decoder borrowing the " +
			"validator's job and getting the malformed string wrong to do it",
		Conflict: "Directly with the core suite, which carries the same module text as " +
			"`assert_invalid` + `\"memory size\"` and needs it to parse: 6 rows at " +
			"testdata/spec/memory.wast:78,82,86,91,95,99 and 3 at testdata/spec/table.wast:36,40,44. " +
			"Nine core rows against these three, and no reading satisfies both.",
		Rows: []string{"memory.wast:83", "memory.wast:87", "memory.wast:91"},
	},
	{
		What: "The import fails as `unknown import`, because the harness's spectest fixture has no " +
			"`shared_memory` export — and neither does any pinned reference.",
		Authority: "third_party/spec-threads/interpreter/host/spectest.ml:25 defines the value and " +
			"its `lookup'` (:40-57) has no arm for the name, so it is unreachable upstream; " +
			"third_party/spec/interpreter/host/spectest.ml has no such value",
		Conflict: "No suite conflict, and no authority to satisfy: this is the one group where the " +
			"vector asks for something upstream never finished wiring, rather than something a " +
			"newer authority changed. The accept direction it would have witnessed has a unit " +
			"witness instead — see internal/interp/link.go's matchMemory comment and grave #522.",
		Rows: []string{"imports.wast:499", "imports.wast:501"},
	},
}

// TestThreadsLaneFailuresAreSupersededCorpusRows pins the identity of every failing row in the
// threads lane against the register above, in both directions.
//
// **Watched die eight ways before it was believed**, since a register-versus-measurement control is
// mostly agreement and agreement is what a dead one looks like:
//
//  1. a row dropped from the register — the count pin and *does not account for* both fire;
//  2. a row re-pointed one line off (58→59) — both directions fire at once, which is the stale-citation
//     case and the reason the check is bidirectional rather than a subset test;
//  3. the register drained — 41 unaccounted rows, not a vacuous pass;
//  4. the board scoring nothing — the floor below fires, where an empty register would have agreed;
//  5. one reason stripped of its `Conflict`; 6. one stripped of both citation constraints — reported
//     as two errors, which is why the guard is three ifs and not a switch;
//  7. a reason accounting for no rows; 8. two reasons claiming one row.
//
// Two of those were *not* born on the first attempt: mutations 4 and 5 initially printed `FAIL` over a
// build error — `declared and not used` and a missing paren — so the control had not run at all and the
// word FAIL was the compiler's. A falsification that does not compile measures nothing, and it looks
// exactly like a falsification that worked.
func TestThreadsLaneFailuresAreSupersededCorpusRows(t *testing.T) {
	want := map[string]int{}
	for i, r := range threadsSuperseded {
		// Three independent ifs and not a switch, which is what the first draft used: a switch
		// reports the first missing field and stops, so an entry stripped of both its citation
		// constraints would be corrected once, re-run, and fail again on the half nobody was told
		// about. Each field is a separate claim and gets a separate verdict.
		if r.What == "" {
			t.Errorf("threadsSuperseded[%d] has no What: an entry that records no verdict "+
				"satisfies the count pin and says nothing", i)
		}
		if r.Authority == "" {
			t.Errorf("threadsSuperseded[%d] has no Authority, which is the first of the two "+
				"constraints this register was ordered under", i)
		}
		if r.Conflict == "" {
			t.Errorf("threadsSuperseded[%d] has no Conflict, which is the second: the mutual "+
				"exclusivity must be visible rather than implied", i)
		}
		if len(r.Rows) == 0 {
			t.Errorf("threadsSuperseded[%d] accounts for no rows", i)
		}
		for _, k := range r.Rows {
			if j, dup := want[k]; dup {
				t.Errorf("row %s is claimed by both threadsSuperseded[%d] and [%d]; two reasons "+
					"for one row means one of them is wrong", k, j, i)
				continue
			}
			want[k] = i
		}
	}
	if len(want) != threadsSupersededTotal {
		t.Errorf("the register names %d rows, threadsSupersededTotal is %d — a new row joined or "+
			"left without the pin moving, which is exactly what the pin is for",
			len(want), threadsSupersededTotal)
	}

	// The domain is derived, not enumerated: the same call the lane itself uses, so a fifth vector
	// file appearing upstream enters this control's population without anyone remembering to add it.
	paths := testenv.RequireProposal(t, suiteDir, threadsProposal, threadsLaneFiles)

	f := binary.DefaultFeatures()
	f.Threads = true
	_, engineFor := gateLane(f)

	got := map[string]int{}
	for _, p := range paths {
		name := filepath.Base(p)
		s, err := ParseFile(p)
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		r := s.RunGated(engineFor())
		for _, fs := range r.Buckets {
			for _, fl := range fs {
				got[fmt.Sprintf("%s:%d", name, fl.Line)]++
			}
		}
	}

	// A floor before any comparison. A lane that scored nothing — a fixture path that moved, a
	// gate that stopped configuring — would agree with an empty register perfectly.
	if len(got) == 0 {
		t.Fatal("the threads lane produced no failures at all, so this control compared two empty " +
			"sets and would have agreed with any register; the fixtures or the gate are wrong")
	}

	total := 0
	for k, n := range got {
		total += n
		if n > 1 {
			t.Errorf("row %s failed %d times; the register keys by file:line and would silently "+
				"collapse them", k, n)
		}
		if _, ok := want[k]; !ok {
			t.Errorf("%s is a threads-lane failure this file does not account for. Either it is a "+
				"real defect — the first one this lane has had — or it is a further superseded "+
				"row needing its own authority and conflict. It is not allowed to be neither.", k)
		}
	}
	for k, i := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("threadsSuperseded[%d] names %s, which does not fail. Either the row now "+
				"passes and its entry should go, or a line moved and the citation is stale; a "+
				"register nobody prunes is a register that stops meaning anything.", i, k)
		}
	}
	if total != threadsSupersededTotal {
		t.Errorf("the lane scored %d failures, threadsSupersededTotal is %d", total, threadsSupersededTotal)
	}

	// **Guarded on `!t.Failed()`, because the unguarded version printed `0 engine defects` beneath
	// every one of the eight falsifications** — including the one whose message was that a failure
	// had no entry, which is the candidate engine defect this line was denying in the same output.
	// A summary that states the conclusion the run just refuted is testimony asserting the property
	// the code lacks, and a reader skimming for the last line would have read the denial.
	if !t.Failed() {
		t.Logf("threads lane: %d failures, all registered as superseded-corpus rows across %d "+
			"register entries; 0 engine defects", total, len(threadsSuperseded))
	}
}
