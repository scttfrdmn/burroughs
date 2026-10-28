#!/usr/bin/env python3
# Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
"""unfired — list the laws and hook routes that have not fired since a release.

The chair's fifth ruling of 2026-10-28, item 6: at each release cut, every law and hook route that has not
fired since the previous release is listed and posted on the release issue, for Scott to retire any. The law
this serves is in docs/laws/graves-and-sweeps.md ("A new law needs two recorded occurrences").

What "fired" means here, because the list is only as honest as that definition:

- A **law** fired when it was cited in the window, by its anchor (`<family>.md#<slug>`) or by its title quoted
  (markup and case ignored, titles of more than three words only): in a commit message or an added line on the
  head branch, in a merged PR's body, or in an issue or PR comment. A law applied without being named is not
  counted, which makes this list an upper bound on what is unused. That is the safe direction for a list whose
  purpose is to propose retirements to a principal.
- A **hook route** fired when the refusal log has an entry with its slug in the window. The log is untracked
  and per-machine (scripts/refusals.sh says why), so this half is about this machine's sessions only.

The population is every `##` and `###` heading in docs/laws/*.md, outside fenced code (a law family uses
`###` for a law, and operations.md uses `##` for a recipe, which CLAUDE.md cites as laws), and every slug in
scripts/editroute.py's RULES. Both counts are printed, so an empty list cannot be read as a checked one.

Times are compared in UTC: the refusal log writes `Z` timestamps, and a tag's date is converted to match.

usage:
  scripts/unfired.py <previous-tag> [--head origin/main]
  scripts/unfired.py --since <ISO date> --evidence <file> [--log <file>]   (offline, for the tests)
"""

import argparse
import datetime
import importlib.util
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LAWS = os.path.join(ROOT, "docs", "laws")
FENCE = re.compile(r"^\s*(```|~~~)")
HEADING = re.compile(r"^#{2,3} (.+)$")


def anchor_for(heading: str) -> str:
    """internal/testenv/laws_test.go's anchorFor, so the slugs here are the ones the link control resolves."""
    out = []
    for r in heading.lower():
        if "a" <= r <= "z" or "0" <= r <= "9" or r in "_-":
            out.append(r)
        elif r == " ":
            out.append("-")
    return "".join(out)


def laws() -> list[tuple[str, str, str]]:
    found = []
    for name in sorted(os.listdir(LAWS)):
        if not name.endswith(".md"):
            continue
        fenced = False
        with open(os.path.join(LAWS, name), encoding="utf-8") as f:
            for line in f:
                if FENCE.match(line):
                    fenced = not fenced
                    continue
                m = None if fenced else HEADING.match(line.rstrip("\n"))
                if m:
                    found.append((name, anchor_for(m.group(1)), m.group(1)))
    return found


def routes() -> list[str]:
    spec = importlib.util.spec_from_file_location("editroute", os.path.join(ROOT, "scripts", "editroute.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return [slug for slug, _ in mod.RULES]


def run(*argv: str) -> str:
    return subprocess.run(argv, cwd=ROOT, check=True, capture_output=True, text=True).stdout


def fetch_evidence(tag: str, head: str, since: str) -> str:
    parts = [run("git", "log", "--format=%B", f"{tag}..{head}")]
    diff = run("git", "log", "-p", "--format=", f"{tag}..{head}")
    parts.append("\n".join(ln for ln in diff.splitlines() if ln.startswith("+") and not ln.startswith("+++")))
    day = since[:10]
    prs = json.loads(run("gh", "pr", "list", "--state", "merged", "--search", f"merged:>={day}",
                         "--json", "body", "--limit", "1000"))
    parts.extend(p.get("body") or "" for p in prs)
    repo = run("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").strip()
    parts.append(run("gh", "api", "--paginate", f"repos/{repo}/issues/comments?since={since}&per_page=100",
                     "--jq", ".[].body"))
    return "\n".join(parts)


def title_key(text: str) -> str:
    """A law's title as it is quoted: markup and the closing period dropped, whitespace folded, lower-cased."""
    return " ".join(re.sub(r"[*`_]", "", text).strip().rstrip(".").lower().split())


def fired(evidence: str, folded: str, family: str, slug: str, title: str) -> bool:
    # The slug must end where the citation ends: a citation of a longer anchor that begins with this one is a
    # different law.
    if re.search(re.escape(f"{family}#{slug}") + r"(?![a-z0-9_-])", evidence):
        return True
    # Or the title quoted, which is how this tree usually cites a law (*Durability is not independence*). A
    # title of three words or fewer is too short for a substring to mean a citation, so it needs the anchor.
    key = title_key(title)
    return len(key.split()) > 3 and key in folded


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("tag", nargs="?")
    ap.add_argument("--head", default="origin/main")
    ap.add_argument("--since")
    ap.add_argument("--evidence")
    ap.add_argument("--log", default=os.environ.get("EDITROUTE_LOG", os.path.join(ROOT, ".editroute-log")))
    a = ap.parse_args()

    if a.evidence:
        if not a.since:
            ap.error("--evidence needs --since: offline, there is no tag to date the window from")
        since = a.since
        with open(a.evidence, encoding="utf-8") as f:
            evidence = f.read()
        window = f"since {since} (offline evidence: {a.evidence})"
    else:
        if not a.tag:
            ap.error("name the previous release's tag")
        if a.since:
            since = a.since
        else:
            epoch = int(run("git", "log", "-1", "--format=%ct", a.tag).strip())
            since = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        evidence = fetch_evidence(a.tag, a.head, since)
        window = f"{a.tag}..{a.head}, since {since}"

    population = laws()
    folded = title_key(evidence)
    unfired_laws = [(fam, slug, title) for fam, slug, title in population
                    if not fired(evidence, folded, fam, slug, title)]
    print(f"unfired: {window}")
    print(f"laws: {len(unfired_laws)} of {len(population)} are neither cited by anchor nor quoted by title in the window")
    for fam, slug, title in unfired_laws:
        print(f"  {fam}#{slug}  {title}")

    slugs = routes()
    entries = []
    if os.path.exists(a.log):
        with open(a.log, encoding="utf-8") as f:
            entries = [ln.rstrip("\n").split("\t") for ln in f if ln.strip()]
    hit = set()
    in_window = 0
    for e in entries:
        if len(e) >= 2 and e[0] >= since:
            in_window += 1
            hit.update(e[1].split(","))
    unfired_routes = [s for s in slugs if s not in hit]
    print(f"routes: {len(unfired_routes)} of {len(slugs)} have no refusal in the window "
          f"({in_window} log entries in it; the log is machine-local: {a.log})")
    for s in unfired_routes:
        print(f"  {s}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
