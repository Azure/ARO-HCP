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

resource userJourneyClusterUpgradeReachPartialRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'user-journey-cluster-upgrade-reach-partial-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hosted_control_plane_version:state_first_seen:timestamp'
        expression: 'min without (prometheus_replica) (min by (cluster, resource_id, subscription_id, cluster_uuid, version, state) (hosted_control_plane_version:state_first_seen:timestamp or (timestamp(backend_cluster_version_info == 1) and on (cluster, resource_id) (count by (cluster, resource_id) (backend_cluster_version_info{state="completed"} == 1) >= 1))))'
      }
      {
        record: 'hosted_control_plane_version:inflight_upgrade:count'
        expression: 'count by (cluster) (count by (cluster, resource_id) (backend_cluster_version_info{state=~"desired|partial"} == 1) >= 1 and on (cluster, resource_id) (count by (cluster, resource_id) (backend_cluster_version_info{state="completed"} == 1) >= 1)) or 0 * count by (cluster) (backend_cluster_version_info)'
      }
      {
        record: 'hosted_control_plane_version:duration_in_desired:seconds'
        expression: '(time() - hosted_control_plane_version:state_first_seen:timestamp{state="desired"}) and on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="desired"} == 1) and on (cluster, resource_id) (count by (cluster, resource_id) (backend_cluster_version_info{state="completed"} == 1) >= 1)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:count'
        expression: 'count by (cluster) (hosted_control_plane_version:duration_in_desired:seconds > 1200) or 0 * hosted_control_plane_version:inflight_upgrade:count'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:ratio'
        expression: 'hosted_control_plane_version:desired:stuck_over_20m:count / clamp_min(hosted_control_plane_version:inflight_upgrade:count, 1)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:burnrate5m'
        expression: 'round(avg_over_time(hosted_control_plane_version:desired:stuck_over_20m:ratio[5m]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:burnrate30m'
        expression: 'round(avg_over_time(hosted_control_plane_version:desired:stuck_over_20m:ratio[30m]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:burnrate1h'
        expression: 'round(avg_over_time(hosted_control_plane_version:desired:stuck_over_20m:ratio[1h]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:burnrate6h'
        expression: 'round(avg_over_time(hosted_control_plane_version:desired:stuck_over_20m:ratio[6h]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:desired:stuck_over_20m:burnrate3d'
        expression: 'round(avg_over_time(hosted_control_plane_version:desired:stuck_over_20m:ratio[3d]) / (1 - 0.9995), 0.01)'
      }
    ]
  }
}

resource userJourneyClusterUpgradeCompleteRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'user-journey-cluster-upgrade-complete-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hosted_control_plane_version:duration_in_progress:seconds'
        expression: '(time() - hosted_control_plane_version:state_first_seen:timestamp{state="desired"}) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1) and on (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state=~"desired|partial"} == 1) and on (cluster, resource_id) (count by (cluster, resource_id) (backend_cluster_version_info{state="completed"} == 1) >= 1)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:count'
        expression: 'count by (cluster) (hosted_control_plane_version:duration_in_progress:seconds > 1800) or 0 * hosted_control_plane_version:inflight_upgrade:count'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:ratio'
        expression: 'hosted_control_plane_version:in_progress:stuck_over_30m:count / clamp_min(hosted_control_plane_version:inflight_upgrade:count, 1)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:burnrate5m'
        expression: 'round(avg_over_time(hosted_control_plane_version:in_progress:stuck_over_30m:ratio[5m]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:burnrate30m'
        expression: 'round(avg_over_time(hosted_control_plane_version:in_progress:stuck_over_30m:ratio[30m]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:burnrate1h'
        expression: 'round(avg_over_time(hosted_control_plane_version:in_progress:stuck_over_30m:ratio[1h]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:burnrate6h'
        expression: 'round(avg_over_time(hosted_control_plane_version:in_progress:stuck_over_30m:ratio[6h]) / (1 - 0.9995), 0.01)'
      }
      {
        record: 'hosted_control_plane_version:in_progress:stuck_over_30m:burnrate3d'
        expression: 'round(avg_over_time(hosted_control_plane_version:in_progress:stuck_over_30m:ratio[3d]) / (1 - 0.9995), 0.01)'
      }
    ]
  }
}
