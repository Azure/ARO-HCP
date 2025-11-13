@description('Name of the Kusto cluster owning this database')
param kustoName string

@description('Name of the database to create')
param databaseName string

@description('Soft delete period for the database (ISO 8601 duration)')
param softDeletePeriod string = 'P14D'

@description('Hot cache period for the database (ISO 8601 duration)')
param hotCachePeriod string = 'P2D'

@description('Geneva viewer principal id (optional)')
param genevaViewerPrincipalId string?

@description('Geneva viewer tenant id (optional)')
param genevaViewerTenantId string?

@description('ICM viewer principal id (optional)')
param icmViewerPrincipalId string?

@description('ICM viewer tenant id (optional)')
param icmViewerTenantId string?

// Create the database as a resource whose name includes the cluster (parent)
resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  name: '${kustoName}/${databaseName}'
  location: resourceGroup().location
  kind: 'ReadWrite'
  properties: {
    softDeletePeriod: softDeletePeriod
    hotCachePeriod: hotCachePeriod
  }

  // Optional principal assignments (viewers) - only created when IDs are provided
  resource geneva_viewer 'principalAssignments' = if (genevaViewerPrincipalId != null && genevaViewerPrincipalId != '') {
    name: 'geneva_viewer'
    properties: {
      principalId: genevaViewerPrincipalId!
      principalType: 'App'
      role: 'Viewer'
      tenantId: genevaViewerTenantId!
    }
  }

  resource icm_viewer 'principalAssignments' = if (icmViewerPrincipalId != null && icmViewerPrincipalId != '') {
    name: 'icm_viewer'
    properties: {
      principalId: icmViewerPrincipalId!
      principalType: 'App'
      role: 'Viewer'
      tenantId: icmViewerTenantId!
    }
  }
}

output name string = database.name
output id string = database.id
