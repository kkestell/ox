.PHONY: check check-docs check-go format format-docs format-go install test

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
