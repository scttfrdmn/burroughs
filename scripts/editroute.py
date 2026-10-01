#!/usr/bin/env python3
"""editroute — a PreToolUse hook that refuses to edit a TRACKED file from a Bash command.

Why this exists
---------------
`scripts/subst1.py` already existed, and its own docstring already told this story: a scripted
``s.replace(OLD, NEW)`` that misses is a silent no-op, the file is rewritten identically, and the
conclusion drawn is the opposite of the truth. The rule that followed was: **ordinary edits go
through the editor tool, which fails loudly when its anchor is not found, or through `subst1.py`
when a scripted edit is genuinely unavoidable.**

That rule was then broken in **all 8** writes to one script in one campaign, and the eighth lost an
edit whose mechanism could not afterwards be determined — because a write whose success is never
read back leaves no evidence of its own failure. A rule ignored eight times in a row is being
remembered, not enforced. This hook enforces it.

What it refuses, and what it does not
-------------------------------------
The subject is **a tracked file being edited from a Bash command**. Three routes:

* an **inline interpreter** (`python3 - <<EOF`, `python3 -c`, `perl -e`, …) that opens a file for
  writing;
* **`sed -i`** (and `perl -i`), whose in-place edit has the same silent-miss shape;
* **shell redirection** into a tracked path — `>`, `>>`, or `tee`.

It does **not** refuse creating a new file. The defect is a silent no-op *on a miss*, which requires
an anchor, which requires the file to already exist. `cat > newfile` has no anchor to miss, so
there is nothing for the loud route to protect.

It also does not refuse a **tool whose job is to rewrite tracked files** — `make fmt` running
`gofumpt`, a code generator, `git checkout`. Those are not anchored replacements: there is no OLD
text that can fail to match, so the silent-miss shape is absent and the loud route would protect
nothing. The subject is a *hand-written edit expressed as a shell command*, and keeping that
boundary explicit is what stops the hook from growing into a general ban on writing.

A deny here is **not** a claim the command is wrong. It is a claim that the command cannot report its
own failure, which is a different and much cheaper thing to be sure of.

Exemptions are by explicit path, never by pattern
-------------------------------------------------
There is **one** predicate — `git ls-files` — and one explicit exemption, `subst1.py`'s own
invocation. Everything the ruling named as an exemption (`/tmp`, `/home/claude`, build outputs)
falls out of the predicate rather than being listed beside it: a build output is gitignored, hence
untracked, hence not the subject. That is strictly better than a list, because a list of exempt
patterns is a second place for the truth to live, and the one here would have had to be kept in step
with `.gitignore` by hand.

It parses, it does not grep
---------------------------
Standing property 21: *a check that greps will eventually match its own documentation.* So heredoc
bodies are lifted out by reading the delimiter, and the remaining command is tokenised with
`shlex` using `punctuation_chars`, which reports `>` as a redirection only when it is unquoted —
`echo "a > b"` is a word, not a write.

**This file's own prose is not in that matcher's domain**, because the matcher reads a command from
the hook's stdin and never a file in the tree. The class of defect property 21 names therefore
cannot arise here, which is worth stating rather than assuming: it is the first check in this tree
whose subject is not a file.

The known gap, stated rather than filed
---------------------------------------
A command `shlex` cannot tokenise at all (unbalanced quotes) is **allowed**, with a warning on
stderr. Failing closed there would block commands whose only problem is that this hook cannot read
them, and failing closed *selectively* would mean grepping the raw text for a tracked path — the
exact thing property 21 forbids. `TestEditRouteHook` pins this behaviour as a deliberate arm, so a
reader can price the gap instead of discovering it.

Protocol
--------
stdin is the PreToolUse JSON payload. Exit 0 allows; **exit 2 denies and the stderr text is what
the agent is told.** So the message names the two permitted routes, because a refusal that does not
say what to do instead is how an agent learns to work around a check rather than through it.
"""

import json
import os
import re
import shlex
import subprocess
import sys

# Interpreters whose program text can arrive inline, on `-c`/`-e` or through a heredoc.
INTERPRETERS = {"python", "python3", "perl", "ruby", "node", "php", "uv"}

