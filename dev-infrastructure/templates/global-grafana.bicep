import { getLocationAvailabilityZonesCSV, determineZoneRedundancy, csvToArray } from '../modules/common.bicep'

@description('Azure Global Location')
param location string = resourceGroup().location

@description('The global msi name')
param globalMSIName string

@description('Metrics global Grafana name')
param grafanaName string

@description('The admin group principal ID to manage Grafana')
param grafanaAdminGroupPrincipalId string

@description('The zone redundant mode of Grafana')
param grafanaZoneRedundantMode string

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

// Azure Managed Grafana Workspace Contributor: Can manage Azure Managed Grafana resources, without providing access to the workspaces themselves.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#azure-managed-grafana-workspace-contributor
var grafanaContributor = '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'

// Grafana Admin: Perform all Grafana operations, including the ability to manage data sources, create dashboards, and manage role assignments within Grafana.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#grafana-admin
var grafanaAdminRole = '22926164-76b3-42b3-bc55-97df8dab3e41'

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
  properties: {
    zoneRedundancy: determineZoneRedundancy(locationAvailabilityZoneList, grafanaZoneRedundantMode)
      ? 'Enabled'
      : 'Disabled'
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
