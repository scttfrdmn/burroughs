// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// declineLane is one Features value the decline control asks about, and the module its arms resolve
// their index-0 immediates against.
type declineLane struct {
	name     string
	features binary.Features
	// wat declares one of everything an instruction's immediate can index, so an arm reached with
	// zero immediates resolves its index and goes on to its typing rule. Without them an arm with a
	// rule would answer "unknown memory" before ever reaching a switch whose fallthrough is the
	// question, and the control would read that as a rule it never got to.
	wat string
	// floor is the fewest admitted opcodes the lane can have and still be the lane its name says.
	floor int
}

// declineLanes is the control's population of lanes. Today it is the default lane alone; #1134's
// class D extends it to each gate whose flip the control conditions (the chair's ruling of
// 2026-10-31, item 2).
func declineLanes() []declineLane {
	return []declineLane{{
		name:     "default",
		features: binary.DefaultFeatures(),
		wat: `(module
			(type (func))
			(memory 1 1 shared)
			(table 1 funcref)
			(global (mut i32) (i32.const 0))
			(elem declare func 0)
			(data "")
			(func))`,
		floor: 400,
	}}
}

// admittedDeclines asks the real dispatch about every opcode a lane's Features admits, and returns
// the ones that decline (validate.ErrUnsupported, which only the validator's seven fallthrough arms
// return), with how many were asked and how many panicked.
//
// The population is `binary.Features.AdmittedOpcodes`, the decoder's own tables filtered by its own
// gate check, so "admitted" is the decoder's answer rather than a list kept here. Each opcode is
// driven with zero immediates into a function body's frame over the lane's module. A rule that
// refuses it (an empty operand stack, a type mismatch) is a rule reached, which is all this asks.
func admittedDeclines(t *testing.T, lane declineLane) (declined []string, asked, panicked int) {
	t.Helper()
	mod := decodedModule(t, lane.wat, func(f *binary.Features) { *f = lane.features })
	for _, op := range lane.features.AdmittedOpcodes() {
		asked++
		err := func() (err error) {
			// A panic means the dispatch chose an arm and the arm indexed something this synthetic
			// state lacks. That is an arm, not a decline, and it is counted so a rise is visible.
			defer func() {
				if r := recover(); r != nil {
					panicked++
					err = fmt.Errorf("panicked: %v", r)
				}
			}()
			v := &validator{mod: mod, curFunc: &binary.Func{}, blocks: map[int]Arity{}}
			v.pushFrame(opFuncBody, nil, nil)
			return v.instr(0, binary.Instr{Prefix: op.Prefix, Op: op.Op})
		}()
		if errors.Is(err, ErrUnsupported) {
			declined = append(declined, fmt.Sprintf("%#02x %#02x %s", op.Prefix, op.Op, op.Label))
		}
	}
	return declined, asked, panicked
}

// TestNoAdmittedOpcodeReachesADeclineArm is #1134's class A proof (A1, the chair's ruling of
// 2026-10-31, item 1): **no opcode the decoder admits under a lane's Features reaches any of the
// validator's seven fallthrough arms.**
//
// That is what makes `interp`'s module-validity checks unreachable from a validated module. ADR
// 0029 lets a module the validator declines run unless `Strict`, so a decline arm reachable by an
// admitted opcode would be a path from the public API into those checks. With no such arm, a
// validated module reaching one is the engine's bug, which is why class A's sites are filed under
// `interp.ErrEngineInvariant`.
//
// The board's decline ceiling (`validateDeclineCeiling`, 0) says the same of the corpus. This says
// it of the grammar, which is the claim ADR 0043's retirement condition needs.
//
// **Neuter, run:** see the PR that landed this. With one rule arm deleted so that its opcode falls
// through, this test names that opcode and fails.
func TestNoAdmittedOpcodeReachesADeclineArm(t *testing.T) {
	for _, lane := range declineLanes() {
		t.Run(lane.name, func(t *testing.T) {
			declined, asked, panicked := admittedDeclines(t, lane)
			if asked < lane.floor {
				t.Fatalf("the %s lane admits %d opcodes, floor %d: an empty population passes this "+
					"test while asking nothing", lane.name, asked, lane.floor)
			}
			t.Logf("%s lane: %d admitted opcodes asked, %d declined, %d panicked in an arm",
				lane.name, asked, len(declined), panicked)
			if len(declined) != 0 {
				t.Errorf("%d opcode(s) the %s lane admits reach a validator decline arm:\n  %s\n"+
					"A decline runs anyway unless Strict (ADR 0029), so each of these is a public path "+
					"into interp's module-validity checks with an unvalidated instruction",
					len(declined), lane.name, strings.Join(declined, "\n  "))
			}
		})
	}
}
