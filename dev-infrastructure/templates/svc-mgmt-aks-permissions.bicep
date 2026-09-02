@description('The name of the AKS cluster')
param aksClusterName string

@description('The name of the VNet containing AKS subnets')
param vnetName string

@description('Session Gate MI resource ID, used to grant AKS access')
param sessiongateMIResourceId string

@description('Fleet MI resource ID, used to grant AKS access for worker node pool management')
param fleetMIResourceId string

import * as res from '../modules/resource.bicep'

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

//
//   S E S S I O N   G A T E   A K S   A C C E S S
//

// Azure Kubernetes Service RBAC Cluster Admin Role
// https://www.azadvertizer.net/azrolesadvertizer/b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b.html
var aksClusterRBACAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

var sessiongateMIRef = res.msiRefFromId(sessiongateMIResourceId)
resource sessiongateMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(sessiongateMIRef.resourceGroup.subscriptionId, sessiongateMIRef.resourceGroup.name)
  name: sessiongateMIRef.name
}

resource sessiongateAksAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(resourceGroup().id, aksClusterName, sessiongateMIResourceId, aksClusterRBACAdminRoleId)
  properties: {
    roleDefinitionId: aksClusterRBACAdminRoleId
    principalId: sessiongateMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

//
//   F L E E T   A K S   A C C E S S
//

var fleetMIRef = res.msiRefFromId(fleetMIResourceId)
resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(fleetMIRef.resourceGroup.subscriptionId, fleetMIRef.resourceGroup.name)
  name: fleetMIRef.name
}

// Azure Kubernetes Service Contributor — scoped to the MC AKS cluster so the
// fleet controller can create, update, and delete worker agent pools.
// https://www.azadvertizer.net/azrolesadvertizer/ed7f3fbd-7b88-4dd4-9017-9adb7ce333f8.html
var aksContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'ed7f3fbd-7b88-4dd4-9017-9adb7ce333f8'
)

resource fleetAksContributorAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(resourceGroup().id, aksClusterName, fleetMIResourceId, aksContributorRoleId)
  properties: {
    roleDefinitionId: aksContributorRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Network Contributor — scoped to the MC VNet so the fleet controller can
// create agent pools that join subnets (required by AKS for subnet/join/action).
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
}

resource fleetVnetNetworkContributorAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(resourceGroup().id, vnetName, fleetMIResourceId, networkContributorRoleId)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
