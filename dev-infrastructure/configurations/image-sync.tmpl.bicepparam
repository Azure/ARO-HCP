using '../templates/image-sync.bicep'

param acrResourceGroup = 'global'
param keyVaultName = '{{ .serviceKeyVaultName }}'

param requiredSecretNames = [
  'pull-secret'
  'bearer-secret'
]
