@description('Resource ID of the ADX cluster (empty string if kusto is not enabled)')
param kustoClusterId string

@description('Name of the Kusto cluster')
param kustoName string

@description('URI of the ADX cluster')
param kustoUri string

@description('Resource ID of the SL ICM action group (empty string if not available)')
param slActionGroupId string

@description('Name of the service logs database')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database')
param hostedControlPlaneLogsDatabase string

module kustoAlerting '../modules/kusto-alerts/main.bicep' = if (kustoClusterId != '') {
  name: 'kusto-alerting'
  params: {
    kustoClusterId: kustoClusterId
    slActionGroupId: slActionGroupId
    serviceLogsDatabase: serviceLogsDatabase
    hostedControlPlaneLogsDatabase: hostedControlPlaneLogsDatabase
    kustoName: kustoName
    kustoUri: kustoUri
  }
}
