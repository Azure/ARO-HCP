using '../templates/output-mgmt.bicep'

param mgmtClusterName = '{{ .mgmt.aks.name }}'

param cxKeyVaultName = '{{ .cxKeyVault.name }}'

param msiKeyVaultName = '{{ .msiKeyVault.name }}'
