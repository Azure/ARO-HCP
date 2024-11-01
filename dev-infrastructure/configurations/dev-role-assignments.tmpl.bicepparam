using '../templates/dev-roleassignments.bicep'

param aksClusterName = '{{ .aksName }}'
param grantCosmosAccess = true
param cosmosDBName = '{{ .frontendCosmosDBName }}'
param sharedKvNames = ['{{ .serviceKeyVaultName }}']
param sharedKvResourceGroup = '{{ .serviceKeyVaultRG }}'
param principalID = ''
