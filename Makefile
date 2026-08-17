.PHONY: check check-docs check-go format format-docs format-go test

check: check-docs check-go test

format: format-docs format-go

format-go:
	gofmt -l -w .

format-docs:
	dprint fmt

check-go:
	go vet ./...
	staticcheck ./...

check-docs:
	dprint check

test:
	go test -race ./...
