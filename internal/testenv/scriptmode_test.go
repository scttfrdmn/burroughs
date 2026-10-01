// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryTrackedScriptIsExecutableInTheIndex asserts that every tracked script carries mode 100755 in
// git's index.
//
// # The specimen
//
// `scripts/civerdict.sh` was created by an editor that does not set the executable bit, and `make ci`
// invoked it directly. The write failed with `Permission denied`, so the verdict file was never written —
// and because the previous run's file was still on disk, **a dead writer was indistinguishable from a live
// green** except that the SHA happened to have moved. The repair for that was to remove the file first; this
// is the repair for the cause.
//
// # Why the index mode and not the filesystem
//
// A local `chmod` fixes one checkout. The index mode is what every other clone and CI get, so it is the
// thing that can be wrong for everyone at once — and it is the thing a `git add` of an editor-created file
// silently sets to 100644.
//
// # The domain is derived, not listed
//
// A list of `scripts/*.sh` would miss `scripts/labrun` and `scripts/labprov`, which have no extension, and
// would say nothing about the `.py` files. The population is instead **every tracked file whose first line
// is a shebang**: a shebang is a declaration that the file is meant to be executed, and whether a given
// caller happens to prefix an interpreter today is not a property of the file. That derivation is what makes
// this non-vacuous — it caught `scripts/editroute.py` at 100644, the hook's own implementation, which worked
// only because the settings file invokes it through `python3`.
func TestEveryTrackedScriptIsExecutableInTheIndex(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	out, lsErr := exec.Command("git", "-C", root, "ls-files", "-s").Output()
	if lsErr != nil {
		t.Fatalf("git ls-files -s: %v", lsErr)
	}

	type entry struct {
		path string
		mode string
	}
	var shebanged []entry
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		// `<mode> <hash> <stage>\t<path>` — the path is after the TAB, so splitting on whitespace would
		// break on any path containing a space.
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(line[:tab])
		if len(fields) < 1 {
			continue
		}
		path := line[tab+1:]

		full := filepath.Join(root, path)
		fh, openErr := os.Open(full)
		if openErr != nil {
			continue // a tracked file absent from the worktree is not this control's subject
		}
		first := ""
		fsc := bufio.NewScanner(fh)
		if fsc.Scan() {
			first = fsc.Text()
		}
		_ = fh.Close()
		if strings.HasPrefix(first, "#!") {
			shebanged = append(shebanged, entry{path: path, mode: fields[0]})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	// A floor on the population, because an empty domain passes every assertion below. This is a floor and
	// not a census: it sits below the real count so it does not need editing when a script is added.
	const floor = 15
	if len(shebanged) < floor {
		t.Fatalf("only %d tracked files have a shebang, want at least %d — the derivation is broken and "+
			"this control is passing over an empty or truncated population", len(shebanged), floor)
	}

	var bad []string
	for _, e := range shebanged {
		if e.mode != "100755" {
			bad = append(bad, e.mode+" "+e.path)
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d tracked script(s) are not 100755 in the index:\n  %s\n\n"+
			"A shebang says the file is meant to be executed. An editor that creates a file does not set "+
			"the bit, and `git add` then records 100644 for every clone and for CI — which is how a "+
			"directly-invoked writer died with `Permission denied` and left the previous run's verdict in "+
			"place, where a dead writer looks exactly like a live green.\n"+
			"Fix with: git update-index --chmod=+x <path>",
			len(bad), strings.Join(bad, "\n  "))
	}
	// Printed as a census rather than as a verdict. The first version said "all 100755" unconditionally and
	// printed it *beside* a failure naming four that were not — a control's own output asserting the opposite
	// of its finding, which no sweep can see because it is a literal.
	t.Logf("SCRIPT MODES: %d tracked file(s) with a shebang, %d not 100755", len(shebanged), len(bad))
}

// TestCIRemovesItsVerdictBeforeRunningTheGates asserts the ordering that makes a failed run leave **absence
// rather than staleness**.
//
// # Why absence is the state worth guaranteeing
//
// `prmerge.sh` refuses a missing verdict by name. A stale one it can only refuse by noticing the SHA moved,
// which is luck: a run that dies before writing, on a tree whose tip has not changed since the last run,
// produces a file that is indistinguishable from a fresh green. That happened — see
// `TestEveryTrackedScriptIsExecutableInTheIndex` — and the SHA had moved, which is the only reason it was
// caught.
//
// # Why `make -n`
//
// The ordering was asserted only by reading the Makefile, and a reordering would have been invisible.
// `make -n` prints the recipe without running the gates, so the order is checkable in milliseconds rather
// than in a full `make ci`. It is the recipe's *text* in execution order, which is exactly the subject —
// this is not a claim that the gates pass.
func TestCIRemovesItsVerdictBeforeRunningTheGates(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("make", "-n", "ci")
	cmd.Dir = root
	// MAKEFLAGS is cleared so an ambient value from a parent `make` (this test can run under `make ci`
	// itself) cannot change what -n prints.
	cmd.Env = append(os.Environ(), "MAKEFLAGS=")
	printed, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("make -n ci: %v\n%s", runErr, printed)
	}
	text := string(printed)

	idxRemove := strings.Index(text, "rm -f .ci-verdict")
	idxWrite := strings.Index(text, "civerdict.sh")
	idxGates := strings.Index(text, "ci-gates")

	if idxRemove < 0 {
		t.Fatalf("`make ci` does not remove .ci-verdict at all. Without it, a run that dies before "+
			"writing leaves the PREVIOUS verdict on disk, and prmerge.sh can only catch that when the SHA "+
			"has moved:\n%s", text)
	}
	if idxWrite < 0 {
		t.Fatalf("`make ci` never invokes civerdict.sh, so it records no verdict:\n%s", text)
	}
	if idxGates < 0 {
		t.Fatalf("`make ci` does not reach ci-gates:\n%s", text)
	}
	if idxRemove > idxGates {
		t.Errorf("the removal of .ci-verdict comes AFTER the gate run, so a gate that dies hard leaves a "+
			"stale verdict:\n%s", text)
	}
	if idxRemove > idxWrite {
		t.Errorf("the removal of .ci-verdict comes AFTER civerdict.sh writes it, which deletes the verdict "+
			"the run just produced:\n%s", text)
	}
	t.Logf("CI ORDER: rm at %d, ci-gates at %d, civerdict.sh at %d", idxRemove, idxGates, idxWrite)
}
