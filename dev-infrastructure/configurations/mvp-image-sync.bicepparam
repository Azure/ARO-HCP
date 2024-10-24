using '../templates/image-sync.bicep'

param acrResourceGroup = 'global'

param keyVaultName = 'aro-hcp-dev-global-kv'

param requiredSecretNames = [
  'component-sync-pull-secret'
  'bearer-secret'
]
