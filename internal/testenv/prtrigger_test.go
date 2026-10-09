// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The trigger control, and it exists because #411's chosen remedy names its own failure mode.
//
// CI's two PR-body scans (`citecheck.sh --pr`, `closecheck.sh --pr`) read the body as of the
// event that started the run, and a default `pull_request:` trigger never fires on `edited`. So a
// green from them was a statement about *a revision of* the body — and since a squash message is
// derived from the PR title and body by default (grave #314), a `Closes #N` added after that green
// rides into the merge commit past a check that already answered zero.
//
// The fix adds `edited` to the trigger. But `types:` is workflow-wide and an edit cannot change a
// byte the compiler sees, so every job whose subject is the *tree* carries a condition to opt back
// out. #411 states the cost of that shape in one line — **"One trigger, six conditions, and the
// failure mode is a job added later without the condition"** — which is a defect predicted at
// design time, so it gets an instrument at design time rather than a grave later.
//
// # The two directions are different failures and both are checked
//
// A tree job *missing* the condition wastes a runner on an event that cannot change its answer:
// annoying, never wrong. A body job *carrying* it is the original defect restored — the scan stops
// running on the only event that changes its population — and it arrives the plausible way, by
// someone gating the jobs uniformly because five of six had the line. So the allow-list is
// asserted in both directions: `citations` must not carry the condition, and every other job must.
//
// # The domain is derived, not listed
//
// The jobs come from parsing the workflow's `jobs:` mapping and the workflows from a glob, because
// a control scoped to today's six jobs inherits today's blind spot, which is precisely the seventh
// job. `nightly.yml` has no `pull_request:` trigger and is therefore out of scope *by measurement*
// — the scan looks for the trigger rather than being told which file has one.
//
// # The falsification bill
//
// Each neuter was applied to `.github/workflows/ci.yml`, run, and reverted. The arm each one is
// meant to kill is named, because a mutation that reddens the *wrong* arm certifies nothing:
//
//	N1   `edited` dropped from `types:`              trigger, the `edited` half
//	N2   `vuln` loses its condition                  a tree job ungated
//	N3   `citations` gains a job-level condition     a body job gated — the defect restored
//	N4   `types: [edited]` alone                     trigger, the three defaults
//	N5a  the `jobs:` header renamed                  the vacuity floor (0 jobs parsed)
//	N5b  job names re-indented to three spaces       the vacuity floor, from the other regexp
//	N6   `pull_request:` with no `types:` at all     all four types — the pre-#411 shape
//	N7   the `pull_request:` trigger removed         `scoped == 0`, the whole-control vacuity arm
//	N8   `citations` renamed                         the stale-exemption direction
//	N9   `github.event_name != 'edited'`             the exact-expression arm (always-true)
//	N10  `github.event.action != 'edit'`             the exact-expression arm (typo'd action)
//	N11  a *step* of `citations` gains the condition the any-depth arm — and see below
//
// The positive control runs on every green: the `citations` job's own comment contains the
// condition's text, explaining why it is the one job without it, and the any-depth arm must not
// fire on it. A scan that counted prose would fail on the documentation of the property it checks.
//
// **N11 is in the bill because the control did not have that arm when the neuter was first run.**
// A step-level `if:` at eight spaces was invisible to the job-level regexp, so gating the body scan
// one level down passed — the job would start, the scan would not run, and the check would report a
// green it never computed. The arm exists because a falsification was run rather than because a
// review imagined it.
//
// **Two of these neuters did not apply on the first attempt, and both read exactly like a control
// tolerating them.** `jobs:` rewritten with trailing spaces still matched `jobsHeaderRE`, which
// ends `[ \t]*$`; and N9's first substitution silently failed to match. Both printed `ok`, which is
// the same output a control with a missing arm prints. So every neuter above was confirmed *applied*
// — by printing the mutated line — before its result was read: a mutation that never landed is not
// a falsification, and it is indistinguishable from one at the exit code.
//
// # What no control here can assert
//
// Nothing re-scans between the last event and the merge click. Even with `edited` wired up, an
// edit-then-immediately-merge races the run: this narrows the window and does not close the hole.
// Closing it means a `merge_group` trigger and a required-checks change, which is a repo-settings
// decision and Scott's (#411). Stated here because a control that checks the YAML thoroughly is
// exactly the artifact a reader would mistake for a proof that the hole is shut.

