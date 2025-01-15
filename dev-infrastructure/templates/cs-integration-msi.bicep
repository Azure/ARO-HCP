@description('The format string for the namespace')
param namespaceFormatString string

@description('The name of the user-assigned managed identity to create')
param clusterServiceManagedIdentityName string

@description('The name of the cluster to integrate with')
param clusterName string

@description('The name of the CS service account')
param clusterServiceServiceAccountName string

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: clusterServiceManagedIdentityName
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-04-02-preview' existing = {
  name: clusterName
}

@batchSize(1)
resource uami_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, 19): {
    parent: uami
    name: 'fedcred-${i}'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${format(namespaceFormatString, i)}:${clusterServiceServiceAccountName}'
    }
  }
]
