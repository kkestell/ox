.PHONY: check check-docs format format-docs e2e server install install-release release

# The client finds ox-server next to its own binary, so each build puts the
# server in the Cargo profile directory.
SERVER_BUILD = cd server && go build -trimpath -ldflags="-s -w" -o ../target/$(1)/ox-server ./cmd/ox-server

check: check-docs server
	test -z "$$(gofmt -l server)" || (gofmt -l server && exit 1)
	cd server && go vet ./...
	cd server && go test ./...
	cargo fmt --all -- --check
	cargo test --workspace --all-targets --all-features
	cargo build --workspace --all-features
	cargo clippy --workspace --all-targets --all-features -- -D warnings

server:
	$(call SERVER_BUILD,debug)

e2e: server
	cargo test -p ox --test tui -- --ignored

check-docs:
	dprint check

format: format-docs
	cargo fmt --all
	gofmt -w server

format-docs:
	dprint fmt

PREFIX ?= $(HOME)/.local/bin
OX_CONFIG_DIR ?= $(HOME)/.config/ox

release:
	cargo build --release -p ox
	$(call SERVER_BUILD,release)

install:
	cargo build --profile fast -p ox
	$(call SERVER_BUILD,fast)
	$(MAKE) PROFILE_DIR=fast do-install

install-release: release
	$(MAKE) PROFILE_DIR=release do-install

.PHONY: do-install
do-install:
	mkdir -p $(PREFIX)
	install -m 755 target/$(PROFILE_DIR)/ox $(PREFIX)/ox
	install -m 755 target/$(PROFILE_DIR)/ox-server $(PREFIX)/ox-server
	mkdir -p $(OX_CONFIG_DIR)
	cp examples/settings.json $(OX_CONFIG_DIR)/settings.json
