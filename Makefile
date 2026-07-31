GO ?= go

# Quality tools are pinned in tools/go.mod via `tool` directives (decision 0005),
# so every target below runs the same version CI runs. Nothing here installs
# anything globally.
TOOL = $(GO) tool -modfile=tools/go.mod

.PHONY: all build test vet fmt lint check vuln deadcode fuzz bench spec-tests tidy conformance strict

# The default gate. `check` is what must be green before a report — it is the
# local mirror of CI, so a surprise in CI means a bug in this line, not a bug in
# the habit.
all: check

check: fmt-check build vet lint test deadcode

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

# The engine module only. Deliberately NOT tools/go.mod: a tool modfile has no
# packages of its own, so tidy pulls in the tools' transitive test dependencies
# and — via the module proxy — adds this very repo as a requirement of its own
# tooling (`require github.com/scttfrdmn/burroughs v0.0.1`). `go get -tool` is
# what maintains that file. Verified the hard way; see decision 0005.
tidy:
	$(GO) mod tidy

spec-tests:
	./scripts/fetch-spec-tests.sh
