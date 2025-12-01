@description('The name of the AdminAPI MSI')
param adminApiMsiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the AKS cluster in which the AdminAPI will run')
param aksClusterName string

@description('The name of the CosmosDB in which the AdminAPI will store data')
param cosmosDbName string

@description('The name of the resource group for regional infrastructure')
param regionalResourceGroup string

//
//   A D M I N   A P I   L O O K U P
//

resource adminApiIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: adminApiMsiName
}

output tenantId string = tenant().tenantId
output adminApiMsiClientId string = adminApiIdentity.properties.clientId

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId

//
//   C S I   S E C R E T   S T O R E   L O O K U P
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

output csiSecretStoreClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId

//
//   C O S M O S D B   L O O K U P
//

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: cosmosDbName
  scope: resourceGroup(regionalResourceGroup)
}

output cosmosDBDocumentEndpoint string = cosmosDbAccount.properties.documentEndpoint
