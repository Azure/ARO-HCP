import { safeTake } from '../common.bicep'

param azureMonitoringWorkspaceId string
param hcpAzureMonitoringWorkspaceId string = ''
param azureMonitorWorkspaceLocation string
param hcpAzureMonitorWorkspaceLocation string = azureMonitorWorkspaceLocation
@description('Cluster region used in resource names, independent of workspace placement')
param location string = azureMonitorWorkspaceLocation
param aksClusterName string
param prometheusPrincipalId string

// Keep names stable when selecting workspaces in another region; output templates reconstruct these names.
var dceName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var dcrName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var hcpDcrName = safeTake('HCP-${location}-${aksClusterName}', 44)
var separateHcpDce = hcpAzureMonitoringWorkspaceId != '' && hcpAzureMonitorWorkspaceLocation != azureMonitorWorkspaceLocation

resource hcpDce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' = if (separateHcpDce) {
  name: hcpDcrName
  location: hcpAzureMonitorWorkspaceLocation
  kind: 'Linux'
  tags: {
    purpose: 'hcp'
  }
  properties: {}
}

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
  location: hcpAzureMonitorWorkspaceLocation
  kind: 'Linux'
  tags: {
    purpose: 'hcp'
  }
  properties: {
    dataCollectionEndpointId: separateHcpDce ? hcpDce!.id : dce.id
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
