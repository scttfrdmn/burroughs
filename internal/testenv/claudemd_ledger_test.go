// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The per-entry byte ledger for `CLAUDE.md`, and it exists because a total was standing in for a
// ledger over items that are individually countable.
//
// # Why the ceiling was not enough
//
// `claudeMDCeiling` is one number over a file whose entries are enumerable, which is exactly the
// aggregate *a total is not a ledger* demotes (`docs/laws/controls.md`, minted PR #295): **one
// recall key can bloat back into a body while the file total stays green, and trimming an
// unrelated key buys the room for it.** That is the same opposite-sign cancellation the ≤896
// forecast bound suffered — four modules over their own bounds paid for by one 45 under, netting
// to a comfortable −39 — with the difference that here the cancellation is not even accidental:
// the ratchet the restructure exists to stop is *the corpus growing back into the index one
// defensible paragraph at a time*, and the cheapest way past a total is to shorten something else.
//
// So this ledger is the **primary** control and it asserts every entry on the nose. The ceiling
// keeps its life under the exception clause Scott added to the law with this PR — *a total is not a
// ledger, except where the consumer consumes the total, and even then it is never a substitute for
// per-item assertion when the items are individually checkable* — because the consumer of
// `CLAUDE.md` is a context window and context cost is total bytes. The two stack, and their jobs
// are different: **this ledger says which entry moved; the ceiling says whether the file still
// fits.** A breach that names its cause is the point. Before this existed, a trip meant a hunt.
//
// # The partition is the whole file, so the sum is a checksum
//
// Every byte of `CLAUDE.md` lands in exactly one row: one row per top-level bullet in the
// Disciplines section, and one row per `## ` section for everything around them. The sum is then
// asserted equal to `os.Stat`'s size — which is a genuine two-mechanism reconciliation, bytes read
// versus bytes reported, not a sum checked against itself. Per the law, that sum *cannot* fail
// while every row passes; what it detects is a region nobody is counting, which is what a
// restructure of the file's section layout would produce.
//
// # What is deliberately not in a row
//
// **No line numbers.** Including one would make every row a function of every row above it, so
// inserting a law near the top would churn fifty golden values and bury the one real change — the
// cross-item coupling this ledger exists to remove, reintroduced in its own record.
//
// Rows are labelled by `family/anchor` rather than by the lead sentence, because the anchor is
// *derived* (`anchorFor`) and already checked against the lead by `TestEveryLawIsIndexed`. A
// hand-written label would be a third copy of a sentence that already has two homes.
//
// # Watched die, twice, and the first one is the law demonstrated inside its own control
//
//   - **The compensating perturbation.** `no-cgo-pure-go` was grown by 4 bytes and
//     `decision-before-code` shrunk by 4, leaving `CLAUDE.md` **byte-identical at 38068**. The
//     ceiling passed — it could not have done otherwise, there being nothing for it to see — and
//     the bijection passed, since neither lead nor pointer moved. This ledger reported both rows
//     with their signed deltas. That is precisely the cross-item cancellation the law names, and
//     the reason the per-entry assertion is primary rather than decorative.
//   - **The vacuity floor, on the regenerate path as well as the compare path.** Breaking
//     `lawBulletRE` to match nothing made the reader find 0 entries; both paths fataled at the
//     floor, and `-update-ledger` **left the golden file untouched** rather than writing two rows
//     over 51. Checked by diffing the file afterwards, not by reading the exit status: *a
//     completion state can be true while its payload vanished*, and the inverse — a refusal that
//     still wrote — would look identical from the verdict channel.

// updateLedger regenerates the golden file instead of checking it — `make claudemd-ledger`.
//
// Same shape and same reason as `-update-census` in `internal/binary`: a flag rather than a
// generator command, since the inputs are a test's own parse of a repo file. And the same hazard,
// worth restating where the next person will read it — **`-update` on a broken reader cheerfully
// writes the breakage into the golden file**, which is why the vacuity floor below runs on the
// regenerate path too and not only on the compare path.
var updateLedger = flag.Bool("update-ledger", false,
	"regenerate testdata/claudemd-ledger.txt instead of checking it")

