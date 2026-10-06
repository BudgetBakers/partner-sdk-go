.PHONY: lint test build

# Guarded on go: machines without the Go toolchain no-op honestly instead of
# failing the repo-wide make lint/test.
ifeq (,$(shell command -v go 2>/dev/null))
lint test build:
	@echo "sdks/go: go not installed - skipping $@ (https://go.dev/dl/)"
else
lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

test:
	go test -race ./...

build:
	go build -o bin/conformance ./cmd/conformance
endif
