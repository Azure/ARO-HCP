targetScope = 'subscription'

@description('The principal ID of the mgmt-agent managed identity')
param mgmtAgentPrincipalId string

@description('Resource ID of the deployment identity that revokes disabled mitigation assignments')
param deploymentMsiId string

param aksClusterName string
param aksResourceGroupName string
param nodeMitigationEnabled bool = false

resource machineDeletionRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = if (nodeMitigationEnabled) {
  name: guid(subscription().id, 'aro-hcp-mgmt-agent-machine-deletion')
  properties: {
    roleName: 'ARO HCP mgmt-agent machine deletion ${subscription().subscriptionId}'
    description: 'Delete specific machines through the AKS agent-pool API.'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.ContainerService/managedClusters/agentPools/deleteMachines/action'
        ]
        notActions: []
        dataActions: []
        notDataActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}

resource permissionCleanupRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, 'aro-hcp-mgmt-agent-permission-cleanup')
  properties: {
    roleName: 'ARO HCP mgmt-agent permission cleanup ${subscription().subscriptionId}'
    description: 'Read and remove role assignments, restricted by the assignment condition.'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.Authorization/roleAssignments/read'
          'Microsoft.Authorization/roleAssignments/delete'
        ]
        notActions: []
        dataActions: []
        notDataActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}

module machineDeletionAssignment '../modules/mgmt-agent/machine-deletion-permission.bicep' = {
  name: 'mgmt-agent-machine-deletion-${uniqueString(aksClusterName)}'
  scope: resourceGroup(aksResourceGroupName)
  params: {
    enabled: nodeMitigationEnabled
    aksClusterName: aksClusterName
    principalId: mgmtAgentPrincipalId
    cleanupPrincipalId: reference(deploymentMsiId, '2023-01-31').principalId
    cleanupRoleDefinitionId: permissionCleanupRole.id
    roleDefinitionId: nodeMitigationEnabled
      ? machineDeletionRole.id
      : subscriptionResourceId('Microsoft.Authorization/roleDefinitions', guid(subscription().id, 'aro-hcp-mgmt-agent-machine-deletion'))
  }
}

output machineDeletionAssignmentId string = machineDeletionAssignment.outputs.assignmentId
output machineDeletionRoleId string = machineDeletionAssignment.outputs.roleId
output machineDeletionClusterId string = machineDeletionAssignment.outputs.clusterId
output mitigationEnabled string = nodeMitigationEnabled ? 'true' : 'false'

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
