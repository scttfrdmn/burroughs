#!/usr/bin/env python3
"""markercheck — the README's status names the release being cut (the chair's fifth ruling of 2026-10-28, 4b).

Why this exists
---------------
The README's status section is a short list of what is true at a release. It carries a marker,
*Written against `vX.Y.Z`*, so a reader knows which release the list describes. A list nobody re-reads at the cut
goes stale silently: the release ships while the front door describes the one before. The site has the same check
for its pages (burroughs.run's `scripts/release_snippet.py`, `stale_markers`), and this is the engine repo's.

What it checks
--------------
The README's marker must name the **newest version block** in CHANGELOG.md, the first `## [x.y.z]` heading. A
README with no marker, or with more than one, is a FAIL, because a status nobody dated was never checked.

When it runs
------------
Exactly when `limitcheck.py` does, and by the same test:
- **In the cut procedure, always** (`--force`).
- **In `make ci` (through `make cite`), while a cut is in progress**, meaning `[Unreleased]` holds no entries,
  which is the release PR's state. Otherwise it says so and checks nothing: between cuts the marker names the last
  release, which is correct.

So the release PR fails until the README has been re-read and its marker moved, and that is the point.

Usage: markercheck.py [--force] [CHANGELOG] [README]
"""

import re
import sys

VERSION = re.compile(r"^## \[(\d+\.\d+\.\d+)\]")
UNRELEASED = re.compile(r"^## \[Unreleased\]")
MARKER = re.compile(r"Written against `v(\d+\.\d+\.\d+)`")


def blocks(lines):
    """(name, body lines) for each `## [` block, in file order. limitcheck.py's, so the two agree on a cut."""
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


def main(argv):
    args = argv[1:]
    force = "--force" in args
    args = [a for a in args if a != "--force"]
    changelog = args[0] if args else "CHANGELOG.md"
    readme = args[1] if len(args) > 1 else "README.md"
    try:
        lines = open(changelog, encoding="utf-8").read().split("\n")
        text = open(readme, encoding="utf-8").read()
    except OSError as e:
        print(f"FAIL  markercheck: {e}")
        return 1
    bl = blocks(lines)
    unrel = next((b for n, b in bl if UNRELEASED.match(n)), None)
    if not force and unrel is not None and has_entries(unrel):
        print("markercheck: [Unreleased] has entries, so no release cut is in progress; nothing checked "
              "(the cut procedure runs this with --force)")
        return 0
    newest = next((n for n, _ in bl if VERSION.match(n)), None)
    if newest is None:
        print(f"FAIL  markercheck: {changelog} has no version block")
        return 1
    cutting = VERSION.match(newest).group(1)
    markers = MARKER.findall(text)
    if len(markers) != 1:
        print(f"FAIL  markercheck: {readme} has {len(markers)} 'Written against `vX.Y.Z`' markers, want exactly 1")
        return 1
    if markers[0] != cutting:
        print(f"FAIL  markercheck: {readme}'s status says it is written against v{markers[0]}, and the release "
              f"being cut is v{cutting}. Re-read the status section against the tree, then move the marker.")
        return 1
    print(f"markercheck: {readme}'s status names v{cutting}, the release being cut")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
