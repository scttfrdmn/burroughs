// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"strings"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestTheSpawnDoorIsRefusedAtLinkWhenItsShapeIsWrong pins the door's link-time refusals (#1122). A guest whose door
// has the wrong type, or whose `mstart` has the wrong type, or which has no `mstart` at all, is refused by name
// before it runs. Without the refusal it would fail at its first spawn, with nothing to say why. A module that does
// not import the door is untouched.
func TestTheSpawnDoorIsRefusedAtLinkWhenItsShapeIsWrong(t *testing.T) {
	decode := func(t *testing.T, src string) *bin.Module {
		t.Helper()
		w, err := text.EncodeModule([]byte(src))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		m, err := (&bin.Decoder{Features: GuestFeatures()}).DecodeModule(w)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m
	}
	for _, c := range []struct{ name, src, want string }{
		{
			"the_door_has_the_wrong_type",
			`(module (import "burroughs" "spawn" (func (param i64))) (func (export "mstart") (param i32)))`,
			"must have type (i32) -> () or (i32) -> (i32)",
		},
		{
			"mstart_has_the_wrong_type",
			`(module (import "burroughs" "spawn" (func (param i32))) (func (export "mstart")))`,
			`"mstart" export must have type (i32) -> ()`,
		},
		{
			"no_mstart_at_all",
			`(module (import "burroughs" "spawn" (func (param i32))) (func (export "_start")))`,
			`exports no function "mstart"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			door, err := spawnDoorFor(decode(t, c.src))
			if err == nil || door != nil {
				t.Fatalf("spawnDoorFor = (%v, %v), want a refusal containing %q", door, err, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused, but not by name: %v", err)
			}
		})
	}
	t.Run("a_module_without_the_door_is_untouched", func(t *testing.T) {
		door, err := spawnDoorFor(decode(t, `(module (func (export "_start")))`))
		if err != nil || door != nil {
			t.Fatalf("spawnDoorFor = (%v, %v), want (nil, nil)", door, err)
		}
	})
	t.Run("the_tid_returning_form_is_accepted", func(t *testing.T) {
		door, err := spawnDoorFor(decode(t,
			`(module (import "burroughs" "spawn" (func (param i32) (result i32))) (func (export "mstart") (param i32)))`))
		if err != nil || door == nil {
			t.Fatalf("spawnDoorFor = (%v, %v), want a door", door, err)
		}
	})
}
