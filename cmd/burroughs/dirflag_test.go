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
// That is why this lives in the tree: the grammar is something this package can ask directly, and a scrollback
// is not a place an answer survives. *A model-mechanics claim is a hypothesis until run* — twice here, once
// for the separator and once for the cwd.
//
// # The forms, and what each is measured to do
//
//	HOST          maps the directory under its own (absolute host) name. READABLE there.
//	HOST:/GUEST   the documented form: maps HOST at the absolute guest path. READABLE there.
//	HOST:.        ACCEPTED AND DEAD. A relative guest path; the grant succeeds and the guest's open returns
//	              EBADF, "Bad file number" — a preopen name it can never resolve against.
//	HOST::GUEST   wasmtime 14+'s form, NOT this one. `strings.Cut` splits at the first colon, so the guest
//	              path becomes ":/d". Also accepted, also dead, also EBADF.
//
// # The errno is the discriminator, not the failure
//
// A relative READ under an absolute grant (`--dir HOST:/` then open `in.txt`) fails with **ENOENT**, not
// EBADF, and the file is demonstrably readable at `/in.txt` under that same grant. Two different errnos mean
// two different mechanisms: EBADF is "no preopen matches this name", ENOENT is "the preopen matched and the
// file is not at the resolved path". So the guest's working directory is not the grant, and a relative read
// is a guest-side question rather than a mapping one. This arm exists because assuming the cwd was `/` would
// have predicted a PASS, and it does not pass.
//
// # What it asserts versus what it proposes
//
// The two dead forms are asserted as they behave, not as they arguably should. Narrowing what `--dir` accepts
// is CLI surface, so the refusal is a proposal rather than a change made here — and these arms are what make
// it checkable. **If the parser is later narrowed, these arms fail, which is the point**: the test is named
// for the mapping rule, so a repair rewrites an expectation rather than orphaning a test named after a defect.
func TestDirFlagMapsOnlyAnAbsoluteGuestPathAndTakesOneColon(t *testing.T) {
	guest := buildGuestFile(t, "cat")
	dir := t.TempDir()
	const body = "dir-flag-probe-body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// The guest's own bytes are the verdict, not the CLI's exit code: a grant of a path nothing can open
	// leaves the flag parser perfectly happy, which is the whole defect being measured.
	readable := func(t *testing.T, dirArg, guestPath string) (bool, int, string) {
		t.Helper()
		var out, errBuf bytes.Buffer
		code := dispatch(&out, &errBuf, []string{"run", "--dir", dirArg, guest, "--", guestPath})
		return strings.Contains(out.String(), body), code, errBuf.String()
	}

	for _, tc := range []struct {
		name         string
		dirArg       string // ":" + this is appended to the host dir, or "" for a bare host grant
		read         string // "" means the host's own absolute path
		wantReadable bool
		wantErrno    string // required when wantReadable is false, because "it failed" is not a finding
		why          string
	}{
		{
			name: "bare_host_maps_under_its_own_name", wantReadable: true,
			why: "HOST alone means HOST:HOST. This is the form that DID work during the sweep, which is why " +
				"#822's archive/zip finding is not a --dir artifact: its run read testdata at n=154 err=<nil>.",
		},
		{
			name: "absolute_guest_path_is_the_documented_form", dirArg: "/d", read: "/d/in.txt",
			wantReadable: true,
			why: "ADR 0083's grant, and the form the flag help names. If this breaks, " +
				"TestRunGrantsAndDeniesFilesystemAccess breaks with it.",
		},
		{
			name: "relative_guest_path_is_accepted_and_maps_nowhere", dirArg: ".", read: "in.txt",
			wantErrno: "Bad file number",
			why: "THE FORM THAT FAILED TWICE. EBADF: no preopen name matches, so the grant is dead on " +
				"arrival while the flag parser reports nothing.",
		},
		{
			name: "relative_guest_path_dead_for_a_dotted_read_too", dirArg: ".", read: "./in.txt",
			wantErrno: "Bad file number",
			why:       "Writing the read as ./in.txt does not rescue it; the grant is what is unreachable.",
		},
		{
			name: "wasmtime_two_colon_form_is_accepted_and_maps_nowhere", dirArg: ":/d", read: "/d/in.txt",
			wantErrno: "Bad file number",
			why: "A different tool's grammar. The guest path becomes \":/d\", granted successfully — the " +
				"trap I wrongly blamed for the two failures above.",
		},
		{
			name: "relative_read_under_an_absolute_grant_is_a_cwd_question", dirArg: "/", read: "in.txt",
			wantErrno: "No such file or directory",
			why: "ENOENT, not EBADF, and the same file IS readable at /in.txt under this grant — so the " +
				"preopen matched and the guest's cwd is not the grant. A DIFFERENT mechanism from the two above.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arg := dir
			if tc.dirArg != "" {
				arg = dir + ":" + tc.dirArg
			}
			read := tc.read
			if read == "" {
				read = filepath.Join(dir, "in.txt")
			}
			ok, code, stderr := readable(t, arg, read)
			t.Logf("DIRFLAG dir=%q read=%q readable=%v exit=%d stderr=%q", arg, read, ok, code, stderr)

			if tc.wantReadable {
				if !ok {
					t.Errorf("--dir %q did not make %q readable (exit %d): stderr %q\n\t%s",
						arg, read, code, stderr, tc.why)
				}
				return
			}
			if ok {
				t.Fatalf("--dir %q DID make %q readable: this form is recorded as dead, so either the "+
					"mapping was widened or this expectation was wrong.\n\t%s", arg, read, tc.why)
			}
			if !strings.Contains(stderr, tc.wantErrno) {
				// The errno, not the failure, is what separates the three dead forms from each other.
				t.Errorf("--dir %q failed with stderr %q, want it to name %q\n\t%s\n"+
					"\tA dead form failing for a NEW reason is a different defect from the one recorded here.",
					arg, stderr, tc.wantErrno, tc.why)
			}
		})
	}
}
