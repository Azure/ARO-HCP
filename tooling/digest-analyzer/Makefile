.PHONY: build clean run test

BINARY_NAME=digest-analyzer
CONFIG_DIR=../../config

build:
	GOWORK=off go build -o $(BINARY_NAME) .

clean:
	rm -f $(BINARY_NAME)

run: build
	./$(BINARY_NAME) $(CONFIG_DIR)

test:
	GOWORK=off go test ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

.DEFAULT_GOAL := build