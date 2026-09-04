GO ?= go

# Recipes run under bash with `pipefail`. Make's default is `/bin/sh -c`, which does not
# have it, and the cost was a real lost verdict: `make bench` piped `go test -bench` into
# `tee`, so a benchmark package that failed to *compile* left `[build failed]` inside
# new.txt while the pipeline reported success — the target exited 0 and benchstat printed a
# confident table, geomean included, from whichever packages happened to build. The one
# discipline that target exists to serve is *benchstat or it didn't happen*, and it was
# satisfiable by output with a build failure in it (grave #289).
#
# **`-e` is deliberately absent, and that is not timidity.** Adding it would abort a recipe
# at the first non-zero status, which is exactly how `strict` and `conformance` are written
# *not* to behave: `strict` runs `out="$$(go test …)"; status=$$?` and then prints the FAIL
# and SKIP lines it found. Under `-e` the shell would exit on the assignment, make would
# still go red, and the diagnosis would be gone — a verdict that keeps its exit code and
# loses its testimony. Make already fails a recipe on the line's final status, so `-e` buys
# little here and costs the reporting. `pipefail` alone is the surgical change: pipe heads
# stop being silently discarded, and nothing else moves.
#
# That absence is also why `conformance`'s `ls … | wc -l` counter below stays a pipe while
# ci.yml's two copies of it had to become loops: those run under `-e` as well, where the failing
# assignment kills the step *before* the floor can print its diagnosis. Here the assignment's
# status is simply discarded by the next command in the `; \` chain, the count is honestly 0, and
# the floor reports. Verified rather than assumed, both shells. Do not "fix" it for symmetry.
#
# **The flag is in SHELL and not in `.SHELLFLAGS`, and that is a portability fact with teeth.**
# `.SHELLFLAGS` arrived in GNU Make 3.82; macOS ships **3.81**, the last GPLv2 release, where
# it is accepted and **silently ignored**. The first version of this fix used it, went green,
# and did nothing: `SHELL := /bin/bash` took effect, the flags did not, and `make bench` kept
# swallowing build failures on the dev box while the same change would have worked on CI's
# make 4.x. That is decision 0005's own promise inverted — a laptop board and a CI board
# meaning different things — and it is the exact shape a fix is *most* likely to have when it
# is only ever validated in CI. Caught by re-running the falsification locally, which is why
# it is run locally. Embedding the flag in SHELL works on both: 3.81 invokes `$(SHELL) -c`,
# 4.x invokes `$(SHELL) $(.SHELLFLAGS)`, and the flag rides along either way.
SHELL := /bin/bash -o pipefail

# Quality tools are pinned in tools/go.mod via `tool` directives (decision 0005),
# so every target below runs the same version CI runs. Nothing here installs
# anything globally.
TOOL = $(GO) tool -modfile=tools/go.mod

.PHONY: all build test race vet test-endtable fmt fmt-check lint check vuln deadcode fuzz bench ab lab-ab lab-test ratio cite close spec-tests spec-ref threads-ref tidy conformance strict pipefail-check opcodes opcode-drift keywords keyword-drift opcodes-text opcodes-text-drift memarg memarg-drift gate-census xcorpus

# The default gate. `check` is what must be green before a report — it is the
# local mirror of CI, so a surprise in CI means a bug in this line, not a bug in
# the habit.
#
# **The exception, stated where the maxim is: fetched-artifact presence is machine
# state, not repo state.** A gitignored corpus (the suite, either reference pin) is
# either on this box or not, and a gate running where it is already present cannot
# test the case where it is missing — so a CI red on an unfetched corpus is not a bug
# in this line. See docs/laws/operations.md, "The maxim's stated exception". The
# checkable half is the join, not the absence: TestEveryPinnedCorpusIsFetchedByEveryUnitTestJob
# asserts every pinned corpus is fetched by every job that runs unit tests.
all: check

# The gate list, named once so the recipe below cannot drift from it.
CHECK_GATES = pipefail-check fmt-check build vet lint test test-endtable deadcode

# **An unreached gate is not a passed gate, and the abort must say which is which.**
#
# This was `check: pipefail-check fmt-check build vet lint test deadcode` — a prerequisite list,
# which make walks in order and abandons at the first red. Correct behaviour, and it produced a
# genuinely misleading artifact: a dangling citation in `internal/validate/sig.go` (naming a test
# that never existed) reached a working tree and was caught only by the *cross-architecture* run,
# because `make check` had failed at `fmt-check` and never got as far as the citation gate. The
# transcript said `make: *** [fmt-check] Error 1` and nothing else, so the five gates that never
# ran were indistinguishable from five gates that ran clean — the omission lived in the reader's
# head rather than on the artifact.
#
# So the recipe runs the same gates in the same order and, on the first failure, prints the ones it
# did not reach. Naming them is the whole feature; a reader who sees `NOT reached: lint test
# deadcode` cannot mistake the run for a partial pass. Same shape as `strict`'s grep and
# `deadcode`'s filtered output: *a verdict channel cannot say what*, so the recipe says it.
#
# Two costs, both accepted and stated rather than discovered later. Recursive `$(MAKE)` per gate
# adds a process each (negligible against `go test`), and `check` is now **serial even under
# `-j`**, where the prerequisite form would have run gates concurrently. The second is arguably a
# fix: under `-j` "which gate did not run" has no single answer, because several are in flight when
# one goes red, and a report that cannot be exact about its own subject is the kind of instrument
# this file keeps deleting. (Directive: Scott, PR #295.)
check:
	@failed=""; \
	for g in $(CHECK_GATES); do \
		[ -n "$$failed" ] && continue; \
		$(MAKE) --no-print-directory "$$g" || failed="$$g"; \
	done; \
	if [ -n "$$failed" ]; then \
		rest=""; past=""; \
		for g in $(CHECK_GATES); do \
			if [ "$$g" = "$$failed" ]; then past=1; continue; fi; \
			[ -n "$$past" ] && rest="$$rest $$g"; \
		done; \
		echo; \
		echo "make check FAILED at gate: $$failed"; \
		if [ -n "$$rest" ]; then \
			echo "gates NOT reached — these did not pass, they did not run:$$rest"; \
			echo "a green from them is unavailable, not implied. Fix $$failed and re-run."; \
		else \
			echo "$$failed is the last gate; every other gate ran."; \
		fi; \
		exit 1; \
	fi

