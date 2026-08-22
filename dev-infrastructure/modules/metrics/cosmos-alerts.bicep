@description('Resource ID of the Cosmos DB account to monitor')
param cosmosDbAccountId string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param enabled bool

@description('ARO HCP region name')
param region string

var cosmosDbName = last(split(cosmosDbAccountId, '/'))

resource normalizedRUConsumptionHigh 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'Cosmos DB Normalized RU Consumption High - ${cosmosDbName}'
  location: 'global'
  properties: {
    description: 'Cosmos DB normalized RU consumption is above 70% averaged over a 10-minute window, evaluated every minute. Investigate workload patterns or increase provisioned throughput. https://learn.microsoft.com/azure/cosmos-db/monitor-normalized-request-units'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      cosmosDbAccountId
    ]
    evaluationFrequency: 'PT1M'
    windowSize: 'PT10M'
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
        webHookProperties: {
          'IcM.Title': '${region}: Cosmos DB Normalized RU Consumption High - ${cosmosDbName}'
          // TODO: This will associate all Collections to the same IcM.  There doesn't appear to
          // be a way to reference the Collection that fired within the alert itself without
          // defining multiple alerts.
          'IcM.CorrelationId': 'CosmosDBNormalizedRUConsumption/${cosmosDbName}'
        }
      }
    ]
  }
}

resource throttledRequestsHigh 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'Cosmos DB Throttled Requests (429) High - ${cosmosDbName}'
  location: 'global'
  properties: {
    description: 'Cosmos DB returned more than 100 throttled requests (HTTP 429 - request rate too large) summed over a 5-minute window, evaluated every minute. Sustained 429s mean the workload is exceeding provisioned throughput - investigate hot partitions or increase provisioned throughput (RU/s). A low, steady rate of 429s that client SDKs retry transparently can be normal. https://learn.microsoft.com/azure/cosmos-db/troubleshoot-request-rate-too-large'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      cosmosDbAccountId
    ]
    evaluationFrequency: 'PT1M'
    windowSize: 'PT5M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          threshold: 100
          name: 'ThrottledRequestsCriteria'
          metricName: 'TotalRequests'
          operator: 'GreaterThan'
          timeAggregation: 'Count'
          criterionType: 'StaticThresholdCriterion'
          dimensions: [
            {
              name: 'StatusCode'
              operator: 'Include'
              values: [
                '429'
              ]
            }
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
        webHookProperties: {
          'IcM.Title': '${region}: Cosmos DB Throttled Requests (429) High - ${cosmosDbName}'
          // TODO: This will associate all Collections to the same IcM.  There doesn't appear to
          // be a way to reference the Collection that fired within the alert itself without
          // defining multiple alerts.
          'IcM.CorrelationId': 'CosmosDBThrottledRequests429/${cosmosDbName}'
        }
      }
    ]
  }
}
