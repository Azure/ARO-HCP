// This is used to grant CI the ability to deploy resources into
// dev AKS clusters. It should not be used in higher environments.
//
// This template is deployed to the service cluster resource group,
// which is where the AKS cluster lives. Cosmos DB role assignments
// live in the regional resource group, see
// dev-roleassignments-region.bicep.
param aksClusterName string
param sharedKvNames array = []
param sharedKvResourceGroup string = 'global'
param location string = resourceGroup().location
param principalID string

// https://learn.microsoft.com/en-us/azure/aks/manage-azure-rbac#create-role-assignments-for-users-to-access-the-cluster
// Azure Kubernetes Service RBAC Cluster Admin
// Allows super-user access to perform any action on any resource. It gives full control over every resource in the cluster and in all namespaces.
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

// A K S

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: guid(location, aksClusterName, aksClusterRbacClusterAdminRoleId, principalID)
  properties: {
    roleDefinitionId: aksClusterRbacClusterAdminRoleId
    principalId: principalID
  }
}

// K E Y V A U L T

module keyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for name in sharedKvNames: {
    name: guid(name, 'ghci', 'read', principalID)
    scope: resourceGroup(sharedKvResourceGroup)
    params: {
      keyVaultName: name
      roleName: 'Key Vault Secrets User'
      managedIdentityPrincipalIds: [principalID]
    }
  }
]
