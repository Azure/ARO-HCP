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
AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID ?= {{ .miMockPrincipalId }}
AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID ?= {{ .miMockClientId }}
AZURE_ARM_HELPER_IDENTITY_CLIENT_ID ?= {{ .armHelperClientId }}
AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID ?= {{ .armHelperFPAPrincipalId }}
MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME ?= msiMockCert
ARM_HELPER_CERT_NAME ?= armHelperCert
ZONE_NAME ?= {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }}

DATABASE_DISABLE_TLS ?= {{ not .clusterService.postgres.deploy }}
DATABASE_AUTH_METHOD ?= {{ ternary "az-entra" "postgres" .clusterService.postgres.deploy }}
DATABASE_SERVER_NAME ?= {{ .clusterService.postgres.name }}
DB_SECRET_TARGET = {{ ternary "deploy-azure-db-secret" "deploy-local-db-secret" .clusterService.postgres.deploy }}

DEVOPS_MSI_ID ?= {{ .aroDevopsMsiId }}

# MGMT CLUSTER KVs
MGMT_RESOURCEGROUP ?= {{ .mgmt.rg }}
CX_SECRETS_KV_NAME ?= {{ .cxKeyVault.name }}
CX_MI_KV_NAME ?= {{ .msiKeyVault.name }}
