using '../templates/image-sync.bicep'

param acrResourceGroup = 'gobal'

param keyVaultName = 'aro-hcp-dev-global-kv'

param requiredSecretNames = [
  'pull-secret'
  'bearer-secret'
]
