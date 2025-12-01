ADMIN_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../tooling/hcpctl/Variables.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ADMIN_API_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
ADMIN_API_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${ADMIN_API_IMAGE_REPOSITORY} ../generate-repo.sh)
ADMIN_API_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ADMIN_API_GENERATED_IMAGE_REPOSITORY):$(ADMIN_API_IMAGE_TAG)

# Binary name
BINARY = aro-hcp-admin
CLI_BINARY = aro-hcp-admin-cli

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/admin/pkg/version.commit=${ARO_HCP_REVISION} \
	-X github.com/Azure/ARO-HCP/admin/pkg/version.buildDate=${BUILD_DATE}"

# Source files
ADMIN_API_SOURCES = $(shell find server ../internal -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
CLI_SOURCES = $(shell find client -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(ADMIN_API_SOURCES)
	cd server && go build $(LDFLAGS) -o ../$(BINARY) .

run: $(BINARY)
	@COSMOS_DB_URL=$$(az cosmosdb show -n ${COSMOS_DB_NAME} -g ${REGION_RG} --query documentEndpoint -o tsv) && \
	./${BINARY} serve --location ${LOCATION} --cosmos-url $${COSMOS_DB_URL} --cosmos-name ${COSMOS_DB_NAME}
.PHONY: run

run-with-sc-integration: $(BINARY)
	COSMOS_DB_URL=$$(az cosmosdb show -n ${COSMOS_DB_NAME} -g ${REGION_RG} --query documentEndpoint -o tsv) && \
	KUSTO_ENDPOINT=$$(az kusto cluster show -n ${KUSTO_CLUSTER_NAME} -g ${KUSTO_RG} --query uri -o tsv) && \
	HCPCTL=$(HCPCTL) ./hack/run-with-port-forward.sh "${SVC_CLUSTER}" "${CS_PORT_FORWARD_SPEC}" \
		./hack/run-as-mi.sh "${SVC_CLUSTER}" "${ADMIN_API_NAMESPACE}" "${ADMIN_API_SERVICE_ACCOUNT}" \
			./${BINARY} serve \
				--location ${LOCATION} \
				--clusters-service-url http://localhost:${CS_LOCAL_PORT} \
				--cosmos-url $${COSMOS_DB_URL} \
				--cosmos-name ${COSMOS_DB_NAME} \
				--kusto-endpoint $${KUSTO_ENDPOINT}
.PHONY: run-with-sc-integration

clean:
	rm -f ${BINARY}
.PHONY: clean

image: Dockerfile $(ADMIN_API_SOURCES) Makefile
	@rm -f $(BINARY)
	cd $(ADMIN_DIR)/.. && \
	docker build . --file admin/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${ADMIN_API_IMAGE_TAG} \
		--tag ${ADMIN_API_TAGGED_IMAGE}

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ADMIN_API_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/amin-api-override-config-$(date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${ADMIN_API_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.adminApi.image.repository = \"$(ADMIN_API_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.adminApi.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

deploy: build-and-push record-override
	make -C .. pipeline/AdminAPI OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy

port-forward: $(HCPCTL)
	HCPCTL=$(HCPCTL) ./hack/run-with-port-forward.sh "${SVC_CLUSTER}" "${ADMIN_API_NAMESPACE}/admin-api/8443/8443" \
		sleep 42d
.PHONY: port-forward

#
#   C L I
#

build-cli: $(CLI_BINARY)
.PHONY: build-cli

$(CLI_BINARY): $(CLI_SOURCES)
	cd client && go build $(LDFLAGS) -o ../$(CLI_BINARY) .

run-hello-world-test: $(CLI_BINARY)
	./${CLI_BINARY} hello-world \
		--ga-auth-cert-kv ${GA_AUTH_CERT_KV} \
		--ga-auth-cert-secret ${GA_AUTH_CERT_SECRET} \
		--ga-auth-tenant-id ${GA_AUTH_TENANT_ID} \
		--ga-auth-client-id ${GA_AUTH_CLIENT_ID} \
		--ga-auth-scopes ${GA_AUTH_SCOPES} \
		--host ${ADMIN_API_HOST} \
		--admin-api-endpoint ${ADMIN_API_ENDPOINT_BASE} \
		--insecure-skip-verify
.PHONY: run-hello-world-test

run-hello-world-test-with-port-forward: $(CLI_BINARY) $(HCPCTL)
	HCPCTL=$(HCPCTL) ./hack/run-with-port-forward.sh "${SVC_CLUSTER}" "${ISTIO_PORT_FORWARD_SPEC}" \
		./${CLI_BINARY} hello-world \
			--ga-auth-cert-kv "${GA_AUTH_CERT_KV}" \
			--ga-auth-cert-secret "${GA_AUTH_CERT_SECRET}" \
			--ga-auth-tenant-id "${GA_AUTH_TENANT_ID}" \
			--ga-auth-client-id "${GA_AUTH_CLIENT_ID}" \
			--ga-auth-scopes "${GA_AUTH_SCOPES}" \
			--host "${ADMIN_API_HOST}" \
			--admin-api-endpoint "https://localhost:$(PORT_FORWARD_LOCAL_PORT)" \
			--insecure-skip-verify
.PHONY: run-with-sc-integration