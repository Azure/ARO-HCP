SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_BASE_IMAGE ?= devarohcp.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

# for deploying frontend into private aks cluster via invoke command
# these values must be set
RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
CLUSTER_NAME ?=
DEPLOYMENTNAME=$(RESOURCEGROUP)
REGION ?= $(shell az group show -n ${RESOURCEGROUP} --query location)
NAMESPACE ?= aro-hcp

frontend:
	go build -o aro-hcp-frontend .

run:
	./aro-hcp-frontend --use-cache --region eastus --clusters-service-url http://localhost:8000

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
ifeq ($(shell docker images --filter reference=${ARO_HCP_FRONTEND_IMAGE} --quiet),)
	pushd .. && git archive --output frontend/archive.tar.gz HEAD && popd
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE} .
	rm -f archive.tar.gz
endif

push: image
	docker push ${ARO_HCP_FRONTEND_IMAGE}


kustomize-update:
	pushd deploy/overlays/dev;\
	FRONTEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend-${REGION} \
			--query clientId);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name') DB_NAME=$${DB_NAME:-"none"};\
	kustomize edit set configmap frontend-config \
		--from-literal=DB_NAME="$${DB_NAME}" \
		--from-literal=DB_URL="https://$${DB_NAME}.documents.azure.com:443/" \
		--from-literal=FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" \
		--from-literal=CURRENT_VERSION=${ARO_HCP_FRONTEND_IMAGE} \
		--from-literal=REGION="${REGION}"

kustomize-update-gha:
	pushd deploy/overlays/dev;\
	FRONTEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend-${REGION} \
			--query clientId);\
	kustomize edit set configmap frontend-config \
		--from-literal=DB_NAME="${DB_NAME}" \
		--from-literal=DB_URL="${DB_URL}" \
		--from-literal=FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" \
		--from-literal=CURRENT_VERSION=${ARO_HCP_FRONTEND_IMAGE} \
		--from-literal=REGION="${REGION}"

kustomize-deploy:
	CHECK=$(shell kubectl get ns -o name ${NAMESPACE} 2>/dev/null);\
	if [[ -z "$${CHECK}" ]]; then kubectl create ns ${NAMESPACE}; fi
	kubectl apply -n ${NAMESPACE} -k deploy/overlays/dev

kustomize-undeploy:
	kubectl delete -n ${NAMESPACE} -k deploy/overlays/dev && \
	kubectl delete ns ${NAMESPACE}

deploy:
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCEGROUP} \
			-n ${DEPLOYMENTNAME} \
			--query properties.outputs.frontend_mi_client_id.value);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name') DB_NAME=$${DB_NAME:-"none"};\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" \
		-p DB_NAME="$${DB_NAME}" \
		-p REGION=${REGION}| oc apply -f -

undeploy:
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="null" \
		-p REGION="null" | oc delete -f -

deploy-private:
	TMP_DEPLOY=$(shell mktemp);\
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCEGROUP} \
			-n ${DEPLOYMENTNAME} \
			--query properties.outputs.frontend_mi_client_id.value);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name' || echo "none");\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" \
		-p DB_NAME="$${DB_NAME}" \
		-p REGION=${REGION}> "$${TMP_DEPLOY}";\
	az aks command invoke --resource-group ${RESOURCEGROUP} --name ${CLUSTER_NAME} --command "kubectl create -f $$(basename $${TMP_DEPLOY})" --file "$${TMP_DEPLOY}"

undeploy-private:
	TMP_DEPLOY=$(shell mktemp);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="null" \
		-p REGION="null" > "$${TMP_DEPLOY}";\
	az aks command invoke --resource-group ${RESOURCEGROUP} --name ${CLUSTER_NAME} --command "kubectl delete -f $$(basename $${TMP_DEPLOY})" --file "$${TMP_DEPLOY}"

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go

.PHONY: frontend clean image deploy undeploy deploy-private undeploy-private
