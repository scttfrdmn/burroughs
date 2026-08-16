#!/usr/bin/env sh
# citecheck.sh — every citation a diff adds must resolve to the artifact it names.
#
# Two guessed issue numbers reached a working tree in consecutive PRs (`#283`, which resolved
# to the *wrong* artifact, and `#284`, which did not exist when it was typed and then happened
# to be the number the filing came back with). Both were self-caught; both were saved by luck,
# and the third would not be. So the check exists, and it is the mechanical half of a rule this
# repo already states in prose — *a deferral's citation outlives its subject*, *a self-citation
# must resolve like any other*, *fixtures cite the suite, and the citations are checked*.
#
# Usage:
#   scripts/citecheck.sh <rev>              # one commit, against its first parent
#   scripts/citecheck.sh <base> <head>      # a range, e.g. a PR's merge base to its tip
#   scripts/citecheck.sh --worktree [base]  # base (default `main`) against the working tree
#
# The two revision forms match ratio.sh deliberately: read the sibling before writing the
# reader. Both tools answer a question about *a diff*, so both take a diff the same way.
#
# `--worktree` exists because the local case is the *uncommitted* one, and leaving it out would
# repeat the specimen this check is a sibling of: the #281 sweep ran `git grep`, whose domain is
# tracked files, and so excluded exactly the new unstaged region the defect lived in. A guessed
# issue number is in the working tree before it is anywhere else — that is where it is cheapest
# to catch and where a commit-range-only tool cannot look.
#
# ## What is binding and what is printed
#
# Three checks, and they are not equally strong. Saying which is which is the point, because a
# tool that gates on existence while its name suggests it gates on correctness is testimony
# about itself:
#
#   1. **ADR citations resolve — binding, offline.** `decision NNNN` / `ADR NNNN` must have a
#      matching `docs/decisions/NNNN-*.md`. Needs no network, so this half runs everywhere and
#      always, including on a plane.
#   2. **Issue citations resolve — binding, needs the network.** Every `#NNNN` added by the diff
#      must be a real issue or PR in this repo. This is the half that catches a guessed number.
#   3. **`grave #N` carries `type:grave` — binding, needs the network.** The one *title-shaped*
#      claim in the set that has a mechanical oracle: a citation whose immediately preceding word
#      is "grave" must resolve to an **issue** (not a PR) labelled `type:grave`. `#283`'s failure
#      mode was resolving to the wrong *kind* of artifact, and existence alone cannot see that.
#
# What is deliberately **not** gated: whether the resolved title matches the sentence citing it.
# There is no oracle for that — agreement between a citation's context and an issue's title is a
# judgement, and a fuzzy word-overlap gate would fail on correct prose and pass on wrong prose,
# which is worse than no gate. So the title is **printed beside every citation** for the reviewer
# and the CI log, and the verdict channel carries only what a machine can decide. Verdict channel
# and mechanism channel are different instruments; this script uses both and says which.
#
# ## The domain is printed, always
#
# The last line reports the range, the number of added lines scanned, and the count of
# citation-shaped tokens broken down by what each turned out to be — *coverage is a claim*, and a
# checker that reports "OK" without saying over what has made an unstated claim about its own
# population. A diff that cites nothing passes and says so in those words: zero citations is a
# legitimate state, and it is not the same fact as zero failures.
#
# **It says "citation-shaped tokens" and not "citations" because two of the categories are not
# citations** — a qualified reference is one this repo cannot resolve, and a `verb` is a Go format
# flag that was never a reference at all (#308). While the total was called the citation count, the
# `%#02x` phantom made "12 citations, all resolving" an over-count, and the only number that had
# ever been checkable — how many *issue* citations were resolved — was buried inside it. The
# breakdown is the fix: the total counts what the extractor matched, and each category says what it
# was.
#
# ## Trigger coverage, stated because an under-matching trigger fails silently
#
# * `#NNNN` is matched as `#` plus a run of digits, accepted only at 1–4 digits. A six-digit hex
#   colour is therefore skipped whole rather than shredded into a false four-digit citation.
#   (Written without an example, because this file is in the diff it checks: the first draft's
#   example number was itself scanned, reported as unresolvable, and was right to be. **The hazard
#   is not this file — it is any file the diff touches.** A `CHANGELOG.md` paragraph explaining a
#   dangling-citation defect quoted three unqualified numbers as examples and became three dangling
#   citations, on this checker's own PR. Prose about a citation defect describes the form; it does
#   not instantiate it.)
# * The grave rule triggers on the *immediately preceding* word — `grave #78`, `graves #78`,
#   `grave issue #78` — and on a following run of numbers joined by `/`, `,`, or `and`, which is
#   how this repo writes `graves #78/#105`. It deliberately does **not** trigger on the word
#   appearing anywhere earlier in the line: "sweep after a grave … see #262" would then demand a
#   label #262 has no reason to carry. That under-match is the stated cost, and it is the right
#   direction of error for a gate — a missed grave citation is caught by review, a false demand
#   trains people to work around the tool.
# * **An ADR citation is a four-digit number, and a one-digit one is not a citation at all.** ADRs
#   number their own decisions — 0027 has five, 0028 three — and this repo refers to them in prose
#   as "decision 3", "decisions 2 and 3", "0029 decision 2". The first version of this trigger read
#   any run of digits after the word, so every such sentence became a citation to
#   `docs/decisions/3-*.md` and failed. That is an **over**-matching trigger predicate, the mirror
#   of the under-matching one the grave rule's note above is careful about, and it fails in the
#   worse direction for a gate: an under-match misses a finding, an over-match makes the gate
#   unpassable for correct prose and *teaches people to work around the tool*. Measured on merged
#   history rather than argued: `6a36e97` (the commit that added ADR 0028) fails this check three
#   times, on its own record's headings, and so did every ADR with numbered decisions. The
#   narrowing is the artifact's own convention — every file in `docs/decisions/` is `NNNN-`, all 29
#   of them — so four digits is what a citation looks like and anything shorter is prose.
#   A 2- or 3-digit run is neither shape and is reported as such (phase 1b) rather than dropped,
#   because dropping it would trade the over-match for the silent under-match.
# * Deleted lines are not scanned. A diff is responsible for the citations it *adds*; the ones it
#   removes are the previous author's, and re-litigating them turns every edit into a sweep.
#
# The per-diff domain is the reason a *one-time* repo-wide sweep was run at authorship rather than
# assumed away: this check can only ever see what a diff adds, so everything already committed is
# outside its population by construction. That sweep resolved 63 `grave #N` citations and flagged
# 10 — five graves missing the label, five citations naming the wrong artifact — triaged in #286.
# A checker that starts clean on a corpus it never read would be claiming coverage it does not
# have, which is the law it was written under.

