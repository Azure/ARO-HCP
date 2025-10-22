@description('Name of the management cluster.')
param mgmtClusterName string

@description('Name of the backup storage account.')
param backupsStorageAccountName string
@description('The name of the Velero managed identity')
param veleroMsiName string

@description('The name of the OADP controller manager managed identity')
param oadpControllerMsiName string

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

//
//   O A D P   W O R K L O A D   I D E N T I T I E S
//

resource veleroIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: veleroMsiName
}

output veleroMsiClientId string = veleroIdentity.properties.clientId

resource oadpControllerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: oadpControllerMsiName
}

output oadpControllerMsiClientId string = oadpControllerIdentity.properties.clientId

output tenantId string = tenant().tenantId
output subscriptionId string = subscription().subscriptionId
