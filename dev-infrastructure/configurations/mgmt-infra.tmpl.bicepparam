using '../templates/mgmt-infra.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'
param cxKeyVaultPrivate = {{ .cxKeyVault.private }}
param cxKeyVaultSoftDelete = {{ .cxKeyVault.softDelete }}

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'
param msiKeyVaultPrivate = {{ .msiKeyVault.private }}
param msiKeyVaultSoftDelete = {{ .msiKeyVault.softDelete }}

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'
param mgmtKeyVaultPrivate = {{ .mgmtKeyVault.private }}
param mgmtKeyVaultSoftDelete = {{ .mgmtKeyVault.softDelete }}

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'

// MI for resource access during pipeline runs
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

// Cluster Service identity
// used for Key Vault access
param clusterServiceMIResourceId = '__clusterServiceMIResourceId__'

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'
