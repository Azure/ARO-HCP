using '../templates/dev-roleassignments-region.bicep'

param grantCosmosAccess = true
param cosmosDBName = '{{ .frontend.cosmosDB.name }}'
param principalID = ''
