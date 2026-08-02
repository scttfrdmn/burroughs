package binary

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The census: every accepted arm of the generated table, with the gate that governs it
// (decision 0012, #91).
//
// # The direction this exists to assert
//
// `gatedOpcodes` holds **whole-region** entries — `{prefix: 0xfb, lo: 0x00, hi: 0xff, gate:
// gateGC}` — on the measured fact that 0xfb is entirely GC at bdd7164. That is a claim about
// every arm the region will *ever* hold, not about the 37 there today.
//
// The two older controls both walk the **mapping**: TestEveryMappedOpcodeExistsInTheTable
// asks whether each entry covers a real arm, TestEveryGateMapsAtLeastOneConstruct asks
// whether each gate maps something. Neither starts from the table, so an arm arriving
// upstream inside a region range inherits that region's gate with every control still green
// — the range still covers something, the gate still maps something. This test is the
// table-side direction, and the two are complements rather than duplicates: delete either
// and a direction goes unasserted.
//
// The gap was found by sweeping cited-versus-defined test names (#88). The comment at the
// mapping site cited `TestEveryTableOpcodeIsClassified`, which never existed, **for exactly
// this direction** — "the classification test walks the table rather than this file". The
// missing control was documented as present, which is why nobody went looking. *A test name
// is as checkable as a `.wast:N`.*
//
// # Why the census covers ungated arms too
//
// #91 framed the risk as an arm inheriting a region's gate. The mirror is an arm arriving
// with **no** gate — decoding clean with every gate off — and that is the #48 defect itself,
// not a cousin of it. A census listing only the 298 gated arms would be scoped to the
// population today's risk lives in, which is the blind spot *scope controls to the space*
// names. So all 499 accepted arms are rows, and `-` is a verdict.
//
// 0xfc earns its mention: bulk-memory and the non-trapping conversions, entirely core Wasm
// 2.0 at the pin, so it holds no mapping entry and every arm reads `-`. A correct answer a
// gated-arms-only census could not have expressed.
//
// # Why exact agreement rather than a floor
//
// Both inputs are committed artifacts — `optable.go` is generated from `decode.ml` at the
// pin, `gatemap.go` is hand-authored testimony — so this number cannot move because upstream
// moved. It moves when `make opcodes` runs or a human edits the mapping, which are the two
// events that should demand review. That is the difference from the board's pass floors
// (0013), whose corpus is unpinned (#42) and which therefore get slack instead of a golden
// file: *the strongest control the inputs admit*, at each site.

// updateCensus regenerates the golden file instead of checking it — `make gate-census`.
//
// A flag rather than a separate command because the census's inputs (`prefixRegions`,
// `gatedOpcodes`) are unexported: a cmd/ generator would need them exported, putting a
// generation-only door in the package for the sake of tidiness. Go's own convention is this
// flag.
var updateCensus = flag.Bool("update-census", false,
	"regenerate testdata/gate-census.txt instead of checking it")

// censusPath is the golden file. Committed, unlike the corpora: it is derived from two
// committed inputs, so it is ours, and .gitignore's asymmetry note covers exactly this
// distinction.
var censusPath = filepath.Join("testdata", "gate-census.txt")

// censusRegionFloors is the per-region minimum arm count.
//
// **Per-region, not a total**, and that is the whole point of the shape: a total floor of
// 400 passes while the 37-arm 0xfb region has dissolved entirely, because 0xfd's 256 arms
// carry the sum. `prefixRegions` is precisely where a region can go missing — a fourth
// prefix arriving is a build failure by TestPrefixRegionsCoverTheTable, but an existing
// region emptying is not. This is the vacuity law's per-region form: *a comparison against
// an empty set succeeds*, and a census computed over a vanished region agrees with a golden
// file only if the golden file vanished too, which `-update-census` would cheerfully do.
//
// Stamped from the printed counts at bdd7164, set well under each real figure so the table
// growing does not trip them: 0x00 had 211 accepted arms, 0xfb 37, 0xfc 18, 0xfd 256.
var censusRegionFloors = map[byte]int{
	0x00: 150,
	0xfb: 25,
	0xfc: 12,
	0xfd: 200,
}

// censusRow is one arm's classification, in the golden file's column order.
type censusRow struct {
	prefix   byte
	sub      uint32
	gate     string // "-" for no gate
	mnemonic string
}

