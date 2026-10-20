.DEFAULT_GOAL := all

PPROF_DIR            ?= .pprof
PPROF_PORT           ?= 6060
PPROF_PROFILE        ?= heap
PPROF_DIFF_HTTP_PORT ?= 8081

GOBIN                := $(shell go env GOPATH)/bin
GOLANGCI_LINT_VERSION ?= v2.15.0
GOLANGCI_LINT        ?= $(GOBIN)/golangci-lint


.PHONY: build
build:
	@echo "==> Building..."
	go build -v ./...

.PHONY: test
test:
	@echo "==> Running tests with race detector..."
	go test -race -v ./...

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix:
	$(GOLANGCI_LINT) run --fix

.PHONY: install-dev
install-dev:
	@echo "==> Installing development dependencies..."
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(GOBIN) $(GOLANGCI_LINT_VERSION)
	$(GOLANGCI_LINT) --version