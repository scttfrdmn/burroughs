#!/usr/bin/env python3
"""Derive each preview-1 function's wasm signature from the witx, per the canonical ABI.

VALIDATED AGAINST KNOWN-GOOD before being trusted: the 26 signatures Burroughs already declares must all
agree. Two earlier versions of this script failed that check -- one skipped `string` params (builtin types
carry no `$`), the other skipped `(@witx pointer ...)` params -- and each was caught by the comparison rather
than by reading the output.

ABI: params map by type; the FIRST result ($errno) is the wasm return; ADDITIONAL results become trailing i32
out-pointers.
"""
import re, sys, json

I64 = {"filesize", "timestamp", "rights", "filedelta", "dircookie", "linkcount", "device", "inode"}
PAIR = {"string", "iovec_array", "ciovec_array"}


def scan_args(body):
    """Yield (kind, typetext) for each param/result, handling nested parens in the type."""
    i = 0
    while True:
        m = re.compile(r'\((param|result)\s+\$\S+\s+').search(body, i)
        if not m:
            return
        kind = m.group(1)
        j, depth = m.end(), 1
        while j < len(body) and depth:
            if body[j] == '(':
                depth += 1
            elif body[j] == ')':
                depth -= 1
                if depth == 0:
                    break
            j += 1
        yield kind, body[m.end():j].strip()
        i = j + 1


def wasm_of(t):
    if "pointer" in t:          # (@witx pointer X) / (@witx const_pointer X)
        return ["i32"]
    t = t.lstrip("$")
    if t in PAIR:
        return ["i32", "i32"]
    if t in I64:
        return ["i64"]
    return ["i32"]


src = open(sys.argv[1]).read()
blocks = re.findall(r'\(@interface func \(export "([a-z_0-9]+)"\)(.*?)\n  \)', src, re.S)
out = {}
for name, body in blocks:
    params, nres = [], 0
    for kind, t in scan_args(body):
        if kind == "param":
            params += wasm_of(t)
        else:
            nres += 1
    ret = ["i32"] if nres else []
    params += ["i32"] * max(0, nres - 1)
    out[name] = {"params": params, "results": ret}
json.dump(out, open(sys.argv[2], "w"), indent=0, sort_keys=True)
print(f"derived {len(out)} signatures", file=sys.stderr)
