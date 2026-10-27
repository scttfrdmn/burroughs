// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestRunRoutesAComponentWhateverFlagsAreGiven is #1128's witness (v0.12.0; the chair's ruling of 2026-10-27, item
// 3). `burroughs run <component>` with guest args or `--features` reaches the component path and runs. It used to
// return the wasip1 probe's decode error, *"malformed module: unknown binary version: 65549"*, exit 3, for a
// well-formed component. A flag that does not apply to the module's kind is refused by name, as a usage error,
// where it used to be dropped silently.
//
// **Neuter, run:** with the component check moved back after the wasip1 command probe, both routing arms exit 3
// with the "malformed module" message.
func TestRunRoutesAComponentWhateverFlagsAreGiven(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "") // the default build: the gate is on
	want, err := os.ReadFile("../../internal/component/testdata/p3hello.stdout")
	if err != nil {
		t.Fatal(err)
	}
	run := func(argv ...string) (int, string, string) {
		var out, errBuf bytes.Buffer
		code := dispatch(&out, &errBuf, append([]string{"run"}, argv...))
		return code, out.String(), errBuf.String()
	}

	for _, c := range []struct {
		name string
		argv []string
	}{
		{"guest_args_after_--", []string{p3hello, "--", "a", "b"}},
		{"--features", []string{"--features=threads", p3hello}},
	} {
		t.Run("routes_"+c.name, func(t *testing.T) {
			code, out, errOut := run(c.argv...)
			if code != 0 {
				t.Fatalf("exit %d, want 0: the component was not routed to its path (stderr: %q)", code, errOut)
			}
			if out != string(want) {
				t.Errorf("stdout = %q, want %q", out, want)
			}
		})
	}

	for _, c := range []struct {
		name, want string
		argv       []string
	}{
		{"a_function_name", "takes no function name", []string{p3hello, "run"}},
		{"--dir", "takes no --dir or --scratch", []string{"--dir", os.TempDir(), p3hello}},
		{"--scratch", "takes no --dir or --scratch", []string{"--scratch", t.TempDir(), p3hello}},
		{"--env", "takes no --env", []string{"--env", "X=1", p3hello}},
	} {
		t.Run("refuses_"+c.name+"_by_name", func(t *testing.T) {
			code, out, errOut := run(c.argv...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d (usage): stderr %q", code, exitUsage, errOut)
			}
			if out != "" || !strings.Contains(errOut, c.want) {
				t.Errorf("stdout %q, stderr %q: want empty stdout and a refusal naming %q", out, errOut, c.want)
			}
		})
	}

	t.Run("a_core_module_refuses_--features_by_name", func(t *testing.T) {
		core := writeModule(t, "core.wasm", `(module (func (export "f")))`)
		code, _, errOut := run("--features=threads", core, "f")
		if code != exitUsage || !strings.Contains(errOut, "--features applies to a wasip1 command or a component") {
			t.Fatalf("exit %d, stderr %q: want a usage refusal naming --features", code, errOut)
		}
	})
}
