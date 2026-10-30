@description('AKS cluster name')
param aksClusterName string

@description('Configured Istio revisions to bootstrap a new service cluster.')
param configuredVersions string

// Existing clusters retain their live revisions, including an in-progress canary.
@nullIfNotFound()
resource existingSvcCluster 'Microsoft.ContainerService/managedClusters@2026-04-02-preview' existing = {
  name: aksClusterName
}

output istioVersions string = existingSvcCluster == null ? configuredVersions : ''
