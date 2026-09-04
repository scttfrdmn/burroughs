#!/bin/sh
# ab.sh — run a two- or three-arm benchmark A/B under grave #552's protocol, so that the
# protocol is executed rather than re-derived.
#
# Every clean measurement in this tree hand-rolled the same four steps: compile each arm to a
# binary up front, check the binaries' hashes distinct, run one `-count=1` round per arm per
# round, then benchstat two files. Eight ADRs did it by hand (0050, 0052, 0053, 0054, 0057,
# 0058, 0059, 0061) while `make bench` could not express any of it — one hardcoded output name,
# an echoed comparison nothing ran, and a `benchstat` over a single file, which prints no
# p-value at all (grave #612). **A protocol carried only by prose is re-derived per use, and
# the re-derivation is where a step gets dropped.**
#
# The steps are not interchangeable, and each is here for a failure that happened:
#
#   - **Arms as binaries, before any round runs.** Nothing compiles during measurement, so no
#     round pays for a build and the tree cannot be edited mid-run. Grave #552's specimen was
#     measured with the arms built in place.
#   - **Hashes compared, and the two arms must differ.** Identical arms agree perfectly and say
#     nothing, and the failure is silent: ADR 0059's second build landed in the first's
#     directory, so two arms were one binary twice, and the hash check is what caught it.
#   - **`-trimpath` on every build, which is what makes that check mean anything here.** Arms
#     are built in separate worktrees, and without `-trimpath` each binary embeds its own
#     directory in its debug info — so two builds of the *same commit* hash differently and the
#     check passes on a pair that is identical in every way it is measuring. `-trimpath` moves
#     no code; it removes the one difference that is an artifact of where the arm was built.
#   - **One `-count=1` round per arm, rounds driven from out here.** `go test -bench` does not
#     interleave: at `-count=4` it runs `A A A A B B B B`, and `-shuffle=on` permutes the two
#     blocks once rather than interleaving them (measured, grave #612). Two benchmark rows in
#     one binary are therefore exactly as sequential as two binaries, and no flag fixes it.
#     Sequential arms make run order a confounder perfectly correlated with the arm, and on this
#     hardware the thermal envelope moved 4.1–9.1% on unchanged code fifteen minutes apart —
#     further than most decisions under test.
#   - **Slots rotated.** With k arms, the arm at index i runs in slot (i+r) mod k in round r, so
#     each arm holds each position equally often once the round count is a multiple of k.
#   - **benchstat over named files, one per arm.** A p-value exists only for a comparison.
#
# What it does NOT do, so nobody reads more into a clean run than is here:
#
#   - It does not choose the criterion. Pre-registering what the numbers must show is a decision
#     doc's job; this script has no opinion about the verdict.
#   - It does not cross architectures. `scripts/xcheck-amd64.sh` is the amd64 half, and a claim
#     that needs both memory models needs both runs.
#   - It does not judge the null arm's spread. A floor is informative only when it is narrower
#     than the bar it is compared against, and that reading belongs to whoever set the bar.
#
# Usage:
#   scripts/ab.sh --pkg ./internal/interp/membench --base main --head HEAD
#   scripts/ab.sh --pkg ./internal/interp/rmwbench --base main --head worktree \
#                 --rounds 12 --benchtime 300x --null --bench Rmw
#
#   --pkg P         exactly one package (a `...` pattern is refused: `go test -c` takes one)
#   --base/--head   a git rev, or `worktree` for the tree as it stands, uncommitted included
#   --rounds N      rounds; each arm runs once per round at -count=1 (default 10)
#   --benchtime X   passed through as -test.benchtime (unset means `go test`'s own default)
#   --bench RE      benchmark filter, -test.bench (default .)
#   --null          a third arm built independently from --base's source, so the instrument's
#                   own resolution sits on the same board as the effect. Its hash must come out
#                   EQUAL to base's; a mismatch is a finding, since it means the base-vs-head
#                   hash check cannot tell a code difference from a build difference.
#   --graft         copy --head's copy of the package over every other arm's before building, so
#                   the measurement is one source and only the code under test differs. Needed
#                   whenever the benchmark was written alongside the change it measures, which is
#                   the usual case: without it, base's build simply fails on a missing directory.
#                   It is off by default and announces itself, because it is the one flag here
#                   that changes what the arms have in common.
#   --out DIR       where the per-arm logs land (default a fresh mktemp dir, printed at the end)

set -u

