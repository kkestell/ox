.PHONY: check check-docs check-go eval-live format format-docs format-go install test test-client test-client-live test-eval

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

test-client-live:
	git submodule update --init --depth 1 internal/e2e/browser/acp-ui
	npm ci --no-audit --no-fund --prefix internal/e2e/browser
	npm exec --prefix internal/e2e/browser playwright install chromium
	set -a; . ./.env; set +a; \
		test -n "$${OPENROUTER_API_KEY:-}" || (echo "OPENROUTER_API_KEY is required" >&2; exit 1); \
	npm run test:live --prefix internal/e2e/browser

test-eval:
	go test -tags=evalsmoke -count=1 ./evals/internal/eval

eval-live:
	test -n "$${TASK:-}" || (echo "TASK is required" >&2; exit 1)
	set -a; . ./.env; set +a; \
		eval_build_dir="$$(mktemp -d)"; \
		trap 'rm -r "$$eval_build_dir"' EXIT; \
		go build -o "$$eval_build_dir/ox" ./cmd/ox; \
		go run ./evals/cmd/ox-eval -live \
			-ox "$$eval_build_dir/ox" \
			-model openai/gpt-5.6-luna \
			-task "$$TASK" \
			-out "$${OUTPUT:-eval-results}" \
			-repetitions "$${REPETITIONS:-1}"
