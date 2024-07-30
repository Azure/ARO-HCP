// This template is copied from https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/ARO-Pipelines?path=/metrics/infra/Templates/Alerts.bicep
// Ideally this template is consumed from ACR.

param azureMonitoring string

resource prometheusRuleGroups 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-prometheus-rules'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        // Copy from https://github.com/Azure/prometheus-collector/blob/main/AddonBicepTemplate/recommendedMetricAlerts.bicep
        alert: 'KubePodCrashLooping'
        expression: 'max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff", job="kube-state-metrics"}[5m]) >= 1'
        for: 'PT15M'
        enabled: true
        severity: 4
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
