#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource prometheusWipRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'prometheus-wip-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusJobUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusJobUp/{{ $labels.cluster }}'
          description: '''Prometheus has not been reachable for the past 10 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          info: '''Prometheus has not been reachable for the past 10 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 10 minutes.'
          title: 'Prometheus is unreachable for 10 minutes.'
        }
        expression: 'group by (cluster) (up{job="kube-state-metrics"}) unless on(cluster) group by (cluster) (up{job="prometheus/prometheus",namespace="prometheus"} == 1)'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusUptime'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusUptime/{{ $labels.cluster }}'
          description: '''Prometheus has been unreachable for more than 5% of the time over the past 24 hours.
This may indicate that the Prometheus server is down, experiencing network issues, or stuck in a crash loop.
Please check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          info: '''Prometheus has been unreachable for more than 5% of the time over the past 24 hours.
This may indicate that the Prometheus server is down, experiencing network issues, or stuck in a crash loop.
Please check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 1 day.'
          title: 'Prometheus is unreachable for 1 day.'
        }
        expression: 'avg by (job, namespace, cluster) (avg_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d])) < 0.95'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusPendingRate'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusPendingRate/{{ $labels.cluster }}'
          description: '''The pending sample rate of Prometheus remote storage is above 40% for the last 15 minutes.
This means that more than 40% of samples are waiting to be sent to remote storage, which may indicate
a bottleneck or issue with the remote write endpoint, network connectivity, or Prometheus performance.
If this condition persists, it could lead to increased memory usage and potential data loss if the buffer overflows.
Investigate the health and performance of the remote storage endpoint, network latency, and Prometheus resource utilization.
'''
          info: '''The pending sample rate of Prometheus remote storage is above 40% for the last 15 minutes.
This means that more than 40% of samples are waiting to be sent to remote storage, which may indicate
a bottleneck or issue with the remote write endpoint, network connectivity, or Prometheus performance.
If this condition persists, it could lead to increased memory usage and potential data loss if the buffer overflows.
Investigate the health and performance of the remote storage endpoint, network latency, and Prometheus resource utilization.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus pending sample rate is above 40%.'
          title: 'Prometheus pending sample rate is above 40%.'
        }
        expression: '( prometheus_remote_storage_samples_pending / prometheus_remote_storage_samples_in_flight ) > 0.4'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusFailedRate'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusFailedRate/{{ $labels.cluster }}'
          description: '''The failed sample rate for Prometheus remote storage has exceeded 10% over the past 15 minutes.
This indicates that more than 10% of samples are not being successfully sent to remote storage, which could be caused by
issues with the remote write endpoint, network instability, or Prometheus resource constraints.
Persistent failures may result in increased memory usage and potential data loss if the buffer overflows.
Please check the health and performance of the remote storage endpoint, network connectivity, and Prometheus resource utilization.
'''
          info: '''The failed sample rate for Prometheus remote storage has exceeded 10% over the past 15 minutes.
