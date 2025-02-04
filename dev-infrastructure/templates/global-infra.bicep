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

@description('SafeDnsIntApplication object ID use to delegate child DNS')
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
var dnsZoneContributor = subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', 'befefa01-2a29-4197-83a8-272ff33ce314')

// Azure Managed Grafana Workspace Contributor: Can manage Azure Managed Grafana resources, without providing access to the workspaces themselves.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#azure-managed-grafana-workspace-contributor
var contributor = '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'

// Grafana Admin: Perform all Grafana operations, including the ability to manage data sources, create dashboards, and manage role assignments within Grafana.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#grafana-admin
var admin = '22926164-76b3-42b3-bc55-97df8dab3e41'

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

resource grafanaInstance 'Microsoft.Dashboard/grafana@2023-09-01' = {
  name: grafanaName
  location: resourceGroup().location
  sku: {
    name: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
}

module grafana 'br:arointacr.azurecr.io/grafana.bicep:metrics.20240814.1' = {
  name: 'grafana'
  params: {
    msiName: msiName
    grafanaName: grafanaName
    grafanaAdmin: grafanaAdmin
  }
}

resource contributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafanaInstance.id, aroDevopsMsiId, contributor)
  scope: grafanaInstance
  properties: {
    principalId: reference(aroDevopsMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', contributor)
  }
}

resource adminRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafanaInstance.id, grafanaAdmin.principalId, admin)
  scope: grafanaInstance
  properties: {
    principalId: grafanaAdmin.principalId
    principalType: grafanaAdmin.principalType
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', admin)
  }
}

output grafanaId string = grafana.outputs.grafanaId
