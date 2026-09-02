#!/usr/bin/env sh
# closecheck.sh — no PR body or commit message may close an issue by keyword.
#
# `Filed, not fixed: #310` closed #310 on the merge of PR #313. GitHub matched `fixed: #310` and
# the "not" is not a token its parser has an opinion about, so the sentence asserting that the
# issue was unresolved is the sentence that resolved it. #310 carries `decision-needed:scott`,
# which per CLAUDE.md *is* the decision queue, so the close silently removed a pending decision
# from a principal's queue and left `CLOSED` behind as the reassuring answer to every query that
# might have noticed. Grave #314.
#
# ## Why a ban rather than a declared intended-closes list
#
# #314 first proposed the softer rule: scan for keywords and require each to appear in a list the
# PR body declares. The ban is stronger and cheaper, for a reason particular to this repo — **a
# closing keyword is never the right mechanism here anyway.**
#
# Issues in this project are closed by hand, and they have to be: the lesson comment must land
# *before* the close, or the close eats it. That failure recurs, and *most* of its specimens are merge
# keywords closing an issue whose lesson was never written — which is what makes this ban the cheap
# half of the repair. The specimens are enumerated by query and not by prose (`gh issue list --label
# type:grave --state closed --json number,comments`, zero-comment rows; #303 is the control that runs
# it), because the tally this sentence used to quote was a measured quantity living in a comment.
# So the correct sequence is `gh issue comment` then `gh issue close`, in which a keyword has no part.
#
# A declared list would also have re-created the thing it guards against: a list is an exemption
# surface, and *the actor never chooses the instrument that judges the actor*. A ban has no
# surface to plead against — the remedy is to write the sentence differently, which costs one word.
#
# Cheap consequence worth stating: banning the construct removes the *cause* of the zero-comment
# tombstones #303 checks for as a *symptom*. The two remain separate instruments (#303 catches a
# hand close whose comment failed to post), but this one drains its population.
#
# ## Usage
#
#   scripts/closecheck.sh <rev>              # one commit, against its first parent
#   scripts/closecheck.sh <base> <head>      # a range: a PR merge base to its tip
#   scripts/closecheck.sh --worktree [base]  # base (default `main`) against HEAD
#   scripts/closecheck.sh --pr <number>      # the PR body and title, which need the network
#   scripts/closecheck.sh --body <file>      # a body before it is a PR, offline
#
# The revision forms match citecheck.sh and ratio.sh deliberately: three tools that answer a
# question about a diff should take a diff the same way. Read the siblings before writing the
# reader.
#
# ## `--body <file>`, and the gap it closes
#
# Ordered by Scott on the #396 report: *"It rides the next slice. No issue — filing one costs more
# than the fix, and 'a body is a file before it is a PR' is the whole design."* The `--pr` form can
# only run **after** a push, so `make check` cannot mirror CI on the body axis and the recipe in
# `docs/laws/operations.md` has to compensate by hand after `gh pr create`. It compensates
# imperfectly: at #398 the body opened with a banned construct, `make close` reported green over the
# *commit* half, and that green was read as the sweep's verdict — a verdict about a population that
# was not the one at risk.
#
# So this form takes the file the body is written into and scans it before the push. Two refusals
# rather than one, both for the reason the `--pr` arm refuses a failing fetch: **an unreadable file
# and an empty one are both "the question was not asked"**, and an empty body is the likelier
# mistake of the two, since a file that does not exist yet reads as zero lines under a redirect that
# went somewhere else.
#
# **The title is a stated under-match.** GitHub parses the title too, and a local file holds only the
# body; supplying the title as a second argument would invent a second convention for the caller to
# get wrong. The `--pr` form covers both channels and CI runs it, which is the same split as
# everywhere else here: the local run is runnable, the CI run is binding.
#
# ## Both channels, because GitHub acts on both
#
# A keyword closes an issue when it appears in a commit message that reaches the default branch
# **or** in the pull request body. The specimen came in through the body — a squash commit message
# is derived from the PR title and body by default, which is how prose written for a human reader
# became an instruction stream. So:
#
#   * **commit messages** in the range, scanned offline, always;
#   * **the PR title and body**, scanned when a number is supplied, which needs `gh`.
#
# Note what is *not* scanned, because this file is the obvious counterexample: **file contents.**
# The header above quotes the banned construct verbatim, and that is safe — GitHub parses commit
# messages and pull request bodies, not the tree. So the script describing the ban does not trip it,
# and the gate scans channels rather than files for a reason rather than an oversight.
#
# Checking only commits would have missed the specimen until the moment it fired, which is the
# whole point of the gate. `make close` runs the offline half over the working branch; CI runs both
# on the pull_request event, where the number exists. That split is citecheck.sh's ruling applied
# unchanged: the local run is runnable, the CI run is binding, and a half that cannot ask its
# question does not report green.
#
# ## Trigger coverage, stated because an under-matching trigger fails silently
#
# * The keywords are GitHub's own set: close/closes/closed, fix/fixes/fixed, resolve/resolves/
#   resolved, case-insensitive.
# * A reference is `#N`, `GH-N`, or `owner/repo#N`, and **each of the three also closes wrapped in a
#   markdown link** — `Close [#543](url)`. All four are matched. The fourth arm is grave #595:
#   this note used to read *"all three close, so all three are matched"*, which is the sentence
#   enumerating the coverage certifying the hole in it. `Close [#543](…)` in #594's **Next** section
#   scanned clean here, and closed #543 on the merge. **The missed form was the house style** — every
#   reference in every PR body in this repo is a markdown link, because that is the form `citecheck`
#   resolves — so the trigger matched the three forms GitHub *documents* and missed the one the corpus
#   *uses*. Note that the two axes are independent: this file had argued its **adjacency** boundary
#   carefully and never asked the same question about the **reference form**.
# * Three further forms are **untested and unmatched**, tracked in #596 rather than guessed at: a
#   bare issue URL, an autolink, and a reference-style `[#N][ref]`. They are not matched
#   conservatively, because the paragraph below rules on that axis in the other direction, and the
#   experiment that would settle them costs a merged pull request on the default branch.
# * Adjacency is **same-line**: keyword, an optional colon, spaces or tabs, then the reference.
#   This is the form that fired. A keyword separated from its reference by a newline is a
#   **stated under-match**: whether GitHub binds across a line break is version-dependent
#   behaviour this script would be asserting rather than checking, and the wrong direction to
#   guess in is the one that fails correct prose. `Fixed in #313` does not match and must not —
#   the keyword is not adjacent, GitHub does not act on it, and it is the recommended phrasing.
# * Ordinary prose is untouched. "Fixed" as a changelog heading, "this fixes the bug", "closes the
#   seam" — none are adjacent to a reference. The construct being banned is narrow.
#
# The domain is printed whether or not anything fails: *coverage is a claim*, and a checker that
# reports OK without saying over what has made a silent claim about its own population.
#
# ## Falsification record
#
# Run over the commit that caused #314: FAIL, exit 1. Run over the recommended phrasings: no hits.
# Run over the pull request that introduced this script: **FAIL, two constructs** — the graves
# section had quoted the specimen verbatim, so the body announcing the ban would have closed the same
# issue a second time. Prose about a defect describes the form; it does not instantiate it, and the
# checker enforced that against its own author before a human read the page. That third case is the
# one worth keeping: the first two were arranged, and this one was not.
#
# `--body` arrived with its own four-arm control (`internal/testenv/closebody_test.go`), five since
# grave #595 split the reference-form arm in two, and both of
# its refusals were watched die under their own mutation: with the empty-file guard removed the form
# prints `0 lines scanned, 0 banned constructs` and exits 0, which is the exact green it exists to
# prevent. Its first real run was over the body of the PR that added it — 161 lines against the `--pr`
# form's 162, the difference being the title, which is this form's stated under-match rather than an
# off-by-one.
#
# One arithmetic note, because the figure was checked against its subject and did not match. The
# line count is one lower than `git log --format=%B <range> | grep -c ''` emits: command
# substitution strips trailing newlines, so a message ending in a blank line loses it. The count is
# therefore exactly what the scanner saw, which is the number worth printing, and the lost lines are
# always trailing blanks and can hold no construct. Stated rather than papered over — an instrument
# whose self-report is off by one for an unexplained reason is the more expensive kind of small
# error, and this one has a reason.

