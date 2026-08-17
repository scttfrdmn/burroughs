#!/usr/bin/env sh
# ratio.sh — the instrument-to-engine ratio for a commit range, under the uniform comparator.
#
# CLAUDE.md requires every PR to quote this figure, and #117 requires the *command* to be
# recorded so the next trailing window is re-measured rather than re-invented. This is that
# command. It is not wired into `make check`: the ratio is a quoted figure, not a gate (see the
# ruling in CLAUDE.md), and a script CI runs would be a gate whether or not it was called one.
#
# Usage:
#   scripts/ratio.sh <rev>              # one commit, against its first parent
#   scripts/ratio.sh <base> <head>      # a range, e.g. a PR's merge base to its tip
#   scripts/ratio.sh --window N [rev]   # the trailing N first-parent merges ending at rev
#
# `--window` takes an explicit endpoint precisely so a historical window can be measured *without
# checking anything out*. The first draft defaulted the endpoint to HEAD and nothing else, which
# made re-measuring a past era require a `git checkout` — and that checkout failed on a dirty tree,
# in the very session that wrote the script. A measurement tool that needs the working tree moved
# to the thing it measures will be run against whatever is convenient instead.
#
# The comparator is fixed by ruling (Scott, PR #113) and takes no arguments, which is the point:
#
#   engine     = non-test .go under the module path
#   instrument = _test.go anywhere, plus internal/gen, internal/spec, internal/testenv,
#                internal/interp/dispatchbench — tests, generators, harness
#   other      = everything else (markdown, YAML, testdata), reported but in neither column
#
# **Added lines, not net.** That is what the #113 quote used — 127/844 reproduces exactly as the
# added columns of its feature commit — and the two differ: net rewards deletion in a way that
# lets a refactor of instruments read as engine work. Stated here because a comparator whose
# summation rule is unwritten is a comparator that will be re-chosen by whoever measures next,
# which is the defect #113 ruled on wearing a different hat.
#
# internal/gen non-test code counts as instrument, which is the uniform rule's main mover against
# the pre-#113 era: generators are how tables are known to be right, not the tables themselves.
#
# POSIX sh, matching its two siblings in this directory rather than the bash the first draft
# reached for — the only bashisms were `set -o pipefail` and two here-strings, none of them
# needed. Read the sibling before writing the reader (#105/#108). The classification and the
# arithmetic then live entirely in the awk program below rather than half here and half there,
# which is what the here-strings were papering over: *one concept, one place*.
#
# ## The instrument column is split by provenance (Scott's directive, PR #339)
#
# "A number I can move by ordering work isn't a measure of your discipline. So split the
# instrument column: lines carried by the work, and lines ordered in review." So the
# instrument total keeps being quoted whole — it is the drift figure and #113 fixed its
# comparator — and beneath it the same lines are attributed to *whose* decision put them
# there, per commit, by a `Ratio-Class:` trailer:
#
#   Ratio-Class: carried
#   Ratio-Class: ordered — #339, Scott review 2
#
# Three properties, each of them a law in this repo wearing a shell script:
#
#   - **`ordered` must cite.** A claim that moves lines out of the column measuring the actor
#     is the actor classifying the actor (#113), so it points at an artifact outside the
#     actor — the review it names. An `ordered` with nothing after it is counted
#     unattributed and printed as uncited; the citation is quoted so review can refuse it.
#   - **Absence is `unattributed`, never `carried`.** A missing trailer is a measurement that
#     was not taken, and folding it into `carried` would make the silent default the
#     flattering one. Unattributed lines are excluded from the carried-only ratio, so a range
#     nobody annotated reports that instead of reporting a number.
#   - **`--window` cannot answer this at all.** `gh pr merge --squash` collapses a branch to
#     one commit, so per-commit provenance does not survive into main's history. That path
#     prints NOT AVAILABLE and names the reason: zero ordered lines would be a false verdict
#     where the honest report is an absent measurement.
#
# ## Two ledgers, and they are never read as one (Scott's ruling, PR #363)
#
# `Ratio-Class` and purpose classification answer different questions about different subjects,
# and the temptation is to treat an `ordered` trailer as though it argued about class:
#
#   - **`Ratio-Class` attributes lines *within* the instrument column** — who caused them. It
#     moves nothing out of the column and nothing between classes; the total is still quoted
#     whole, because the total is the drift figure.
#   - **Purpose classification decides whether the PR is product or instrument**, and that is
#     the quantity the two-consecutive-instrument-PRs stop condition counts (#159).
#
# So an order from a principal changes a PR's *priority* and never its class: it moves lines
# into the `ordered` bucket and leaves the PR exactly as product or instrument as it was. #345
# was instrument work, stayed instrument work, and the trailer only made the chair's share of
# it visible. Written here because the tension is a natural misreading and was in fact raised
# on #363 — "an order changes priority, never class" versus a `Ratio-Class: ordered #345`
# trailer reads as a contradiction until the two subjects are separated, and then it isn't one.
#
# **And a chair-ordered PR with a zero reward figure is a cost the chair caused, reported as
# such.** That is the trailer working as directed rather than an accounting embarrassment to be
# smoothed: "I'd rather see it than not" (Scott, same ruling). The `ordered` bucket is not an
# exemption surface — it is the chair's own line in the ledger.

