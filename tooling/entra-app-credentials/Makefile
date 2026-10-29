SHELL = /bin/bash

BINARY = entra-app-credentials
SOURCES = $(shell find . -name '*.go')

$(BINARY): $(SOURCES) $(MAKEFILE_LIST) go.mod go.sum force-go-build
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY) .

clean:
	rm -f $(BINARY)

.PHONY: clean

# Always consult Go's build cache, including for dependency-only changes.
.PHONY: force-go-build
force-go-build:
