#!/usr/bin/env python3
"""subst1 — replace EXACTLY ONE occurrence in a file, or fail.

Why this exists
---------------
Four times in one day a scripted edit changed nothing and the result was reported as a measurement. The
shape was always the same::

    s = open(p).read()
    s = s.replace(OLD, NEW)      # no match -> returns s unchanged, no error
    open(p, 'w').write(s)        # writes the original back

`str.replace` on a miss is a silent no-op, so the file is rewritten identically, the subject passes, and the
conclusion drawn is the opposite of the truth. The worst instance built a multi-round narrative about a
"lost" section that had never been written.

The first choice is not this script
-----------------------------------
For ordinary edits, use the editor tool's ``str_replace``, which fails loudly when its target is not found.
This helper is for the case where a scripted edit is genuinely unavoidable — a generated file, a loop over
many files, a step inside a shell pipeline.

What it refuses
---------------
* **zero matches** — the anchor is wrong, and a rewrite-identical is the defect above;
* **more than one match** — the edit is ambiguous, and picking the first silently is how an injection lands
  in the wrong one of three identical call sites (that happened too, and only a compile error caught it);
* **an unchanged result** — belt and braces: if OLD == NEW the file is untouched and saying so is cheaper
  than wondering later.

Old and new text come from FILES, not from argv or a shell string, because two of the four losses were shell
quoting accidents — a ``\\$`` inside single quotes, and a backtick executed away. A quoted heredoc writing a
temp file has no expansion layer at all.

Usage
-----
::

    cat > /tmp/old <<'EOF'
    ...the exact text to find...
    EOF
    cat > /tmp/new <<'EOF'
    ...the text to put there...
    EOF
    python3 scripts/subst1.py TARGET /tmp/old /tmp/new

Exit status is 0 only when exactly one occurrence was replaced, and the count is printed either way.
"""

import sys


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print(f"usage: {argv[0]} <target> <old-file> <new-file>", file=sys.stderr)
        return 2
    target, oldf, newf = argv[1], argv[2], argv[3]

    with open(target, encoding="utf-8") as fh:
        body = fh.read()
    with open(oldf, encoding="utf-8") as fh:
        old = fh.read()
    with open(newf, encoding="utf-8") as fh:
        new = fh.read()

    # A trailing newline in a heredoc-written anchor is almost never wanted, and its presence or absence is
    # invisible in a terminal — which is exactly the class of difference that makes an anchor miss.
    old = old.rstrip("\n")
    new = new.rstrip("\n")

    if not old:
        print("subst1: FAIL the old text is empty", file=sys.stderr)
        return 1

    n = body.count(old)
    if n == 0:
        print(
            "subst1: FAIL 0 matches — the anchor is not in the file, so a replace here would rewrite it\n"
            "        identically and report success. First line sought:\n"
            f"          {old.splitlines()[0][:100]!r}",
            file=sys.stderr,
        )
        return 1
    if n > 1:
        print(
            f"subst1: FAIL {n} matches — the edit is ambiguous. Picking the first silently is how an\n"
            "        injection lands in the wrong one of several identical sites. Extend the anchor.",
            file=sys.stderr,
        )
        return 1

    out = body.replace(old, new, 1)
    if out == body:
        print("subst1: FAIL the replacement left the file unchanged (old and new are identical)", file=sys.stderr)
        return 1

    with open(target, "w", encoding="utf-8") as fh:
        fh.write(out)
    print(f"subst1: replaced 1 occurrence in {target}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
