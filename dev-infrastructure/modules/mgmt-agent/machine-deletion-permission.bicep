param aksClusterName string
param principalId string
param roleDefinitionId string
param enabled bool = false
@description('Deployment identity permitted to revoke the mitigation assignment')
param cleanupPrincipalId string
@description('Role with only roleAssignments/read and roleAssignments/delete')
param cleanupRoleDefinitionId string

resource aks 'Microsoft.ContainerService/managedClusters@2025-10-01' existing = {
  name: aksClusterName
}

resource assignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enabled) {
  name: guid(aks.id, principalId, roleDefinitionId)
  scope: aks
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
  }
}

var machineDeletionRoleGuid = last(split(roleDefinitionId, '/'))

resource cleanupAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aks.id, cleanupPrincipalId, cleanupRoleDefinitionId)
  scope: aks
  properties: {
    principalId: cleanupPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: cleanupRoleDefinitionId
    conditionVersion: '2.0'
    condition: '(!(ActionMatches{\'Microsoft.Authorization/roleAssignments/delete\'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {${machineDeletionRoleGuid}} AND @Resource[Microsoft.Authorization/roleAssignments:PrincipalId] ForAnyOfAnyValues:GuidEquals {${principalId}})'
    description: 'Deployment identity may revoke only this mgmt-agent principal and machine-deletion role.'
  }
}

output assignmentId string = extensionResourceId(
  aks.id,
  'Microsoft.Authorization/roleAssignments',
  guid(aks.id, principalId, roleDefinitionId)
)
output roleId string = roleDefinitionId
output clusterId string = aks.id
