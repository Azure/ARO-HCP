@description('Resource ID of the Kusto cluster to monitor')
param kustoClusterId string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param enabled bool

var kustoName = last(split(kustoClusterId, '/'))

resource ingestionLatencyAboveAverage 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'Kusto Ingestion Latency Above Average - ${kustoName}'
  location: 'global'
  properties: {
    description: 'Kusto ingestion latency is above its dynamic baseline. Investigate the ingestion pipeline for slowdowns. https://learn.microsoft.com/azure/data-explorer/monitor-batching-ingestion'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      kustoClusterId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT30M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.MultipleResourceMultipleMetricCriteria'
      allOf: [
        {
          name: 'IngestionLatencyCriteria'
          metricName: 'IngestionLatencyInSeconds'
          operator: 'GreaterThan'
          timeAggregation: 'Average'
          criterionType: 'DynamicThresholdCriterion'
          alertSensitivity: 'Medium'
          failingPeriods: {
            numberOfEvaluationPeriods: 4
            minFailingPeriodsToAlert: 3
          }
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

resource ingestionLatencyHigh 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'Kusto Ingestion Latency High - ${kustoName}'
  location: 'global'
  properties: {
    description: 'Kusto ingestion latency exceeds 15 minutes. This indicates a serious ingestion pipeline issue. https://learn.microsoft.com/azure/data-explorer/monitor-batching-ingestion'
    severity: 2
    enabled: enabled
    autoMitigate: true
    scopes: [
      kustoClusterId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT30M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          threshold: 900
          name: 'IngestionLatencyHighCriteria'
          metricName: 'IngestionLatencyInSeconds'
          operator: 'GreaterThan'
          timeAggregation: 'Average'
          criterionType: 'StaticThresholdCriterion'
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
