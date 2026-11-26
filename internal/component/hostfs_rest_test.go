// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"math"
	"strings"
	"testing"
	"time"
)

// fsRestRows is fs-rest.wasm's rows, in fs-rest.reading's row-line order. A row is marked posix where its reading
// rests on a hard link, a stat record's times or link count, nanosecond file times, or metadata-hash's identity, none
// of which Windows gives as darwin and linux do (ADR 0138's named limits).
var fsRestRows = []fsFixtureRow{
	{name: "readlink", links: true},
	{name: "readlink-kinds", links: true},
	{name: "readlink-errors", links: true},
	{name: "symlink", links: true},
	{name: "symlink-errors", links: true},
	{name: "symlink-escape-target", links: true},
	{name: "link", posix: true},
	{name: "link-errors", links: true},
	{name: "link-escapes", links: true},
	{name: "link-two", posix: true},
	{name: "link-cross-grant"},
	{name: "link-symlink", links: true},
	{name: "set-size"},
	{name: "set-size-errors"},
	{name: "set-times", posix: true},
	{name: "set-times-partial", posix: true},
	{name: "set-times-kinds", posix: true},
	{name: "set-times-at", links: true},
	{name: "set-times-at-errors", links: true},
	{name: "timestamps", posix: true},
	{name: "sync"},
	{name: "sync-more"},
	{name: "advise"},
	{name: "advise-more"},
	{name: "mhash-same"},
	{name: "mhash-links", links: true},
	{name: "mhash-change", posix: true},
	{name: "mhash-errors"},
	{name: "same-object"},
	{name: "same-object-more", links: true},
	{name: "append"},
	{name: "append-content"},
	{name: "append-errors"},
	{name: "base-file"},
	{name: "child-read"},
	{name: "read-only", readOnly: true},
	{name: "read-only-reads", readOnly: true, links: true},
	{name: "sync-lower", links: true},
	{name: "advise-bad", trap: true},
	{name: "set-times-at-link", links: true},
	{name: "read-only-file", readOnly: true},
}

// fsRestAfter is each fs-rest row's own after-check: what the row must have written, or must not have.
var fsRestAfter = map[string]map[string]string{
	"symlink":               {"tree/newlink": "<link>"},
	"symlink-errors":        {"tree/x": "<absent>", "tree/in-link": "<link>"},
	"symlink-escape-target": {"tree/esc": "<link>"},
	"link":                  {"tree/hard": "hello world"},
	"link-errors":           {"tree/x": "<absent>", "tree/subhard": "<absent>", "tree/w.txt": "0123456789"},
	"link-escapes":          {"tree/stolen": "<absent>"},
	"link-two":              {"tree/sub/hard": "hello world", "tree/inner-hard": "inner"},
	"link-cross-grant":      {"tree2/hard": "<absent>", "tree/x": "<absent>"},
	"link-symlink":          {"tree/lhard": "<link>"},
	"set-size":              {"tree/w.txt": "0123" + strings.Repeat("\x00", 16)},
	"set-size-errors":       {"tree/hello.txt": "hello world", "tree/w.txt": "0123456789"},
	"mhash-links":           {"tree/hard": "hello world"},
	"mhash-change":          {"tree/w.txt": "01"},
	"append":                {"tree/w.txt": "0123456789ABCDE"},
	"append-content":        {"tree/w.txt": "0123456789ABCDE"},
	"append-errors":         {"tree/hello.txt": "hello world"},
	"base-file":             {"tree/l": "<absent>", "tree/x": "<absent>"},
	"child-read":            {"tree/sub/l": "<absent>", "tree/sub/inner-hard": "<absent>"},
	"read-only":             {"tree/l": "<absent>", "tree/hard": "<absent>"},
	"read-only-file":        {"tree/hello.txt": "hello world"},
}

// TestFSRestMatchesWasmtime is E1d at the ABI (ADR 0138): every row of fs-rest.wasm against fs-rest.reading, as
// TestFSMutationsMatchWasmtime runs fs-mutate's, each on fresh trees with gate:async on and gate:cm-threads off and on.
// The reading is wasmtime 49.0.3's (10 of 10 per row) but for the read-only-grant rows, the WIT's, and the rows where
// Burroughs departs by ADR 0138, whose wasmtime readings the file records beside them. The advise-bad row traps, as
// wasmtime's does. After each row the host's trees are read, outside/'s times included, and every file goroutine is
// gone after Close.
func TestFSRestMatchesWasmtime(t *testing.T) {
	testFSFixture(t, "fs-rest", fsRestRows, false, fsRestAfter)
}

// TestAnInstantIsNormalisedAndClamped is ADR 0138's instant rule at fsInstantTime: nanoseconds of 10^9 or more carry
// into the seconds, and the result is clamped to Go's int64-nanosecond range, so no instant becomes the zero time,
// which set-times reads as "unchanged", and no carry overflows the seconds.
func TestAnInstantIsNormalisedAndClamped(t *testing.T) {
	for _, c := range []struct {
		sec  int64
		ns   uint32
		want time.Time
	}{
		{1500000000, 987654321, time.Unix(1500000000, 987654321)},
		{1500000000, 1000000000, time.Unix(1500000001, 0)},
		{1500000000, math.MaxUint32, time.Unix(1500000004, 294967295)},
		{-1, 0, time.Unix(-1, 0)},
		{-1, 999999999, time.Unix(-1, 999999999)},
		{1 << 62, 0, fsMaxTime},
		{math.MaxInt64, math.MaxUint32, fsMaxTime},
		{math.MinInt64, 0, fsMinTime},
		{-62135596800, 0, fsMinTime}, // the zero time's own instant, 0001-01-01
		{9223372036, 854775807, fsMaxTime},
		{9223372036, 854775808, fsMaxTime},
	} {
		got := fsInstantTime(c.sec, c.ns)
		if !got.Equal(c.want) {
			t.Errorf("fsInstantTime(%d, %d) = %v, want %v", c.sec, c.ns, got, c.want)
		}
		if got.IsZero() {
			t.Errorf("fsInstantTime(%d, %d) is the zero time, which set-times reads as unchanged", c.sec, c.ns)
		}
	}
}
