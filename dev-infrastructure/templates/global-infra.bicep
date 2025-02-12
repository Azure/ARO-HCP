@description('The global msi name')
param globalMSIName string

@description('The cxParentZone Domain')
param cxParentZoneName string

@description('The svcParentZone Domain')
param svcParentZoneName string

@description('Metrics global Grafana name')
param grafanaName string

@description('The admin group principal ID to manage Grafana')
param grafanaAdminGroupPrincipalId string

@description('Domain Team MSI to delegate child DNS')
param safeDnsIntAppObjectId string

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
var dnsZoneContributor = 'befefa01-2a29-4197-83a8-272ff33ce314'

// Azure Managed Grafana Workspace Contributor: Can manage Azure Managed Grafana resources, without providing access to the workspaces themselves.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#azure-managed-grafana-workspace-contributor
var grafanaContributor = '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'

// Grafana Admin: Perform all Grafana operations, including the ability to manage data sources, create dashboards, and manage role assignments within Grafana.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#grafana-admin
var grafanaAdminRole = '22926164-76b3-42b3-bc55-97df8dab3e41'

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, ev2MSI.id, readerRoleId)
  properties: {
    principalId: ev2MSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource cxParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(cxParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: cxParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', dnsZoneContributor)
  }
}

resource svcParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(svcParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: svcParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', dnsZoneContributor)
  }
}

var grafanaAdminGroup = {
  principalId: grafanaAdminGroupPrincipalId
  principalType: 'group'
}

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' = {
  name: grafanaName
  location: resourceGroup().location
  sku: {
    name: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
}

resource contributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, ev2MSI.id, grafanaContributor)
  scope: grafana
  properties: {
    principalId: ev2MSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaContributor)
  }
}

resource adminRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, grafanaAdminGroup.principalId, grafanaAdminRole)
  scope: grafana
  properties: {
    principalId: grafanaAdminGroup.principalId
    principalType: grafanaAdminGroup.principalType
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaAdminRole)
  }
}

output grafanaId string = grafana.id
