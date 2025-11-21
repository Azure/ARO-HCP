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

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'

// HCP Backups Storage Account
param hcpBackupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccount.name }}'
param hcpBackupsStorageAccountZoneRedundantMode = '{{ .mgmt.hcpBackups.storageAccount.zoneRedundantMode }}'
param hcpBackupsStorageAccountPublic = {{ .mgmt.hcpBackups.storageAccount.public }}
param hcpBackupsStorageAccountContainerName = '{{ .mgmt.hcpBackups.storageAccountContainerName }}'
