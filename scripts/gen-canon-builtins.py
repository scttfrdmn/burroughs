#!/usr/bin/env python3
"""Emit the canon built-in table from the Canonical ABI's `Binary.md` at a pinned revision.

## Why a committed table rather than a fetch at test time

`Binary.md`'s `canon` production is the authority on which canonical built-ins exist and what their opcodes
are. The witness that consumes this table (`TestEngineCanonCoverageEqualsTheSpecAtThePin`) must run with no
network and with `BURROUGHS_NO_SKIP=1`, so the table is **committed as the oracle's reading** -- the same
discipline as `spec-images` and `canon-fixtures`: fetch deliberately, commit the reading, keep the tool out
of the test path.

## What this emits, and what it deliberately does NOT

It emits only what the spec says: the opcode bytes, the canon name, and the proposal marker. **It does not
emit a status.** Burroughs' status for each built-in is authored beside the witness, in Go, because a status
generated from the engine would make the equality check compare the engine to itself -- which passes no
matter what the engine does. The pin is written into the table so the witness can refuse a pin bump that did
not regenerate it.

## Usage

    scripts/gen-canon-builtins.py <pin> [--url-base URL] > internal/component/testdata/canon-builtins.tsv

`make canon-builtins` is the intended caller and supplies the pin from `CANON_PIN`, so the pin cannot drift
between this table and the fixture generator that shares it.
"""

import argparse
import re
import sys
import urllib.request

DEFAULT_BASE = "https://raw.githubusercontent.com/WebAssembly/component-model"

# The proposal markers Binary.md hangs off each production. Mapped to ASCII slugs because the table is read
# by a Go test and an emoji is not a stable sort key -- and because 'threads' versus 'threads phase 2' is a
# distinction the witness has to make, while the glyphs differ only by a trailing circled digit.
MARKERS = {
    "\U0001f500": "async",
    "\U0001f4dd": "error-context",
    "\U0001f9f5②": "threads-phase2",  # must precede the bare thread marker: it is a prefix of this
    "\U0001f9f5": "threads",
}


def productions(text: str) -> list[tuple[str, str, str]]:
    """Parse the `canon ::=` production block into (opcodes, name, marker-slug) triples."""
    lines = text.split("\n")
    try:
        start = next(i for i, l in enumerate(lines) if l.startswith("canon "))
    except StopIteration:
        sys.exit("gen-canon-builtins: no `canon` production found — Binary.md's grammar shape changed")

    rows = []
    for line in lines[start:]:
        # The block is the `canon ::=` line plus its `|` continuations; the first line that is neither ends
        # it. Anchored on the grammar's own shape rather than a blank line, which appears inside the block.
        if not (line.startswith("canon ") or line.lstrip().startswith("|")):
            break
        m = re.search(r"=>\s*\(canon ([a-z0-9.\-]+)", line)
        if not m:
            continue
        # Opcodes are the hex bytes LEFT of the `=>`; the right-hand side can carry none. Every form is one
        # or two bytes, and a two-byte form's second byte is a sort discriminant rather than an opcode --
        # both are emitted so the witness compares what the decoder actually reads.
        lhs = (line.split("::=", 1)[1] if "::=" in line else line.lstrip()[1:]).split("=>")[0]
        ops = re.findall(r"0x[0-9a-f]{2}", lhs)
        if not ops:
            sys.exit(f"gen-canon-builtins: production {m.group(1)!r} has no opcode — cannot be tabulated")
        tail = line.split("(core func)")[-1]
        marker = ""
        for glyph, slug in MARKERS.items():
            if glyph in tail:
                marker = slug
                break
        rows.append((" ".join(ops), m.group(1), marker))

    if not rows:
        sys.exit("gen-canon-builtins: the production block parsed to zero rows")
    names = [n for _, n, _ in rows]
    if len(set(names)) != len(names):
        dupes = sorted({n for n in names if names.count(n) > 1})
        sys.exit(f"gen-canon-builtins: duplicate canon name(s) {dupes} — the name cannot be the table's key")
    firsts = [o.split()[0] for o, _, _ in rows]
    if len(set(firsts)) != len(firsts):
        dupes = sorted({o for o in firsts if firsts.count(o) > 1})
        sys.exit(f"gen-canon-builtins: two productions share first opcode byte {dupes}; the engine keys "
                 f"its built set on that byte, so the table would be ambiguous")
    return rows


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("pin", help="component-model revision to read Binary.md at")
    ap.add_argument("--url-base", default=DEFAULT_BASE)
    ap.add_argument("--from-file", help="read Binary.md from a path instead of fetching (for offline work)")
    args = ap.parse_args(argv[1:])

    if args.from_file:
        text = open(args.from_file, encoding="utf-8").read()
    else:
        url = f"{args.url_base}/{args.pin}/design/mvp/Binary.md"
        with urllib.request.urlopen(url) as fh:  # noqa: S310 — a pinned raw.githubusercontent URL
            text = fh.read().decode("utf-8")

    rows = productions(text)
    print("# Generated by scripts/gen-canon-builtins.py — do not hand-edit; run `make canon-builtins`.")
    print("# Every canonical built-in in Binary.md's `canon` production, at the pin below. Spec facts only:")
    print("# Burroughs' status for each is authored in canonbuiltins_test.go, so the equality witness that")
    print("# compares the two is not comparing the engine to itself.")
    print(f"pin\t{args.pin}")
    print("# opcodes\tname\tmarker")
    for ops, name, marker in rows:
        print(f"{ops}\t{name}\t{marker}")
    print(f"# {len(rows)} productions", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
