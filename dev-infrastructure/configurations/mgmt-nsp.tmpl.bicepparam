using '../templates/mgmt-nsp.bicep'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'

// ETCD KV
param aksKeyVaultName = '{{ .mgmt.aks.etcd.name }}'


param mgmtNSPName = '{{ .mgmt.nsp.name }}'
param mgmtNSPAccessMode = '{{ .mgmt.nsp.accessMode }}'

param serviceClusterSubscriptionId = '__serviceClusterSubscriptionId__'

// HCP Backups
param hcpBackupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccountName }}'
