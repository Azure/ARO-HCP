LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ADMIN_API_IMAGE_REPOSITORY ?= {{ .adminApi.image.repository }}

GA_AUTH_CERT_KV ?= https://{{ .geneva.actions.keyVault.name }}.{{ .keyVaultDNSSuffix }}
GA_AUTH_CERT_SECRET ?= {{ .geneva.actions.certificate.name }}
GA_AUTH_TENANT_ID ?= {{ .tenantId }}
GA_AUTH_CLIENT_ID ?= $(shell az ad app list --display-name '{{ .geneva.actions.application.name }}' --query '[].appId' -o tsv)
GA_AUTH_SCOPES ?= "https://management.azure.com/.default"
ADMIN_API_HOST ?= "admin.{{ .dns.regionalSubdomain }}.{{ .dns.svcParentZoneName }}"
ADMIN_API_ENDPOINT_BASE ?= "https://${ADMIN_API_HOST}"
SVC_RG ?= {{ .svc.rg }}

# Portforwarding details
SVC_CLUSTER ?= {{ .svc.aks.name }}
PORT_FORWARD_LOCAL_PORT ?= 8443
ISTIO_PORT_FORWARD_SPEC ?= aks-istio-ingress/aks-istio-ingressgateway-external/${PORT_FORWARD_LOCAL_PORT}/443

# CS details
CS_LOCAL_PORT ?= 8001
CS_REMOTE_PORT ?= 8000
CS_PORT_FORWARD_SPEC ?= {{ .clustersService.k8s.namespace }}/clusters-service/${CS_LOCAL_PORT}/${CS_REMOTE_PORT}

# Cosmos DB details
COSMOS_DB_NAME ?= {{ .frontend.cosmosDB.name }}
REGION_RG ?= {{ .regionRG }}

# Admin API details
ADMIN_API_NAMESPACE ?= {{ .adminApi.k8s.namespace }}
ADMIN_API_SERVICE_ACCOUNT ?= {{ .adminApi.k8s.serviceAccountName }}

# Kusto details
KUSTO_CLUSTER_NAME ?= {{ .kusto.kustoName }}
KUSTO_RG ?= {{ .kusto.rg }}

# FPA
FPA_CLIENT_ID ?= {{ .firstPartyAppClientId }}
FPA_CERT_NAME ?= {{ .firstPartyAppCertificate.name }}
FPA_KEY_VAULT_NAME ?= {{ .serviceKeyVault.name }}

# Sessiongate
SESSIONGATE_NAMESPACE ?= {{ .sessiongate.k8s.namespace }}