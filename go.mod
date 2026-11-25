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
// # Why 1.26.11 and not a 1.27 release
//
// A patch on the 1.26 line is the smallest change that clears an advisory and keeps the language line
// at `go 1.26`, which is what `CLAUDE.md` states this project requires. A minor-line move carries new
// vet and lint behaviour; conflating that with a security fix makes both harder to review and revert.
// That was 1.26.9 for GO-2026-6604. It is 1.26.11 for GO-2026-6859 (a `net/http` MaxBytesReader
// header race, fixed in 1.26.11) and GO-2026-6809 (compromised intermediates distrusted in
// `crypto/x509`, fixed in 1.26.10). Both became reachable from the engine when `wasi:http`'s client
// (#1383) first called `net/http.Transport`, so `make ci`'s vuln gate refused that tree on 1.26.9.
toolchain go1.26.11
