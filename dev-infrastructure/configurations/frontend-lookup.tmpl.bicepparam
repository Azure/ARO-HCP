using '../templates/frontend-lookup.bicep'

param frontendMsiName = '{{ .frontend.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param aksClusterName = '{{ .svc.aks.name }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param regionalResourceGroup = '{{ .regionRG }}'