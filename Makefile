.PHONY: dev web

dev: web
	go build -o bin/ ./cmd/ox ./cmd/oxd
	bin/oxd & trap 'kill $$!' EXIT; bin/ox

web: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run build

cmd/ox/web/node_modules: cmd/ox/web/package-lock.json
	npm --prefix cmd/ox/web ci
	touch $@
