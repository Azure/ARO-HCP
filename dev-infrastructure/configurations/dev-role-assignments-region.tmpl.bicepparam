using '../templates/dev-roleassignments-region.bicep'

param cosmosDBName = '{{ .frontend.cosmosDB.name }}'
param principalID = ''
