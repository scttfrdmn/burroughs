// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/spec"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// # The materiality input for #136 — how far is a real opener from its `end`?
//
// `internal/interp/scanbench` established that `matchEnd`'s cost **exists** and **scales with
// distance**, on modules whose distances were chosen for convenience: 0, 64, 512, 4096. It could not
// say whether any of those distances occur in code anyone writes, so it could not say whether the cost
// matters. This file supplies the missing half, and it is deliberately the *cheap* half — the suite is
// already vendored, so the distribution is obtainable statically with no guest to build.
//
// Ordered by Scott on the #502 review: *"the materiality datum is wanted, and it goes first … measure
// it before the A/B, because it tells you which distances are real. Sweeping distances chosen from that
// distribution rather than from convenience is what makes the ≥5%-at-largest-distance criterion mean
// something."*
//
// # What is measured, in the units the scan actually pays
//
// For every structural opener in every function body the suite's modules decode to, the distance is
// `matchEnd(body, pc) - pc`: **the number of `[]binary.Instr` slots the scan walks**. Not bytes, not
// source lines, not nesting depth — the loop in `matchEnd` advances one slot per iteration, so the slot
// count is the cost. Measured by calling `matchEnd` rather than by reimplementing the walk, which is
// why this file is in `package interp`: *measure with the instrument, not with a second copy of it.*
//
// The `if`s are counted at their opener too, because `matchEnd` is what `opIf` calls to find its own
// `end`; `elseOf` scans the same span again, so an `if` with an else-arm pays the span twice and this
// distribution under-counts it. Stated rather than modelled — a weighting nobody asked for would be a
// second unmeasured claim inside the measurement meant to remove one.
//
// # The limitation that decides how this may be used, stated before the numbers
//
// **This is a static census of openers in code, not a dynamic census of block entries.** `matchEnd` is
// called once per *executed* block entry, so the cost a program actually pays is this distribution
// weighted by execution count — and the two differ in the direction that matters: a cold error path
// with a 4000-slot body contributes one opener here and zero entries at run time, while a two-slot loop
// body inside a hot loop contributes one opener here and millions of entries. So:
//
//   - a distance that is **absent** here is absent from code, which is a real negative and the useful
//     half: it retires the swept distances nothing in the corpus reaches;
//   - a distance that is **common** here is not thereby common at run time, and no claim in that
//     direction may be sourced from this test.
//
// Naming this is not a hedge. #502's probe already showed that the scanned-to-executed ratio is the
// whole question — its padding was unexecuted by construction, giving 6.4:1 at d=64 against 1:1 in a
// real loop — and a static distribution is the same confound one level out. The dynamic half needs an
// execution counter in `runFrame` and is not built here.
//
// # Why the corpus is the right population despite not being Go
//
// The thesis workload is Go guests, and this suite is hand-written conformance tests — so the honest
// statement is that this is the **only** distribution obtainable without building the guest Scott
// declined, and that it bounds the question rather than answering it. What it can settle is whether the
// probe's swept distances were fantasy: if the corpus's maximum is 300 slots, the 4096 row measured
// something no program contains, and the criterion has to be restated over distances that occur.
const scanDistSuiteDir = "../../testdata/spec"

// scanDistBuckets are the distribution's report buckets, chosen to straddle the probe's swept
// distances (0, 64, 512, 4096) so the two measurements can be read against each other. A bucket
// boundary at a swept distance is deliberate: it makes "how many openers are at least as far as the
// row I benchmarked" answerable by reading one line.
var scanDistBuckets = []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096}

