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

Two known gaps, stated rather than filed
----------------------------------------

**A truncating pipe loses the landing display as surely as /dev/null does.** `subst1.py … | head -4`
keeps the first few lines and drops the rest, and that is a form actually used in this session. Only
the /dev/null forms are refused, because that is the shape the ruling named and because "how many
lines are enough" has no principled answer a check could hold. Named here so the next reader can
price it rather than assume the route is sealed.

**Today the display goes to stderr, so `> /dev/null` alone does not in fact lose it.** It is refused
anyway: which stream the display uses is an implementation detail no caller should have to track, and
a caller who wants the command quiet is the caller who adds `2>&1` next. The refusal is about the
intent, and saying so is better than letting a reader infer a precision the check does not have.

The known gap in parsing
------------------------
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


def cd_base(tokens: list[str], cwd: str) -> tuple[str, bool]:
    """The directory a relative path in this command resolves against, and whether a `cd` set it.

    **This exists because the hook produced a false positive on its author.** A command beginning
    `cd /tmp/mfprobe && printf ... > Makefile` was refused as overwriting the tracked root `Makefile`:
    the bare name was resolved against the repo root, and nothing had looked at the `cd`. The file
    being written was a new one in /tmp.

    An over-refusing check is not the safe direction. It blocks work it was never aimed at, and the
    way a blocked actor proceeds is by working around the check — which is the failure mode this
    whole mechanism exists to prevent.

    The last `cd` wins, because that is what the shell does. `~` is expanded; a `cd` with no argument
    means home; `cd -` is left alone rather than guessed at, since tracking OLDPWD is beyond what this
    needs and a wrong guess here is another false positive.
    """
    base, moved = cwd, False
    for cmdv in simple_commands(tokens):
        head = 0
        while head < len(cmdv) and cmdv[head] in SHELL_KEYWORDS:
            head += 1
        if head >= len(cmdv) or basename(cmdv[head]) != "cd":
            continue
        args = [a for a in cmdv[head + 1 :] if not a.startswith("-")]
        if not args:
            base, moved = os.path.expanduser("~"), True
            continue
        target = os.path.expanduser(args[0])
        base = target if os.path.isabs(target) else os.path.normpath(os.path.join(base, target))
        moved = True
    return base, moved


def is_tracked(path: str, root: str, cwd: str, moved: bool = False) -> bool:
    """True when `path` names a file git tracks in this repo.

    `cwd` is the directory the command actually ends up in, so a `cd` has already been applied by
    `cd_base`. The repo root is tried as a fallback **only when no `cd` moved us** — with an explicit
    `cd`, the shell's answer is unambiguous and a second guess can only manufacture a false match,
    which is precisely the false positive this signature was changed to fix.

    `realpath` is not used: a symlink is a different question and resolving one would silently widen
    the subject.
    """
    if not path or path.startswith("-"):
        return False
    cands = []
    if os.path.isabs(path):
        cands.append(os.path.normpath(path))
    elif moved:
        cands.append(os.path.normpath(os.path.join(cwd, path)))
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

# Words that may precede a simple command's actual command word. `shlex` gives these as plain
# tokens, so finding the command word means stepping over them.
SHELL_KEYWORDS = {"do", "then", "else", "elif", "if", "while", "until", "for", "!", "time", "nohup"}

