SHELL = /bin/bash

# Define the binary name
BINARY = secret-sync

# Define the source files
SOURCES = $(shell find . -name '*.go' -o -name '*.json')

ENCRYPTED_SECRETS_FILE ?= ../../dev-infrastructure/data/encryptedsecrets.yaml
RENDERED_CONFIG ?= ../../config/rendered/dev/dev/westus3.yaml
KEY_VAULT_NAME ?= $(shell yq '.global.keyVault.name' < $(RENDERED_CONFIG))
KEY_VAULT_DNS_SUFFIX ?= $(shell yq '.keyVaultDNSSuffix' < $(RENDERED_CONFIG))
KEY_VAULT_URI ?= https://$(KEY_VAULT_NAME).$(KEY_VAULT_DNS_SUFFIX)
KEY_ENCRYPTION_KEY_NAME ?= $(shell yq '.global.keyVault.encryptionKeyName' < $(RENDERED_CONFIG))

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
