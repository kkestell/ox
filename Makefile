.PHONY: format-docs inspect install

install:
	@./scripts/install.sh

format-docs:
	npx prettier --write docs/**/*.md
