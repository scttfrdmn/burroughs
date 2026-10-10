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

A known FALSE POSITIVE: reading a tracked file inside an inline interpreter
---------------------------------------------------------------------------

An inline script that **reads** a tracked file and writes somewhere else is refused as though it
wrote the tracked one. Reproduced:

	python3 -c "import pathlib; s=pathlib.Path('scripts/editroute.py').read_text(); \
	            pathlib.Path('/tmp/out.py').write_text(s)"
	-> editroute: REFUSED
	   * an inline interpreter opens the tracked file 'scripts/editroute.py' for writing

The write target is `/tmp/out.py`. The inline-interpreter route sees a tracked path and a
write-shaped call in the same body and attributes the write to the path, without matching which
argument belongs to which call.

**Not fixed, deliberately.** Matching the path to the call means parsing the embedded language —
Python here, but the route covers any interpreter — and a half-parse would be a new way to miss a
real write, which is the one direction this guard may not err in. The cost is an operator rewriting
one command, usually by reading the file through the Read tool instead, and it was paid twice in the
session that found it: once copying a tracked script to /tmp, once building a neutered variant of
this very file.

Recorded rather than filed because nothing here will act on it: it is a standing property of the
route, not a defect awaiting a slice. (Chair's ruling, in session, 2026-10-10: record it, do not fix
it now.)

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

import hashlib
import json
import os
import re
import shlex
import subprocess
import sys
import time

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

# --- the refusal log ----------------------------------------------------------------------------------------
#
# **Why a log and not a tally.** This hook has refused ten times and **three of those were false positives**,
# all three on its own author — a bare name after a `cd` outside the repo, a *backgrounded* `sleep` (a process,
# not a wait), and a `/dev/null` belonging to a later command. It has been narrowed three times as a result.
# That ratio matters more than any single catch, because **an over-refusing check is the kind that gets
# deleted** — and until now the ratio was counted from memory, which is the one thing this project does not
# accept for a figure that decides something.
#
# So each refusal appends **one line**: when, which rule, and a hash of the command. The hash and not the
# command, because a command can carry a path or a secret and this file is for counting, not for forensics.
#
# Untracked and gitignored: it is a fact about one machine's recent sessions. Classifying the entries
# true/false is a separate, deliberate step at the next tooling decision — a log nobody reads is a tally with
# extra steps.
REFUSAL_LOG = ".editroute-log"

# Rule slugs, matched by a distinctive phrase from each reason. **Ordered**, and the first match wins.
#
# A reason is prose, so this is a classifier over text the hook itself emits — not over a file, which is why
# property 21's parse-don't-grep does not reach it. What *does* reach it is the risk that a new reason lands
# with no rule and logs as `unclassified`, which is why
# `TestEveryRefusalReasonHasARule` asserts the classifier is TOTAL over the hook's own deny arms.
RULES: tuple[tuple[str, str], ...] = (
    ("replace-all", "`replace_all: true`"),
    ("subst1-discarded", "landing display"),
    ("subst1-chained", "runs whatever its exit status was"),
    ("sleep-as-wait", "a duration is not a signal"),
    ("inline-write", "an inline interpreter opens"),
    ("in-place-edit", "in place"),
    ("tee-write", "tee would overwrite"),
    ("redirect-write", "a shell redirection"),
    ("git-discard", "would DISCARD uncommitted changes"),
    # The fail-closed case needs its own slug: it is a DIFFERENT finding from a measured discard — one is
    # "this destroys work", the other "I could not tell". Grouping them would hide how often the guard is
    # refusing blind, which is the figure that would justify changing how it asks.
    ("git-discard-unknown", "could not be checked"),
    # The override is logged like a refusal and therefore needs a slug like one. Without it the entry read
    # `unclassified`, which `scripts/refusals.sh` calls "a DEFECT in the rule table, not a category" — and
    # a deliberate discard is precisely the entry a later reader most wants to be able to group.
    ("discard-override", "override used"),
)


def rule_for(text: str) -> str:
    for slug, phrase in RULES:
        if phrase in text:
            return slug
    return "unclassified"


