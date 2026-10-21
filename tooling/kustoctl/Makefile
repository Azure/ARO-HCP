SHELL = /bin/bash

BINARY = kustoctl

SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build -o $(BINARY) .

test:
	go test -v -timeout 300s -cover ./...
.PHONY: test

clean:
	rm -f $(BINARY)
.PHONY: clean