// computeCensus walks the table and classifies every accepted arm through `gateFor` — the
// same function the decoder calls, so this measures the composition the engine actually
// uses rather than re-deriving it. *Measure with the instrument, not a regex.*
func computeCensus(tb testing.TB) []censusRow {
	tb.Helper()

	rows := make([]censusRow, 0, 512)
	perRegion := map[byte]int{}
	for prefix, region := range prefixRegions {
		for sub, info := range region {
			// Neither illegal nor escape: an arm the reference defines in order to reject
			// cannot be gated (it is refused anyway), and a prefix byte is a dispatch, not
			// a construct. Same exclusions as TestEveryMappedOpcodeExistsInTheTable, which
			// is what makes the two directions comparable.
			if info.illegal || info.escape {
				continue
			}
			perRegion[prefix]++
			gate := "-"
			if g, ok := gateFor(prefix, sub); ok {
				gate = string(g.gate)
			}
			rows = append(rows, censusRow{prefix, sub, gate, censusName(info)})
		}
	}

	// Vacuity, per region, before the rows are trusted for anything.
	for prefix, floor := range censusRegionFloors {
		if got := perRegion[prefix]; got < floor {
			tb.Errorf("region %#02x contributed %d accepted arms, floor %d: a census computed "+
				"over a region that has emptied agrees with a golden file that emptied with it",
				prefix, got, floor)
		}
	}
	for prefix := range perRegion {
		if _, ok := censusRegionFloors[prefix]; !ok {
			tb.Errorf("region %#02x has no floor in censusRegionFloors: a region with no "+
				"vacuity bound is a region that can empty silently", prefix)
		}
	}

	slices.SortFunc(rows, func(a, b censusRow) int {
		if a.prefix != b.prefix {
			return int(a.prefix) - int(b.prefix)
		}
		return int(a.sub) - int(b.sub)
	})
	return rows
}

// censusName is an arm's label, and it exists because two arms have no mnemonic.
//
// 0x05 and 0x0b — ELSE and END — are the table's `reason` rows: delimiters the reference
// defines in order to *report on* rather than to execute, so they carry an error text and
// no constructor name. They are neither `illegal` nor `escape`, so they pass the census
// filter correctly (the exclusion set is deliberately identical to
// TestEveryMappedOpcodeExistsInTheTable's, or the two directions would be measuring
// different populations), and they then rendered a **blank fourth column** — a field that
// vanishes in a whitespace-delimited file, which censusLines would silently read as a
// three-field row.
//
// Found by reading the generated file rather than by trusting it, which is the only way a
// formatting defect in a golden file gets found: the file is its own expected value, so a
// blank column agrees with itself forever. *Print what the code actually returns.*
func censusName(info opInfo) string {
	if info.mnemonic != "" {
		return info.mnemonic
	}
	if info.reason != "" {
		// Underscored: the column is whitespace-delimited, so the label cannot contain a
		// space without becoming two fields.
		return "reason:" + strings.ReplaceAll(info.reason, " ", "_")
	}
	return "?"
}

// renderCensus is the golden file's text. Header names the authority, per 0008's
// one-answer-to-who-says-so rule — here the answer is "both, composed".
func renderCensus(rows []censusRow) string {
	var b strings.Builder
	b.WriteString(`# Gate census — every accepted arm of the opcode table and the gate governing it.
#
# Generated by 'make gate-census'; DO NOT EDIT. Authority is the *composition* of two
# committed artifacts, neither alone: internal/binary/optable.go (generated from the
# reference at the pin in scripts/fetch-spec-ref.sh) and internal/binary/gatemap.go
# (hand-authored testimony, decision 0008). Regenerating is deterministic, which is why
# a derived-from-two-inputs file does not have optable.go's two-authorities problem.
#
# A '-' gate is a verdict, not a gap: the arm is core Wasm, ungated by design. An arm
# whose gate changes without a proposal document changing is the #91 defect — a whole-
# region range silently swallowing an arm upstream added.
#
# columns: prefix sub gate mnemonic
`)
	for _, r := range rows {
		fmt.Fprintf(&b, "%#02x %#06x %s %s\n", r.prefix, r.sub, r.gate, r.mnemonic)
	}
	return b.String()
}

