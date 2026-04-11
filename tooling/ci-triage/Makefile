BINARY = ci-triage
SOURCES = $(shell find . -name '*.go' -not -path './vendor/*')

.PHONY: build test clean

build: $(BINARY)

$(BINARY): $(SOURCES)
	go build -o $(BINARY) .

test:
	go test -v -timeout 300s -cover ./...

clean:
	rm -f $(BINARY)
