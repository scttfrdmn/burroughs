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

.PHONY: all build test race vet fmt fmt-check lint check vuln deadcode fuzz bench ratio cite spec-tests spec-ref tidy conformance strict pipefail-check opcodes opcode-drift keywords keyword-drift opcodes-text opcodes-text-drift memarg memarg-drift gate-census claudemd-ledger xcorpus

# The default gate. `check` is what must be green before a report — it is the
# local mirror of CI, so a surprise in CI means a bug in this line, not a bug in
# the habit.
all: check

# The gate list, named once so the recipe below cannot drift from it.
CHECK_GATES = pipefail-check fmt-check build vet lint test deadcode

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
pipefail-check:
	@if false | true; then \
		echo "recipes are NOT running with pipefail: a pipe here discards its head's exit"; \
		echo "status, which is how a failing 'go test | tee' reported success (grave #289)."; \
		echo "SHELL is: $(SHELL)  — and note .SHELLFLAGS is ignored by GNU Make < 3.82."; \
		exit 1; \
	fi

# Skip-forbidden mode (grave #29). Exported so every recipe below inherits it —
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
	@n="$$(ls testdata/spec/*.wast 2>/dev/null | wc -l | tr -d ' ')"; \
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
strict:
	@out="$$($(STRICT) $(GO) test -v -shuffle=on ./... 2>&1)"; \
	status=$$?; \
	printf '%s\n' "$$out" | grep -E '^[[:space:]]*(---[[:space:]]*)?(FAIL|SKIP)' || true; \
	if [ $$status -ne 0 ]; then echo "tests failed"; exit 1; fi; \
	if printf '%s\n' "$$out" | grep -qE '^[[:space:]]*(---[[:space:]]*)?SKIP'; then \
		echo "a test skipped under BURROUGHS_NO_SKIP=1"; exit 1; \
	fi; \
	echo "no SKIP lines"

build:
	$(GO) build -o bin/burroughs ./cmd/burroughs

# -shuffle=on so test order is never load-bearing.
test:
	$(GO) test -shuffle=on ./...

race:
	$(GO) test -race -shuffle=on ./...

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
fmt-check:
	@$(TOOL) golangci-lint fmt --diff ./... || { echo "run: make fmt"; exit 1; }

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
deadcode:
	@out="$$($(TOOL) deadcode -test ./... 2>/dev/null || true)"; \
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

# benchstat or it didn't happen (decision 0005). Any performance claim in a PR
# cites this target's output, never a single -count=1 run: n=10 and a p-value,
# or no claim. Writes new.txt; compare against a saved old.txt with:
#   $(TOOL) benchstat old.txt new.txt
BENCHCOUNT ?= 10
bench:
	$(GO) test ./internal/interp/... -run XXX -bench . -count=$(BENCHCOUNT) | tee new.txt
	@echo
	@echo "baseline comparison: $(TOOL) benchstat old.txt new.txt"
	@$(TOOL) benchstat new.txt

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

# Regenerate the opcode table from the vendored reference (decision 0007). The output
# is committed, so this is run when the pin moves, not on every build.
opcodes: spec-ref
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
opcode-drift:
	@if [ ! -f third_party/spec/interpreter/binary/decode.ml ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
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

# Re-base the CLAUDE.md byte ledger: one row per index entry, one per surrounding section
# (#298, under `a total is not a ledger`'s exception clause).
#
# Same shape and the same no-corpus-dependency property as `gate-census` above — the sole
# input is CLAUDE.md, committed — so its check is part of `check` and not `conformance`.
#
# **Re-basing is a normal motion and is never the whole motion.** A row moves because a
# recall key changed, and the question the ledger asks is which branch that text is:
# a law's *body*, which belongs in docs/laws/, or *governance*, which stays. Run this
# after answering, and say which in the PR — a re-base with no answer is the ratchet the
# docs/laws/ restructure exists to stop, laundered through a golden file.
claudemd-ledger:
	$(GO) test ./internal/testenv/ -run TestClaudeMDIndexLedger -update-ledger -count=1
	@echo "regenerated internal/testenv/testdata/claudemd-ledger.txt"

# Regenerate the wat keyword table from the same vendored reference, one grammar over
# (decision 0009). Same shape as `opcodes` above and deliberately not folded into it:
# two authorities (decode.ml, lexer.mll), two extractors, two committed tables, and a
# single target would make "regenerate the binary table" and "regenerate the text table"
# indistinguishable in a log and in a diff.
keywords: spec-ref
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
opcodes-text: spec-ref
	$(GO) run ./internal/gen/opgen/cmd/opgen -o internal/text/opcodes.go
	@echo "regenerated internal/text/opcodes.go"

# The join's drift check. 0007's condition 4 a third time, and it has a failure mode the
# other two do not: the join can go vacuous *on one side*, so its floors are per-partition
# and the package's tests pin exact counts rather than only floors. A floor of 350 stayed
# green while a regexp silently lost 25 wrapped lexer arms (411 of 436) — see the package's
# TestWrappedArmsAreRead.
#
# Only the reference is guarded, not the suite: unlike keyword-drift this package's tests read
# no suite vector. Each recipe states its own preconditions, which is the whole reason that
# rule exists — an inherited guard would send a reader to fix an absence that is not there.
#
# Whole package, -count=1, $(STRICT): see opcode-drift for each.
opcodes-text-drift:
	@if [ ! -f third_party/spec/interpreter/binary/decode.ml ] || \
	    [ ! -f third_party/spec/interpreter/text/parser.mly ] || \
	    [ ! -f third_party/spec/interpreter/text/lexer.mll ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
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
# argument for machine-reading these 45 numbers rather than typing them.
memarg: spec-ref
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
# Only the reference is guarded — no suite vector is read, and there is no vector that could be.
#
# Whole packages, -count=1, $(STRICT): see opcode-drift for each.
memarg-drift:
	@if [ ! -f third_party/spec/interpreter/text/lexer.mll ]; then \
		echo "reference not vendored; run: make spec-ref"; exit 1; \
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
