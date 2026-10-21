@description('The name of the AKS cluster')
param aksClusterName string

@description('Session Gate MI resource ID, used to grant AKS access')
param sessiongateMIResourceId string

@description('Fleet MI resource ID, used to grant AKS read access for node pool scaling')
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
//   F L E E T   A K S   R E A D   A C C E S S
//

// Reader role — scoped to the MC AKS cluster so the fleet controller's
// node-pool-scaling controller can list agent pools and read SKU metadata.
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

var fleetMIRef = res.msiRefFromId(fleetMIResourceId)
resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(fleetMIRef.resourceGroup.subscriptionId, fleetMIRef.resourceGroup.name)
  name: fleetMIRef.name
}

resource fleetAksReaderAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(resourceGroup().id, aksClusterName, fleetMIResourceId, readerRoleId)
  properties: {
    roleDefinitionId: readerRoleId
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
