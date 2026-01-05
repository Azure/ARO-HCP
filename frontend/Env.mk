LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
FRONTEND_IMAGE_REPOSITORY ?= {{ .frontend.image.repository }}
DB_NAME ?= {{ .frontend.cosmosDB.name }}
DB_URL ?= $(shell az cosmosdb show -n {{ .frontend.cosmosDB.name }} -g {{ .regionRG }} --query documentEndpoint -o tsv)
