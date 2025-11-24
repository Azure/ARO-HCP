param clusterLogPrincipalId string

param databaseName string

@description('Name of the Kusto cluster to grant ingest to')
param kustoName string

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' existing = {
  name: '${kustoName}/${databaseName}'
}

resource grantSVCIngest 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = {
  parent: database
  name: 'grant-${guid(clusterLogPrincipalId, databaseName)}'
  properties: {
    principalId: clusterLogPrincipalId
    principalType: 'App'
    role: 'Ingestor'
    tenantId: tenant().tenantId
  }
}
