include ../.bingo/Variables.mk
SHELL = /bin/bash

# VERSION determines which API version to generate.
# The default value for VERSION should be the latest API version.
# Note: typespec-go can only generate for the latest API version defined in
# the TypeSpec source. Older versions' generated code is stable and stays
# committed as-is. To regenerate older versions, use a git checkout of the
# code from when that version was the latest.
VERSION ?= v20260630preview

TYPESPEC_GO_OUTPUT_DIR = ../internal/api/${VERSION}/generated
TESTSDK_OUTPUT_DIR = ../test/sdk/${VERSION}/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp

SPEC_BASE_DIR = redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters
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

.PHONY: fmt
fmt:
	npx prettier \
		--write \
		--plugin @typespec/compiler/internals/prettier-formatter \
		"redhatopenshift/**/*.tsp"

swagger: fmt
	npx tsp compile $(SPEC_BASE_DIR) \
		--warn-as-error \
		--config $(SPEC_BASE_DIR)/tspconfig.yaml
.PHONY: swagger

.PHONY: models
models: $(GOIMPORTS)
	npx tsp compile $(SPEC_BASE_DIR) \
		--warn-as-error \
		--emit @azure-tools/typespec-go \
		--option "@azure-tools/typespec-go.emitter-output-dir=$(CURDIR)/$(TYPESPEC_GO_OUTPUT_DIR)" \
		--option "@azure-tools/typespec-go.module=github.com/Azure/ARO-HCP/internal/api/$(VERSION)/generated" \
		--option "@azure-tools/typespec-go.disallow-unknown-fields=true" \
		--option "@azure-tools/typespec-go.generate-fakes=false" \
		--option "@azure-tools/typespec-go.inject-spans=false" \
		--option "@azure-tools/typespec-go.fix-const-stuttering=true" \
		--option "@azure-tools/typespec-go.flavor=azure" \
		--option "@azure-tools/typespec-go.generate-samples=false" \
		--option "@azure-tools/typespec-go.head-as-boolean=false" \
		--option "@azure-tools/typespec-go.service-dir=" \
		--config redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/tspconfig.yaml
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../internal
	# Remove client API files, keep only constants and models.
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/client_factory.go
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/*_client.go
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/options.go
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/responses.go
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/go.mod
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/go.sum
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/LICENSE.txt
	rm $(RM_FORCE) ${TYPESPEC_GO_OUTPUT_DIR}/version.go
	rm -rf ${TYPESPEC_GO_OUTPUT_DIR}/testdata

.PHONY: testsdk
testsdk: $(GOIMPORTS)
	npx tsp compile $(SPEC_BASE_DIR) \
		--warn-as-error \
		--emit @azure-tools/typespec-go \
		--option "@azure-tools/typespec-go.emitter-output-dir=$(CURDIR)/$(TESTSDK_OUTPUT_DIR)" \
		--option "@azure-tools/typespec-go.module=github.com/Azure/ARO-HCP/test/sdk/$(VERSION)/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp" \
		--option "@azure-tools/typespec-go.generate-fakes=false" \
		--option "@azure-tools/typespec-go.fix-const-stuttering=true" \
		--option "@azure-tools/typespec-go.flavor=azure" \
		--option "@azure-tools/typespec-go.generate-samples=false" \
		--option "@azure-tools/typespec-go.head-as-boolean=false" \
		--option "@azure-tools/typespec-go.service-dir=" \
		--config redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/tspconfig.yaml
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../test/sdk

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
		echo ""; \
		echo "==> Linting $$version"; \
		npx spectral lint --verbose --ruleset node_modules/@microsoft.azure/openapi-validator-rulesets/dist/spectral/az-arm.js $$spec_path || exit 1; \
	done