WAIT_ROUTES = (
    "    - scripts/detach.sh, which records its pid and process group before starting and bounds\n"
    "      the run four ways; then wait on that process, e.g.\n"
    "        while kill -0 \"$pid\" 2>/dev/null; do sleep 20; done\n"
    "      whose `sleep` is the poll interval of a wait on a real signal, not the wait itself; or\n"
    "    - scripts/ciwatch.sh, which resolves the run by SHA and asserts the required jobs RAN; or\n"
    "    - run_in_background, and read the harness notification — but read the VERDICT from the\n"
    "      run or the output file, because a notification's exit code is the wrapper's.\n"
)


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

    # Where a relative path in this command actually resolves. Computed once, before any path is judged,
    # because every judgement below depends on it — and because the version that skipped this step refused a
    # `cd /tmp/mfprobe && printf … > Makefile` as overwriting the tracked root `Makefile`.
    base, moved = cd_base(tokens, cwd)

    # subst1.py's own invocation is the one explicit exemption: it IS a permitted route, and it
    # writes its target by design. But the exemption is conditional on the route still being the route.
    #
    # --- route 5: subst1.py with its output discarded --------------------------------------------
    #
    # The scripted route's one defence against a WRONG aim is the landing display it prints: the hunk it
    # wrote, with context, so the site is visible at the moment of the edit. Discard that and the exemption
    # buys a silent write — strictly worse than the inline interpreter it replaces, because it carries the
    # authority of a permitted route.
    #
    # This is not hypothetical. The third misplaced anchor in one slice went in through
    # `subst1.py … > /dev/null 2>&1 && make ci`, which silenced the display in the same command that relied
    # on it, minutes after the display was built for exactly that purpose. A structural control caught it.
    # The three discard forms, read from what the tokeniser actually produces rather than guessed:
    # `> /dev/null` and `>/dev/null` give ('>', '/dev/null'); `2>/dev/null` gives ('2', '>', '/dev/null');
    # `&> /dev/null` gives ('&>', '/dev/null'). All three are a redirection token followed immediately by
    # /dev/null, so one test covers them, and `> /dev/null 2>&1` is caught by its first half.
    if any(basename(t) == "subst1.py" for t in tokens):
        if any(
            t in REDIRECTS and i + 1 < len(tokens) and tokens[i + 1] == "/dev/null"
            for i, t in enumerate(tokens)
        ):
            reasons.append(
                "subst1.py's output is redirected to /dev/null — the landing display is the scripted "
                "route's only defence against an anchor that is unique, present and in the wrong place"
            )
        return reasons

    # --- route 1: redirection into a tracked path, including `tee` -----------------------------
    for i, t in enumerate(tokens):
        if t in REDIRECTS and i + 1 < len(tokens):
            target = tokens[i + 1]
            if is_tracked(target, root, base, moved):
                reasons.append(
                    f"a shell redirection ({t}) would overwrite the tracked file {target!r}"
                )
    for cmdv in simple_commands(tokens):
        if cmdv and basename(cmdv[0]) in ("tee",):
            for arg in cmdv[1:]:
                if is_tracked(arg, root, base, moved):
                    reasons.append(f"tee would overwrite the tracked file {arg!r}")

        # --- route 2: in-place stream editors -------------------------------------------------
        if cmdv and basename(cmdv[0]) in ("sed", "gsed", "perl", "ruby"):
            inplace = any(a == "-i" or a.startswith("-i") for a in cmdv[1:])
            if inplace:
                for arg in cmdv[1:]:
                    if is_tracked(arg, root, base, moved):
                        reasons.append(
                            f"{basename(cmdv[0])} -i would edit the tracked file {arg!r} in place"
                        )

    # --- route 4: `sleep` used as a wait --------------------------------------------------------
    #
    # Behaviour 5: wait on the verdict, never on a timer. This slipped more than once in one session,
    # including inside the slice that built this hook — which is the same shape as everything else here, a
    # rule recitable and broken at the moment acting was cheaper than remembering.
    #
    # The discriminator is LOOP DEPTH, not the word. `while kill -0 "$pid"; do sleep 20; done` is waiting on
    # the process — a named allowed route — and its `sleep` is the poll interval of a wait on a real signal.
    # A bare `sleep 240` at top level is the timer itself. So depth is tracked across the token stream and
    # only a depth-0 `sleep` is refused, which also means a `sleep` inside a committed script is untouched:
    # the hook never sees past the command line.
    depth = 0
    for cmdv in simple_commands(tokens):
        head = 0
        while head < len(cmdv) and cmdv[head] in SHELL_KEYWORDS:
            head += 1
        word = basename(cmdv[head]) if head < len(cmdv) else ""
        if word == "sleep" and depth == 0:
            reasons.append(
                "`sleep` is being used as the wait, at the top level of the command — a duration is not a "
                "signal, and the signal being waited for already exists"
            )
        for w in cmdv:
            if w in ("while", "until", "for"):
                depth += 1
            elif w == "done":
                depth = max(0, depth - 1)

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
        hit = [l for l in lits if is_tracked(l, root, base, moved)]
        if hit:
            reasons.append(
                "an inline interpreter opens the tracked file "
                + ", ".join(repr(h) for h in hit)
                + " for writing"
            )
        elif not any(
            is_tracked(l, root, base, moved) or os.path.sep in l or "." in l for l in lits
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
    tool = payload.get("tool_name")
    inp = payload.get("tool_input") or {}
    root = repo_root()
    cwd = payload.get("cwd") or os.getcwd()

    # --- the editor tool's own escape from the one-match check -----------------------------------
    #
    # `subst1.py` refuses >1 match, and the reason is stated in its own docstring: picking the first
    # silently is how an injection lands in the wrong one of several identical call sites. The editor
    # tool's `replace_all: true` opts out of exactly that refusal — and in the slice that built this
    # hook it hit a **sixth** call site nobody intended, leaving the hook crashing with a NameError.
    #
    # So the route around the loud check gets closed, which is this hook's whole premise. A wide edit
    # across a tracked file is not forbidden work; it is work that must be done where its scope is
    # visible, which means one anchored edit at a time or a longer unique anchor.
    #
    # Scoped to TRACKED files for the same reason every other route is: an untracked or new file has
    # no reviewers and no history to disturb.
    if tool in ("Edit", "MultiEdit") and inp.get("replace_all") is True:
        target = inp.get("file_path") or ""
        if is_tracked(target, root, cwd):
            print(
                "editroute: REFUSED — `replace_all: true` on the tracked file "
                f"{target!r}.\n\n"
                "  It replaces every occurrence without telling you how many there were, which is the\n"
                "  one thing `scripts/subst1.py` refuses outright (>1 match is an ambiguous edit). In\n"
                "  the slice that added this hook, a `replace_all` hit a sixth call site nobody\n"
                "  intended and left the hook itself crashing.\n\n"
                "  Do instead one of:\n"
                "    - extend the anchor until it is unique, and edit once; or\n"
                "    - make the edits one at a time, so each one's site is visible; or\n"
                "    - python3 scripts/subst1.py <target> <old-file> <new-file>, which refuses 0 and\n"
                "      >1 matches and prints the hunk it wrote with context.\n\n"
                "  Untracked and new files are not refused — a wide edit there disturbs no history.\n",
                file=sys.stderr,
            )
            return 2
        return 0

    if tool != "Bash":
        return 0
    cmd = inp.get("command") or ""
    if not cmd:
        return 0

    reasons = findings(cmd, root, cwd)
    if not reasons:
        return 0

    # The two subjects get their own guidance, because a refusal that names the wrong route is worse
    # than one that names none: it sends the reader to a tool that cannot help.
    sleeping = [r for r in reasons if r.startswith("`sleep`")]
    editing = [r for r in reasons if not r.startswith("`sleep`")]

    msg = "editroute: REFUSED.\n\n" + "".join(f"  * {r}\n" for r in reasons) + "\n"
    if editing:
        msg += (
            "  A tracked file must not be edited from a Bash command. This is enforced because the\n"
            "  rule was broken in all 8 writes to one script in one campaign, and the eighth lost an\n"
            "  edit whose mechanism could not afterwards be determined: a write whose success is never\n"
            "  read back leaves no evidence of its own failure, and `bash -n` cannot see an absence.\n\n"
            "  Use one of the two loud routes:\n"
            + ALLOWED_ROUTES
            + "\n  Creating a NEW file this way is fine and is not what was refused — the defect is a\n"
            "  silent no-op on a missed anchor, which requires the file to already exist.\n\n"
        )
    if sleeping:
        msg += (
            "  Wait on the verdict, never on a timer. Use one of:\n"
            + WAIT_ROUTES
            + "\n  A `sleep` inside a loop that tests a real condition is NOT refused — only a `sleep`\n"
            "  standing on its own as the wait is.\n"
        )
    print(msg, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
