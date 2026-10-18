package component

import (
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TEMPORARY (the chair's ruling of 2026-10-17, item 1): the merged fence soaked on the platforms where #1020
// showed. Reverted by the next commit of the same PR. Each subtest is one reading against the reference's
// value; the CI step runs it -count=200 under -race and prints the per-subtest tally.

func soakHost(r *crossCallReports) *Host {
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			go func() { time.Sleep(time.Millisecond); onResolve(canon.U32(49)) }() // later: after the impl returned
			return func() {}, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil },
		"report": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			if len(args) >= 2 {
				r.report(uint32(args[0].Bits), uint32(args[1].Bits))
			}
			return nil, nil
		},
	}
	return h
}

func soakComposed(t *testing.T, path string) string {
	t.Helper()
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &crossCallReports{}
	in, err := InstantiateWithHost(b, soakHost(r))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	ch := make(chan error, 1)
	go func() { _, e := in.CallValues("go"); ch <- e }()
	select {
	case e := <-ch:
		if e != nil {
			return "err: " + e.Error()
		}
		reports, _ := r.snapshot()
		return fmt.Sprint(reports)
	case <-time.After(optionABound):
		return "HANG"
	}
}

func TestZZSoakFence(t *testing.T) {
	t.Run("cancel_suspended_natural", func(t *testing.T) {
		_, res, err := callBounded(t, "cancel-suspended", "run")
		wantU32(t, "cancel-suspended", res, err, 4)
	})
	t.Run("a_widened_host", func(t *testing.T) {
		soakWidenHost = 2 * time.Millisecond
		defer func() { soakWidenHost = 0 }()
		if got := soakComposed(t, "testdata/asynclift/call-wat-parent/composed.wasm"); got != "[[1 1] [3 49]]" {
			t.Errorf("(a) widened: %s, want [[1 1] [3 49]] (STARTED, then the post-park value)", got)
		}
	})
	t.Run("b_widened_cancel_wat", func(t *testing.T) {
		soakWidenCancel = 2 * time.Millisecond
		defer func() { soakWidenCancel = 0 }()
		if got := soakComposed(t, "testdata/asynclift/cancel-wat-parent/composed.wasm"); got != "[[1 1] [2 4]]" {
			t.Errorf("(b) cancel-wat widened: %s, want [[1 1] [2 4]] (20)", got)
		}
	})
	t.Run("b_widened_cancel_suspended", func(t *testing.T) {
		soakWidenCancel = 2 * time.Millisecond
		defer func() { soakWidenCancel = 0 }()
		_, res, err := callBounded(t, "cancel-suspended", "run")
		wantU32(t, "cancel-suspended widened", res, err, 4)
	})
	for _, r := range []struct{ name, path, export, gate string }{
		{"reenter", "testdata/reenter.wasm", "run", ""},
		{"thread_wait_callback", "testdata/cmthreads/thread-wait-callback.wasm", "run", "1"},
		{"residual", "testdata/cmthreads/residual.wasm", "run", "1"},
		{"reenter_after_suspend_off", "testdata/reenter-after-suspend.wasm", "go", ""},
		{"reenter_after_suspend_on", "testdata/reenter-after-suspend.wasm", "go", "1"},
	} {
		t.Run(r.name, func(t *testing.T) {
			got := readGo(t, r.path, r.export, r.gate, 1)
			if got[8] != 1 {
				t.Errorf("%s: %v, want 8", r.name, got)
			}
		})
	}
}
