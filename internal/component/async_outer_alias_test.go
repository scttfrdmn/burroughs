// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAnOuterAliasedFuncTypeLowersItsResult is #756's witness. An imported instance whose exported func's
// TYPE is an outer type alias must lower that func's result exactly as the inline-typed form does.
//
// **The oracle** is `outer-alias-oracle.reading`. wasmtime 49.0.3 reads the composed aliased and inline
// forms alike, 242, so the alias is only a spelling of the type. **The subject is the host-impl path**,
// because it is the only one #756 reaches: a cross-component lower takes its signature from the callee's own
// lift, and Burroughs reads 242 on the composed pair before and after this fix. A host impl's lower reads
// the import's instance type through `lowerSignature`, and there the alias used to give a nil signature, so
// `hasResult` was false and the result was never stored.
//
// The impl resolves inline with 42, so `run()` reads status*100 + the stored result: 242.
//
// **Neuter, run, which is the pre-fix code:** `instanceAlias` appending the placeholder instead of the
// resolved type. The aliased arm reads 200, status RETURNED with nothing stored, and fails. The inline arm
// reads 242 and passes. `TestAnOuterAliasedTypeResolvesToItsInlineSibling` fails under it too.
func TestAnOuterAliasedFuncTypeLowersItsResult(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	for _, fx := range []string{"outer-alias-lower", "outer-alias-lower-inline"} {
		t.Run(fx, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + fx + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			h := NewHost(io.Discard, io.Discard, nil)
			var gotX int32
			h.asyncImpls = map[string]asyncLowerImpl{
				"test:alias/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value,
					onResolve func(canon.Value),
				) (func(), error) {
					if p := onStart(); len(p) > 0 {
						gotX = p[0].Int32()
					}
					onResolve(canon.U32(42))
					return func() {}, nil
				},
			}
			in, err := InstantiateWithHost(b, h)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			res, err := in.CallValues("run")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if gotX != 7 {
				t.Errorf("the impl got x=%d, want 7: the lower did not pass the lifted param", gotX)
			}
			if v, ok := res[0].U32(); len(res) != 1 || !ok || v != 242 {
				t.Errorf("run() = %v, want u32 242 (RETURNED with 42 stored at the retptr). 200 is #756: the "+
					"status is right and the result was never lowered", res)
			}
		})
	}
}

// TestAnOuterAliasedTypeResolvesToItsInlineSibling replaces #753's refuse-by-name witness, whose premise
// #756 removed. `future-read-outer-alias.wasm` declares `get-future`'s `future<u32>` through an outer type
// alias, and `future-read-synth.wasm` declares it inline. Before #756 the alias decoded to
// `VUnresolvedAlias` and the lower refused by name at instantiate. Now it must decode to the same
// signature as its inline sibling.
//
// **What #753 still guards** is the crash: `Load` used to stack-overflow `resolveVal` on a self-referential
// placeholder. Both fixtures still `Load`, and an alias naming no resolvable type still decodes to
// `VUnresolvedAlias` (`instanceAlias`), which resolveVal treats as a leaf.
func TestAnOuterAliasedTypeResolvesToItsInlineSibling(t *testing.T) {
	sig := func(t *testing.T, fx string) *FuncType {
		t.Helper()
		b, err := os.ReadFile("testdata/" + fx)
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		c, err := Load(b)
		if err != nil {
			t.Fatalf("Load(%s): %v", fx, err)
		}
		for _, td := range c.Types {
			if td.Kind != TDInstance || td.Inst == nil {
				continue
			}
			for _, e := range td.Inst.Exports {
				if e.Name == "get-future" {
					return e.Func
				}
			}
		}
		t.Fatalf("%s declares no instance type exporting get-future", fx)
		return nil
	}
	aliased, inline := sig(t, "future-read-outer-alias.wasm"), sig(t, "future-read-synth.wasm")
	if aliased == nil || sigHasUnresolvedAlias(aliased) {
		t.Fatalf("the aliased get-future decoded to %+v, want a resolved signature", aliased)
	}
	if !reflect.DeepEqual(aliased, inline) {
		t.Errorf("the aliased get-future decoded to %+v, its inline sibling to %+v: an outer alias must "+
			"be only a spelling of the type", aliased, inline)
	}
}
