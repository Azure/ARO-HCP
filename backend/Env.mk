LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
BACKEND_IMAGE_REPOSITORY ?= {{ .backend.image.repository }}
COSMOS_ACCOUNT_RESOURCE_ID ?= $(shell az cosmosdb show -n {{ .frontend.cosmosDB.name }} -g {{ .regionRG }} --query id -o tsv)
RP_NAMESPACE ?= {{ .backend.k8s.namespace }}
