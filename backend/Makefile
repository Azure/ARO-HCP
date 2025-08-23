-include ../setup-env.mk

ARO_HCP_REVISION = $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_TAG ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

ifeq ($(ARO_HCP_IMAGE_REGISTRY:%.azurecr.io=azurecr.io),azurecr.io)
ARO_HCP_IMAGE_REGISTRY_IS_ACR = 1
endif

.DEFAULT_GOAL := backend

backend:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_IMAGE_TAG}" -o aro-hcp-backend .

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
	                         --build-arg REVISION=${ARO_HCP_REVISION} \
	                         --build-arg TAG=${ARO_HCP_IMAGE_TAG} \
	                         --tag ${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:${ARO_HCP_IMAGE_TAG}"
.PHONY: image

login:
ifdef ARO_HCP_IMAGE_REGISTRY_IS_ACR
	az acr login --name ${ARO_HCP_IMAGE_ACR}
else
	docker login ${ARO_HCP_IMAGE_REGISTRY}
endif
.PHONY: login

push: image login
	docker push \
		${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:${ARO_HCP_IMAGE_TAG}
ifndef ARO_HCP_IMAGE_REGISTRY_IS_ACR
	# The floating "latest" tag tracks the most recently pushed image for "deploy" to use.
	docker tag \
		${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:${ARO_HCP_IMAGE_TAG} \
		${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:latest
endif
.PHONY: push

config:
	DIGEST=$$(../get-digest.sh ${ARO_HCP_IMAGE_ACR} ${ARO_HCP_IMAGE_REPOSITORY}) && \
	yq --inplace '.clouds.*.environments.*.defaults.backend.image.digest = strenv(DIGEST)' ../config/config.msft.clouds-overlay.yaml && \
	git diff
.PHONY: config

deploy:
	if [ -n "${ARO_HCP_IMAGE_REGISTRY_IS_ACR}" ]; \
	then \
		IMAGE_TAG=${ARO_HCP_IMAGE_TAG} && \
		DIGEST=$$(../get-digest.sh ${ARO_HCP_IMAGE_ACR} ${ARO_HCP_IMAGE_REPOSITORY}) && \
		ARO_HCP_IMAGE_DIGEST=${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}@$${DIGEST}; \
	else \
		ARO_HCP_IMAGE_DIGEST=$$(docker inspect --format '{{ index .RepoDigests 0 }}' \
			${ARO_HCP_IMAGE_REGISTRY}/${ARO_HCP_IMAGE_REPOSITORY}:latest); \
	fi; \
	BACKEND_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n backend \
			--query clientId -o tsv) && \
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${REGION_RG} --query documentEndpoint -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	../hack/helm.sh aro-hcp-backend-dev deploy aro-hcp \
		--set configMap.databaseName=${DB_NAME} \
		--set configMap.databaseUrl="$${DB_URL}" \
		--set configMap.backendMiClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion="$${ARO_HCP_IMAGE_DIGEST}" \
		--set configMap.location=${LOCATION} \
		--set clustersService.namespace=${CS_NAMESPACE} \
		--set clustersService.serviceAccount=${CS_SERVICE_ACCOUNT_NAME} \
		--set deployment.imageName="$${ARO_HCP_IMAGE_DIGEST}" \
		--set tracing.address=${TRACING_ADDRESS} \
		--set tracing.exporter=${TRACING_EXPORTER}
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-backend-dev --namespace aro-hcp
.PHONY: undeploy