// TestSuiteScanDistanceDistributionIsMeasured prints the distribution and asserts only what a
// measurement must assert to be one: that it looked at a plausible amount of the corpus.
//
// **No pin on the shape.** The suite is revision-pinned, so an exact histogram would be reproducible —
// and it would also be a second oracle for a fact this test exists to *report*, in a file whose whole
// point is that nobody yet knows which distances matter. A pin would freeze a distribution before
// anyone has decided what about it is load-bearing, which is *an unmeasured stability claim* wearing a
// control's clothes. The floors below are vacuity guards, not a shape claim: they fire when the scan
// stops seeing the corpus, which is the failure this test can actually have.
func TestSuiteScanDistanceDistributionIsMeasured(t *testing.T) {
	testenv.RequireSuite(t, scanDistSuiteDir)

	paths, err := testenv.SuitePaths(scanDistSuiteDir)
	if err != nil {
		t.Fatalf("SuitePaths %s after RequireSuite passed: %v", scanDistSuiteDir, err)
	}

	var (
		dists       []int
		modulesOK   int
		modulesFail = map[string]int{}
		funcs       int
		bodiesEmpty int
		byOp        = map[string]int{}
		matchErrs   int
		// The witnesses. A tail count with no site is a number the next reader cannot check, and
		// the interesting cell here is the *empty* one — an exact zero on the distances #502
		// benchmarked is the shape that is usually an instrument reporting its own blindness, so
		// the report carries the widest spans found and the longest body seen. If the longest body
		// in the corpus is shorter than a swept distance, the zero is arithmetic rather than luck.
		widest    []scanDistWitness
		maxBody   int
		maxBodyAt string
	)

	for _, p := range paths {
		s, err := spec.ParseFile(filepath.Join(scanDistSuiteDir, p))
		if err != nil {
			// A parse failure is the harness's business, not this measurement's, and the board
			// already scores it. Counted so the population is not silently smaller than it looks.
			modulesFail["wast parse"]++
			continue
		}
		for _, c := range s.Commands {
			img, ok := scanDistImage(c)
			if !ok {
				continue
			}
			m, err := binary.DecodeModule(img)
			if err != nil {
				// Expected in bulk: the suite is full of modules that must *fail* to decode.
				// Bucketed by nothing finer than "decode", because which malformed module refused
				// is `assert_malformed`'s subject and not this file's.
				modulesFail["decode"]++
				continue
			}
			modulesOK++
			for i := range m.Funcs {
				body := m.Funcs[i].Body
				funcs++
				if len(body) > maxBody {
					maxBody, maxBodyAt = len(body), p
				}
				if len(body) == 0 {
					bodiesEmpty++
					continue
				}
				for pc := range body {
					ins := body[pc]
					if ins.Prefix != 0x00 {
						continue
					}
					name, structural := scanDistOpName(ins.Op)
					if !structural {
						continue
					}
					end, err := matchEnd(body, pc)
					if err != nil {
						// `matchEnd`'s not-found arm is the layering debt its own comment names:
						// this walks *decoded* bodies, so it should be unreachable here, and a
						// non-zero count is a finding rather than noise.
						matchErrs++
						continue
					}
					byOp[name]++
					dists = append(dists, end-pc)
					widest = scanDistKeep(widest, scanDistWitness{d: end - pc, op: name, file: p, fn: i})
				}
			}
		}
	}

	// Vacuity guards. The corpus is pinned, so these are far below the live figures and exist to
	// catch a scan that stopped seeing it — *a floor bounds the catastrophic case only*, which is
	// exactly what is wanted from a test whose subject is a distribution nobody has pinned.
	if modulesOK < 500 {
		t.Errorf("decoded %d modules from %d suite files, want >=500 — a distribution over a corpus "+
			"this test can no longer read is a print with no subject", modulesOK, len(paths))
	}
	if len(dists) < 2000 {
		t.Errorf("measured %d opener distances, want >=2000 — see above", len(dists))
	}

	if matchErrs > 0 {
		t.Errorf("matchEnd reported no END for %d openers in *decoded* bodies. Its own header says "+
			"the decoder cannot produce an unterminated body, so this is either that claim being "+
			"false or this walk handing it a pc it does not own", matchErrs)
	}

	sort.Ints(dists)
	t.Logf("modules decoded: %d (skipped: %s) · functions: %d (empty bodies: %d) · openers: %d\n"+
		"  by opener: %s\n%s"+
		"  longest function body in the corpus: %d slots (%s)\n"+
		"  widest spans, with witnesses:\n%s",
		modulesOK, scanDistMap(modulesFail), funcs, bodiesEmpty, len(dists),
		scanDistMap(byOp), scanDistReport(dists), maxBody, maxBodyAt, scanDistWitnesses(widest))

	// The independent check on the empty buckets. A span cannot exceed the body that contains it, so
	// the longest body is an arithmetic ceiling on the largest distance — a second mechanism for the
	// zero above, and one that cannot fail the same way the opener walk can. Asserted rather than
	// printed, because the reason to have it is that a clean zero from one mechanism is not evidence.
	if len(dists) > 0 && dists[len(dists)-1] > maxBody {
		t.Errorf("largest opener span %d exceeds the longest body seen (%d slots, %s), which is "+
			"impossible: a span is contained in its body, so one of the two walks is wrong",
			dists[len(dists)-1], maxBody, maxBodyAt)
	}
}

