-include ../setup-env.mk
-include ../helm-cmd.mk

CURRENT_COMMIT := $(shell git rev-parse --short=7 HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_BACKEND_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ARO_HCP_IMAGE_REPOSITORY)

.DEFAULT_GOAL := backend

backend:
	go build -o aro-hcp-backend .
.PHONY: backend

run:
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	./aro-hcp-backend --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cosmos-name ${DB_NAME} \
		--cosmos-url $${DB_URL}
.PHONY: run

clean:
	rm -f aro-hcp-backend
.PHONY: clean

image:
	pushd .. && git archive --output backend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_BACKEND_IMAGE}:${CURRENT_COMMIT} .
	rm -f archive.tar.gz
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_BACKEND_IMAGE}:${CURRENT_COMMIT}
.PHONY: push

deploy:
	DIGEST=$$(../get-digest.sh ${ARO_HCP_IMAGE_ACR} arohcpbackend) \
	BACKEND_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n backend \
			--query clientId -o tsv) && \
	DB_URL=$$(az cosmosdb show -n ${DB_NAME} -g ${RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	${HELM_CMD} aro-hcp-backend-dev \
		deploy/ \
		--set configMap.databaseName=${DB_NAME} \
		--set configMap.databaseUrl="$${DB_URL}" \
		--set configMap.backendMiClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_BACKEND_IMAGE}@$${DIGEST} \
		--set configMap.location=${LOCATION} \
		--set deployment.imageName=${ARO_HCP_BACKEND_IMAGE}@$${DIGEST} \
		--namespace aro-hcp
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-backend-dev --namespace aro-hcp
.PHONY: undeploy
