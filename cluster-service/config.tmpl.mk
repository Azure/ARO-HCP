REGION ?= {{ .region }}
CONSUMER_NAME ?= {{ .maestroConsumerName }}
RESOURCEGROUP ?= {{ .serviceClusterRG }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
AKS_NAME ?= {{ .aksName }}
SERVICE_KV ?= {{ .serviceKeyVaultName }}
OIDC_STORAGE_ACCOUNT ?= {{ .oidcStorageAccountName }}
IMAGE_REPO ?= {{ .clusterServiceImageRepo }}
IMAGE_TAG ?= {{ .clusterServiceImageTag }}
ACR_NAME ?= {{ .svcAcrName }}
OCP_ACR_NAME ?= {{ .ocpAcrName }}
AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID ?= {{ .firstPartyAppClientId }}
FPA_CERT_NAME ?= firstPartyCert
ZONE_NAME ?= {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }}

DATABASE_DISABLE_TLS ?= {{ not .clusterServicePostgresDeploy }}
DATABASE_AUTH_METHOD ?= {{ ternary "az-entra" "postgres" .clusterServicePostgresDeploy }}
DATABASE_SERVER_NAME ?= {{ .clusterServicePostgresName }}
DB_SECRET_TARGET = {{ ternary "deploy-azure-db-secret" "deploy-local-db-secret" .clusterServicePostgresDeploy }}

DEVOPS_MSI_ID ?= {{ .aroDevopsMsiId }}

# MGMT CLUSTER KVs
MGMT_RESOURCEGROUP ?= {{ .managementClusterRG }}
CX_SECRETS_KV_NAME ?= {{ .cxKeyVaultName }}
CX_MI_KV_NAME ?= {{ .msiKeyVaultName }}
