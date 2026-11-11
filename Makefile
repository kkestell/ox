.DEFAULT_GOAL := help
.PHONY: help smoke-claude smoke-opencode

PYTHON ?= python3
CLAUDE_MODEL ?= haiku
OPENCODE_MODEL ?= openai/gpt-6-luna

help:
	@printf '%s\n' 'make smoke-claude    Run live Claude ACP smoke checks' 'make smoke-opencode  Run live OpenCode ACP smoke checks'

smoke-claude:
	$(PYTHON) scripts/smoke-acp.py claude --model "$(CLAUDE_MODEL)"

smoke-opencode:
	$(PYTHON) scripts/smoke-acp.py opencode --model "$(OPENCODE_MODEL)"
