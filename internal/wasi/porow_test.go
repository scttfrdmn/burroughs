// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// poRow is one structured row emitted by a Phase 4 pool harness, and `parsePoRow` is the ONE place that
// reads it.
//
// # Why this type exists
//
// The pool harness printed a prose line and every tally re-derived it with an ad hoc regex. **The same
// mistake was made three times**: a pattern written as `PE-FINISHED t=...` when the line is
// `PE-FINISHED guest=... t=...`, so the `guest=` field between them made every tally match nothing. Two of
// those three printed a confident count of zero rows — an instrument reporting on output it never received,
// which is the same family as the earlier `grep` block-buffering defect.
//
// The repair is not a better regex. It is that **a row is parsed once, into named fields, and every tally
// reads the fields** — so a field added or moved cannot silently empty a count, and a missing field is an
// error rather than a zero.
type poRow struct {
	Outcome string // FINISHED | HUNG | PROGRESS
	Fields  map[string]string
}

// Int answers a field as an integer. A missing or unparsable field is an **error**, never a zero, which is
// the whole point: a zero that means "absent" is indistinguishable from a zero that means "measured zero",
// and this harness's history is exactly that confusion.
func (r poRow) Int(name string) (int64, error) {
	v, ok := r.Fields[name]
	if !ok {
		return 0, fmt.Errorf("%w: %q (row has %s)", errPoRowMissingField, name, r.fieldNames())
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("field %q = %q is not an integer: %w", name, v, err)
	}
	return n, nil
}

// A string accessor is deliberately absent. One was written and `deadcode` found it unreachable — no tally
// reads a string field today, and speculative API is declined with a trigger rather than kept: add it when a
// tally needs `guest=` or `spawnedTIDs=`, with the same absent-is-an-error rule as `Int`.

func (r poRow) fieldNames() string {
	names := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// parsePoRow parses one harness line of the form
//
//	PE-<OUTCOME> key=value key=value ...
//
// **Field ORDER is irrelevant and unknown fields are kept.** That is what makes this immune to the defect
// it replaces: a tally asks for `verdicts` by name and cannot be broken by a `guest=` appearing before it,
// by a new field being inserted, or by two fields swapping places.
//
// A value may contain no spaces, which is the one constraint the emitter must honour; bracketed lists like
// `spawnedTIDs=[2 3]` are therefore normalised by the emitter into `spawnedTIDs=2,3` rather than parsed
// here — a parser that guessed at bracket nesting would be the ad hoc regex again, one level down.
func parsePoRow(line string) (poRow, error) {
	line = strings.TrimSpace(line)
	const prefix = "PE-"
	if !strings.HasPrefix(line, prefix) {
		return poRow{}, errPoRowNotARow
	}
	rest := line[len(prefix):]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return poRow{}, fmt.Errorf("%w: no fields after the outcome in %q", errPoRowMalformed, line)
	}
	r := poRow{Outcome: rest[:sp], Fields: map[string]string{}}
	switch r.Outcome {
	case "FINISHED", "HUNG", "PROGRESS":
	default:
		return poRow{}, fmt.Errorf("%w: unknown outcome %q", errPoRowMalformed, r.Outcome)
	}
	for _, tok := range strings.Fields(rest[sp+1:]) {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			// A bare token is not silently dropped: a harness that starts printing prose mid-row should
			// fail loudly rather than have the prose vanish into a successful parse.
			return poRow{}, fmt.Errorf("%w: token %q has no `=` in %q", errPoRowMalformed, tok, line)
		}
		r.Fields[tok[:eq]] = tok[eq+1:]
	}
	if len(r.Fields) == 0 {
		return poRow{}, fmt.Errorf("%w: no key=value fields in %q", errPoRowMalformed, line)
	}
	return r, nil
}

// parsePoRows parses every row in a harness log and reports how many lines were examined, so a tally can
// assert it saw rows at all. **A count of zero rows out of many lines is the signature of the defect this
// replaces**, and it is now reportable rather than invisible.
func parsePoRows(log string) (rows []poRow, lines int, err error) {
	for _, ln := range strings.Split(log, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "PE-") {
			continue
		}
		lines++
		r, perr := parsePoRow(ln)
		if perr != nil {
			if errors.Is(perr, errPoRowNotARow) {
				continue
			}
			return nil, lines, fmt.Errorf("line %d: %w", lines, perr)
		}
		rows = append(rows, r)
	}
	return rows, lines, nil
}

