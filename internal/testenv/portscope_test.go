// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// adr0076 is the record this control holds to the tree. Named once, because a path spelled twice is a
// path that will be renamed once.
const adr0076 = "docs/decisions/0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md"

// portScopeFence is the fence's info string in that ADR. The list inside it is prose in every other
// respect; this word is what makes it a checkable claim rather than a sentence.
const portScopeFence = "```text non-conformant-ports"

// TestTheNonConformantPortsAreTheOnesWithoutAMapping holds decision 0076's non-conformance record to the
// build tags, by cross-compilation, on every GOOS the toolchain knows.
//
// # Why this control exists, and why it is not a list
//
// Scott ruled on [#671](https://github.com/scttfrdmn/burroughs/issues/671) that §8 M-1's MUST is **not**
// port-scoped: *"port-scoping makes the clause true by construction everywhere and erases the fact that
// three ports are worse … Record windows, plan9 and the wasm ports as non-conformant with a measured
// figure."* A record of that shape has a failure mode a measured figure does not fix — it goes **stale in
// the favourable direction**. [#674](https://github.com/scttfrdmn/burroughs/issues/674) is filed to give
// windows a `VirtualAlloc` reservation, and the day it lands, an unchecked list in an ADR would still
// name windows as non-conformant while the tree had stopped agreeing. A record that over-reports its own
// gap is not the safe direction of wrong: it is a reason for the next reader to distrust the rest of the
// document.
//
// So the population is **derived, not listed**: `go tool dist list` gives every GOOS the toolchain can
// build for, and `go list` under each one reports which of `reserve_unix.go` / `reserve_other.go` the
// build constraints actually select. Neither half is a judgement call, and neither can be kept in sync
// by remembering to.
//
// # What it asserts, in both directions
//
// Set equality, because each direction catches a different drift:
//
//   - a GOOS on `reserve_other.go` that the ADR does not name is an **unrecorded** non-conformance — the
//     clause is unmet somewhere the document claims it is met;
//   - a GOOS the ADR names that is *not* on `reserve_other.go` is a **stale** non-conformance, which is
//     the #674 case above and the one a human would never think to look for.
//
// # What it deliberately does not assert
//
// That a port *works*. Cross-compiling proves file selection and nothing else, which is the whole reason
// arm F of `internal/interp/memladder_test.go:BenchmarkMemoryReservationLadder` exists beside it: *the
// classification test is runtime-vs-harness*, and this control is entirely on the harness side. The
// composite claim in the ADR — *"these ports are non-conformant, by this much"* — is a build-tag fact from
// here and a timing from there, and stating which half is which is why neither one is asked to carry the
// other's weight.
func TestTheNonConformantPortsAreTheOnesWithoutAMapping(t *testing.T) {
	recorded := recordedNonConformantPorts(t)
	if len(recorded) == 0 {
		t.Fatalf("decision 0076 records no non-conformant ports, so this control asserts nothing.\n"+
			"The record lives in a fenced block opened with %q in\n  %s\n"+
			"An empty list satisfies set equality against an empty derivation and would make this "+
			"test green on a tree that had lost the record entirely", portScopeFence, adr0076)
	}

	derived := derivedNonConformantPorts(t)
	if len(derived) == 0 {
		t.Fatal("no GOOS was classified, so the derivation is empty and the comparison below is " +
			"empty-against-empty — which agrees perfectly and means nothing. Either `go tool dist " +
			"list` returned nothing or every `go list` failed")
	}

	slices.Sort(recorded)
	slices.Sort(derived)
	if slices.Equal(recorded, derived) {
		return
	}

	for _, goos := range derived {
		if !slices.Contains(recorded, goos) {
			t.Errorf("GOOS=%s builds `reserve_other.go` and decision 0076 does not record it as "+
				"non-conformant.\n"+
				"That port takes the allocator's fallback, so `memory.grow` is a full copy on "+
				"it and §8 M-1's MUST is unmet there. Scott's #671 ruling is that the gap is "+
				"recorded rather than the clause narrowed — add it to the fenced list in\n  %s",
				goos, adr0076)
		}
	}
	for _, goos := range recorded {
		if !slices.Contains(derived, goos) {
			t.Errorf("decision 0076 records GOOS=%s as non-conformant and it now builds the "+
				"mapping path.\n"+
				"This is the direction a human does not check: the record over-reports the "+
				"project's own gap, and a document that is wrong in its own favour is a "+
				"document the next reader has to re-verify line by line. If this is "+
				"#674 landing, the ADR's list and its measured figure both need the "+
				"edit — remove it from the fenced list in\n  %s", goos, adr0076)
		}
	}
}

