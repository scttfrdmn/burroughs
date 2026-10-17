#!/usr/bin/env python3
"""refcite — every citation into the Canonical ABI reference names a symbol that exists at its pin.

Why this exists
---------------
Citations into ``definitions.py`` were line numbers, and a line number rots on every pin move. The move from
2bed77e to 2e7895c (#1027) remapped 269 of them by line alignment, and three of five spot checks the chair
made afterwards were wrong. The chair's ruling of 2026-10-17 made symbol citations the standing protocol
and ordered this check. A symbol survives edits around it, and when upstream renames or removes it, this
check fails on the next pin move instead of the citation quietly pointing somewhere else.

The grammar
-----------
``def`` and ``definitions.py`` are interchangeable spellings of the prefix::

    def:<symbol>                  a function, class, method or module/class-level name at CANON_PIN
    def:<symbol>:<quoted>         and the quote appears inside that symbol's source
    def:<quoted>                  the quote appears at module scope (a statement no symbol contains)
    def@<sha>:<symbol>[:<quoted>] the same, resolved at a record pin (a CANON_RECORD_PINS entry)

``<symbol>`` is dotted: ``Class.method``, ``outer.inner`` for a nested function, ``Class.ATTR``.
``<quoted>`` is a line's text between double quotes, with no double quote inside it. It is compared with
runs of whitespace collapsed, so a citation does not have to reproduce indentation. (This docstring writes
the forms with placeholders because the grammar would otherwise read its own examples as citations.)

**A pin-qualified citation is for records.** An accepted ADR or a released CHANGELOG entry states what was
true at the pin it was written against, and the symbol it read may since have been renamed upstream, so it
names that pin. Live code and tests cite unqualified, so a pin move re-asks every one of them.

What it refuses
---------------
* a line-number citation: the prefix, a colon or a pin, and then a digit;
* a symbol that does not exist at its pin;
* a quote that does not appear inside the named symbol;
* a pin that is not vendored, and a vendored reference that is absent. **An absent reference is a failure
  and never a skip**: a check that passed by not asking would be a licensed skip of the kind this repo has
  already paid for.

Usage
-----
::

    refcite.py --pin <sha> --vendor <dir> [--root <dir>] [--record-pin <sha>]...

The vendored files are ``<dir>/definitions-<sha>.py``. ``make canon-ref`` fetches them, and ``make refcite``
runs this script.
"""

import argparse
import ast
import os
import re
import subprocess
import sys

IDENT = r"[A-Za-z_][A-Za-z0-9_]*"
CITE = re.compile(
    r"(?<![A-Za-z0-9_])(?:def|definitions\.py)"
    r"(?:@(?P<pin>[0-9a-f]{7,40}))?:"
    r"(?:(?P<sym>" + IDENT + r"(?:\." + IDENT + r")*)(?::\"(?P<q1>[^\"\n]+)\")?"
    r"|\"(?P<q2>[^\"\n]+)\")"
)
LINE_FORM = re.compile(r"(?<![A-Za-z0-9_])(?:def|definitions\.py)(?:@[0-9a-f]{7,40})?:[0-9]+(?:\s*[-–]\s*[0-9]+)?")


def norm(s):
    return " ".join(s.split())


class Reference:
    def __init__(self, path):
        src = open(path, encoding="utf-8").read()
        self.lines = src.split("\n")
        self.module = norm(src)
        self.symbols = {}
        self._walk(ast.parse(src).body, "", in_function=False)

    def _walk(self, body, prefix, in_function):
        for n in body:
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                q = prefix + n.name
                start = min([d.lineno for d in n.decorator_list] + [n.lineno])
                self.symbols[q] = (start, n.end_lineno)
                self._walk(n.body, q + ".", in_function=not isinstance(n, ast.ClassDef))
            elif not in_function and isinstance(n, (ast.Assign, ast.AnnAssign)):
                for tg in n.targets if isinstance(n, ast.Assign) else [n.target]:
                    if isinstance(tg, ast.Name):
                        self.symbols[prefix + tg.id] = (n.lineno, n.end_lineno)

    def source(self, sym):
        s, e = self.symbols[sym]
        return norm("\n".join(self.lines[s - 1:e]))


def population(root):
    out = subprocess.run(["git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z"],
                         capture_output=True, check=True).stdout
    for rel in out.decode().split("\0"):
        if not rel:
            continue
        p = os.path.join(root, rel)
        if not os.path.isfile(p):
            continue  # listed by the index but deleted in the working tree
        raw = open(p, "rb").read()
        if b"\0" in raw:
            continue  # a binary: a .wasm fixture cannot carry a citation
        try:
            yield rel, raw.decode("utf-8")
        except UnicodeDecodeError:
            continue


def main(argv):
    ap = argparse.ArgumentParser(prog="refcite.py")
    ap.add_argument("--pin", required=True)
    ap.add_argument("--vendor", required=True)
    ap.add_argument("--root", default=".")
    ap.add_argument("--record-pin", action="append", default=[])
    a = ap.parse_args(argv[1:])

    refs, fails = {}, []
    for sha in [a.pin] + a.record_pin:
        path = os.path.join(a.vendor, f"definitions-{sha}.py")
        if not os.path.isfile(path):
            print(f"refcite: FAIL the reference at {sha[:12]} is not vendored ({path}). Run `make canon-ref`;"
                  " an absent reference is a failure, never a skip.", file=sys.stderr)
            return 1
        refs[sha] = Reference(path)

    def pin_for(short):
        hits = [s for s in refs if s.startswith(short)]
        return hits[0] if len(hits) == 1 else None

    n = quoted = recorded = files = 0
    for rel, text in population(a.root):
        hit = False
        for i, ln in enumerate(text.split("\n"), 1):
            for m in LINE_FORM.finditer(ln):
                hit = True
                fails.append(f"{rel}:{i}: a line-number citation ({m.group(0)}): cite a symbol, "
                             "and quote the line if the exact statement matters")
            for m in CITE.finditer(ln):
                hit = True
                n += 1
                sha = a.pin
                if m.group("pin"):
                    sha = pin_for(m.group("pin"))
                    recorded += 1
                    if sha is None:
                        fails.append(f"{rel}:{i}: {m.group(0)}: pin {m.group('pin')} is not a vendored "
                                     "record pin")
                        continue
                ref, sym, quote = refs[sha], m.group("sym"), m.group("q1") or m.group("q2")
                if sym is not None and sym not in ref.symbols:
                    fails.append(f"{rel}:{i}: {m.group(0)}: no symbol {sym!r} at {sha[:12]}")
                    continue
                if quote is not None:
                    quoted += 1
                    scope = ref.source(sym) if sym is not None else ref.module
                    if norm(quote) not in scope:
                        where = f"inside {sym}" if sym is not None else "in the module"
                        fails.append(f"{rel}:{i}: {m.group(0)}: the quote does not appear {where} at "
                                     f"{sha[:12]}")
        files += hit

    for f in fails:
        print(f"refcite: FAIL {f}", file=sys.stderr)
    print(f"refcite: {n} citation(s) in {files} file(s); {quoted} quoted, {recorded} at a record pin; "
          f"pin {a.pin[:12]}; {len(fails)} failure(s). Python {sys.version.split()[0]}.", file=sys.stderr)
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
