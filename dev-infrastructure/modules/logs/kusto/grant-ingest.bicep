param clusterLogManagedIdentityId string

param databaseName string

@description('Geo short ID of the region')
param geoShortId string

var kustoName = 'hcp-${geoShortId}'

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' existing = {
  name: '${kustoName}/${databaseName}'
}

resource grantSVCIngest 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = {
  parent: database
  name: 'grant-${guid(clusterLogManagedIdentityId, databaseName)}'
  properties: {
    principalId: clusterLogManagedIdentityId
    principalType: 'App'
    role: 'Ingestor'
    tenantId: tenant().tenantId
  }
}
