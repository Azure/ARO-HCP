SHELL = /bin/bash
ARO_HCP_BASE_IMAGE ?= devarohcp.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

# for deploying frontend into private aks cluster via invoke command
# these values must be set
RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
CLUSTER_NAME ?=
DEPLOYMENTNAME=$(RESOURCEGROUP)
REGION ?= eastus


deploy:
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p IMAGE_REGISTRY=devarohcp.azurecr.io \
	  -p IMAGE_REPOSITORY=uhc-clusters-service \
	  -p IMAGE_TAG=1717599112 | oc apply -f -

.PHONY: deploy