# The falsification above, kept. `SHELL := /bin/bash -o pipefail` is a claim about how every
# recipe in this file runs, and it is a claim that has already been silently false once — the
# `.SHELLFLAGS` form was ignored by macOS's make 3.81 and nothing said so. A settings line
# nobody checks is an intention, so the property is asserted rather than configured: under
# `pipefail` a pipeline whose head fails is non-zero, so `false | true` is non-zero, so the
# `if` does not fire. Without it the pipeline reports success and this target says which
# mechanism went missing rather than leaving a silently weaker board.
#
# In `check` because that is where the cost of being wrong lands: every swallowed pipe head in
# this file — and the `make bench` build failure that started this (grave #289) — is invisible
# on a green board, which is the one place an instrument's absence must not be free.
#
# # What this does not cover, by design: ad-hoc interactive commands
#
# The domain is **two configured shells** — recipes in this file, via `SHELL` above, and `run:`
# blocks in `ci.yml`, via `defaults.run.shell`, which that workflow's own step asserts by running
# this target beside its own `false | true`. Both are places where a pipe is *written once and read
# by everyone afterwards*, which is what makes a gate the right instrument for them.
#
# A command typed at a prompt during a session is **outside that domain on purpose, and there is no
# gate that could be inside it.** The recurring specimen is a compound command whose last link is a
# counting filter — `docker run … go test … | grep -c FAIL` — where `grep` exits 1 on *zero matches*,
# so a clean run reports failure and the reported exit code is an opinion about matches rather than
# about tests. That is *verdict channel and mechanism channel are different instruments* arriving
# through the shell, and `pipefail` would not have helped: nothing in that pipeline failed. The head
# succeeded, the tail's convention differs from the caller's expectation, and no setting reconciles
# them.
#
# So this is stated rather than gated. Three instances of the same habit earn a sentence at the
# checker saying where the checker's writ ends, not a fourth mechanism: the remedy is to put the
# verdict-bearing command last, or to capture its status before filtering. Written here so the next
# recurrence reads as a **known boundary** rather than as a gap someone should close — an
# instrument's domain being an assertion it cannot check about itself, which is the one thing it can
# do about that. (Ruling: Scott, PR #317 — *"state the boundary, build nothing."*)
pipefail-check:
	@if false | true; then \
		echo "recipes are NOT running with pipefail: a pipe here discards its head's exit"; \
		echo "status, which is how a failing 'go test | tee' reported success (grave #289)."; \
		echo "SHELL is: $(SHELL)  — and note .SHELLFLAGS is ignored by GNU Make < 3.82."; \
		exit 1; \
	fi

# Skip-forbidden mode (grave #407). Exported so every recipe below inherits it —
# see internal/testenv for what it revokes and why. `check` deliberately does NOT
# set it: `check` must stay green on a fresh clone, and the local-dev skip license
# is exactly the case that makes it so.
STRICT = BURROUGHS_NO_SKIP=1

# The board, asserted rather than skipped. Separate from `check` because it needs
# the vendored suite and `check` must stay green on a fresh clone — but CI runs
# both, so this is not optional, it is the other half of the mirror.
#
# Every board test calls requireSuite(), which *skips* when testdata/spec is absent,
# which is why this target refuses to run without it: a skip is not a verdict, and
# a target that passes by asking nothing is the failure the board controls exist to
# prevent. $(STRICT) makes that refusal come from the harness rather than from this
# file — the presence check below is the belt, the flag is the suspenders, and a
# skip would have to defeat both. Includes the all-gates-on lane, where the gated
# count must be zero.
conformance:
	@n="$$(scripts/suite-count.sh testdata/spec)"; \
	if [ "$$n" -lt 250 ]; then \
		echo "spec suite not vendored ($$n files); run: make spec-tests"; exit 1; \
	fi; \
	echo "vendored $$n .wast files"
	$(STRICT) $(GO) test -v -shuffle=on ./internal/spec/ ./internal/binary/
	$(STRICT) $(GO) test -v -run TestAllGatesOnLeavesNothingGated ./internal/spec/

