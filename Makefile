# Makefile for weblinks, a Go weblinks server with an embedded UI.
# Targets cover the ordinary loop of building, running and testing, plus a
# single `check` target that runs formatting, vetting and tests together.
# Go maintains its own build cache, so every target is phony and incremental
# work is left to the toolchain rather than to file timestamps.

BINARY := weblinks
PKG    := ./...

# ARGS passes flags through to the server, for example
# `make run ARGS="-dir /tmp/scratch -no-open"`. Assigning it here keeps an
# unrelated ARGS in the environment from leaking in; a command-line value
# still overrides it.
ARGS :=

# -s -w drop the symbol table and DWARF data, and -trimpath removes local
# filesystem paths from the binary, giving a smaller and reproducible build.
BUILDFLAGS := -trimpath -ldflags="-s -w"

.DEFAULT_GOAL := build

.PHONY: build
build: ## Compile a stripped, trimmed binary into the current directory
	go build $(BUILDFLAGS) -o $(BINARY) .

.PHONY: run
run: ## Build and serve, opening a browser (server flags in ARGS="...")
	go run . $(ARGS)

.PHONY: test
test: ## Run the test suite
	go test $(PKG)

.PHONY: race
race: ## Run the test suite under the race detector
	go test -race $(PKG)

.PHONY: check
check: ## Fail on unformatted sources, then run go vet and the tests
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed for:"; echo "$$unformatted"; exit 1; \
	fi
	go vet $(PKG)
	go test $(PKG)

.PHONY: install
install: ## Install a stripped, trimmed binary into GOBIN
	go install $(BUILDFLAGS) .

.PHONY: screenshots
screenshots: ## Regenerate the README screenshots with Playwright
	uv run screenshots/generate.py

.PHONY: clean
clean: ## Remove the built binary, leaving the data directory alone
	rm -f $(BINARY)

.PHONY: help
help: ## List the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
