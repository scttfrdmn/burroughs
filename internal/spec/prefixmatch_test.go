// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// # The substring-vs-prefix census (#455)
//
// The reference matches an expected error text by **prefix**. `assert_message`
// (`script/runner.ml:498-501`) is `String.length msg < String.length re || String.sub msg 0
// (String.length re) <> re`, negated — `HasPrefix`, with a parameter name (`re`) that suggests a
// regex the function does not contain — and all nine text-matching call sites in the reference go
// through it. This harness matches by **substring**, at six sites in the run loop plus one in a
// control, which is strictly looser: everything the reference accepts we accept, plus every message
// carrying the expected text anywhere after position 0.
//
// That is an **accept-direction** divergence, so no negative-direction vector can witness it: the
// rows that would are rows the suite expects to pass and we do pass, for a reason the reference
// would not have accepted. The evidence has to be a census of the passing population, and #455 asks
// for exactly one number before any of its three options can be priced — how many rows pass under
// `Contains` and would fail under `HasPrefix`.
//
// **This file is the probe and not the repair.** Which option the project takes (the reference's
// rule with the engine's texts made to conform; a normalized prefix; substring recorded as a
// bounded looseness) is Scott's, and #455 pre-registers nothing about which way the number will
// come out — the issue says so in its own words, and this file adds no forecast of its own.
//
// **A separate file, for `kindgate_test.go`'s reason** (#447): `foreclosingLicensed` in
// `internal/testenv/foreclose_test.go` keys its entries on `<file>:<line>`, so an insertion into
// `spec_test.go` re-keys every entry below it. A new file inserts nothing above anything.

