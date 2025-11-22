@description('Name of the Kusto cluster to lookup')
param kustoName string

@description('Toggle if instance should be created/managed')
param manageInstance bool

resource kusto 'Microsoft.Kusto/clusters@2024-04-13' existing = if (manageInstance) {
  name: kustoName
}

output kustoResourceId string = manageInstance ? kusto.id : ''
output kustoUri string = manageInstance ? kusto.properties.uri : ''
output kustoDataIngestionUri string = manageInstance ? kusto.properties.dataIngestionUri : ''
