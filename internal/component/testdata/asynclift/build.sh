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
: "${WASMTIME:=wasmtime}"      # the CLI, for the single guest's value reading (#864)

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

# The cancellation parents (#862, CANCELLATION.md). TWO parents cancelling ONE child, because issuing a
# cancellation and observing its status are different capabilities and `wit-bindgen` has only the first:
#   * the RUST parent is what a real guest does — drop an in-flight async import — and sees no status;
#   * the WAT parent calls `subtask.cancel` by hand and reports the numeric status it returns.
# Their agreement on every shared fact is what licenses the WAT parent's number (coverage split in
# CANCELLATION.md). The child is the committed `receipt/` guest, UNCHANGED.
echo "== cancel-rust-parent ==" >&2
rm -f "$here/cancel-rust-parent/component.wasm"
core=$( cd "$here/cancel-rust-parent" &&
	RUSTC="$RUSTUP_RUSTC" CARGO_TARGET_DIR="$here/cancel-rust-parent/target" \
		"$RUSTUP_CARGO" build --target wasm32-unknown-unknown --release --message-format=json |
		emit_artifact .wasm )
[ -n "$core" ] || { echo "build.sh: cargo reported no core module for cancel-rust-parent" >&2; exit 1; }
"$WASM_TOOLS" component new "$core" -o "$here/cancel-rust-parent/component.wasm"
echo "   wrote cancel-rust-parent/component.wasm from $core" >&2

echo "== cancel-wat-parent ==" >&2
"$WASM_TOOLS" parse "$here/cancel-wat-parent/parent.wat" -o "$here/cancel-wat-parent/parent.wasm"
"$WASM_TOOLS" validate --features all "$here/cancel-wat-parent/parent.wasm"
echo "   assembled cancel-wat-parent/parent.wasm" >&2

# The COMPLETING parent (#888). Every other composed artefact here cancels, which left the
# cross-component call itself unwitnessable — see call-wat-parent/parent.wat's header.
echo "== call-wat-parent ==" >&2
"$WASM_TOOLS" parse "$here/call-wat-parent/parent.wat" -o "$here/call-wat-parent/parent.wasm"
"$WASM_TOOLS" validate --features all "$here/call-wat-parent/parent.wasm"
echo "   assembled call-wat-parent/parent.wasm" >&2

echo "== harness ==" >&2
bin=$( cd "$here/harness" &&
	RUSTC="$HOST_RUSTC" CARGO_TARGET_DIR="$here/harness/target" \
		"$HOST_CARGO" build --release --message-format=json | emit_artifact concwitness )
[ -n "$bin" ] || { echo "build.sh: concwitness was not built" >&2; exit 1; }

echo "== readings ==" >&2
"$bin" concurrent "$here/suspending/component.wasm" > "$here/concurrent.reading" 2>&1 || true
"$bin" sequential "$here/suspending/component.wasm" > "$here/sequential.reading" 2>&1 || true
cat "$here/concurrent.reading" "$here/sequential.reading" >&2

# The INLINE arm (#870): `tick` resolves at once with `id + 100`. Every other reading here uses the
# rendezvous `tick`, which DEFERS — so when Burroughs first ran this guest with an inline host impl, its
# output had nothing to check against. A number with no reference is not evidence.
#
# Two calls, because one cannot tell a passthrough or a constant from a real `id + 100` — the same reason
# `compute.reading` commits two pairs.
"$bin" inline "$here/suspending/component.wasm" > "$here/inline.reading" 2>&1 || true
cat "$here/inline.reading" >&2
grep -q 'run(1)=Ok((101,)) run(2)=Ok((102,))' "$here/inline.reading" || {
	echo "build.sh: the inline reading did not capture both results — a reading that records neither" >&2
	echo "          value is indistinguishable from one that was never taken." >&2
	exit 1
}