# The whole tree with no skip licenses — the local mirror of CI's workflow-level
# BURROUGHS_NO_SKIP=1. `make check` proves the code is sound on any clone; this
# proves nothing declined to answer. Run it after `make spec-tests`.
#
# The grep is not redundant with the flag: the flag closes the skips that route
# through internal/testenv, and TestEverySkipSiteIsLicensed proves that is all of
# them today. This reads the output channel for the case where a future skip escapes
# both. A skip does not fail a test run, so the exit code cannot be asked.
#
# **On failure it prints the `-shuffle` seed first and then everything** (grave #539).
# The FAIL-and-SKIP grep above was the whole failure report, and it is the wrong filter
# for the failing case: `-test.shuffle <seed>` is the one line that makes a shuffled
# order reproducible, it matches neither pattern, and it was therefore discarded on
# exactly the runs that needed it. #540 is a real arm64 failure that cannot be worked
# because no seed survived. The seed goes first so a truncated log still holds it, the
# full output second, and the verdict word last where a reader's tail lands. Volume is
# only a cost on green runs, and on a green run none of this prints.
strict:
	@out="$$($(STRICT) $(GO) test -v -shuffle=on ./... 2>&1)"; \
	status=$$?; \
	printf '%s\n' "$$out" | grep -E '^[[:space:]]*(---[[:space:]]*)?(FAIL|SKIP)' || true; \
	if [ $$status -ne 0 ]; then \
		printf '%s\n' "$$out" | grep -E '^-test\.shuffle ' || echo "(no -test.shuffle line in the output)"; \
		echo "--- full output follows; the seed above reproduces this order via -shuffle=<seed> ---"; \
		printf '%s\n' "$$out"; \
		echo "tests failed"; exit 1; \
	fi; \
	if printf '%s\n' "$$out" | grep -qE '^[[:space:]]*(---[[:space:]]*)?SKIP'; then \
		echo "a test skipped under BURROUGHS_NO_SKIP=1"; exit 1; \
	fi; \
	echo "no SKIP lines"

build:
	$(GO) build -o bin/burroughs ./cmd/burroughs
# `burroughs_endtable` (#136, 0048) is the tree's only build tag — every other gate is a `Features`
# field, so nothing here compiled a tagged arm before. A tagged arm the gate never builds rots
# without a signal, which is the *unbuilt arm* shape rather than a new instrument: this is the same
# build over the same tree, one flag different, not a second oracle.
	$(GO) build -tags burroughs_endtable ./...

# -shuffle=on so test order is never load-bearing.
test:
	$(GO) test -shuffle=on ./...

# **A built arm is not a tested arm**, and for two milestones this gate only built it. That was
# defensible while the tagged arm was a probe whose whole body was one file the board never ran; it
# stopped being defensible when 0048 moved the mechanism into `internal/binary`, where the tag now
# changes the *decoder* — `Module`'s layout, `structural`'s recursion, what `DecodeModule` retains.
# A compile error was the only defect the old line could see, and none of the three bugs the port's
# own oracle test catches (a stale scratch buffer, a shifted arena base, an off-by-one on `end`)
# compiles wrong.
#
# `./...` and not the two packages that differ today, because the tag's domain is the module: any
# package may grow a tagged file, and a control scoped to today's cases inherits today's blind spot.
test-endtable:
	$(GO) test -shuffle=on -tags burroughs_endtable ./...

# **The timeout is stated because it was inherited, and the inherited one had 7 seconds of room.**
# `go test` applies a 10-minute per-package default when none is given, and `internal/spec` under
# `-race` had grown into it: nine consecutive `main` runs on CI's x86-64 leg ran that one package for
# 430.9, 550.5, 460.6, 562.3, 566.3, 547.5, 540.3, 593.0 and 580.6 seconds, oldest first — a **1.38x
# spread whose maximum sits 6.98s under the limit**, and the last seven all above 540. It fired on
# PR #607's run at 600.050s, where the package was killed mid-test and the arm64 leg of the same
# commit passed at 564.479s. The arm64 leg is flat over those runs (563.5-566.8s, 0.6% spread), so
# the variance being measured is one runner's and not the suite's.
#
# **An unasserted distance is the vacuum**: a pass at 593/600 and a pass at 431/600 printed the same
# word, so nothing could report that the room was gone. Naming the number is what makes the next
# erosion visible — 25m is ~2.5x the largest *completed* observation, and note what that multiplier
# is not measured against: the killed run's true duration is unknown, so this is headroom over the
# biggest number the suite has finished in, not over a measured requirement.
#
# A failsafe, not a budget, in the sense the `fuzz-smoke` job's own timeout comment means it: loose
# enough that runner variance cannot reach it, tight enough that a genuine hang is still caught.
# **Not a conformance bar and not a performance target** — no verdict in this tree reads it.
race:
	$(GO) test -race -timeout 25m -shuffle=on ./...

vet:
	$(GO) vet ./...

# gofumpt via golangci-lint's formatters section — a strict superset of gofmt.
fmt:
	$(TOOL) golangci-lint fmt ./...

