import {
  csvToArray
} from '../../../modules/common.bicep'

@description('Name of the Kusto cluster to create')
param kustoName string

@description('The SKU of the cluster')
param sku string = 'Standard_D12_v2'

@description('Tier used')
param tier string = 'Basic'

@description('CSV seperated list of groups to assign admin in the Kusto cluster, format: "(tenantId/)groupId", if tenantId is not provided, the current tenant will be used')
param adminGroups string

@description('CSV seperated list of groups to assign viewer in the Kusto cluster, format: "(tenantId/)groupId", if tenantId is not provided, the current tenant will be used')
param viewerGroups string

@description('Minimum number of nodes for autoscale')
param autoScaleMin int

@description('Maximum number of nodes for autoscale')
param autoScaleMax int

@description('Toggle if autoscale should be enabled')
param enableAutoScale bool

type Permission = {
  tenantId: string
  groupId: string
}

var adminPermissions = [
  for group in csvToArray(adminGroups): {
    tenantId: contains(group, '/') ? split(group, '/')[0] : tenant().tenantId
    groupId: contains(group, '/') ? split(group, '/')[1] : group
  }
]

var viewerPermissions = [
  for group in csvToArray(viewerGroups): {
    tenantId: contains(group, '/') ? split(group, '/')[0] : tenant().tenantId
    groupId: contains(group, '/') ? split(group, '/')[1] : group
  }
]

// Core Kusto cluster (no databases here; those are in separate modules)
resource kusto 'Microsoft.Kusto/clusters@2024-04-13' = {
  name: kustoName
  location: resourceGroup().location
  sku: {
    name: sku
    tier: tier
  }
  identity: {
    type: 'SystemAssigned'
  }

  properties: {
    optimizedAutoscale: {
      version: 1
      isEnabled: enableAutoScale
      minimum: autoScaleMin
      maximum: autoScaleMax
    }
    enableAutoStop: false
  }

  // Cluster level permissions
  resource clusterAdminPermissionsForGroups 'principalAssignments' = [
    for permission in adminPermissions: {
      name: 'admin-group-${permission.groupId}'
      properties: {
        principalId: permission.groupId
        principalType: 'Group'
        role: 'AllDatabasesAdmin'
        tenantId: permission.tenantId
      }
    }
  ]

  resource clusterViewPermissionsForGroups 'principalAssignments' = [
    for permission in viewerPermissions: {
      name: 'viewer-group-${permission.groupId}'
      properties: {
        principalId: permission.groupId
        principalType: 'Group'
        role: 'AllDatabasesViewer'
        tenantId: permission.tenantId
      }
    }
  ]
}

output id string = kusto.id
output name string = kusto.name
