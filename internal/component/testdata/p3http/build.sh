#!/usr/bin/env bash
# Rebuild p3http.wasm and re-take testdata/p3http.reading (#1383 E2-out, ADR 0135).
#
# **Run deliberately and out of CI.** The committed p3http.wasm and p3http.reading ARE the artefacts; this script
# exists so they can be regenerated when a tool moves, not so anything on the test path invokes a Rust toolchain or
# wasmtime (p3sys/build.sh's shape: commit the oracle's reading, never depend on the oracle at test time).
#
# Offline: cargo resolves wit-bindgen 0.61.1 and its dependencies from the local registry (`--offline`, and `--locked`
# against the committed Cargo.lock), so a rebuild fetches nothing. The peer is peer/, built with the repository's own
# Go toolchain, and every row's network is 127.0.0.1 or a `.invalid` name, which never resolves.
#
# ## Pitfalls already paid for
#
#   * `rustc +nightly` resolves to Homebrew's 1.99.1 on this machine, which has no wasm32-wasip3 target. The nightly
#     is named by its toolchain directory, and cargo is handed that rustc by RUSTC, not by PATH.
#   * The pinned WIT is not committed. It is the WASI 0.3.0 WIT the nightly's std vendors for its own bindings
#     (library/vendor/wasip3-0.8.0+wasi-0.3.0/wit/deps), staged beside wit/world.wit in a scratch copy of the crate.
#   * The build runs in that scratch copy, never in the tree, so no target/ directory appears under testdata.
#   * **wasmtime gives outgoing HTTP to `run` only with `-S http`**, and without it the component does not link. The
#     denied row is that reading, and the deny-at-call is Burroughs' named divergence from it (ADR 0135).
#   * **A port must never reach a reading.** The guest prints none, and the peer's /dump writes the Host it read with
#     its own port replaced by PORT.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../../.." && pwd)
td="$repo/internal/component/testdata"
: "${NIGHTLY:=$HOME/.rustup/toolchains/nightly-aarch64-apple-darwin}"
: "${CARGO:=$NIGHTLY/bin/cargo}"
: "${RUSTC:=$NIGHTLY/bin/rustc}"
: "${WASM_TOOLS:=wasm-tools}"
: "${GO:=go}"
# The pinned oracle that scripts/oracle-fetch.sh installs, never the wasmtime on PATH (50.0.1 on this machine).
# A worktree has no .oracle of its own, so the main checkout's is the fallback.
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
work=$(mktemp -d "$scratch_root/p3http-build.XXXXXX")
peer_pid=
cleanup() {
	[ -n "$peer_pid" ] && kill "$peer_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
cp -R "$here/Cargo.toml" "$here/Cargo.lock" "$here/src" "$work/"
mkdir -p "$work/wit/deps"
cp "$here/wit/world.wit" "$work/wit/"
for f in random clocks cli filesystem sockets http; do cp "$WIT_DEPS/$f.wit" "$work/wit/deps/"; done

# The outputs are deleted before the build, so a failed build cannot leave the old artefact to be read.
rm -f "$here/p3http.wasm" "$td/p3http.reading"
( cd "$work" && RUSTC="$RUSTC" CARGO_TARGET_DIR="$work/target" \
	"$CARGO" build --offline --locked --release --target wasm32-wasip3 )
cp "$work/target/wasm32-wasip3/release/p3http.wasm" "$here/p3http.wasm"
"$WASM_TOOLS" validate --features all "$here/p3http.wasm"

( cd "$repo" && "$GO" build -o "$work/peer" ./internal/component/testdata/p3http/peer )
# The ports arrive through a FIFO, so the read waits on the peer's line, not on a timer.
mkfifo "$work/ports"
"$work/peer" >"$work/ports" &
peer_pid=$!
read -r port closed <"$work/ports"
[ -n "${port:-}" ] && [ -n "${closed:-}" ] || { echo "build.sh: the peer printed no ports" >&2; exit 1; }

# Every row: the network rows reach the peer, the rest touch no network. The trap rows' readings keep the line naming
# the trap, and the denied row is the run with no -S http.
rows="get head purge dump post big post-declared post-cl post-empty post-dropbody bigresp early-drop res-first
refused dns https otherscheme noscheme nopath noauthority timeout fields order request options options-child-drop
response response-consume request-consume consume-empty consume-request-res consume-response-res request-drop
response-drop"

: "${RUNS:=10}"
# one <row> <flags...>: RUNS runs of the row, compared byte for byte, into $work/stdout, $work/stderr and $work/code.
one() {
	local row=$1 c i
	shift
	"$WASMTIME" run "$@" "$here/p3http.wasm" "$row" "$port" "$closed" >"$work/stdout" 2>"$work/stderr" && c=0 || c=$?
	echo "$c" >"$work/code"
	for i in $(seq 2 "$RUNS"); do
		"$WASMTIME" run "$@" "$here/p3http.wasm" "$row" "$port" "$closed" >"$work/stdout.$i" 2>"$work/stderr.$i" && c=0 || c=$?
		if [ "$c" != "$(cat "$work/code")" ] || ! cmp -s "$work/stdout" "$work/stdout.$i" || ! cmp -s "$work/stderr" "$work/stderr.$i"; then
			echo "build.sh: run $i of row $row differs from run 1 (exit $c); a reading must be stable" >&2
			diff "$work/stdout" "$work/stdout.$i" >&2 || true
			exit 1
		fi
	done
	if [ "$(cat "$work/code")" = 0 ] && [ -s "$work/stderr" ]; then
		echo "build.sh: row $row exited 0 with stderr:" >&2
		cat "$work/stderr" >&2
		exit 1
	fi
}
# record <name>: the row's block of the reading. A failed run keeps the last line of its stderr, which names the
# trap or the link failure; the backtrace above it is wasmtime's addresses, not the guest's behaviour.
record() {
	echo "row: $1"
	awk '{print "stdout: " $0}' "$work/stdout"
	if [ "$(cat "$work/code")" != 0 ]; then
		echo "cause: $(grep -v '^\s*$' "$work/stderr" | tail -n 1 | sed 's/^ *[0-9]*: //')"
	fi
	echo "exit: $(cat "$work/code")"
}
{
	echo "# Committed reading of internal/component/testdata/p3http/p3http.wasm (#1383 E2-out; ADR 0135)."
	echo "# Regenerated by p3http/build.sh; never hand-edited."
	echo "# Built:       p3http/build.sh ($("$RUSTC" --version | awk '{print $1, $2}'), wit-bindgen 0.61.1, wasm32-wasip3)"
	echo "# Invocation:  wasmtime run -S http p3http.wasm <row> <port> <closed-port>, the peer peer/ on 127.0.0.1;"
	echo "#              the denied row with no -S http."
	echo "# Engine:      $("$WASMTIME" --version), $(uname -s | tr 'A-Z' 'a-z')/$(uname -m | sed 's/aarch64/arm64/')."
	echo "# Runs:        $RUNS of $RUNS identical per row (stdout, stderr and exit code); stderr empty on every exit 0."
	echo "#"
	echo "# Each block is one row: its stdout lines, the cause of a failed run (the last line of its stderr), and its"
	echo "# exit code."
	for row in $rows; do
		one "$row" -S http
		record "$row"
	done
	one get
	record denied
} >"$td/p3http.reading"
cat "$td/p3http.reading"
shasum -a 256 "$here/p3http.wasm"
wc -c <"$here/p3http.wasm"
