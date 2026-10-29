// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/gen/xcorpus"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// encoderComparableFloor is the comparable population, measured on #467 at main with the corpus's own suite revision
// (de54fd27): 1954 corpus rows, less 92 that are not text modules and 45 that wabt's image needs a default-off gate
// for. A comparison whose left side quietly shrinks agrees with anything, so the floor is the measured figure, not a
// round number below it. It moves when the population does, and says why.
const encoderComparableFloor = 1817

// explicitTypeuseBlocktype is the one named divergence from wabt: a block, loop or if written with an explicit
// `(type $t)`.
//
// The reference parser (`interpreter/text/parser.mly`, `block_type`) makes an explicit typeuse a `VarBlockType x`,
// which `binary/encode.ml` emits as the type index, and only the sugared form without `(type …)` becomes the inline
// `ValBlockType`. So our encoder agrees with the reference, and wabt substitutes the inline spelling where the
// referenced type is `[] → []` or `[] → [t]`. Both decode to the same block types. Measured on #467: these three, and
// no others.
var explicitTypeuseBlocktype = map[string]bool{"block:3": true, "if:3": true, "loop:3": true}

// TestTheEncoderEmitsTheModuleTheTextDenotes is #67's half 2, re-charged as #467: for every must-succeed suite text
// module that is comparable in the default posture, `text.EncodeModule(source)` must equal the image an independent
// producer (wabt 1.0.41, `testdata/xcorpus`) made from the same source.
//
// Rows are joined to the harness's commands by file and line, not ordinal, because wabt's ordinal also counts
// `module binary` and `module quote`.
//
// A row in explicitTypeuseBlocktype must differ **only** at blocktype immediates, each one ours a type index whose
// function type is wabt's inline spelling. A difference anywhere else fails, and so does a class row that has become
// byte-identical (the class would then be stale).
//
// NEUTERS, run (2026-10-29, darwin/arm64):
//   - with `block:3` removed from the class, it fails as an unexplained difference;
//   - with our image of `block:3` corrupted by one bit at offset 100 (a temporary line in this test), the class check
//     rejects it: *"byte 100: type 7 has params or several results, so it has no inline spelling"*. So the class
//     admits blocktype spellings, not any difference in a listed row.
func TestTheEncoderEmitsTheModuleTheTextDenotes(t *testing.T) {
	testenv.RequireSuite(t, suiteDir)
	m, err := xcorpus.Load("../../testdata/xcorpus")
	if err != nil {
		t.Fatalf("the corpus is the comparator's other side, and it does not load: %v", err)
	}
	byFile := map[string]map[int]Command{}
	compared, identical := 0, 0
	seenClass := map[string]bool{}
	var unexplained []string
	for _, row := range m.Modules {
		cmds, ok := byFile[row.File]
		if !ok {
			s, perr := ParseFile(filepath.Join(suiteDir, row.File+".wast"))
			if perr != nil {
				t.Fatalf("%s: %v", row.File, perr)
			}
			cmds = map[int]Command{}
			for _, c := range s.Commands {
				cmds[c.Line] = c
			}
			byFile[row.File] = cmds
		}
		c, ok := cmds[row.Line]
		if !ok || c.Kind != KindModuleText {
			continue // module binary / module quote: not the encoder's output
		}
		wabt, derr := (&binary.Decoder{Features: binary.DefaultFeatures()}).DecodeModule(row.Image)
		if derr != nil {
			continue // needs a default-off gate (GC, EH): not comparable in this posture
		}
		ours, eerr := text.EncodeModule(c.Source)
		if eerr != nil {
			t.Errorf("%s:%d: the encoder refuses a module wabt encoded: %v", row.File, row.Line, eerr)
			continue
		}
		compared++
		key := fmt.Sprintf("%s:%d", row.File, row.Line)
		if bytes.Equal(ours, row.Image) {
			identical++
			if explicitTypeuseBlocktype[key] {
				t.Errorf("%s is in explicitTypeuseBlocktype but now byte-identical to wabt: remove the row", key)
			}
			continue
		}
		if !explicitTypeuseBlocktype[key] {
			unexplained = append(unexplained, key)
			continue
		}
		seenClass[key] = true
		if why := onlyBlocktypeSpellingsDiffer(ours, row.Image, wabt.Types); why != "" {
			t.Errorf("%s differs from wabt in more than its blocktype spelling: %s", key, why)
		}
	}
	sort.Strings(unexplained)
	for _, k := range unexplained {
		t.Errorf("%s: the encoder's bytes differ from wabt's for the same source, outside every named divergence. "+
			"Either the encoder emits a different module, or this is a new equivalent spelling that needs the "+
			"reference read and a class of its own", k)
	}
	for k := range explicitTypeuseBlocktype {
		if !seenClass[k] && !t.Failed() {
			t.Errorf("%s is in explicitTypeuseBlocktype but was never compared: its row moved or left the corpus", k)
		}
	}
	// Exact re-base (slack 0): a population that grows, as it does when a gate flips, is re-measured and said in the
	// PR that moves it, and one that shrinks is a comparison over fewer rows, which agrees with more.
	boardBound(t, "encoderComparableFloor", compared, encoderComparableFloor, 0, floorBound,
		"the encoder comparator's comparable population shrank, so it agrees with more")
	t.Logf("compared %d of %d corpus rows: %d byte-identical, %d in the named blocktype class",
		compared, len(m.Modules), identical, len(seenClass))
}

// onlyBlocktypeSpellingsDiffer reports, as "", that ours and wabt's are the same length and differ only at blocktype
// immediates: the byte before is block (0x02), loop (0x03) or if (0x04), ours is a type index, and wabt's is that
// function type spelled inline (0x40 for [] → [], or the single result's value type).
func onlyBlocktypeSpellingsDiffer(ours, wabt []byte, types []binary.CompType) string {
	if len(ours) != len(wabt) {
		return fmt.Sprintf("lengths %d and %d", len(ours), len(wabt))
	}
	for i := range ours {
		if ours[i] == wabt[i] {
			continue
		}
		if i == 0 || (ours[i-1] != 0x02 && ours[i-1] != 0x03 && ours[i-1] != 0x04) {
			return fmt.Sprintf("byte %d (% x vs % x) does not follow a block, loop or if opcode", i, ours[i], wabt[i])
		}
		idx := int(ours[i])
		if idx >= 0x40 || idx >= len(types) {
			return fmt.Sprintf("byte %d: ours %#02x is not a type index into %d types", i, ours[i], len(types))
		}
		ft := types[idx].Func
		if len(ft.Params) != 0 || len(ft.Results) > 1 {
			return fmt.Sprintf("byte %d: type %d has params or several results, so it has no inline spelling", i, idx)
		}
		want := byte(0x40)
		if len(ft.Results) == 1 {
			k, ok := ft.Results[0].Kind()
			if !ok {
				return fmt.Sprintf("byte %d: type %d's result has no one-byte spelling", i, idx)
			}
			want = k
		}
		if wabt[i] != want {
			return fmt.Sprintf("byte %d: wabt's %#02x is not type %d spelled inline (%#02x)", i, wabt[i], idx, want)
		}
	}
	return ""
}
