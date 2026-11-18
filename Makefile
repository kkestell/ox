.PHONY: dev web test live

dev: web
	go build -race -o bin/ ./cmd/ox ./cmd/oxd
	export GORACE=halt_on_error=1; bin/oxd & oxd=$$!; bin/ox & ox=$$!; trap 'kill $$oxd $$ox' EXIT; npm --prefix cmd/ox/web run dev

test:
	go test -race ./...

# live drives real Claude Code processes with Haiku.
live:
	OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server

web: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run build

cmd/ox/web/node_modules: cmd/ox/web/package-lock.json
	npm --prefix cmd/ox/web ci
	touch $@