This indicates that more than 10% of samples are not being successfully sent to remote storage, which could be caused by
issues with the remote write endpoint, network instability, or Prometheus resource constraints.
Persistent failures may result in increased memory usage and potential data loss if the buffer overflows.
Please check the health and performance of the remote storage endpoint, network connectivity, and Prometheus resource utilization.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus failed sample rate to remote storage is above 10%.'
          title: 'Prometheus failed sample rate to remote storage is above 10%.'
        }
        expression: '( rate(prometheus_remote_storage_samples_failed_total[5m]) / rate(prometheus_remote_storage_samples_total[5m]) ) > 0.1'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource prometheusRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'prometheus-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusRemoteStorageFailures'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusRemoteStorageFailures/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.remote_name }}/{{ $labels.url }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} failed to send {{ printf "%.1f" $value }}% of the samples to {{ $labels.remote_name}}:{{ $labels.url }}'
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} failed to send {{ printf "%.1f" $value }}% of the samples to {{ $labels.remote_name}}:{{ $labels.url }}'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusremotestoragefailures'
          summary: 'Prometheus fails to send samples to remote storage.'
          title: 'Prometheus fails to send samples to remote storage.'
        }
        expression: '((rate(prometheus_remote_storage_failed_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) / ((rate(prometheus_remote_storage_failed_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) + (rate(prometheus_remote_storage_succeeded_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m])))) * 100 > 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusNotIngestingSamples'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusNotIngestingSamples/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} is not ingesting samples.'
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} is not ingesting samples.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusnotingestingsamples'
          summary: 'Prometheus is not ingesting samples.'
          title: 'Prometheus is not ingesting samples.'
        }
        expression: '(sum without (type) (rate(prometheus_tsdb_head_samples_appended_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) <= 0 and sum without (scrape_job) (prometheus_target_metadata_cache_entries{job="prometheus/prometheus",namespace="prometheus"}) > 0)'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusBadConfig'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusBadConfig/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to reload its configuration.'
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to reload its configuration.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusbadconfig'
          summary: 'Failed Prometheus configuration reload.'
          title: 'Failed Prometheus configuration reload.'
        }
        expression: 'max_over_time(prometheus_config_last_reload_successful{job="prometheus/prometheus",namespace="prometheus"}[5m]) == 0'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusScrapeSampleLimitHit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusScrapeSampleLimitHit/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusscrapesamplelimithit'
          summary: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
          title: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
        }
        expression: 'increase(prometheus_target_scrapes_exceeded_sample_limit_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) > 0'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource prometheusOperatorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'prometheus-operator-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusOperatorNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusOperatorNotReady/{{ $labels.cluster }}/{{ $labels.controller }}/{{ $labels.namespace }}'
          description: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
          info: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatornotready'
          summary: 'Prometheus operator not ready'
          title: 'Prometheus operator not ready'
        }
        expression: 'min by (cluster, controller, namespace) (max_over_time(prometheus_operator_ready{job="prometheus-operator",namespace="prometheus"}[5m])) == 0'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'PrometheusOperatorRejectedResources'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusOperatorRejectedResources/{{ $labels.cluster }}/{{ $labels.controller }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
          info: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatorrejectedresources'
          summary: 'Resources rejected by Prometheus operator'
          title: 'Resources rejected by Prometheus operator'
        }
        expression: 'min_over_time(prometheus_operator_managed_resources{job="prometheus-operator",namespace="prometheus",state="rejected"}[5m]) > 0'
        for: 'PT20M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource frontend 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'frontend'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FrontendLatency'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendLatency/{{ $labels.cluster }}'
          description: 'The 95th percentile of frontend request latency has exceeded 5 seconds over the past hour.'
          info: 'The 95th percentile of frontend request latency has exceeded 5 seconds over the past hour.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend latency is high: 95th percentile exceeds 5 seconds'
          title: 'Frontend latency is high: 95th percentile exceeds 5 seconds'
        }
        expression: 'histogram_quantile(0.95, rate(frontend_http_requests_duration_seconds_bucket[1h])) > 5'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FrontendClusterServiceErrorRate'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendClusterServiceErrorRate/{{ $labels.cluster }}'
          description: 'The Frontend Cluster Service 5xx error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          info: 'The Frontend Cluster Service 5xx error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'High 4xx|5xx Error Rate on Frontend Cluster Service'
          title: 'High 4xx|5xx Error Rate on Frontend Cluster Service'
        }
        expression: '(sum by (cluster) (max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count{code=~"4..|5.."}[1h])))) / (sum by (cluster) (max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count[1h])))) > 0.05'
        for: 'PT5M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FrontendHighAuditLogErrorRate'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendHighAuditLogErrorRate/{{ $labels.cluster }}'
          description: 'Audit log error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          info: 'Audit log error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'High Frontend audit log error rate.'
          title: 'High Frontend audit log error rate.'
        }
        expression: '( sum by (cluster) (rate(otel_audit_log_send_errors_total{job="aro-hcp-frontend-metrics"}[1h])) / sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-frontend-metrics"}[1h])) ) > 0.05'
        for: 'PT5M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FrontendAuditLogConnectionDegraded'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendAuditLogConnectionDegraded/{{ $labels.cluster }}'
          description: 'The frontend failed to connect to the audit server and is running with a no-op audit client. No audit logs are being sent.'
          info: 'The frontend failed to connect to the audit server and is running with a no-op audit client. No audit logs are being sent.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend audit log connection is degraded.'
          title: 'Frontend audit log connection is degraded.'
        }
        expression: 'otel_audit_log_connection_degraded{job="aro-hcp-frontend-metrics"} == 1'
        for: 'PT5M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpCsSloAvailabilityAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cs_slo_availability_alerts'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPIAvailability5mto1hor30mto6hErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'warning'
          short: '30m'
        }
        annotations: {
          correlationId: 'ClustersServiceAPIAvailability5mto1hor30mto6hErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'API is rapidly burning its 28 day availability error budget (99% SLO)'
          info: 'API is rapidly burning its 28 day availability error budget (99% SLO)'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API availability error budget burn rate is too high'
          title: 'Cluster Service API availability error budget burn rate is too high'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.99)) ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.99)) )'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPIAvailability6hto3dErrorBudgetBurn'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'api-availability'
        }
        annotations: {
          correlationId: 'ClustersServiceAPIAvailability6hto3dErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          info: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day availability error budget (99% SLO)'
          title: 'API is slowly but steadily burning its 28 day availability error budget (99% SLO)'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.99))'
        for: 'PT30M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPILatency5mto1hor30mto6hP99ErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'warning'
          short: '30m'
          slo: 'api-latency-p99'
        }
        annotations: {
          correlationId: 'ClustersServiceAPILatency5mto1hor30mto6hP99ErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'API is rapidly burning its 28 day 1s latency error budget (99% SLO)'
          info: 'API is rapidly burning its 28 day 1s latency error budget (99% SLO)'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API P99 latency error budget burn rate is too high'
          title: 'Cluster Service API P99 latency error budget burn rate is too high'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.99)) ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.99)) )'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPILatency6hto3dP99ErrorBudgetBurn'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'api-latency-p99'
        }
        annotations: {
          correlationId: 'ClustersServiceAPILatency6hto3dP99ErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          info: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day 1s latency error budget (99% SLO)'
          title: 'API is slowly but steadily burning its 28 day 1s latency error budget (99% SLO)'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.99)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.99))'
        for: 'PT30M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPILatency5mto1hor30mto6hP90ErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'warning'
          short: '30m'
          slo: 'api-latency-p90'
        }
        annotations: {
          correlationId: 'ClustersServiceAPILatency5mto1hor30mto6hP90ErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'API is rapidly burning its 28 day 0.1s latency error budget (90% SLO)'
          info: 'API is rapidly burning its 28 day 0.1s latency error budget (90% SLO)'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API P90 latency error budget burn rate is too high'
          title: 'Cluster Service API P90 latency error budget burn rate is too high'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.90)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1 - 0.90)) ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.90)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1 - 0.90)) )'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ClustersServiceAPILatency6hto3dP90ErrorBudgetBurn'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'api-latency-p90'
        }
        annotations: {
          correlationId: 'ClustersServiceAPILatency6hto3dP90ErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          info: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day 0.1s latency error budget (90% SLO)'
          title: 'API is slowly but steadily burning its 28 day 0.1s latency error budget (90% SLO)'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.90)) and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > (0.934 * (1 - 0.90))'
        for: 'PT30M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource backend 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'backend'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'BackendControllerRetryHotLoop'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'BackendControllerRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Backend controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Backend controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Backend controller workqueue {{ $labels.name }} retry hot loop'
          title: 'Backend controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace="aro-hcp"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace="aro-hcp"}[10m]) ) ) ) > 0.5'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'BackendControllerQueueDepthHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'BackendControllerQueueDepthHigh/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Backend controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Backend controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Backend controller workqueue {{ $labels.name }} depth is high'
          title: 'Backend controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) ( max without(prometheus_replica) ( workqueue_depth{namespace="aro-hcp"} ) ) > 10'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'BackendControllerPanic'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'BackendControllerPanic/{{ $labels.cluster }}/{{ $labels.controller }}'
          description: 'Backend controller {{ $labels.controller }} has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          info: 'Backend controller {{ $labels.controller }} has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Backend controller {{ $labels.controller }} is panicking'
          title: 'Backend controller {{ $labels.controller }} is panicking'
        }
        expression: 'sum by (controller, cluster) ( increase(panic_total{namespace="aro-hcp"}[5m]) ) > 0'
        for: 'PT1M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource adminApi 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'admin-api'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AdminHighAuditLogErrorRate'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'AdminHighAuditLogErrorRate/{{ $labels.cluster }}'
          description: 'Audit log error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          info: 'Audit log error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'TBD'
          summary: 'High Admin API audit log error rate.'
          title: 'High Admin API audit log error rate.'
        }
        expression: '( sum by (cluster) (rate(otel_audit_log_send_errors_total{job="aro-hcp-admin-api-metrics"}[1h])) / sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-admin-api-metrics"}[1h])) ) > 0.05'
        for: 'PT5M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AdminAuditLogConnectionDegraded'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'AdminAuditLogConnectionDegraded/{{ $labels.cluster }}'
          description: 'The admin API failed to connect to the audit server and is running with a no-op audit client. No audit logs are being sent.'
          info: 'The admin API failed to connect to the audit server and is running with a no-op audit client. No audit logs are being sent.'
          runbook_url: 'TBD'
          summary: 'Admin API audit log connection is degraded.'
          title: 'Admin API audit log connection is degraded.'
        }
        expression: 'otel_audit_log_connection_degraded{job="aro-hcp-admin-api-metrics"} == 1'
        for: 'PT5M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource maestro 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'maestro'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'MaestroGRPCSourceClientExcessConnections'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'MaestroGRPCSourceClientExcessConnections/{{ $labels.cluster }}'
          description: 'Maestro gRPC server has {{ $value }} registered source clients, which is unusually high. Only clusters-service and backend are expected as source clients. This may indicate a connection leak or clients failing to unregister.'
          info: 'Maestro gRPC server has {{ $value }} registered source clients, which is unusually high. Only clusters-service and backend are expected as source clients. This may indicate a connection leak or clients failing to unregister.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'Maestro has too many gRPC source client connections'
          title: 'Maestro has too many gRPC source client connections'
        }
        expression: 'sum(grpc_server_registered_source_clients{namespace="maestro"}) > 100'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'MaestroRESTAPIErrorRate'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'MaestroRESTAPIErrorRate/{{ $labels.cluster }}'
          description: 'Maestro REST API 5xx error rate is above 5% for the last 5 minutes. Current value: {{ $value | humanizePercentage }}.'
          info: 'Maestro REST API 5xx error rate is above 5% for the last 5 minutes. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'Maestro REST API error rate is high'
          title: 'Maestro REST API error rate is high'
        }
        expression: 'sum(rate(rest_api_inbound_request_count{namespace="maestro", code=~"5.."}[5m])) / sum(rate(rest_api_inbound_request_count{namespace="maestro"}[5m])) > 0.05'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'MaestroGRPCServerErrorRate'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'MaestroGRPCServerErrorRate/{{ $labels.cluster }}'
          description: 'Maestro gRPC server error rate is above 5% for the last 5 minutes. Current value: {{ $value | humanizePercentage }}.'
          info: 'Maestro gRPC server error rate is above 5% for the last 5 minutes. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'Maestro gRPC server error rate is high'
          title: 'Maestro gRPC server error rate is high'
        }
        expression: 'sum(rate(grpc_server_processed_total{namespace="maestro", code!="OK"}[5m])) / sum(rate(grpc_server_processed_total{namespace="maestro"}[5m])) > 0.05'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'MaestroSpecControllerReconcileErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'MaestroSpecControllerReconcileErrors/{{ $labels.cluster }}'
          description: 'Maestro spec controller reconcile error rate is above 10% for the last 10 minutes. Resources may not be reaching management clusters. Current value: {{ $value | humanizePercentage }}.'
          info: 'Maestro spec controller reconcile error rate is above 10% for the last 10 minutes. Resources may not be reaching management clusters. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'Maestro spec controller reconcile error rate is high'
          title: 'Maestro spec controller reconcile error rate is high'
        }
        expression: 'sum(rate(spec_controller_event_reconcile_total{namespace="maestro", status="error"}[5m])) / sum(rate(spec_controller_event_reconcile_total{namespace="maestro"}[5m])) > 0.1'
        for: 'PT10M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arobitRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arobit-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'ArobitForwarderJobUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'ArobitForwarderJobUp/{{ $labels.cluster }}'
          description: '''The Arobit forwarder metrics endpoint on cluster {{ $labels.cluster }} has been unreachable for 15 minutes.
'''
          info: '''The Arobit forwarder metrics endpoint on cluster {{ $labels.cluster }} has been unreachable for 15 minutes.
'''
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/arobit.html'
          summary: 'Arobit forwarder metrics endpoint is unreachable on cluster {{ $labels.cluster }}.'
          title: 'Arobit forwarder metrics endpoint is unreachable on cluster {{ $labels.cluster }}.'
        }
        expression: 'group by (cluster) (up{job="kube-state-metrics"}) unless on(cluster) group by (cluster) (up{job="arobit-forwarder",namespace="arobit"} == 1)'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FluentBitIngestionPaused'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FluentBitIngestionPaused/{{ $labels.cluster }}/{{ $labels.pod }}'
          description: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} has paused collecting new log data for at least 5 minutes.
