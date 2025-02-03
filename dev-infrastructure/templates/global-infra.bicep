@description('The global msi name')
param globalMSIName string

@description('The cxParentZone Domain')
param cxParentZoneName string

@description('The svcParentZone Domain')
param svcParentZoneName string

@description('Metrics global Grafana name')
param grafanaName string

@description('Metrics global MSI name')
param msiName string

@description('The admin group principal ID to manage Grafana')
param grafanaAdminGroupPrincipalId string

@description('MSI that will be used during pipeline runs to Azure resources')
param aroDevopsMsiId string

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: globalMSIName
  location: resourceGroup().location
}

resource cxParentZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: cxParentZoneName
  location: 'global'
}

resource svcParentZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: svcParentZoneName
  location: 'global'
}

// DNS Zone Contributor: Lets SafeDnsIntApplication manage DNS zones and record sets in Azure DNS, but does not let it control who has access to them.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/networking#dns-zone-contributor
var dnsZoneContributor = subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', 'befefa01-2a29-4197-83a8-272ff33ce314')

//  SafeDnsIntApplication object ID use to delegate child DNS
var safeDnsIntAppObjectId = 'c54b6bce-1cd3-4d37-bebe-aa22f4ce4fbc'

resource cxParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(cxParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: cxParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: dnsZoneContributor
  }
}

resource svcParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(svcParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: svcParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: dnsZoneContributor
  }
}

var grafanaAdmin = {
  principalId: grafanaAdminGroupPrincipalId
  principalType: 'group'
}

module grafana 'br:arointacr.azurecr.io/grafana.bicep:metrics.20240814.1' = {
  name: 'grafana'
  params: {
    msiName: msiName
    grafanaName: grafanaName
    grafanaAdmin: grafanaAdmin
  }
}

resource grafanaInstance 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaName
}

// https://www.azadvertizer.net/azrolesadvertizer/a79a5197-3a5c-4973-a920-486035ffd60f.html
var grafanaEditorRole = 'a79a5197-3a5c-4973-a920-486035ffd60f'

resource grafanaDevopsAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafanaInstance.id, aroDevopsMsiId, grafanaEditorRole)
  scope: grafanaInstance
  properties: {
    principalId: reference(aroDevopsMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', grafanaEditorRole)
  }
}

output grafanaId string = grafana.outputs.grafanaId
