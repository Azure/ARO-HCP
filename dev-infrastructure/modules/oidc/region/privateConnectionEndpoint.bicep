@description('The name of the storage account')
param storageName string

@description('The message that was sent when a private link was created to storage')
param requestMessage string

// Need to read the storage resource once the private link is enabled by Orign under AFD
resource storage 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: storageName
}

module approveStorageEndpoint 'approvePrivateConnectionEndpoint.bicep' = {
  name: 'approve-storage-endpoint'
  params: {
    privateConnections: storage.properties.privateEndpointConnections
    storageName: storageName
    requestMessage: requestMessage
  }
  dependsOn: [
    storage
  ]
}
