param ingestAccessPrincipalIds array = []

param readAccessPrincipalIds array = []

param databaseName string

@description('Name of the Kusto cluster to grant ingest to')
param kustoName string

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' existing = {
  name: '${kustoName}/${databaseName}'
}

resource grantIngest 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = [
  for id in ingestAccessPrincipalIds: {
    parent: database
    name: 'grant-${guid(id, databaseName)}'
    properties: {
      principalId: id
      principalType: 'App'
      role: 'Ingestor'
      tenantId: tenant().tenantId
    }
  }
]

resource grantRead 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = [
  for id in readAccessPrincipalIds: {
    parent: database
    name: 'grant-${guid(id, databaseName)}'
    properties: {
      principalId: id
      principalType: 'App'
      role: 'Viewer'
      tenantId: tenant().tenantId
    }
  }
]