# Reports which files gofumpt would change without rewriting them, so it is
# usable on a dirty tree — a `git diff` check here would flag the work in
# progress rather than the formatting. The exit code is the verdict; testing for
# non-empty output instead would also trip on `go: downloading ...` lines when
# the module cache is cold.
#
# **The probe above the verdict is #322**, and it is the mechanism channel this
# gate was missing: `fmt --diff` exits 0 both when the tree is clean and when the
# formatter examined nothing at all, so silence had two meanings and the gate
# reported the flattering one. A verdict channel cannot say whether it looked.
#
# So the formatter is first asked a question whose answer is known: a source with
# a blank line at the top of a function body, which is a gofumpt rule that plain
# gofmt leaves alone. If the reformatted text comes back equal to the input (or
# empty), the tool did not run, did not load its formatters, or is not gofumpt —
# and the tree check below would have passed regardless. `--stdin` is used because
# `fmt --diff --stdin` prints the *formatted source* and exits 0 either way, which
# makes it useless as a verdict and exactly right as a liveness probe.
#
# The two halves are deliberately different instruments: the probe proves gofumpt
# is reachable and opinionated, the `./...` run proves the tree agrees with it.
#
# The probe's stderr goes to a file rather than to `/dev/null`, and its exit status
# is tested on its own line, because **the probe was dropping its own mechanism
# channel** (grave #444): with the stream discarded, a formatter that never started
# was indistinguishable from one that answered unchanged, and this recipe reported
# the second — a visible message naming the wrong cause, while CI's `set -e` copy of
# the same two lines aborted with no message at all. Three outcomes, three messages:
# could not run, ran but is not opinionated, ran and the tree disagrees. The stream
# is suppressed *for the comparison* only, because `go: downloading ...` on a cold
# cache would otherwise be compared against the probe.
fmt-check:
	@probe="$$(printf 'package p\n\nfunc F() {\n\n\tx := 0\n\t_ = x\n}\n')"; \
	err="$$(mktemp)"; \
	if ! got="$$(printf '%s\n' "$$probe" | $(TOOL) golangci-lint fmt --diff --stdin 2>"$$err")"; then \
		echo "fmt-check could not run the formatter at all: it exited non-zero before"; \
		echo "answering the probe, so neither 'formatted' nor 'unformatted' is known."; \
		echo "Its stderr follows (grave #444):"; \
		cat "$$err"; \
		rm -f "$$err"; \
		exit 1; \
	fi; \
	rm -f "$$err"; \
	if [ -z "$$got" ] || [ "$$got" = "$$probe" ]; then \
		echo "fmt-check cannot confirm the formatter ran: a deliberately misformatted"; \
		echo "probe came back unchanged or empty, so the tree check below would report"; \
		echo "'formatted' whether or not gofumpt ever looked at it (#322)."; \
		exit 1; \
	fi; \
	$(TOOL) golangci-lint fmt --diff ./... || { echo "run: make fmt"; exit 1; }

lint:
	$(TOOL) golangci-lint run ./...

vuln:
	$(TOOL) govulncheck ./...

# The unreachable-error grave (#3) promoted to a tool. Every finding is a
# classification question to answer: declared-and-tracked passes, silent fails.
#
# The allowlist is empty, and that is a state worth naming. It held exactly one
# entry — reader.u64, tracked in #19 — from the tooling-gates PR until #36 gave
# limits min/max their true 64-bit width and made it reachable. That is the
# placeholder discipline's intended ending: a deferral retired by a production
# caller, not by an allowlist entry outliving the reason for it. An entry left here
# after its subject became reachable would license the next regression silently,
# which is the suppression-wearing-a-disguise shape the target exists to catch.
#
# **The status is checked, and reading the output alone was grave #544.** This was
# `out="$$(... 2>/dev/null || true)"` followed by an emptiness test. Reading the
# *output* is right — `deadcode` puts findings on stdout and exits 0 either way,
# measured — but `|| true` deleted the only channel that says whether the question
# was asked, so **an empty capture was this gate's spelling of "no dead code"**. A
# flag rename of the kind a toolchain bump produces (`-tests` for `-test`) exits 2
# with `flag provided but not defined` on stderr, and the gate printed one blank
# line and passed. Same shape as a skip: it agreed by asking nothing.
#
# The two channels are now split rather than merged, which is why stderr goes to a
# file instead of into `$$out`: findings are the verdict, the status is the
# mechanism, and `go: downloading ...` on a cold cache is neither. Stderr is
# printed only on the mechanism failure, where it is the whole diagnosis.
#
# **`-e` is absent here and that is what makes the repair expressible.** ci.yml's
# copy of this script was deleted rather than fixed: under that workflow's
# `bash -e` the failing assignment kills the step before `status` can be read, so
# the identical repair prints nothing and exits 2 — grave #539 exactly, measured in
# both shells. *Text mirrors are not failure-behaviour mirrors*, so the step calls
# this target. The cost is the `::error::` annotation, stated rather than found later.
deadcode:
	@err="$$(mktemp)"; \
	out="$$($(TOOL) deadcode -test ./... 2>"$$err")"; status=$$?; \
	if [ "$$status" -ne 0 ]; then \
		echo "deadcode exited $$status without reporting, so an empty finding list is not a"; \
		echo "clean bill of health — the tool did not run (grave #544). Its stderr:"; \
		cat "$$err"; rm -f "$$err"; exit 1; \
	fi; \
	rm -f "$$err"; \
	printf '%s\n' "$$out"; \
	filtered="$$(printf '%s\n' "$$out" | grep -v '^$$' || true)"; \
	if [ -n "$$filtered" ]; then \
		echo "unreachable code with no tracking issue:"; printf '%s\n' "$$filtered"; exit 1; \
	fi

# Local fuzzing. Corpora seed from testdata/spec, so run spec-tests first.
# FUZZTIME overrides the budget: make fuzz FUZZTIME=5m
#
# Wall clock here is deliberate, and the contrast with CI's executions budgets
# (#28) is the whole point of naming it. This target's purpose is "fuzz until I
# get bored", an interactive one whose unit genuinely *is* time; CI's purpose is
# "ask a fixed number of questions", whose unit is executions. Same flag, two
# purposes, so two units. Accepts either form — FUZZTIME=2000000x works.
FUZZTIME ?= 30s
fuzz:
	$(GO) test ./internal/binary/ -run XXX -fuzz FuzzDecodeModule -fuzztime $(FUZZTIME)
	$(GO) test ./internal/binary/ -run XXX -fuzz FuzzULEB -fuzztime $(FUZZTIME)
	$(GO) test ./internal/spec/ -run XXX -fuzz FuzzWastLexer -fuzztime $(FUZZTIME)
	$(GO) test ./internal/spec/ -run XXX -fuzz FuzzParseNodeProgress -fuzztime $(FUZZTIME)
	$(GO) test ./internal/binary/ -run XXX -fuzz FuzzConstExprProgress -fuzztime $(FUZZTIME)
	$(GO) test ./internal/text/ -run XXX -fuzz FuzzLexerProgress -fuzztime $(FUZZTIME)

