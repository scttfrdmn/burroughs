#!/usr/bin/env bash
# Rebuild the 🧵 phase-1 switching fixture and re-take its wasmtime reading (ADR 0100).
#
# **Run deliberately and out of CI.** `switch.wasm` and `switch.reading` ARE the artefacts; this script
# exists so they can be regenerated when a tool moves, the same arrangement `asynclift/build.sh` has.
# Nothing on the test path invokes wasmtime.
#
# ## Why this fixture is hand-authored WAT, and what it costs to not be
#
# No toolchain emits the 🧵 built-ins. `wasm-tools` 1.258.0 knows them in its text format, so a component
# that exercises all eight phase-1 productions is authorable directly and needs **no Rust toolchain** —
# which is why this directory has no `Cargo.lock` and `asynclift/`'s two-toolchain problem does not reach
# it.
#
# ## What the fixture does, and why the mask is the witness
#
# The entry Thread records its own index, then creates five Threads and reaches each of them through a
# different switching production. Each body ORs one bit into `mem[0]` **and writes its own
# `thread.index`** to its own slot. **A body can only run if its switch happened**, so the mask is
# positive evidence per production rather than a claim that the call did not trap; the indices then pin
# what the productions returned, which a reachability bit cannot.
#
# **The order of execution is deliberately not asserted.** `Store.tick` picks `random.choice` from the
# ready set, so an order is not a property of a conforming implementation. The mask is an OR and each
# index is written by its own body, so the asserted value is order-independent by construction.
#
# Two table slots, and the split is load-bearing. The `yield-*` forms leave the switcher **ready**, and
# `thread.resume-later` traps on a target that is not suspended — so a body reached by a `yield-*` switch
# must NOT resume the switcher, while a body reached by a `suspend-*` switch must. Slot 0 marks only; slot
# 1 marks and resumes.
#
# ## Three fixtures that did not work, recorded so they are not re-paid for
#
#   * A callback returning YIELD unconditionally **hangs**: the event loop re-enters it forever. The hang
#     is the fixture's, not the engine's.
#   * A callee that never calls `task.return` traps with `async-lifted export failed to produce a result`.
#   * A `peek()` export cannot observe a previous call's memory: each `wasmtime run` instantiates afresh.
#     The mask has to come back through `task.return`, which is why it does.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
: "${WASM_TOOLS:=wasm-tools}"
: "${WASMTIME:=wasmtime}"
for t in "$WASM_TOOLS" "$WASMTIME"; do
	command -v "$t" >/dev/null || { echo "build.sh: $t not found" >&2; exit 2; }
done

echo "== provenance ==" >&2
"$WASM_TOOLS" --version >&2
"$WASMTIME" --version >&2

"$WASM_TOOLS" parse "$here/switch.wat" -o "$here/switch.wasm"
echo "build.sh: switch.wasm is $(wc -c <"$here/switch.wasm" | tr -d ' ') bytes" >&2

# The reading is assembled rather than captured, because three channels go into it: the run with the
# gate on, the refusal with it off, and this engine's own refusal. Each is taken here.
on=$("$WASMTIME" run -W component-model-threading=y -W component-model-async=y \
	--invoke 'run(0)' "$here/switch.wasm" 2>&1) || {
	echo "build.sh: the gate-on run FAILED, so there is no reading to take:" >&2
	echo "$on" >&2
	exit 1
}
echo "build.sh: gate-on result $on" >&2

# **Asserted whole, not eyeballed, and not just the mask.** The packed value carries the six marker bits
# AND six 4-bit thread.index values, so this check pins what each production RETURNED and not merely that
# it was reached: a run that set every marker while numbering Threads monotonically would give a different
# figure here. See switch.reading for the layout and for which properties are pinned.
[ "$on" = "143165567" ] || {
	echo "build.sh: expected 143165567 (0x0888887f), got '$on'." >&2
	echo "          Decode: mask = value & 0x3f (want 0x3f); index k = (value >> (6 + 4k)) & 0xf," >&2
	echo "          entry at k=0 (want 1) then the five bodies in creation order (want 2 each)." >&2
	echo "          A mask short of 0x3f is a switch that did not happen; a changed index is the" >&2
	echo "          upstream's Thread numbering having moved." >&2
	exit 1
}

# The same fixture with the gate off, which must be a refusal naming a production.
off=$("$WASMTIME" run -W component-model-threading=n -W component-model-async=y \
	--invoke 'run(0)' "$here/switch.wasm" 2>&1 || true)
case $off in
*"requires the component model threading feature"*) : ;;
*)
	echo "build.sh: the gate-off run did not refuse by name. Got:" >&2
	echo "$off" >&2
	exit 1
	;;
esac
echo "build.sh: gate-off refusal carries the feature name" >&2

# ## index-call: the single-production fixture ADR 0100's first build slice needs
#
# `switch.wasm` uses all eight phase-1 forms, so Burroughs cannot run it until all eight are modeled.
# One form lands at a time (ADR 0100 point 4), so the slice that models `thread.index` needs a fixture
# using only that, with its own reading. This is it.
"$WASM_TOOLS" parse "$here/index-call.wat" -o "$here/index-call.wasm"
echo "build.sh: index-call.wasm is $(wc -c <"$here/index-call.wasm" | tr -d ' ') bytes" >&2

ic=$("$WASMTIME" run -W component-model-threading=y --invoke 'idx()' "$here/index-call.wasm" 2>&1) || {
	echo "build.sh: the index-call run FAILED:" >&2
	echo "$ic" >&2
	exit 1
}
# Asserted: the entry Thread's index is 1, which is the reference's reserved-zero Table policy showing
# through. A different figure here is the upstream's numbering having moved, not a formatting change.
[ "$ic" = "1" ] || {
	echo "build.sh: index-call expected 1 (the entry Thread's index), got '$ic'." >&2
	echo "          The reference reserves index 0, so the first allocation is 1." >&2
	exit 1
}
echo "build.sh: index-call result $ic" >&2

# ## spawn: the three-production fixture ADR 0100's second build slice needs
#
# thread.index + thread.new-indirect + thread.resume-later, and nothing else. The entry Thread yields
# rather than returning, so it is still registered while the spawned Thread runs — which is what makes
# this the first fixture able to measure CONCURRENT distinct indices.
"$WASM_TOOLS" parse "$here/spawn.wat" -o "$here/spawn.wasm"
echo "build.sh: spawn.wasm is $(wc -c <"$here/spawn.wasm" | tr -d ' ') bytes" >&2

sp=$("$WASMTIME" run -W component-model-threading=y -W component-model-async=y \
	--invoke 'run(0)' "$here/spawn.wasm" 2>&1) || {
	echo "build.sh: the spawn run FAILED:" >&2
	echo "$sp" >&2
	exit 1
}
# Asserted whole: marker in bit 0, entry index 1 in bits 4-7, body index 2 in bits 8-11. A changed
# figure is the upstream's Thread numbering or scheduling having moved, not a formatting change.
[ "$sp" = "529" ] || {
	echo "build.sh: spawn expected 529 (0x211), got '$sp'." >&2
	echo "          Decode: marker = v & 1 (want 1); entry = (v >> 4) & 0xf (want 1);" >&2
	echo "          body = (v >> 8) & 0xf (want 2). A body index of 1 is the entry's slot having" >&2
	echo "          been freed at the yield, which is two Threads sharing one index." >&2
	exit 1
}
echo "build.sh: spawn result $sp" >&2

echo "build.sh: switch.reading is hand-assembled from the three channels above — update it by hand" >&2
echo "          when a figure here moves, and say which channel moved." >&2
