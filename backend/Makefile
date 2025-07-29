-include ../setup-env.mk
-include ../helm-cmd.mk

ARO_HCP_IMAGE_TAG ?= $(shell git rev-parse --short=7 HEAD)$(shell git status --porcelain --untracked-files=no | grep --quiet . && echo -dirty)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_BACKEND_IMAGE ?= ${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:${ARO_HCP_IMAGE_TAG}

.DEFAULT_GOAL := backend

backend:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_IMAGE_TAG}" -o aro-hcp-backend .
.PHONY: backend

run:
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${REGION_RG} --query documentEndpoint -o tsv) && \
	./aro-hcp-backend --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cosmos-name ${DB_NAME} \
		--cosmos-url $${DB_URL}
.PHONY: run

clean:
	rm -f aro-hcp-backend
.PHONY: clean

image:
	@bash -c "set -e; pushd .. > /dev/null; \
	          trap 'rm --force image-environment && popd > /dev/null' EXIT; \
	          cp ${ENV_VARS_FILE} image-environment; \
	          docker build . --file backend/Dockerfile \
	                         --build-arg PLATFORM=linux/amd64 \
	                         --build-arg TAG=${ARO_HCP_IMAGE_TAG} \
	                         --tag ${ARO_HCP_BACKEND_IMAGE}"
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_BACKEND_IMAGE}
.PHONY: push

deploy:
	DIGEST=$$(../get-digest.sh ${ARO_HCP_IMAGE_ACR} arohcpbackend) \
	BACKEND_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n backend \
			--query clientId -o tsv) && \
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${REGION_RG} --query documentEndpoint -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	${HELM_CMD} aro-hcp-backend-dev \
		deploy/ \
		--set configMap.databaseName=${DB_NAME} \
		--set configMap.databaseUrl="$${DB_URL}" \
		--set configMap.backendMiClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}@$${DIGEST} \
		--set configMap.location=${LOCATION} \
		--set clustersService.namespace=${CS_NAMESPACE} \
		--set clustersService.serviceAccount=${CS_SERVICE_ACCOUNT_NAME} \
		--set deployment.imageName=${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}@$${DIGEST} \
		--set tracing.address=${TRACING_ADDRESS} \
		--set tracing.exporter=${TRACING_EXPORTER} \
		--namespace aro-hcp
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-backend-dev --namespace aro-hcp
.PHONY: undeploy
