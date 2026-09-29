.PHONY: check check-docs format format-docs e2e install install-release release

check: check-docs
	cargo fmt --all -- --check
	cargo test --workspace --all-targets --all-features
	cargo build --workspace --all-features
	cargo clippy --workspace --all-targets --all-features -- -D warnings

e2e:
	cargo build --workspace
	cargo test -p ox --test tui -- --ignored

check-docs:
	dprint check

format: format-docs
	cargo fmt --all

format-docs:
	dprint fmt

PREFIX ?= $(HOME)/.local/bin
CONFIG_DIR ?= $(HOME)/.config/ox

release:
	cargo build --release -p ox -p ox-acp

install:
	cargo build --profile fast -p ox -p ox-acp
	$(MAKE) PROFILE_DIR=fast do-install

install-release:
	cargo build --release -p ox -p ox-acp
	$(MAKE) PROFILE_DIR=release do-install

.PHONY: do-install
do-install:
	mkdir -p $(PREFIX)
	install -m 755 target/$(PROFILE_DIR)/ox $(PREFIX)/ox
	install -m 755 target/$(PROFILE_DIR)/ox-acp $(PREFIX)/ox-acp
	mkdir -p $(CONFIG_DIR)
	cp examples/settings.json $(CONFIG_DIR)/settings.json
	cp examples/tui.json $(CONFIG_DIR)/tui.json
