LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
KUBE_APPLIER_IMAGE_REPOSITORY ?= {{ .kubeApplier.image.repository }}
DB_NAME ?= {{ .kubeApplier.cosmosContainerName }}
DB_URL ?= $(shell az cosmosdb show -n {{ .frontend.cosmosDB.name }} -g {{ .regionRG }} --query documentEndpoint -o tsv)
RP_NAMESPACE ?= {{ .kubeApplier.k8s.namespace }}