// recordedNonConformantPorts reads the GOOS list out of decision 0076's fenced block.
func recordedNonConformantPorts(t *testing.T) []string {
	t.Helper()

	blob, err := os.ReadFile(filepath.Join(repoRoot, adr0076))
	if err != nil {
		t.Fatalf("reading decision 0076: %v", err)
	}
	var ports []string
	inFence := false
	sc := bufio.NewScanner(strings.NewReader(string(blob)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case !inFence && line == portScopeFence:
			inFence = true
		case inFence && line == "```":
			inFence = false
		case inFence && line != "":
			ports = append(ports, line)
		}
	}
	if inFence {
		t.Errorf("decision 0076's %q block is never closed, so this control read to the end of "+
			"the file and may have taken prose for port names", portScopeFence)
	}
	return ports
}

// derivedNonConformantPorts asks the toolchain which GOOS values select `reserve_other.go`.
//
// One `go list` per GOOS, concurrently, because the sequential form is a dozen-odd process spawns and a
// control that is slow is a control someone will scope down. The first GOOS/GOARCH pair from `go tool dist
// list` is used for each GOOS: the reserve files' constraints are `unix` and `!unix`, which are GOOS
// properties, so the architecture cannot change the answer — and the 32-bit exclusion in `allocate` is
// arithmetic on `math.MaxInt` rather than a build tag, so it is not this control's subject either.
func derivedNonConformantPorts(t *testing.T) []string {
	t.Helper()

	out, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		t.Fatalf("go tool dist list: %v", err)
	}
	firstArch := map[string]string{}
	var order []string
	for _, pair := range strings.Fields(string(out)) {
		goos, goarch, ok := strings.Cut(pair, "/")
		if !ok {
			continue
		}
		if _, seen := firstArch[goos]; !seen {
			firstArch[goos] = goarch
			order = append(order, goos)
		}
	}

	type result struct {
		goos  string
		other bool
		err   error
	}
	results := make([]result, len(order))
	var wg sync.WaitGroup
	for i, goos := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("go", "list", "-f", "{{range .GoFiles}}{{.}} {{end}}",
				"github.com/scttfrdmn/burroughs/internal/interp")
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+firstArch[goos])
			files, err := cmd.Output()
			switch {
			case err != nil:
				results[i] = result{goos: goos, err: fmt.Errorf("go list: %w", err)}
			case strings.Contains(string(files), "reserve_other.go"):
				results[i] = result{goos: goos, other: true}
			case strings.Contains(string(files), "reserve_unix.go"):
				results[i] = result{goos: goos}
			default:
				// Neither file selected is not a third classification — it means the
				// package did not resolve, so this GOOS says nothing about the record
				// and must not be silently counted as conformant.
				results[i] = result{goos: goos, err: fmt.Errorf(
					"neither reserve file was selected, so the package did not resolve")}
			}
		}()
	}
	wg.Wait()

	var ports []string
	var unresolved int
	for _, r := range results {
		if r.err != nil {
			// Reported, not fatal: a GOOS the local toolchain cannot list is a hole in this
			// control's coverage and is named as one. Failing outright would make the control
			// hostage to a toolchain's port set, and *an unmeasured complement is not an empty
			// one* — so the count is printed rather than the miss assumed to be favourable.
			t.Logf("GOOS=%s not classified (%v) — this control's domain is that much smaller "+
				"than the toolchain's port set", r.goos, r.err)
			unresolved++
			continue
		}
		if r.other {
			ports = append(ports, r.goos)
		}
	}
	t.Logf("classified %d of %d GOOS values, %d non-conformant, %d unresolved",
		len(order)-unresolved, len(order), len(ports), unresolved)
	return ports
}
