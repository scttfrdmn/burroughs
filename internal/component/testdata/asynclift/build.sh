#!/usr/bin/env bash
# Rebuild slice 1's async-lift guests and re-take the concurrency readings.
#
# **Run deliberately and out of CI.** The committed `component.wasm` files and `.reading` files ARE the
# artefacts; this script exists so they can be regenerated when a tool moves, not so that anything on the test
# path invokes a Rust toolchain. Same shape as `make watref` and ADR 0086's committed wasmtime readings:
# commit the oracle's reading, never depend on the oracle at test time.
#
# ## Two Rust toolchains are required, and neither alone suffices
#
# This is a fact about the slice, not a note:
#
#   * the GUESTS need rustup's toolchain, which has the wasm targets — but its rustc (1.91.1) is TOO OLD for
#     the wasmtime 49 crate;
#   * the HARNESS needs a newer rustc (Homebrew's 1.98.1) — but that install has NO wasm targets.
#
# A single-version provenance record would be wrong in a way that costs the next reader a session.
#
# ## Three blocked paths, recorded so they are not re-paid for
#
#   * `cargo-component` 0.21.1 CANNOT build an async guest: it pins wit-bindgen 0.41.0, whose async codegen is
#     internally inconsistent — the cabi calls `T::async_compute` while the trait declares `fn compute`,
#     identically in both the world-inline and interface WIT shapes. Use `wit-bindgen` 0.62.0 via the macro.
#   * The rustup `wasm32-wasip2` sysroot CANNOT componentize an async guest: its bundled `wasm-component-ld`
#     fails with `failed to parse core wasm for componentization`. Build a core module for
#     `wasm32-unknown-unknown` and componentize with `wasm-tools` instead, which is what this script does.
#   * `async` is a RESERVED KEYWORD in WIT and cannot appear in a package name.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
: "${RUSTUP_CARGO:=$HOME/.cargo/bin/cargo}"   # has the wasm targets
: "${RUSTUP_RUSTC:=$HOME/.cargo/bin/rustc}"   # must be pinned WITH it — see below
: "${HOST_CARGO:=/opt/homebrew/bin/cargo}"    # new enough for the wasmtime crate
: "${HOST_RUSTC:=/opt/homebrew/bin/rustc}"    # pinned for the same reason, in the other direction
: "${WASM_TOOLS:=wasm-tools}"

# ## Pinning the cargo is not enough: `rustc` must be pinned too
#
# **Measured, by running this script from a clean checkout and watching it fail.** Cargo invokes a BARE
# `rustc`, resolved through `PATH` — so on a machine where Homebrew's bin directory precedes `~/.cargo/bin`
# (which is this one), the rustup cargo above drives the HOMEBREW rustc. That install has no wasm targets, and
# the error names the wrong cause:
#
#     error[E0463]: can't find crate for `core`
#     = note: the `wasm32-unknown-unknown` target may not be installed
#
# The target IS installed; `rustup target list --installed` says so. The toolchain that cannot see it is the
# one PATH chose. Two toolchains with disjoint capabilities is the condition this slice is stuck with, so
# **which rustc runs cannot be left to PATH order** — ambient state is exactly what made the first clean-tree
# rebuild fail while the dirty development tree kept working.
# The harness is pinned too, and the failure it avoids is the mirror image: with `~/.cargo/bin` ahead of
# Homebrew's, the host cargo would drive the 1.91.1 rustc, which is too old for the wasmtime 49 crate. Neither
# side of this build may inherit its compiler from PATH.
for t in "$RUSTUP_CARGO" "$RUSTUP_RUSTC" "$HOST_CARGO" "$HOST_RUSTC"; do
	[ -x "$t" ] || { echo "build.sh: $t is not executable — see the toolchain note above" >&2; exit 2; }
done

echo "== provenance ==" >&2
"$RUSTUP_CARGO" --version >&2
"$RUSTUP_RUSTC" --version >&2
"$HOST_CARGO" --version >&2
"$WASM_TOOLS" --version >&2