set -eu

prmode=0
prnum=""
bodymode=0
bodyfile=""
base="${1:?usage: closecheck.sh <rev> | <base> <head> | --worktree [base] | --pr <number> | --body <file>}"
head="${2-}"
case "$base" in
--pr)
	prmode=1
	prnum="${2:?--pr needs a number}"
	;;
--body)
	bodymode=1
	bodyfile="${2:?--body needs a file}"
	;;
--worktree)
	base="${2-main}"
	head="HEAD"
	;;
*)
	if [ -z "$head" ]; then
		head="$base"
		base="$base^"
	fi
	;;
esac

# scan reads text on stdin and prints one `<keyword> <reference>` row per banned construct.
#
# The classification lives in this one awk program rather than half here and half in the shell, so
# there is a single place to read the trigger from — citecheck.sh's structure, for its reason.
#
# **No apostrophes inside this program**: it is a single-quoted shell string, and one apostrophe
# ends the quote and breaks the script at parse time. citecheck.sh carries the same warning after
# the same mistake was made in it.
scan() {
	awk '
	{
		line = $0
		while (match(line, /[Cc][Ll][Oo][Ss][Ee][SsDd]?|[Ff][Ii][Xx]([Ee][SsDd])?|[Rr][Ee][Ss][Oo][Ll][Vv][Ee][SsDd]?/)) {
			kw = substr(line, RSTART, RLENGTH)
			rest = substr(line, RSTART + RLENGTH)
			line = rest
			# Optional colon, then spaces or tabs only — adjacency is same-line, see the
			# trigger-coverage note in the header for why a newline is a stated under-match.
			if (match(rest, /^:?[ \t]+/) || match(rest, /^:/)) {
				after = substr(rest, RLENGTH + 1)
			} else {
				continue
			}
			# A reference in any of the three closing forms, each optionally wrapped in a
			# markdown link: the optional `[` is grave #595, where one bracket between the
			# keyword and the `#` was the entire hole, in the form this repo writes every
			# reference in.
			if (match(after, /^\[?#[0-9]+/) ||
			    match(after, /^\[?[Gg][Hh]-[0-9]+/) ||
			    match(after, /^\[?[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+#[0-9]+/)) {
				ref = substr(after, RSTART, RLENGTH)
				# Report the reference GitHub acts on rather than the markdown around it, so
				# the remedy line below offers a phrasing rather than a half-open bracket —
				# and a third field saying whether it was wrapped, because a caller who
				# greps the body for the reported text has to find it.
				linked = "bare"
				if (sub(/^\[/, "", ref)) {
					linked = "linked"
				}
				print kw " " ref " " linked
			}
		}
	}
	'
}

label=""
found=""
nlines=0

if [ "$prmode" -eq 1 ]; then
	if ! command -v gh >/dev/null 2>&1; then
		echo "FAIL  gh is not installed, so the PR body was not scanned."
		echo "      This is not a pass. A check that could not ask its question does not"
		echo "      get to report green; CI is where this half is binding."
		exit 1
	fi
	# **Explicit, though `set -e` already catches it here.** This assignment is not a pipeline, so
	# a failing `gh` does abort the script — which is why this arm exits 1 where `citecheck.sh`'s
	# equivalent exited 0 until grave #365 fixed it. Relying on that is relying on the absence
	# of a `|` that any later edit could add, and the failure it guards is silent by construction:
	# an empty body scans clean. So the status is read here, and the message names the cause the
	# way the guard above names its own. Both scripts' `--pr` arms are asserted to refuse a green
	# on a failing fetch by `TestPRFetchFailureIsNeverAPass` (`internal/testenv`).
	if ! body="$(gh pr view "$prnum" --json title,body --jq '.title + "\n" + .body')"; then
		echo "FAIL  PR #$prnum's body could not be fetched, so it was not scanned."
		echo "      This is not a pass, for the same reason a missing gh is not. Common causes"
		echo "      are an API rate limit, an unauthenticated gh, and a PR number that does not"
		echo "      exist — run \`gh pr view $prnum\` to see which, then re-run this."
		exit 1
	fi
	label="PR #$prnum (title and body)"
	nlines="$(printf '%s\n' "$body" | grep -c '' || true)"
	found="$(printf '%s\n' "$body" | scan | sort -u)"
elif [ "$bodymode" -eq 1 ]; then
	if [ ! -r "$bodyfile" ]; then
		echo "FAIL  $bodyfile could not be read, so no body was scanned."
		echo "      This is not a pass, for the reason a missing gh is not: a check that could"
		echo "      not ask its question does not get to report green."
		exit 1
	fi
	nlines="$(grep -c '' <"$bodyfile" || true)"
	if [ "$nlines" -eq 0 ]; then
		echo "FAIL  $bodyfile is empty, so the scan below would have found nothing in nothing."
		echo "      An empty file is the likelier of this form's two mistakes — a redirect that"
		echo "      went elsewhere leaves a readable file with no body in it."
		exit 1
	fi
	label="the body file $bodyfile"
	found="$(scan <"$bodyfile" | sort -u)"
else
	msgs="$(git log --format='%B' "$base..$head" || true)"
	label="commit messages in $(git rev-parse --short "$base")..$(git rev-parse --short "$head")"
	nlines="$(printf '%s\n' "$msgs" | grep -c '' || true)"
	found="$(printf '%s\n' "$msgs" | scan | sort -u)"
fi

nfound="$(printf '%s' "$found" | grep -c '' || true)"

if [ "$nfound" -gt 0 ]; then
	printf '%s\n' "$found" | while read -r kw ref linked; do
		echo "FAIL  \"$kw $ref\" — a closing keyword adjacent to a reference."
		if [ "$linked" = linked ]; then
			echo "      It reads \"$kw [$ref](…)\" in the text: wrapping a reference in a markdown"
			echo "      link does not defuse it, and this repo writes every reference that way."
			echo "      Grave #595 is the specimen, and it is why the report above is normalised."
		fi
		echo "      GitHub will close $ref on merge, whatever the sentence around it says:"
		echo "      the parser reads tokens, not negations. Write \"Landed in $ref\","
		echo "      \"Filed, deferred: $ref\", or \"see $ref\", and close the issue by hand"
		echo "      with \`gh issue comment\` then \`gh issue close\` so the lesson lands first."
	done
fi

printf 'closecheck %s: %d lines scanned, %d banned constructs\n' "$label" "$nlines" "$nfound"

if [ "$nfound" -gt 0 ]; then
	echo "closecheck: prose in this PR would change an issue state as a side effect. See grave #314."
	exit 1
fi
