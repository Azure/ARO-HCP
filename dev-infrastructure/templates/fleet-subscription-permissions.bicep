targetScope = 'subscription'

@description('The full resource ID of the Fleet managed identity')
param fleetMIResourceId string

import * as res from '../modules/resource.bicep'

// The fleet node-pool-scaling controller calls the Azure Resource SKUs API at
// subscription scope to look up VM size capabilities (vCPU, memory). That API
// requires at least Reader on the subscription; the AKS-cluster-scoped Reader
// granted in svc-mgmt-aks-permissions.bicep does not cover it.
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

var fleetMIRef = res.msiRefFromId(fleetMIResourceId)
resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(fleetMIRef.resourceGroup.subscriptionId, fleetMIRef.resourceGroup.name)
  name: fleetMIRef.name
}

resource fleetSubscriptionReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, fleetMSI.id, readerRoleId)
  scope: subscription()
  properties: {
    principalId: fleetMSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}