// TestSubstringOnlyMatchCensus prints the census and pins only what a probe may pin.
//
// **Printed unconditionally rather than inside a failing assertion**, for the kind-gate census's
// reason: #455's whole input is a number, and a number that exists only when a test fails is a
// number nobody has read. `go test ./internal/spec/ -run TestSubstringOnlyMatchCensus -v` is the
// instrument.
//
// What is asserted here is **not** the size of the divergent population — that is the quantity
// under deliberation, and pinning it now would be this PR forecasting the answer to the question it
// was ordered to ask. What is asserted is that the census walked: `ExpectMatched` has a floor,
// because a lane reporting `0 divergent` out of `0 matched` has stopped walking rather than found
// nothing, and those two readings are indistinguishable from the divergent count alone.
func TestSubstringOnlyMatchCensus(t *testing.T) {
	requireSuite(t)

	_, _, allOnEngine := allOnLane(t)

	for _, lane := range []struct {
		name string
		eng  func() Engine
	}{
		{"default", engine},
		{"all-on", allOnEngine},
	} {
		matched, divergent := 0, 0
		matchedByKind := map[string]int{}
		byStratum := map[string]int{}
		byKind := map[string]int{}
		byFile := map[string]int{}
		byFamily := map[string]int{}
		var rows []string
		var widest []substringOnlyMatch

		for _, f := range boardFiles(t) {
			s, err := ParseFile(filepath.Join(suiteDir, f))
			if err != nil {
				t.Errorf("%s: parse: %v", f, err)
				continue
			}
			r := s.RunGated(lane.eng())
			for k, n := range r.ExpectMatched {
				matched += n
				matchedByKind[k.String()] += n
			}
			for _, m := range r.SubstringOnly {
				divergent++
				byStratum[m.Stratum.String()]++
				byKind[m.Kind.String()]++
				byFile[f]++
				byFamily[m.Stratum.String()+" "+prefixFamily(m.Got, m.Offset)]++
				widest = append(widest, m)
				rows = append(rows, fmt.Sprintf("%s:%d [%s/%s] +%d expected %q, got %q",
					f, m.Line, m.Kind, m.Stratum, m.Offset, m.Expect, m.Got))
			}
		}

		// The encode-stratum site is a *control's* predicate rather than a board award, so it is
		// counted separately and never summed with the six above. `TestAssertInvalidPassesFromAboveTheValidator`
		// asks `Contains(err.Error(), c.Expect)` of assert_invalid rows the engine refuses before
		// validation, to decide whether a green arrived by the wrong layer; the same looseness
		// applies to that decision, and it is measured through `validateModule` — the entry point
		// the control itself uses — rather than through a reconstruction of it.
		encodeMatched, encodeDivergent := 0, 0
		var encodeRows []string
		for _, f := range boardFiles(t) {
			s, err := ParseFile(filepath.Join(suiteDir, f))
			if err != nil {
				continue // already reported above
			}
			for _, c := range s.Commands {
				if c.Kind != KindAssertInvalid {
					continue
				}
				st, err := validateModule(c)
				if isGated(err) || err == nil || st != StratumEncode {
					continue
				}
				got := err.Error()
				if !strings.Contains(got, c.Expect) {
					continue
				}
				encodeMatched++
				if i := strings.Index(got, c.Expect); i > 0 {
					encodeDivergent++
					encodeRows = append(encodeRows,
						fmt.Sprintf("%s:%d +%d expected %q, got %q", f, c.Line, i, c.Expect, got))
				}
			}
		}

		sort.Slice(widest, func(i, j int) bool { return widest[i].Offset > widest[j].Offset })
		sort.Strings(rows)

		t.Logf("substring-vs-prefix census, %s lane: %d awards made by an expected-text match, "+
			"%d of them substring-only — rows the reference's prefix rule would have refused",
			lane.name, matched, divergent)
		t.Logf("  by stratum: %s", censusCounts(byStratum))
		// Numerator and denominator on adjacent lines, so an arm with matches and no divergences is
		// a **cell** rather than an absence. Two of the six sites read that way, and the difference
		// between "this arm's texts already conform" and "this arm was never instrumented" is not
		// in either line — it is in TestSubstringOnlyProbeSeesEveryArm, which is why that test
		// exists.
		t.Logf("  by command kind, divergent: %s", censusCounts(byKind))
		t.Logf("  by command kind, matched:   %s", censusCounts(matchedByKind))
		// **The line that turns the count into a price**, and the reason it is here rather than in
		// a session's scratch analysis: a divergent row is not a defective message, it is a message
		// carrying something *before* the phrase, and what matters to #455's choice is how many
		// distinct renderings put it there. N rows across three renderings is a different bill from
		// N rows across N distinct strings, and the count alone cannot tell them apart —
		// *measure with the instrument, not a regex*, so the grouping ships with the census.
		t.Logf("  by prefix family (digits → N, parenthesized opcode → (op)), %d distinct: %s",
			len(byFamily), censusCounts(byFamily))
		t.Logf("  by file: %s", censusCounts(byFile))
		for _, row := range rows {
			t.Logf("  %s", row)
		}
		if n := len(widest); n > 0 {
			t.Logf("  widest offenders (offset of the expected text in the engine's message):")
			for i, m := range widest {
				if i == 10 {
					t.Logf("    … and %d more", n-10)
					break
				}
				t.Logf("    +%d [%s] %q ⊂ %q", m.Offset, m.Stratum, m.Expect, m.Got)
			}
		}
		t.Logf("  encode-stratum control site (TestAssertInvalidPassesFromAboveTheValidator's own predicate, "+
			"not a board award): %d matched, %d substring-only", encodeMatched, encodeDivergent)
		for _, row := range encodeRows {
			t.Logf("    %s", row)
		}

		// Vacuity, and the only floor this probe is entitled to. The corpus holds thousands of
		// `assert_malformed`/`assert_invalid`/`assert_trap` vectors, every one of which is awarded
		// through a matched expected text, so a lane that walked cannot report a small number here.
		// The floor is deliberately far below the observed figure and above zero-plus-noise: it
		// catches a census pointed at an empty file list, a lane that stopped scoring, or a
		// `noteSubstringOnly` call deleted from every arm at once — and nothing else, which is what
		// a floor is for (*floors bound the catastrophic case only*).
		if matched < 1000 {
			t.Errorf("%s lane: only %d expected-text matches; the census has stopped walking, "+
				"and a divergent count of %d read against it would be a zero that could not have "+
				"come out otherwise", lane.name, matched, divergent)
		}
	}
}

// prefixFamily is the shape of what an engine message says *before* the expected phrase, with the
// two things that vary per vector collapsed: index numbers to `N` and the parenthesized opcode name
// to `(op)`.
//
// **The collapse is the measurement.** Without it the validator's location context reads as ~200
// distinct prefixes, one per opcode spelling, which prices a repair as two hundred edits; with it
// the same rows read as one rendering, which is where the repair would actually be made. Both
// numbers are printed — the family count and the family list — so a reader can see the collapse and
// disagree with it.
var (
	prefixDigits = regexp.MustCompile(`[0-9]+`)
	prefixParens = regexp.MustCompile(`\([^)]*\)`)
)

func prefixFamily(got string, offset int) string {
	if offset <= 0 || offset > len(got) {
		return "<none>"
	}
	s := prefixDigits.ReplaceAllString(got[:offset], "N")
	return prefixParens.ReplaceAllString(s, "(op)")
}

