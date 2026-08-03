GO ?= go

# Quality tools are pinned in tools/go.mod via `tool` directives (decision 0005),
# so every target below runs the same version CI runs. Nothing here installs
# anything globally.
TOOL = $(GO) tool -modfile=tools/go.mod

.PHONY: all build test vet fmt lint check vuln deadcode fuzz bench spec-tests spec-ref tidy conformance strict opcodes opcode-drift keywords keyword-drift opcodes-text opcodes-text-drift gate-census xcorpus

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
