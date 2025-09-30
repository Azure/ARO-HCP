using '../templates/mgmt-infra-lookup.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'
