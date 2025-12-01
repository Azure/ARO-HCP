using '../templates/svc-mgmt-permissions.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'

// Cluster Service identity
// used for Key Vault access
param clusterServiceMIResourceId = '__clusterServiceMIResourceId__'

// MSI Refresher identity
// used for Key Vault access
param msiRefresherMIResourceId = '__msiRefresherMIResourceId__'

// Admin API identity
// used for Resource Group introspection
param adminApiMIResourceId = '__adminApiMIResourceId__'
