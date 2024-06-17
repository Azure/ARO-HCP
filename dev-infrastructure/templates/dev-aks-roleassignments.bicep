// This is used to grant CI the ability to deploy resources into
// dev AKS clusters. It should not be used in higher environments.
param aksClusterName string
param location string = resourceGroup().location
param githubActionsPrincipalID string

// https://learn.microsoft.com/en-us/azure/aks/manage-azure-rbac#create-role-assignments-for-users-to-access-the-cluster
// Azure Kubernetes Service RBAC Cluster Admin
// Allows super-user access to perform any action on any resource. It gives full control over every resource in the cluster and in all namespaces.
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
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