def log_refusal(texts: list[str], subject: str, root: str) -> None:
    """Append exactly one line per refusal: `when<TAB>rules<TAB>hash`.

    Failures are swallowed on purpose. A hook that cannot write its log must still refuse — making the
    refusal depend on the log would turn a disk-full into a permissive hook, which is the inverse of what
    this file is for.
    """
    try:
        rules = ",".join(sorted({rule_for(t) for t in texts})) or "unclassified"
        digest = hashlib.sha256(subject.encode("utf-8", "replace")).hexdigest()[:12]
        when = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        # `EDITROUTE_LOG` overrides the path, matching what `scripts/refusals.sh` already reads. It exists
        # for the witness: redirecting `CLAUDE_PROJECT_DIR` instead would move the repo root, and the root is
        # what `git ls-files` is asked about — so the tracked-file routes would stop refusing and the arms
        # would measure an empty repo. That is what the first version of the witness did.
        path = os.environ.get("EDITROUTE_LOG") or (
            os.path.join(root, REFUSAL_LOG) if root else REFUSAL_LOG
        )
        # **`newline="\n"`, because this file has a format and Windows would change it.** Python's
        # text mode translates `\n` to the platform line ending, so on Windows every record gained a
        # `\r` — measured on the Windows job as `field 3 is not a 12-char digest: "61d961bd2b8a\r"`.
        #
        # That is not only a test's problem: `scripts/refusals.sh` parses this log by tab-separated
        # field, so a trailing `\r` rides along on the last field of every line and the digest it
        # reports is wrong by one character. A log whose own format drifts by platform cannot be the
        # record of a refusal rate. Same reasoning as `.gitattributes` pinning `eol=lf`: the artifact
        # is machine-read, so its bytes are part of its contract.
        with open(path, "a", encoding="utf-8", newline="\n") as fh:
            fh.write(f"{when}\t{rules}\t{digest}\n")
    except OSError:
        pass


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


def path_key(path: str) -> str:
    """The one spelling a path is compared by, for both the tracked set and its lookups.

    **This exists because the two sides were keyed differently and the hook went silently inert.**
    `tracked_set` built `os.path.join(root, p)` from `git ls-files` output — forward slashes on
    every platform, and `join` does not rewrite a component's internal separators — while
    `is_tracked` looked up through `normpath`. On Windows that is `D:\\repo\\scripts/ratio.sh`
    against `D:\\repo\\scripts\\ratio.sh`: never equal, `is_tracked` False for every path, nothing
    refused. On POSIX `normpath` leaves both alone, which is why it was invisible where it was
    written.

    A function rather than a rule in a comment, so the two sides *cannot* diverge: there is no
    second place to forget. The same shape as the fix to the provenance control's glob keys, and
    the reason that one needed a membership check afterwards while this one does not — a shared
    constructor is a stronger guarantee than a check over two independent constructors.
    """
    return os.path.normpath(path)


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
                    # Keyed through `path_key`, which is also what every lookup uses. See its
                    # docstring: keying these two sides differently is what made this hook refuse
                    # nothing at all on Windows.
                    _tracked = {
                        path_key(os.path.join(root, p))
                        for p in out.stdout.split("\0")
                        if p
                    }
            except (OSError, subprocess.SubprocessError):
                _tracked = set()
    return _tracked


