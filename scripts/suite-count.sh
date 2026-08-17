#!/usr/bin/env sh
# Count the spec-suite vectors in a directory — and count them the way Go counts them.
#
# usage: scripts/suite-count.sh [dir]      # default testdata/spec; prints one integer
#
# # Why this is a file and not four expressions (#340)
#
# There were four shell-side counts of the suite population (`Makefile`, `ci.yml` twice,
# `fetch-spec-tests.sh`) and one Go-side definition, and they were **different definitions of
# which files are vectors**. `testenv.SuitePaths` is now the Go definition; this script is its
# shell twin, and `TestShellAndGoAgreeOnTheSuitePopulation` runs the two against a poisoned
# tree and requires the same integer. A policy in two languages needs a control between them
# or it is a policy in one language and a coincidence in the other.
#
# # Why two globs and a `case`, rather than `ls`, `find`, or a pipe
#
#   - `*.wast` alone is **dot-blind**: a POSIX `*` skips a leading dot and Go's
#     `filepath.Match` does not. That asymmetry *is* the grave — 257 AppleDouble sidecars
#     (`._address.wast`) written beside 257 vectors read as 257 to every shell count here and
#     514 to Go, so the shell checker reported the poisoned tree and the clean one alike.
#     `.*.wast` is the other half of the name space, so the union is what Go globs.
#   - `._*` is then excluded **on both sides**, because a sidecar is not a vector. #340
#     prescribed `find … ! -name '._*'` for the shell alone; measured, that expression
#     reproduces the *shell's* population and not Go's, so the exclusion has to land in
#     `SuitePaths` too — it does, and that is what makes these two counts comparable at all.
#     Excluding `._` rather than every dotfile: `._` is AppleDouble's own marker, and
#     excluding all dotfiles would silently widen this to a class nobody measured.
#   - **No pipe**, so there is no exit code to swallow (grave #289): `ls … | wc -l` answers a
#     question about the corpus's size with a number that may really be an enumeration
#     failure, which is a verdict travelling on the wrong channel.
#   - An absent or empty directory is an honest `0` rather than an error, because every caller
#     has its own diagnostic for that and a floor's message is more use than `find`'s. `[ -e ]`
#     is what makes an unmatched glob honest instead of literal.
set -eu

dir="${1:-testdata/spec}"
n=0
for f in "$dir"/*.wast "$dir"/.*.wast; do
	case "${f##*/}" in
	._*) continue ;;
	esac
	if [ -e "$f" ]; then n=$((n + 1)); fi
done
echo "$n"