// ledgerPath is the golden file. Committed: it is derived from a committed input, so it is ours.
var ledgerPath = filepath.Join("testdata", "claudemd-ledger.txt")

const ledgerHeader = `# CLAUDE.md byte ledger — one row per index entry, plus the regions around them.
#
# Generated by 'make claudemd-ledger'; DO NOT EDIT. Columns: bytes, kind, label.
#
# This is the per-entry control that 'a total is not a ledger' requires over an index whose
# entries are individually enumerable (docs/laws/controls.md). claudeMDCeiling remains fatal
# beside it, under that law's exception clause: the consumer of CLAUDE.md consumes the total.
#
# The rows partition the file exactly, and their sum is checked against os.Stat's size. A row
# growing is a recall key growing back into a body — the ratchet the docs/laws/ restructure
# exists to stop. Re-basing a row is fine; doing it without reading what grew is the failure.
#
# Order is document order. No line numbers, deliberately: a row keyed by position is a row
# that churns whenever anything above it moves.
`

// ledgerRow is one accounted region of `CLAUDE.md`.
type ledgerRow struct {
	bytes int
	kind  string // "law" for an index entry, "section" for the prose around them
	label string // family/anchor for a law; the `## ` heading text for a section
}

func (r ledgerRow) String() string { return fmt.Sprintf("%d\t%s\t%s", r.bytes, r.kind, r.label) }

// lineBytes is a line's contribution to the file's size: its own bytes plus the newline that
// follows it, except for the last element of a `strings.Split` on "\n", which has none.
//
// Written out rather than left implicit because the checksum below is exact, and an off-by-one
// here would be absorbed silently into whichever row happened to own the final line.
func lineBytes(lines []string, i int) int {
	if i == len(lines)-1 {
		return len(lines[i])
	}
	return len(lines[i]) + 1
}

