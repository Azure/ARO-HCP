
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
        expression: 'count by (cluster) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_credential_operation:terminal_total'
        expression: 'count by (cluster) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_credential_operation:error_rate'
        expression: '(count by (cluster) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) or 0 * count by (cluster) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) / clamp_min(count by (cluster) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}), 1)'
      }
    ]
  }
}

resource arohcpClusterProvisionSloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_provision_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'errors:backend_cluster_provision:succeeded_total'
        expression: 'count by (cluster) (backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_cluster_provision:terminal_total'
        expression: 'count by (cluster) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_cluster_provision:error_rate'
        expression: '(count by (cluster) (backend_resource_operation_phase_info{operation_type="create",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) or 0 * count by (cluster) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) / clamp_min(count by (cluster) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}), 1)'
      }
    ]
  }
}

resource arohcpClusterDeletionSloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_deletion_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'errors:backend_cluster_deletion_operation:error_rate'
        expression: '(count by (cluster) (backend_resource_operation_phase_info{operation_type="delete",phase=~"failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) or 0 * count by (cluster) (backend_resource_operation_phase_info{operation_type="delete",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) / clamp_min(count by (cluster) (backend_resource_operation_phase_info{operation_type="delete",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}), 1)'
      }
      {
        record: 'errors:backend_cluster_deletion_operation:timeliness_error_rate'
        expression: '(count by (cluster) ((backend_resource_operation_last_transition_time_seconds{operation_type="delete",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} - backend_resource_operation_start_time_seconds{operation_type="delete",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) >= 1800 and backend_resource_operation_phase_info{operation_type="delete",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1) or 0 * count by (cluster) (backend_resource_operation_phase_info{operation_type="delete",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1)) / clamp_min(count by (cluster) (backend_resource_operation_phase_info{operation_type="delete",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1), 1)'
      }
    ]
  }
}
