@description('Resource ID of the Cosmos DB account to monitor')
param cosmosDbAccountId string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param enabled bool

var cosmosDbName = last(split(cosmosDbAccountId, '/'))

resource normalizedRUConsumptionHigh 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'Cosmos DB Normalized RU Consumption High - ${cosmosDbName}'
  location: 'global'
  properties: {
    description: 'Cosmos DB normalized RU consumption is above 70% averaged over a 15-minute window, evaluated every 5 minutes. Investigate workload patterns or increase provisioned throughput. https://learn.microsoft.com/azure/cosmos-db/monitor-normalized-request-units'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      cosmosDbAccountId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT15M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          threshold: 70
          name: 'NormalizedRUConsumptionCriteria'
          metricName: 'NormalizedRUConsumption'
          operator: 'GreaterThan'
          timeAggregation: 'Average'
          criterionType: 'StaticThresholdCriterion'
          dimensions: [
            {
              name: 'CollectionName'
              operator: 'Include'
              values: [
                '*'
              ]
            }
          ]
        }
      ]
    }
    actions: [
      for g in actionGroups: {
        actionGroupId: g
      }
    ]
  }
}
