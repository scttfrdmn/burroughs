// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"slices"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The host's import keying (#1229, ADR 0115). A lowered import names `interface@version::export`, and a
// host table entry names `interface@class::export`: the version-free name plus the version's **semver
// compatibility class**, the Component Model's own rule for which versions may satisfy an import. In `0.x`
// the minor version breaks compatibility, so `0.2.3` and `0.2.6` are one class and `0.3.0` another. An
// unversioned table key is a wildcard.
//
// This replaced `stripVersion`, which cut the version and kept nothing of it. WASI 0.2 and WASI 0.3 share
// names with different signatures, so a 0.3 guest bound 0.2's implementation of a name by the merge, and
// the 0.3 fixtures ran partly on entries nobody had said were 0.3's.

// classNone is the compatibility class of an unversioned key: a bare world-level import (`tick`), or a
// hand-built test key. Only a wildcard entry serves it.
const classNone = "none"

// classUnparseable prefixes the class of a version that is not SemVer 2.0.0. Such a class is its own, and
// nothing serves it, wildcards included: a version the engine cannot read is not one it can match.
const classUnparseable = "unparseable "

// splitVersion splits a lowered func's "interface@version::export" identity into the version-free name
// ("interface::export") and the version's compatibility class. The version sits on the interface, before
// "::"; the export name (which may itself contain '.', as in "[method]output-stream.write") is left
// untouched.
func splitVersion(key string) (name, class string) {
	name, version, versioned := cutVersion(key)
	if !versioned {
		return name, classNone
	}
	return name, semverClass(version)
}

// cutVersion cuts the `@…` from an identity's interface, returning the version-free name and what followed
// the '@' verbatim. A lowered import carries a version there; a host table key carries a class (`classKey`).
func cutVersion(key string) (name, version string, versioned bool) {
	iface, export, found := strings.Cut(key, "::")
	if !found {
		return key, "", false
	}
	at := strings.IndexByte(iface, '@')
	if at < 0 {
		return key, "", false
	}
	return iface[:at] + "::" + export, iface[at+1:], true
}

// classKey is the host table key serving `name` for one class: the class sits where an import's version
// does, so `wasi:cli/stdout::get-stdout` for 0.2 is `wasi:cli/stdout@0.2::get-stdout`.
func classKey(name, class string) string {
	iface, export, found := strings.Cut(name, "::")
	if !found {
		return name + "@" + class
	}
	return iface + "@" + class + "::" + export
}

// The tracks a production entry is registered for. WASI 0.2 and 0.3 are the two the host serves.
var (
	wasiP2   = []string{"0.2"}
	wasiP3   = []string{"0.3"}
	wasiP2P3 = []string{"0.2", "0.3"}
)

// serve registers one implementation of `name` for each of `classes`, so an entry declares the tracks it
// serves where it is written, and one serving both tracks says so rather than inheriting the other's.
func serve[V any](m map[string]V, classes []string, name string, v V) {
	for _, c := range classes {
		m[classKey(name, c)] = v
	}
}

// semverClass is a version's compatibility class: `0.0.p` for 0.0.x (every patch breaks), `0.m` for 0.x,
// and the major alone from 1 up. A pre-release keeps its core's class (`0.3.0-rc-2026-02-09` is `0.3`). The
// parse is strict SemVer 2.0.0, so `0.3`, `v0.3.0` or `0.03.0` is unparseable rather than guessed at.
func semverClass(v string) string {
	bad := classUnparseable + v
	core, build, hasBuild := strings.Cut(v, "+")
	if hasBuild && !semverIdents(build, false) {
		return bad
	}
	core, pre, hasPre := strings.Cut(core, "-")
	if hasPre && !semverIdents(pre, true) {
		return bad
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return bad
	}
	for _, p := range parts {
		if !semverNumeric(p) {
			return bad
		}
	}
	switch {
	case parts[0] != "0":
		return parts[0]
	case parts[1] != "0":
		return "0." + parts[1]
	default:
		return "0.0." + parts[2]
	}
}

// semverNumeric reports a SemVer numeric identifier: ASCII digits, with no leading zero unless it is "0".
func semverNumeric(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// semverIdents reports a dot-separated pre-release or build list: each identifier non-empty and drawn
// from [0-9A-Za-z-]. In a pre-release (numericNoLeadingZero) an all-digit identifier has no leading zero.
func semverIdents(s string, numericNoLeadingZero bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		digits := true
		for i := range len(id) {
			switch c := id[i]; {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				digits = false
			default:
				return false
			}
		}
		if numericNoLeadingZero && digits && !semverNumeric(id) {
			return false
		}
	}
	return true
}

// lookupHost meets a lowered import's identity with one host table. All four binding sites in walk.go use
// it, so the instantiate-time unmodeled-signature check sees the binding the call would get. Three outcomes:
//
//   - ok: an entry registered for the key's class (`classKey`), or a wildcard — an unversioned table key,
//     which is every test-only registration and every bare world-level import — when the class is
//     parseable;
//   - !ok with servedFor set: the table has the name, but only for other classes. That is the cross-track
//     case, which binds `refuseTrack` rather than the other track's implementation;
//   - !ok with servedFor nil: a plain miss, unchanged.
//
// The cross-track arm scans the table, so it is paid only on a miss, at instantiate, over a table of a
// few dozen entries.
func lookupHost[V any](table map[string]V, key string) (v V, ok bool, servedFor []string) {
	name, class := splitVersion(key)
	if entry, found := table[classKey(name, class)]; found {
		return entry, true, nil
	}
	if !strings.HasPrefix(class, classUnparseable) {
		if entry, found := table[name]; found {
			return entry, true, nil
		}
	}
	// NEUTER THIS by returning `table[k]` for the first same-name key, which ignores the class as
	// `stripVersion` did, and the three rows of `TestCrossTrackImportRefusesNamingBothTracks` bind the other
	// track's implementation while every fixture test stays green.
	for k := range table {
		if n, c, versioned := cutVersion(k); versioned && n == name {
			servedFor = append(servedFor, c)
		}
	}
	slices.Sort(servedFor)
	return v, false, servedFor
}

// refuseTrack is the stub a cross-track import binds: called, it refuses by name, naming the classes the
// host serves the name under and the one the guest asked for. At call rather than at instantiate, for the
// plain stub's reason: a guest can import a function it never calls, and refusing the import would refuse
// the guest.
func refuseTrack(mod, name, lowerName string, servedFor []string) interp.HostFunc {
	_, class := splitVersion(lowerName)
	served := make([]string, len(servedFor))
	for i, c := range servedFor {
		served[i] = trackName(lowerName, c)
	}
	return func(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
		return nil, fmt.Errorf("%w: import %s has only a %s implementation; it is not served for %s "+
			"(ADR 0115; lowered at core %s::%s)", ErrLinkRefused, lowerName, strings.Join(served, " and "),
			class, mod, name)
	}
}

// trackName renders a class as the track a reader knows it by: `WASI 0.2` for a wasi: import, the bare
// class otherwise.
func trackName(lowerName, class string) string {
	if strings.HasPrefix(lowerName, "wasi:") {
		return "WASI " + class
	}
	return class
}
