@description('The name of the OCP ACR')
param ocpAcrName string

@description('The name of the SVC ACR')
param svcAcrName string

@description('The CX parent DNS zone name')
param cxParentZoneName string

@description('The SVC parent DNS zone name')
param svcParentZoneName string

@description('Metrics global Grafana name')
param grafanaName string

@description('The name of the global AFD instance')
param azureFrontDoorProfileName string

@description('The global MSI name')
param globalMSIName string

@description('The name of the global KV')
param globalKVName string

@description('The name of the geneva actions KV')
param genevaActionsKVName string

@description('Should geneva actions be enabled')
param genevaActionsEnabled bool

//
//   A C R
//

resource ocpAcr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: ocpAcrName
}

resource svcAcr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: svcAcrName
}

output ocpAcrResourceId string = ocpAcr.id
output ocpAcrLoginServer string = ocpAcr.properties.loginServer
output svcAcrResourceId string = svcAcr.id
output svcAcrLoginServer string = svcAcr.properties.loginServer

//
//   D N S
//

resource cxParentZone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: cxParentZoneName
}

resource svcParentZone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: svcParentZoneName
}

output cxParentZoneResourceId string = cxParentZone.id
output svcParentZoneResourceId string = svcParentZone.id

//
//   G R A F A N A
//

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaName
}

output grafanaResourceId string = grafana.id

//
//   A Z U R E   F R O N T   D O O R
//

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = if (!empty(azureFrontDoorProfileName)) {
  name: azureFrontDoorProfileName
}

output azureFrontDoorResourceId string = empty(azureFrontDoorProfileName) ? '' : frontDoorProfile.id

//
//  G L O B A L   M S I
//

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

output globalMSIId string = globalMSI.id

//
//   G L O B A L   KV
//

resource globalKV 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: globalKVName
}

output globalKeyVaultUrl string = globalKV.properties.vaultUri

//
//   G E N E V A   A C T I O N S   KV
//

resource genevaActionsKV 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = if (genevaActionsEnabled) {
  name: genevaActionsKVName
}

output genevaActionKeyVaultUrl string = genevaActionsEnabled ? genevaActionsKV.properties.vaultUri : ''
