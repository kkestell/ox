.PHONY: install

INSTALL_DIR ?= $(HOME)/.local/bin

install:
	cargo build --release -p claude-code-acp
	install -d "$(INSTALL_DIR)"
	install -m 755 ../../target/release/claude-code-acp "$(INSTALL_DIR)/claude-code-acp"
