using '../templates/backend-lookup.bicep'

param backendMsiName = '{{ .backend.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param regionalResourceGroup = '{{ .regionRG }}'