.PHONY: help build test lint format clean up down db db-reset dev run stop restart prepush postpull init docker-build app-init app-dev app-build app-clean

# Default target
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

# Build variables
BINARY_NAME=kit
MAIN_PATH=./cmd/kit
BUILD_DIR=./dist
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X github.com/mrdon/kit/internal/buildinfo.Version=$(VERSION) -X github.com/mrdon/kit/internal/buildinfo.Commit=$(COMMIT) -X github.com/mrdon/kit/internal/buildinfo.Date=$(DATE)"

build: app-build ## Build the binary (includes frontend)
	@echo "Building $(BINARY_NAME)..."
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"

# Frontend PWA lifecycle — all npm invocation goes through these.
APP_DIR=web/app

app-init: ## Install frontend deps (npm install)
	@echo "Installing frontend deps..."
	@cd $(APP_DIR) && npm install

app-dev: ## Run Vite dev server (proxies /api to :$(APP_PORT))
	@cd $(APP_DIR) && npm run dev

app-build: $(APP_DIR)/node_modules ## Build the frontend to $(APP_DIR)/dist
	@echo "Building frontend..."
	@cd $(APP_DIR) && npm run build

$(APP_DIR)/node_modules:
	@cd $(APP_DIR) && npm install

app-clean: ## Remove frontend build output + node_modules
	@rm -rf $(APP_DIR)/dist $(APP_DIR)/node_modules

test: up ## Run tests (requires Postgres via `make up`)
	@echo "Running tests..."
	@OUTPUT=$$(go test -race -cover ./... 2>&1 | grep -v 'no such tool "covdata"'); \
	RESULT=$$?; \
	if echo "$$OUTPUT" | grep -q "^FAIL"; then \
		echo "$$OUTPUT"; \
		exit 1; \
	else \
		PASSED=$$(echo "$$OUTPUT" | grep -c "^ok"); \
		echo "✓ All $$PASSED packages passed"; \
	fi

lint: ## Run linters (requires golangci-lint)
	@echo "Running linters..."
	@GOBIN=$$(go env GOPATH)/bin; \
	if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run; \
	elif [ -x "$$GOBIN/golangci-lint" ]; then \
		"$$GOBIN/golangci-lint" run; \
	else \
		echo "golangci-lint not found. Run 'make init' to install." && exit 1; \
	fi

format: ## Format code
	@echo "Formatting code..."
	@gofmt -s -w .
	@go mod tidy

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@go clean

# Docker Compose targets
up: ## Start Postgres via Docker Compose
	@docker compose up -d
	@echo "Waiting for Postgres..."
	@docker compose exec postgres pg_isready -U kit -q --timeout=30 || (echo "Postgres not ready" && exit 1)
	@echo "Postgres is ready"

down: ## Stop Docker Compose services
	@docker compose down

db: ## Connect to local Postgres
	@PGPASSWORD=kit psql -U kit -h localhost -p $${PG_PORT:-5488} kit

db-reset: ## Wipe and restart Postgres (destroys all data)
	@docker compose down -v
	@docker compose up -d
	@echo "Waiting for Postgres..."
	@sleep 2
	@docker compose exec postgres pg_isready -U kit -q --timeout=30 || (echo "Postgres not ready" && exit 1)
	@echo "Postgres reset complete"

# Development targets
TUNNEL_BIN=$(shell which cloudflared 2>/dev/null || echo /tmp/cloudflared)
APP_PORT=$(shell grep '^PORT=' .env 2>/dev/null | cut -d= -f2 || echo 8488)

run: up build ## Start Postgres, tunnel, and the app
	@if [ -f /tmp/kit-app.pid ]; then kill $$(cat /tmp/kit-app.pid) 2>/dev/null; rm -f /tmp/kit-app.pid; fi
	@if [ -f /tmp/kit-tunnel.pid ]; then kill $$(cat /tmp/kit-tunnel.pid) 2>/dev/null; rm -f /tmp/kit-tunnel.pid; fi
	@nohup $(TUNNEL_BIN) tunnel --url http://localhost:$(APP_PORT) > /tmp/kit-tunnel.log 2>&1 & echo $$! > /tmp/kit-tunnel.pid
	@URL=""; \
	for i in 1 2 3 4 5 6 7 8 9 10; do \
		URL=$$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' /tmp/kit-tunnel.log 2>/dev/null | head -1); \
		if [ -n "$$URL" ]; then break; fi; \
		sleep 1; \
	done; \
	if [ -z "$$URL" ]; then \
		echo "⚠ Tunnel not ready — check /tmp/kit-tunnel.log"; \
	else \
		echo ""; \
		echo "══════════════════════════════════════════════════════"; \
		echo "  Slack Event URL:  $$URL/slack/events"; \
		echo "  OAuth Redirect:   $$URL/slack/oauth/callback"; \
		echo "  Install URL:      $$URL/slack/install"; \
		echo "══════════════════════════════════════════════════════"; \
		echo ""; \
	fi
	@$(BUILD_DIR)/$(BINARY_NAME) & echo $$! > /tmp/kit-app.pid; wait $$!

stop: ## Stop the app and tunnel
	@if [ -f /tmp/kit-app.pid ]; then kill $$(cat /tmp/kit-app.pid) 2>/dev/null; rm -f /tmp/kit-app.pid; fi
	@if [ -f /tmp/kit-tunnel.pid ]; then kill $$(cat /tmp/kit-tunnel.pid) 2>/dev/null; rm -f /tmp/kit-tunnel.pid; fi
	@echo "Stopped"

restart: stop run ## Restart everything

dev: up build ## Start Postgres + hot reload (requires air)
	@which air > /dev/null || go install github.com/cosmtrek/air@latest
	@air

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	@docker build -t $(BINARY_NAME) .

# Module management
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@go mod download

tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	@go mod tidy

init: ## Initialize development environment (install tools, download deps)
	@echo "Initializing development environment..."
	@echo "Installing golangci-lint..."
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$$(go env GOPATH)/bin" v2.8.0
	@echo "Downloading dependencies..."
	@go mod download
	@echo ""
	@echo "✓ Development environment initialized"

prepush: format lint build ## Run before pushing (format, lint, build) — run `make test` separately

postpull: init ## Run after pulling (install tools and download dependencies)