Ingestion pauses when Fluent Bit\'s internal memory or storage buffers are full, typically caused by backpressure from a slow or failing output.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          info: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} has paused collecting new log data for at least 5 minutes.
Ingestion pauses when Fluent Bit\'s internal memory or storage buffers are full, typically caused by backpressure from a slow or failing output.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/arobit.html'
          summary: 'Fluent Bit input ingestion paused due to backpressure.'
          title: 'Fluent Bit input ingestion paused due to backpressure.'
        }
        expression: 'sum(fluentbit_input_ingestion_paused) by (cluster, pod) > 0'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FluentBitHighOutputRetries'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FluentBitHighOutputRetries/{{ $labels.cluster }}/{{ $labels.pod }}'
          description: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} is retrying chunk delivery.
Retries occur when the azure_kusto output encounters a recoverable error (e.g. transient network failure, HTTP 429/5xx from Kusto).
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          info: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} is retrying chunk delivery.
Retries occur when the azure_kusto output encounters a recoverable error (e.g. transient network failure, HTTP 429/5xx from Kusto).
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/arobit.html'
          summary: 'High Kusto output retries'
          title: 'High Kusto output retries'
        }
        expression: 'sum(increase(fluentbit_output_retries_total{name=~"azure_kusto.*"}[5m])) by (cluster, pod) > 3'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FluentBitOutputErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FluentBitOutputErrors/{{ $labels.cluster }}/{{ $labels.pod }}'
          description: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} is encountering errors sending log chunks to Kusto.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          info: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} is encountering errors sending log chunks to Kusto.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/arobit.html'
          summary: 'Unrecoverable Kusto output errors - log data is being dropped.'
          title: 'Unrecoverable Kusto output errors - log data is being dropped.'
        }
        expression: 'sum(increase(fluentbit_output_errors_total{name=~"azure_kusto.*"}[5m])) by (cluster, pod) > 0'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'FluentBitOutputRetriesExhausted'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FluentBitOutputRetriesExhausted/{{ $labels.cluster }}/{{ $labels.pod }}'
          description: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} has chunks that exceeded the configured Retry_Limit for the Kusto output.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          info: '''Fluent Bit pod {{ $labels.pod }} on cluster {{ $labels.cluster }} has chunks that exceeded the configured Retry_Limit for the Kusto output.
Investigate the Fluent Bit logs for the specific error details and check the Kusto instance health.
'''
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/arobit.html'
          summary: 'Kusto output retries exhausted - chunks discarded after max retries.'
          title: 'Kusto output retries exhausted - chunks discarded after max retries.'
        }
        expression: 'sum(increase(fluentbit_output_retries_failed_total{name=~"azure_kusto.*"}[5m])) by (cluster, pod) > 0'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource serviceTagCapacityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'service-tag-capacity-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpNonprodInboundCustomerapiCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpNonprodInboundCustomerapiCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-inbound-customerapi"} / 32 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpNonprodInboundSvcCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpNonprodInboundSvcCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-inbound-svc"} / 18 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpNonprodOutboundCxCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpNonprodOutboundCxCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-outbound-cx"} / 8 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpNonprodOutboundSvcCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpNonprodOutboundSvcCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-outbound-svc"} / 18 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdInboundCustomerapiCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdInboundCustomerapiCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-customerapi",region!="uswest2"} / 64 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdInboundCustomerapiUswest2Capacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdInboundCustomerapiUswest2Capacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-customerapi",region="uswest2"} / 128 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdInboundCxCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdInboundCxCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-cx"} / 32 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdInboundSvcCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdInboundSvcCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-svc"} / 32 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdOutboundCxCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdOutboundCxCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-outbound-cx"} / 32 > 0.8'
        for: 'PT15M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'AroHcpProdOutboundSvcCapacity'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'AroHcpProdOutboundSvcCapacity/{{ $labels.cluster }}'
          description: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          info: 'Service Tag IP Usage {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its capacity. Current count exceeds warning threshold of 80%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/serviceiptagusage'
          summary: 'Service Tag IP Usage is reaching 80%'
          title: 'Service Tag IP Usage is reaching 80%'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-outbound-svc"} / 32 > 0.8'
        for: 'PT15M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hcpDeletionRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-deletion-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'HCPClusterStuckDeleting'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'HCPClusterStuckDeleting/{{ $labels.cluster }}/{{ $labels.exported_namespace }}'
          description: '''Cluster {{ $labels.exported_namespace }} has been in a deleting state for more than 2 hours. 
This may indicate that finalizers are stuck or resources are failing to cleanup.
'''
          info: '''Cluster {{ $labels.exported_namespace }} has been in a deleting state for more than 2 hours. 
This may indicate that finalizers are stuck or resources are failing to cleanup.
'''
          runbook_url: 'TBD'
          summary: 'Cluster stuck deleting'
          title: 'Cluster stuck deleting'
        }
        expression: 'sum by (cluster, exported_namespace, name) (hypershift_cluster_deleting_duration_seconds) > 7200'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubeContainerOomRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kube-container-oom-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'KubeContainerOOMKilled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeContainerOOMKilled/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Container {{ $labels.container }} in pod {{ $labels.namespace }}/{{ $labels.pod }} on cluster {{ $labels.cluster }} has been OOMKilled. This indicates the container exceeded its memory limit and was terminated by the kernel.'
          info: 'Container {{ $labels.container }} in pod {{ $labels.namespace }}/{{ $labels.pod }} on cluster {{ $labels.cluster }} has been OOMKilled. This indicates the container exceeded its memory limit and was terminated by the kernel.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/service-lifecycle.html'
          summary: 'Container {{ $labels.container }} was OOMKilled'
          title: 'Container {{ $labels.container }} was OOMKilled'
        }
        expression: 'kube_pod_container_status_last_terminated_reason{reason="OOMKilled", job="kube-state-metrics"} == 1'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubeNodeRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kube-node-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'KubeMemoryPressure'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryPressure/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Node {{ $labels.node }} is reporting MemoryPressure condition'
          info: 'Node {{ $labels.node }} is reporting MemoryPressure condition'
          summary: 'Node under memory pressure'
          title: 'Node under memory pressure'
        }
        expression: 'kube_node_status_condition{condition="MemoryPressure",status="true"} == 1'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kustoLogsAgeRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kusto-logs-age-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'KustoLogsDataStale'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KustoLogsDataStale/{{ $labels.cluster }}/{{ $labels.kusto_cluster }}/{{ $labels.table }}'
          description: '''Kusto log data for table {{ $labels.table }} on cluster {{ $labels.cluster }} (Kusto cluster {{ $labels.kusto_cluster }}) is stale. Check the ingestion pipeline.
'''
          info: '''Kusto log data for table {{ $labels.table }} on cluster {{ $labels.cluster }} (Kusto cluster {{ $labels.kusto_cluster }}) is stale. Check the ingestion pipeline.
'''
          runbook_url: 'TBD'
          summary: 'Kusto log data is stale for {{ $labels.table }} on {{ $labels.cluster }}.'
          title: 'Kusto log data is stale for {{ $labels.table }} on {{ $labels.cluster }}.'
        }
        expression: 'kusto_logs_age_in_seconds > 3600'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
