// CI Monitoring Infrastructure
// This template creates SHARED persistent monitoring infrastructure for ephemeral CI jobs
// Metrics from ephemeral CI environments will be remote written to this persistent AMW

@description('Location for the CI monitoring resources')
param location string = resourceGroup().location

@description('Name of the shared CI Azure Monitor Workspace')
param ciWorkspaceName string

@description('Name of the shared CI HCP Azure Monitor Workspace')
param ciHcpWorkspaceName string = ''

@description('Whether to create HCP workspace for CI')
param createHcpWorkspace bool = false

@description('Principal ID of the Prometheus workload identity that will write metrics')
param prometheusPrincipalId string

// Shared CI Services Metrics Workspace
resource ciMonitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: ciWorkspaceName
  location: location
  tags: {
    aroHCPPurpose: 'ci-ephemeral-metrics'
    environment: 'ci'
    retentionDays: '90'
  }
}

// Shared CI HCP Metrics Workspace (optional, for CI jobs that create HCPs)
resource ciHcpMonitor 'microsoft.monitor/accounts@2021-06-03-preview' = if (createHcpWorkspace) {
  name: ciHcpWorkspaceName
  location: location
  tags: {
    aroHCPPurpose: 'ci-hcp-metrics'
    environment: 'ci'
    retentionDays: '90'
  }
}

// Data Collection Endpoint for Prometheus remote write
resource ciDce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' = {
  name: 'ci-metrics-dce'
  location: location
  kind: 'Linux'
  tags: {
    purpose: 'ci'
  }
  properties: {
    description: 'Data Collection Endpoint for CI ephemeral environments Prometheus remote write'
  }
}

// Data Collection Rule for Services Metrics
resource ciDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = {
  name: 'ci-metrics-dcr'
  location: location
  kind: 'Linux'
  tags: {
    purpose: 'ci-services'
  }
  properties: {
    dataCollectionEndpointId: ciDce.id
    dataFlows: [
      {
        destinations: [
          'CIMonitoringAccount'
        ]
        streams: [
          'Microsoft-PrometheusMetrics'
        ]
      }
    ]
    dataSources: {
      prometheusForwarder: [
        {
          name: 'PrometheusDataSource'
          streams: [
            'Microsoft-PrometheusMetrics'
          ]
          labelIncludeFilter: {}
        }
      ]
    }
    description: 'DCR for CI environments - services and infrastructure metrics'
    destinations: {
      monitoringAccounts: [
        {
          accountResourceId: ciMonitor.id
          name: 'CIMonitoringAccount'
        }
      ]
    }
  }
}

// Data Collection Rule for HCP Metrics (if enabled)
resource ciHcpDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = if (createHcpWorkspace) {
  name: 'ci-hcp-metrics-dcr'
  location: location
  kind: 'Linux'
  tags: {
    purpose: 'ci-hcp'
  }
  properties: {
    dataCollectionEndpointId: ciDce.id
    dataFlows: [
      {
        destinations: [
          'CIHCPMonitoringAccount'
        ]
        streams: [
          'Microsoft-PrometheusMetrics'
        ]
      }
    ]
    dataSources: {
      prometheusForwarder: [
        {
          name: 'PrometheusDataSource'
          streams: [
            'Microsoft-PrometheusMetrics'
          ]
          labelIncludeFilter: {}
        }
      ]
    }
    description: 'DCR for CI environments - HCP metrics'
    destinations: {
      monitoringAccounts: [
        {
          accountResourceId: ciHcpMonitor.id
          name: 'CIHCPMonitoringAccount'
        }
      ]
    }
  }
}

// Grafana-to-AMW integration (Monitoring Data Reader role assignments) is managed
// by grafanactl via the GrafanaDatasources pipeline action, not bicep.

// Prometheus Workload Identity - Grant Monitoring Metrics Publisher role on Services DCR
var metricsPublisher = '3913510d-42f4-4e42-8a64-420c390055eb' // Monitoring Metrics Publisher role

resource ciPrometheusMetricsPublisher 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(ciDcr.id, prometheusPrincipalId, metricsPublisher)
  scope: ciDcr
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', metricsPublisher)
    principalId: prometheusPrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Prometheus Workload Identity - Grant Monitoring Metrics Publisher role on HCP DCR (if enabled)
resource ciHcpPrometheusMetricsPublisher 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (createHcpWorkspace) {
  name: guid(ciHcpDcr.id, prometheusPrincipalId, metricsPublisher)
  scope: ciHcpDcr
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', metricsPublisher)
    principalId: prometheusPrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Outputs
output ciWorkspaceId string = ciMonitor.id
output ciWorkspacePrometheusQueryEndpoint string = ciMonitor.properties.metrics.prometheusQueryEndpoint

output ciHcpWorkspaceId string = createHcpWorkspace ? ciHcpMonitor.id : ''
output ciHcpWorkspacePrometheusQueryEndpoint string = createHcpWorkspace
  ? ciHcpMonitor.properties.metrics.prometheusQueryEndpoint
  : ''

output ciDceId string = ciDce.id
output ciDceMetricsIngestionEndpoint string = ciDce.properties.metricsIngestion.endpoint

output ciDcrId string = ciDcr.id
output ciDcrRemoteWriteUrl string = '${ciDce.properties.metricsIngestion.endpoint}/dataCollectionRules/${ciDcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'

output ciHcpDcrId string = createHcpWorkspace ? ciHcpDcr.id : ''
output ciHcpDcrRemoteWriteUrl string = createHcpWorkspace
  ? '${ciDce.properties.metricsIngestion.endpoint}/dataCollectionRules/${ciHcpDcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
  : 'NONE'
