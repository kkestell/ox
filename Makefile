.PHONY: check check-docs check-go format format-docs format-go install test test-client

check: check-docs check-go test

format: format-docs format-go

format-go:
	gofmt -l -w .

format-docs:
	dprint fmt

install:
	GOBIN="$${HOME:?}/.local/bin" go install ./cmd/ox

check-go:
	test -z "$$(gofmt -l . | tee /dev/stderr)"
	go vet ./...
	staticcheck ./...

check-docs:
	dprint check

test:
	go test -race -count=1 ./...

test-client:
	git submodule update --init --depth 1 internal/e2e/browser/acp-ui
	npm ci --no-audit --no-fund --prefix internal/e2e/browser
	npm exec --prefix internal/e2e/browser playwright install chromium
	npm test --prefix internal/e2e/browser
