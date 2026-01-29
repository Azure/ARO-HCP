@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the Backend MSI')
param backendMsiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CosmosDB into which the Frontend will write data')
param rpCosmosDbName string

@description('The name of the Storage Account used by Backend to configure OIDC in ARO-HCP clusters')
param regionalOidcStorageAccountName string

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
//   B A C K E N D   L O O K U P
//

output tenantId string = tenant().tenantId

resource backendIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: backendMsiName
}

output backendMsiClientId string = backendIdentity.properties.clientId
output backendMsiTenantId string = backendIdentity.properties.tenantId

//
//   C O S M O S D B   L O O K U P
//

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: rpCosmosDbName
}

output cosmosDBDocumentEndpoint string = cosmosDbAccount.properties.documentEndpoint

//
//   S T O R A G E   A C C O U N T   L O O K U P
//

resource regionalOidcStorageAccount 'Microsoft.Storage/storageAccounts@2025-06-01' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: regionalOidcStorageAccountName
}

output regionalOidcStorageAccountBlobEndpoint string = regionalOidcStorageAccount.properties.primaryEndpoints.blob
output regionalOidcStorageAccountWebEndpoint string = regionalOidcStorageAccount.properties.primaryEndpoints.web