# benchstat or it didn't happen (decision 0005): n=10 and a p-value, or no claim. **A
# p-value exists only for a comparison, and this target runs one arm** — so it cannot
# supply one, and it now says so instead of implying otherwise.
#
# It used to imply otherwise, in three lines. It wrote a hardcoded `new.txt`, so a
# two-arm A/B driven through it overwrote arm A with arm B and the caller ended with one
# file; it printed the old-vs-new comparison as a *suggestion* that nothing ran and no
# rule in this tree produced an `old.txt` for; and it then ran `benchstat` over that
# single file, which prints per-row summaries with no comparison and no p-value. The
# target demanding a p-value ran the one benchstat invocation that cannot print one —
# the defect stated as the rule, in the instrument whose whole purpose *is* the rule —
# and the overwrite produced byte-identical arm logs from differing binaries on janus
# (grave #612).
#
# Two arms go through `ab` below. What is repaired here is the single-arm path: the log
# is named after the rev it was built from rather than after a fixed word, an existing
# log is never overwritten, and the closing text names what one arm can and cannot say.
BENCHCOUNT ?= 10
BENCHPKG ?= ./internal/interp/...
BENCHREV = $(shell git describe --always --dirty --exclude '*' 2>/dev/null || echo unknown)
BENCHOUT ?= bench-$(BENCHREV).txt
bench:
	@test ! -e "$(BENCHOUT)" || { \
		echo "$(BENCHOUT) already exists."; \
		echo "Refusing to overwrite it: a fixed output name is how one arm's log became the other's"; \
		echo "(grave #612). Remove it, or pass BENCHOUT=<file>."; \
		exit 1; \
	}
	$(GO) test $(BENCHPKG) -run XXX -bench . -count=$(BENCHCOUNT) | tee "$(BENCHOUT)"
	@echo
	@echo "One arm, n=$(BENCHCOUNT), written to $(BENCHOUT). What follows is a per-row summary:"
	@echo "there is no comparison in it and no p-value, so no performance claim can cite it."
	@echo "For a claim, run the two-arm protocol:"
	@echo "  make ab AB='--pkg <pkg> --base <rev> --head <rev>'"
	@$(TOOL) benchstat "$(BENCHOUT)"

# The two-arm A/B, executing grave #552's protocol rather than asking each caller to
# re-derive it: arms compiled to binaries up front, their hashes checked distinct, one
# `-count=1` round per arm per round with the slots rotated, and benchstat over two named
# files so every row carries a p-value. Eight ADRs hand-rolled those four steps because
# nothing in the tree could run them, which is how a broken `bench` sat beside eight
# correct measurements without anything contradicting it.
#
# The rounds are driven from out here because they cannot be driven from within: `go test`
# does not interleave two benchmark rows in one binary — `-count=N` runs each benchmark N
# times consecutively, and `-shuffle=on` permutes the blocks once (measured, grave #612).
# Sequential arms make run order a confounder perfectly correlated with the arm.
AB ?=
ab:
	@test -n "$(AB)" || { \
		echo "usage: make ab AB='--pkg ./internal/interp/membench --base main --head HEAD'"; \
		echo "       make ab AB='--pkg ./internal/interp/growbench --base main --head HEAD --null --graft'"; \
		echo "  --graft is what you want whenever the benchmark was written alongside the change:"; \
		echo "  it puts head's copy of the package on every arm, so only the code under test differs."; \
		echo "see scripts/ab.sh --help for the full argument list and what each step is there for."; \
		exit 1; \
	}
	./scripts/ab.sh $(AB)

# The same two runs on shared lab hardware, through that host's pueue queue. See CLAUDE.md's
# "Running tests and benchmarks on lab hardware" for the fleet table and the group semantics;
# the short version is that those boxes carry several projects at once and an unqueued run
# lands on top of whatever is measuring, which both projects then read as divergence in their
# own numbers.
#
# **`bench` and `ab` above are deliberately left local and unqueued.** They run on this dev
# box, which is not lab hardware and not anyone else's measurement slot, so queueing them
# would buy nothing and cost a round trip. The queue governs runs on the *shared* machines.
#
# LABHOST is not a guess. `janus.local` is this repo's own recorded default — the value
# scripts/xcheck-amd64.sh has carried since it was written, and the host named in the
# `verdict from NATIVE x86_64` lines that ADRs 0054, 0058 and 0061 cite, so changing it would
# silently break comparability with every landed x86-64 figure. Override with LABHOST=<host>;
# CLAUDE.md's fleet table says which hosts have a `measured` slot and which are build-only.
LABHOST ?= janus.local
LABTEST ?= go test ./...

# Measured, so `measured`: the whole two-arm protocol goes over as ONE queued task. Not one
# task per arm or per round — another project's job interleaving between rounds is exactly the
# confounder grave #552's interleaving exists to remove, and per-arm tasks would additionally
# give the arms differing provenance, which benchstat answers by splitting the table and
# dropping the p-value.
lab-ab:
	@test -n "$(AB)" || { \
		echo "usage: make lab-ab AB='--pkg ./internal/interp/membench --base main --head HEAD'"; \
		echo "  runs the two-arm protocol on $(LABHOST) in its 'measured' group, as one task."; \
		echo "  same arguments as 'make ab' — see scripts/ab.sh --help."; \
		exit 1; \
	}
	XCHECK_HOST=$(LABHOST) XCHECK_GROUP=measured ./scripts/xcheck-amd64.sh ./scripts/ab.sh $(AB)

