SHELL = /bin/bash

BINARY = entra-app-credentials
SOURCES = $(shell find . -name '*.go')

$(BINARY): $(SOURCES) $(MAKEFILE_LIST) go.mod go.sum
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY) .

clean:
	rm -f $(BINARY)

.PHONY: clean
