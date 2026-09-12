@description('Object ID of the principal to grant access to.')
param principalId string

@description('Name of the Kusto cluster holding the database.')
param kustoName string

@description('Name of the database to grant access on.')
param databaseName string

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' existing = {
  name: '${kustoName}/${databaseName}'
}

// The role is part of each assignment's name so that one principal can hold
// both without the two resources colliding.
resource grantIngest 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = {
  parent: database
  name: 'grant-ingestor-${guid(principalId, databaseName, 'Ingestor')}'
  properties: {
    principalId: principalId
    principalType: 'App'
    role: 'Ingestor'
    tenantId: tenant().tenantId
  }
}

resource grantRead 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = {
  parent: database
  name: 'grant-viewer-${guid(principalId, databaseName, 'Viewer')}'
  properties: {
    principalId: principalId
    principalType: 'App'
    role: 'Viewer'
    tenantId: tenant().tenantId
  }
}
