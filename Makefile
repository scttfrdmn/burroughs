GO ?= go

.PHONY: build test vet fmt spec-tests all

all: build vet test

build:
	$(GO) build -o bin/burroughs ./cmd/burroughs

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

spec-tests:
	./scripts/fetch-spec-tests.sh
