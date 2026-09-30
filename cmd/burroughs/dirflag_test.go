// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDirFlagMapsOnlyAnAbsoluteGuestPathAndTakesOneColon probes `--dir`'s mapping grammar by RUNNING a guest
// read through each form, rather than by reading `preopenFlag.Set` and concluding what the guest will see.
//
// # Why this is committed, and why it is not the test I set out to write
//
// A `--dir` invocation failed twice while measuring the unaimed-program sweep, both times presenting as a
// missing file rather than as a flag mistake. I attributed that to wasmtime's `HOST::GUEST` grammar, which is
// a real trap — and then grepped what had actually been typed, which contained **no double-colon invocation at
// all**. The forms used were `HOST`, `HOST:/` and `HOST:.`, and the one that failed is the relative one. So
// the separator was never the cause, and the arms below are the ones the failures were actually in.
//
// *A model-mechanics claim is a hypothesis until run* — three times over here: once for the separator, once
// for the cwd, and once for which engine was at fault.
//
// # The forms, and what each is measured to do
//
//	HOST          maps the directory under its RESOLVED own name. Readable there.
//	HOST:/GUEST   the documented form: maps HOST at the absolute guest path. Readable there.
//	HOST:.        REFUSED at parse time (#828). Measured dead first: the grant succeeded and every open
//	              through it returned EBADF.
//	HOST::GUEST   REFUSED, with an error naming wasmtime's grammar. Also measured dead first, the same way.
//
// # The ENOENT arm was a Burroughs defect, it took a second engine to say so, and it is now FIXED
//
// A relative read under an absolute grant used to fail with **ENOENT**, not EBADF, while the same file was
// readable at `/in.txt` under that same grant. I concluded from one engine that this was guest-side. **It was
// not**, and standing property 20 is why the comparison was ordered: wasmtime 49.0.1, given the same guest
// bytes and `--dir D::/`, read `in.txt` successfully. The cause was `burroughs run` forwarding `os.Environ()`,
// so the host's `PWD` became the guest's cwd — Go's `wasip1` runtime takes `cwd` from `PWD` and falls back to
// `preopens[0].name` only when it is unset, which is what wasmtime's empty environment produces.
//
// **The arm below was registered to flip when #830 landed, and it flipped.** Its previous form asserted that
// the outcome was *determined by the forwarded `PWD`*, with a note saying that if the forwarding stopped both
// values would read alike and the arm would fail — correctly, because the finding would have changed. #830
// landed, both values read alike, and the arm went red on exactly the sentence that predicted it. What
// replaces it asserts **both halves of the repair**, which is strictly more than the old arm did:
//
//	no --env at all           the relative read SUCCEEDS, and the host's PWD is irrelevant — two host
//	                          values, same outcome. This is the leak being closed.
//	--env PWD=<elsewhere>     the relative read FAILS. The mechanism is still reachable, by explicit
//	                          grant only. This is the capability working.
//
// A single arm asserting only the first half would pass against a CLI that had lost the ability to set `PWD`
// at all, which is why the second is there.
func TestDirFlagMapsOnlyAnAbsoluteGuestPathAndTakesOneColon(t *testing.T) {
	guest := buildGuestFile(t, "cat")
	dir := t.TempDir()
	const body = "dir-flag-probe-body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// The guest's own bytes are the verdict, not the CLI's exit code: before #828 a grant of a path nothing
	// could open left the flag parser perfectly happy, which is the defect these arms were written to measure.
	readable := func(t *testing.T, dirArg, guestPath string, extra ...string) (bool, int, string) {
		t.Helper()
		var out, errBuf bytes.Buffer
		argv := append([]string{"run", "--dir", dirArg}, extra...)
		argv = append(argv, guest, "--", guestPath)
		code := dispatch(&out, &errBuf, argv)
		return strings.Contains(out.String(), body), code, errBuf.String()
	}

	t.Run("bare_host_maps_under_its_resolved_own_name", func(t *testing.T) {
		ok, code, stderr := readable(t, dir, filepath.Join(dir, "in.txt"))
		if !ok {
			t.Errorf("a bare HOST did not make the file readable at the HOST path (exit %d): %s\n"+
				"\tHOST alone means HOST:abs(HOST). This is the form that DID work during the sweep, which is "+
				"why #822's archive/zip finding is not a --dir artifact: its run read testdata at "+
				"n=154 err=<nil>.", code, stderr)
		}
	})

	t.Run("absolute_guest_path_is_the_documented_form", func(t *testing.T) {
		ok, code, stderr := readable(t, dir+":/d", "/d/in.txt")
		if !ok {
			t.Errorf("HOST:/GUEST did not make the file readable at the guest path (exit %d): %s\n"+
				"\tADR 0083's grant. If this breaks, TestRunGrantsAndDeniesFilesystemAccess breaks with it.",
				code, stderr)
		}
	})

	// #828's refusals. Each was measured DEAD before it was refused, so the refusal replaces a specific
	// observed failure rather than a guess about one — and the error must name the shape, because an operator
	// who sees only "invalid --dir" has to rediscover which half of their argument was wrong.
	for _, tc := range []struct {
		name, dirArg string
		wantInError  []string
	}{
		{
			name: "relative_guest_path_is_refused", dirArg: ".",
			wantInError: []string{"not absolute", "EBADF"},
		},
		{
			name: "relative_guest_path_without_a_dot_is_refused_too", dirArg: "sub",
			wantInError: []string{"not absolute"},
		},
		{
			name: "wasmtime_two_colon_form_is_refused_and_says_so", dirArg: ":/d",
			wantInError: []string{"starts with a colon", "wasmtime"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, code, stderr := readable(t, dir+":"+tc.dirArg, "/d/in.txt")
			t.Logf("DIRFLAG refused-form %q: readable=%v exit=%d stderr=%q", tc.dirArg, ok, code, stderr)
			if ok {
				t.Fatalf("--dir HOST:%s made the file readable: this form is refused, so either the parser "+
					"was widened or this expectation is wrong", tc.dirArg)
			}
			if code == 0 {
				t.Errorf("--dir HOST:%s exited 0; a refused flag must fail the run", tc.dirArg)
			}
			for _, want := range tc.wantInError {
				if !strings.Contains(stderr, want) {
					// The message is the whole value of the refusal: the run failed before #828 too, just
					// without saying why.
					t.Errorf("--dir HOST:%s: stderr %q does not mention %q — the refusal must name the shape, "+
						"or it is no more useful than the EBADF it replaced", tc.dirArg, stderr, want)
				}
			}
		})
	}

	t.Run("a_relative_read_no_longer_depends_on_the_hosts_PWD", func(t *testing.T) {
		// #830's half: with nothing forwarded, the guest's cwd falls back to the preopen name, so the
		// host's own PWD cannot move where a guest path resolves. Two host values, one outcome.
		for _, hostPWD := range []string{"/", dir, "/somewhere/else"} {
			t.Setenv("PWD", hostPWD)
			ok, code, stderr := readable(t, dir+":/", "in.txt")
			if !ok {
				t.Errorf("with the host's PWD=%q the relative read failed (exit %d, stderr %q)\n"+
					"\tNothing is forwarded to the guest unless --env names it (#830), so the host's "+
					"PWD must not reach the guest's cwd at all. If this fails, the forwarding is back.",
					hostPWD, code, stderr)
			}
		}
	})

	t.Run("and_PWD_still_moves_it_when_explicitly_granted", func(t *testing.T) {
		// The other half. Without this, the arm above would also pass against a CLI that had lost the
		// ability to set PWD at all — a capability silently removed reads exactly like a leak fixed.
		t.Setenv("PWD", "/")
		ok, code, stderr := readable(t, dir+":/", "in.txt", "--env", "PWD=/somewhere/else")
		if ok {
			t.Errorf("with --env PWD=/somewhere/else the relative read still succeeded (exit %d): the "+
				"grant is not reaching the guest's cwd, so --env is not carrying PWD", code)
		}
		if !strings.Contains(stderr, "No such file or directory") {
			t.Errorf("stderr %q does not name ENOENT: the granted PWD should resolve the read to a "+
				"path inside the grant that does not exist, which is a different failure from EBADF",
				stderr)
		}
	})
}