PKG=""
BASE=""
HEAD=""
ROUNDS=10
BENCHTIME=""
BENCHRE="."
WANT_NULL=0
WANT_GRAFT=0
OUTDIR=""

die() { printf 'ab.sh: %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--pkg) PKG="${2:-}"; shift 2 ;;
	--base) BASE="${2:-}"; shift 2 ;;
	--head) HEAD="${2:-}"; shift 2 ;;
	--rounds) ROUNDS="${2:-}"; shift 2 ;;
	--benchtime) BENCHTIME="${2:-}"; shift 2 ;;
	--bench) BENCHRE="${2:-}"; shift 2 ;;
	--null) WANT_NULL=1; shift ;;
	--graft) WANT_GRAFT=1; shift ;;
	--out) OUTDIR="${2:-}"; shift 2 ;;
	-h|--help) sed -n '2,69p' "$0"; exit 0 ;;
	*) die "unknown argument '$1' — see --help" ;;
	esac
done

[ -n "$PKG" ] || die "--pkg is required (e.g. ./internal/interp/membench)"
[ -n "$BASE" ] || die "--base is required (a git rev, or 'worktree')"
[ -n "$HEAD" ] || die "--head is required (a git rev, or 'worktree')"
case "$ROUNDS" in ''|*[!0-9]*) die "--rounds must be a positive integer, got '$ROUNDS'" ;; esac
[ "$ROUNDS" -ge 2 ] || die "--rounds $ROUNDS cannot rotate slots; the protocol needs at least 2"
# Not fatal — a 4-round smoke run of this script is a legitimate thing to do — but said out loud,
# because below 6 samples benchstat prints `± ∞` and no confidence interval, and a table whose
# spread is unstated is not the n=10-and-a-p-value the discipline asks for (decision 0005).
[ "$ROUNDS" -ge 6 ] ||
	printf 'ab.sh: NOTE %s rounds is under benchstat'\''s 6-sample floor: expect `± ∞` and no confidence interval, which is not a citable board.\n' "$ROUNDS"

REPO="$(git rev-parse --show-toplevel 2>/dev/null)" || die "not inside a git repository"

# `go test -c -o` compiles one package. A pattern that expands to several would build only the
# last, or fail — and the default of the `bench` target next door IS such a pattern, so this is
# the mistake a caller is most likely to arrive with.
NPKGS="$(cd "$REPO" && go list "$PKG" 2>/dev/null | grep -c .)" || NPKGS=0
[ "$NPKGS" = "1" ] ||
	die "--pkg '$PKG' names $NPKGS packages; this protocol compiles one arm per binary, so pass exactly one"

benchstat() { ( cd "$REPO" && go tool -modfile=tools/go.mod benchstat "$@" ); }

# Worktrees live outside the repo tree: a copy of the engine inside it answers every grep with a
# past version of the file, which has cost one measurement here already (CLAUDE.md's own rule).
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/ab-XXXXXX")" || die "mktemp failed"
[ -n "$OUTDIR" ] || OUTDIR="$SCRATCH/logs"
mkdir -p "$OUTDIR" || die "cannot create --out '$OUTDIR'"

