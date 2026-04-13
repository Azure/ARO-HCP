@description('Name of the alert rule')
param alertName string

@description('Azure region')
param location string

@description('Resource ID of the ADX cluster to query')
param kustoClusterId string

@description('KQL query to evaluate')
param query string

@description('Alert severity: 0=Critical, 1=Error, 2=Warning, 3=Informational')
@allowed([0, 1, 2, 3])
param severity int

@description('Threshold value for the alert condition')
param threshold int

@description('Comparison operator for the alert condition')
@allowed(['GreaterThan', 'GreaterThanOrEqual', 'LessThan', 'LessThanOrEqual', 'Equal'])
param operator string

@description('How often the query is evaluated (ISO 8601 duration, e.g. PT5M)')
param evaluationFrequency string

@description('Time window for the query (ISO 8601 duration, e.g. PT15M)')
param windowSize string

@description('Aggregation type for the query result')
@allowed(['Count', 'Average', 'Minimum', 'Maximum', 'Total'])
param timeAggregation string

@description('Resource IDs of the action groups to notify')
param actionGroupIds array

@description('Whether the alert rule is enabled')
param enabled bool

@description('Resource ID of the user-assigned managed identity for ADX access')
param identityId string

@description('Auto-resolve when condition clears')
param autoMitigate bool = true

@sys.description('Description of the alert rule')
param alertDescription string = ''

@description('Dimensions to split alerts by (e.g. cluster, name)')
param dimensions array = []

resource alertRule 'Microsoft.Insights/scheduledQueryRules@2023-03-15-preview' = {
  name: alertName
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${identityId}': {}
    }
  }
  properties: {
    actions: {
      actionGroups: actionGroupIds
    }
    autoMitigate: autoMitigate
    criteria: {
      allOf: [
        {
          dimensions: dimensions
          failingPeriods: {
            minFailingPeriodsToAlert: 1
            numberOfEvaluationPeriods: 1
          }
          operator: operator
          query: query
          resourceIdColumn: ''
          threshold: threshold
          timeAggregation: timeAggregation
        }
      ]
    }
    description: alertDescription
    displayName: alertName
    enabled: enabled
    evaluationFrequency: evaluationFrequency
    scopes: [kustoClusterId]
    severity: severity
    targetResourceTypes: ['Microsoft.Kusto/clusters']
    windowSize: windowSize
  }
}
