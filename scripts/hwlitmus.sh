#!/bin/sh
# hwlitmus.sh — the hardware weak-memory reading (#1126): every arm, on this machine, as one table.
#
#   scripts/hwlitmus.sh [outdir]
#
# Runs internal/interp's `hwlitmus`-tagged tests three ways: the engine build (arm 1, plus the native plain-Go
# control and the wasmtime arm), and the `burroughs_plainmem` neuter build (arm 2). Each is logged to outdir
# (default: a fresh temp dir), and the table rows are printed at the end. BURROUGHS_HWLITMUS_TRIALS sets the
# trials per cell (default 2^20).
#
# **wasmtime is fetched here, by digest, and it is not the oracle pin.** The pin, 49.0.3, has no
# wasi-threads (removed upstream in #13558, first absent from v46.0.0). The arm uses the v36 LTS line, which
# keeps them. The digests are the release's own, as GitHub serves them for each asset. A mismatch stops the run.
#
# Measured work: on a lab host, run it through pueue's `measured` group (CLAUDE.md), for example
#   XCHECK_HOST=janus.local XCHECK_GROUP=measured scripts/xcheck-amd64.sh scripts/hwlitmus.sh
set -eu

WT_VERSION=v36.0.19
out=${1:-$(mktemp -d)}
mkdir -p "$out"

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) asset=aarch64-macos sum=11374e5bd835da9040b16889beb5731b946a53424f432a28098c414dfd740e70 ;;
Linux-x86_64) asset=x86_64-linux sum=c6070b3043023f3b8e8dc3473a4c360c0d6784aa4294f5a6fc803580fcb2c875 ;;
Linux-aarch64) asset=aarch64-linux sum=cd6f66bbd24c3ef7915135df71a963de06939f01752b02783f093e307f09ad37 ;;
*)
	echo "hwlitmus: no wasmtime $WT_VERSION digest recorded for $(uname -s)-$(uname -m)" >&2
	exit 2
	;;
esac

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

# The archive is untrusted data: its own empty directory, and only the binary is used.
dl="$out/wasmtime-dl"
mkdir -p "$dl"
curl -sSfL -o "$dl/wt.tar.xz" \
	"https://github.com/bytecodealliance/wasmtime/releases/download/$WT_VERSION/wasmtime-$WT_VERSION-$asset.tar.xz"
got=$(sha256 "$dl/wt.tar.xz")
if [ "$got" != "$sum" ]; then
	echo "hwlitmus: wasmtime $WT_VERSION $asset digest $got, want $sum" >&2
	exit 2
fi
tar -xJf "$dl/wt.tar.xz" -C "$dl" --strip-components=1
wasmtime="$dl/wasmtime"

{
	echo "host: $(hostname -s) · $(uname -sm) · $(go version)"
	echo "wasmtime: $("$wasmtime" --version) · $asset · sha256 $sum"
} | tee "$out/provenance.txt"

status=0
BURROUGHS_WASMTIME=$wasmtime go test -tags hwlitmus ./internal/interp/ -run 'TestHWLitmus' -v -count=1 \
	-timeout 120m >"$out/engine.log" 2>&1 || status=1
go test -tags hwlitmus,burroughs_plainmem ./internal/interp/ -run 'TestHWLitmus$' -v -count=1 \
	-timeout 120m >"$out/neuter.log" 2>&1 || status=1

grep -hE 'arm "|\| ' "$out/engine.log" "$out/neuter.log" | sed -E 's/^ *hwlitmus_test.go:[0-9]+: //'
grep -hE '^(--- FAIL|FAIL|ok)' "$out/engine.log" "$out/neuter.log"
echo "hwlitmus: logs in $out"
exit $status