# The paths are recomputed here rather than accumulated as the arms are created, because
# `tree_for` runs inside a command substitution: anything it appended to a variable would die
# with its subshell. The first version of this trap did accumulate, and it left three `prunable`
# worktree registrations behind on its first run — cleanup ran over an empty list and reported
# nothing wrong. `prune` is the second half, since the directories are also removed outright.
cleanup() {
	for t in base head null; do
		git -C "$REPO" worktree remove --force "$SCRATCH/src/$t" >/dev/null 2>&1 || true
	done
	rm -rf "$SCRATCH/src" 2>/dev/null || true
	git -C "$REPO" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# tree_for <arm> <rev|worktree> -> prints the directory holding that arm's source.
tree_for() {
	_arm="$1"; _rev="$2"
	if [ "$_rev" = "worktree" ]; then
		printf '%s\n' "$REPO"
		return 0
	fi
	git -C "$REPO" rev-parse --verify --quiet "$_rev^{commit}" >/dev/null ||
		die "arm $_arm: '$_rev' is not a commit — pass a rev, or the literal 'worktree'"
	_dir="$SCRATCH/src/$_arm"
	mkdir -p "$SCRATCH/src"
	git -C "$REPO" worktree add --detach --quiet "$_dir" "$_rev" ||
		die "arm $_arm: could not create a worktree of $_rev"
	printf '%s\n' "$_dir"
}

# build <arm> <tree> -> prints the test binary's path. -trimpath is load-bearing; see the header.
build() {
	_arm="$1"; _tree="$2"
	_bin="$SCRATCH/$_arm.test"
	( cd "$_tree" && go test -trimpath -c -o "$_bin" "$PKG" ) ||
		die "arm $_arm: 'go test -c $PKG' failed in $_tree — a build failure is not a slow arm"
	[ -x "$_bin" ] || die "arm $_arm: no binary at $_bin after a build that reported success"
	printf '%s\n' "$_bin"
}

# HASHER is the sha256 tool, resolved once from a preference list rather than assumed (grave #624).
#
# This read `shasum -a 256`, which is stock on macOS and **absent on the lab host** — so `make ab`
# worked from the day it landed and `make lab-ab` could never have run at all. No gate could see it:
# `make check` reaches nothing whose subject is another machine, so the defect waited for the first
# real use of the queue. Measured on both boxes rather than assumed:
#
#	                        dev box (darwin)   janus.local (Rocky 9)
#	sha256sum               /sbin              yes
#	shasum -a 256           /usr/bin           ABSENT
#	openssl dgst -sha256    Homebrew           yes
#
# `sha256sum` first because it is present on both; the other two are the stock fallbacks for a box
# that has one but not the other. Order is preference, not correctness — all three hash the same bytes.
#
# Override with AB_HASHER=<command> to point this at something else. It exists so hash_of's two
# assertions can be *watched die* rather than certified by reading them: `AB_HASHER=/nonexistent` kills
# the run check, `AB_HASHER=echo` kills the shape check (it ignores stdin and prints one empty line).
# It does **not** reach the resolution branch below — an override that is set is exactly the case that
# skips the list — so that branch is falsified by mutating the candidate list, and was.
HASHER="${AB_HASHER:-}"
# _tried accumulates what was actually looked for, so the failure below names the list rather than a
# copy of it. Watching the mutation die is what surfaced this: the candidates were replaced with bogus
# names and the message still said "tried sha256sum, shasum, openssl" — *a control's failure message is
# an unscanned claim*, and the next edit to the list is what would have made it a lie in production.
_tried=""
if [ -z "$HASHER" ]; then
	for _cand in "sha256sum" "shasum -a 256" "openssl dgst -sha256"; do
		# ${_cand%% *} is the tool name without its arguments, and works for the one-word candidate too.
		_tried="$_tried ${_cand%% *}"
		if command -v "${_cand%% *}" >/dev/null 2>&1; then HASHER="$_cand"; break; fi
	done
fi
[ -n "$HASHER" ] || die "no sha256 tool on this box: tried$_tried.
The arms cannot be checked distinct without one, and running the rounds anyway would measure a pair
this script cannot tell apart. Install any of the three, or pass AB_HASHER=<command>."

# hash_of prints one arm binary's sha256, and **fails loudly rather than returning nothing.**
#
# Grave #624's real half. With the hasher missing, this returned the empty string for every arm, the
# empty strings compared equal, and the script announced *"the two arms are byte-identical"* — a
# substantive finding about two binaries it had never hashed, printed with the empty hash inline. It
# fell safe only because equality happens to be the refusal condition, and it named the wrong cause.
# The `--null` arm's assertion is the direction that does not fall safe: `NULL_HASH = BASE_HASH` is
# satisfied *vacuously* by two empty strings, so the arm whose whole job is to certify that a hash
# difference means a code difference is the arm that passes when there are no hashes.
#
# So the shape of the output is asserted before any comparison sees it. A malformed hash is a
# mechanism failure with its own message; it is never allowed to become a claim about the arms.
hash_of() {
	# Fed on **stdin**, not by path, so no filename appears in the output and the one 64-hex run in it
	# is unambiguously the hash. That is what lets three tools with three output formats — `<hash>  -`
	# from the sum tools, `SHA2-256(stdin)= <hash>` from openssl — share one parser instead of one
	# branch each.
	_out=$($HASHER < "$1" 2>&1) ||
		die "hashing $1 with '$HASHER' failed, so the arms cannot be checked distinct: $_out"
	_h=$(printf '%s\n' "$_out" | tr -cs '0-9a-f' '\n' | grep -E '^[0-9a-f]{64}$')
	# Counted, not first-matched: a first match declines to ask whether there were others, and
	# "exactly one" is what makes the extraction-by-shape sound. Lowercase only, deliberately — all
	# three tools print lowercase, and an uppercase digest would count zero and stop here rather than
	# compare unequal against a lowercase sibling and read as a code difference.
	_n=$(printf '%s' "$_h" | grep -c . || true)
	[ "$_n" = "1" ] || die "'$HASHER' produced no single 64-hex-digit sha256 for $1 ($_n found).
Its output was:
$_out
This is a failure of the hashing mechanism, not a finding about the arms. It is reported separately
because the alternative is what grave #624 records: an unhashed pair compares equal and the script
says the two arms are byte-identical, which sends the reader looking for two revs naming one commit."
	printf '%s\n' "$_h"
}

# Each arm runs from its own copy of the package directory, so a benchmark that reads testdata
# finds the revision of it that its own arm shipped.
PKGREL="$(printf '%s\n' "$PKG" | sed 's|^\./||')"

# graft <arm> <tree> — put head's copy of the package over this arm's, so the measurement itself
# is one source across the arms and only the code under test differs.
graft() {
	_arm="$1"; _tree="$2"
	[ "$_tree" != "$REPO" ] ||
		die "--graft would write into the repository itself: arm $_arm resolves to $REPO. Pass a
commit for that arm, because grafting into the live tree would edit the code being measured."
	rm -rf "$_tree/$PKGREL" || die "arm $_arm: could not clear $PKGREL before grafting"
	mkdir -p "$(dirname "$_tree/$PKGREL")" || die "arm $_arm: could not create $PKGREL's parent"
	cp -R "$HEAD_TREE/$PKGREL" "$_tree/$PKGREL" ||
		die "arm $_arm: could not copy $PKGREL out of the head tree"
	printf 'ab.sh: grafted head'\''s %s over arm %s — the arms now share the benchmark source\n' \
		"$PKGREL" "$_arm"
}

printf 'ab.sh: %s, %s rounds, base=%s head=%s%s%s\n' \
	"$PKG" "$ROUNDS" "$BASE" "$HEAD" \
	"$([ "$WANT_NULL" -eq 1 ] && printf ' null=base')" \
	"$([ "$WANT_GRAFT" -eq 1 ] && printf ' graft=head')"

BASE_TREE="$(tree_for base "$BASE")" || exit 1
HEAD_TREE="$(tree_for head "$HEAD")" || exit 1
[ -d "$HEAD_TREE/$PKGREL" ] ||
	die "the head arm has no $PKGREL directory, so there is nothing to measure with"
[ "$WANT_GRAFT" -eq 0 ] || graft base "$BASE_TREE"
BASE_BIN="$(build base "$BASE_TREE")" || exit 1
HEAD_BIN="$(build head "$HEAD_TREE")" || exit 1

# `|| exit 1` on every hash_of call, the same contract `tree_for` and `build` above carry: `die` inside
# a `$(...)` exits the **subshell**, so without it a failed hash returns the empty string and execution
# continues into the comparison below. Found by watching grave #624's repair die: the new assertion
# printed its message and the script ran on to announce the arms byte-identical anyway — the grave's own
# shape recurring inside its fix, which is why the falsification is run rather than read.
BASE_HASH="$(hash_of "$BASE_BIN")" || exit 1
HEAD_HASH="$(hash_of "$HEAD_BIN")" || exit 1
printf 'ab.sh: base %s\nab.sh: head %s\n' "$BASE_HASH" "$HEAD_HASH"

if [ "$BASE_HASH" = "$HEAD_HASH" ]; then
	die "the two arms are byte-identical (sha256 $BASE_HASH), so nothing was measured.

Identical arms agree perfectly and the board reads flat whatever the change does, which is what
an unchecked effect arm looks like from the outside. Either the revs name the same code, or one
arm's build did not land where it was meant to — ADR 0059's specimen was the second build running
in the first's directory. Fix the arms and re-run; no round was started."
fi

ARMS="base head"
NULL_BIN=""
if [ "$WANT_NULL" -eq 1 ]; then
	[ "$BASE" != "worktree" ] ||
		die "--null needs --base to be a commit. The null arm is a second independent build of
base's source, and uncommitted state cannot be checked out twice; a copy of base's binary would
make the equal-hash assertion true by construction and assert nothing."
	NULL_TREE="$(tree_for null "$BASE")" || exit 1
	[ "$WANT_GRAFT" -eq 0 ] || graft null "$NULL_TREE"
	NULL_BIN="$(build null "$NULL_TREE")" || exit 1
	NULL_HASH="$(hash_of "$NULL_BIN")" || exit 1
	printf 'ab.sh: null %s\n' "$NULL_HASH"
	[ "$NULL_HASH" = "$BASE_HASH" ] || die "the null arm is NOT byte-identical to base:
  base $BASE_HASH
  null $NULL_HASH
Both were built from $BASE with -trimpath, so this build is not reproducible here — and that
falsifies the premise the base-vs-head check rests on, since a hash difference can then mean a
build difference rather than a code difference. This is a finding about the toolchain, not a
reason to drop the arm."
	ARMS="base head null"
fi

bin_for() {
	case "$1" in
	base) printf '%s\n' "$BASE_BIN" ;;
	head) printf '%s\n' "$HEAD_BIN" ;;
	null) printf '%s\n' "$NULL_BIN" ;;
	esac
}
tree_for_arm() {
	case "$1" in
	head) printf '%s\n' "$HEAD_TREE" ;;
	null) printf '%s\n' "$NULL_TREE" ;;
	*) printf '%s\n' "$BASE_TREE" ;;
	esac
}
arm_at() {
	_want="$1"; _n=0
	for _a in $ARMS; do
		[ "$_n" -eq "$_want" ] && { printf '%s\n' "$_a"; return 0; }
		_n=$((_n + 1))
	done
	return 1
}

