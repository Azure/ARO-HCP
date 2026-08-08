using '../templates/dev-resource-cleaner.bicep'

param clusterName = '{{ .svc.aks.name }}'
param cxKeyVaultName = '{{ .cxKeyVault.name }}'
param cxKeyVaultResourceGroup = '{{ .mgmt.rg }}'
param miKeyVaultName = '{{ .msiKeyVault.name }}'
param miKeyVaultResourceGroup = '{{ .mgmt.rg }}'
param ocpAcrName = '{{ .acr.ocp.name }}'
param ocpAcrResourceGroup = '{{ .global.rg }}'
