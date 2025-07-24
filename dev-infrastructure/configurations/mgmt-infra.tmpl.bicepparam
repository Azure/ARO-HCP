using '../templates/mgmt-infra.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'
param cxKeyVaultPrivate = {{ .cxKeyVault.private }}
param cxKeyVaultSoftDelete = {{ .cxKeyVault.softDelete }}
param cxKeyVaultTagName = '{{ .cxKeyVault.tagKey }}'
param cxKeyVaultTagValue = '{{ .cxKeyVault.tagValue }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'
param msiKeyVaultPrivate = {{ .msiKeyVault.private }}
param msiKeyVaultSoftDelete = {{ .msiKeyVault.softDelete }}
param msiKeyVaultTagName = '{{ .msiKeyVault.tagKey }}'
param msiKeyVaultTagValue = '{{ .msiKeyVault.tagValue }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'
param mgmtKeyVaultPrivate = {{ .mgmtKeyVault.private }}
param mgmtKeyVaultSoftDelete = {{ .mgmtKeyVault.softDelete }}
param mgmtKeyVaultTagName = '{{ .mgmtKeyVault.tagKey }}'
param mgmtKeyVaultTagValue = '{{ .mgmtKeyVault.tagValue }}'

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'

// MI for resource access during pipeline runs
param globalMSIId = '__globalMSIId__'

// Cluster Service identity
// used for Key Vault access
param clusterServiceMIResourceId = '__clusterServiceMIResourceId__'

// MSI Refresher identity
// used for Key Vault access
param msiRefresherMIResourceId = '__msiRefresherMIResourceId__'

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'
