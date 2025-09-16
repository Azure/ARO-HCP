include ../.bingo/Variables.mk
SHELL = /bin/bash

# VERSION determines which API version to generate with autorest.
# The default value for VERSION should be the latest API version.
VERSION ?= v20251223preview

AUTOREST_OUTPUT_DIR = ../internal/api/${VERSION}/generated

.DEFAULT_GOAL := generate

.PHONY: generate
generate: swagger models

.PHONY: swagger
swagger:
	npm run format-check
	npm run compile

.PHONY: models
models: $(GOIMPORTS)
	VERSION=${VERSION} npm run models
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../internal
	# Remove the client API, leave only the constants and models.
	rm --force ${AUTOREST_OUTPUT_DIR}/client_factory.go
	rm --force ${AUTOREST_OUTPUT_DIR}/*_client.go
	rm --force ${AUTOREST_OUTPUT_DIR}/options.go
	rm --force ${AUTOREST_OUTPUT_DIR}/responses.go

.PHONY: fmt
fmt:
	npm run format
