.DEFAULT_GOAL := help
.PHONY: help dev smoke-claude smoke-opencode

PYTHON ?= python3
CLAUDE_MODEL ?= haiku
OPENCODE_MODEL ?= openai/gpt-6-luna
DEV_SOCKET := $(CURDIR)/target/dev/oxd.sock

help:
	@printf '%s\n' 'make dev             Run the app with its own oxd' 'make smoke-claude    Run live Claude ACP smoke checks' 'make smoke-opencode  Run live OpenCode ACP smoke checks'

DEV_OXD := pkill -f "[o]xd --socket $(DEV_SOCKET)$$"

# The dev oxd is stopped before building and whenever the app exits,
# including on Ctrl-C.
dev:
	-$(DEV_OXD)
	cargo build -p oxd -p claude-code-acp
	trap '$(DEV_OXD)' EXIT; cd ui && OX_SOCKET="$(DEV_SOCKET)" npm run tauri dev

smoke-claude:
	$(PYTHON) scripts/smoke-acp.py claude --model "$(CLAUDE_MODEL)"

smoke-opencode:
	$(PYTHON) scripts/smoke-acp.py opencode --model "$(OPENCODE_MODEL)"
