SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

COMMIT = $(shell git rev-parse --short=7 HEAD)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

frontend:
	go build -o aro-hcp-frontend .

run:
	./aro-hcp-frontend --use-cache --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cluster-service-provision-shard 1 \
		--cluster-service-noop-provision \
		--cluster-service-noop-deprovision

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	pushd .. && git archive --output frontend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE} .
	rm -f archive.tar.gz

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_FRONTEND_IMAGE}

deploy:
	FRONTEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend \
			--query clientId);\
	ISTO_VERSION=$(shell az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	DB_URL=$(shell az cosmosdb show -n ${DB_NAME} -g ${RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=$${ISTO_VERSION}" --overwrite=true && \
	helm upgrade --install aro-hcp-frontend-dev \
		deploy/helm/frontend/ \
		--set configMap.databaseName=${DB_NAME} \
		--set configMap.databaseUrl="$${DB_URL}" \
		--set configMap.frontendMiClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_FRONTEND_IMAGE} \
		--set configMap.location=${LOCATION}  \
		--set deployment.imageName=${ARO_HCP_FRONTEND_IMAGE} \
		--namespace aro-hcp

undeploy:
	helm uninstall aro-hcp-frontend-dev --namespace aro-hcp

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go

.PHONY: frontend clean image deploy undeploy
