@description('The name of the SessionGate MSI')
param sessiongateMsiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the AKS cluster in which the SessionGate will run')
param aksClusterName string

//
//   S E S S I O N   G A T E   L O O K U P
//

resource sessiongateIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: sessiongateMsiName
}

output tenantId string = tenant().tenantId
output sessiongateMsiClientId string = sessiongateIdentity.properties.clientId

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId

//
//   C S I   S E C R E T   S T O R E   L O O K U P
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

output csiSecretStoreClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId
