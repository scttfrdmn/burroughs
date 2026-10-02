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
: "${HOST_CARGO:=/opt/homebrew/bin/cargo}"    # new enough for the wasmtime crate
: "${WASM_TOOLS:=wasm-tools}"

for t in "$RUSTUP_CARGO" "$HOST_CARGO"; do
	[ -x "$t" ] || { echo "build.sh: $t is not executable — see the toolchain note above" >&2; exit 2; }
done

echo "== provenance ==" >&2
"$RUSTUP_CARGO" --version >&2
"$HOST_CARGO" --version >&2
"$WASM_TOOLS" --version >&2

for g in single suspending; do
	echo "== guest: $g ==" >&2
	( cd "$here/$g" && "$RUSTUP_CARGO" build --target wasm32-unknown-unknown --release )
	core=$(find "$here/$g/target" /Volumes/External\ HD/cargo-target/wasm32-unknown-unknown/release \
		-name '*.wasm' -newer "$here/$g/src/lib.rs" 2>/dev/null | head -1)
	[ -n "$core" ] || { echo "build.sh: no core module found for $g" >&2; exit 1; }
	"$WASM_TOOLS" component new "$core" -o "$here/$g/component.wasm"
	echo "   wrote $g/component.wasm" >&2
done

echo "== harness ==" >&2
( cd "$here/harness" && "$HOST_CARGO" build --release )
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
