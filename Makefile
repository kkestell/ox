.PHONY: dev web test live lint e2e

dev: web
	go build -race -o bin/ ./cmd/ox ./cmd/ox-mcp ./cmd/oxd
	GORACE=halt_on_error=1 node cmd/ox/web/dev.mjs

test: lint web
	go test -race ./...

lint: cmd/ox/web/node_modules
	gofmt -l . | (! grep .)
	go mod tidy -diff
	go fix -diff ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...
	npm --prefix cmd/ox/web run lint

# live drives real Claude Code processes with Haiku, through oxd and through
# the web client.
live: cmd/ox/web/node_modules
	OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server
	OX_LIVE=1 npm --prefix cmd/ox/web run e2e -- approval

# e2e drives the web client in Chrome against oxd and the fake harness.
e2e: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run e2e

web: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run build

cmd/ox/web/node_modules: cmd/ox/web/package-lock.json
	npm --prefix cmd/ox/web ci
	touch $@
