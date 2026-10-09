module github.com/scttfrdmn/burroughs

go 1.26

// The one place the toolchain version is written. **It is a floor, not an exact pin** — that
// distinction is the whole reason the Makefile and CI both derive from it rather than trusting it.
//
// Under the default `GOTOOLCHAIN=auto` the go command switches *up* to satisfy this line and never
// down, so a machine with 1.27.1 installed keeps using 1.27.1 and this line is satisfied. Measured,
// not assumed: with `toolchain go1.26.9` present, `go version` on such a machine still reports
// go1.27.1. Exactness comes from `GOTOOLCHAIN=go1.26.9`, which the Makefile sets from this line, and
// in CI from `go-version-file: go.mod`, which installs it.
//
// `make ci` then asserts that the toolchain it actually ran under equals this version, so a machine
// running something else is a **stated failure** rather than a silent superset.
//
// # Why a pin at all
//
// CI pinned `go-version: '1.26'` and developer machines ran whatever was installed, so `make ci` green
// meant "green on some toolchain", not "green on CI's". GO-2026-6604 is what that gap cost: a Windows
// `os.Root` escape fixed in 1.26.9, invisible to a machine on 1.27 and reported by CI on 1.26.8. The
// advisory's traces were in the `--scratch` write path, so the gap hid a confinement finding rather
// than a cosmetic one.
//
// # Why 1.26.9 and not 1.27.2, the newest release
//
// 1.26.9 is the smallest change that clears the advisory and keeps the language line at `go 1.26`,
// which is what `CLAUDE.md` states this project requires. A minor-line move carries new vet and lint
// behaviour; conflating that with a security fix makes both harder to review and harder to revert.
toolchain go1.26.9
