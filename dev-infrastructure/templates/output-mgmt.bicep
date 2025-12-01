@description('Name of the management cluster.')
param mgmtClusterName string

@description('Name of the backup storage account.')
param backupsStorageAccountName string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: mgmtClusterName
}
output azureKeyvaultSecretsProviderIdentityClientId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.clientId

// Why not retreive the account name from config/config.yaml?
// Because the config could contain account name with an upper case (regionShortName), storage accounts must be lower case.
resource hcpBackupsStorageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: backupsStorageAccountName
}

output hcpBackupsStorageAccountName string = hcpBackupsStorageAccount.name