def cd_base(tokens: list[str], cwd: str) -> tuple[list[str], bool]:
    """The directories a relative path in this command may resolve against, and whether a `cd` set them.

    **A list, not one directory, because a `cd` can fail.** Bash leaves the working directory
    unchanged when `cd` fails, so a command like `cd /nonexistent; printf x > CHANGELOG.md` writes
    the tracked file where it started. Returning only the target made this hook allow that. The
    first element is the shell's most likely directory; the rest are the ones it may still be in.

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

    # The stance: a construct this function does not model must never NARROW

    Narrowing the candidate list is the only way this guard can be made to miss a write. So the rule
    for anything not modelled here is that it leaves every candidate in place:

      * **subshells** — `( … )`; a `cd` inside one never moves the parent, handled below;
      * **`pushd` / `popd`** — not recognised as a `cd` at all, so the candidates stand;
      * **`builtin cd` / `command cd`** — the command word is `builtin` or `command`, so likewise;
      * **`eval`, `bash -c '…'`, shell functions** — the directory change is inside a string or a
        body this function never reads.

    Measured, all four of the recognisable ones refuse rather than allow: `pushd /tmp; <write>`,
    `pushd /tmp && <write>`, `builtin cd /tmp && <write>` and `command cd /tmp && <write>` all come
    back `rc=2`. The `&&` forms are false positives — the same one the `cd` handling exists to
    remove — and they are **left unmodelled on purpose**: adding each construct is adding a way to
    narrow, and the cost of a false positive here is an operator rewriting one command, while the
    cost of failing open is a tracked file silently overwritten.

    So the next construct someone finds starts from refusing. If one is ever modelled, the arm that
    proves it narrows correctly belongs beside the `&&`/`;` pair in `editroute_test.go`, because
    those two are what test the rule rather than its instances.
    """
    bases, moved = [cwd], False
    # **A subshell is a construct this function does not model, so no `cd` anywhere in the command
    # may narrow.** A `cd` inside `( … )` changes the subshell's directory and never the parent's,
    # so `(cd /tmp && true) && printf x > CHANGELOG.md` writes the repo's tracked `CHANGELOG.md`.
    # The splitter treats `(` as a separator and discards it, so the inner `cd` looked like any
    # other, `&&` narrowed to `/tmp`, and the write was allowed — measured `rc=0`.
    #
    # Deciding *which* subshell a write belongs to is real modelling, and this guard does not do it.
    # The consequence, taken deliberately: `(cd /tmp && printf x > Makefile)` is refused even though
    # the `cd` does apply to that write, because the whole command is inside the subshell. That is a
    # false positive on the safe side, and the alternative is a construct that silently fails open.
    # Command substitution runs its body in a child shell too, so it is the same case. `$( … )`
    # already arrives with a `(` token — verified, `x=$(cd /tmp && true) && …` tokenises with one —
    # but a **backtick** substitution does not, and that form refuses today only by accident: the
    # token is `` `cd ``, so `basename` does not match "cd" and the `cd` is never detected at all.
    # Fail-closed by a property of the tokeniser rather than by this rule, which is one tokeniser
    # change away from failing open. Named here so it holds by design.
    has_subshell = "(" in tokens or any("`" in t for t in tokens)
    for cmdv, sep in simple_commands_with_sep(tokens):
        head = 0
        while head < len(cmdv) and cmdv[head] in SHELL_KEYWORDS:
            head += 1
        if head >= len(cmdv) or basename(cmdv[head]) != "cd":
            continue
        args = [a for a in cmdv[head + 1 :] if not a.startswith("-")]
        if not args:
            bases, moved = [os.path.expanduser("~")], True
            continue
        raw = os.path.expanduser(args[0])
        # **A relative target resolves against EVERY current candidate, not just the first.**
        #
        # `cd /nonexistent; cd scripts && printf x > editroute.py` is the case: the first `cd` fails,
        # bash stays in the repo root, `cd scripts` succeeds, and the write lands on the tracked
        # `scripts/editroute.py`. Resolving `scripts` against `bases[0]` alone gave
        # `/nonexistent/scripts`, and then `&&` narrowed to that one candidate and the write was
        # allowed — measured `rc=0`.
        #
        # `&&` says the `cd` succeeded. It does **not** say which directory it started from, and
        # when the preceding `cd` was uncertain there is more than one answer. So each candidate
        # produces a resolved target and `&&` keeps all of them.
        if os.path.isabs(raw):
            targets = [path_key(raw)]
        else:
            targets = [path_key(os.path.join(b, raw)) for b in bases if b]
        # **The OPERATOR decides, not the filesystem.**
        #
        # Whether the `cd` succeeded is not knowable here, and two drafts of this function tried to
        # know it anyway. Checking `os.path.isdir(target)` is unsound in both directions, measured:
        #
        #   - `cd <dir with mode 000>; printf x > CHANGELOG.md` — the target EXISTS, so the check
        #     said the `cd` worked, but bash cannot enter it and stays put. Allowed, and the write
        #     lands on the tracked file.
        #   - `mkdir /proc/nope; cd /proc/nope; printf x > CHANGELOG.md` — a `mkdir`-tracking
        #     carve-out (added to keep the false positive below from returning) assumed the
        #     directory would exist. The `mkdir` fails, so the `cd` fails. Allowed, same write.
        #
        # Both were found by the chair reading the fix rather than by a test, and both are the same
        # error: inferring a runtime outcome from a filesystem snapshot taken at a different time.
        #
        # So ask the shell instead. Bash guarantees that after `&&` the next command runs ONLY if
        # this one succeeded; after `;`, `||`, `&` or a newline it runs either way. That is a
        # statement about the program text, which is all this hook has and all it needs:
        #
        #   `cd X && …`   the write cannot run unless the `cd` worked, so X alone is the candidate.
        #   `cd X; …`     the shell may still be where it was, so keep BOTH.
        #   `cd X || …`   likewise — `||` runs the next command precisely when the `cd` failed.
        #
        # This also retires the `mkdir` carve-out rather than keeping it as insurance:
        # `mkdir -p /tmp/x && cd /tmp/x && printf … > Makefile` is joined by `&&` throughout, so the
        # original false positive stays fixed with no special case for `mkdir` at all. A rule that
        # needs no exceptions is the evidence it is the right rule — and leaving the dead checks in
        # would make this look more complete than it is.
        #
        # The cost is that `cd /tmp; printf x > Makefile` is refused. That is the safe direction, and
        # `&&` is the obvious rewrite.
        #
        # (Chair's ruling, given in session on the two unsound drafts above. Deliberately no issue
        # number: the ruling predates this slice's own PR, and the first draft of this comment cited
        # a guess at what that number would turn out to be. `citecheck` refused it as not resolving,
        # which is the check earning its keep — an in-session order has no citation, and inventing
        # one that looks like an artifact is worse than saying there is none.)
        if sep == "&&" and not has_subshell:
            bases, moved = targets, True
        else:
            bases = targets + [b for b in bases if b not in targets]
            moved = True
    return bases, moved


