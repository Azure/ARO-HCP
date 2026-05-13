@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the Kube Applier MSI')
param kubeApplierMsiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CosmosDB account')
param rpCosmosDbName string

//
//   I M A G E   P U L L E R   L O O K U P
//

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId
output imagePullerMsiTenantId string = imagePullerIdentity.properties.tenantId

//
//   K U B E   A P P L I E R   L O O K U P
//

resource kubeApplierIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: kubeApplierMsiName
}

output kubeApplierMsiClientId string = kubeApplierIdentity.properties.clientId
output kubeApplierMsiTenantId string = kubeApplierIdentity.properties.tenantId

//
//   C O S M O S D B   L O O K U P
//

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: rpCosmosDbName
}

output cosmosDBDocumentEndpoint string = cosmosDbAccount.properties.documentEndpoint