// TestGateCensusIsClassifiedArmByArm is 0012's control: the table-side walk #91 asked for.
func TestGateCensusIsClassifiedArmByArm(t *testing.T) {
	rows := computeCensus(t)
	got := renderCensus(rows)

	if *updateCensus {
		if err := os.MkdirAll(filepath.Dir(censusPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(censusPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s: %d arms", censusPath, len(rows))
		return
	}

	want, err := os.ReadFile(censusPath)
	if err != nil {
		t.Fatalf("reading the census: %v\n\nrun: make gate-census", err)
	}
	if string(want) == "" {
		t.Fatal("the census is empty: a comparison against nothing agrees with anything")
	}
	if got == string(want) {
		t.Logf("census agrees: %d accepted arms classified", len(rows))
		return
	}

	// Report the *rows* that differ, not a whole-file diff. A classification change is
	// per-arm, and a reader needs to know which arm and which direction — an arm that
	// gained a gate and an arm that lost one are different findings.
	gotLines := censusLines(got)
	wantLines := censusLines(string(want))
	for key, g := range gotLines {
		if w, ok := wantLines[key]; !ok {
			t.Errorf("%s: arm is in the table but not the census — it arrived upstream and "+
				"inherited its classification (%s) from a range rather than from a proposal "+
				"document. Classify it in gatemap.go, then: make gate-census", key, g)
		} else if g != w {
			t.Errorf("%s: census says %q, the mapping now computes %q", key, w, g)
		}
	}
	for key, w := range wantLines {
		if _, ok := gotLines[key]; !ok {
			t.Errorf("%s (census: %s): arm is in the census but no longer in the table — the "+
				"pin moved and the reference dropped it; regenerate both: make opcodes gate-census",
				key, w)
		}
	}
	if !t.Failed() {
		t.Errorf("census text differs but no row does: the header or formatting changed.\n"+
			"got %d bytes, want %d — run: make gate-census", len(got), len(want))
	}
}

// censusLines keys a rendered census by "prefix sub", value "gate mnemonic", skipping
// comments. Keying by position is what lets the comparison name the arm.
func censusLines(s string) map[string]string {
	out := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			// Not skipped: a short row is the ELSE/END blank-mnemonic defect, and a parser
			// that skips what it cannot read is how a golden file comes to be "checked" by
			// a mechanism that reads past everything in it (#78). Key it so the comparison
			// reports it; TestCensusRowsAreWellFormed is what fails on it.
			out["malformed:"+line] = "short row, " + strconv.Itoa(len(f)) + " fields"
			continue
		}
		out[f[0]+" "+f[1]] = f[2] + " " + f[3]
	}
	return out
}

// TestCensusRowsAreWellFormed reads the committed file as *text* and requires every data
// row to have four fields.
//
// This is the control the blank-mnemonic defect needed. A golden file is its own expected
// value, so a malformed row agrees with itself on every future run — the drift check cannot
// see it, because both sides render the same missing column. The only way to catch it is to
// assert the file's *shape* independently of the computation that produced it, which is why
// this reads bytes rather than calling renderCensus.
//
// Same class as *registration is not verification* (#78): a file checked by a reader that
// silently tolerates what it cannot parse looks checked and is worse than unchecked.
func TestCensusRowsAreWellFormed(t *testing.T) {
	data, err := os.ReadFile(censusPath)
	if err != nil {
		t.Fatalf("reading the census: %v", err)
	}

	rows, comments := 0, 0
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			comments++
			continue
		case line == "":
			t.Errorf("line %d is blank: the census has no record separators, so a blank line "+
				"is a rendering defect", i+1)
			continue
		}
		rows++
		if f := strings.Fields(line); len(f) != 4 {
			t.Errorf("line %d has %d fields, want 4 (prefix sub gate mnemonic): %q — a column "+
				"that renders empty disappears in a whitespace-delimited file and agrees with "+
				"itself forever", i+1, len(f), line)
		}
	}
	if rows < 400 {
		t.Errorf("%d data rows; 499 at bdd7164 — a file this short means this test is "+
			"asserting a shape over almost nothing", rows)
	}
	if comments == 0 {
		t.Error("no header comments: the census must name its authority (0008)")
	}
	// Guarded, because an unconditional summary contradicts the failures above it. The
	// first draft logged "every row 4 fields" directly beneath a report of a three-field
	// row — a summary asserting the very property the test had just refuted. Same shape as
	// the `Fatalf`-then-`Skip` helper that reported a fail *and* a skip: a witness whose
	// closing sentence disagrees with its testimony. Found by reading the falsification
	// output rather than only its exit code (*verdict channel and mechanism channel are
	// different instruments* — the FAIL was right and the prose was wrong).
	if !t.Failed() {
		t.Logf("%d data rows, %d header lines, every row 4 fields", rows, comments)
	}
}

// TestCensusGatesAreRealGates keeps the census's third column inside the gate vocabulary.
//
// Without this the golden file could carry a typo'd or retired gate name and agree with
// itself forever: the census is compared against a computation, and a computation reading a
// misspelled `gateID` would produce the same misspelling on both sides. So the column is
// checked against the domain derived from `Features` — *derive the domain, never enumerate
// it* — which is the same reason featureGateIDs exists.
func TestCensusGatesAreRealGates(t *testing.T) {
	valid := map[string]bool{"-": true}
	for _, id := range featureGateIDs(t) {
		valid[string(id)] = true
	}

	data, err := os.ReadFile(censusPath)
	if err != nil {
		t.Fatalf("reading the census: %v", err)
	}
	rows := censusLines(string(data))
	if len(rows) < 400 {
		t.Fatalf("the census has %d rows; it had 499 at bdd7164, and a population this "+
			"small means this test is checking almost nothing", len(rows))
	}
	for key, val := range rows {
		gate := strings.Fields(val)[0]
		if !valid[gate] {
			t.Errorf("%s: gate %q is not a field of Features (nor %q)", key, gate, "-")
		}
	}
	if !t.Failed() { // see TestCensusRowsAreWellFormed on why this is guarded
		t.Logf("%d census rows, all gates in the derived vocabulary", len(rows))
	}
}