# A file opened for writing. The mode is matched as a *mode* — a quote, then mode letters of which
# at least one is w/a/x/+ — so `open(p, encoding="utf-8")` and `open(p, "rt")` do not match: 'u' and
# 'r','t' alone are not write modes.
WRITE_OPEN = re.compile(
    r"""
      open\s*\(            [^)]*? ['"][rwaxbt+]*[wax+][rwaxbt+]*['"]   # python / ruby
    | \.\s*write_text\s*\(                                             # pathlib
    | \.\s*write_bytes\s*\(
    | os\s*\.\s*(?:replace|rename)\s*\(
    | shutil\s*\.\s*(?:copy\w*|move)\s*\(
    | File\s*\.\s*(?:write|open)\s*\(                                  # ruby
    | fs\s*\.\s*(?:writeFile|appendFile|renameSync|writeFileSync)      # node
    """,
    re.VERBOSE,
)

# Quoted string literals inside an inline program, as candidate paths.
LITERAL = re.compile(r"""['"]([^'"\n`$]{1,256})['"]""")

HEREDOC = re.compile(r"""<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)\1""")

ALLOWED_ROUTES = (
    "    - the editor tool (str_replace), which FAILS LOUDLY when its anchor is not found; or\n"
    "    - python3 scripts/subst1.py <target> <old-file> <new-file>, which refuses 0 matches,\n"
    "      refuses >1 match, and refuses a replacement that leaves the file unchanged.\n"
)


def repo_root() -> str:
    root = os.environ.get("CLAUDE_PROJECT_DIR")
    if root and os.path.isdir(os.path.join(root, ".git")):
        return root
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, timeout=10, check=False,
        )
        if out.returncode == 0:
            return out.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        pass
    return ""


_tracked: set[str] | None = None


def tracked_set(root: str) -> set[str]:
    """Absolute paths of every tracked file. Computed at most once, and lazily — most Bash commands
    never reach a write candidate, and they should not pay for `git ls-files`."""
    global _tracked
    if _tracked is None:
        _tracked = set()
        if root:
            try:
                out = subprocess.run(
                    ["git", "-C", root, "ls-files", "-z"],
                    capture_output=True, text=True, timeout=30, check=False,
                )
                if out.returncode == 0:
                    _tracked = {
                        os.path.join(root, p) for p in out.stdout.split("\0") if p
                    }
            except (OSError, subprocess.SubprocessError):
                _tracked = set()
    return _tracked


def is_tracked(path: str, root: str, cwd: str) -> bool:
    """True when `path` names a file git tracks in this repo.

    Resolution is tried from the command's cwd and from the repo root, because a Bash command may
    `cd` first and its paths are relative to wherever it ends up. `realpath` is not used: a symlink
    is a different question and resolving one would silently widen the subject.
    """
    if not path or path.startswith("-"):
        return False
    cands = []
    if os.path.isabs(path):
        cands.append(os.path.normpath(path))
    else:
        for base in (cwd, root):
            if base:
                cands.append(os.path.normpath(os.path.join(base, path)))
    t = tracked_set(root)
    return any(c in t for c in cands)


def lift_heredocs(cmd: str) -> tuple[str, list[str]]:
    """Return (command text with heredoc bodies removed, list of the bodies).

    The bodies are the inline programs, so they are the thing to inspect for a write; leaving them in
    the command text would also make it untokenisable, since a heredoc body contains arbitrary
    quotes.
    """
    lines = cmd.split("\n")
    bodies: list[str] = []
    kept: list[str] = []
    i = 0
    while i < len(lines):
        line = lines[i]
        kept.append(line)
        delims = [m.group(2) for m in HEREDOC.finditer(line)]
        i += 1
        for delim in delims:
            body: list[str] = []
            while i < len(lines) and lines[i].strip() != delim:
                body.append(lines[i])
                i += 1
            if i < len(lines):
                i += 1  # consume the terminator
            bodies.append("\n".join(body))
    return "\n".join(kept), bodies


SEPARATORS = {";", "|", "||", "&&", "&", "\n", "(", ")", "{", "}"}
REDIRECTS = {">", ">>", "1>", "2>", "&>", ">|"}


def tokenize(text: str) -> list[str] | None:
    lex = shlex.shlex(text, posix=True, punctuation_chars=True)
    lex.whitespace_split = True
    try:
        return list(lex)
    except ValueError:
        return None


def simple_commands(tokens: list[str]) -> list[list[str]]:
    out: list[list[str]] = [[]]
    for t in tokens:
        if t in SEPARATORS:
            out.append([])
        else:
            out[-1].append(t)
    return [c for c in out if c]


