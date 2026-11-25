.PHONY: dev build web test live lint e2e metrics docs hooks worktree prune

# A recipe that fails leaves no target behind, such as a partial download.
.DELETE_ON_ERROR:

# lint runs this golangci-lint release, downloaded once into bin. The version
# is in the file's name, so changing it downloads the new release.
GOLANGCI_LINT_VERSION := 2.15.0
GOLANGCI_LINT := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)

# .env holds OPENROUTER_API_KEY for Ur. Only that key reaches oxd and tests.
-include .env
export OPENROUTER_API_KEY

dev: build
	GORACE=halt_on_error=1 node cmd/ox/web/dev.mjs

build: web
	go build -race -o bin/ ./cmd/ox ./cmd/ox-mcp ./cmd/oxd

test: lint
	go test -race ./...
	node --test cmd/ox/web/stack.test.mjs cmd/ox/web/highlight.test.mjs cmd/ox/web/toolInput.test.mjs cmd/ox/web/id.test.mjs

lint: web $(GOLANGCI_LINT)
	gofmt -l . | (! grep .)
	go mod tidy -diff
	go fix -diff ./...
	$(GOLANGCI_LINT) run ./...
	npm --prefix cmd/ox/web run lint

# live drives real Claude Code processes with Haiku, through oxd and through
# the web client, and Ur through OpenRouter.
live: cmd/ox/web/node_modules
	OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server
	OX_LIVE=1 npm --prefix cmd/ox/web run e2e -- approval

# e2e drives the web client in headless Chromium against oxd and the fake harness.
e2e: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run e2e

web: cmd/ox/web/node_modules
	npm --prefix cmd/ox/web run build

# metrics prints size and complexity of the files staged for commit.
metrics: cmd/ox/web/node_modules
	@python3 scripts/metrics.py

docs:
	dprint fmt

hooks:
	git config core.hooksPath .githooks

# worktree makes $(WORKTREES)/$(NAME) on a new branch $(NAME) from
# origin/main, with no upstream, so the first push names its own branch.
WORKTREES ?= $(HOME)/.local/share/worktrees/ox
worktree: prune
	@test -n "$(NAME)" || { echo "usage: make worktree NAME=<slug>" >&2; exit 1; }
	git fetch origin
	mkdir -p $(WORKTREES)
	git worktree add $(WORKTREES)/$(NAME) -b $(NAME) --no-track origin/main
	if [ -e .env ]; then ln -s $(CURDIR)/.env $(WORKTREES)/$(NAME)/.env; fi

# prune removes each local branch whose pull request has merged, and its
# worktree.
prune:
	scripts/prune.sh

$(GOLANGCI_LINT):
	mkdir -p bin
	release=golangci-lint-$(GOLANGCI_LINT_VERSION)-$$(go env GOOS)-$$(go env GOARCH); \
	curl -fsSL https://github.com/golangci/golangci-lint/releases/download/v$(GOLANGCI_LINT_VERSION)/$$release.tar.gz \
		| tar -xzO $$release/golangci-lint > $@
	chmod +x $@

cmd/ox/web/node_modules: cmd/ox/web/package-lock.json
	npm --prefix cmd/ox/web ci
	touch $@
