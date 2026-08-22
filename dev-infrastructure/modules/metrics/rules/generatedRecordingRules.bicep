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
        expression: 'min without (prometheus_replica) (min by (cluster, resource_id, subscription_id, cluster_uuid, version, state) ((hosted_control_plane_upgrade:version_state_first_seen:timestamp or (timestamp(backend_cluster_version_info{state=~"desired|partial"} == 1) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1))) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1))))'
      }
      {
        record: 'hosted_control_plane_upgrade:in_progress:count'
        expression: 'count by (cluster) (count by (cluster, resource_id) ((max by (cluster, resource_id, subscription_id, cluster_uuid, version, state) (backend_cluster_version_info{state=~"desired|partial"} == 1) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) max by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1))) >= 1 and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)) or 0 * count by (cluster) (backend_cluster_version_info)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_desired:seconds'
        expression: '(time() - hosted_control_plane_upgrade:version_state_first_seen:timestamp{state="desired"}) and on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, state) (backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="partial"} == 1 or backend_cluster_version_info{state="completed"} == 1)))) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1)'
      }
      {
        record: 'hosted_control_plane_upgrade:duration_in_progress:seconds'
        expression: '((time() - min without (state) (hosted_control_plane_upgrade:version_state_first_seen:timestamp{state=~"desired|partial"})) and on (cluster, resource_id) (hosted_control_plane_upgrade:upgrade_eligible:info == 1) unless on (cluster, resource_id, subscription_id, cluster_uuid, version) (max by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="completed"} == 1))) * on (cluster, resource_id, subscription_id, cluster_uuid, version) group_left (state) (max by (cluster, resource_id, subscription_id, cluster_uuid, version, state) (backend_cluster_version_info{state="partial"} == 1 or (backend_cluster_version_info{state="desired"} == 1 unless on (cluster, resource_id, subscription_id, cluster_uuid, version) max by (cluster, resource_id, subscription_id, cluster_uuid, version) (backend_cluster_version_info{state="partial"} == 1))))'
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
        expression: '((sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{code!~"5..",route!~".*hcpoperation(results|statuses).*"}[5m]))) or 0 * sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) / sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'sli:frontend_http:availability:rate_avg_30d'
        expression: 'avg_over_time(sli:frontend_http:availability:rate5m[30d:5m])'
      }
      {
        record: 'errors:frontend_http:error_rate:rate5m'
        expression: '((sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{code=~"5..",route!~".*hcpoperation(results|statuses).*"}[5m]))) or 0 * sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) / sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'sli:frontend_http:latency_p99:rate5m'
        expression: 'histogram_quantile(0.99, sum by (cluster, le) (max without (prometheus_replica) (rate(frontend_http_requests_duration_seconds_bucket{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_duration_seconds_count{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'sli:frontend_http:latency_p95:rate5m'
        expression: 'histogram_quantile(0.95, sum by (cluster, le) (max without (prometheus_replica) (rate(frontend_http_requests_duration_seconds_bucket{route!~".*hcpoperation(results|statuses).*"}[5m])))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_duration_seconds_count{route!~".*hcpoperation(results|statuses).*"}[5m]))) > 0)'
      }
      {
        record: 'traffic:frontend_http:request_rate:rate5m'
        expression: 'sum by (cluster) (max without (prometheus_replica) (rate(frontend_http_requests_total{route!~".*hcpoperation(results|statuses).*"}[5m])))'
      }
      {
        record: 'sli:frontend:ready:ratio5m'
        expression: '(sum by (cluster) (max without (prometheus_replica) (kube_deployment_status_replicas_available{deployment="aro-hcp-frontend",namespace="aro-hcp"})) / sum by (cluster) (max without (prometheus_replica) (kube_deployment_spec_replicas{deployment="aro-hcp-frontend",namespace="aro-hcp"}))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (kube_deployment_spec_replicas{deployment="aro-hcp-frontend",namespace="aro-hcp"})) > 0)'
      }
      {
        record: 'sli:frontend:saturation_cpu:ratio5m'
        expression: '(sum by (cluster) (max without (prometheus_replica) (rate(container_cpu_usage_seconds_total{container="aro-hcp-frontend",namespace="aro-hcp"}[5m]))) / sum by (cluster) (max without (prometheus_replica) (kube_pod_container_resource_requests{container="aro-hcp-frontend",namespace="aro-hcp",resource="cpu"}))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (kube_pod_container_resource_requests{container="aro-hcp-frontend",namespace="aro-hcp",resource="cpu"})) > 0)'
      }
      {
        record: 'sli:frontend:saturation_memory:ratio5m'
        expression: '(sum by (cluster) (max without (prometheus_replica) (container_memory_working_set_bytes{container="aro-hcp-frontend",namespace="aro-hcp"})) / sum by (cluster) (max without (prometheus_replica) (kube_pod_container_resource_limits{container="aro-hcp-frontend",namespace="aro-hcp",resource="memory"}))) and on (cluster) (sum by (cluster) (max without (prometheus_replica) (kube_pod_container_resource_limits{container="aro-hcp-frontend",namespace="aro-hcp",resource="memory"})) > 0)'
      }
    ]
  }
}
