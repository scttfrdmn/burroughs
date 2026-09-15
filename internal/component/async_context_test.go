// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

type contextOpsFixture struct {
	Slots        int `json:"slots"`
	Get0Unset    int `json:"get0_unset"`
	SetValue     int `json:"set_value"`
	Get0AfterSet int `json:"get0_after_set"`
	Get1Unset    int `json:"get1_unset"`
}

func loadContextOps(t *testing.T) contextOpsFixture {
	t.Helper()
	var doc struct {
		ContextOps contextOpsFixture `json:"context_ops"`
	}
	loadFixtures(t, &doc)
	return doc.ContextOps
}

// TestSynthContextGetSetMatchesTheOracle drives context.get/set end-to-end through a real component and a
// real Invoke (a real per-agent stack), matched to the committed context_ops pin: an unset get returns 0
// (the guest's opening-move path), and get-after-set returns the value. The built-in + decode + binding +
// the per-agent stack accessor are all exercised; the per-caller isolation is the interp test's job.
func TestSynthContextGetSetMatchesTheOracle(t *testing.T) {
	fx := loadContextOps(t)
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/context-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "set-then-get")
	if ci == nil {
		t.Fatal("no core instance exports set-then-get")
	}
	// set(slot 0, 107) then get(slot 0) -> 107 (roundtrip on the agent's own stack).
	res, err := ci.Invoke("set-then-get", interp.I32(int32(fx.SetValue)))
	if err != nil {
		t.Fatalf("Invoke(set-then-get): %v", err)
	}
	if len(res) != 1 || res[0].Int32() != int32(fx.Get0AfterSet) {
		t.Errorf("set-then-get = %v, want [%d] (get after set)", res, fx.Get0AfterSet)
	}
	// A fresh Invoke reads slot 1 unset -> 0 (also a fresh stack, so slot 0 would be 0 too).
	res, err = ci.Invoke("get1-unset")
	if err != nil {
		t.Fatalf("Invoke(get1-unset): %v", err)
	}
	if len(res) != 1 || res[0].Int32() != int32(fx.Get1Unset) {
		t.Errorf("get1-unset = %v, want [%d] (unset slot returns 0)", res, fx.Get1Unset)
	}
}