def is_tracked(path: str, root: str, cwds: list[str], moved: bool = False) -> bool:
    """True when `path` names a file git tracks in this repo.

    `cwds` are the directories the command may actually end up in, with any `cd` already applied by
    `cd_base` — a list rather than one directory because a failed `cd` leaves the shell where it was.
    The repo root is tried as a fallback **only when no `cd` moved us**: with a `cd` whose target
    exists the shell's answer is unambiguous, and a second guess could only manufacture a false
    match, which is precisely the false positive this signature was changed to fix. Where the `cd`
    could not have succeeded, `cd_base` supplies the pre-`cd` directory as a candidate instead, so
    that case is covered without widening the no-`cd` rule.

    `realpath` is not used: a symlink is a different question and resolving one would silently widen
    the subject.
    """
    if not path or path.startswith("-"):
        return False
    cands = []
    if os.path.isabs(path):
        cands.append(path_key(path))
    else:
        bases = list(cwds) if moved else list(cwds) + [root]
        for base in bases:
            if base:
                cands.append(path_key(os.path.join(base, path)))
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


def separate_commands(text: str) -> str:
    """Normalise newlines so each top-level command is one `shlex` command, quote-aware.

    Three jobs, in one pass because they interact:

    * a **line continuation** (`\\` then newline) outside quotes joins — it is one command, and splitting it
      would put `sed -i` and its target in different commands;
    * any other newline **outside quotes** becomes `;` — `shlex` eats newlines as whitespace, so without this
      `… | head -3` and `git add -A` tokenise as a single command and every per-command check is widened;
    * newlines **inside quotes** are left exactly as they are — they are content, and the inline-interpreter
      check reads that content.

    A single pass rather than two regexes because inside single quotes a backslash is literal: a blanket
    `\\\\\\n -> ' '` would corrupt the quoted text it was supposed to leave alone.
    """
    out: list[str] = []
    quote: str | None = None
    i = 0
    n = len(text)
    while i < n:
        c = text[i]
        if quote is not None:
            # In double quotes a backslash still escapes; in single quotes nothing does.
            if quote == '"' and c == "\\" and i + 1 < n:
                out.append(c)
                out.append(text[i + 1])
                i += 2
                continue
            if c == quote:
                quote = None
            out.append(c)
            i += 1
            continue
        if c in ("'", '"'):
            quote = c
            out.append(c)
            i += 1
            continue
        if c == "\\" and i + 1 < n and text[i + 1] == "\n":
            out.append(" ")  # continuation: one command, not two
            i += 2
            continue
        if c == "\\" and i + 1 < n:
            out.append(c)
            out.append(text[i + 1])
            i += 2
            continue
        if c == "\n":
            out.append(" ; ")
            i += 1
            continue
        out.append(c)
        i += 1
    return "".join(out)


def tokenize(text: str) -> list[str] | None:
    lex = shlex.shlex(text, posix=True, punctuation_chars=True)
    lex.whitespace_split = True
    try:
        return list(lex)
    except ValueError:
        return None


def simple_commands(tokens: list[str]) -> list[list[str]]:
    return [c for c, _ in simple_commands_with_sep(tokens)]


def simple_commands_with_sep(tokens: list[str]) -> list[tuple[list[str], str]]:
    """Each simple command with the separator token that FOLLOWS it (`""` for the last).

    The separator is carried rather than dropped because it is the shell's own statement about
    whether the next command runs: after `&&` it runs only if this one succeeded, and after `;`,
    `||` or a newline it runs either way. `cd_base` needs exactly that, and nothing else in this
    file can supply it — reconstructing it by re-scanning the token list afterwards would be
    guessing back information the splitter already had.
    """
    out: list[tuple[list[str], str]] = [([], "")]
    for t in tokens:
        if t in SEPARATORS:
            cmd, _ = out[-1]
            out[-1] = (cmd, t)
            out.append(([], ""))
        else:
            out[-1][0].append(t)
    return [(c, s) for c, s in out if c]


def basename(word: str) -> str:
    return os.path.basename(word)


def basename_in_cmd(cmd: str, name: str) -> bool:
    """Whether `name` appears as a command word. Used only to decide whether to LOG an override, so a
    coarse match is right: over-logging an override costs a line, under-logging hides one."""
    toks = tokenize(separate_commands(lift_heredocs(cmd)[0]))
    if toks is None:
        return name in cmd
    return any(c and basename(c[0]) == name for c in simple_commands(toks))


def discard_override() -> str | None:
    """The deliberate-discard escape, or None.

    A guard with no override is a guard people route around by other means, and the route they pick is
    not logged. This one is: `main` records the override in the refusal log alongside refusals, so a
    deliberate discard leaves a trace rather than being invisible.
    """
    v = os.environ.get("EDITROUTE_ALLOW_DISCARD", "")
    return v if v not in ("", "0") else None


