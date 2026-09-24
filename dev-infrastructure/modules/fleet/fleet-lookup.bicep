import { monitoringWorkspaceRefFromId } from '../resource.bicep'

@description('The name of the fleet managed identity')
param msiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CosmosDB account')
param rpCosmosDbName string

@description('The name of the CX DNS zone (e.g. usw3gobe.hcp.osadev.cloud)')
param cxDnsZoneName string

@description('The name of the SVC Azure Monitor Workspace')
param svcMonitorName string

@description('The name of the HCP Azure Monitor Workspace')
param hcpMonitorName string

@description('Optional existing SVC Azure Monitor Workspace resource ID; defaults to the regional workspace')
param svcWorkspaceResourceId string = ''

@description('Optional existing HCP Azure Monitor Workspace resource ID; defaults to the regional workspace')
param hcpWorkspaceResourceId string = ''

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

//
//   A Z U R E   M O N I T O R   W O R K S P A C E   L O O K U P
//

var svcMonitorRef = monitoringWorkspaceRefFromId(
  empty(svcWorkspaceResourceId) ? resourceId(regionalResourceGroup, 'Microsoft.Monitor/accounts', svcMonitorName) : svcWorkspaceResourceId
)
var hcpMonitorRef = monitoringWorkspaceRefFromId(
  empty(hcpWorkspaceResourceId) ? resourceId(regionalResourceGroup, 'Microsoft.Monitor/accounts', hcpMonitorName) : hcpWorkspaceResourceId
)

resource svcMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  scope: resourceGroup(svcMonitorRef.resourceGroup.subscriptionId, svcMonitorRef.resourceGroup.name)
  name: svcMonitorRef.name
}

resource hcpMonitor 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  scope: resourceGroup(hcpMonitorRef.resourceGroup.subscriptionId, hcpMonitorRef.resourceGroup.name)
  name: hcpMonitorRef.name
}

output svcAzureMonitorWorkspaceId string = svcMonitor.id
output hcpAzureMonitorWorkspaceId string = hcpMonitor.id
