ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_ACR_URL ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

SVC_RESOURCEGROUP ?= {{ .svc.rg }}
MGMT_RESOURCEGROUP ?= {{ .mgmt.rg }}