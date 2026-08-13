include .env
export

# Database name parsed out of DB_SOURCE so the helpers below don't need
# the user to repeat it. Format: postgres://user:pass@host:port/dbname?...
DB_NAME ?= stellarflow_x402
PG_ADMIN_URL ?= postgres://postgres:$(POSTGRES_PASSWORD)@localhost:5432/postgres?sslmode=disable

.PHONY: help run dev build clean tidy \
        db-create db-drop db-reset migrate-up migrate-down migrate-force \
        seed setup sep10-test catalog-test \
        sqlc

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ─── Day-to-day ────────────────────────────────────────────────────────────

run: ## Run the API server (foreground)
	go run ./cmd/api

dev: build ## Build + run (handy when iterating on cmd entrypoints)
	./bin/stellarflow

build: ## Compile the API server to bin/stellarflow
	go build -o bin/stellarflow ./cmd/api

build-sidecar: ## Compile the x402-sidecar binary to bin/x402-sidecar
	go build -o bin/x402-sidecar ./cmd/sidecar

tidy: ## go mod tidy
	go mod tidy

clean: ## Remove built binaries
	rm -rf bin/

# ─── First-time setup ──────────────────────────────────────────────────────

setup: db-create migrate-up seed ## Full first-time setup: create DB, run migrations, seed demo data
	@echo ""
	@echo "✓ Setup complete. Start the server with: make run"
	@echo "  Then open http://localhost:8086"

db-create: ## Create the local Postgres database (uses POSTGRES_PASSWORD from env)
	@PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U postgres -p 5432 -tc "SELECT 1 FROM pg_database WHERE datname = '$(DB_NAME)'" | grep -q 1 \
	  || PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U postgres -p 5432 -c "CREATE DATABASE $(DB_NAME)"
	@echo "✓ database '$(DB_NAME)' ready"

db-drop: ## Drop the local Postgres database (destructive)
	PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U postgres -p 5432 -c "DROP DATABASE IF EXISTS $(DB_NAME)"

db-reset: db-drop db-create migrate-up seed ## Drop + recreate + migrate + seed (destructive)

# ─── Migrations ────────────────────────────────────────────────────────────

migrate-up: ## Apply all pending migrations
	migrate -path internal/db/migration -database "$(DB_SOURCE)" -verbose up

migrate-down: ## Roll back the most recent migration (no-op for this repo since no .down.sql files)
	migrate -path internal/db/migration -database "$(DB_SOURCE)" -verbose down

migrate-force: ## Force the migration version to N (use after fixing a failed migration). Usage: make migrate-force VERSION=10
	migrate -path internal/db/migration -database "$(DB_SOURCE)" force $(VERSION)

# ─── Seed ──────────────────────────────────────────────────────────────────

seed: ## Wipe and re-seed the demo data (operator + 3 brands + 90 posts + pricing)
	go run ./example/stellarflow/seed

gen-wallet: ## Generate a fresh Stellar keypair (G-address + S-key) for the operator treasury
	@go run ./cmd/gen-wallet

fund-wallet: ## Send USDC from the treasury to a target wallet (ADDR=G... AMOUNT=0.50)
	@ADDR=$(ADDR) AMOUNT=$(AMOUNT) go run ./cmd/fund-wallet

# ─── Smoke tests ───────────────────────────────────────────────────────────

sep10-test: ## Generate a test wallet and exchange a SEP-10 challenge for a Paseto token
	go run ./cmd/dev-sep10-test

catalog-test: ## Hit the public /api/catalog endpoint and pretty-print the response
	@curl -s http://localhost:8086/api/catalog | python3 -m json.tool

demo-call: ## Make ONE sample paid call to compare_competitors (server must be running)
	@PAY=$$(printf '{"x402Version":2,"scheme":"exact","network":"stellar:testnet","payload":{"from":"GLIVEDEMOSINGLE0001XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}}' | base64); \
	curl -s -X POST http://localhost:8086/api/x402/compare_competitors \
	  -H "Content-Type: application/json" \
	  -H "X-Payment: $$PAY" \
	  -H "X-StellarFlow-Client: claude-code" \
	  -H "X-StellarFlow-Reason: demo end-to-end via make demo-call" \
	  -d '{"competitor_ids":[1,2,3]}' | python3 -m json.tool

demo-flood: ## Make 5 paid calls (one per endpoint, distinct payers) — fills the live feed
	@bash scripts/demo-flood.sh

# ─── sqlc (intentionally unused, see store.go) ─────────────────────────────

sqlc: ## (unused) sqlc generate — kept for compatibility
	@echo "Note: this project hand-rolls its DB layer in internal/db/sqlc/store.go"
	@echo "Running sqlc would generate conflicting types. See HACKATHON.md."
