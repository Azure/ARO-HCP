using '../templates/mgmt-infra-lookup.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'
