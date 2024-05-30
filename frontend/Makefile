SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

# for deploying frontend into private aks cluster via invoke command
# these values must be set
RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
CLUSTER_NAME ?=
DEPLOYMENTNAME=$(RESOURCEGROUP)
REGION ?= eastus

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

deploy:
	@test "${RESOURCEGROUP}" != "" || (echo "RESOURCEGROUP must be defined" && exit 1)
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
	@test "${RESOURCEGROUP}" != "" || (echo "RESOURCEGROUP must be defined" && exit 1)
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="null" \
		-p REGION="null" | oc delete -f -

deploy-private:
	@test "${RESOURCEGROUP}" != "" && test "${CLUSTER_NAME}" != "" || (echo "RESOURCEGROUP and CLUSTER_NAME must be defined" && exit 1)
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
	@test "${RESOURCEGROUP}" != "" && test "${CLUSTER_NAME}" != "" || (echo "RESOURCEGROUP and CLUSTER_NAME must be defined" && exit 1)
	TMP_DEPLOY=$(shell mktemp);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="null" \
		-p REGION="null" > "$${TMP_DEPLOY}";\
	az aks command invoke --resource-group ${RESOURCEGROUP} --name ${CLUSTER_NAME} --command "kubectl delete -f $$(basename $${TMP_DEPLOY})" --file "$${TMP_DEPLOY}"

.PHONY: frontend clean image deploy undeploy deploy-private undeploy-private