// editedCondition is the job-level opt-out, matched exactly.
//
// Exactly rather than approximately: a near miss like `github.event_name != 'edited'` is always
// true and gates nothing, and the whole point of a condition nobody watches is that it reads
// right. The expression is also the one thing a reader can grep the workflow for, so it may not
// drift between the six sites and this file.
const editedCondition = "github.event.action != 'edited'"

// bodyScanJobs are the jobs whose population is the PR body, so they are the jobs that MUST run on
// an `edited` event. The value is the reason, which is the part a reviewer needs; the condition's
// absence is what is checked.
var bodyScanJobs = map[string]string{
	"citations": "its second and fourth invocations scan the PR title and body (`citecheck.sh --pr`, " +
		"`closecheck.sh --pr`), which is the only population in this workflow that an edit changes",
}

var (
	jobsHeaderRE = regexp.MustCompile(`(?m)^jobs:[ \t]*$`)
	jobNameRE    = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):[ \t]*$`)
	// A *job-level* `if:`, which is four spaces. A step's `if:` is deeper, and matching it here
	// would let a condition on one step of a job be read as a condition on the job.
	jobIfRE = regexp.MustCompile(`^    if:[ \t]*(.*[^ \t])[ \t]*$`)
)

// TestBodyEditRetriggersOnlyTheBodyScans is the #411 control over every workflow that has a
// `pull_request:` trigger.
func TestBodyEditRetriggersOnlyTheBodyScans(t *testing.T) {
	paths, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("found %d workflow files; the glob is not finding them, so every assertion below "+
			"is a statement about an empty set", len(paths))
	}

	scoped := 0
	// The union of job names across every scoped workflow, for the global staleness check below.
	seenJobs := map[string]bool{}
	for _, path := range paths {
		src := readFile(t, path)
		types, ok := pullRequestTypes(src)
		if !ok {
			// Out of scope by measurement: no `pull_request:` trigger, so it has no body-scan
			// snapshot to go stale. nightly.yml is the case today.
			t.Logf("%s: no pull_request trigger, out of scope", path)
			continue
		}
		scoped++
		checkTriggerTypes(t, path, types)
		for _, job := range checkJobConditions(t, path, src) {
			seenJobs[job] = true
		}
	}
	// Asked once, over the union — see checkAllowListIsNotStale for why it cannot be per-workflow.
	checkAllowListIsNotStale(t, seenJobs)
	if scoped == 0 {
		t.Fatal("no workflow in .github/workflows has a pull_request trigger, so this control " +
			"asserted nothing at all — either the trigger moved or pullRequestTypes stopped parsing " +
			"it, and both look like a pass")
	}
}

// checkTriggerTypes asserts the trigger names `edited` and has not lost a default.
//
// Both halves, because `types:` *replaces* the default set rather than adding to it: writing
// `types: [edited]` would fix #411 and simultaneously stop the workflow running on pushes to a PR
// branch, trading the entire per-commit gate for a body scan. That trade would be invisible on the
// PR that made it — the run that matters is the *next* push, and there would not be one.
func checkTriggerTypes(t *testing.T, path string, types []string) {
	t.Helper()
	for _, want := range []string{"opened", "synchronize", "reopened", "edited"} {
		if !slices.Contains(types, want) {
			why := "the default set that `types:` replaces"
			if want == "edited" {
				why = "#411's whole subject: without it the PR-body scans read an event-time snapshot"
			}
			t.Errorf("%s: pull_request types are %v, missing %q — %s",
				path, types, want, why)
		}
	}
}

// checkJobConditions asserts every job either carries the opt-out or is a named body-scan job, and
// returns the job names it saw so the caller can check the global allow-list once over their union.
func checkJobConditions(t *testing.T, path, src string) []string {
	t.Helper()

	bodies, order := jobBodies(src)
	// The vacuity floor. An empty job map agrees with every assertion below by having no subject, and
	// a reformatted `jobs:` block or a changed indent produces exactly that.
	//
	// **It was `len(bodies) < 5`, and that was a constant tuned to one file.** When this control was
	// written `ci.yml` was the only workflow with a `pull_request:` trigger and it had seven jobs, so a
	// floor of five read as "the parse found roughly what it should". It is wrong as soon as a second
	// such workflow exists with fewer: `windows.yml` legitimately has **one** job, and the floor failed
	// it for having the right number. A magic number cannot tell "too few jobs" from "few jobs".
	//
	// So the floor is **derived**: count the job headers textually and require the structured parse to
	// agree. That catches what the constant was for — a reformatted block where the regex finds 0 while
	// the file plainly has jobs — and it works for a one-job workflow and a twenty-job one without
	// anyone retuning it. *A literal duplicating a property of the thing it describes is correct once;
	// derive it instead.*
	wantJobs := countJobHeaders(src)
	if wantJobs == 0 {
		t.Fatalf("%s: no job headers found at all; either the file has no `jobs:` block or the text "+
			"scan stopped matching, and both look like a pass to every assertion below", path)
	}
	if len(bodies) != wantJobs {
		t.Fatalf("%s: the structured parse found %d job(s) (%v) where the text scan found %d; the "+
			"parse is not finding them, so the allow-list check has nothing to run against",
			path, len(bodies), order, wantJobs)
	}
	conds := map[string]string{}
	for job, body := range bodies {
		conds[job] = body.cond
	}

	for _, job := range order {
		cond := conds[job]
		reason, isBodyScan := bodyScanJobs[job]
		switch {
		case isBodyScan && cond != "":
			t.Errorf("%s: job %q carries `if: %s`, and it must not — %s.\n\t"+
				"Gating it is #411's defect restored: the scan stops running on the one event that "+
				"changes what it reads, and the way this arrives is gating the jobs uniformly "+
				"because the others have the line.", path, job, cond, reason)
		case !isBodyScan && cond == "":
			t.Errorf("%s: job %q has no job-level `if:`, so a PR-body edit runs it.\n\t"+
				"Either add `if: %s` (its subject is the tree, which an edit cannot change), or — if "+
				"its subject really is the PR body — add it to bodyScanJobs with the reason. This is "+
				"the failure mode #411 named when it chose this shape: a job added later without the "+
				"condition.", path, job, editedCondition)
		case !isBodyScan && cond != editedCondition:
			t.Errorf("%s: job %q carries `if: %s`, want exactly `if: %s`.\n\t"+
				"An expression that is always true gates nothing while reading as though it does, and "+
				"this string is what a reader greps the workflow for.", path, job, cond, editedCondition)
		}
		// The same defect one level down, which the job-level check cannot see and which a
		// falsification found rather than a review: a `citations` *step* carrying the condition
		// stops the body scan running on `edited` while the job still starts. Comments are
		// excluded, because the job's own comment says the words — it explains why it is the one
		// job without the condition, and a scan that counted prose would fail on the
		// documentation of the property it is checking.
		if isBodyScan {
			for _, at := range bodies[job].mentions {
				t.Errorf("%s: job %q has `%s` at %s, and it must not appear anywhere in this job.\n\t"+
					"A step-level condition reintroduces #411 one level down — the job starts, the "+
					"scan does not run, and the check reports a green it never computed. %s",
					path, job, editedCondition, at, reason)
			}
		}
	}

	// **Counted from this file's own jobs, not from the global allow-list.** This read
	// `len(conds)-len(bodyScanJobs)` and `len(bodyScanJobs)`, which is the one-scoped-workflow
	// assumption again: it subtracts the size of a map describing *all* workflows from the job count of
	// *one*. For a workflow holding none of the allow-listed jobs it reported one fewer gated job than
	// there are and a body scan that is not there — `windows.yml` logged "1 jobs, 0 gated, 1 body
	// scans" for a file whose single job is gated and scans nothing.
	if !t.Failed() {
		gated, scans := 0, 0
		for job, cond := range conds {
			if _, isScan := bodyScanJobs[job]; isScan {
				scans++
				continue
			}
			if cond != "" {
				gated++
			}
		}
		t.Logf("%s: %d jobs, %d gated off `edited`, %d body scans running on it",
			path, len(conds), gated, scans)
	}
	return order
}

// checkAllowListIsNotStale asserts every `bodyScanJobs` entry is a real job **somewhere**.
//
// It was per-workflow and inside `checkJobConditions`, which was right while `ci.yml` was the only
// workflow with a `pull_request:` trigger and wrong the moment a second one existed: it demanded that
// every allow-listed job appear in *every* scoped workflow, so `windows.yml` failed for not containing
// `citations` — a job it has no business containing.
//
// The allow-list is **global**, so its staleness is a global question and is asked once, over the union
// of jobs across every scoped workflow. The check itself is worth keeping exactly as it was in spirit:
// *an exemption for a condition that no longer exists is an instrument looking away from nothing*, and
// this is the arm that catches a renamed or deleted body-scan job leaving its exemption behind.
func checkAllowListIsNotStale(t *testing.T, seen map[string]bool) {
	t.Helper()
	for job, reason := range bodyScanJobs {
		if !seen[job] {
			t.Errorf("bodyScanJobs names %q (%s), which is not a job in ANY workflow with a "+
				"pull_request trigger; a stale exemption is an exemption for nothing", job, reason)
		}
	}
}

// pullRequestTypes returns the workflow's `pull_request:` trigger types, and whether the trigger
// is present at all.
//
// A line parse rather than a YAML decode because the engine's go.mod is dependency-free
// (`CLAUDE.md`, conventions) and this is the only place in the tree that needs one. The cost is
// that the parse is structural rather than semantic, which is why every caller carries a floor:
// a parse that finds nothing must fail loudly rather than agree with an empty expectation.
//
// The empty-list case is deliberately distinguished from the absent case. `pull_request:` with no
// `types:` is #411's original state — present, and firing on the default three — so it returns
// (nil, true) and fails the check by missing `edited`, rather than reporting "no trigger" and
// being skipped as out of scope.
func pullRequestTypes(src string) ([]string, bool) {
	lines := strings.Split(src, "\n")
	inOn := false
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "on:"):
			inOn = true
			continue
		// A column-zero key that is not a comment ends the `on:` block.
		case inOn && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#"):
			return nil, false
		case !inOn:
			continue
		}
		if strings.TrimRight(line, " \t") != "  pull_request:" {
			continue
		}
		// Present. Its `types:` is a nested key, so scan forward while the indent is deeper.
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.HasPrefix(next, "    ") {
				return nil, true // no types: at all — the pre-#411 shape
			}
			if rest, ok := strings.CutPrefix(trimmed, "types:"); ok {
				rest = strings.Trim(strings.TrimSpace(rest), "[]")
				var out []string
				for _, tok := range strings.Split(rest, ",") {
					if tok = strings.Trim(strings.TrimSpace(tok), `"'`); tok != "" {
						out = append(out, tok)
					}
				}
				return out, true
			}
		}
		return nil, true
	}
	return nil, false
}

