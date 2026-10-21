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
* a line number glued to a symbol citation (a slash and a number, or an ``and :`` and a number, straight
  after it), which the conversion from line numbers left three times and which resolves on its own;
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
import glob
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
# A line number left glued to a symbol citation: `<citation>/<n>`, or `<citation> and :<n>`. The conversion
# from line numbers produced both, and both resolve, because the citation before the residue is well-formed.
GLUED = re.compile(r"(?:/|\s+(?:and|or)\s+:)[0-9]{2,4}\b")
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
    ap.add_argument("--negative-claims", default=None)
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
                g = GLUED.match(ln, m.end())
                if g:
                    fails.append(f"{rel}:{i}: {m.group(0)}{g.group(0)}: a line number glued to a symbol "
                                 "citation: cite the second statement as its own symbol or quote")
                    continue
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

    claims = None
    if a.negative_claims is not None:
        claims = negative_claims(a.negative_claims, a.root, a.pin, fails)

    for f in fails:
        print(f"refcite: FAIL {f}", file=sys.stderr)
    print(f"refcite: {n} citation(s) in {files} file(s); {quoted} quoted, {recorded} at a record pin; "
          f"pin {a.pin[:12]}; {len(fails)} failure(s). Python {sys.version.split()[0]}.", file=sys.stderr)
    if claims is not None:
        print(f"refcite: {claims} negative claim(s) in {a.negative_claims}, each re-read at {a.pin[:12]}.",
              file=sys.stderr)
    return 1 if fails else 0


def negative_claims(path, root, pin, fails):
    """Check the list of negative claims about the reference is kept (the chair's third ruling of 2026-10-20).

    A negative claim ("the reference does not…") names no symbol, so the citation check above cannot see it rot:
    ADR 0100's no-poison premise was true at 2bed77e and false at 2e7895c, and nothing fired. The list is the
    population a pin move must re-read. Each row is checked for three things:

    * its quote still appears in its ADR, so a claim edited away takes its row with it;
    * its ``read_at`` pin is the current pin, so **a pin move fails this check until every row is re-read**;
    * a ``false`` verdict names an issue, because a false premise is a defect someone must own.

    What it cannot do is find a negative claim nobody listed. That half is the sweep's, and is stated rather than
    claimed."""
    full = os.path.join(root, path)
    if not os.path.isfile(full):
        fails.append(f"{path}: the negative-claims list is missing")
        return 0
    rows = 0
    for i, line in enumerate(open(full, encoding="utf-8").read().split("\n"), 1):
        if not line.strip() or line.startswith("#"):
            continue
        cols = line.split("\t")
        if len(cols) != 6:
            fails.append(f"{path}:{i}: want 6 tab-separated columns (adr, quote, read_at, verdict, issue, note), "
                         f"got {len(cols)}")
            continue
        rows += 1
        adr, quote, read_at, verdict, issue, _ = cols
        hits = glob.glob(os.path.join(root, "docs", "decisions", adr + "-*.md"))
        if len(hits) != 1:
            fails.append(f"{path}:{i}: ADR {adr} names {len(hits)} file(s) in docs/decisions, want 1")
            continue
        if norm(quote) not in norm(open(hits[0], encoding="utf-8").read()):
            fails.append(f"{path}:{i}: the quote is no longer in ADR {adr}: {quote[:80]!r}")
        if read_at != pin:
            fails.append(f"{path}:{i}: ADR {adr}'s claim was last read at {read_at[:12]}, and the pin is "
                         f"{pin[:12]}: re-read it at the new pin, then set read_at")
        if verdict not in ("holds", "false"):
            fails.append(f"{path}:{i}: verdict {verdict!r}, want holds or false")
        elif verdict == "false" and not issue.strip().isdigit():
            fails.append(f"{path}:{i}: ADR {adr}'s claim is false and names no issue")
    return rows


if __name__ == "__main__":
    sys.exit(main(sys.argv))
