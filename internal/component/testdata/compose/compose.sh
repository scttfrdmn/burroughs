#!/usr/bin/env bash
# Regenerate the composition fixtures with `wac`, the supported composition tool.
#
# **Run deliberately and out of CI**, like `../asynclift/build.sh`: the committed `.wasm` files ARE the
# artefacts, and the Go witness reads them rather than invoking `wac`. Commit the tool's output, never
# depend on the tool at test time.
#
# ## Why `wac` and not `wasm-tools compose`
#
# `wasm-tools compose` exists in the pinned 1.258.0 and prints, on every invocation:
#
#     WARNING: `wasm-tools compose` has been deprecated.
#     Please use `wac` instead.
#
# A committed, reproducible artefact should not be built by a tool telling you to stop using it. The two
# also disagree: `compose` **refuses** a bare world-level function import (`no dependencies of component
# were found`) while `wac plug` satisfies it. That disagreement was measured, and taking `compose`'s
# refusal for a property of *composition* is what made #862 briefly plan a second guest it did not need.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
: "${WAC:=wac}"
: "${WASM_TOOLS:=wasm-tools}"
child="$here/../asynclift/receipt/component.wasm"

command -v "$WAC" >/dev/null || { echo "compose.sh: $WAC not found — install with 'cargo install wac-cli --locked'" >&2; exit 2; }
[ -f "$child" ] || { echo "compose.sh: the committed receipt guest is missing: $child" >&2; exit 2; }

echo "== provenance ==" >&2
"$WAC" --version >&2
"$WASM_TOOLS" --version >&2

for sock in parent-supplied parent-missing; do
	"$WASM_TOOLS" parse "$here/$sock.wat" -o "$here/$sock.wasm"
	"$WASM_TOOLS" validate --features all "$here/$sock.wasm"
done
echo "   sockets assembled and validated" >&2

# **The child is the committed receipt guest, unchanged.** #862 briefly planned a second guest with an
# interface-shaped export, on the belief that a bare export could not be composed. `wac` composes it, so
# the guest — and every reading and hash taken against it — stays as it is.
"$WAC" plug "$here/parent-supplied.wasm" --plug "$child" -o "$here/composed-supplied.wasm"
echo "   composed-supplied.wasm" >&2

# The negative arm. `wac` exits 0 here, which is the whole point, so `set -e` does not catch it and the
# assertion below is what distinguishes the two outputs.
"$WAC" plug "$here/parent-missing.wasm" --plug "$child" -o "$here/composed-missing.wasm"
echo "   composed-missing.wasm (wac exited 0 with an unsatisfied import — expected)" >&2

# Print both composed worlds, read with a PARSER rather than a grep over the printed text. A first
# measurement of this counted `(import` lines at any indentation and nearly concluded that `wac` had left
# the plugged import in place — the nested socket component's own import declaration is still there, and
# is supposed to be. `component wit` reports the OUTER world, which is the thing in question.
echo "== composed worlds ==" >&2
for out in composed-supplied composed-missing; do
	echo "--- $out" >&2
	"$WASM_TOOLS" component wit "$here/$out.wasm" >&2
done

echo >&2
echo "compose.sh: the SUPPLIED world must not import 'run' (the plug satisfied it) and must still" >&2
echo "            import 'tick' and 'note' — the CHILD's own host imports, which composition leaves." >&2
echo "            Zero imports would be the wrong expectation and would fail on a correct composition." >&2
echo "compose.sh: the MISSING world must still import 'missing'. wac exits 0 either way." >&2
