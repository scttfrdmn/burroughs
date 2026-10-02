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

for g in single suspending; do
	echo "== guest: $g ==" >&2
	# `wit_bindgen::generate!` reads `path: "wit"`, a DIRECTORY relative to the crate root, so each guest
	# keeps `wit/world.wit` rather than a flat `world.wit`. The first staging of these artefacts flattened
	# it, and the macro's failure blamed an unresolved `exports` module three errors down from the cause.
	( cd "$here/$g" && RUSTC="$RUSTUP_RUSTC" "$RUSTUP_CARGO" build --target wasm32-unknown-unknown --release )
	core=$(find "$here/$g/target" /Volumes/External\ HD/cargo-target/wasm32-unknown-unknown/release \
		-name '*.wasm' -newer "$here/$g/src/lib.rs" 2>/dev/null | head -1)
	[ -n "$core" ] || { echo "build.sh: no core module found for $g" >&2; exit 1; }
	"$WASM_TOOLS" component new "$core" -o "$here/$g/component.wasm"
	echo "   wrote $g/component.wasm" >&2
done

echo "== harness ==" >&2
( cd "$here/harness" && RUSTC="$HOST_RUSTC" "$HOST_CARGO" build --release )
bin=$(find "$here/harness/target/release" /Volumes/External\ HD/cargo-target/release \
	-name concwitness -type f 2>/dev/null | head -1)
[ -n "$bin" ] || { echo "build.sh: concwitness was not built" >&2; exit 1; }

echo "== readings ==" >&2
"$bin" concurrent "$here/suspending/component.wasm" > "$here/concurrent.reading" 2>&1 || true
"$bin" sequential "$here/suspending/component.wasm" > "$here/sequential.reading" 2>&1 || true
cat "$here/concurrent.reading" "$here/sequential.reading" >&2

echo >&2
echo "build.sh: the SEQUENTIAL reading is expected to report an expired rendezvous." >&2
echo "          That is the negative arm's verdict, not a failure of this script." >&2