// censusCounts renders a count map deterministically, largest first, so a census line is diffable
// between runs and between lanes. Named apart from `lowering_test.go`'s `sortedCounts`, which is
// keyed by lane width and renders `N×W-wide`: one name over two renderings is how a caller comes to
// quote a figure in a shape it did not mean.
func censusCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// TestSubstringOnlyProbeSeesEveryArm is the probe's birth certificate, and it is here because a
// census whose recording is wired into five of six arms reports a number that reads as a
// measurement of the corpus and is partly a measurement of the wiring.
//
// **Two directions, per arm.** A message carrying the expected text at a non-zero offset must be
// recorded; a message that *begins* with it must not, because that row is one the reference accepts
// too and a probe that counts it inflates the very figure #455 turns on. Every arm gets both,
// because *a control can test the helper rather than the path*: `noteSubstringOnly` is one closure,
// and asserting it in isolation would prove the closure works while an arm that never calls it
// stays invisible.
//
// **A synthetic engine, not the real one.** The subject is the six call sites, and each row needs a
// message the arm's own component produces with the phrase at a chosen offset — which is a thing
// only a stub can be asked for. The real engine's figures are the census above.
func TestSubstringOnlyProbeSeesEveryArm(t *testing.T) {
	// `Expect` is the same phrase in every row, so the offset is the only variable: the divergent
	// message pads it with a prefix the reference would refuse, and the prefix row is the same
	// sentence with the padding removed.
	const want = "wanted text"
	const pad = "engine says: " // 13 bytes, which is the offset every divergent row must report

	for _, arm := range []struct {
		name    string
		src     string
		kind    Kind
		stratum Stratum
		// eng builds the engine whose relevant component returns `msg`; every other component
		// answers successfully, so the row reaches exactly one match site.
		eng func(msg string) Engine
	}{
		{
			name:    "assert_invalid, through scoreValidation",
			src:     `(assert_invalid (module binary "\00asm\01\00\00\00") "` + want + `")`,
			kind:    KindAssertInvalidBinary,
			stratum: StratumValidate,
			eng: func(msg string) Engine {
				return Engine{
					Decode:     func([]byte) error { return nil },
					Validate:   func(Command) (Stratum, error) { return StratumValidate, errString(msg) },
					IsDeclined: func(error) bool { return false },
					Has:        []Capability{CapValidator},
				}
			},
		},
		{
			name:    "assert_malformed, binary",
			src:     `(assert_malformed (module binary "\01") "` + want + `")`,
			kind:    KindAssertMalformed,
			stratum: StratumBinary,
			eng: func(msg string) Engine {
				return Engine{Decode: func([]byte) error { return errString(msg) }}
			},
		},
		{
			name:    "assert_malformed, text",
			src:     `(assert_malformed (module quote "(func") "` + want + `")`,
			kind:    KindAssertMalformedText,
			stratum: StratumText,
			eng: func(msg string) Engine {
				return Engine{
					Decode:     func([]byte) error { return nil },
					ReadText:   func([]byte) error { return errString(msg) },
					Validate:   stubValidate,
					IsDeclined: stubDeclined,
					Has:        []Capability{CapWatReader},
				}
			},
		},
		{
			name:    "assert_trap, wrapping a module",
			src:     `(assert_trap (module (func) (start 0)) "` + want + `")`,
			kind:    KindAssertTrapModule,
			stratum: StratumExec,
			eng: func(msg string) Engine {
				return Engine{
					Decode: func([]byte) error { return nil }, ReadText: func([]byte) error { return nil },
					Assemble:   func([]byte) ([]byte, error) { return []byte{0}, nil },
					Validate:   stubValidate,
					IsDeclined: stubDeclined,
					IsTrap:     func(error) bool { return true },
					InstantiateLinked: func(cmd Command, _ Registry) (Instance, Stratum, error) {
						// The spectest bootstrap comes through this same door (0017 part 3): a stub
						// that failed every call panics in spectestRegistry before the vector is
						// judged, which is the harness protecting its own fixture. Discriminated on
						// the source, as its sibling in sexpr_test.go is.
						if !strings.Contains(string(cmd.Source), "(start 0)") {
							return "spectest", StratumUnset, nil
						}
						return nil, StratumExec, errString(msg)
					},
					Invoke: func(Instance, string, []Val) ([]Val, error) { return nil, nil },
					Has:    []Capability{CapWatReader, CapInterpreter},
				}
			},
		},
		{
			name:    "assert_unlinkable",
			src:     `(assert_unlinkable (module (import "m" "f" (func))) "` + want + `")`,
			kind:    KindAssertUnlinkable,
			stratum: StratumExec,
			eng: func(msg string) Engine {
				return Engine{
					Decode: func([]byte) error { return nil }, ReadText: func([]byte) error { return nil },
					Assemble:   func([]byte) ([]byte, error) { return []byte{0}, nil },
					Validate:   stubValidate,
					IsDeclined: stubDeclined,
					IsTrap:     func(error) bool { return false },
					InstantiateLinked: func(cmd Command, _ Registry) (Instance, Stratum, error) {
						// The spectest bootstrap comes through this same door (0017 part 3), and a
						// stub that failed every call would panic in spectestRegistry before the
						// vector was judged. Discriminated on the source, as its sibling in
						// sexpr_test.go is, since a positional stub inverts silently the day the
						// bootstrap moves.
						if !strings.Contains(string(cmd.Source), `"m" "f"`) {
							return "spectest", StratumUnset, nil
						}
						return nil, StratumExec, errString(msg)
					},
					Invoke: func(Instance, string, []Val) ([]Val, error) { return nil, nil },
					Has:    []Capability{CapWatReader, CapInterpreter},
				}
			},
		},
		{
			name: "assert_trap, on an action",
			src: "(module (func (export \"f\")))\n" +
				`(assert_trap (invoke "f") "` + want + `")`,
			kind:    KindAssertTrapAction,
			stratum: StratumExec,
			eng: func(msg string) Engine {
				return Engine{
					Decode: func([]byte) error { return nil }, ReadText: func([]byte) error { return nil },
					Assemble:   func([]byte) ([]byte, error) { return []byte{0}, nil },
					Validate:   stubValidate,
					IsDeclined: stubDeclined,
					IsTrap:     func(error) bool { return true },
					InstantiateLinked: func(Command, Registry) (Instance, Stratum, error) {
						return "stub", StratumUnset, nil
					},
					Invoke: func(Instance, string, []Val) ([]Val, error) {
						return nil, errString(msg)
					},
					Has: []Capability{CapWatReader, CapInterpreter},
				}
			},
		},
	} {
		t.Run(arm.name, func(t *testing.T) {
			s, err := Parse("t.wast", []byte(arm.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			// The divergent direction: the phrase is present, at `len(pad)`, and the row must be
			// awarded *and* recorded.
			r := s.RunWith(arm.eng(pad + want + " and then some"))
			if r.Fail != 0 || r.Pass == 0 {
				t.Fatalf("the fixture did not reach the match site: %d pass / %d fail / "+
					"%d unsupported / %d unimplemented\n%s",
					r.Pass, r.Fail, r.Unsupported, r.Unimplemented, r.Board())
			}
			// Asserted on this arm's own key rather than on the map's size: a fixture whose
			// award came from some *other* command in the same script would satisfy a bare
			// non-empty check while leaving the arm under test uninstrumented.
			if r.ExpectMatched[arm.kind] == 0 {
				t.Fatalf("no award under %v passed through noteSubstringOnly, so this arm is "+
					"outside the census's reach entirely (matched: %v)\n%s",
					arm.kind, r.ExpectMatched, r.Board())
			}
			if len(r.SubstringOnly) != 1 {
				t.Fatalf("recorded %d substring-only rows, want 1 — this arm's call site is "+
					"missing or records the wrong population: %+v", len(r.SubstringOnly), r.SubstringOnly)
			}
			m := r.SubstringOnly[0]
			if m.Offset != len(pad) {
				t.Errorf("Offset = %d, want %d; the recorded distance is not the distance into "+
					"the engine's message", m.Offset, len(pad))
			}
			if m.Kind != arm.kind {
				t.Errorf("Kind = %v, want %v", m.Kind, arm.kind)
			}
			if m.Stratum != arm.stratum {
				t.Errorf("Stratum = %v, want %v; a census partitioned by stratum is only as good "+
					"as the value each arm passes", m.Stratum, arm.stratum)
			}
			if m.Expect != want {
				t.Errorf("Expect = %q, want %q", m.Expect, want)
			}

			// The prefix direction: the same message with the padding removed is a row the
			// reference accepts too, and recording it would inflate the figure #455 turns on.
			r = s.RunWith(arm.eng(want + " and then some"))
			if r.Fail != 0 || r.ExpectMatched[arm.kind] == 0 {
				t.Fatalf("the prefix fixture did not reach the match site: %d pass / %d fail\n%s",
					r.Pass, r.Fail, r.Board())
			}
			if len(r.SubstringOnly) != 0 {
				t.Errorf("a message *beginning* with the expected text was recorded as a "+
					"divergence: %+v", r.SubstringOnly)
			}
		})
	}
}
