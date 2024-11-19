REGION ?= {{ .region }}
CONSUMER_NAME ?= {{ .maestro.consumerName }}
RESOURCEGROUP ?= {{ .svc.rg }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
AKS_NAME ?= {{ .aksName }}
SERVICE_KV ?= {{ .serviceKeyVault.name }}
OIDC_STORAGE_ACCOUNT ?= {{ .oidcStorageAccountName }}
IMAGE_REPO ?= {{ .clusterService.imageRepo }}
IMAGE_TAG ?= {{ .clusterService.imageTag }}
ACR_NAME ?= {{ .svcAcrName }}
OCP_ACR_NAME ?= {{ .ocpAcrName }}
AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID ?= {{ .firstPartyAppClientId }}
FPA_CERT_NAME ?= firstPartyCert
ZONE_NAME ?= {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }}

DATABASE_DISABLE_TLS ?= {{ not .clusterService.postgres.deploy }}
DATABASE_AUTH_METHOD ?= {{ ternary "az-entra" "postgres" .clusterService.postgres.deploy }}
DATABASE_SERVER_NAME ?= {{ .clusterService.postgres.name }}
DB_SECRET_TARGET = {{ ternary "deploy-azure-db-secret" "deploy-local-db-secret" .clusterService.postgres.deploy }}

DEVOPS_MSI_ID ?= {{ .aroDevopsMsiId }}

# MGMT CLUSTER KVs
MGMT_RESOURCEGROUP ?= {{ .managementClusterRG }}
CX_SECRETS_KV_NAME ?= {{ .cxKeyVaultName }}
CX_MI_KV_NAME ?= {{ .msiKeyVaultName }}
