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

resource arohcpUserJourneyClusterUpgradeRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_user_journey_cluster_upgrade_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hosted_control_plane_upgrade:upgrade_eligible:info'
        expression: '((count by (cluster, resource_id, subscription_id, cluster_uuid) (count by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info == 1)) >= 2) and on (cluster, resource_id) (count by (cluster, resource_id) (backend_cluster_version_info{state="completed"} == 1) >= 1)) * 0 + 1'
      }
      {
        record: 'hosted_control_plane_upgrade:version_state_first_seen:timestamp'
        expression: 'min without (prometheus_replica) (min by (cluster, resource_id, subscription_id, cluster_uuid, version, state) ((hosted_control_plane_upgrade:version_state_first_seen:timestamp or (timestamp(backend_cluster_version_info{state=~"desired|partial"} == 1) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1))) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1)))'
      }
      {
        record: 'hosted_control_plane_upgrade:in_progress:count'
        expression: 'count by (cluster) (count by (cluster, resource_id) ((backend_cluster_version_info{state=~"desired|partial"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) backend_cluster_version_info{state="completed"} == 1)) >= 1 and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)) or 0 * count by (cluster) (backend_cluster_version_info)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_desired:seconds'
        expression: '(time() - hosted_control_plane_upgrade:version_state_first_seen:timestamp{state="desired"}) and on (cluster, resource_id, subscription_id, cluster_uuid, version) ((backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="partial"} == 1 or backend_cluster_version_info{state="completed"} == 1))) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_progress:seconds'
        expression: '(sum without (state) ((time() - hosted_control_plane_upgrade:version_state_first_seen:timestamp{state="desired"}) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1)) * on (cluster, resource_id, subscription_id, cluster_uuid, version) group_left (state) (backend_cluster_version_info{state="partial"} == 1 or (backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) backend_cluster_version_info{state="partial"} == 1))'
      }
    ]
  }
}
