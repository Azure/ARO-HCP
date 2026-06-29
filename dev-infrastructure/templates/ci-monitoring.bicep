// CI Monitoring Infrastructure
// This template creates SHARED persistent monitoring infrastructure for ephemeral CI jobs
// Metrics from ephemeral CI environments will be remote written to this persistent AMW

@description('Location for the CI monitoring resources')
param location string = resourceGroup().location

@description('Name of the shared CI Azure Monitor Workspace')
param ciWorkspaceName string

@description('Name of the shared CI HCP Azure Monitor Workspace')
param ciHcpWorkspaceName string = ''

@description('Resource ID of the Grafana instance to integrate with')
param grafanaResourceId string

@description('Whether to create HCP workspace for CI')
param createHcpWorkspace bool = false

import * as res from '../modules/resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

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

// Grafana Integration - Services Metrics
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136' // Monitoring Data Reader role

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

resource ciGrafanaRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(ciMonitor.id, grafana.id, dataReader)
  scope: ciMonitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

// Grafana Integration - HCP Metrics (if enabled)
resource ciHcpGrafanaRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (createHcpWorkspace) {
  name: guid(ciHcpMonitor.id, grafana.id, dataReader)
  scope: ciHcpMonitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
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
output ciDcrRemoteWriteUrl string = '${ciDce.properties.metricsIngestion.endpoint}/dataCollectionRules/${ciDcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2021-11-01-preview'

output ciHcpDcrId string = createHcpWorkspace ? ciHcpDcr.id : ''
output ciHcpDcrRemoteWriteUrl string = createHcpWorkspace
  ? '${ciDce.properties.metricsIngestion.endpoint}/dataCollectionRules/${ciHcpDcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2021-11-01-preview'
  : ''
