using '../modules/adminapi/adminapi-lookup.bicep'

param adminApiMsiName = '{{ .adminApi.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param aksClusterName = '{{ .svc.aks.name }}'
param cosmosDbName = '{{ .frontend.cosmosDB.name }}'
param regionalResourceGroup = '{{ .regionRG }}'
