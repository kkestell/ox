HUGO_VERSION := 0.161.1
HUGO := ./bin/hugo

.PHONY: help build serve clean

help:
	@echo "make build  - build the site to ./public"
	@echo "make serve  - run the dev server with drafts on http://localhost:1313"
	@echo "make clean  - remove generated output"

$(HUGO):
	@mkdir -p bin
	@echo "Installing Hugo v$(HUGO_VERSION) into ./bin..."
	GOBIN=$(CURDIR)/bin go install github.com/gohugoio/hugo@v$(HUGO_VERSION)

build: $(HUGO)
	$(HUGO) --minify

serve: $(HUGO)
	$(HUGO) server --buildDrafts --bind 0.0.0.0

clean:
	rm -rf public resources .hugo_build.lock
