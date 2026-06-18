import { safeTake } from '../common.bicep'

param azureMonitoringWorkspaceId string
param hcpAzureMonitoringWorkspaceId string = ''
param globalFleetMonitorId string = ''
param azureMonitorWorkspaceLocation string
param aksClusterName string
param prometheusPrincipalId string

var dceName = safeTake('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)
var dcrName = safeTake('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)
var hcpDcrName = safeTake('HCP-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)
var fleetDcrName = safeTake('Fleet-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' = {
  name: dceName
  tags: {
    purpose: 'aks'
  }
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
  properties: {}
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = {
  name: dcrName
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
  tags: {
    purpose: 'services'
  }
  properties: {
    dataCollectionEndpointId: dce.id
    dataFlows: [
      {
        destinations: [
          'MonitoringAccount1'
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
    description: 'DCR for Azure Monitor Metrics Profile (Managed Prometheus)'
    destinations: {
      monitoringAccounts: [
        {
          accountResourceId: azureMonitoringWorkspaceId
          name: 'MonitoringAccount1'
        }
      ]
    }
  }
}

resource hcpDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = if (hcpAzureMonitoringWorkspaceId != '') {
  name: hcpDcrName
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
  tags: {
    purpose: 'hcp'
  }
  properties: {
    dataCollectionEndpointId: dce.id
    dataFlows: [
      {
        destinations: [
          'MonitoringAccount1'
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
    description: 'DCR for Azure Monitor Metrics Profile (Managed Prometheus)'
    destinations: {
      monitoringAccounts: [
        {
          accountResourceId: hcpAzureMonitoringWorkspaceId
          name: 'MonitoringAccount1'
        }
      ]
    }
  }
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2023-03-01' existing = {
  name: aksClusterName
}

resource aksClusterDcra 'Microsoft.Insights/dataCollectionRuleAssociations@2022-06-01' = {
  name: '${aksClusterName}-dcra'
  scope: aksCluster
  properties: {
    description: 'Association of data collection rule. Deleting this association will break the data collection for this AKS Cluster.'
    dataCollectionRuleId: dcr.id
  }
}

resource monitoringMetricsPublisher 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksClusterName)
  scope: dcr
  properties: {
    roleDefinitionId: subscriptionResourceId(
      'Microsoft.Authorization/roleDefinitions',
      '3913510d-42f4-4e42-8a64-420c390055eb'
    )
    principalId: prometheusPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource hcpMonitoringMetricsPublisher 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (hcpAzureMonitoringWorkspaceId != '') {
  name: guid('hcp', aksClusterName)
  scope: hcpDcr
  properties: {
    roleDefinitionId: subscriptionResourceId(
      'Microsoft.Authorization/roleDefinitions',
      '3913510d-42f4-4e42-8a64-420c390055eb'
    )
    principalId: prometheusPrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Fleet-level DCR: forwards golden-signal metrics to the global AMW
// for fleet-wide dashboards. Only a small subset of metrics is forwarded
// to keep ingestion costs low.
resource fleetDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = if (globalFleetMonitorId != '') {
  name: fleetDcrName
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
  tags: {
    purpose: 'fleet-metrics'
  }
  properties: {
    dataCollectionEndpointId: dce.id
    dataFlows: [
      {
        destinations: [
          'FleetMonitoringAccount'
        ]
        streams: [
          'Microsoft-PrometheusMetrics'
        ]
      }
    ]
    dataSources: {
      prometheusForwarder: [
        {
          name: 'FleetPrometheusDataSource'
          streams: [
            'Microsoft-PrometheusMetrics'
          ]
          labelIncludeFilter: {
            __name__: [
              'backend_cluster_provision_state'
              'backend_node_pool_provision_state'
              'backend_cluster_created_time_seconds'
              'backend_resource_operation_phase_info'
              'backend_resource_operation_duration_seconds'
            ]
          }
        }
      ]
    }
    description: 'DCR for fleet golden-signal metrics forwarding to global AMW'
    destinations: {
      monitoringAccounts: [
        {
          accountResourceId: globalFleetMonitorId
          name: 'FleetMonitoringAccount'
        }
      ]
    }
  }
}

// Monitoring Metrics Publisher role (3913510d-42f4-4e42-8a64-420c390055eb)
resource fleetMonitoringMetricsPublisher 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (globalFleetMonitorId != '') {
  name: guid('fleet', aksClusterName)
  scope: fleetDcr
  properties: {
    roleDefinitionId: subscriptionResourceId(
      'Microsoft.Authorization/roleDefinitions',
      '3913510d-42f4-4e42-8a64-420c390055eb' // Monitoring Metrics Publisher
    )
    principalId: prometheusPrincipalId
    principalType: 'ServicePrincipal'
  }
}
