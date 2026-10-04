# Development entrypoints. See docs/DEVELOPMENT.md.

BINARY_NAME := aseprite-builder

.PHONY: check fmt vet test build release dist release-check clean

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

# release cross-compiles and packs the four spec §3 artifacts into
# dist/ and runs the §38 release-safety check over them. VERSION
# defaults to the nearest git tag (or 0.1.0-dev); override it with
# `make release VERSION=1.2.3`.
release:
	VERSION="$(VERSION)" go run ./cmd/release

# dist is an alias for release: the artifacts live in dist/.
dist: release

# release-check re-runs the §38 checker over an existing dist/ tree.
release-check:
	go run ./cmd/releasecheck dist/*

clean:
	rm -rf $(BINARY_NAME) dist/