set -eu

# comparator_awk is the classification half of the comparator, shared verbatim by the two
# reports below rather than pasted into each. The comparator is fixed by ruling (#113); a
# second copy is a second place for it to drift, which is the defect that ruling is about
# wearing a maintenance hat.
#
# destination resolves a rename row to the path the code now lives at:
# `a/{b => c}/d.go` -> `a/c/d.go`, and the bare `b => c` form -> `c`.
comparator_awk='
	function destination(p,   pre, mid, post, k) {
		k = index(p, "{")
		if (k > 0) {
			pre = substr(p, 1, k - 1)
			mid = substr(p, k + 1)
			post = substr(mid, index(mid, "}") + 1)
			mid = substr(mid, 1, index(mid, "}") - 1)
			sub(/^.* => /, "", mid)
			# `{a => b}` with an empty side leaves a doubled slash; harmless for the
			# prefix tests below, but normalized so a printed path is a real one.
			p = pre mid post
			gsub(/\/\//, "/", p)
			return p
		}
		if (p ~ / => /) {
			sub(/^.* => /, "", p)
			return p
		}
		return p
	}
	# The comparator, and the order of these tests is the comparator: _test.go wins over
	# everything, then the instrument directories, then any remaining .go is engine.
	function column(p) {
		p = destination(p)
		if (p ~ /_test\.go$/) return "i"
		if (p ~ /^internal\/(gen|spec|testenv)\// || p ~ /^internal\/interp\/dispatchbench\//)
			return (p ~ /\.go$/) ? "i" : "o"
		return (p ~ /\.go$/) ? "e" : "o"
	}
'

# report reads a stream of `<added> <deleted> <path>` numstat rows, interleaved with
# `# <label>` lines that close off a group, and prints one quoted ratio per group plus a total.
#
# **Tab-separated, and a rename's path needs resolving.** `--numstat` renders a rename as
# `internal/{text/internal => gen}/keywordgen/emit.go` — one field containing spaces and braces —
# so awk's default FS shreds it into three, and even a tab-split leaves a path that matches no
# prefix rule. Both mistakes were made here and both were caught the same way: 0014 is the *only*
# merge in forty with rename rows, its figure came out 1:2.8 from the script against 1:2.7 from
# the probe that had the tab right and the braces wrong, and a 0.1 disagreement between two
# supposedly equivalent readings was the tell. Two wrong figures agreeing would have shipped.
# The destination path is the one that classifies, since that is where the code now lives.
# The label row is `# <text>` and cannot collide with a numstat row, whose first field is a
# count. `-v` assignments come from the caller and must precede the program text, which is why
# they are spliced in ahead of the quoted script rather than appended after it.
report() {
	awk -F'\t' "$@" "$comparator_awk"'
	function quote(label, e, i, o,   r) {
		if (e == 0) r = (i == 0 ? "n/a (empty diff)" : "1:inf (no engine lines)")
		else r = sprintf("1:%.1f", i / e)
		printf "%-70s engine %d / instrument %d = %s   (other %d)\n", label, e, i, r, o
	}
	/^# / {
		quote(substr($0, 3), e, i, o)
		te += e; ti += i; to += o; groups++
		e = i = o = 0
		next
	}
	$1 == "-" { next }   # a binary file has no line counts
	{
		c = column($3)
		if (c == "e") e += $1
		else if (c == "i") i += $1
		else o += $1
	}
	END {
		if (groups > 1) {
			printf "\n"
			quote(TOTAL, te, ti, to)
		}
	}
	'
}

# classify answers a commit's provenance from its `Ratio-Class:` trailer, as
# `<class>\t<citation>`. Four answers, and the two failure answers are deliberately not
# `carried`: `uncited` is an ordered claim with nothing to point at, `unknown` is a value the
# comparator does not recognize. Both are summed as unattributed *and named*, because a
# malformed trailer that silently landed in the actor-flattering column is the same defect as
# no trailer at all, one step better hidden.
classify() {
	line=$(git log -1 --format=%B "$1" | grep -i '^Ratio-Class:' | head -1)
	value=$(printf '%s' "$line" | sed 's/^[Rr]atio-[Cc]lass:[[:space:]]*//;s/[[:space:]]*$//')
	rest=$(printf '%s' "${value#[Oo]rdered}" | sed 's/^[[:space:]]*//')
	case "$value" in
	'') printf 'unattributed\t' ;;
	[Cc]arried*) printf 'carried\t' ;;
	[Oo]rdered*)
		# A citation has to say something: an em-dash on its own is punctuation, not an
		# artifact outside the actor.
		case "$rest" in
		*[0-9A-Za-z]*) printf 'ordered\t%s' "$rest" ;;
		*) printf 'uncited\t' ;;
		esac
		;;
	*) printf 'unknown\t%s' "$value" ;;
	esac
}