// jobBody is what the parse extracts per job: the job-level condition ("" when there is none) and
// every non-comment line mentioning the condition, positioned.
//
// Two fields rather than one because they answer opposite questions. The condition is *required*
// on a tree job and read at exactly one depth; the mentions are *forbidden* in a body-scan job at
// every depth. A single "does this job mention it" field would have to mean both.
type jobBody struct {
	cond     string
	mentions []string
}

// jobBodies maps each job in the workflow to its jobBody, plus the jobs in file order so a
// diagnosis reads in the order a reader would scroll.
// countJobHeaders counts job headers by a **second, independent** scan, so the structured parse above
// can be checked against something rather than against a constant.
//
// Deliberately not sharing `jobBodies`' state machine: the point is a cross-check, and two readings
// that share the bug they are meant to catch is one reading wearing two names. This walks the whole
// file looking for the two-space `name:` shape at the top of the `jobs:` block, with no notion of
// "current job" to lose — so the reformatting that would make the state machine return an empty map
// does not make this return zero.
//
// The one thing it must not do is count a two-space key that is *not* a job. `on:`, `permissions:` and
// `defaults:` sit at column zero, and everything nested under a job is indented four or more, so the
// two-space depth is the job level by construction in this repo's workflows — and the comparison fails
// loudly if that ever stops being true, which is the right outcome rather than a silent miscount.
func countJobHeaders(src string) int {
	loc := jobsHeaderRE.FindStringIndex(src)
	if loc == nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(src[loc[1]:], "\n") {
		// A column-zero key ends the `jobs:` block: nothing after it belongs to a job.
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
			break
		}
		if jobNameRE.MatchString(line) {
			n++
		}
	}
	return n
}

func jobBodies(src string) (map[string]jobBody, []string) {
	loc := jobsHeaderRE.FindStringIndex(src)
	if loc == nil {
		return nil, nil
	}
	// The line the `jobs:` header sits on, so a position is a file line and not an offset into a
	// substring. A diagnosis that names line 12 of the tail of a file names nothing.
	base := strings.Count(src[:loc[1]], "\n") + 1

	bodies := map[string]jobBody{}
	var order []string
	current := ""
	for i, line := range strings.Split(src[loc[1]:], "\n") {
		if m := jobNameRE.FindStringSubmatch(line); m != nil {
			current = m[1]
			bodies[current] = jobBody{}
			order = append(order, current)
			continue
		}
		if current == "" {
			continue
		}
		body := bodies[current]
		if m := jobIfRE.FindStringSubmatch(line); m != nil {
			body.cond = m[1]
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, editedCondition) {
			body.mentions = append(body.mentions, fmt.Sprintf("line %d", base+i))
		}
		bodies[current] = body
	}
	return bodies, order
}
