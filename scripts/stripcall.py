#!/usr/bin/env python3
"""Delete the single `task.cancel` call from a component, producing the cancellation witness's negative arm.

## What this is for

The negative arm of #857's cancellation witness is a guest that receives the cancellation and then
completes **without** calling `task.cancel` or `task.return`. Under the Canonical ABI that is a protocol
violation, and wasmtime refuses it with `Trap::NoAsyncResult` ("async-lifted export failed to produce a
result"). The arm exists to show the discriminator can fail — a positive arm whose negative twin also
passes has measured nothing.

## Why an edited binary rather than a second guest

Cutting both arms from **one** guest keeps the comparison to a single variable. A hand-written WAT guest,
or a Rust variant with the guard suppressed, would differ from the positive arm in the source, the
toolchain, and the receipt import — and then a difference in the reading could not be attributed to the
missing `task.cancel`.

**And the round-trip is applied to BOTH arms**, because `wasm-tools print` + `parse` is not byte-identical:
measured on the suspending guest, `a2600925…` becomes `bb766fec…` through a no-op round-trip. Comparing the
committed guest against an edited one would therefore differ by the edit *and* by normalization. So the
positive arm is the round-tripped guest and the negative arm is the round-tripped-and-edited guest.

## Why the symbol is derived and never written down

The call targets a Rust-mangled symbol whose hash suffix changes with the crate name and the compiler
(`…TaskCancelOnDrop…drop6cancel17h11288b8e1d1d4b6aE`). Hard-coding it would make this script correct
exactly once — the defect this project has a law for. So the symbol is read from the `[task-cancel]`
import declaration in the same file, and the call is matched against it.

## Three refusals, each closing a way a scripted edit passes while doing nothing

  1. **Not exactly one import, or not exactly one call** — `subst1.py`'s rule. A zero-match edit is a
     silent no-op; a two-match edit is a different change than the one intended.
  2. **The edited module must validate.** The import's type is `(func)` — no params, no results — so
     deleting the call *should* be stack-neutral. "Should" is an argument; `wasm-tools validate` is a
     measurement.
  3. **The bytes must actually differ** from the unedited round-trip. An edit that leaves the output
     identical has not happened, however confidently the script reports success.

## Usage

    scripts/stripcall.py <component.wasm> --out-dir <dir>

Writes, into the output directory: `roundtrip.wasm` (the positive arm), `stripped.wasm` (the negative arm),
and `stripped.wat.diff` — the committed one-line diff, so a reader can see the single change without
running a Rust toolchain or `wasm-tools`.
"""

import argparse
import difflib
import hashlib
import pathlib
import re
import shutil
import subprocess
import sys


def run(*argv: str) -> bytes:
    """Run a tool, failing loudly. A tool that is absent must not read as a tool that found nothing."""
    try:
        p = subprocess.run(argv, capture_output=True, check=False)
    except FileNotFoundError:
        sys.exit(f"stripcall: {argv[0]} is not on PATH; this script cannot do its own job without it")
    if p.returncode != 0:
        sys.exit(f"stripcall: {' '.join(argv)} failed (exit {p.returncode}):\n{p.stderr.decode()}")
    return p.stdout


def sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("component", type=pathlib.Path)
    ap.add_argument("--out-dir", type=pathlib.Path, required=True)
    ap.add_argument("--wasm-tools", default="wasm-tools")
    args = ap.parse_args(argv[1:])

    out = args.out_dir
    out.mkdir(parents=True, exist_ok=True)
    wt = args.wasm_tools

    # --- the positive arm: a plain round-trip, so normalization is common to both arms ---------------
    wat = run(wt, "print", str(args.component)).decode()
    rt_wat = out / "roundtrip.wat"
    rt_wat.write_text(wat)
    rt_wasm = out / "roundtrip.wasm"
    run(wt, "parse", str(rt_wat), "-o", str(rt_wasm))
    run(wt, "validate", str(rt_wasm))

    # --- locate the import, and DERIVE the symbol the call must name --------------------------------
    lines = wat.split("\n")
    imports = [
        (i, m.group(1))
        for i, l in enumerate(lines)
        if '"[task-cancel]"' in l and "(import" in l
        for m in [re.search(r"\(func (\$[^\s)]+)", l)]
        if m
    ]
    if len(imports) != 1:
        sys.exit(
            f"stripcall: found {len(imports)} `[task-cancel]` import declaration(s), want exactly 1.\n"
            f"           The symbol is derived from that declaration, so this script cannot aim without it."
        )
    _, symbol = imports[0]

    call_idx = [i for i, l in enumerate(lines) if l.strip() == f"call {symbol}"]
    if len(call_idx) != 1:
        sys.exit(
            f"stripcall: found {len(call_idx)} call(s) to {symbol}, want exactly 1.\n"
            f"           Zero means the guest never calls task.cancel and the negative arm is already\n"
            f"           the positive one; more than one means deleting a single call changes less than\n"
            f"           the whole cancellation path, and the arm would not be the control it claims."
        )

    # --- the edit ------------------------------------------------------------------------------------
    stripped = lines[: call_idx[0]] + lines[call_idx[0] + 1 :]
    st_wat = out / "stripped.wat"
    st_wat.write_text("\n".join(stripped))
    st_wasm = out / "stripped.wasm"
    run(wt, "parse", str(st_wat), "-o", str(st_wasm))

    # Refusal 2: the edited module must VALIDATE, not merely assemble.
    run(wt, "validate", str(st_wasm))

    # Refusal 3: the bytes must differ from the unedited round-trip.
    if sha(st_wasm) == sha(rt_wasm):
        sys.exit(
            "stripcall: the stripped module is byte-identical to the round-trip, so the edit did not\n"
            "           happen. Reporting success here would hand the witness two identical arms."
        )

    # --- the committed artifact a reader can check without any tool ---------------------------------
    diff = difflib.unified_diff(
        lines, stripped, fromfile="roundtrip.wat", tofile="stripped.wat", lineterm="", n=3
    )
    (out / "stripped.wat.diff").write_text("\n".join(diff) + "\n")

    added = sum(1 for l in (out / "stripped.wat.diff").read_text().split("\n") if l.startswith("+") and not l.startswith("+++"))
    removed = sum(1 for l in (out / "stripped.wat.diff").read_text().split("\n") if l.startswith("-") and not l.startswith("---"))
    if (added, removed) != (0, 1):
        sys.exit(
            f"stripcall: the diff is +{added}/-{removed}, want +0/-1. The negative arm must differ from\n"
            f"           the positive one by exactly the deleted call and nothing else."
        )

    print(f"stripcall: symbol derived from the import: {symbol}", file=sys.stderr)
    print(f"stripcall: deleted the single call at wat line {call_idx[0] + 1}", file=sys.stderr)
    print(f"stripcall: roundtrip {sha(rt_wasm)[:12]}  stripped {sha(st_wasm)[:12]}", file=sys.stderr)
    print("stripcall: both modules validate; diff is +0/-1", file=sys.stderr)
    shutil.rmtree(out / "__pycache__", ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
