@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param enabled bool

@description('Workspace configurations to monitor')
param workspaces array

// Severity 4 (Informational): approaching limits — capacity planning signal
// Severity 3 (Warning): high risk of throttling — matches all other production alerts in the repo

resource approachingActiveTimeSeries 'Microsoft.Insights/metricAlerts@2018-03-01' = [
  for ws in workspaces: {
    name: 'AMW Approaching Active TimeSeries Limit - ${ws.label}'
    location: 'global'
    properties: {
      description: 'Active Time Series utilization is above 75%. Plan a limit increase before throttling occurs. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
      severity: 4
      enabled: enabled
      autoMitigate: true
      scopes: [
        ws.id
      ]
      evaluationFrequency: 'PT5M'
      windowSize: 'PT30M'
      criteria: {
        'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
        allOf: [
          {
            threshold: 75
            name: 'ActiveTimeSeriesCriteria'
            metricName: 'ActiveTimeSeriesPercentUtilization'
            operator: 'GreaterThan'
            timeAggregation: 'Average'
            criterionType: 'StaticThresholdCriterion'
          }
        ]
      }
      actions: [
        for g in actionGroups: {
          actionGroupId: g
          webHookProperties: {
            'IcM.CorrelationId': 'AMWActiveTimeSeriesLimit/${ws.label}/${last(split(ws.id, '/'))}'
          }
        }
      ]
    }
  }
]

resource highRiskActiveTimeSeries 'Microsoft.Insights/metricAlerts@2018-03-01' = [
  for ws in workspaces: {
    name: 'AMW High Risk Active TimeSeries Limit - ${ws.label}'
    location: 'global'
    properties: {
      description: 'Active Time Series utilization is above 95%. Throttling is imminent. Request a limit increase immediately. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
      severity: 3
      enabled: enabled
      autoMitigate: true
      scopes: [
        ws.id
      ]
      evaluationFrequency: 'PT5M'
      windowSize: 'PT30M'
      criteria: {
        'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
        allOf: [
          {
            threshold: 95
            name: 'ActiveTimeSeriesCriteria'
            metricName: 'ActiveTimeSeriesPercentUtilization'
            operator: 'GreaterThan'
            timeAggregation: 'Average'
            criterionType: 'StaticThresholdCriterion'
          }
        ]
      }
      actions: [
        for g in actionGroups: {
          actionGroupId: g
          webHookProperties: {
            'IcM.CorrelationId': 'AMWActiveTimeSeriesLimit/${ws.label}/${last(split(ws.id, '/'))}'
          }
        }
      ]
    }
  }
]

resource approachingEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = [
  for ws in workspaces: {
    name: 'AMW Approaching Event Ingestion Limit - ${ws.label}'
    location: 'global'
    properties: {
      description: 'Events Per Minute utilization is above 75%. Plan a limit increase before throttling occurs. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
      severity: 4
      enabled: enabled
      autoMitigate: true
      scopes: [
        ws.id
      ]
      evaluationFrequency: 'PT5M'
      windowSize: 'PT30M'
      criteria: {
        'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
        allOf: [
          {
            threshold: 75
            name: 'EventsPerMinuteCriteria'
            metricName: 'EventsPerMinuteIngestedPercentUtilization'
            operator: 'GreaterThan'
            timeAggregation: 'Average'
            criterionType: 'StaticThresholdCriterion'
          }
        ]
      }
      actions: [
        for g in actionGroups: {
          actionGroupId: g
          webHookProperties: {
            'IcM.CorrelationId': 'AMWEventIngestionLimit/${ws.label}/${last(split(ws.id, '/'))}'
          }
        }
      ]
    }
  }
]

resource highRiskEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = [
  for ws in workspaces: {
    name: 'AMW High Risk Event Ingestion Limit - ${ws.label}'
    location: 'global'
    properties: {
      description: 'Events Per Minute utilization is above 95%. Throttling is imminent. Request a limit increase immediately. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
      severity: 3
      enabled: enabled
      autoMitigate: true
      scopes: [
        ws.id
      ]
      evaluationFrequency: 'PT5M'
      windowSize: 'PT30M'
      criteria: {
        'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
        allOf: [
          {
            threshold: 95
            name: 'EventsPerMinuteCriteria'
            metricName: 'EventsPerMinuteIngestedPercentUtilization'
            operator: 'GreaterThan'
            timeAggregation: 'Average'
            criterionType: 'StaticThresholdCriterion'
          }
        ]
      }
      actions: [
        for g in actionGroups: {
          actionGroupId: g
          webHookProperties: {
            'IcM.CorrelationId': 'AMWEventIngestionLimit/${ws.label}/${last(split(ws.id, '/'))}'
          }
        }
      ]
    }
  }
]

resource lowEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = [
  for ws in workspaces: {
    name: 'AMW Low Event Ingestion Utilization - ${ws.label}'
    location: 'global'
    properties: {
      description: 'Events Per Minute ingestion is below ${ws.lowEventIngestionThreshold}. This may indicate that Prometheus remote write is broken or that very few metrics are being ingested. Investigate the ingestion pipeline. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
      severity: 3
      enabled: enabled
      autoMitigate: true
      scopes: [
        ws.id
      ]
      evaluationFrequency: 'PT5M'
      windowSize: 'PT30M'
      criteria: {
        'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
        allOf: [
          {
            threshold: ws.lowEventIngestionThreshold
            name: 'EventsPerMinuteCriteria'
            metricName: 'EventsPerMinuteIngested'
            operator: 'LessThan'
            timeAggregation: 'Average'
            criterionType: 'StaticThresholdCriterion'
          }
        ]
      }
      actions: [
        for g in actionGroups: {
          actionGroupId: g
          webHookProperties: {
            'IcM.CorrelationId': 'AMWEventIngestionLimit/${ws.label}/${last(split(ws.id, '/'))}'
          }
        }
      ]
    }
  }
]
