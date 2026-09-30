@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Kusto cluster owning this database')
param kustoName string

@description('Name of the database to create')
param databaseName string

@description('Soft delete period for the database (ISO 8601 duration)')
param softDeletePeriod string = 'P14D'

@description('Hot cache period for the database (ISO 8601 duration)')
param hotCachePeriod string = 'P2D'

// Create the database as a resource whose name includes the cluster (parent)
resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  name: '${kustoName}/${databaseName}'
  location: location
  kind: 'ReadWrite'
  properties: {
    softDeletePeriod: softDeletePeriod
    hotCachePeriod: hotCachePeriod
  }
}

output name string = database.name
output id string = database.id
