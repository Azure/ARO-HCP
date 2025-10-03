@description('Name of the management cluster.')
param mgmtClusterName string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

//
//   M G M T   C L U S T E R
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: mgmtClusterName
}
output azureKeyvaultSecretsProviderIdentityClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId

output aksOutboundIPResourceID string = aksCluster.properties.networkProfile.loadBalancerProfile.outboundIPs.publicIPs[0].id

//
//   K E Y V A U L T S
//

resource cxKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: cxKeyVaultName
}
resource msiKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: msiKeyVaultName
}

output cxKeyVaultUrl string = cxKeyVault.properties.vaultUri
output msiKeyVaultUrl string = msiKeyVault.properties.vaultUri
