BINARY = ci-triage
SOURCES = $(shell find . -name '*.go' -not -path './vendor/*')
CURRENT_COMMIT := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

.PHONY: build test clean

build: $(BINARY)

$(BINARY): $(SOURCES)
	CGO_ENABLED=1 go build -o $(BINARY) .

test:
	CGO_ENABLED=1 go test -v -timeout 300s -cover ./...

clean:
	rm -f $(BINARY)
