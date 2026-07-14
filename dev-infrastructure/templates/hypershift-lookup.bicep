@description('The name of the AKS cluster where HyperShift will be deployed')
param aksClusterName string

@description('The name of the etcd backup managed identity')
param etcdBackupMsiName string

//
//   H Y P E R S H I F T   L O O K U P
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-02-01' existing = {
  name: aksClusterName
}

output csiSecretStoreClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId

resource etcdBackupIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: etcdBackupMsiName
}

output etcdBackupMsiClientId string = etcdBackupIdentity.properties.clientId
