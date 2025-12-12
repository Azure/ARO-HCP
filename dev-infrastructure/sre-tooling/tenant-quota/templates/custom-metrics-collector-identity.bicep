@description('Managed Identity name for custom metrics collector')
param identityName string = 'custom-metrics-collector'

@description('AKS cluster name')
param aksClusterName string

@description('Kubernetes namespace for the service account')
param namespace string = 'tenant-quota'

@description('Kubernetes service account name')
param serviceAccountName string = 'custom-metrics-collector'

resource customMetricsCollectorUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' = {
  name: identityName
  location: resourceGroup().location
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: aksClusterName
}

resource federatedCredential 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2024-11-30' = {
  parent: customMetricsCollectorUAMI
  name: '${identityName}-fedcred'
  properties: {
    audiences: [
      'api://AzureADTokenExchange'
    ]
    issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
    subject: 'system:serviceaccount:${namespace}:${serviceAccountName}'
  }
}

output customMetricsCollectorUAMIClientId string = customMetricsCollectorUAMI.properties.clientId
output customMetricsCollectorUAMIResourceId string = customMetricsCollectorUAMI.id
output customMetricsCollectorUAMIPrincipalId string = customMetricsCollectorUAMI.properties.principalId

