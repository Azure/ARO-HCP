// This is used to grant CI the ability to deploy resources into
// dev AKS clusters. It should not be used in higher environments.
param aksClusterName string
param grantCosmosAccess bool = false
param cosmosDBName string = 'replaceme'
param kvNames array = []
param location string = resourceGroup().location
param githubActionsPrincipalID string

// https://learn.microsoft.com/en-us/azure/aks/manage-azure-rbac#create-role-assignments-for-users-to-access-the-cluster
// Azure Kubernetes Service RBAC Cluster Admin
// Allows super-user access to perform any action on any resource. It gives full control over every resource in the cluster and in all namespaces.
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

// Grants Github Actions access to Cosmos data
param cosmosRoleDefinitionId string = '00000000-0000-0000-0000-000000000002'
var cosmosRoleAssignmentId = guid(cosmosRoleDefinitionId, githubActionsPrincipalID, cosmosDbAccount.id)

// C O S M O S

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = if (grantCosmosAccess) {
  name: cosmosDBName
}

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(location, aksClusterName, aksClusterRbacClusterAdminRoleId, githubActionsPrincipalID)
  properties: {
    roleDefinitionId: aksClusterRbacClusterAdminRoleId
    principalId: githubActionsPrincipalID
  }
}

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = if (grantCosmosAccess) {
  name: cosmosRoleAssignmentId
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '/${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.DocumentDB/databaseAccounts/${cosmosDbAccount.name}/sqlRoleDefinitions/${cosmosRoleDefinitionId}'
    principalId: githubActionsPrincipalID
    scope: cosmosDbAccount.id
  }
}

// K E Y V A U L T

module keyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for name in kvNames: {
    name: guid(name, 'ghci', 'read')
    params: {
      keyVaultName: name
      roleName: 'Key Vault Secrets User'
      managedIdentityPrincipalId: githubActionsPrincipalID
    }
  }
]
