.DEFAULT_GOAL := help
.PHONY: help dev check check-rust check-ui check-scripts fix e2e smoke-claude smoke-opencode

PYTHON ?= python3
RUFF ?= uvx ruff@0.17.4
CLAUDE_MODEL ?= claude-haiku-5-5
OPENCODE_MODEL ?= openai/gpt-6-luna
DEV_SOCKET := $(CURDIR)/target/dev/oxd.sock

help:
	@printf '%s\n' 'make dev             Run the app with its own oxd' 'make check           Check formatting, lints, and tests' 'make check-rust      Check the Rust workspace' 'make check-ui        Check the UI' 'make check-scripts   Check scripts/' 'make fix             Apply formatting and safe lint fixes' 'make e2e             Run the app end-to-end tests' 'make smoke-claude    Run live Claude ACP smoke checks' 'make smoke-opencode  Run live OpenCode ACP smoke checks'

DEV_OXD := pkill -f "[o]xd --socket $(DEV_SOCKET)$$"

# The dev oxd is stopped before building and whenever the app exits,
# including on Ctrl-C.
dev:
	-$(DEV_OXD)
	cargo build -p oxd -p claude-code-acp
	trap '$(DEV_OXD)' EXIT; cd ui && OX_SOCKET="$(DEV_SOCKET)" npm run tauri dev

check: check-rust check-ui check-scripts

check-rust:
	cargo fmt --all --check
	cargo clippy --workspace --all-targets -- -D warnings
	cargo test --workspace

check-ui:
	cd ui && npm run check

check-scripts:
	$(RUFF) check scripts
	$(RUFF) format --check scripts

fix:
	cargo fmt --all
	cd ui && npm run fix
	$(RUFF) check --fix scripts
	$(RUFF) format scripts

# The app is built with the embedded WebDriver server and the UI bundled
# into it, beside the oxd, oxctl, and fake agent binaries the tests use.
e2e:
	cargo build -p oxd -p oxctl
	cd ui && npm run tauri -- build --debug --no-bundle --features webdriver
	cd ui && npm run e2e

smoke-claude:
	$(PYTHON) scripts/smoke-acp.py claude --model "$(CLAUDE_MODEL)"

smoke-opencode:
	$(PYTHON) scripts/smoke-acp.py opencode --model "$(OPENCODE_MODEL)"
