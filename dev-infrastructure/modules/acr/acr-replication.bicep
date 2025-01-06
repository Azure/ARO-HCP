@description('ACR replication resource location')
param acrReplicationLocation string

@description('Parent ACR resource name')
param acrReplicationParentAcrName string

@minLength(5)
@maxLength(40)
@description('ACR replication name (must be globally unique)')
param acrReplicationReplicaName string

resource parentAcr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrReplicationParentAcrName
}

resource acrReplication 'Microsoft.ContainerRegistry/registries/replications@2023-11-01-preview' = {
  parent: parentAcr
  name: acrReplicationReplicaName
  location: acrReplicationLocation
  properties: {
    regionEndpointEnabled: true
  }
}
