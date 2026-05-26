@description('The name of the fleet managed identity')
param msiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CosmosDB account')
param rpCosmosDbName string

@description('The name of the CX DNS zone (e.g. usw3gobe.hcp.osadev.cloud)')
param cxDnsZoneName string

//
//   F L E E T   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId

//
//   C O S M O S D B   L O O K U P
//

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: rpCosmosDbName
}

output cosmosDBDocumentEndpoint string = cosmosDbAccount.properties.documentEndpoint

//
//   D N S   Z O N E   L O O K U P
//

resource cxDnsZone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: cxDnsZoneName
}

output cxDnsZoneResourceId string = cxDnsZone.id
