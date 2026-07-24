using '../templates/dev-cs-pr-auth-app.bicep'

param aksClusterName = '{{ .svc.aks.name }}'
param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
