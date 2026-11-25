#!/usr/bin/env bash
# Rebuild p3serve.wasm and re-take testdata/p3serve.reading (#1385 E2-in-b, ADR 0137).
#
# **Run deliberately and out of CI.** The committed p3serve.wasm and p3serve.reading ARE the artefacts; this script
# exists so they can be regenerated when a tool moves, not so anything on the test path invokes a Rust toolchain or
# wasmtime (p3http/build.sh's shape: commit the oracle's reading, never depend on the oracle at test time).
#
# Offline: cargo resolves wit-bindgen 0.61.1 and its dependencies from the local registry (`--offline`, and `--locked`
# against the committed Cargo.lock), so a rebuild fetches nothing. The reading is taken by the Go test that checks it,
# p3serve_test.go's driver, run with -update-p3serve: it starts the pinned wasmtime's serve itself, reads the address
# serve prints, and drives every row over raw TCP on 127.0.0.1.
#
# ## Pitfalls already paid for
#
#   * `rustc +nightly` resolves to Homebrew's 1.99.1 on this machine, which has no wasm32-wasip3 target. The nightly
#     is named by its toolchain directory, and cargo is handed that rustc by RUSTC, not by PATH (p3http's).
#   * The pinned WIT is not committed. It is the WASI 0.3.0 WIT the nightly's std vendors for its own bindings
#     (library/vendor/wasip3-0.8.0+wasi-0.3.0/wit/deps), staged beside wit/world.wit in a scratch copy of the crate.
#   * The build runs in that scratch copy, never in the tree, so no target/ directory appears under testdata.
#   * **wasmtime's default instance reuse panics this guest** under concurrent requests: wit-bindgen 0.61.1's
#     `start_task` asserts its context slot is empty, and a second task entering an instance whose first has returned
#     but not exited finds it set (R2, #1372; ADR 0137 reads it again). The reading is taken with
#     --max-instance-reuse-count 1, a fresh instance per request.
#   * **No port reaches a reading.** Every request names Host p3serve.test, and the guest prints no address.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../../.." && pwd)
td="$repo/internal/component/testdata"
: "${NIGHTLY:=$HOME/.rustup/toolchains/nightly-aarch64-apple-darwin}"
: "${CARGO:=$NIGHTLY/bin/cargo}"
: "${RUSTC:=$NIGHTLY/bin/rustc}"
: "${WASM_TOOLS:=wasm-tools}"
: "${GO:=go}"
# The pinned oracle that scripts/oracle-fetch.sh installs, never the wasmtime on PATH; the driver finds it the same
# way, the tree's own or the main checkout's for a worktree.
: "${WASMTIME:=$repo/.oracle/wasmtime-49.0.3/wasmtime}"
[ -x "$WASMTIME" ] || WASMTIME="$HOME/src/burroughs/.oracle/wasmtime-49.0.3/wasmtime"
WIT_DEPS="$NIGHTLY/lib/rustlib/src/rust/library/vendor/wasip3-0.8.0+wasi-0.3.0/wit/deps"

for t in "$CARGO" "$RUSTC" "$WASMTIME"; do
	[ -x "$t" ] || { echo "build.sh: $t is not executable" >&2; exit 2; }
done
[ -d "$WIT_DEPS" ] || { echo "build.sh: no pinned WIT at $WIT_DEPS" >&2; exit 2; }

: "${EXPECT_RUSTC:=rustc 1.100.0-nightly}"
: "${EXPECT_WASM_TOOLS:=wasm-tools 1.258.0}"
: "${EXPECT_WASMTIME:=wasmtime 49.0.3}"
expect_version() { # expect_version <tool> <"name version">: the first two fields of `<tool> --version`
	got=$("$1" --version | awk 'NR == 1 {print $1, $2}')
	[ "$got" = "$2" ] || {
		echo "build.sh: $1 is '$got', and the committed artefacts were built and read with '$2'." >&2
		echo "          Rebuild and re-take on the new version, and move the expectation in the same commit." >&2
		exit 2
	}
}
expect_version "$RUSTC" "$EXPECT_RUSTC"
expect_version "$WASM_TOOLS" "$EXPECT_WASM_TOOLS"
expect_version "$WASMTIME" "$EXPECT_WASMTIME"

scratch_root=${TMPDIR:-/tmp}
[ -d "$HOME/.xtmp" ] && scratch_root="$HOME/.xtmp"
work=$(mktemp -d "$scratch_root/p3serve-build.XXXXXX")
trap 'rm -rf "$work"' EXIT
cp -R "$here/Cargo.toml" "$here/Cargo.lock" "$here/src" "$work/"
mkdir -p "$work/wit/deps"
cp "$here/wit/world.wit" "$work/wit/"
for f in random clocks cli filesystem sockets http; do cp "$WIT_DEPS/$f.wit" "$work/wit/deps/"; done

# The outputs are deleted before the build, so a failed build cannot leave the old artefact to be read.
rm -f "$here/p3serve.wasm" "$td/p3serve.reading"
( cd "$work" && RUSTC="$RUSTC" CARGO_TARGET_DIR="$work/target" \
	"$CARGO" build --offline --locked --release --target wasm32-wasip3 )
cp "$work/target/wasm32-wasip3/release/p3serve.wasm" "$here/p3serve.wasm"
"$WASM_TOOLS" validate --features all "$here/p3serve.wasm"

# The driver takes 10 drives, each against a fresh `wasmtime serve`, and writes the reading only if all agree.
( cd "$repo" && "$GO" test ./internal/component/ -run '^TestP3ServeMatchesWasmtime$' -count=1 -update-p3serve )
cat "$td/p3serve.reading"
shasum -a 256 "$here/p3serve.wasm"
wc -c <"$here/p3serve.wasm"
