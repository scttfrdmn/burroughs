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
# progress rather than the formatting.
fmt-check:
	@out="$$($(TOOL) golangci-lint fmt --diff ./... 2>&1)"; \
	if [ -n "$$out" ]; then \
		echo "gofumpt would reformat:"; printf '%s\n' "$$out"; \
		echo "run: make fmt"; exit 1; \
	fi

lint:
	$(TOOL) golangci-lint run ./...

vuln:
	$(TOOL) govulncheck ./...

# The unreachable-error grave (#3) promoted to a tool. reader.u64 is expected
# here: declared, tracked in #19, and suppressed at its definition site with a
# reason. Anything else is a classification question to answer.
deadcode:
	@out="$$($(TOOL) deadcode -test ./... || true)"; \
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

tidy:
	$(GO) mod tidy
	$(GO) mod tidy -modfile=tools/go.mod

spec-tests:
	./scripts/fetch-spec-tests.sh
