// Kusto (ADX) alert rule definitions.
// Add new alerts here — no changes to shared infrastructure (main.bicep) needed.
//
// To add a new alert:
//   1. Define a query template variable using triple-quoted KQL
//      - Use adx('{0}').tableName and replace '{0}' with adxServiceLogs
//      - Add explicit time filters: | where timestamp >= ago(5m) and timestamp <= now()
//      - Project the columns needed for dimensions and context
//   2. Create a module block referencing 'alert-rule.bicep'
//      - Use replace() to inject adxServiceLogs or adxHcpLogs into the query
//      - Set dimensions to split alerts (e.g. by cluster)

@description('Azure region')
param location string

@description('Resource ID of the ADX cluster')
param kustoClusterId string

@description('Resource IDs of the action groups to notify')
param actionGroupIds array

@description('Resource ID of the user-assigned managed identity')
param identityId string

@description('Pre-built adx() URI for ServiceLogs database')
param adxServiceLogs string

// --- Fluent-bit OOM killing alert ---

var oomKillingQueryTemplate = '''
adx('{0}').kubernetesEvents
| where timestamp >= ago(5m) and timestamp <= now()
| where reason == 'OOMKilling'
| extend name = extract(@"\d+ \(([^)]+)\)", 1, message)
| where name == 'fluent-bit'
| project timestamp, cluster, name, message
'''

module oomKillingAlert 'alert-rule.bicep' = {
  name: 'oom-killing-alert'
  params: {
    alertName: 'FluentBitOOMKilling'
    location: location
    kustoClusterId: kustoClusterId
    query: replace(oomKillingQueryTemplate, '{0}', adxServiceLogs)
    severity: 2
    threshold: 0
    operator: 'GreaterThan'
    timeAggregation: 'Count'
    evaluationFrequency: 'PT5M'
    windowSize: 'PT5M'
    actionGroupIds: actionGroupIds
    identityId: identityId
    enabled: true
    alertDescription: 'Fluent-bit container is being OOM killed'
    dimensions: [
      {
        name: 'cluster'
        operator: 'Include'
        values: ['*']
      }
    ]
  }
}