# The single guest's VALUE reading (#864), captured through the wasmtime CLI rather than the harness: the
# question is what a value-carrying async-lifted export returns, and the CLI answers it without a bespoke
# embedding. Three arms, and the third is what makes the first two mean anything.
echo "== compute reading ==" >&2
{
	echo "# wasmtime $("$WASMTIME" --version | awk '{print $2, $3, $4}') reading of the single guest's async-lifted export."
	echo "# Regenerated by build.sh; never hand-edited. See README.md."
	echo
	echo "## Positive arms. TWO pairs, because one cannot discriminate: a guest that returned its argument"
	echo "## would read 5 -> 5, and one that returned a constant would read the same value twice."
	for v in 5 41; do
		printf 'compute(%s) -> %s\n' "$v" "$("$WASMTIME" run --invoke "compute($v)" "$here/single/component.wasm" 2>&1 | tail -1)"
	done
	echo
	echo "## The negative arm. Without it the arms above are consistent with a SYNC fallback path, and a"
	echo "## Burroughs result matching them would say nothing about the async tier. Async is DEFAULT-ON in"
	echo "## wasmtime 49, so the positive arms need no flag and this one needs an explicit disable."
	printf 'compute(5) with -W component-model-async=n -> REFUSED: %s\n' \
		"$("$WASMTIME" run -W component-model-async=n --invoke 'compute(5)' "$here/single/component.wasm" 2>&1 |
			grep -oE '`task\.return` requires[^)]*\)')"
} > "$here/compute.reading"
cat "$here/compute.reading" >&2
# The refusal text must be PRESENT. The first capture used `tail -1` and recorded an empty string, because
# wasmtime's error ends with a blank line — a negative arm that silently records nothing is indistinguishable
# from one that was never run.
grep -q 'REFUSED: `task.return` requires' "$here/compute.reading" || {
	echo "build.sh: the compute reading's negative arm captured no refusal text." >&2
	exit 1
}

# The abandonment arms (#857, ABANDONMENT.md). Both are cut from the receipt guest by
# `scripts/stripcall.py`, which refuses on anything but exactly one matched call, requires the edited
# module to VALIDATE, requires the bytes to change, and requires the committed diff to be +0/-1.
#
# **Both arms go through the round-trip**, because `wasm-tools print`/`parse` is not byte-identical
# (measured: a2600925… -> bb766fec… on a no-op round-trip). Comparing a committed guest against an
# edited one would differ by the edit AND by normalization; comparing round-tripped against
# round-tripped-and-edited leaves the deleted call as the only variable.
# The composed cancellation readings (#862). Each parent is plugged into the SAME child with `wac`, the
# pinned composition tool — see ../compose/README.md for why `wasm-tools compose` is not used.
echo "== composed cancellation ==" >&2
: "${WAC:=wac}"
command -v "$WAC" >/dev/null || { echo "build.sh: $WAC not found — 'cargo install wac-cli --locked'" >&2; exit 2; }
"$WAC" --version >&2
# `call-wat-parent` rides this loop rather than getting its own: it is plugged into the SAME child by the
# SAME tool, and the whole point of #862's two-parent arrangement is that the child is not a variable.
# A second composition step would be a second place for the child to drift.
for p in cancel-wat-parent cancel-rust-parent call-wat-parent; do
	sock="$here/$p/parent.wasm"
	[ -f "$sock" ] || sock="$here/$p/component.wasm"
	"$WAC" plug "$sock" --plug "$here/receipt/component.wasm" -o "$here/$p/composed.wasm"
	echo "   composed $p/composed.wasm" >&2
done
"$bin" cancel-wat "$here/cancel-wat-parent/composed.wasm" > "$here/cancel-wat.reading" 2>&1 || true
"$bin" cancel-rust "$here/cancel-rust-parent/composed.wasm" > "$here/cancel-rust.reading" 2>&1 || true
cat "$here/cancel-wat.reading" "$here/cancel-rust.reading" >&2

# **The two readings must AGREE on every shared fact**, which is what licenses the WAT parent's status as
# what a real canceller would have seen. Asserted here rather than left to the eye: a disagreement is the
# finding, and a silent one would let the status be trusted when it should not be.
for fact in 'ARRIVALS  1' 'RECEIPT   observed, code=1' 'TICKDROP  true'; do
	grep -q "^$fact" "$here/cancel-wat.reading" && grep -q "^$fact" "$here/cancel-rust.reading" || {
		echo "build.sh: the two cancellation readings DISAGREE on '$fact'." >&2
		echo "          Report that as the finding — do not derive anything from the status." >&2
		exit 1
	}
done
# And the order: the pending host call is torn down BEFORE the guest's cancellation path runs.
for r in cancel-wat cancel-rust; do
	grep -q "tick-dropped -> receipt(1)" "$here/$r.reading" || {
		echo "build.sh: $r.reading no longer shows tick-dropped before receipt — the ORDER is part of" >&2
		echo "          the finding, not an incidental detail." >&2
		exit 1
	}
done
grep -q "^STATUS  4 (CANCELLED_BEFORE_RETURNED)" "$here/cancel-wat.reading" || {
	echo "build.sh: the WAT canceller no longer reports status 4; re-derive CANCELLATION.md rather than" >&2
	echo "          editing its assertions." >&2
	exit 1
}
echo "   both readings agree on every shared fact" >&2

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
