ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
LOCATION ?= {{ .region }}
RESOURCEGROUP ?= {{ .svc.rg }}
AKS_NAME ?= {{ .aksName }}
DB_NAME ?= {{ .frontend.cosmosDB.name }}
