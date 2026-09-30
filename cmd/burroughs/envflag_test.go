// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestEnvIsEmptyUnlessNamed is #830's witness: `burroughs run` passes **nothing** into the guest's
// environment unless a `--env` names it.
//
// # Why this needs a guest that can speak
//
// #830 was found two removes from its cause. `burroughs run` forwarded `os.Environ()`, nothing
// noticed, and the symptom that eventually surfaced was a *filesystem* one — Go's `wasip1` runtime
// takes its working directory from `PWD`, so the leaked variable moved where a relative path
// resolved. The reason it went unnoticed for so long is that **no guest could report its own
// environment**, so every observation was indirect. `testdata/envprint` exists to end that: it prints
// one `NAME=VALUE` per line plus an `ENVCOUNT`, so a test can assert both what crossed and that
// nothing else did.
//
// The `ENVCOUNT` line matters more than it looks. An empty environment and a guest that never started
// produce the same *absence* of `ENV` lines; the count makes "nothing crossed" an observation rather
// than a missing one, which is the difference between a measurement and a vacuum.
func TestEnvIsEmptyUnlessNamed(t *testing.T) {
	guest := buildGuestFile(t, "envprint")

	// A variable that is certainly in this process's environment — set here rather than assumed, so
	// the pass-through arms are not resting on the runner's ambient state.
	t.Setenv("BURROUGHS_ENV_WITNESS", "host-value")
	t.Setenv("BURROUGHS_ENV_SECRET", "must-not-cross")

	run := func(t *testing.T, args ...string) (env map[string]string, count, code int, stderr string) {
		t.Helper()
		var out, errBuf bytes.Buffer
		code = dispatch(&out, &errBuf, append(append([]string{"run"}, args...), guest))
		env = map[string]string{}
		count = -1
		for _, ln := range strings.Split(out.String(), "\n") {
			switch {
			case strings.HasPrefix(ln, "ENV "):
				k, v, _ := strings.Cut(strings.TrimPrefix(ln, "ENV "), "=")
				env[k] = v
			case strings.HasPrefix(ln, "ENVCOUNT "):
				// Parsed rather than counted from the map, so a duplicated name cannot hide.
				n := 0
				if _, err := fmt.Sscanf(strings.TrimPrefix(ln, "ENVCOUNT "), "%d", &n); err == nil {
					count = n
				}
			}
		}
		return env, count, code, errBuf.String()
	}

	t.Run("nothing_crosses_without_env", func(t *testing.T) {
		env, count, code, stderr := run(t)
		if code != 0 {
			t.Fatalf("the guest exited %d: %s", code, stderr)
		}
		if count != 0 {
			t.Errorf("the guest saw ENVCOUNT %d, want 0: %v\n"+
				"\tThis is #830. The CLI used to pass os.Environ() — every variable this process "+
				"holds, including credentials — into a sandbox whose model is that nothing is "+
				"visible unless named.", count, env)
		}
		if _, leaked := env["BURROUGHS_ENV_SECRET"]; leaked {
			t.Errorf("BURROUGHS_ENV_SECRET reached the guest without being named")
		}
	})

	t.Run("a_named_variable_passes_the_hosts_value_through", func(t *testing.T) {
		env, count, code, stderr := run(t, "--env", "BURROUGHS_ENV_WITNESS")
		if code != 0 {
			t.Fatalf("the guest exited %d: %s", code, stderr)
		}
		if got := env["BURROUGHS_ENV_WITNESS"]; got != "host-value" {
			t.Errorf("BURROUGHS_ENV_WITNESS = %q, want the host's %q", got, "host-value")
		}
		// The grant is exactly one variable, not "the host environment, filtered".
		if count != 1 {
			t.Errorf("ENVCOUNT %d with one --env, want 1: %v — a grant must pass what it names and "+
				"nothing beside it", count, env)
		}
		if _, leaked := env["BURROUGHS_ENV_SECRET"]; leaked {
			t.Errorf("naming one variable let BURROUGHS_ENV_SECRET across too")
		}
	})

	t.Run("an_explicit_value_ignores_the_host", func(t *testing.T) {
		env, count, code, stderr := run(t, "--env", "BURROUGHS_ENV_WITNESS=literal")
		if code != 0 {
			t.Fatalf("the guest exited %d: %s", code, stderr)
		}
		if got := env["BURROUGHS_ENV_WITNESS"]; got != "literal" {
			t.Errorf("BURROUGHS_ENV_WITNESS = %q, want %q: NAME=VALUE is literal and the host's "+
				"value for the same name must not win", got, "literal")
		}
		if count != 1 {
			t.Errorf("ENVCOUNT %d, want 1: %v", count, env)
		}
	})

	t.Run("an_empty_explicit_value_is_accepted", func(t *testing.T) {
		env, count, code, _ := run(t, "--env", "BURROUGHS_ENV_WITNESS=")
		if code != 0 || count != 1 {
			t.Errorf("--env NAME= exited %d with ENVCOUNT %d, want 0 and 1: an explicit empty value "+
				"says what it means and is a different request from a bare NAME", code, count)
		}
		if got, ok := env["BURROUGHS_ENV_WITNESS"]; !ok || got != "" {
			t.Errorf("BURROUGHS_ENV_WITNESS = %q present=%v, want an empty value present", got, ok)
		}
	})

	t.Run("a_bare_name_that_is_unset_is_refused_by_name", func(t *testing.T) {
		_, _, code, stderr := run(t, "--env", "BURROUGHS_ENV_ABSENT")
		if code == 0 {
			t.Fatal("--env with an unset variable exited 0; the operator asked for something and " +
				"would have received nothing, which is #828's silent-dead-input shape")
		}
		for _, want := range []string{"BURROUGHS_ENV_ABSENT", "is not set", "=VALUE"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr %q does not mention %q — the refusal must name the variable and the "+
					"way to fix it, or it is no better than passing nothing silently", stderr, want)
			}
		}
	})

	t.Run("multiple_env_flags_accumulate", func(t *testing.T) {
		env, count, code, _ := run(t, "--env", "A=1", "--env", "B=2")
		if code != 0 || count != 2 {
			t.Errorf("two --env flags gave exit %d ENVCOUNT %d, want 0 and 2: %v", code, count, env)
		}
		if env["A"] != "1" || env["B"] != "2" {
			t.Errorf("accumulated environment = %v, want A=1 and B=2", env)
		}
	})
}