# Each entry is (spelling, matcher). The matcher gets the argv AFTER `git` and returns the paths the
# command would discard, or None if this spelling does not apply.
#
# **The coverage is a LIMIT, not a guarantee**, and it is stated here because the PR body states it too:
# this matches COMMAND TEXT. It sees a direct invocation on the command line and does not see a script
# that runs git internally, a shell function, or an alias — `editroute.py` never gets past the command it
# is handed. So "the hook refuses discards" is false as a general claim; what is true is "the hook refuses
# these spellings, typed directly".
def git_discard_subject(cmdv: list[str], cwd: str) -> dict | None:
    a = [x for x in cmdv[1:] if x]

    # **Global options come BEFORE the subcommand, and skipping them is not optional.** `git -C <dir>
    # checkout -- x` and `git -c k=v reset --hard` put the option first, so reading `a[0]` as the
    # subcommand found `-C` and matched nothing — the guard was bypassed by the most ordinary scripted
    # spelling there is. `-C` also moves the directory the dirtiness must be measured in.
    cdir: str | None = None
    while a:
        if a[0] == "-C" and len(a) >= 2:
            cdir = a[1] if os.path.isabs(a[1]) else os.path.join(cwd, a[1])
            a = a[2:]
        elif a[0] == "-c" and len(a) >= 2:
            a = a[2:]
        elif a[0].startswith(("--git-dir=", "--work-tree=", "--namespace=")):
            a = a[1:]
        elif a[0] in ("--no-pager", "--literal-pathspecs", "--paginate", "-P"):
            a = a[1:]
        else:
            break
    if not a:
        return None
    sub = a[0]
    rest = a[1:]
    # Paths are judged relative to the SHELL's cwd (or `-C`'s directory), never the hook's own. The first
    # version called `os.path.exists(p)` directly, so a command issued from a subdirectory could have
    # `git checkout foo.go` misread as a BRANCH switch and let through — the dirtiness check joined
    # against cwd correctly, but the is-this-a-path decision happened before it and did not.
    pbase = cdir or cwd

    def here(p: str) -> str:
        return p if os.path.isabs(p) else os.path.join(pbase, p)

    def paths_after_ddash(args: list[str]) -> list[str]:
        return args[args.index("--") + 1 :] if "--" in args else []

    # `git checkout -- <path>` / `git checkout <rev> -- <path>` / `git checkout .`
    if sub == "checkout":
        if "--" in rest:
            return {"spelling": "checkout --", "paths": paths_after_ddash(rest), "cdir": cdir}
        # `git checkout .` and `git checkout <path>` discard too; a branch name does not. Only an
        # existing path is treated as a path, so switching branches is untouched.
        cand = [x for x in rest if not x.startswith("-")]
        hits = [p for p in cand if os.path.exists(here(p))]
        if hits:
            return {"spelling": "checkout", "paths": hits, "cdir": cdir}
        return None

    # `git restore` restores the WORKING TREE by default, which is the discard.
    #
    # **`--staged` alone only unstages** — the working tree keeps the content, so nothing is discarded and
    # it is allowed. `--staged --worktree` together do discard, so the exemption is for `--staged` with no
    # `--worktree`, not for the flag's presence.
    if sub == "restore":
        if any(x in ("--staged", "-S") for x in rest) and not any(
            x in ("--worktree", "-W") for x in rest
        ):
            return None
        hits = paths_after_ddash(rest) or [x for x in rest if not x.startswith("-")]
        return {"spelling": "restore", "paths": hits or ["."], "cdir": cdir}

    # `git reset --hard` throws away the working tree wholesale.
    if sub == "reset" and any(x == "--hard" for x in rest):
        return {"spelling": "reset --hard", "paths": ["."], "cdir": cdir}

    # `git clean -f` DELETES UNTRACKED files — untracked new work is lost the same way, which is why a
    # tracked-file-only guard would have missed it.
    if sub == "clean" and any(x.startswith("-") and "f" in x.lstrip("-") for x in rest):
        hits = [x for x in rest if not x.startswith("-")]
        return {"spelling": "clean -f", "paths": hits or ["."], "cdir": cdir}

    # `git stash drop` / `git stash clear` — the stash spellings that DISCARD. Plain `git stash` saves.
    if sub == "stash" and rest and rest[0] in ("drop", "clear"):
        return {"spelling": f"stash {rest[0]}", "paths": ["<the stash>"], "cdir": cdir}

    return None


# The marker for "the dirty state could not be determined". **A guard on an irreversible step fails
# CLOSED**: the first version returned an empty list when `git status` or `git stash list` failed, which
# let the discard through — *absence read as permission*, inside the guard built to stop a data loss.
UNKNOWN_DIRTY = "<could not determine>"