// scanDistImage picks the image for a command, or reports that the command carries no module.
//
// **Both arms, deliberately.** `KindModuleBinary` and friends carry a wire image directly; the text
// kinds carry source and go through `text.EncodeModule`, which is the only path from wat to an image
// (0011). Taking only the binary arm would have measured the suite's *malformed-encoding* files and
// almost nothing else, since the overwhelming majority of real modules in the corpus are written as
// text — a population selected by which field happened to be populated.
func scanDistImage(c spec.Command) ([]byte, bool) {
	if len(c.Module) > 0 {
		return c.Module, true
	}
	if len(c.Source) == 0 {
		return nil, false
	}
	img, err := text.EncodeModule(c.Source)
	if err != nil {
		return nil, false
	}
	return img, true
}

// scanDistOpName names the four instructions `matchEnd` counts depth on, and reports whether the
// opcode is one of them.
//
// Derived from `matchEnd`'s own switch rather than from a list of opcodes someone believed were
// structural: if a fifth opener lands, the two go out of sync and this census silently stops counting
// it. That is the *scope controls to the space* failure, and the honest mitigation short of sharing the
// switch is to say so here — the sibling case, `opElse`, is not an opener and closes nothing.
func scanDistOpName(op uint32) (string, bool) {
	switch op {
	case opBlock:
		return "block", true
	case opLoop:
		return "loop", true
	case opIf:
		return "if", true
	case opTryTable:
		return "try_table", true
	}
	return "", false
}

// scanDistReport renders the distribution: percentiles, then a cumulative tail by bucket.
//
// The **tail** is the column that answers #136's question. "How many openers are at least 64 slots
// from their end" is what decides whether the probe's 64 row described anything, and a per-bucket
// count cannot be read that way without the reader summing columns by eye — *count with a counter*.
func scanDistReport(sorted []int) string {
	if len(sorted) == 0 {
		return "  (no openers)"
	}
	var b strings.Builder
	pct := func(q float64) int {
		i := int(q * float64(len(sorted)-1))
		return sorted[i]
	}
	fmt.Fprintf(&b, "  distance in decoded instruction slots (matchEnd's own unit):\n"+
		"    min %d · p50 %d · p90 %d · p99 %d · p99.9 %d · max %d · mean %.1f\n",
		sorted[0], pct(0.50), pct(0.90), pct(0.99), pct(0.999), sorted[len(sorted)-1],
		scanDistMean(sorted))
	b.WriteString("    cumulative tail — openers at least this far from their end:\n")
	for _, lo := range scanDistBuckets {
		n := len(sorted) - sort.SearchInts(sorted, lo)
		if n == 0 {
			fmt.Fprintf(&b, "      >=%-6d %6d  (0.000%%)  <- and every larger bucket\n", lo, n)
			break
		}
		fmt.Fprintf(&b, "      >=%-6d %6d  (%.3f%%)\n", lo, n, 100*float64(n)/float64(len(sorted)))
	}
	return b.String()
}

func scanDistMean(xs []int) float64 {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// scanDistMap renders a count map in a stable order, because a map printed in range order makes two
// identical runs look like two different measurements.
func scanDistMap(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// scanDistWitness is one measured span with enough provenance to be re-derived by hand.
type scanDistWitness struct {
	d    int
	op   string
	file string
	fn   int
}

// scanDistKeep holds the widest few spans, so the tail of the distribution has sites attached.
// Insertion sort into a fixed-size slice: the population is small and a full sort of every span with
// its provenance would cost more than the measurement.
func scanDistKeep(top []scanDistWitness, w scanDistWitness) []scanDistWitness {
	const keep = 6
	i := sort.Search(len(top), func(i int) bool { return top[i].d < w.d })
	if i >= keep {
		return top
	}
	top = append(top, scanDistWitness{})
	copy(top[i+1:], top[i:])
	top[i] = w
	if len(top) > keep {
		top = top[:keep]
	}
	return top
}

func scanDistWitnesses(top []scanDistWitness) string {
	var b strings.Builder
	for _, w := range top {
		fmt.Fprintf(&b, "    %6d slots  %-9s %s (func index %d)\n", w.d, w.op, w.file, w.fn)
	}
	return b.String()
}
