include ../../.bingo/Variables.mk

.PHONY: build
build: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) custom

.PHONY: test
test: build
	@echo "Running unit tests..."
	go test -v .