def dirty_paths(root: str, cwd: str, paths: list[str]) -> list[str]:
    """Which of `paths` have uncommitted changes (modified, staged, or untracked).

    `<the stash>` is reported dirty whenever the stash is non-empty: dropping an entry discards it
    whatever the working tree looks like.

    Returns `[UNKNOWN_DIRTY]` when git could not be asked — a non-zero exit as well as an OSError, since
    a git that ran and failed tells us no more than one that could not run.
    """
    if paths == ["<the stash>"]:
        try:
            p = subprocess.run(
                ["git", "-C", root, "stash", "list"], capture_output=True, text=True, check=False
            )
        except OSError:
            return [UNKNOWN_DIRTY]
        if p.returncode != 0:
            return [UNKNOWN_DIRTY]
        return ["<the stash>"] if p.stdout.strip() else []
    # **`git status --porcelain` reports paths relative to the REPOSITORY ROOT**, whatever directory `-C`
    # names — measured: `git -C sub status --porcelain` prints `sub/f.go`, not `f.go`. So a relative path
    # computed against `-C`'s subdirectory can never match a status line, the dirty file goes unfound, and
    # the discard is allowed. The comparison base has to be the top level.
    #
    # `rev-parse --show-toplevel` also resolves symlinks (`/tmp` -> `/private/tmp` here), so both sides go
    # through `realpath` — otherwise the relpath is computed across two spellings of one directory and
    # misses for a second, unrelated reason.
    #
    # A `rev-parse` that fails is the "couldn't check" case, not a clean tree.
    try:
        tp = subprocess.run(
            ["git", "-C", root, "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, check=False,
        )
    except OSError:
        return [UNKNOWN_DIRTY]
    if tp.returncode != 0 or not tp.stdout.strip():
        return [UNKNOWN_DIRTY]
    top = os.path.realpath(tp.stdout.strip())
    try:
        proc = subprocess.run(
            ["git", "-C", top, "status", "--porcelain"], capture_output=True, text=True, check=False
        )
    except OSError:
        return [UNKNOWN_DIRTY]
    if proc.returncode != 0:
        return [UNKNOWN_DIRTY]
    changed = {ln[3:].strip().strip('"') for ln in proc.stdout.splitlines() if len(ln) > 3}
    if not changed:
        return []
    hits: list[str] = []
    for p in paths:
        # A path given relative to the shell's cwd is compared against ROOT-relative status output, with
        # both sides realpath'd — see the note above for why the base is `top` and not `root`.
        abs_p = p if os.path.isabs(p) else os.path.join(cwd, p)
        rel = os.path.relpath(os.path.realpath(abs_p), top)
        rel = "" if rel == "." else rel
        # A path outside the repository cannot be matched against its status, and saying so is better than
        # a silent miss: the `..` prefix is how that shows up after the relpath.
        if rel.startswith(".."):
            continue
        for c in changed:
            if rel == "" or c == rel or c.startswith(rel.rstrip("/") + "/"):
                hits.append(c)
    return sorted(set(hits))


def findings(cmd: str, root: str, cwd: str) -> list[str]:
    """Every reason this command must not run, as sentences. Empty means allow."""
    text, bodies = lift_heredocs(cmd)
    # **A newline is a command separator and `shlex` eats it.** Measured: with `whitespace_split`, a newline
    # is whitespace, so `… | head -3\ngit add -A` tokenises as one command ending `head -3 git add -A`. Two
    # commands became one, which silently widened every per-command judgement below — and it is the separator
    # in the specimen that motivated the subst1-chaining check, where a refused edit was followed by a commit
    # on the next LINE rather than after a `;`.
    #
    # **But not every remaining newline separates commands**, and a blanket replace broke two cases:
    #
    #  * **Line continuation.** `sed -i '' s/a/b/ \<newline> CHANGELOG.md` is ONE command. Replacing the
    #    newline gives `… \ ; CHANGELOG.md`, where the backslash escapes a space and the `;` then splits
    #    `-i` from its target into two commands — letting through exactly the in-place edit this hook exists
    #    to refuse.
    #  * **Newlines inside quotes.** A multi-line `-c` program or quoted commit message would get `; `
    #    injected into its *content*, which is the text the inline-interpreter check reads.
    #
    # So the scan is quote-aware and does both jobs in one pass. The continuation join cannot be a separate
    # blanket `\\\n -> ' '` either: inside single quotes a backslash is literal, so joining there would
    # corrupt the content it was meant to preserve.
    text = separate_commands(text)
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
    bases, moved = cd_base(tokens, cwd)

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
        # **Scoped to subst1.py's OWN simple command, not the whole token stream.** The first version scanned
        # every token, so `subst1.py … && make fmt > /dev/null` was refused for a redirect belonging to
        # `make fmt` — a false positive on this hook's author, and the third of them. An over-refusing check is
        # the kind that gets deleted, so the subject is the redirect *attached to the invocation*: the tokens
        # from `subst1.py` up to the next separator.
        start = next(i for i, t in enumerate(tokens) if basename(t) == "subst1.py")
        end = len(tokens)
        for i in range(start + 1, len(tokens)):
            if tokens[i] in SEPARATORS:
                end = i
                break
        own = tokens[start:end]
        if any(
            t in REDIRECTS and i + 1 < len(own) and own[i + 1] == "/dev/null"
            for i, t in enumerate(own)
        ):
            reasons.append(
                "subst1.py's output is redirected to /dev/null — the landing display is the scripted "
                "route's only defence against an anchor that is unique, present and in the wrong place"
            )

        # --- route 6: a refusal nobody reads ----------------------------------------------------
        #
        # `subst1.py` refused an ambiguous edit — 7 matches of `### Added` — and the commit chained after
        # it ran anyway, so the change landed without its CHANGELOG entry. The loud route was loud; the
        # SEQUENCING swallowed it.
        #
        # So the separator that follows the invocation is the subject. `;` and a newline carry on
        # regardless of the exit status, and `||` runs the next command *because* it failed — all three
        # turn a refusal into a no-op. **`&&` is permitted**, because it is what the chaining meant: the
        # next step happens only if the edit happened.
        #
        # This closes the route rather than asking the next actor to remember an ordering, which is the
        # difference between a mechanism and a note — and the note would have been mine to forget.
        idx = next(i for i, t in enumerate(tokens) if basename(t) == "subst1.py")
        for t in tokens[idx + 1 :]:
            if t in ("&&",):
                break
            if t in (";", "||", "&"):
                reasons.append(
                    f"a command follows subst1.py past `{t}`, which runs whatever its exit status was — "
                    "so a refused edit is followed by the next step anyway. Use `&&`, so the next step "
                    "happens only if the edit did"
                )
                break
        return reasons

    # --- route 1: redirection into a tracked path, including `tee` -----------------------------
    for i, t in enumerate(tokens):
        if t in REDIRECTS and i + 1 < len(tokens):
            target = tokens[i + 1]
            if is_tracked(target, root, bases, moved):
                reasons.append(
                    f"a shell redirection ({t}) would overwrite the tracked file {target!r}"
                )
    for cmdv in simple_commands(tokens):
        if cmdv and basename(cmdv[0]) in ("tee",):
            for arg in cmdv[1:]:
                if is_tracked(arg, root, bases, moved):
                    reasons.append(f"tee would overwrite the tracked file {arg!r}")

        # --- route 2: in-place stream editors -------------------------------------------------
        if cmdv and basename(cmdv[0]) in ("sed", "gsed", "perl", "ruby"):
            inplace = any(a == "-i" or a.startswith("-i") for a in cmdv[1:])
            if inplace:
                for arg in cmdv[1:]:
                    if is_tracked(arg, root, bases, moved):
                        reasons.append(
                            f"{basename(cmdv[0])} -i would edit the tracked file {arg!r} in place"
                        )

        # --- route 3: a git command that DISCARDS uncommitted work ----------------------------
        #
        # The specimen is this hook's own author losing work. One compound command was
        #
        #     git add -A && git commit -m "wip: baseline" && python3 <<'PY' … PY
        #
        # whose heredoc wrote a tracked file, so **this hook refused the whole command and nothing in it
        # ran** — including the `git commit` at its front. The baseline therefore never existed, and a
        # later `git checkout -- scripts/ciwatch.sh`, issued to "restore from the injection", reverted
        # real work: a poll loop, a default, and a fixture mechanism, all uncommitted.
        #
        # **A hook refusal kills the entire compound command**, so anything earlier in it did not run.
        # Treating a refusal as "the risky part was blocked" rather than "nothing happened" is what made
        # the restore look safe. The project already had the rule — *an injection battery needs a
        # committed baseline* — cited earlier in the same session and then applied without checking the
        # baseline existed, which is why this is a mechanism and not another sentence.
        #
        # **`git stash` is NOT here, deliberately.** Stashing *saves* the changes, so it is recoverable —
        # and it is the move the refusal message below recommends. Refusing it would block the remedy.
        # What discards is `stash drop` and `stash clear`.
        if cmdv and basename(cmdv[0]) == "git" and discard_override() is None:
            # `bases[-1]`, not `bases[0]`, and the distinction is load-bearing. This resolves a
            # relative `git -C` against the shell's ACTUAL directory, so unlike `is_tracked` —
            # which asks `any()` over candidates and does not care about order — it needs the one
            # right answer. When the `cd` succeeded `cd_base` returns a single element, so first
            # and last agree. When it could not have succeeded the shell stayed where it was, and
            # that directory is the one `cd_base` appends last.
            what = git_discard_subject(cmdv, bases[-1])
            if what is not None:
                # `-C <dir>` moves the repository the dirtiness must be measured in; without it the check
                # would read the session's root and clear a discard aimed somewhere else entirely.
                dirty = dirty_paths(
                    what.get("cdir") or root, what.get("cdir") or bases[-1], what["paths"]
                )
                if dirty == [UNKNOWN_DIRTY]:
                    reasons.append(
                        f"`git {what['spelling']}` could not be checked: git would not report whether "
                        f"this discards uncommitted work. Refused rather than allowed — a guard on an "
                        f"irreversible step fails closed. Set EDITROUTE_ALLOW_DISCARD=1 to proceed anyway"
                    )
                elif dirty:
                    listed = ", ".join(repr(p) for p in dirty[:4])
                    # The stash case gets its own remedy: "commit or stash them first" is nonsense advice
                    # for dropping a stash entry, and wrong advice is the failure this hook's dispatch
                    # already guards against one level up.
                    remedy = (
                        "inspect it with `git stash show -p` first"
                        if what["paths"] == ["<the stash>"]
                        else "commit or `git stash` them first"
                    )
                    reasons.append(
                        f"`git {what['spelling']}` would DISCARD uncommitted changes in {listed} "
                        f"— {remedy}, or set EDITROUTE_ALLOW_DISCARD=1 to discard deliberately"
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
    # **A BACKGROUNDED `sleep` is not a wait**, and refusing it was a false positive on this hook's own
    # author: `sleep 400 &` was a live process to hold a gate lock in a witness, and the shell does not block
    # on it at all. An over-refusing check is not the safe direction — it blocks work it was never aimed at,
    # and a blocked actor proceeds by working around the check.
    #
    # The discriminator is the separator that FOLLOWS the command, which is exactly the structure the
    # subst1-chaining check reads, one token further on.
    backgrounded = set()
    seen_cmds = 0
    for i, t in enumerate(tokens):
        if t in SEPARATORS:
            if t == "&":
                backgrounded.add(seen_cmds)
            seen_cmds += 1

    depth = 0
    for n, cmdv in enumerate(simple_commands(tokens)):
        head = 0
        while head < len(cmdv) and cmdv[head] in SHELL_KEYWORDS:
            head += 1
        word = basename(cmdv[head]) if head < len(cmdv) else ""
        if word == "sleep" and depth == 0 and n not in backgrounded:
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
        hit = [l for l in lits if is_tracked(l, root, bases, moved)]
        if hit:
            reasons.append(
                "an inline interpreter opens the tracked file "
                + ", ".join(repr(h) for h in hit)
                + " for writing"
            )
        elif not any(
            is_tracked(l, root, bases, moved) or os.path.sep in l or "." in l for l in lits
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
    # **`MultiEdit` carries `replace_all` PER ENTRY, inside `edits`, not at the top level.** Checking
    # only `inp["replace_all"]` therefore refused `Edit` and let `MultiEdit` through unchecked — the
    # wider tool, unguarded, which is the worse half to miss. Both shapes are read here, and the
    # per-entry scan is what the `MultiEdit` arm exists to hold.
    wide = inp.get("replace_all") is True
    if not wide:
        edits = inp.get("edits")
        if isinstance(edits, list):
            wide = any(
                isinstance(e, dict) and e.get("replace_all") is True for e in edits
            )
    if tool in ("Edit", "MultiEdit") and wide:
        target = inp.get("file_path") or ""
        if is_tracked(target, root, [cwd]):
            log_refusal(["`replace_all: true`"], tool + " " + target, root)
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

    # **A used override is logged.** The discard guard has an escape, and an escape nobody can see is
    # indistinguishable from the guard not firing — so a deliberate discard leaves the same kind of trace a
    # refusal does. Logged whether or not the command is then refused for some other reason, because the
    # fact worth recording is that the escape was taken.
    if discard_override() is not None and basename_in_cmd(cmd, "git"):
        log_refusal(["EDITROUTE_ALLOW_DISCARD override used for a git discard"], cmd, root)

    reasons = findings(cmd, root, cwd)
    if not reasons:
        return 0
    log_refusal(reasons, cmd, root)

    # The two subjects get their own guidance, because a refusal that names the wrong route is worse
    # than one that names none: it sends the reader to a tool that cannot help.
    sleeping = [r for r in reasons if r.startswith("`sleep`")]
    # The discard route gets its OWN guidance. The first version let it fall into `editing`, so a refused
    # `git checkout --` was answered with "use str_replace or subst1.py" — advice for a problem the actor
    # does not have, which is the failure this very dispatch exists to prevent: *a refusal that names the
    # wrong route is worse than one that names none.*
    discarding = [
        r
        for r in reasons
        if "would DISCARD uncommitted changes" in r or "could not be checked" in r
    ]
    editing = [r for r in reasons if r not in sleeping and r not in discarding]

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
    if discarding:
        msg += (
            "  This discards uncommitted work, and it is refused because it already cost some. One\n"
            "  compound command was `git add -A && git commit … && python3 <<'PY' …`, whose heredoc\n"
            "  wrote a tracked file — so this hook refused the WHOLE command and nothing in it ran,\n"
            "  including the commit at its front. The baseline never existed, and a later\n"
            "  `git checkout --` meant to undo an injection reverted real work instead.\n\n"
            "  **A hook refusal kills the entire compound command**: anything earlier in it did not run.\n\n"
            "  Do instead one of:\n"
            "    - `git stash` (which SAVES, and is why plain `git stash` is not refused here); or\n"
            "    - commit first, even as a throwaway, so the discard has something to return to; or\n"
            "    - `EDITROUTE_ALLOW_DISCARD=1 <command>` to discard deliberately. The override is\n"
            "      LOGGED, so a deliberate discard leaves a trace rather than being invisible.\n\n"
            "  Coverage is a LIMIT, not a guarantee: this matches command text, so it sees a direct\n"
            "  invocation and not a script that runs git internally, a shell function, or an alias.\n\n"
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
