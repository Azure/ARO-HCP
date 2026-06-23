targetScope = 'subscription'

@description('Pipeline MSI resource ID, used to grant read-only access to AKS node resource groups')
param globalMSIId string

import * as res from '../modules/resource.bicep'

// The recreate-system-pool-if-broken and recreate-broken-user-nodepools Shell steps run as the global pipeline MSI and
// need to query activity logs on AKS-created node resource groups. AKS node RG
// names are not predictable at deployment time, so we cannot scope this to a
// specific resource group. Subscription Reader is the narrowest reliable
// read-only scope, matching the mgmt-agent pattern.
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

var globalMSIRef = res.msiRefFromId(globalMSIId)
resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(globalMSIRef.resourceGroup.subscriptionId, globalMSIRef.resourceGroup.name)
  name: globalMSIRef.name
}

resource pipelineMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, globalMSIId, readerRoleId)
  scope: subscription()
  properties: {
    principalId: globalMSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}
