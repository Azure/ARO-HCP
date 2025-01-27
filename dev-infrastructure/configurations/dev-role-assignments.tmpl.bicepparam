using '../templates/dev-roleassignments.bicep'

param aksClusterName = '{{ .svc.aks.name }}'
param grantCosmosAccess = true
param cosmosDBName = '{{ .frontend.cosmosDB.name }}'
param sharedKvNames = ['{{ .serviceKeyVault.name }}']
param sharedKvResourceGroup = '{{ .serviceKeyVault.rg }}'
param principalID = ''
