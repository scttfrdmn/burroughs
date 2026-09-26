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
// # The ENOENT arm was a Burroughs defect, and it took a second engine to say so
//
// A relative READ under an absolute grant fails with **ENOENT**, not EBADF, and the file is readable at
// `/in.txt` under that same grant. I concluded from one engine that this was guest-side. **It is not**, and
// standing property 20 is why the comparison was ordered: wasmtime 49.0.1, given the same guest bytes and
// `--dir D::/`, **reads `in.txt` successfully**. The cause is `burroughs run` forwarding `os.Environ()`, so the
// host's `PWD` becomes the guest's cwd — Go's `wasip1` runtime takes `cwd` from `PWD` and only falls back to
// `preopens[0].name` when it is unset, which is what wasmtime's empty environment produces. Filed as #830,
// where the larger half is that the whole host environment crosses into a sandbox whose model says nothing is
// visible unless named. **The arm below therefore asserts the MECHANISM, not the symptom**: the outcome is
// determined by the forwarded `PWD`, both values run. If #830 stops the forwarding, both values will read
// alike and this arm fails — correctly, because the finding will have changed.
func TestDirFlagMapsOnlyAnAbsoluteGuestPathAndTakesOneColon(t *testing.T) {
	guest := buildGuestFile(t, "cat")
	dir := t.TempDir()
	const body = "dir-flag-probe-body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// The guest's own bytes are the verdict, not the CLI's exit code: before #828 a grant of a path nothing
	// could open left the flag parser perfectly happy, which is the defect these arms were written to measure.
	readable := func(t *testing.T, dirArg, guestPath string) (bool, int, string) {
		t.Helper()
		var out, errBuf bytes.Buffer
		code := dispatch(&out, &errBuf, []string{"run", "--dir", dirArg, guest, "--", guestPath})
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

	t.Run("a_relative_read_is_decided_by_the_forwarded_PWD", func(t *testing.T) {
		// Two values of ONE variable, which is what makes this a mechanism claim rather than a symptom.
		// wasmtime forwards no environment and reads the same file successfully (49.0.1, measured); its
		// behaviour is what `PWD=/` reproduces here.
		type arm struct {
			pwd          string
			wantReadable bool
		}
		for _, a := range []arm{
			{"/", true},
			{dir, false}, // a host path the guest was never granted — what os.Environ() actually forwards
		} {
			t.Setenv("PWD", a.pwd)
			ok, code, stderr := readable(t, dir+":/", "in.txt")
			t.Logf("DIRFLAG relative-read PWD=%q readable=%v exit=%d stderr=%q", a.pwd, ok, code, stderr)
			if ok != a.wantReadable {
				t.Errorf("with PWD=%q the relative read was readable=%v, want %v (exit %d, stderr %q)\n"+
					"\tThe guest's cwd comes from the forwarded PWD (Go's syscall/fs_wasip1.go init), and "+
					"cmd/burroughs/run.go passes os.Environ(). If #830 stopped the forwarding, both arms read "+
					"alike and this arm is what says so.", a.pwd, ok, a.wantReadable, code, stderr)
			}
		}
	})
}
