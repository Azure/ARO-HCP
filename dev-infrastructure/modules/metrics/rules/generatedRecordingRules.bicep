param azureMonitoring string

param location string = resourceGroup().location

resource hcpKmsRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-kms-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_30d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_7d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1w])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_1d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_3d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_2h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_2h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_15m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[15m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_15m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[15m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[6h:5m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[6h:5m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[3d:30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[3d:30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
    ]
  }
}

resource userjourneyKubeapiserverAvailabilityRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'userjourney-kubeapiserver-availability-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_5m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[5m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_5m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[5m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[1h:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[1h:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[30m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[30m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
    ]
  }
}

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
        expression: 'count by (cluster, region) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_credential_operation:terminal_total'
        expression: 'count by (cluster, region) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})'
      }
      {
        record: 'errors:backend_credential_operation:error_rate'
        expression: '(count by (cluster, region) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) or 0 * count by (cluster, region) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) / clamp_min(count by (cluster, region) (backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"succeeded|failed|canceled",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}), 1)'
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
        expression: 'count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))'
      }
      {
        record: 'errors:backend_cluster_provision:terminal_total'
        expression: 'count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))'
      }
      {
        record: 'errors:backend_cluster_provision:error_rate'
        expression: '(count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) or 0 * count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))) / clamp_min(count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})), 1)'
      }
    ]
  }
}

resource arohcpClusterProvisionSloWindowedRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_provision_slo_windowed_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'errors:backend_cluster_provision:failed_1h'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 3600)) or 0 * count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))'
      }
      {
        record: 'errors:backend_cluster_provision:total_1h'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 3600))'
      }
      {
        record: 'errors:backend_cluster_provision:error_rate_1h'
        expression: 'errors:backend_cluster_provision:failed_1h / clamp_min(errors:backend_cluster_provision:total_1h, 1)'
      }
      {
        record: 'errors:backend_cluster_provision:failed_6h'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 21600)) or 0 * count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))'
      }
      {
        record: 'errors:backend_cluster_provision:total_6h'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 21600))'
      }
      {
        record: 'errors:backend_cluster_provision:error_rate_6h'
        expression: 'errors:backend_cluster_provision:failed_6h / clamp_min(errors:backend_cluster_provision:total_6h, 1)'
      }
      {
        record: 'errors:backend_cluster_provision:failed_3d'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase="failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 259200)) or 0 * count by (cluster, region) (max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}))'
      }
      {
        record: 'errors:backend_cluster_provision:total_3d'
        expression: 'count by (cluster, region) ((max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_phase_info{operation_type="create",phase=~"succeeded|failed",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) and ((time() - max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (backend_resource_operation_last_transition_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"})) < 259200))'
      }
      {
        record: 'errors:backend_cluster_provision:error_rate_3d'
        expression: 'errors:backend_cluster_provision:failed_3d / clamp_min(errors:backend_cluster_provision:total_3d, 1)'
      }
    ]
  }
}

resource arohcpClusterProvisionLatencyRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_provision_latency_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'latency:backend_cluster_provision:inflight_duration_seconds'
        expression: 'max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) ((time() - backend_resource_operation_start_time_seconds{operation_type="create",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) and backend_resource_operation_phase_info{operation_type="create",phase=~"accepted|provisioning",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1)'
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
        expression: '((count by (cluster, resource_id, subscription_id, cluster_uuid, region) (count by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info == 1)) >= 2) and on (cluster, resource_id) (count by (cluster, resource_id, region) (backend_cluster_version_info{state="completed"} == 1) >= 1)) * 0 + 1'
      }
      {
        record: 'hosted_control_plane_upgrade:version_state_first_seen:timestamp'
        expression: 'min without (prometheus_replica) (min by (cluster, resource_id, subscription_id, cluster_uuid, version, state, region) ((hosted_control_plane_upgrade:version_state_first_seen:timestamp or (timestamp(backend_cluster_version_info{state=~"desired|partial"} == 1) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1))) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info{state="completed"} == 1))))'
      }
      {
        record: 'hosted_control_plane_upgrade:in_progress:count'
        expression: 'count by (cluster, region) (count by (cluster, resource_id, region) ((max by (cluster, resource_id, subscription_id, cluster_uuid, version, state, region) (backend_cluster_version_info{state=~"desired|partial"} == 1) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) max by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info{state="completed"} == 1))) >= 1 and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)) or 0 * count by (cluster, region) (backend_cluster_version_info)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_desired:seconds'
        expression: '(time() - hosted_control_plane_upgrade:version_state_first_seen:timestamp{state="desired"}) and on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, state, region) (backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info{state="partial"} == 1 or backend_cluster_version_info{state="completed"} == 1)))) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_progress:seconds'
        expression: '((time() - min without (state) (hosted_control_plane_upgrade:version_state_first_seen:timestamp{state=~"desired|partial"})) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info{state="completed"} == 1))) * on (cluster, resource_id, subscription_id, cluster_uuid, version) group_left (state) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, state, region) (backend_cluster_version_info{state="partial"} == 1 or (backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) max by (cluster, resource_id, subscription_id, cluster_uuid, version, region) (backend_cluster_version_info{state="partial"} == 1))))'
      }
    ]
  }
}

