#!/bin/sh
# oracle-fetch.sh — install the pinned wasmtime oracle at a fixed path, verified by SHA-256.
#
# The committed wasmtime readings were taken on wasmtime 49.0.3, and the fixture scripts assert that
# version before they read anything. The oracle used to be whatever the package manager had installed,
# and it moved twice underneath the readings: 49.0.2 to 49.0.3, then Homebrew's 50.0.0 on 2026-10-26.
# So it is fetched instead (the chair's third ruling of 2026-10-26):
#   - the official release archive for this host's platform, from wasmtime's GitHub releases;
#   - checked against the SHA-256 committed beside this script (`oracle-wasmtime.sha256`);
#   - installed at `.oracle/wasmtime-49.0.3/wasmtime` (gitignored), which the build scripts read by default.
#
# The committed sums were checked against GitHub's own digest for each release asset, and against a
# download of each, on 2026-10-26. wasmtime publishes no checksum file of its own.
#
# **A mismatch is refused, and nothing is installed.** The archive is verified before it is unpacked,
# and the install is a rename of a fully unpacked directory, so a refused or failed run leaves any
# previous install as it was.
#
# Moving the oracle to another version is its own slice: every committed reading is re-taken on the new
# version, in the commit that moves the version and its sums.
#
# Overrides, for the witness (internal/testenv/oraclefetch_test.go) and nothing else:
#   ORACLE_ARCHIVE  a local archive to verify instead of downloading one
#   ORACLE_SUMS     a sums file to verify against
#   ORACLE_DIR      the install root (default: <repo>/.oracle)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
version=49.0.3
sums=${ORACLE_SUMS:-$root/scripts/oracle-wasmtime.sha256}
dest=${ORACLE_DIR:-$root/.oracle}

die() {
	echo "oracle-fetch: $*" >&2
	exit 1
}

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) plat=aarch64-macos ;;
Darwin-x86_64) plat=x86_64-macos ;;
Linux-x86_64) plat=x86_64-linux ;;
Linux-aarch64 | Linux-arm64) plat=aarch64-linux ;;
*) die "no wasmtime $version archive is pinned for $(uname -s)-$(uname -m); the sums cover darwin and linux only" ;;
esac
name=wasmtime-v$version-$plat.tar.xz

want=$(awk -v n="$name" '$2 == n { print $1 }' "$sums")
[ -n "$want" ] || die "$sums has no checksum for $name"

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{ print $1 }'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{ print $1 }'; }
else
	die "neither sha256sum nor shasum is installed, so the archive cannot be verified"
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/oracle-fetch.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

if [ -n "${ORACLE_ARCHIVE:-}" ]; then
	cp "$ORACLE_ARCHIVE" "$tmp/$name"
else
	url=https://github.com/bytecodealliance/wasmtime/releases/download/v$version/$name
	curl -fsSL -o "$tmp/$name" "$url" || die "download failed: $url"
fi

got=$(sha256 "$tmp/$name")
[ "$got" = "$want" ] || die "checksum mismatch for $name: got $got, the committed sum is $want. Nothing was installed."

mkdir "$tmp/x"
tar -xJf "$tmp/$name" -C "$tmp/x"
bin=$tmp/x/wasmtime-v$version-$plat/wasmtime
[ -x "$bin" ] || die "$name has no executable wasmtime-v$version-$plat/wasmtime"
ver=$("$bin" --version | awk 'NR == 1 { print $1, $2 }')
[ "$ver" = "wasmtime $version" ] || die "$name's binary reports '$ver', not 'wasmtime $version'"

# Only the binary and its license are installed. The archive's README.md would put an upstream markdown file
# inside the tree, where TestMarkdownLinksResolve and every grep would read it as this repo's own (its
# relative links name wasmtime's source tree, so the control reported them broken on the first install).
mkdir -p "$dest"
final=$dest/wasmtime-$version
rm -rf "$final.new"
mkdir "$final.new"
cp "$bin" "$final.new/wasmtime"
src=$tmp/x/wasmtime-v$version-$plat
[ ! -f "$src/LICENSE" ] || cp "$src/LICENSE" "$final.new/LICENSE"
rm -rf "$final"
mv "$final.new" "$final"
echo "oracle-fetch: installed $ver at $final/wasmtime (sha256 $got)"
