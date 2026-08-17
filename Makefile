.PHONY: check check-docs format format-docs

check: check-docs

format: format-docs

format-docs:
	dprint fmt

check-docs:
	dprint check
