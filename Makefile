SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

# for deploying frontend into private aks cluster via invoke command
# these values must be set
RESOURCE_GROUP ?=
CLUSTER_NAME ?=

all: test lint

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m | xargs go test -cover

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint:
	golangci-lint run -v $(MODULES)

frontend-build-container:
	docker build --platform="linux/amd64" -f "frontend/Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE} .

frontend-deploy:
	@test "${RESOURCE_GROUP}" != "" || (echo "RESOURCE_GROUP must be defined" && exit 1)
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCE_GROUP} \
			-n "hcp-${USER}-dev-infra" \
			--query properties.outputs.frontend_mi_client_id.value);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" | oc apply -f -

frontend-undeploy:
	@test "${RESOURCE_GROUP}" != "" || (echo "RESOURCE_GROUP must be defined" && exit 1)
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCE_GROUP} \
			-n "hcp-${USER}-dev-infra" \
			--query properties.outputs.frontend_mi_client_id.value);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" | oc delete -f -

frontend-deploy-private:
	@test "${RESOURCE_GROUP}" != "" && test "${CLUSTER_NAME}" != "" || (echo "RESOURCE_GROUP and CLUSTER_NAME must be defined" && exit 1)
	TMP_DEPLOY=$(shell mktemp);\
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCE_GROUP} \
			-n "hcp-${USER}-dev-infra" \
			--query properties.outputs.frontend_mi_client_id.value);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" > "$${TMP_DEPLOY}";\
	az aks command invoke --resource-group ${RESOURCE_GROUP} --name ${CLUSTER_NAME} --command "kubectl create -f $$(basename $${TMP_DEPLOY})" --file "$${TMP_DEPLOY}"

frontend-undeploy-private:
	@test "${RESOURCE_GROUP}" != "" && test "${CLUSTER_NAME}" != "" || (echo "RESOURCE_GROUP and CLUSTER_NAME must be defined" && exit 1)
	TMP_DEPLOY=$(shell mktemp);\
	FRONTEND_MI_CLIENT_ID=$(shell az deployment group show \
			-g ${RESOURCE_GROUP} \
			-n "hcp-${USER}-dev-infra" \
			--query properties.outputs.frontend_mi_client_id.value);\
	oc process -f ./deploy/aro-hcp-frontend.yml --local \
		-p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} \
		-p FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" > "$${TMP_DEPLOY}";\
	az aks command invoke --resource-group ${RESOURCE_GROUP} --name ${CLUSTER_NAME} --command "kubectl delete -f $$(basename $${TMP_DEPLOY})" --file "$${TMP_DEPLOY}"

.PHONY: all clean frontend-build frontend-build-container frontend-deploy frontend-undeploy lint test
