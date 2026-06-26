param azureMonitoring string

param location string = resourceGroup().location

resource arohcpAccessClusterSloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_access_cluster_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'errors:backend_credential_operation:succeeded_total'
        expression: 'count by (cluster) (backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase="succeeded" })'
      }
      {
        record: 'errors:backend_credential_operation:terminal_total'
        expression: 'count by (cluster) (backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase=~"succeeded|failed|canceled" })'
      }
      {
        record: 'errors:backend_credential_operation:error_rate'
        expression: '( count by (cluster) (backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase="failed" }) or 0 * count by (cluster) (backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase=~"succeeded|failed|canceled" }) ) / clamp_min( count by (cluster) (backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase=~"succeeded|failed|canceled" }), 1 )'
      }
    ]
  }
}