set -eu

base="${1:?usage: citecheck.sh <rev> | <base> <head> | --worktree [base]}"
head="${2-}"
if [ "$base" = "--worktree" ]; then
	base="${2-main}"
	head="" # the empty head *is* the working tree, for git diff and for the label below
elif [ -z "$head" ]; then
	head="$base"
	base="$base^"
fi

diffout="$(git diff "$base" $head || true)"

# In worktree mode, `git diff` is not the whole working tree: an **untracked** file is invisible
# to it, which is the tracked-files defect this mode was added to avoid, one layer in. A new file
# is the most likely home for a freshly guessed number, so its lines are appended to the stream as
# synthetic additions — `+++` first, so the paragraph join below cannot weld a new file's opening
# line to the previous hunk's tail. Found while falsifying the check: the first `--worktree` run
# could not see this script.
if [ -z "$head" ]; then
	for f in $(git ls-files --others --exclude-standard); do
		diffout="$diffout
+++ b/$f
$(sed 's/^/+/' "$f")"
	done
fi
nlines="$(printf '%s\n' "$diffout" | grep '^+' | grep -v '^+++' | grep -c '' || true)"

# extract prints one `<kind> <number>` row per citation the diff adds: `issue N`, `grave N`, or
# `adr N`. The classification lives entirely in this awk program rather than half here and half
# in the shell, so there is one place to read the trigger from.
#
# **It joins consecutive added lines before matching, and that is not a nicety.** The first draft
# matched the `+` lines one at a time and missed `ADR 0025` on this script's own PR, because the
# citation was split by a prose wrap — `... citing ADR` / `0025's carve-out ...`. That is #78 /
# #80 / #105 exactly, the wrapped-lead defect, committed by the checker written to enforce the law
# about it, and found the only way it can be: by running the instrument over the diff that
# introduced it (*artifacts become oracles*). `internal/testenv`'s law-index reader had already
# paid for this and split-then-joins for the same reason; read the sibling.
#
# The join stops at any non-added line — a context line, a deletion, a hunk header, a `+++` file
# header — so the unit is a run of consecutive added lines, which is what a wrapped paragraph is.
# It cannot fabricate a citation by welding the tail of one file's hunk to the head of another's.
extract() {
	awk '
	function emit(kind, n) {
		if (length(n) >= 1 && length(n) <= 4) print kind " " n
	}
	function scan(s,   t, tok, gap, num, isgrave, prevgrave, tail) {
		# ADR citations first: `decision 0025`, `decisions 0025`, `ADR 0025`. **Four digits,
		# because that is the filename convention** — see the trigger-coverage note above on why a
		# one-to-three digit run after the word is a sub-decision reference and not a citation.
		t = s
		while (match(t, /([Dd]ecision[s]?|ADR[s]?)[ ]+[0-9][0-9]*/)) {
			tok = substr(t, RSTART, RLENGTH)
			sub(/^[^0-9]*/, "", tok)
			if (length(tok) == 4) {
				emit("adr", tok)
			} else if (length(tok) >= 2) {
				# A 2- or 3-digit run is neither: no ADR is spelled that way and no record has 10
				# numbered decisions. Reported rather than dropped, since dropping it is the
				# under-match this narrowing could otherwise introduce.
				emit("adrshort", tok)
			}
			t = substr(t, RSTART + RLENGTH)
		}

		# Issue citations, left to right, carrying the grave-run state forward.
		t = s
		prevgrave = 0
		while (match(t, /#[0-9][0-9]*/)) {
			gap = substr(t, 1, RSTART - 1)
			num = substr(t, RSTART + 1, RLENGTH - 1)
			t = substr(t, RSTART + RLENGTH)

			# **A `#` that is a Go format-verb flag, not a citation** (#308). The narrow
			# hex verb `%#02x` put its own flag-and-width into this checkers output as a
			# citation, and because the number it landed on names a real grave, the gate
			# printed `ok` beside a field width — a *phantom resolved citation*, which is
			# worse than a false alarm. (The number is deliberately not written here: see
			# this files header on prose about a citation defect describing the form
			# rather than instantiating it. The first draft of this block wrote it out,
			# and this checker duly resolved it against an unrelated LEB128 grave.) A
			# false alarm is
			# visibly wrong; this asserted that a citation had been checked and found
			# good where there was no citation at all, and it inflated the total the
			# last line prints, so "all resolving" was an over-count nobody could
			# falsify from the green.
			#
			# The predicate is the **verb form**, not the two call sites that happened
			# to have one: a `#` whose preceding run is `%` plus Go flag characters
			# (`+-0#`). `%#x` is invisible only because no digits follow it, and `%#4x`
			# would have been the next specimen — the population is Go format verbs,
			# not the two files a grep found.
			#
			# **A space is deliberately not in the flag class**, though `% d` is a real
			# verb: the ordinary English "100% #298" would otherwise parse as a verb and
			# a genuine citation would stop being resolved. That is the `pre-#298`
			# direction of error, an exemption wearing a fix, and this arm sits one
			# character away from it — so the under-match on the vanishingly rare
			# space-flag-then-hash form is taken on purpose. (Also not written out: with
			# the space excluded from the class, spelling that verb here would emit its
			# width as a bare citation, which is the same self-instantiation the block
			# above records.)
			#
			# Printed and counted rather than dropped, on the precedent the next arm
			# sets: a token that vanished from the output would be an exemption
			# mechanism. **No apostrophes in this block** — the awk program is inside a
			# single-quoted shell string, which is why its neighbours read "this
			# checkers own header"; the first draft of this comment used one and broke
			# the script at parse time.
			if (match(gap, /%[-+0#]*$/)) {
				print "verb " substr(gap, RSTART, RLENGTH) "#" num
				prevgrave = 0
				continue
			}

			# **A bare `#N` names *this* repo, so a cross-project citation needs a
			# qualifier or it is dangling by construction.** `umami#396` is the
			# GitHub-conventional form and is what this arm recognizes: a token
			# butted directly against the `#`, no space. Emitted as `qualified` and
			# *printed* below rather than dropped — an unresolvable-by-design
			# reference that vanished from the output would be an exemption
			# mechanism, and the count is the only thing making it reviewable.
			#
			# Reported as a *qualified reference* and not as a cross-project
			# citation, because this arm cannot tell those apart: a markdown anchor
			# whose slug opens with digits matches identically. Naming it what the
			# match actually establishes is the difference between a note and a
			# lying witness. The previous behaviour failed such a link as a bad
			# issue citation, a false positive in the other direction. (No literal
			# example here, per this files own header: an example would be scanned
			# by the very run it documents, and the first draft of this comment was.)
			#
			# Not resolved against that repo: doing so would make this checkers
			# verdict depend on a repo whose numbering nobody here controls, and a
			# gate that can go red because a sibling project renumbered is a gate
			# that will be disabled. Provenance is asserted by the qualifier; the
			# claim it makes is checkable by a human following it, which is all a
			# cross-repo citation can honestly offer.
			#
			# The qualifier must **end** in an alphanumeric, and that is not
			# cosmetic: the first draft accepted a trailing `-` or `.`, so the
			# ordinary English `pre-#298` parsed as project `pre-`, and a real
			# citation was silently exempted from resolution instead of being
			# checked. Found by running this checker over the diff that first
			# wrote that phrase — *artifacts become oracles* — and it is the
			# dangerous direction of error, since the whole cost of this arm is
			# that whatever it claims is unresolvable stops being resolved. No
			# `owner/repo#N` ends in punctuation, so the tightening costs nothing.
			if (gap ~ /[A-Za-z]([A-Za-z0-9_.-]*[A-Za-z0-9])?$/) {
				match(gap, /[A-Za-z]([A-Za-z0-9_.-]*[A-Za-z0-9])?$/)
				if (length(num) >= 1 && length(num) <= 5)
					print "qualified " substr(gap, RSTART, RLENGTH) "#" num
				prevgrave = 0
				continue
			}

			# The trigger: the word immediately before this citation, with a
			# sentence-ish break stripped so an earlier mention cannot reach it.
			tail = tolower(gap)
			sub(/^.*[.;:]/, "", tail)
			isgrave = (tail ~ /graves?[ ]*(issue[ ]*)?$/)
			# ... or a continuation of a `graves #78/#105` run.
			if (!isgrave && prevgrave && gap ~ /^[ ]*([\/,]|and)[ ]*$/) isgrave = 1

			if (length(num) < 1 || length(num) > 4) { prevgrave = 0; continue }
			emit(isgrave ? "grave" : "issue", num)
			prevgrave = isgrave
		}
	}
	function flush() {
		if (buf != "") scan(buf)
		buf = ""
	}
	/^\+\+\+/ { flush(); next }
	/^\+/ {
		buf = (buf == "" ? "" : buf " ") substr($0, 2)
		next
	}
	{ flush() }
	END { flush() }
	'
}

cites="$(printf '%s\n' "$diffout" | extract | sort -u)"
ncites="$(printf '%s' "$cites" | grep -c '' || true)"

fail=0
adrs=0
issues=0
graves=0
foreigns=0
verbs=0

# Phase 0a: Go format verbs whose `#` flag looked like a citation (#308). Not resolved, because
# there is nothing to resolve; printed because a silently dropped token is an exemption mechanism.
for v in $(printf '%s\n' "$cites" | awk '$1 == "verb" { print $2 }'); do
	verbs=$((verbs + 1))
	echo "note  $v -> Go format verb flag, not a citation (see #308)"
done

# Phase 0: qualified references — a cross-project citation, or a markdown anchor shaped alike. Named,
# counted, and deliberately not resolved: see the `qualified` arm in extract() for why a gate must
# not depend on a sibling repo's numbering, and why this line does not claim which kind it is.
for q in $(printf '%s\n' "$cites" | awk '$1 == "qualified" { print $2 }'); do
	foreigns=$((foreigns + 1))
	echo "note  $q -> qualified reference, not resolved against this repo (provenance is the qualifier)"
done

# Phase 1: ADR citations, offline.
for n in $(printf '%s\n' "$cites" | awk '$1 == "adr" { print $2 }'); do
	adrs=$((adrs + 1))
	# The glob is the resolution: `docs/decisions/0025-*.md`. A number with no file is a
	# citation to a decision that does not exist, which is the ADR face of a guessed issue.
	found="$(find docs/decisions -maxdepth 1 -name "$n-*.md" 2>/dev/null | head -1)"
	if [ -z "$found" ]; then
		echo "FAIL  decision $n -> no docs/decisions/$n-*.md"
		fail=1
	else
		echo "ok    decision $n -> ${found#docs/decisions/}"
	fi
done

# Phase 1b: a `decision NN` / `decision NNN` that is neither an ADR citation nor a sub-decision
# reference. Binding, because the alternative to reporting it is silently dropping it, and this
# arm exists precisely so narrowing the ADR trigger to four digits could not become an under-match.
for n in $(printf '%s\n' "$cites" | awk '$1 == "adrshort" { print $2 }'); do
	adrs=$((adrs + 1))
	echo "FAIL  decision $n -> ADR citations are four digits (docs/decisions/NNNN-*.md);" \
		"a one-digit reference is a record's own numbered decision. This is neither."
	fail=1
done

# Phase 2 and 3: issue citations, which need the network.
#
# `gh api repos/{owner}/{repo}/issues/N` rather than `gh issue view N`, and the difference is
# load-bearing: `gh issue view` resolves a *PR* number happily and reports it as an issue, so it
# cannot answer check 3 at all. The REST payload carries `pull_request` (present iff the number
# is a PR) plus the labels, in one request per citation.
need_gh="$(printf '%s\n' "$cites" | awk '$1 == "issue" || $1 == "grave"' | grep -c '' || true)"
if [ "$need_gh" -gt 0 ]; then
	if ! command -v gh >/dev/null 2>&1; then
		echo "FAIL  gh is not installed, so $need_gh issue citations were not resolved."
		echo "      This is not a pass. The offline half above ran; the network half did not,"
		echo "      and a check that could not ask its question does not get to report green."
		echo "      CI is where this half is binding (see .github/workflows/ci.yml)."
		exit 1
	fi
	for row in $(printf '%s\n' "$cites" | awk '$1 == "issue" || $1 == "grave" { print $1 ":" $2 }'); do
		kind="${row%:*}"
		n="${row#*:}"
		if ! meta="$(gh api "repos/{owner}/{repo}/issues/$n" \
			--jq '(if .pull_request then "pr" else "issue" end) + "\t" + ([.labels[].name] | join(",")) + "\t" + .title' 2>/dev/null)"; then
			echo "FAIL  #$n -> does not resolve: no such issue or PR in this repo"
			fail=1
			continue
		fi
		what="$(printf '%s' "$meta" | cut -f1)"
		labels="$(printf '%s' "$meta" | cut -f2)"
		title="$(printf '%s' "$meta" | cut -f3)"
		if [ "$kind" = grave ]; then
			graves=$((graves + 1))
			case ",$labels," in
			*,type:grave,*)
				if [ "$what" != issue ]; then
					echo "FAIL  grave #$n -> resolves to a PR, not an issue: $title"
					fail=1
				else
					echo "ok    grave #$n -> [$labels] $title"
				fi
				;;
			*)
				echo "FAIL  grave #$n -> $what without type:grave [${labels:-no labels}]: $title"
				echo "      Cited as a grave; the graveyard is \`label:type:grave\` and this is not in it."
				fail=1
				;;
			esac
		else
			issues=$((issues + 1))
			echo "ok    #$n -> $what [${labels:-no labels}] $title"
		fi
	done
fi

# The domain, printed whether or not anything failed. A checker that says OK without saying over
# what has made a silent claim about its own coverage.
printf 'citecheck %s..%s: %d added lines, %d citation-shaped tokens (%d issue, %d grave, %d ADR, %d qualified, %d verb)\n' \
	"$(git rev-parse --short "$base")" \
	"$([ -n "$head" ] && git rev-parse --short "$head" || echo worktree)" \
	"$nlines" "$ncites" "$issues" "$graves" "$adrs" "$foreigns" "$verbs"

if [ "$fail" -ne 0 ]; then
	echo "citecheck: at least one citation does not resolve to the artifact it names."
	exit 1
fi
if [ "$ncites" -eq 0 ]; then
	echo "citecheck: this diff cites nothing — zero citations, which is not the same fact as zero failures."
fi
