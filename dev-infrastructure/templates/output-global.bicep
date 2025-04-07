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
