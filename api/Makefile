include ../.bingo/Variables.mk
SHELL = /bin/bash

# VERSION determines which API version to generate with autorest.
# The default value for VERSION should be the latest API version.
VERSION ?= v20251223preview

AUTOREST_OUTPUT_DIR = ../internal/api/${VERSION}/generated

SPEC_BASE_DIR = redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters
API_SPEC_PATHS = $(shell find $(SPEC_BASE_DIR) -type f -name "openapi.json" 2>/dev/null | sort)
API_VERSIONS = $(shell echo '$(API_SPEC_PATHS)' | tr ' ' '\n' | sed 's|/openapi.json||' | xargs -n1 basename | sort -u)

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    RM_FORCE = -f
else
    RM_FORCE = --force
endif

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
	rm $(RM_FORCE) ${AUTOREST_OUTPUT_DIR}/client_factory.go
	rm $(RM_FORCE) ${AUTOREST_OUTPUT_DIR}/*_client.go
	rm $(RM_FORCE) ${AUTOREST_OUTPUT_DIR}/options.go
	rm $(RM_FORCE) ${AUTOREST_OUTPUT_DIR}/responses.go

.PHONY: testsdk
testsdk: $(GOIMPORTS)
	VERSION=${VERSION} npm run testsdk
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../test/sdk

.PHONY: fmt
fmt:
	npm run format

.PHONY: validate-examples
validate-examples:
	@echo "Validating examples for all API versions..."
	@for spec_path in $(API_SPEC_PATHS); do \
		version=$$(basename $$(dirname $$spec_path)); \
		echo ""; \
		echo "==> Validating $$version"; \
		npx oav validate-example $$spec_path || exit 1; \
	done

.PHONY: lint-openapi
lint-openapi:
	@echo "Linting OpenAPI specs with Azure ARM rules for all API versions..."
	@for spec_path in $(API_SPEC_PATHS); do \
		version=$$(basename $$(dirname $$spec_path)); \
		readme_path=$$(dirname $$(dirname $$spec_path))/readme.md; \
		echo ""; \
		echo "==> Linting $$version (using $$readme_path)"; \
		npx autorest \
			--v3 \
			--azure-validator \
			--spectral \
			--semantic-validator=false \
			--model-validator=false \
			--openapi-type=arm \
			$$readme_path || exit 1; \
	done
