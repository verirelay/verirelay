.PHONY: build test fmt vet lint check clean

DIST_DIR := dist
VERIRELAY := $(DIST_DIR)/verirelay

# Keep in sync with .github/workflows/ci.yml.
GOFUMPT_VERSION := v0.12.0
GOLANGCI_LINT_VERSION := v2.15.0

build:
	mkdir -p $(DIST_DIR)
	go build -o $(VERIRELAY) ./cmd/verirelay

test:
	go test ./...

fmt:
	@unformatted="$$(go run mvdan.cc/gofumpt@$(GOFUMPT_VERSION) -l .)"; \
	[ -z "$$unformatted" ] || { echo "error: unformatted files:" >&2; printf '%s\n' "$$unformatted" >&2; exit 1; }

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

check: fmt vet test build lint

clean:
	rm -rf $(DIST_DIR)
