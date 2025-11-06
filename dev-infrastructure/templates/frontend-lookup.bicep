@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the Frontend MSI')
param frontendMsiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CosmosDB into which the Frontend will write data')
param rpCosmosDbName string

@description('The name of the AKS cluster in which the Frontend will run')
param aksClusterName string

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
//   F R O N T E N D   L O O K U P
//

resource frontendIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: frontendMsiName
}

output frontendMsiClientId string = frontendIdentity.properties.clientId
output frontendMsiTenantId string = frontendIdentity.properties.tenantId

//
//   C S I   S E C R E T   S T O R E   L O O K U P
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

output csiSecretStoreClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId

output tenantId string = tenant().tenantId

//
//   C O S M O S D B   L O O K U P
//

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: rpCosmosDbName
}

output cosmosDBDocumentEndpoint string = cosmosDbAccount.properties.documentEndpoint
