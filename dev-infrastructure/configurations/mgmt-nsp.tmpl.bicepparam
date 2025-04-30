using '../templates/mgmt-nsp.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'

// ETCD KV
param aksKeyVaultName = '{{ .mgmt.aks.etcd.kvName }}'


param mgmtNSPName = '{{ .mgmt.nsp.name }}'
param mgmtNSPAccessMode = '{{ .mgmt.nsp.accessMode }}'
