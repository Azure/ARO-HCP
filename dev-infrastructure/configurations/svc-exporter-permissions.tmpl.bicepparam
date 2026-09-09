using '../templates/svc-exporter-permissions.bicep'

param exporterPrincipalId = '__exporterPrincipalId__'
param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultSubscription = '__serviceKeyVaultSubscription__'
