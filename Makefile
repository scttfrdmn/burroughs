GO ?= go

# Quality tools are pinned in tools/go.mod via `tool` directives (decision 0005),
# so every target below runs the same version CI runs. Nothing here installs
# anything globally.
TOOL = $(GO) tool -modfile=tools/go.mod

.PHONY: all build test vet fmt lint check vuln deadcode fuzz bench spec-tests tidy

# The default gate. `check` is what must be green before a report — it is the
# local mirror of CI, so a surprise in CI means a bug in this line, not a bug in
# the habit.
all: check

check: fmt-check build vet lint test deadcode

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

# The unreachable-error grave (#3) promoted to a tool. reader.u64 is expected
# here: declared, tracked in #19, and suppressed at its definition site with a
# reason. Anything else is a classification question to answer.
deadcode:
	@out="$$($(TOOL) deadcode -test ./... 2>/dev/null || true)"; \
	printf '%s\n' "$$out"; \
	filtered="$$(printf '%s\n' "$$out" | grep -v 'reader\.u64' | grep -v '^$$' || true)"; \
	if [ -n "$$filtered" ]; then \
		echo "unreachable code with no tracking issue:"; printf '%s\n' "$$filtered"; exit 1; \
	fi

# Local fuzzing. Corpora seed from testdata/spec, so run spec-tests first.
# FUZZTIME overrides the budget: make fuzz FUZZTIME=5m
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
