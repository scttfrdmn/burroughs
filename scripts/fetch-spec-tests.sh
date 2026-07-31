#!/usr/bin/env sh
# Vendor the upstream Wasm spec test suite (the oracle — contract §9).
# Gitignored; never committed.
set -e
dest="testdata/spec"
if [ -d "$dest/.git" ]; then
  git -C "$dest" pull --ff-only
else
  git clone --depth 1 https://github.com/WebAssembly/testsuite "$dest"
fi
echo "spec suite vendored at $dest ($(ls "$dest"/*.wast 2>/dev/null | wc -l) .wast files)"