// computeLedger partitions `CLAUDE.md` into rows in document order.
func computeLedger(tb testing.TB) []ledgerRow {
	tb.Helper()

	src, err := os.ReadFile(claudeMD)
	if err != nil {
		tb.Fatalf("reading the index: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	start, end := disciplinesRange(tb, lines)

	// Outside the section, attribute by `## ` heading rather than lumping. The first draft made it
	// one `outside` row and the row came out at **20526 bytes — 54% of the file in a single
	// unnamed lump**, which is the ceiling's own defect at half scale: most of the protected
	// quantity attributed to nothing, so most of a breach would still prompt a hunt. An
	// instrument whose purpose is naming the cause does not get to leave the majority unnamed.
	var rows []ledgerRow
	section := "(before the first ## heading)"
	acc := 0
	emit := func() {
		if acc > 0 {
			rows = append(rows, ledgerRow{acc, "section", section})
		}
		acc = 0
	}

	// Bullets are flushed at their own position, so the golden file reads in document order — a
	// ledger sorted by kind would put the law rows after every section that follows Disciplines and
	// make the file's shape unrecoverable from its own record.
	bn, lns := 0, []string(nil)
	flushBullet := func() {
		if lns == nil {
			return
		}
		joined := strings.Join(lns, " ")
		size := bn
		bn, lns = 0, nil
		lead := lawLeadRE.FindStringSubmatch(joined)
		ptr := lawPointerRE.FindStringSubmatch(joined)
		if lead == nil || ptr == nil {
			// Left to `TestEveryLawIsIndexed`, which reports both of these with the messages they
			// deserve. Duplicating the diagnosis here would give one defect two voices; the row
			// still has to exist, or the checksum would blame the omission on the file's size.
			rows = append(rows, ledgerRow{size, "law", "UNPARSED-see-TestEveryLawIsIndexed"})
			return
		}
		rows = append(rows, ledgerRow{size, "law", ptr[1] + "/" + anchorFor(lead[1])})
	}

	// Walk the whole file once. Inside Disciplines, a line that is not a top-level bullet and
	// follows one belongs to it — that is what makes sub-bullets and wrapped continuations count
	// against the law they elaborate, which is the quantity that matters: a key's cost is its whole
	// entry, not its first line.
	for i := range lines {
		if i == start {
			emit()
			section = "## Disciplines heading and preamble"
		} else if i == end || (strings.HasPrefix(lines[i], "## ") && (i < start || i > end)) {
			flushBullet()
			emit()
			section = strings.TrimSpace(lines[i])
		}
		if i >= start && i < end {
			if lawBulletRE.MatchString(lines[i]) {
				flushBullet()
				emit() // closes the preamble, the first time through
				lns = []string{}
			}
			if lns != nil {
				bn += lineBytes(lines, i)
				lns = append(lns, strings.TrimSpace(lines[i]))
				continue
			}
		}
		acc += lineBytes(lines, i)
	}
	flushBullet()
	emit()

	// Vacuity, before these rows are trusted for anything and *including* on the `-update` path: a
	// reader that has silently stopped matching bullets produces two rows and a golden file that
	// agrees with it perfectly. Same floor as the bijection's, same #78/#80/#105 defect.
	laws := 0
	for _, r := range rows {
		if r.kind == "law" {
			laws++
		}
	}
	if laws < lawsFloor {
		tb.Fatalf("the ledger found %d law entries in %s's Disciplines section, floor %d.\n\n"+
			"Every assertion below is a per-row comparison, and a per-row comparison over two rows "+
			"is satisfied by a golden file with two rows in it — which `-update-ledger` would "+
			"write without complaint. The bullet reader has stopped matching.", laws, claudeMD, lawsFloor)
	}
	return rows
}

// TestClaudeMDIndexLedger asserts each index entry's byte count on the nose, and closes the
// partition against the file's own size.
//
// Separate from `TestClaudeMDStaysAnIndex` (the ceiling) and from `TestEveryLawIsIndexed` (the
// bijection) because all three fail for unrelated reasons: this one says an entry grew, the
// ceiling says the file no longer fits, the bijection says a key and its body disagree. One test
// asserting three properties with one message is the partition defect (grave #34).
func TestClaudeMDIndexLedger(t *testing.T) {
	rows := computeLedger(t)

	if *updateLedger {
		var b strings.Builder
		b.WriteString(ledgerHeader)
		for _, r := range rows {
			b.WriteString(r.String())
			b.WriteString("\n")
		}
		if err := os.WriteFile(ledgerPath, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("writing %s: %v", ledgerPath, err)
		}
		t.Logf("regenerated %s with %d rows", ledgerPath, len(rows))
		return
	}

	golden, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("reading %s: %v\n\nRun `make claudemd-ledger` to create it.", ledgerPath, err)
	}
	want := map[string]int{}
	var wantOrder []string
	for _, l := range strings.Split(string(golden), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != 3 {
			t.Errorf("%s: malformed row %q: want three tab-separated fields", ledgerPath, l)
			continue
		}
		n, aerr := strconv.Atoi(f[0])
		if aerr != nil {
			t.Errorf("%s: row %q has a non-numeric byte count: %v", ledgerPath, l, aerr)
			continue
		}
		key := f[1] + "\t" + f[2]
		if _, dup := want[key]; dup {
			t.Errorf("%s: %q appears twice — a duplicate row makes the count agree while covering "+
				"one entry less", ledgerPath, key)
		}
		want[key] = n
		wantOrder = append(wantOrder, key)
	}
	if len(want) < lawsFloor {
		t.Fatalf("%s holds %d rows, floor %d: comparing against a nearly-empty golden file agrees "+
			"with anything. Regenerate it deliberately and read the diff.", ledgerPath, len(want), lawsFloor)
	}

	// Per row, on the nose. The message names the entry and the delta, because naming the cause is
	// this instrument's entire reason for existing over the ceiling that preceded it.
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.kind + "\t" + r.label
		seen[key] = true
		exp, ok := want[key]
		if !ok {
			t.Errorf(`%s: a new entry the ledger does not carry: %s %q, %d bytes.

If this is a newly minted law, that is expected: re-base with `+"`make claudemd-ledger`"+` in the
same PR, and check `+"`claudeMDCeiling`"+` in the same motion — a new key spends the file's headroom,
which is what the ceiling is a budget on. If the budget is short, the room comes from **demoting a
law a live control already enforces** to a bare pointer, never from raising the number (ruling:
Scott, PR #298).`, ledgerPath, r.kind, r.label, r.bytes)
			continue
		}
		if r.bytes != exp {
			delta := r.bytes - exp
			sign := "grew by"
			if delta < 0 {
				sign, delta = "shrank by", -delta
			}
			t.Errorf(`%s: %s %q is %d bytes, ledger says %d — %s %d.

The question this asks is the ceiling's question, aimed at one entry instead of at the file: is
the added text this law's **body** — a specimen, a minting record, a measurement — which belongs
in docs/laws/ with only the compressed form left here? Or is it **governance**, which must be in
context every turn and therefore stays?

A recall key that has grown a paragraph is the ratchet the restructure exists to stop, and it is
invisible to a file-total ceiling: trimming an unrelated entry pays for it. Re-base this row only
after answering, and say which branch in the PR.`,
				ledgerPath, r.kind, r.label, r.bytes, exp, sign, delta)
		}
	}
	for _, key := range wantOrder {
		if !seen[key] {
			f := strings.SplitN(key, "\t", 2)
			t.Errorf(`%s: the ledger carries %s %q and %s does not.

A row with no entry is a law that left the index — which `+"`TestEveryLawIsIndexed`"+` will also
report if its body is still in the corpus. If the removal is deliberate, re-base and leave
`+"`claudeMDCeiling`"+` where it is: the bytes freed are **for the next mint to spend**, which is
exactly the remedy the #298 ruling names — room comes from demoting a law a live control already
enforces, not from raising the budget. Lowering the ceiling by the bytes a demotion freed would
cancel the only mechanism there is for earning index space.`,
				ledgerPath, f[0], f[1], claudeMD)
		}
	}

	// The checksum, and it is a two-mechanism reconciliation rather than a sum checked against
	// itself: bytes attributed by this reader versus bytes reported by the filesystem. It cannot
	// fail while every row above passes — that is the point of having it. What it detects is an
	// unaccounted region, which is what restructuring the file's `## ` layout would produce, and
	// what the ceiling would then be bounding without this ledger seeing any of it.
	info, err := os.Stat(claudeMD)
	if err != nil {
		t.Fatalf("stat %s: %v", claudeMD, err)
	}
	sum := 0
	for _, r := range rows {
		sum += r.bytes
	}
	if int64(sum) != info.Size() {
		t.Errorf("the ledger's rows account for %d bytes of a %d-byte file — %d unaccounted. Every "+
			"byte belongs to a row; a partition that does not close names a region nobody is "+
			"counting, and the ceiling above would bound it invisibly",
			sum, info.Size(), info.Size()-int64(sum))
	}

	entries, biggest, biggestLabel := 0, 0, ""
	for _, r := range rows {
		if r.kind != "law" {
			continue
		}
		entries++
		if r.bytes > biggest {
			biggest, biggestLabel = r.bytes, r.label
		}
	}
	// Printed from the measured rows, never from the golden file's copy of them: *a log that
	// recites the constants it is meant to be reporting makes the reader confirm a number by
	// reading the assertion's other copy of it* (the defect ledger_test.go's own first draft had).
	t.Logf("CLAUDE.md ledger: %d bytes over %d rows, %d of them index entries. %d bytes of headroom "+
		"under the %d-byte ceiling. Largest entry: %s at %d bytes.",
		sum, len(rows), entries, claudeMDCeiling-sum, claudeMDCeiling, biggestLabel, biggest)
}
