.PHONY: dev build web test live lint e2e metrics hooks

# .env holds OPENROUTER_API_KEY for Ur. Only that key reaches oxd and tests.
-include .env
export OPENROUTER_API_KEY

dev: web build
	GORACE=halt_on_error=1 node cmd/ox/web/dev.mjs

build:
	go build -race -o bin/ ./cmd/ox ./cmd/ox-mcp ./cmd/oxd

test: lint
	go test -race ./...
	node --test cmd/ox/web/stack.test.mjs

lint: web
	gofmt -l . | (! grep .)
	go mod tidy -diff
	go fix -diff ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...
	npm --prefix cmd/ox/web run lint

# live drives real Claude Code processes with Haiku, through oxd and through
# the web client, and Ur through OpenRouter.
live: cmd/ox/web/node_modules
	OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server
	OX_LIVE=1 npm --prefix cmd/ox/web run e2e -- approval

# e2e drives the web client in Chrome against oxd and the fake harness.
e2e: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run e2e

web: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run build

# metrics writes docs/metrics.md from the files staged for commit. The
# pre-commit hook runs it on every commit.
metrics: cmd/ox/web/node_modules
	python3 scripts/metrics.py

hooks:
	git config core.hooksPath .githooks

cmd/ox/web/node_modules: cmd/ox/web/package-lock.json
	npm --prefix cmd/ox/web ci
	touch $@
