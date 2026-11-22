// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// unwakeableLibPeriod is the CLI witness's period, for the same reason (cmd/burroughs's unwakeablePeriod): the
// children run at GOMAXPROCS=1, where the defect's fatal fires as soon as the wait is reached.
const unwakeableLibPeriod = 2 * time.Second

// unwakeableLibRun is one of #1303's guests driven through a library entry point.
type unwakeableLibRun struct {
	name, wasm, reading string
	// frame is a substring of the frame the dump must show, the function's bare name, because a closure's printed
	// name differs by toolchain (cmd/burroughs/unwakeable_test.go's unwakeableRun.frame has the two readings).
	frame  string
	export string // "" runs the component through ComponentConfig.Run; else Instance.Call
}

var unwakeableLibRuns = []unwakeableLibRun{
	{
		"unwakeable-wait", "internal/component/testdata/unwakeable-wait.wasm",
		"internal/component/testdata/unwakeable-wait.reading", "waitableSetWait", "",
	},
	{
		"unwakeable-wait-woken", "internal/component/testdata/unwakeable-wait-woken.wasm",
		"internal/component/testdata/unwakeable-wait.reading", "", "",
	},
	{
		"forever", "internal/interp/testdata/wait-forever.wasm",
		"internal/interp/testdata/wait-forever.reading", "interp.(*memory).wait", "forever",
	},
	{
		"forever64", "internal/interp/testdata/wait-forever.wasm",
		"internal/interp/testdata/wait-forever.reading", "interp.(*memory).wait", "forever64",
	},
	{
		"brief", "internal/interp/testdata/wait-forever.wasm",
		"internal/interp/testdata/wait-forever.reading", "", "brief",
	},
}

// TestAnUnwakeableWaitHangsThroughTheLibrary is #1303's witness at the library (ADR 0128): the CLI witness's
// guests through `ComponentConfig.Run` and `Instance.Call`, the entry points an embedder calls. Burroughs at d8076a1
// ended an embedder's process with Go's deadlock fatal through both. A library caller's wait now blocks the way a
// guest stuck in an infinite loop already blocks it, and the controls still return.
//
// Each arm runs in a child for the CLI witness's reason, and a hang is read from the child's goroutine dump.
//
// NEUTER: holdHostAwake returning a stopped timer: unwakeable-wait, forever and forever64 each fail with "the host
// ended 4ms after starting the guest, exit 2: fatal error: all goroutines are asleep - deadlock!" (3 to 5 ms, with
// and without -race). The controls pass.
func TestAnUnwakeableWaitHangsThroughTheLibrary(t *testing.T) {
	t.Parallel()
	for _, r := range unwakeableLibRuns {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			want := testenv.ReadingRows(t, r.reading)[r.name]
			if want == "" {
				t.Fatalf("%s has no row for %s", r.reading, r.name)
			}
			got := testenv.RunHangChild(t, "^TestAnUnwakeableWaitHangsThroughTheLibraryChild$", r.name,
				unwakeableLibPeriod)
			// The child prints a core result as `Value`'s String spells it.
			testenv.JudgeHang(t, r.name, want, r.frame, got, "result: i32:"+want)
		})
	}
}

// TestAnUnwakeableWaitHangsThroughTheLibraryChild is the child half: as the CLI witness's, it runs one arm when
// testenv.HangChildEnv is set and does nothing otherwise. A component's exit code becomes the process's, and a
// core call's result is printed.
func TestAnUnwakeableWaitHangsThroughTheLibraryChild(t *testing.T) {
	name := testenv.HangChildCase()
	if name == "" {
		return
	}
	for _, r := range unwakeableLibRuns {
		if r.name != name {
			continue
		}
		wasm, err := os.ReadFile(r.wasm)
		if err != nil {
			t.Fatal(err)
		}
		if r.export == "" {
			fmt.Println(testenv.HangMark)
			code, rerr := ComponentConfig{}.Run(wasm)
			if rerr != nil {
				fmt.Fprintln(os.Stderr, "Run:", rerr)
				os.Exit(1)
			}
			os.Exit(code)
		}
		in, err := Instantiate(wasm)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(testenv.HangMark)
		res, err := in.Call(r.export)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Call:", err)
			os.Exit(1)
		}
		for _, v := range res {
			fmt.Println("result:", v)
		}
		os.Exit(0)
	}
	t.Fatalf("no arm named %q", name)
}
