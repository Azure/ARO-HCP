ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
LOCATION ?= {{ .region }}
RESOURCEGROUP ?= {{ .serviceClusterRG }}
AKS_NAME ?= {{ .aksName }}
DB_NAME ?= {{ .frontendCosmosDBName }}
