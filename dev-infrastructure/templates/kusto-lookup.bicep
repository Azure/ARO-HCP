@description('Name of the Kusto cluster to lookup')
param kustoName string

@description('Toggle if instance is expected to exist')
param kustoEnabled bool

resource kusto 'Microsoft.Kusto/clusters@2024-04-13' existing = if (kustoEnabled) {
  name: kustoName
}

output kustoResourceId string = kustoEnabled ? kusto.id : ''
output kustoUri string = kustoEnabled ? kusto.properties.uri : ''
output kustoDataIngestionUri string = kustoEnabled ? kusto.properties.dataIngestionUri : ''