def basename(word: str) -> str:
    return os.path.basename(word)


def findings(cmd: str, root: str, cwd: str) -> list[str]:
    """Every reason this command must not run, as sentences. Empty means allow."""
    text, bodies = lift_heredocs(cmd)
    tokens = tokenize(text)
    if tokens is None:
        print(
            "editroute: this command could not be tokenised, so it was ALLOWED unchecked. "
            "See the known-gap note in scripts/editroute.py.",
            file=sys.stderr,
        )
        return []

    reasons: list[str] = []

    # subst1.py's own invocation is the one explicit exemption: it IS a permitted route, and it
    # writes its target by design.
    if any(basename(t) == "subst1.py" for t in tokens):
        return []

    # --- route 1: redirection into a tracked path, including `tee` -----------------------------
    for i, t in enumerate(tokens):
        if t in REDIRECTS and i + 1 < len(tokens):
            target = tokens[i + 1]
            if is_tracked(target, root, cwd):
                reasons.append(
                    f"a shell redirection ({t}) would overwrite the tracked file {target!r}"
                )
    for cmdv in simple_commands(tokens):
        if cmdv and basename(cmdv[0]) in ("tee",):
            for arg in cmdv[1:]:
                if is_tracked(arg, root, cwd):
                    reasons.append(f"tee would overwrite the tracked file {arg!r}")

        # --- route 2: in-place stream editors -------------------------------------------------
        if cmdv and basename(cmdv[0]) in ("sed", "gsed", "perl", "ruby"):
            inplace = any(a == "-i" or a.startswith("-i") for a in cmdv[1:])
            if inplace:
                for arg in cmdv[1:]:
                    if is_tracked(arg, root, cwd):
                        reasons.append(
                            f"{basename(cmdv[0])} -i would edit the tracked file {arg!r} in place"
                        )

    # --- route 3: an inline interpreter that opens a file for writing ---------------------------
    programs: list[str] = list(bodies)
    for cmdv in simple_commands(tokens):
        if not cmdv or basename(cmdv[0]) not in INTERPRETERS:
            continue
        for j, arg in enumerate(cmdv[1:], start=1):
            if arg in ("-c", "-e") and j + 1 < len(cmdv):
                programs.append(cmdv[j + 1])

    inline_present = any(
        basename(c[0]) in INTERPRETERS for c in simple_commands(tokens) if c
    )
    for prog in programs:
        if not inline_present or not WRITE_OPEN.search(prog):
            continue
        lits = [m.group(1) for m in LITERAL.finditer(prog)]
        hit = [l for l in lits if is_tracked(l, root, cwd)]
        if hit:
            reasons.append(
                "an inline interpreter opens the tracked file "
                + ", ".join(repr(h) for h in hit)
                + " for writing"
            )
        elif not any(
            is_tracked(l, root, cwd) or os.path.sep in l or "." in l for l in lits
        ):
            # A write whose target is computed rather than written down cannot be shown to be
            # outside the subject. Exemption is by explicit path, so an unresolvable target is
            # refused rather than assumed safe.
            reasons.append(
                "an inline interpreter opens a file for writing and its target is not an "
                "explicit path, so it cannot be shown to be untracked"
            )
    return reasons


def main() -> int:
    try:
        payload = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        return 0
    if payload.get("tool_name") != "Bash":
        return 0
    cmd = (payload.get("tool_input") or {}).get("command") or ""
    if not cmd:
        return 0

    root = repo_root()
    cwd = payload.get("cwd") or os.getcwd()
    reasons = findings(cmd, root, cwd)
    if not reasons:
        return 0

    print(
        "editroute: REFUSED — a tracked file must not be edited from a Bash command.\n\n"
        + "".join(f"  * {r}\n" for r in reasons)
        + "\n  This is enforced because the rule was broken in all 8 writes to one script in one\n"
        "  campaign, and the eighth lost an edit whose mechanism could not afterwards be\n"
        "  determined: a write whose success is never read back leaves no evidence of its own\n"
        "  failure, and `bash -n` cannot see an absence.\n\n"
        "  Use one of the two loud routes:\n"
        + ALLOWED_ROUTES
        + "\n  Creating a NEW file this way is fine and is not what was refused — the defect is a\n"
        "  silent no-op on a missed anchor, which requires the file to already exist.\n",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    sys.exit(main())