// Terminal keeps only the rows that are a verdict. A `PROGRESS` row is not one — counting it as a result is
// how a truncated run's partial state gets read as an outcome (standing property 17).
func terminalRows(rows []poRow) []poRow {
	out := make([]poRow, 0, len(rows))
	for _, r := range rows {
		if r.Outcome == "FINISHED" || r.Outcome == "HUNG" {
			out = append(out, r)
		}
	}
	return out
}

var (
	errPoRowNotARow      = errors.New("not a harness row")
	errPoRowMalformed    = errors.New("malformed harness row")
	errPoRowMissingField = errors.New("harness row has no such field")
)

// TestPoRowParserSurvivesTheDefectItReplaces pins the three-time mistake as a case rather than as a
// comment. Each row below is a real line shape from this campaign's logs.
func TestPoRowParserSurvivesTheDefectItReplaces(t *testing.T) {
	// **The historical defect, as a case.** Every ad hoc tally wrote `PE-FINISHED t=...` and this line has
	// `guest=` in between, so the pattern matched nothing and printed a confident zero. A name-keyed parse
	// cannot be broken by that field, which is the property being pinned.
	const row1 = `PE-FINISHED guest=/tmp/clean_sync.test t=7s bytes=283 verdicts=4 spawns=2 ` +
		`procExit=1 code=0 exitTID=3`
	r, err := parsePoRow(row1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Outcome != "FINISHED" {
		t.Errorf("outcome = %q, want FINISHED", r.Outcome)
	}
	for name, want := range map[string]int64{"verdicts": 4, "spawns": 2, "procExit": 1, "code": 0, "exitTID": 3} {
		got, gerr := r.Int(name)
		if gerr != nil {
			t.Errorf("Int(%q): %v", name, gerr)
			continue
		}
		if got != want {
			t.Errorf("Int(%q) = %d, want %d", name, got, want)
		}
	}

	// Field order is irrelevant: the same fields in a different order parse identically. A positional
	// reader would disagree here, which is what makes this the discriminating case.
	shuffled := `PE-FINISHED exitTID=3 verdicts=4 t=7s guest=/tmp/x code=0 spawns=2 procExit=1 bytes=283`
	r2, err := parsePoRow(shuffled)
	if err != nil {
		t.Fatalf("parse shuffled: %v", err)
	}
	for _, name := range []string{"verdicts", "spawns", "procExit", "code", "exitTID"} {
		a, _ := r.Int(name)
		b, _ := r2.Int(name)
		if a != b {
			t.Errorf("field %q differs by ORDER: %d vs %d — the parser is positional, which is the "+
				"defect it exists to prevent", name, a, b)
		}
	}

	// An absent field is an ERROR, not a zero. A zero here would be indistinguishable from a measured
	// zero, which is how "wakep=0 declined=0" once read as evidence from an edit that never applied.
	if _, aerr := r.Int("nosuchfield"); !errors.Is(aerr, errPoRowMissingField) {
		t.Errorf("Int on an absent field returned %v, want errPoRowMissingField — a zero there is "+
			"indistinguishable from a measured zero", aerr)
	}

	// A PROGRESS row is not a verdict, so `terminalRows` must drop it (standing property 17).
	rows, lines, perr := parsePoRows(row1 + "\n" +
		`PE-PROGRESS guest=/tmp/x t=20s bytes=0 verdicts=0 spawns=2 procExit=0 code=-1 exitTID=-1` + "\n" +
		`PE-HUNG guest=/tmp/x t=3m0s bytes=283 verdicts=4 spawns=2 procExit=1 code=0 exitTID=3` + "\n")
	if perr != nil {
		t.Fatalf("parsePoRows: %v", perr)
	}
	if lines != 3 || len(rows) != 3 {
		t.Errorf("saw %d lines / %d rows, want 3/3", lines, len(rows))
	}
	if term := terminalRows(rows); len(term) != 2 {
		t.Errorf("terminalRows kept %d, want 2 — a PROGRESS row counted as a verdict is how a truncated "+
			"run's partial state becomes an outcome", len(term))
	}

	// Malformed input fails loudly rather than parsing to an empty row.
	for _, bad := range []string{"PE-FINISHED", "PE-WHAT a=1", "PE-FINISHED prose here"} {
		if _, berr := parsePoRow(bad); berr == nil {
			t.Errorf("parsePoRow(%q) succeeded; a row that silently parses to nothing is the original "+
				"defect wearing a different shape", bad)
		}
	}
}
