@description('List of private connection endpoints under storage')
param privateConnections array

@description('The name of the storage account')
param storageName string

@description('The message that was sent when a private link was created to storage')
param requestMessage string

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: storageName
}

var privateEndpoint = [
  for conn in privateConnections: {
    status: conn.properties.privateLinkServiceConnectionState.status
    name: last(split(conn.id, '/'))
    description: conn.properties.privateLinkServiceConnectionState.description
  }
]

resource privateConnection 'Microsoft.Storage/storageAccounts/privateEndpointConnections@2023-01-01' = [
  for endpoint in privateEndpoint: if (endpoint.status != 'Approved' && endpoint.description == requestMessage) {
    name: endpoint.name
    parent: storageAccount
    properties: {
      privateLinkServiceConnectionState: {
        status: 'Approved'
        description: 'Approved by OIDC pipeline'
        actionRequired: 'None'
      }
    }
  }
]
