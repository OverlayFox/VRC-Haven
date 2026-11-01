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
.PHONY: test
test:
	@echo "==> Running tests with race detector..."
	go test -race -v ./...

.PHONY: lint
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

.PHONY: performance-test
performance-test:
	HAVEN_CPUS=1 HAVEN_MEM=1GB docker compose up --build

.PHONY: performance-graph
performance-graph:
	docker stats

# The pprof targets need a running haven with PPROF_ADDR set, PPROF_PROFILE is heap, allocs, goroutine, profile (30s CPU) or any other /debug/pprof/ name

# Opens the pprof web UI on the live profile
.PHONY: pprof-web
pprof-web:
	go tool pprof -http=:$(PPROF_DIFF_HTTP_PORT) http://localhost:$(PPROF_PORT)/debug/pprof/$(PPROF_PROFILE)

# Saves a timestamped snapshot of the profile to $(PPROF_DIR)
.PHONY: pprof-capture
pprof-capture:
	@mkdir -p $(PPROF_DIR)
	curl -sSf -o $(PPROF_DIR)/$(PPROF_PROFILE)-$$(date +%Y%m%d-%H%M%S).pb.gz http://localhost:$(PPROF_PORT)/debug/pprof/$(PPROF_PROFILE)

# Opens the pprof web UI on what changed between the two newest snapshots, e.g. heap growth while a stream runs
.PHONY: pprof-diff
pprof-diff:
	go tool pprof -http=:$(PPROF_DIFF_HTTP_PORT) \
		-diff_base=$$(ls -t $(PPROF_DIR)/$(PPROF_PROFILE)-*.pb.gz | sed -n 2p) \
		$$(ls -t $(PPROF_DIR)/$(PPROF_PROFILE)-*.pb.gz | head -n 1)

.PHONY: gen-env-example
gen-env-example:
	go run ./cmd/envexample > .env.example.tmp && mv .env.example.tmp .env.example

