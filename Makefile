.PHONY: check check-all check-client check-docs check-go eval-live format format-docs format-go install test test-all test-client test-eval test-race

unit_packages = $$(go list ./... | grep -vE '/(internal/e2e|integration)$$')

check: check-client check-docs check-go test

check-all: check-client check-docs check-go test-all test-client

check-client:
	cd client && bun run check && bun run test

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
	go test -count=1 $(unit_packages)

test-race:
	go test -race -count=1 $(unit_packages)

test-all:
	go test -race -count=1 ./...

test-client:
	cd client && bun run test:e2e

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
			-model deepseek/deepseek-v4-flash-0731 \
			-task "$$TASK" \
			-out "$${OUTPUT:-eval-results}" \
			-repetitions "$${REPETITIONS:-1}"