# split_report reads the same stream `report` does — numstat rows closed by a `# <sha>\t<class>\t
# <citation>` label — and prints the provenance split plus its reconciliation. RANGEADD, MERGES
# and NCOMMITS come from the caller because the walk happens once: asking git for the per-commit
# sum a second time to compare against would be two readings of one quantity, which is how the
# rename bug above got two figures that had to disagree before either was doubted.
split_report() {
	awk -F'\t' "$@" "$comparator_awk"'
	function ratio(e, i,   r) {
		if (e == 0) return (i == 0 ? "n/a (nothing attributed here)" : "1:inf (no engine lines)")
		return sprintf("1:%.1f", i / e)
	}
	function show(label, k) {
		printf "    %-22s engine %5d / instrument %5d   (other %d)\n", label, ie[k], ii[k], io[k]
	}
	/^# / {
		sha = substr($1, 3); cls = $2; cite = $3
		if (cls == "unknown") { unknown = unknown sprintf("      %s  Ratio-Class: %s\n", sha, cite); cls = "unattributed" }
		else if (cls == "uncited") { uncited = uncited sprintf("      %s\n", sha); cls = "unattributed" }
		else if (cls == "ordered") cites = cites sprintf("      %s  %s\n", sha, cite)
		ie[cls] += e; ii[cls] += i; io[cls] += o
		add += e + i + o
		e = i = o = 0
		next
	}
	/^[[:space:]]*$/ { next }
	$1 == "-" { next }
	{
		c = column($3)
		if (c == "e") e += $1
		else if (c == "i") i += $1
		else o += $1
	}
	END {
		printf "\n  instrument column split by provenance — %d non-merge commit(s):\n", NCOMMITS
		show("carried by the work", "carried")
		show("ordered in review", "ordered")
		show("unattributed", "unattributed")
		printf "    carried-only ratio: engine %d / instrument %d = %s\n",
			ie["carried"], ii["carried"], ratio(ie["carried"], ii["carried"])
		if (ie["unattributed"] + ii["unattributed"] + io["unattributed"] > 0)
			print "      unattributed lines are excluded from it: a commit with no Ratio-Class\n      trailer is a measurement not taken, and folding it into carried would make\n      the silent default the flattering one."
		if (uncited != "") printf "    ORDERED WITHOUT A CITATION, counted unattributed:\n%s", uncited
		if (unknown != "") printf "    UNRECOGNIZED Ratio-Class value, counted unattributed:\n%s", unknown
		if (cites != "") printf "    ordered commits and their citations (challengeable, per #113):\n%s", cites
		if (add == RANGEADD)
			printf "    reconciled: per-commit added lines %d = range diff %d.\n", add, RANGEADD
		else {
			printf "    reconciliation: per-commit added lines %d against the range diff %d (%+d).\n", add, RANGEADD, add - RANGEADD
			# The gap is not reported as overlap and left there: the superseded-event count is
			# an independent derivation of the same number, so agreement explains it and
			# disagreement says a second cause is hiding under a plausible figure.
			if (add - RANGEADD == SUPERSEDED)
				printf "      Explained exactly: %d added-line event(s) were superseded by a later commit\n      in the same range — rewritten or added-then-deleted. Derived per line and keyed\n      by path, not inferred from the gap.\n", SUPERSEDED
			else
				printf "      UNEXPLAINED RESIDUAL: the gap is %+d but only %d added-line event(s) were\n      superseded. A second cause is in here — a rename the walk resolved differently,\n      a binary row, a commit outside the walk. Do not quote the split until it is named.\n", add - RANGEADD, SUPERSEDED
			if (FINALONLY > 0)
				printf "      %d line(s) in the range diff correspond to no added-line event in the walk,\n      which should be impossible: the walk is missing a commit that the range contains.\n", FINALONLY
		}
		if (MERGES > 0)
			printf "    %d merge commit(s) in range: skipped by the walk, present in the range diff.\n", MERGES
	}
	'
}