// TestDirIsReadOnlyAndOnlyScratchGrantsWrites is the CLI-level half of ADR 0091's default-unchanged claim.
//
// **It exists because an injection survived.** Making `preopenFlag.preopens()` set `Writable: true` — i.e.
// every `--dir` silently becoming a write grant — left every test in the tree green.
// `TestRunGrantsAndDeniesFilesystemAccess` only exercises reads, and the library-level two-grant witness
// builds `Preopen` values directly rather than going through the flags, so **nothing connected the flag
// name to the capability**. The engine was witnessed; the CLI's mapping onto it was not.
//
// The two arms run the same guest, so the flag is the only variable.
func TestDirIsReadOnlyAndOnlyScratchGrantsWrites(t *testing.T) {
	guest := buildGuestFile(t, "scratchwrite")

	run := func(t *testing.T, flag string) (steps map[string]string, leftover int) {
		t.Helper()
		dir := t.TempDir()
		var out, errBuf bytes.Buffer
		dispatch(&out, &errBuf, []string{"run", flag, dir + ":/s", guest, "--", "/s"})
		steps = map[string]string{}
		for _, ln := range strings.Split(out.String(), "\n") {
			if !strings.HasPrefix(ln, "STEP ") {
				continue
			}
			if parts := strings.SplitN(strings.TrimPrefix(ln, "STEP "), " ", 2); len(parts) == 2 {
				steps[parts[0]] = parts[1]
			}
		}
		if !strings.Contains(out.String(), "SCRATCH-END") {
			t.Fatalf("%s: the guest did not reach its end marker:\n%s\nstderr: %s", flag, out.String(), errBuf.String())
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		return steps, len(ents)
	}

	roSteps, roLeft := run(t, "--dir")
	rwSteps, rwLeft := run(t, "--scratch")

	for _, name := range []string{"mkdir", "writefile", "open-rdwr", "unlink", "rmdir"} {
		if roSteps[name] == "ok" {
			t.Errorf("--dir permitted %q: --dir is READ-ONLY, and only --scratch grants writes (ADR 0091)", name)
		}
		if rwSteps[name] != "ok" {
			t.Errorf("--scratch refused %q (%q): without this arm the refusals above would also be "+
				"satisfied by a CLI that granted nothing at all", name, rwSteps[name])
		}
	}
	// The host's own account, because "the guest was told no" and "nothing was written" are two facts.
	if roLeft != 0 {
		t.Errorf("--dir left %d entr(ies) in the host directory; every write reported refused and "+
			"something landed anyway", roLeft)
	}
	if rwLeft != 0 {
		t.Errorf("--scratch left %d entr(ies) behind; the guest removed what it created, so a leftover "+
			"means an unlink or rmdir silently did nothing", rwLeft)
	}
	t.Logf("CLI GRANTS: --dir=%v --scratch=%v", roSteps, rwSteps)
}
