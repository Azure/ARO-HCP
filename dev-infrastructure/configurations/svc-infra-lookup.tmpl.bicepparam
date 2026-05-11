using '../templates/svc-infra-lookup.bicep'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultSubscription = '__serviceKeyVaultSubscription__'