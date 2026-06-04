@description('The name of the AKS cluster')
param aksClusterName string

@description('Session Gate MI resource ID, used to grant AKS access')
param sessiongateMIResourceId string

@description('FPA service principal object ID, used to grant AKS access for Holmes investigation')
param fpaObjectId string

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

// Azure Kubernetes Service Cluster User Role
// Required for listClusterUserCredential API (to get server URL and CA cert)
// https://www.azadvertizer.net/azrolesadvertizer/4abbcc35-e782-43d8-92c5-2d3f1bd2253f.html
var aksClusterUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4abbcc35-e782-43d8-92c5-2d3f1bd2253f'
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
//   F P A   A K S   A C C E S S   ( H O L M E S   I N V E S T I G A T I O N )
//
// The admin API uses the FPA service principal (not its own MSI) to access
// management clusters via mc.GetAKSRESTConfig(). It needs:
// 1. Cluster User Role — to call listClusterUserCredential API
// 2. RBAC Cluster Admin — to create pods, secrets, CSRs inside the cluster
//

resource fpaAksUserAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(resourceGroup().id, aksClusterName, fpaObjectId, aksClusterUserRoleId)
  properties: {
    roleDefinitionId: aksClusterUserRoleId
    principalId: fpaObjectId
    principalType: 'ServicePrincipal'
  }
}

resource fpaAksRbacAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(resourceGroup().id, aksClusterName, fpaObjectId, aksClusterRBACAdminRoleId)
  properties: {
    roleDefinitionId: aksClusterRBACAdminRoleId
    principalId: fpaObjectId
    principalType: 'ServicePrincipal'
  }
}
