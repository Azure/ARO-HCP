param azureMonitoringWorkspaceId string
param azureMonitorWorkspaceLocation string
param aksClusterName string

var dceName = take('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)
var dcrName = take('MSProm-${azureMonitorWorkspaceLocation}-${aksClusterName}', 44)

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
          accountResourceId: azureMonitoringWorkspaceId
          name: 'MonitoringAccount1'
        }
      ]
    }
  }
}

output dcrId string = dcr.id
