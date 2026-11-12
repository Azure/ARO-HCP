@description('Resource ID of the Kusto cluster')
param kustoClusterId string

@description('SL action group resource ID, injected from monitoring step output')
param actionGroupSL string

@description('Event Hub action group resource ID, injected from monitoring step output')
param actionGroupAlertEH string = ''

@description('Whether alerts are enabled')
param alertsEnabled bool

@description('Region of the Kusto cluster (empty string when Kusto is disabled)')
param kustoRegion string

@description('Region of the monitoring deployment')
param regionLocation string

// The eastus2euap rollout hosts its Kusto cluster in eastus2.
var isKustoOwnerRegion = kustoRegion == regionLocation || (regionLocation == 'eastus2euap' && kustoRegion == 'eastus2')

var ehActionGroups = actionGroupAlertEH != '' ? [actionGroupAlertEH] : []

module kustoAlerts '../modules/metrics/kusto-alerts.bicep' = if (kustoClusterId != '' && isKustoOwnerRegion) {
  name: 'kustoAlerts'
  params: {
    kustoClusterId: kustoClusterId
    actionGroups: actionGroupSL != '' ? concat([actionGroupSL], ehActionGroups) : ehActionGroups
    enabled: alertsEnabled
  }
}
