@description('The location for the resources')
param location string = resourceGroup().location

@description('The format string for the namespace')
param namespaceFormatString string

@description('The name of the user-assigned managed identity to create')
param clusterServiceManagedIdentityName string

@description('The name of the cluster to integrate with')
param clusterName string

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: clusterServiceManagedIdentityName
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-04-02-preview' existing = {
  name: clusterName
}

resource uami_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, 20): {
    parent: uami
    name: 'fedcred-${i}'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${format(namespaceFormatString, i)}:cluster-service'
    }
  }
]
