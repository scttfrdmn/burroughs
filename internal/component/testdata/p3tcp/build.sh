#!/usr/bin/env bash
# Rebuild p3tcp.wasm and re-take testdata/p3tcp.reading and testdata/p3tcp-denied.reading (#1235 E7a, ADR 0123).
#
# **Run deliberately and out of CI.** The committed p3tcp.wasm and the two readings ARE the artefacts; this script
# exists so they can be regenerated when a tool moves, not so anything on the test path invokes a Rust toolchain or
# wasmtime (p3cat/build.sh's shape: commit the oracle's reading, never depend on the oracle at test time).
#
# Offline: the crate has no dependencies, and cargo runs `--offline --locked` against the committed Cargo.lock, so
# a rebuild fetches nothing. The peer is echo/, built with the repository's own Go toolchain.
#
# ## Pitfalls already paid for
#
#   * `rustc +nightly` resolves to Homebrew's 1.99.1 on this machine, which has no wasm32-wasip3 target. The
#     nightly is named by its toolchain directory, and cargo is handed that rustc by RUSTC, not by PATH.
#   * The build runs in a scratch copy, never in the tree, so no target/ directory appears under testdata.
#   * The ports vary from run to run, so the guest prints none: each line is a property (the echo matched, the
#     local address is v4 loopback, the remote port is argv's), and the readings are byte-identical across runs.
#   * The grant is wasmtime's `-S inherit-network` alone, the one switch Scott's stamp mirrors. With no network
#     flag wasmtime refuses `tcp-socket.create` itself (access-denied), which is the denied reading.
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

for t in "$CARGO" "$RUSTC" "$WASMTIME"; do
	[ -x "$t" ] || { echo "build.sh: $t is not executable" >&2; exit 2; }
done

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
work=$(mktemp -d "$scratch_root/p3tcp-build.XXXXXX")
echo_pid=
cleanup() {
	[ -n "$echo_pid" ] && kill "$echo_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
cp -R "$here/Cargo.toml" "$here/Cargo.lock" "$here/src" "$work/"

# The outputs are deleted before the build, so a failed build cannot leave the old artefact to be read.
rm -f "$here/p3tcp.wasm" "$td/p3tcp.reading" "$td/p3tcp-denied.reading"
( cd "$work" && RUSTC="$RUSTC" CARGO_TARGET_DIR="$work/target" \
	"$CARGO" build --offline --locked --release --target wasm32-wasip3 )
cp "$work/target/wasm32-wasip3/release/p3tcp.wasm" "$here/p3tcp.wasm"
"$WASM_TOOLS" validate --features all "$here/p3tcp.wasm"

( cd "$repo" && "$GO" build -o "$work/echo" ./internal/component/testdata/p3tcp/echo )
"$work/echo" >"$work/ports" &
echo_pid=$!
for _ in $(seq 1 100); do
	[ -s "$work/ports" ] && break
	sleep 0.1
done
read -r port closed <"$work/ports"
[ -n "${closed:-}" ] || { echo "build.sh: the echo peer printed no ports" >&2; exit 1; }

: "${RUNS:=10}"
ends() { # ends <file>: "yes" if the file's last byte is a newline, else "no"
	[ -s "$1" ] && [ "$(tail -c 1 "$1" | od -An -c | tr -d ' ')" = '\n' ] && echo yes || echo no
}
prefix() { # prefix <name> <file>: each line of the file, newline-terminated or not, as "<name>: <line>"
	awk -v p="$1: " '{print p $0}' "$2"
}
# reading <its name in the reading> <reading file> <wasmtime flags...>: RUNS runs, compared, then the reading.
reading() {
	local name=$1 out=$2 code c i
	shift 2
	"$WASMTIME" run "$@" "$here/p3tcp.wasm" "$port" "$closed" >"$work/stdout" 2>"$work/stderr" && code=0 || code=$?
	for i in $(seq 2 "$RUNS"); do
		"$WASMTIME" run "$@" "$here/p3tcp.wasm" "$port" "$closed" >"$work/stdout.$i" 2>"$work/stderr.$i" && c=0 || c=$?
		if [ "$c" != "$code" ] || ! cmp -s "$work/stdout" "$work/stdout.$i" || ! cmp -s "$work/stderr" "$work/stderr.$i"; then
			echo "build.sh: run $i of $name differs from run 1 (exit $c, want $code); a reading must be stable" >&2
			diff "$work/stdout" "$work/stdout.$i" >&2 || true
			exit 1
		fi
	done
	{
		echo "# Committed reading of internal/component/testdata/p3tcp/p3tcp.wasm (#1235 E7a; ADR 0123)."
		echo "# Regenerated by p3tcp/build.sh; never hand-edited."
		echo "# Built:       p3tcp/build.sh ($("$RUSTC" --version | awk '{print $1, $2}'), no dependencies, wasm32-wasip3)"
		echo "# Invocation:  wasmtime run${*:+ $*} p3tcp.wasm <echo-port> <closed-port>   ($name)"
		echo "# Peer:        p3tcp/echo, an echo listener on 127.0.0.1, and a port it bound and closed"
		echo "# Engine:      $("$WASMTIME" --version), $(uname -s | tr 'A-Z' 'a-z')/$(uname -m | sed 's/aarch64/arm64/')."
		echo "# Runs:        $RUNS of $RUNS byte-identical (stdout, stderr and exit)"
		echo "# exit:        $code"
		echo "# stdout ends with a newline: $(ends "$work/stdout")"
		echo "# stderr bytes: $(wc -c <"$work/stderr" | tr -d ' ')"
		echo "#"
		echo "# Each line below is one channel's line, prefixed by the channel's name."
		prefix stdout "$work/stdout"
		prefix stderr "$work/stderr"
	} >"$out"
	cat "$out"
}
reading "the network granted" "$td/p3tcp.reading" -S inherit-network
reading "no network flag" "$td/p3tcp-denied.reading"
shasum -a 256 "$here/p3tcp.wasm"
wc -c <"$here/p3tcp.wasm"
