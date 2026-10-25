// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// boardFingerprint is one file's verdict, command by command: the counts, every failing line with what was
// expected and what came back, and every gated line. Counts alone would miss a swap, such as one command
// moving pass→fail while another moves fail→pass in the same file.
func boardFingerprint(r *Result) string {
	var fails []string
	for _, fs := range r.Buckets {
		for _, f := range fs {
			fails = append(fails, fmt.Sprintf("%d|%s|%s|%v", f.Line, f.Expect, f.Got, f.Declined))
		}
	}
	sort.Strings(fails)
	gated := append([]int(nil), r.GatedAt...)
	sort.Ints(gated)
	return fmt.Sprintf("p%d f%d u%d g%d i%d b%d\nfails:%s\ngated:%v",
		r.Pass, r.Fail, r.Unsupported, r.Gated, r.Unimplemented, r.Bound, strings.Join(fails, ";"), gated)
}

// fingerprintUnder scores one file under a feature set and returns its fingerprint. Each call parses afresh,
// so the two settings never share a script's state.
func fingerprintUnder(t *testing.T, path string, eng func() Engine) string {
	t.Helper()
	s, err := ParseFile(path)
	if err != nil {
		t.Fatalf("%s: parse: %v", filepath.Base(path), err)
	}
	return boardFingerprint(s.RunGated(eng()))
}

// TestThreadsGateLeavesTheCoreBoardUnchanged is gap G1 of the `gate:threads` flip forecast on #10 (the chair's
// ruling of 2026-10-25): **a module that uses no threads feature reads the same with the gate off and on.**
//
// Every core board file is scored twice, under `DefaultFeatures()` and under the same set with `Threads`
// added, and the two **per-file** fingerprints must match. A fingerprint is per command, not a total: the
// counts, every failing line with its expected and actual answer, and every gated line.
//
// **The whole core board is asserted, not a filtered subset.** On the day this landed, all 256 files were
// invariant, including any that mention a threads construct only in a vector that is invalid either way. So
// a filter would exempt nothing, and a file that one day starts to differ is a finding. If it differs because
// it uses threads, the fix is to move it into a lane that states its gates, not to teach this control to look
// away.
//
// **The floor:** the threads proposal's own files must differ, through the same comparison. 3 of 4 did
// (`atomic.wast`, `imports.wast`, `memory.wast`, the files the threads lane calls gate-sensitive). Without
// this, two identical engines, a gate that was never applied, would pass the core arm as well.
//
// NEUTER, run: `i32.add` (0x6a) added to `gatedOpcodes` under `gateThreads`, an unrelated opcode
// feature-checked under the gate. The core arm then failed on 36 of the 256 files, each named, starting
// `block.wast`, `br.wast`, `br_if.wast`, `call.wast`.
func TestThreadsGateLeavesTheCoreBoardUnchanged(t *testing.T) {
	requireSuite(t)
	// Both settings are stated explicitly, so this control means the same thing before and after the
	// `gate:threads` flip. Taking `DefaultFeatures()` as "off" would make the two sets identical the moment the
	// default flips, and the comparison would then be of a set against itself.
	off := binary.DefaultFeatures()
	off.Threads = false
	on := off
	on.Threads = true
	_, engOff := gateLane(off)
	_, engOn := gateLane(on)

	files := boardFiles(t)
	if len(files) < 250 {
		t.Fatalf("the core board lists %d files; a short list would make an empty diff mean little", len(files))
	}
	var differ []string
	for _, f := range files {
		p := filepath.Join(suiteDir, f)
		if a, b := fingerprintUnder(t, p, engOff), fingerprintUnder(t, p, engOn); a != b {
			differ = append(differ, f)
			t.Errorf("%s reads differently with gate:threads on.\n  off: %s\n  on:  %s", f, a, b)
		}
	}
	t.Logf("core board: %d of %d files read identically with gate:threads off and on", len(files)-len(differ), len(files))

	tp, err := filepath.Glob(filepath.Join(suiteDir, "proposals", threadsProposal, "*.wast"))
	if err != nil || len(tp) == 0 {
		t.Fatalf("no threads proposal files under %s (%v), so the floor cannot be read", suiteDir, err)
	}
	var sensitive []string
	for _, p := range tp {
		if fingerprintUnder(t, p, engOff) != fingerprintUnder(t, p, engOn) {
			sensitive = append(sensitive, filepath.Base(p))
		}
	}
	if len(sensitive) == 0 {
		t.Fatalf("none of the %d threads proposal files reads differently with the gate on, so the two engines are "+
			"identical and the core arm's empty diff says nothing about the gate", len(tp))
	}
	t.Logf("floor: %d of %d threads proposal files read differently with the gate on: %v", len(sensitive), len(tp), sensitive)
}
