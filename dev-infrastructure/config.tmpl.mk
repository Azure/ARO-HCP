REGION ?= {{ .region }}
SVC_RESOURCEGROUP ?= {{ .svc.rg }}
MGMT_RESOURCEGROUP ?= {{ .mgmt.rg }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
SVC_KV_RESOURCEGROUP ?= {{ .serviceKeyVault.rg }}
SVC_KV_NAME ?= {{ .serviceKeyVault.name }}
GLOBAL_RESOURCEGROUP ?= {{ .global.rg }}
GLOBAL_REGION ?= {{ .global.region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
SVC_AKS_NAME ?= {{ .svc.aks.name }}
MGMT_AKS_NAME ?= {{ .mgmt.aks.name }}
CS_PG_NAME ?= {{ .clusterService.postgres.name }}
CS_MI_NAME ?= {{ .clusterService.managedIdentityName }}
CS_NS_NAME ?= {{ .clusterService.k8s.namespace }}
CS_SA_NAME ?= {{ .clusterService.k8s.serviceAccountName }}
MAESTRO_PG_NAME ?= {{ .maestro.postgres.name }}
OIDC_STORAGE_ACCOUNT ?= {{ .oidcStorageAccountName }}
CX_KV_NAME ?= {{ .cxKeyVault.name }}
MSI_KV_NAME ?= {{ .msiKeyVault.name }}
MGMT_KV_NAME ?= {{ .mgmtKeyVault.name }}
