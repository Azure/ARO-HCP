param aksClusterName string

param auditLogsEventHubName string

param auditLogsEventHubAuthRuleId string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: aksClusterName
}

// Diagnostic settings for AKS audit logs
resource aksDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = {
  scope: aksCluster
  name: 'aks-audit-logs'
  properties: {
    eventHubAuthorizationRuleId: auditLogsEventHubAuthRuleId
    eventHubName: auditLogsEventHubName
    logs: [
      {
        category: 'kube-audit'
        enabled: true
      }
      {
        category: 'cluster-autoscaler'
        enabled: true
      }
      {
        category: 'kube-apiserver'
        enabled: true
      }
      {
        category: 'kube-controller-manager'
        enabled: true
      }
      {
        category: 'kube-scheduler'
        enabled: true
      }
    ]
  }
}
