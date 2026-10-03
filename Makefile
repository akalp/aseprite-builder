# Development entrypoints. See docs/DEVELOPMENT.md.

BINARY_NAME := aseprite-builder

.PHONY: check fmt vet test build clean

check: fmt-check vet test

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; else echo "fmt ok"; fi

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

build:
	go build -o $(BINARY_NAME) ./cmd/aseprite-builder

clean:
	rm -rf $(BINARY_NAME) dist/