NARMS=0
for a in $ARMS; do NARMS=$((NARMS + 1)); done
# The lab-run provenance block, sampled **once** and written identically into every arm.
#
# Once, not per arm, and the reason is measured rather than tidy: benchstat treats `key: value`
# lines as configuration and splits its table on any key whose value differs between the files,
# so two arms disagreeing on one provenance key turn a comparison into two one-arm summaries —
# `vs base` column gone, p-value gone, and no line in the output saying a comparison had been
# declined. Sampling once and writing the same bytes makes that unreachable by construction
# rather than by care. scripts/labprov's header carries the measurement.
#
# Never fatal: labprov reports an unavailable fact as `unknown` and this tolerates its absence
# entirely, because an unrecordable provenance line must not redden a run that measured fine.
PROV="$(sh "$(dirname "$0")/labprov" 2>/dev/null || true)"
for a in $ARMS; do
	: > "$OUTDIR/$a.txt" || die "cannot write $OUTDIR/$a.txt"
	[ -z "$PROV" ] || printf '%s\n\n' "$PROV" >> "$OUTDIR/$a.txt"
done

r=0
while [ "$r" -lt "$ROUNDS" ]; do
	slot=0
	while [ "$slot" -lt "$NARMS" ]; do
		arm="$(arm_at $(( (slot + r) % NARMS )))" || die "internal: no arm at slot $slot"
		set -- -test.run XXX -test.bench "$BENCHRE" -test.count 1
		[ -n "$BENCHTIME" ] && set -- "$@" -test.benchtime "$BENCHTIME"
		if ! ( cd "$(tree_for_arm "$arm")/$PKGREL" && "$(bin_for "$arm")" "$@" ) >> "$OUTDIR/$arm.txt" 2>&1
		then
			printf 'ab.sh: arm %s failed in round %s; its log is %s\n' "$arm" "$((r + 1))" "$OUTDIR/$arm.txt" >&2
			tail -20 "$OUTDIR/$arm.txt" >&2
			die "a failed round is not a slow round, and nothing collected so far is comparable"
		fi
		slot=$((slot + 1))
	done
	r=$((r + 1))
	printf 'ab.sh: round %s/%s\n' "$r" "$ROUNDS"
done

# A filter that matched nothing leaves logs benchstat reads as empty sample sets, and empty
# against empty agrees perfectly.
for a in $ARMS; do
	lines="$(grep -c '^Benchmark' "$OUTDIR/$a.txt" || true)"
	[ "${lines:-0}" -ge "$ROUNDS" ] ||
		die "arm $a produced $lines Benchmark line(s) over $ROUNDS rounds: --bench '$BENCHRE' matched too little, and a comparison over an empty set says nothing"
	printf 'ab.sh: %-5s %s Benchmark line(s) -> %s\n' "$a" "$lines" "$OUTDIR/$a.txt"
done

printf '\nab.sh: benchstat over %s arms, so every row carries a p-value:\n\n' "$NARMS"
set --
for a in $ARMS; do set -- "$@" "$a=$OUTDIR/$a.txt"; done
benchstat "$@" || die "benchstat failed over $OUTDIR"

printf '\nab.sh: logs kept at %s\n' "$OUTDIR"
