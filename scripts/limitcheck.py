#!/usr/bin/env python3
"""limitcheck — every issue a release cites as a known limit is still open (the chair's ruling of 2026-10-24).

Why this exists
---------------
`v0.8.0` and `v0.9.0` both listed #830 under **Known limits**, as an open defect on a default-on path. #832 had
fixed it on 2026-09-30, and it shipped in `v0.7.0`. The issue was simply never closed, and nothing compared a
known-limit entry with the tracker, so the false entry passed through two releases.

What it checks
--------------
The **list being written**: `[Unreleased]` when it has entries, and otherwise the newest version block (the first
`## [x.y.z]` heading), which at a cut is the block the release PR just wrote (`list_being_written`). A released
block is a record and is never what this judges while a newer list exists. Within the block, the paragraph that
starts `**Known limits`, up to the next bold paragraph or heading. Every issue that paragraph cites, as `#N` or as
an `/issues/N` link, must be OPEN on GitHub. **A tracker that cannot be read is a FAIL**, never a pass, and so is a
version block with no Known limits paragraph, because a release that names none was never checked.

When it runs
------------
- **In the cut procedure, always** (`--force`): the release PR moves `[Unreleased]` under the new version, and
  this runs on that tree.
- **In `make ci` (through `make cite`), when the CHANGELOG has a version block at its top**: that is, when
  `[Unreleased]` holds no entries, which is the release PR's state. Otherwise no cut is in progress, it says
  so, and it checks nothing. Every later PR adds to `[Unreleased]`, and a released block's limits are history.

Usage: limitcheck.py [--force] [CHANGELOG]
"""

import os
import re
import shlex
import subprocess
import sys

# The `gh` command, as an argv prefix. `LIMITCHECK_GH` is the test's seam: its tracker is a Python shim run as
# `python3 shim.py`, because a `gh` shell script on PATH cannot be executed by `subprocess` on Windows, and every
# arm there read as an unreadable tracker (PR #1095's first Windows run).
GH = shlex.split(os.environ.get("LIMITCHECK_GH", "gh"))

VERSION = re.compile(r"^## \[(\d+\.\d+\.\d+)\]")
UNRELEASED = re.compile(r"^## \[Unreleased\]")
ISSUE = re.compile(r"(?:#|/issues/)(\d+)\b")


def blocks(lines):
    """(name, body lines) for each `## [` block, in file order."""
    out, name, body = [], None, []
    for ln in lines:
        if ln.startswith("## ["):
            if name is not None:
                out.append((name, body))
            name, body = ln, []
        elif name is not None:
            body.append(ln)
    if name is not None:
        out.append((name, body))
    return out


def has_entries(body):
    return any(ln.startswith("- ") or ln.startswith("### ") for ln in body)


def known_limits(body):
    """The issue numbers cited in the block's Known limits paragraph, or None if it has no such paragraph."""
    start = next((i for i, ln in enumerate(body) if ln.startswith("**Known limits")), None)
    if start is None:
        return None
    seen = []
    for i, ln in enumerate(body[start:]):
        if i > 0 and (ln.startswith("**") or ln.startswith("#")):
            break
        for n in ISSUE.findall(ln):
            if n not in seen:
                seen.append(n)
    return seen


def state(n):
    """The issue's state from GitHub, or None if it could not be read."""
    try:
        out = subprocess.run([*GH, "api", f"repos/{{owner}}/{{repo}}/issues/{n}", "--jq", ".state"],
                             capture_output=True, text=True, timeout=60)
    except (OSError, subprocess.SubprocessError):
        return None
    return out.stdout.strip() if out.returncode == 0 else None


def merged_prs_naming(n):
    """Merged PRs whose title names #n. **Advisory, never a failure.**

    #830's own error was the reverse of what the state check sees. At the `v0.9.0` cut it was OPEN, left so after
    #832 fixed it, so "every cited limit is open" passed it. What would have caught it is the fix's PR, whose
    title read "… (#830) (#832)". A PR can name an issue without fixing it, so this is a prompt to the person
    cutting the release, not a verdict."""
    # `gh` expands {owner}/{repo} in the API path only, never inside a -f parameter. The first draft put it in the
    # query, where the search failed validation, and this returned nothing at all: an advisory that never spoke.
    try:
        repo = subprocess.run([*GH, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"],
                              capture_output=True, text=True, timeout=60).stdout.strip()
        if not repo:
            print(f"note  #{n}: the repository could not be named, so no merged PR was searched for")
            return []
        out = subprocess.run([*GH, "api", "-X", "GET", "search/issues", "-f",
                              f"q=repo:{repo} is:pr is:merged in:title #{n}", "--jq", ".items[].number"],
                             capture_output=True, text=True, timeout=60)
    except (OSError, subprocess.SubprocessError):
        return []
    if out.returncode != 0:
        print(f"note  #{n}: the merged-PR search failed, so this advisory checked nothing for it")
        return []
    return [x for x in out.stdout.split() if x.isdigit() and x != str(n)]


def list_being_written(bl):
    """(label, body) of the block whose Known limits the next release will ship, or None.

    **That is `[Unreleased]` when it has entries, and the newest version block only when it doesn't** (the chair's
    ruling of 2026-11-01, item 3). This read the newest version block always, so between cuts `--force` checked
    the last release's list: a record that correctly stays unedited, and whose FAIL (0.12.0 citing #1120 after
    #1120 closed) was noise that would teach people to ignore the check. An `[Unreleased]` with no entries is the
    release PR's state, where the release's block has just been written under its version, so that block is the
    one being written."""
    unrel = next((b for n, b in bl if UNRELEASED.match(n)), None)
    if unrel is not None and has_entries(unrel):
        return "Unreleased", unrel
    newest = next(((n, b) for n, b in bl if VERSION.match(n)), None)
    if newest is None:
        return None
    return VERSION.match(newest[0]).group(1), newest[1]


def main(argv):
    args = argv[1:]
    force = "--force" in args
    args = [a for a in args if a != "--force"]
    path = args[0] if args else "CHANGELOG.md"
    try:
        lines = open(path, encoding="utf-8").read().split("\n")
    except OSError as e:
        print(f"FAIL  limitcheck: {path} is not readable: {e}")
        return 1
    bl = blocks(lines)
    unrel = next((b for n, b in bl if UNRELEASED.match(n)), None)
    if not force and unrel is not None and has_entries(unrel):
        print("limitcheck: [Unreleased] has entries, so no release cut is in progress; nothing checked "
              "(the cut procedure runs this with --force)")
        return 0
    chosen = list_being_written(bl)
    if chosen is None:
        print(f"FAIL  limitcheck: {path} has no version block")
        return 1
    version, body = chosen
    cited = known_limits(body)
    if cited is None:
        print(f"FAIL  limitcheck: {version}'s block has no **Known limits** paragraph, so its limits were never "
              "stated or checked")
        return 1
    fail = 0
    for n in cited:
        st = state(n)
        if st is None:
            print(f"FAIL  #{n}: its state could not be read from the tracker; this claims nothing about the issue")
            fail = 1
        elif st != "open":
            print(f"FAIL  #{n} is {st}, and {version}'s Known limits cites it as an open limit")
            fail = 1
        else:
            print(f"ok    #{n} is open")
            for pr in merged_prs_naming(n):
                print(f"note  #{n}: merged PR #{pr} names it in its title; confirm the limit still holds")
    print(f"limitcheck: {version}, {len(cited)} known limit issue(s) cited, {'FAIL' if fail else 'all open'}")
    return fail


if __name__ == "__main__":
    sys.exit(main(sys.argv))
