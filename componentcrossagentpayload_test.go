// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestComponentAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote is contract §6 R-3 across agents, as R-4's append
// stamped on #1334 states it, at the public boundary (#1339): `cross-agent-payload.wasm` starts two agents through the
// component spawn door. B fills memory with plain stores and completes a stream or future write; A, which armed the
// read, takes the event by `waitable-set.wait` or `waitable-set.poll` and checks every byte and word B wrote. The
// edge that orders them is mapped at `internal/component`'s `TestAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote`,
// which runs the rows over many more instances. This twin asks the same of a `Component` an embedder loads, reading
// each agent's first fault from the place it recorded it, through the fixture's `word` export.
func TestComponentAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote(t *testing.T) {
	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "1")
	wasm, err := os.ReadFile("internal/component/testdata/cross-agent-payload.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	const runs = 2
	for _, row := range []string{"stream", "future", "future-poll"} {
		t.Run(row, func(t *testing.T) {
			for i := range runs {
				comp, err := LoadComponent(wasm)
				if err != nil {
					t.Fatalf("LoadComponent: %v", err)
				}
				call := func(name string, args ...ComponentValue) uint32 {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
					defer cancel()
					res, err := comp.Call(ctx, name, args...)
					v, ok := firstU32(res)
					if err != nil || !ok {
						place := enginePlaces()
						_ = comp.Close()
						t.Fatalf("run %d: %s: %v, %v; want one u32. %s", i, name, res, err, place)
					}
					return v
				}
				iters := call("iters")
				if joined := call(row); joined != 1 {
					place := enginePlaces()
					_ = comp.Close()
					t.Fatalf("run %d: %s: an agent did not finish within the fixture's 60s join. %s", i, row, place)
				}
				word := func(off uint32) uint32 { return call("word", ComponentU32(off)) }
				// The fixture's places: 24 and 28 each agent's last completed iteration, 64 A's fault count and its
				// first fault (iteration, kind, index, got, want), 96 B's (iteration, kind, got, want).
				if n := word(64); n != 0 {
					t.Errorf("run %d: %s: A recorded %d fault(s); the first at iteration %d, kind %d (the fixture's "+
						"header names the kinds), index %d, is %#x, want %#x", i, row, n, word(68), word(72), word(76),
						word(80), word(84))
				}
				if n := word(96); n != 0 {
					t.Errorf("run %d: %s: B recorded %d fault(s); the first at iteration %d, kind %d, is %#x, want %#x",
						i, row, n, word(100), word(104), word(108), word(112))
				}
				if a, b := word(24), word(28); a != iters || b != iters {
					t.Errorf("run %d: %s: A completed %d iterations and B %d, want %d each", i, row, a, b, iters)
				}
				if err := comp.Close(); err != nil {
					t.Errorf("run %d: Close: %v", i, err)
				}
				if t.Failed() {
					return
				}
			}
		})
	}
}
