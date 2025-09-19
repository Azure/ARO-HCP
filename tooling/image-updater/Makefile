.PHONY: build update clean help

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml

.DEFAULT_GOAL := help

build:
	go build -o $(BINARY_NAME) .

update: build
	./$(BINARY_NAME) update --config $(CONFIG_FILE) && make -C ../../ yamlfmt > /dev/null 2>&1

clean:
	rm -f $(BINARY_NAME)

help:
	@echo "build update clean"