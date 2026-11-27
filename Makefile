# Makefile for weblinks, a Go weblinks server with an embedded UI.
# Targets cover the ordinary loop of building, running and testing, plus a
# single `check` target that runs formatting, vetting and tests together.
# Go maintains its own build cache, so every target is phony and incremental
# work is left to the toolchain rather than to file timestamps.

BINARY := weblinks
PKG    := ./...
ADDR   ?= 127.0.0.1:5309
FILE   ?= weblinks.json
CLASSIFICATION_FILE ?= classification.md
LOG    ?= weblinks.log

# -s -w drop the symbol table and DWARF data, and -trimpath removes local
# filesystem paths from the binary, giving a smaller and reproducible build.
BUILDFLAGS := -trimpath -ldflags="-s -w"

.DEFAULT_GOAL := build

.PHONY: build
build: ## Compile a stripped, trimmed binary into the current directory
	go build $(BUILDFLAGS) -o $(BINARY) .

.PHONY: run
run: ## Build and serve, opening a browser (override ADDR=, FILE=, CLASSIFICATION_FILE=)
	go run . -addr $(ADDR) -file "$(FILE)" -classification-file "$(CLASSIFICATION_FILE)"

.PHONY: serve
serve: ## Serve without launching a browser
	go run . -addr $(ADDR) -file "$(FILE)" -classification-file "$(CLASSIFICATION_FILE)" -no-open

# The compiled binary is launched rather than `go run .` so that no toolchain
# wrapper process sits between the shell and the server; nohup plus & detaches
# it, leaving a process that outlives the shell it was started from.
.PHONY: daemon
daemon: build ## Serve detached with nohup, surviving shell exit (log in LOG=)
	@nohup ./$(BINARY) -addr $(ADDR) -file "$(FILE)" -classification-file "$(CLASSIFICATION_FILE)" -no-open >> $(LOG) 2>&1 & \
	echo "$(BINARY) running on $(ADDR) as pid $$!, logging to $(LOG)"

.PHONY: test
test: ## Run the test suite
	go test $(PKG)

.PHONY: test-v
test-v: ## Run the test suite with per-test output
	go test -v $(PKG)

.PHONY: race
race: ## Run the test suite under the race detector
	go test -race $(PKG)

.PHONY: cover
cover: ## Report test coverage per package
	go test -cover $(PKG)

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: fmt
fmt: ## Rewrite sources with gofmt
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any source needs gofmt
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed for:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: check
check: fmt-check vet test ## Run every check in one pass

.PHONY: install
install: ## Install a stripped, trimmed binary into GOBIN
	go install $(BUILDFLAGS) .

.PHONY: tidy
tidy: ## Prune and verify go.mod
	go mod tidy

.PHONY: screenshots
screenshots: ## Regenerate the README screenshots with Playwright
	uv run screenshots/generate.py

.PHONY: clean
clean: ## Remove the built binary, leaving weblinks.json and backups alone
	rm -f $(BINARY)

.PHONY: help
help: ## List the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
