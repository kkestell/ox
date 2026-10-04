.PHONY: check check-docs format format-docs e2e build install

# ox finds ox-server next to its own binary, so both build into bin/.
build:
	cd server && go build -trimpath -ldflags="-s -w" -o ../bin/ ./cmd/ox ./cmd/ox-server

check: check-docs
	test -z "$$(gofmt -l server)" || (gofmt -l server && exit 1)
	cd server && go vet ./...
	cd server && go test ./...

e2e:
	cd server && OX_E2E=1 go test -count=1 -run '^TestTerminal' ./cmd/ox

check-docs:
	dprint check

format: format-docs
	gofmt -w server

format-docs:
	dprint fmt

PREFIX ?= $(HOME)/.local/bin
OX_CONFIG_DIR ?= $(HOME)/.config/ox

install: build
	mkdir -p $(PREFIX)
	install -m 755 bin/ox $(PREFIX)/ox
	install -m 755 bin/ox-server $(PREFIX)/ox-server
	mkdir -p $(OX_CONFIG_DIR)
	cp examples/settings.json $(OX_CONFIG_DIR)/settings.json