# Unmeasured, so `build`: nothing this produces is a figure, and `build` is wide, so it waits
# on other compiles rather than on the box's measurement slot.
lab-test:
	XCHECK_HOST=$(LABHOST) XCHECK_GROUP=build ./scripts/xcheck-amd64.sh $(LABTEST)

# The engine/instrument ratio every PR's Board line quotes (#117). RATIO defaults to
# the trailing window; pass a rev or a range for one PR:
#   make ratio RATIO=HEAD              make ratio RATIO="--window 6 <rev>"
#
# **Not part of `check`, deliberately.** #117 measured this figure and ruled it a quoted
# context number rather than a bound — it is dominated by PR size, so any threshold on it
# is a threshold on diff length. A target makes the command reachable; a `check`
# dependency would make it a gate, which is the thing the ruling declined.
RATIO ?= --window 6
ratio:
	@./scripts/ratio.sh $(RATIO)

# Every citation a diff adds must resolve to the artifact it names: `#NNNN` to a real issue or
# PR, `decision NNNN` to a `docs/decisions/` file, `grave #N` to something actually labelled
# `type:grave`. Two guessed numbers reached a working tree in consecutive PRs and both were saved
# by luck; the script's header carries the rest.
#
# **Also not part of `check`, but for a different reason than `ratio` above.** The issue half
# needs the network, and `check` must stay green on a fresh clone with no `gh` and no token —
# wiring it in would make the default gate fail for people with no citation problem, which is how
# a gate gets worked around. So the split is deliberate and stated: this target is the *local*
# face, runnable on demand, and `.github/workflows/ci.yml`'s `citations` job is where the verdict
# is **binding**, because CI is where the network exists and where the answer gets recorded. A
# pre-push hook would have been the third option and is the wrong one — `--no-verify` leaves no
# trace that it was skipped, and *a skip is not a verdict*. (Scoping: Scott, PR #285 relay.)
#
#   make cite                       # base `main` against the working tree
#   make cite CITE="<base> <head>"  # an explicit range, e.g. a PR's merge base to its tip
CITE ?= --worktree main
cite:
	@./scripts/citecheck.sh $(CITE)

# closecheck — no PR body or commit message may close an issue by keyword (grave #314).
#
# A **separate script from citecheck.sh, not a fourth phase inside it**, and the reason is the
# population rather than the subject. citecheck.sh scans the lines a *diff adds*; this scans
# *commit messages and the PR body*, which are not in the diff at all. Folding them would give one
# tool two domains and one printed coverage line that could not describe either honestly — the
# opposite of the rule that put the domain line there. Same revision grammar, so the two read as
# siblings.
#
# Local face only, on `cite`'s scoping ruling: the `--pr` half needs the network, so CI is where
# the verdict is binding.
#
# The `--body` form is the offline half of that split, ordered by Scott on the #396 report: a body is
# a file before it is a PR, so the file can be scanned before the push instead of the PR after it.
# `--pr` remains the binding form because it also covers the title.
#
#   make close                       # commit messages on this branch, base `main`
#   make close CLOSE="--pr 313"      # the PR title and body; needs gh
#   make close CLOSE="--body pr.md"  # a body before it is a PR, offline
CLOSE ?= --worktree main
close:
	@./scripts/closecheck.sh $(CLOSE)

# The engine module only. Deliberately NOT tools/go.mod: a tool modfile has no
# packages of its own, so tidy pulls in the tools' transitive test dependencies
# and — via the module proxy — adds this very repo as a requirement of its own
# tooling (`require github.com/scttfrdmn/burroughs v0.0.1`). `go get -tool` is
# what maintains that file. Verified the hard way; see decision 0005.
tidy:
	$(GO) mod tidy

spec-tests:
	./scripts/fetch-spec-tests.sh

# The reference interpreter — the authority for accept-direction facts the suite
# cannot falsify (decision 0007). Not needed to build or to run the board; needed to
# generate and drift-check the opcode table.
spec-ref:
	./scripts/fetch-spec-ref.sh

# The threads proposal's reference interpreter — the *second* authority pin (ADR 0007's
# 2026-08-28 amendment). Separate from spec-ref rather than folded into it: the pins are
# independently dated so drift in one is never silently absorbed by the other, and a
# single target running both fetches would make one `make` invocation move two pins.
#
# The threads proposal was never merged into the core spec, so at bdd7164 all nine files
# spec-ref licenses contain zero occurrences of `atomic` and zero of `shared`. Everything
# contract §§2-5 needs is behind this target and nowhere else.
threads-ref:
	./scripts/fetch-threads-ref.sh

# Regenerate the opcode table from the vendored reference (decision 0007). The output
# is committed, so this is run when the pin moves, not on every build.
opcodes: spec-ref threads-ref
	$(GO) run ./internal/gen/opcodegen/cmd/opcodegen -o internal/binary/optable.go
	@echo "regenerated internal/binary/optable.go"

