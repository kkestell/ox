.PHONY: dev

dev:
	go build -o bin/ ./cmd/ox ./cmd/oxd
	bin/oxd & trap 'kill $$!' EXIT; bin/ox
