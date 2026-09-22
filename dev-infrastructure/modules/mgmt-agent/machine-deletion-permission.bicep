param aksClusterName string
param principalId string
param roleDefinitionId string

resource aks 'Microsoft.ContainerService/managedClusters@2025-10-01' existing = {
  name: aksClusterName
}

resource assignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aks.id, principalId, roleDefinitionId)
  scope: aks
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
  }
}
