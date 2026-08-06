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

set -eu

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
	awk -F'\t' "$@" '
	function quote(label, e, i, o,   r) {
		if (e == 0) r = (i == 0 ? "n/a (empty diff)" : "1:inf (no engine lines)")
		else r = sprintf("1:%.1f", i / e)
		printf "%-70s engine %d / instrument %d = %s   (other %d)\n", label, e, i, r, o
	}
	# destination resolves a rename row to the path the code now lives at:
	# `a/{b => c}/d.go` -> `a/c/d.go`, and the bare `b => c` form -> `c`.
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

if [ "${1-}" = "--window" ]; then
	n="${2:?--window needs a count}"
	end="${3-HEAD}"
	# --first-parent: one entry per merge to main, which is one PR. The window is a sample and
	# its length is a claim; state it wherever the figure is quoted.
	for merge in $(git rev-list --first-parent -n "$n" "$end"); do
		git diff --numstat "$merge^" "$merge"
		echo "# $(git rev-parse --short "$merge")  $(git log -1 --format=%s "$merge" | cut -c1-56)"
	done | report -v TOTAL="WINDOW TOTAL ($n merges ending $(git rev-parse --short "$end"))"
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
