using '../templates/kube-applier-lookup.bicep'

param imagePullerMsiName = 'image-puller'
param kubeApplierMsiName = '{{ .kubeApplier.managedIdentityName }}'
param regionalResourceGroup = '{{ .regionRG }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
