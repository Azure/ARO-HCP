-include ../setup-env.mk

ARO_HCP_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_REPOSITORY := arohcpfrontend
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_REGISTRY)/$(ARO_HCP_REPOSITORY)

ifeq ($(strip $(COMMIT)),)
COMMIT := $(shell az acr repository show-tags --name ${ARO_HCP_IMAGE_ACR} --repository ${ARO_HCP_REPOSITORY} --orderby time_desc --top 1 --output tsv)
endif

BUILD_TAG := $(ARO_HCP_FRONTEND_IMAGE):$(shell git rev-parse --short=7 HEAD)
DEPLOY_TAG := $(ARO_HCP_FRONTEND_IMAGE):$(COMMIT)

.DEFAULT_GOAL := frontend

testjfc:
	@echo "BUILD_TAG: $(BUILD_TAG)"
	@echo "DEPLOY_TAG: $(DEPLOY_TAG)"
	@echo "ARO_HCP_FRONTEND_IMAGE: $(ARO_HCP_FRONTEND_IMAGE)"


frontend:
	go build -o aro-hcp-frontend .

info:
	@echo "ARO_HCP_FRONTEND_IMAGE: ${ARO_HCP_FRONTEND_IMAGE}"
	@echo "COMMIT: ${COMMIT}"

run:
	./aro-hcp-frontend --use-cache --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cluster-service-provision-shard 1 \
		--cluster-service-noop-provision \
		--cluster-service-noop-deprovision

clean:
	rm -f aro-hcp-frontend
.PHONY: clean

build-push: image push

image:
	pushd .. && git archive --output frontend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE}:${BUILD_TAG} .
	rm -f archive.tar.gz
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_FRONTEND_IMAGE}:${BUILD_TAG}

deploy:
	FRONTEND_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend \
			--query clientId -o tsv); \
	SECRET_STORE_MI_CLIENT_ID=$$(az aks show --resource-group ${RESOURCEGROUP} \
			--name ${AKS_NAME} \
			--query addonProfiles.azureKeyvaultSecretsProvider.identity.clientId \
			--output tsv); \
	ISTO_VERSION=$$(az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=$${ISTO_VERSION}" --overwrite=true && \
	helm upgrade --install ${HELM_DRY_RUN} aro-hcp-frontend-dev \
		deploy/helm/frontend/ \
		--set azure.clientId=$${SECRET_STORE_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set configMap.databaseName=${DB_NAME} \
		--set configMap.databaseUrl="$${DB_URL}" \
		--set configMap.frontendMiClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set credsKeyVault.name=${SERVICE_KEY_VAULT} \
		--set credsKeyVault.secret=${CERTIFICATE_NAME} \
		--set serviceAccount.workloadIdentityClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_FRONTEND_IMAGE}:${DEPLOY_TAG} \
		--set configMap.location=${LOCATION}  \
		--set deployment.imageName=${ARO_HCP_FRONTEND_IMAGE}:${DEPLOY_TAG} \
		--namespace aro-hcp
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-frontend-dev --namespace aro-hcp
.PHONY: undeploy

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go
