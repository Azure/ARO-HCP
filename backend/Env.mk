LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
BACKEND_IMAGE_REPOSITORY ?= {{ .backend.image.repository }}
DB_NAME ?= {{ .frontend.cosmosDB.name }}
DB_URL ?= $(shell az cosmosdb show -n {{ .frontend.cosmosDB.name }} -g {{ .regionRG }} --query documentEndpoint -o tsv)
RP_NAMESPACE ?= {{ .backend.k8s.namespace }}