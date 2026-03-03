SHELL = /bin/bash

# Define the binary name
BINARY = secret-sync

# Define the source files
SOURCES = $(shell find . -name '*.go' -o -name '*.json')

ENCRYPTED_SECRETS_FILE ?= ../../dev-infrastructure/data/encryptedsecrets.yaml
KEY_VAULT_URI ?= https://arohcpdev-global.vault.azure.net
KEY_ENCRYPTION_KEY_NAME ?= secretSyncKey

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST) go.mod go.sum
	go build -ldflags="-s -w" -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

# Validates the correctness of encrypted secrets (requires az login to dev tenant)
test-decrypt:
	ENCRYPTED_SECRETS_CONFIG="$$(yq '.' $(ENCRYPTED_SECRETS_FILE))" \
	KEY_VAULT_URI=$(KEY_VAULT_URI) \
	KEY_ENCRYPTION_KEY_NAME=$(KEY_ENCRYPTION_KEY_NAME) \
	go test -tags decrypttest -v -timeout 5m .

.PHONY: clean test-decrypt
