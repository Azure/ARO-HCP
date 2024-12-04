REGION ?= {{ .region }}
SVC_RESOURCEGROUP ?= {{ .svc.rg }}
MGMT_RESOURCEGROUP ?= {{ .mgmt.rg }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
SVC_KV_RESOURCEGROUP ?= {{ .serviceKeyVault.rg }}
SVC_KV_NAME ?= {{ .serviceKeyVault.name }}
GLOBAL_RESOURCEGROUP ?= {{ .global.rg }}
GLOBAL_REGION ?= {{ .global.region }}
IMAGE_SYNC_RESOURCEGROUP ?= {{ .imageSync.rg }}
IMAGE_SYNC_ENVIRONMENT ?= {{ .imageSync.environmentName }}
ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
REPOSITORIES_TO_SYNC ?= '{{ .imageSync.repositories }}'
AKS_NAME ?= {{ .aksName }}
CS_PG_NAME ?= {{ .clusterService.postgres.name }}
MAESTRO_PG_NAME ?= {{ .maestro.postgres.name }}
OIDC_STORAGE_ACCOUNT ?= {{ .oidcStorageAccountName }}
CX_KV_NAME ?= {{ .cxKeyVault.name }}
MSI_KV_NAME ?= {{ .msiKeyVault.name }}
MGMT_KV_NAME ?= {{ .mgmtKeyVault.name }}