resource arohcpFrontendSloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_frontend_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'sli:frontend_http:availability:rate5m'
        expression: '((sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{code!~"5..",route!~".*hcpoperation(results|statuses).*"}[5m]))) or 0 * sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) / sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'sli:frontend_http:good:rate5m'
        expression: '(sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{code!~"5..",route!~".*hcpoperation(results|statuses).*"}[5m]))) or 0 * sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m]))))'
      }
      {
        record: 'errors:frontend_http:error_rate:rate5m'
        expression: '((sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{code=~"5..",route!~".*hcpoperation(results|statuses).*"}[5m]))) or 0 * sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) / sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'sli:frontend_http:latency_p99:rate5m'
        expression: 'histogram_quantile(0.99, sum by (cluster, le, region) (rate(frontend_http_requests_duration_seconds_bucket{route!~".*hcpoperation(results|statuses).*"}[5m] offset 5m))) and on (cluster) (sum by (cluster, region) (rate(frontend_http_requests_duration_seconds_count{route!~".*hcpoperation(results|statuses).*"}[5m] offset 5m)) > 0)'
      }
      {
        record: 'sli:frontend_http:latency_p95:rate5m'
        expression: 'histogram_quantile(0.95, sum by (cluster, le, region) (rate(frontend_http_requests_duration_seconds_bucket{route!~".*hcpoperation(results|statuses).*"}[5m] offset 5m))) and on (cluster) (sum by (cluster, region) (rate(frontend_http_requests_duration_seconds_count{route!~".*hcpoperation(results|statuses).*"}[5m] offset 5m)) > 0)'
      }
      {
        record: 'traffic:frontend_http:request_rate:rate5m'
        expression: 'sum by (cluster, region) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))'
      }
      {
        record: 'sli:frontend_http:availability:rate_avg_30d'
        expression: '(avg_over_time(sli:frontend_http:good:rate5m[30d:5m]) / avg_over_time(traffic:frontend_http:request_rate:rate5m[30d:5m])) and avg_over_time(traffic:frontend_http:request_rate:rate5m[30d:5m]) > 0'
      }
      {
        record: 'sli:frontend:ready:ratio5m'
        expression: '(sum by (cluster, region) (max without (prometheus_replica) (kube_deployment_status_replicas_available{deployment="aro-hcp-frontend",namespace="aro-hcp"})) / sum by (cluster, region) (max without (prometheus_replica) (kube_deployment_spec_replicas{deployment="aro-hcp-frontend",namespace="aro-hcp"}))) and on (cluster) (sum by (cluster, region) (max without (prometheus_replica) (kube_deployment_spec_replicas{deployment="aro-hcp-frontend",namespace="aro-hcp"})) > 0)'
      }
      {
        record: 'sli:frontend:saturation_cpu:ratio5m'
        expression: '(sum by (cluster, region) (max without (prometheus_replica) (rate(container_cpu_usage_seconds_total{container="aro-hcp-frontend",namespace="aro-hcp"}[5m]))) / sum by (cluster, region) (max by (cluster, namespace, pod, container, region) (kube_pod_container_resource_requests{container="aro-hcp-frontend",job="kube-state-metrics",namespace="aro-hcp",resource="cpu"}))) and on (cluster) (sum by (cluster, region) (max by (cluster, namespace, pod, container, region) (kube_pod_container_resource_requests{container="aro-hcp-frontend",job="kube-state-metrics",namespace="aro-hcp",resource="cpu"})) > 0)'
      }
      {
        record: 'sli:frontend:saturation_memory:ratio5m'
        expression: '(sum by (cluster, region) (max without (prometheus_replica) (container_memory_working_set_bytes{container="aro-hcp-frontend",namespace="aro-hcp"})) / sum by (cluster, region) (max by (cluster, namespace, pod, container, region) (kube_pod_container_resource_limits{container="aro-hcp-frontend",job="kube-state-metrics",namespace="aro-hcp",resource="memory"}))) and on (cluster) (sum by (cluster, region) (max by (cluster, namespace, pod, container, region) (kube_pod_container_resource_limits{container="aro-hcp-frontend",job="kube-state-metrics",namespace="aro-hcp",resource="memory"})) > 0)'
      }
    ]
  }
}