# split prints the provenance block for a range, or says why it cannot.
split() {
	b=$1
	h=$2
	commits=$(git rev-list --no-merges "$b..$h")
	if [ -z "$commits" ]; then
		printf '\n  provenance split: n/a — no non-merge commits in %s..%s.\n' \
			"$(git rev-parse --short "$b")" "$(git rev-parse --short "$h")"
		return
	fi
	rangeadd=$(git diff --numstat "$b" "$h" | awk -F'\t' '$1 != "-" { s += $1 } END { print s + 0 }')
	merges=$(git rev-list --merges "$b..$h" | wc -l | tr -d ' ')
	ncommits=$(printf '%s\n' "$commits" | wc -l | tr -d ' ')

	# The residual is **derived, not stated** (Scott's directive, PR #339): "overlap between commits
	# is exactly the set of lines touched more than once — compute it and check it equals 20. Stating
	# a residual you could compute is the thing this campaign keeps correcting; if it doesn't match,
	# there's a second cause hiding under a plausible number."
	#
	# So: every added-line *event* in the walk as a multiset keyed by `<path>\t<+line>`, against the
	# range diff's added lines as the same multiset. An event absent from the range's own diff was
	# superseded by a later commit — rewritten, or added then deleted — and the count of those must
	# equal the gap exactly. Keyed by path because line text repeats across files: an added blank
	# line in one file would otherwise cancel a superseded one in another, and a cancellation is the
	# failure mode this check exists to catch rather than commit.
	events=$(mktemp) || exit 1
	final=$(mktemp) || exit 1
	trap 'rm -f "$events" "$final"' EXIT INT TERM
	tag_added='/^\+\+\+ / { f = substr($0, 7); next } /^\+/ { print f "\t" $0 }'
	for c in $commits; do
		git show -U0 --format= "$c"
	done | awk "$tag_added" | sort >"$events"
	git diff -U0 "$b" "$h" | awk "$tag_added" | sort >"$final"
	superseded=$(comm -23 "$events" "$final" | wc -l | tr -d ' ')
	unexplained=$(comm -13 "$events" "$final" | wc -l | tr -d ' ')

	for c in $commits; do
		git show --numstat --format= "$c"
		printf '# %s\t%s\n' "$(git rev-parse --short "$c")" "$(classify "$c")"
	done | split_report -v RANGEADD="$rangeadd" -v MERGES="$merges" -v NCOMMITS="$ncommits" \
		-v SUPERSEDED="$superseded" -v FINALONLY="$unexplained"
}

if [ "${1-}" = "--window" ]; then
	n="${2:?--window needs a count}"
	end="${3-HEAD}"
	# --first-parent: one entry per merge to main, which is one PR. The window is a sample and
	# its length is a claim; state it wherever the figure is quoted.
	for merge in $(git rev-list --first-parent -n "$n" "$end"); do
		git diff --numstat "$merge^" "$merge"
		echo "# $(git rev-parse --short "$merge")  $(git log -1 --format=%s "$merge" | cut -c1-56)"
	done | report -v TOTAL="WINDOW TOTAL ($n merges ending $(git rev-parse --short "$end"))"
	cat <<-'EOF'

		  provenance split: NOT AVAILABLE for --window. The walk is over main's first-parent
		  commits, which here are squash merges: the branch commits that carried the
		  Ratio-Class trailers no longer exist, so no line can be attributed to whose decision
		  put it there. A commit pushed straight to main would still carry its own trailer, and
		  the walk does not distinguish the two — so this refuses rather than reporting a
		  partial attribution that would read as a whole one. Zero ordered lines would be a
		  verdict; this is an absent measurement, and they are different facts.
	EOF
	exit 0
fi

base="${1:?usage: ratio.sh <rev> | <base> <head> | --window N [rev]}"
head="${2-}"
if [ -z "$head" ]; then
	head="$base"
	base="$base^"
fi
{
	git diff --numstat "$base" "$head"
	echo "# $(git rev-parse --short "$base")..$(git rev-parse --short "$head")"
} | report
split "$base" "$head"
