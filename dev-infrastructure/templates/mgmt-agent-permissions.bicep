targetScope = 'subscription'

@description('The principal ID of the mgmt-agent managed identity')
param mgmtAgentPrincipalId string

// The mgmt-agent controller needs to read VMSS VM resources to determine the
// number of SWIFT NICs available on each node. AKS places node VMs in a
// managed resource group (MC_*) whose name is not predictable at deployment
// time, so we cannot scope this role assignment to a specific resource group.
// A subscription-scoped Reader role is the narrowest scope that reliably
// covers all possible node resource groups. Reader is read-only, so the
// blast radius is minimal.
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource mgmtAgentReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, mgmtAgentPrincipalId, readerRoleId)
  scope: subscription()
  properties: {
    principalId: mgmtAgentPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}