## Locating the build's output: ask cargo, never `find`
#
# **This paid for itself immediately.** The previous form searched a target directory for a `*.wasm` `-newer`
# than `src/lib.rs` and took the first hit, with a hard-coded fallback into an ambient `CARGO_TARGET_DIR` on an
# external drive. Run from a clean checkout — the only condition under which a reproducibility claim means
# anything — every part of that failed at once:
#
#   * a clean checkout stamps `src/lib.rs` with the checkout time, so a warm target directory's artefact is
#     always OLDER than the source and `-newer` matches nothing;
#   * the shared target directory meant the build was a 0.62s cache hit from a DIFFERENT worktree, so the
#     search was being pointed at another tree's artefacts — which answers the question with a past version of
#     the tree whether or not anyone appointed it an oracle;
#   * the script then exited 1 **before** `wasm-tools component new` ran, leaving the committed
#     `component.wasm` untouched — so the hash comparison downstream compared the files to THEMSELVES and
#     reported a clean match. A failing build produced a passing reproducibility check.
#
# So: a per-guest target directory (no cross-tree sharing), the artefact path read from cargo's own JSON
# (no mtime heuristic, no first-match pick), and **the output deleted before the build** — a comparison whose
# subject can survive a failed build is not a comparison.
emit_artifact() {
	# Reads `cargo build --message-format=json` on stdin and prints the single compiler-artifact filename
	# matching the given extension. Refuses on anything but exactly one, because "take the first" is how the
	# previous form silently answered from the wrong tree.
	python3 -c '
import json, sys
want = sys.argv[1]
hits = []
for line in sys.stdin:
    line = line.strip()
    if not line.startswith("{"):
        continue
    try:
        m = json.loads(line)
    except json.JSONDecodeError:
        continue
    if m.get("reason") != "compiler-artifact":
        continue
    hits += [f for f in (m.get("filenames") or []) if f.endswith(want)]
if len(hits) != 1:
    print(f"expected exactly 1 {want} artifact, cargo reported {len(hits)}: {hits}", file=sys.stderr)
    raise SystemExit(1)
print(hits[0])
' "$1"
}

for g in single suspending receipt; do
	echo "== guest: $g ==" >&2
	# `wit_bindgen::generate!` reads `path: "wit"`, a DIRECTORY relative to the crate root, so each guest
	# keeps `wit/world.wit` rather than a flat `world.wit`. The first staging of these artefacts flattened
	# it, and the macro's failure blamed an unresolved `exports` module three errors down from the cause.
	rm -f "$here/$g/component.wasm"
	core=$( cd "$here/$g" &&
		RUSTC="$RUSTUP_RUSTC" CARGO_TARGET_DIR="$here/$g/target" \
			"$RUSTUP_CARGO" build --target wasm32-unknown-unknown --release --message-format=json |
			emit_artifact .wasm )
	[ -n "$core" ] || { echo "build.sh: cargo reported no core module for $g" >&2; exit 1; }
	"$WASM_TOOLS" component new "$core" -o "$here/$g/component.wasm"
	echo "   wrote $g/component.wasm from $core" >&2
done

echo "== harness ==" >&2
bin=$( cd "$here/harness" &&
	RUSTC="$HOST_RUSTC" CARGO_TARGET_DIR="$here/harness/target" \
		"$HOST_CARGO" build --release --message-format=json | emit_artifact concwitness )
[ -n "$bin" ] || { echo "build.sh: concwitness was not built" >&2; exit 1; }

echo "== readings ==" >&2
"$bin" concurrent "$here/suspending/component.wasm" > "$here/concurrent.reading" 2>&1 || true
"$bin" sequential "$here/suspending/component.wasm" > "$here/sequential.reading" 2>&1 || true
cat "$here/concurrent.reading" "$here/sequential.reading" >&2

# The abandonment arms (#857, ABANDONMENT.md). Both are cut from the receipt guest by
# `scripts/stripcall.py`, which refuses on anything but exactly one matched call, requires the edited
# module to VALIDATE, requires the bytes to change, and requires the committed diff to be +0/-1.
#
# **Both arms go through the round-trip**, because `wasm-tools print`/`parse` is not byte-identical
# (measured: a2600925… -> bb766fec… on a no-op round-trip). Comparing a committed guest against an
# edited one would differ by the edit AND by normalization; comparing round-tripped against
# round-tripped-and-edited leaves the deleted call as the only variable.
echo "== abandonment arms ==" >&2
arms=$(mktemp -d)
python3 "$here/../../../../scripts/stripcall.py" "$here/receipt/component.wasm" --out-dir "$arms"
cp "$arms/stripped.wat.diff" "$here/receipt.wat.diff"
"$bin" abandon "$arms/roundtrip.wasm" > "$here/abandonment.reading" 2>&1 || true
"$bin" abandon "$arms/stripped.wasm" > "$here/abandonment-stripped.reading" 2>&1 || true
cat "$here/abandonment.reading" >&2
rm -rf "$arms"

echo >&2
echo "build.sh: the SEQUENTIAL reading is expected to report an expired rendezvous." >&2
echo "          That is the negative arm's verdict, not a failure of this script." >&2
echo "build.sh: the two ABANDONMENT readings are expected to be IDENTICAL. Dropping a host call" >&2
echo "          future does not cancel a started task, so the deleted task.cancel cannot matter." >&2
echo "          That identity is the finding -- see ABANDONMENT.md -- not a failure either." >&2
