param azureMonitorWorkspaceName string
param azureMonitorWorkspaceLocation string
param aksClusterName string
param regionalResourceGroup string

var dceName = take('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)
var dcrName = take('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)

resource amw 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  name: azureMonitorWorkspaceName
  scope: resourceGroup(regionalResourceGroup)
}

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' = {
  name: dceName
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
  properties: {}
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' = {
  name: dcrName
  location: azureMonitorWorkspaceLocation
  kind: 'Linux'
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
          accountResourceId: amw.id 
          name: 'MonitoringAccount1'
        }
      ]
    }
  }
}

output dcrId string = dcr.id
