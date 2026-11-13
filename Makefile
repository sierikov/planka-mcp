BINARY    := planka-mcp
MAIN      := ./cmd/planka-mcp
GO        := go
GOFLAGS   :=
VERSION   ?= dev
MCPB_FILE := planka-mcp.mcpb
# The local Planka: its version, port and compose project. Another version needs its own project
# and port, e.g. make planka-up PLANKA_VERSION=2.1.1 PLANKA_PORT=3334 COMPOSE_PROJECT=planka-mcp-2-1.
PLANKA_VERSION  ?= 2.2.1
PLANKA_PORT     ?= 3333
COMPOSE_PROJECT ?= planka-mcp-dev
COMPOSE   := PLANKA_VERSION=$(PLANKA_VERSION) PLANKA_PORT=$(PLANKA_PORT) docker compose -p $(COMPOSE_PROJECT) -f deploy/compose.yml

.PHONY: all build test lint fmt vet check clean mcpb dist mocks toolsnaps e2e docker planka-up planka-down fixtures

all: check build

build:
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BINARY) $(MAIN)

mcpb: build
	@if [ ! -f icon.png ]; then echo "ERROR: icon.png not found — add a 512x512 PNG icon to the project root first"; exit 1; fi
	zip -j $(MCPB_FILE) manifest.json icon.png $(BINARY)
	@echo "Built $(MCPB_FILE) — drag it into Claude Desktop to install"

# Builds every release file into dist/, as the release workflow does: make dist VERSION=0.1.0
dist:
	./scripts/release.sh $(VERSION)

docker:
	docker build --build-arg VERSION=$(VERSION) -t planka-mcp:dev .

test:
	$(GO) test ./...

mocks:
	$(GO) tool mockery

toolsnaps:
	UPDATE_TOOLSNAPS=true $(GO) test ./cmd/...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

vet:
	$(GO) vet ./...
	$(GO) vet -tags e2e ./...

check: fmt vet lint test

# A local Planka v2 at http://localhost:$(PLANKA_PORT) (admin credentials are in deploy/compose.yml).
planka-up:
	$(COMPOSE) up -d --wait

# Removes the volumes too, so the next planka-up starts from an empty database.
planka-down:
	$(COMPOSE) down -v

# Seeds the running Planka and re-captures internal/planka/testdata/*.json.
fixtures:
	./scripts/capture-fixtures.sh

# Runs against http://localhost:$(PLANKA_PORT) (make planka-up). PLANKA_E2E_URL and PLANKA_E2E_API_KEY (an
# admin's key) point it at another instance; the server's own PLANKA_URL and PLANKA_API_KEY are never read.
e2e:
	PLANKA_E2E_URL=$${PLANKA_E2E_URL:-http://localhost:$(PLANKA_PORT)} $(GO) test -tags e2e -count=1 -v ./e2e/...

clean:
	rm -f $(BINARY) $(MCPB_FILE)
	rm -rf dist
