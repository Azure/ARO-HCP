@description('Name of the management cluster.')
param mgmtClusterName string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: mgmtClusterName
}

output azureKeyvaultSecretsProviderIdentityClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId
output aksOutboundIPResourceID string = aksCluster.properties.networkProfile.loadBalancerProfile.outboundIPs.publicIPs[0].id
