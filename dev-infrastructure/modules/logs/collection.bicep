param aksClusterName string
param logAnalyticsWorkspaceId string

param dcrStreams array = [
  'Microsoft-ContainerLog'
  'Microsoft-ContainerLogV2'
  'Microsoft-KubeEvents'
  'Microsoft-KubePodInventory'
]

resource aksCluster 'Microsoft.ContainerService/managedClusters@2023-03-01' existing = {
  name: aksClusterName
}

resource aksDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2017-05-01-preview' = {
  scope: aksCluster
  name: aksClusterName
  properties: {
    logs: [
      {
        category: 'kube-audit'
        enabled: true
      }
      {
        category: 'kube-audit-admin'
        enabled: true
      }
    ]
    workspaceId: logAnalyticsWorkspaceId
  }
}

resource aksClusterDcr 'Microsoft.Insights/dataCollectionRules@2023-03-11' = {
  name: '${aksClusterName}-dcr'
  location: resourceGroup().location
  kind: 'Linux'
  properties: {
    dataSources: {
      extensions: [
        {
          name: 'ContainerInsightsExtension'
          streams: dcrStreams
          extensionSettings: {
            dataCollectionSettings: {
              interval: '1m'
              namespaceFilteringMode: 'Off'
              enableContainerLogV2: true
            }
          }
          extensionName: 'ContainerInsights'
        }
      ]
    }
    destinations: {
      logAnalytics: [
        {
          name: 'ciworkspace'
          workspaceResourceId: logAnalyticsWorkspaceId
        }
      ]
    }
    dataFlows: [
      {
        destinations: ['ciworkspace']
        streams: dcrStreams
      }
    ]
  }
}

resource aksClusterDcra 'Microsoft.Insights/dataCollectionRuleAssociations@2023-03-11' = {
  name: '${aksClusterName}-logs-dcra'
  scope: aksCluster
  properties: {
    description: 'AKS Cluster DCRA for logs DCR'
    dataCollectionRuleId: aksClusterDcr.id
  }
}