# Condition 4 of 0007: re-extract from the pinned reference and assert the committed
# table still agrees. Drift becomes a build failure rather than a diff nobody ordered.
#
# Its own target rather than part of `check`, for the same reason `conformance` is:
# `check` must pass on a fresh clone with nothing vendored, and this needs
# third_party/spec. $(STRICT) is the point — without it the drift check would *skip*
# when the reference is absent, reporting agreement with an authority it never read,
# which is the #29 grave in a code generator. CI runs it, so it is not optional.
#
# The whole package rather than a -run list of test names: an enumeration is a sample,
# so a control added later would silently not run here, and the gate would quietly
# narrow while still reporting green. -count=1 because a cached result is a verdict
# about a previous tree.
#
# Two guards since the table is composed, for keyword-drift's reason: the extraction reads
# *both* pins' decoders, so a tree holding only the core pin fails on a `refPins` walk whose
# message is about vacuity, and sends the reader to re-run the target they already ran. Which
# pin a recipe needs is not a fact the recipe can infer — it is stated, per pin.
opcode-drift:
	@if [ ! -f third_party/spec/interpreter/binary/decode.ml ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
	fi
	@if [ ! -f third_party/spec-threads/interpreter/binary/decode.ml ]; then \
		echo "threads reference not vendored; run: make threads-ref"; exit 1; \
	fi
	$(STRICT) $(GO) test -v -shuffle=on -count=1 ./internal/gen/opcodegen/

# Regenerate the gate census: every accepted arm of the opcode table with the gate
# governing it (decision 0012, #91).
#
# **No corpus dependency, and that is the interesting part.** Unlike `opcodes` and
# `keywords`, both inputs are already committed — optable.go (generated) and gatemap.go
# (hand-authored) — so this runs on a fresh clone with nothing vendored, and its drift
# check is therefore part of `check` rather than a separate `conformance`-style target.
# A control whose inputs are in the tree should gate every commit; one whose inputs are
# fetched cannot.
#
# The flip side of the same coin: `make opcodes` can now move the census, so the two are
# run together when the pin moves. That coupling is intended — an arm arriving upstream
# inside a whole-region gate range is exactly the event #91 filed, and a census the
# regeneration does not touch would be the staleness defect of #87 in a golden file.
gate-census:
	$(GO) test ./internal/binary/ -run TestGateCensusIsClassifiedArmByArm -update-census -count=1
	@echo "regenerated internal/binary/testdata/gate-census.txt"

# The `claudemd-ledger` target stood here: a golden per-entry byte ledger over CLAUDE.md's
# index entries, re-based whenever a recall key moved. It is retired with the rest of the
# index economy (Scott's directive, the four-workstream brief) — CLAUDE.md is a brief and a
# pointer page now, and a per-entry byte budget over a page of pointers rations nothing.
# `internal/testenv/laws_test.go` carries the account of what dissolved and what was
# re-pointed instead.

# Regenerate the wat keyword table from the same vendored reference, one grammar over
# (decision 0009). Same shape as `opcodes` above and deliberately not folded into it:
# two authorities (decode.ml, lexer.mll), two extractors, two committed tables, and a
# single target would make "regenerate the binary table" and "regenerate the text table"
# indistinguishable in a log and in a diff.
keywords: spec-ref threads-ref
	$(GO) run ./internal/gen/keywordgen/cmd/keywordgen -o internal/text/keywords.go
	@echo "regenerated internal/text/keywords.go"

# The keyword table's drift check — 0007's condition 4 in the wat grammar, and the
# reason the table is allowed to be a committed artifact at all.
#
# Two guards, not one, and the second is a finding rather than caution. keywordgen's
# tests read *both* corpora: lexer.mll for the extraction, and one suite vector
# (obsolete-keywords.wast) for the citation check that proves the eleven mnemonics this
# table omits are the eleven the suite asks about. Under $(STRICT) a missing suite file
# is a hard failure, so guarding only the reference would send a reader to `make
# spec-ref` for an absence `make spec-tests` fixes. Same lesson as ci.yml's vendoring
# order: which package needs which corpus is not a fact a recipe can infer, so each
# recipe states its own preconditions.
#
# Whole package, -count=1, $(STRICT): see opcode-drift above for each. Scoped to the
# generator package rather than ./internal/text/... for opcode-drift's symmetry — this
# target owns the *table's* agreement with the authority, and widening it to every future
# text package would make an unrelated failure read as reference drift.
keyword-drift:
	@if [ ! -f third_party/spec/interpreter/text/lexer.mll ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
	fi
	@if [ ! -f third_party/spec-threads/interpreter/text/lexer.mll ]; then \
		echo "threads reference not vendored; run: make threads-ref"; exit 1; \
	fi
	@if [ ! -f testdata/spec/obsolete-keywords.wast ]; then \
		echo "spec suite not vendored; run: make spec-tests"; exit 1; \
	fi
	$(STRICT) $(GO) test -v -shuffle=on -count=1 ./internal/gen/keywordgen/

# Regenerate the wat mnemonic→opcode table (decision 0014). Third generator, and the only
# one that is a *join*: it reads no opcodes of its own, it links the two tables above
# through the reference's own constructor names.
#
# Three sources at one revision — decode.ml, parser.mly, lexer.mll — which is why it depends
# on `spec-ref` like the other two and not on their committed outputs. Reading optable.go's
# *text* would make this a parser of generated Go, and the seam is the extractor's Go types,
# not its emitted source.
#
# Named `opcodes-text` rather than `optable` or `opcodes-wat` because the pair it belongs to
# is (`opcodes`, `opcodes-text`): same fact, two directions — one says what a byte decodes
# to, the other what a mnemonic encodes to.
opcodes-text: spec-ref threads-ref
	$(GO) run ./internal/gen/opgen/cmd/opgen -o internal/text/opcodes.go
	@echo "regenerated internal/text/opcodes.go"

# The join's drift check. 0007's condition 4 a third time, and it has a failure mode the
# other two do not: the join can go vacuous *on one side*, so its floors are per-partition
# and the package's tests pin exact counts rather than only floors. A floor of 350 stayed
# green while a regexp silently lost 25 wrapped lexer arms (411 of 436) — see the package's
# TestWrappedArmsAreRead.
#
# Six files across two pins, and the second pin's three are as load-bearing as the first's: the
# join is composed now (#524), so a tree holding only the core pin fails on a `refPins` walk whose
# message is about vacuity and sends the reader to re-run the target they already ran — opcode-drift's
# finding, restated because the same absence reaches this package through three readers rather than
# one. Which pin a recipe needs is not a fact the recipe can infer; it is stated, per pin.
#
# No suite vector is guarded, unlike keyword-drift: this package's tests read none. Each recipe
# states its own preconditions, which is the whole reason that rule exists — an inherited guard
# would send a reader to fix an absence that is not there.
#
# Whole package, -count=1, $(STRICT): see opcode-drift for each.
opcodes-text-drift:
	@if [ ! -f third_party/spec/interpreter/binary/decode.ml ] || \
	    [ ! -f third_party/spec/interpreter/text/parser.mly ] || \
	    [ ! -f third_party/spec/interpreter/text/lexer.mll ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
	fi
	@if [ ! -f third_party/spec-threads/interpreter/binary/decode.ml ] || \
	    [ ! -f third_party/spec-threads/interpreter/text/parser.mly ] || \
	    [ ! -f third_party/spec-threads/interpreter/text/lexer.mll ]; then \
		echo "threads reference not vendored; run: make threads-ref"; exit 1; \
	fi
	$(STRICT) $(GO) test -v -shuffle=on -count=1 ./internal/gen/opgen/

# Regenerate the natural-alignment table (#127). Fourth generator, third reader of lexer.mll,
# and the first whose *values* the suite cannot check at all.
#
# The other three tables are checkable in the reject direction: a wrong opcode or a missing
# keyword makes some vector fail. A wrong natural alignment does not — `align=` is optional in
# the text, the flags byte it defaults into is a legal alignment, and validation rejects only
# *over*-alignment. So a mistyped default yields an image that decodes clean and differs from
# its source in a byte no assert_malformed inspects. That is contract §9 G-3, and it is the whole
# argument for machine-reading these 111 numbers rather than typing them.
#
# **Both pins, and the atomic rows are where the argument is strongest** (#524). For every one of the
# threads pin's 66 mnemonics the natural alignment is also the *only legal* one, an atomic access
# having to be naturally aligned, so a wrong row there is not a slow module but a rejected one.
memarg: spec-ref threads-ref
	$(GO) run ./internal/gen/memarggen/cmd/memarggen -o internal/text/memarg.go
	@echo "regenerated internal/text/memarg.go"

# The alignment table's drift check. 0007's condition 4 a fourth time.
#
# Two packages, not one, and the second is the consolidation clause's receipt. `mllex` is the
# shared arm reader all three lexer.mll generators now call — the wrapped-arm shape's third
# occurrence un-froze the tooling, so the substrate moved to one place rather than being avoided
# three times. Its tests are what stand behind every floor in the other three targets: if the
# arm reader silently truncates a wrapped body, memarggen loses rows, opgen loses joins, and
# nothing in either package's own tests would name the cause. A shared reader's drift check
# belongs to whichever target is closest to it, and this is that target.
#
# Only the references are guarded — no suite vector is read, and there is no vector that could be.
#
# Two lexers across two pins, stated per pin: the table is composed now (#524), so a tree holding
# only the core pin fails on a `refPins` walk whose message is about vacuity and sends the reader to
# re-run the target they already ran. Which pin a recipe needs is not a fact the recipe can infer.
#
# Whole packages, -count=1, $(STRICT): see opcode-drift for each.
memarg-drift:
	@if [ ! -f third_party/spec/interpreter/text/lexer.mll ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
	fi
	@if [ ! -f third_party/spec-threads/interpreter/text/lexer.mll ]; then \
		echo "threads reference not vendored; run: make threads-ref"; exit 1; \
	fi
	$(STRICT) $(GO) test -v -shuffle=on -count=1 ./internal/gen/mllex/ ./internal/gen/memarggen/

# Regenerate the #67 cross-check corpus: independently produced binary images of the suite's
# must-succeed text modules (0011's second appendix, #67 half 2).
#
# **This is the one target that needs a tool the project does not pin, and that is why its
# output is committed.** wabt supplies a statement of what a text module denotes that does not
# come from Burroughs — comparing our encoder to our decoder is one witness talking to itself,
# which 0011 rules inadmissible. So wabt runs *here*, once, and the images plus a provenance
# manifest go into the tree; no test invokes it, CI never sees it, and a fresh clone gets the
# control with nothing fetched and nothing installed. Same posture the generated tables have
# toward the reference interpreter, for the reason #8 states: a non-Go binary in the
# conformance loop is reproducibility debt where the project can least afford it.
#
# No `-drift` sibling, and the asymmetry is deliberate rather than an omission. The other
# generated artifacts are drift-checked because their input is *vendored at a pin a fetch can
# move*, so a committed table can silently stop agreeing with the reference in the tree. This
# corpus's inputs are the suite pin (asserted by the package's own test against
# `gen.PinnedSuiteRev`, so a bump fails the board) and a wabt version the manifest records.
# A drift check would have to install wabt in CI, which is exactly the dependency the
# committed artifact exists to avoid — the check would reintroduce the thing it verifies the
# absence of.
xcorpus: spec-tests
	@command -v wast2json >/dev/null 2>&1 || { \
		echo "wast2json not found; install wabt (brew install wabt)"; exit 1; }
	$(GO) run ./internal/gen/xcorpus/cmd/xcorpus
	@echo "regenerated testdata/xcorpus/ (manifest.json, images.bin)"
