@description('Resource ID of the Kusto cluster')
param kustoClusterId string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param alertsEnabled bool

@description('Region of the Kusto cluster (empty string when Kusto is disabled)')
param kustoRegion string

@description('Region of the monitoring deployment')
param regionLocation string

module kustoAlerts '../modules/metrics/kusto-alerts.bicep' = if (kustoClusterId != '' && kustoRegion == regionLocation) {
  name: 'kustoAlerts'
  params: {
    kustoClusterId: kustoClusterId
    actionGroups: actionGroups
    enabled: alertsEnabled
  }
}
