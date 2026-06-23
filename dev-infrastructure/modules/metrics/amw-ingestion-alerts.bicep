@description('Resource ID of the Azure Monitor Workspace to monitor')
param azureMonitorWorkspaceId string

@description('Short name to identify the workspace in alert names (e.g. "svc" or "hcp")')
param workspaceLabel string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('Whether alerts are enabled')
param enabled bool

@description('Threshold (percent) below which the low event ingestion alert fires')
param lowEventIngestionThreshold int

var amwName = last(split(azureMonitorWorkspaceId, '/'))

// Severity 4 (Informational): approaching limits — capacity planning signal
// Severity 3 (Warning): high risk of throttling — matches all other production alerts in the repo

resource approachingActiveTimeSeries 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'AMW Approaching Active TimeSeries Limit - ${workspaceLabel} - ${amwName}'
  location: 'global'
  properties: {
    description: 'Active Time Series utilization is above 75%. Plan a limit increase before throttling occurs. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
    severity: 4
    enabled: enabled
    autoMitigate: true
    scopes: [
      azureMonitorWorkspaceId
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
      }
    ]
  }
}

resource highRiskActiveTimeSeries 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'AMW High Risk Active TimeSeries Limit - ${workspaceLabel} - ${amwName}'
  location: 'global'
  properties: {
    description: 'Active Time Series utilization is above 95%. Throttling is imminent. Request a limit increase immediately. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      azureMonitorWorkspaceId
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
      }
    ]
  }
}

resource approachingEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'AMW Approaching Event Ingestion Limit - ${workspaceLabel} - ${amwName}'
  location: 'global'
  properties: {
    description: 'Events Per Minute utilization is above 75%. Plan a limit increase before throttling occurs. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
    severity: 4
    enabled: enabled
    autoMitigate: true
    scopes: [
      azureMonitorWorkspaceId
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
      }
    ]
  }
}

resource highRiskEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'AMW High Risk Event Ingestion Limit - ${workspaceLabel} - ${amwName}'
  location: 'global'
  properties: {
    description: 'Events Per Minute utilization is above 95%. Throttling is imminent. Request a limit increase immediately. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      azureMonitorWorkspaceId
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
      }
    ]
  }
}

resource lowEventIngestion 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'AMW Low Event Ingestion Utilization - ${workspaceLabel} - ${amwName}'
  location: 'global'
  properties: {
    description: 'Events Per Minute utilization is below limit. This may indicate that Prometheus remote write is broken or that very few metrics are being ingested. Investigate the ingestion pipeline. https://learn.microsoft.com/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits'
    severity: 3
    enabled: enabled
    autoMitigate: true
    scopes: [
      azureMonitorWorkspaceId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT30M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          threshold: lowEventIngestionThreshold
          name: 'EventsPerMinuteCriteria'
          metricName: 'EventsPerMinuteIngestedPercentUtilization'
          operator: 'LessThan'
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
