#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus is unreachable for 10 minutes.'
          title: 'Prometheus is unreachable for 10 minutes.'
        }
        expression: 'group by (cluster) (up{job="kubelet"}) unless on (cluster) group by (cluster) (up{job="prometheus/prometheus",namespace="prometheus"} == 1)'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus is unreachable for 1 day.'
          title: 'Prometheus is unreachable for 1 day.'
        }
        expression: 'avg by (job, namespace, cluster) (avg_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d])) < 0.95'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
        alert: 'PrometheusUptimeSampleCount'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusUptimeSampleCount/{{ $labels.cluster }}'
          description: '''Prometheus has delivered fewer than 95% of expected samples in the past 24 hours (expected 2880 at 30s interval, threshold 2736).
Unlike PrometheusUptime which averages existing samples, this alert treats data gaps as downtime.
Complete metric absence (no samples) will also trigger PrometheusMetricsAbsentPerCluster.
Check the PrometheusAgent pod status, remote write pipeline, and PodMonitor configuration.
'''
          info: '''Prometheus has delivered fewer than 95% of expected samples in the past 24 hours (expected 2880 at 30s interval, threshold 2736).
Unlike PrometheusUptime which averages existing samples, this alert treats data gaps as downtime.
Complete metric absence (no samples) will also trigger PrometheusMetricsAbsentPerCluster.
Check the PrometheusAgent pod status, remote write pipeline, and PodMonitor configuration.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus sample count below 95% SLO threshold for 24 hours.'
          title: 'Prometheus sample count below 95% SLO threshold for 24 hours.'
        }
        expression: '(sum by (job, namespace, cluster) (count_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d])) < 0.95 * (24 * 3600 / 30)) and sum by (job, namespace, cluster) (count_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d] offset 1d)) > 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'PrometheusMetricsAbsentPerCluster'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusMetricsAbsentPerCluster/{{ $labels.cluster }}'
          description: '''Prometheus on cluster {{ $labels.cluster }} has not reported any up metrics in the last 10 minutes, but was reporting within the last 7 days.
This indicates the Prometheus agent on this specific cluster is dead or its remote write pipeline is broken.
Check the PrometheusAgent pod status and remote write configuration on the affected cluster.
'''
          info: '''Prometheus on cluster {{ $labels.cluster }} has not reported any up metrics in the last 10 minutes, but was reporting within the last 7 days.
This indicates the Prometheus agent on this specific cluster is dead or its remote write pipeline is broken.
Check the PrometheusAgent pod status and remote write configuration on the affected cluster.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus metrics absent for cluster {{ $labels.cluster }}.'
          title: 'Prometheus metrics absent for cluster {{ $labels.cluster }}.'
        }
        expression: 'count by (cluster) (count_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1w])) unless count by (cluster) (count_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[10m]))'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus pending sample rate is above 40%.'
          title: 'Prometheus pending sample rate is above 40%.'
        }
        expression: '(prometheus_remote_storage_samples_pending / prometheus_remote_storage_samples_in_flight) > 0.4'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus failed sample rate to remote storage is above 10%.'
          title: 'Prometheus failed sample rate to remote storage is above 10%.'
        }
        expression: '(rate(prometheus_remote_storage_samples_failed_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) / clamp_min(rate(prometheus_remote_storage_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]), 0.000000001)) > 0.1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          title: 'Prometheus fails to send samples to remote storage. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }} remote_name:{{ $labels.remote_name }} url:{{ $labels.url }}'
        }
        expression: '((rate(prometheus_remote_storage_failed_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) / ((rate(prometheus_remote_storage_failed_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) + (rate(prometheus_remote_storage_succeeded_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_total{job="prometheus/prometheus",namespace="prometheus"}[5m])))) * 100 > 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          title: 'Prometheus is not ingesting samples. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }}'
        }
        expression: '(sum without (type) (rate(prometheus_tsdb_head_samples_appended_total{job="prometheus/prometheus",namespace="prometheus"}[5m])) <= 0 and sum without (scrape_job) (prometheus_target_metadata_cache_entries{job="prometheus/prometheus",namespace="prometheus"}) > 0)'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Failed Prometheus configuration reload. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }}'
        }
        expression: 'max_over_time(prometheus_config_last_reload_successful{job="prometheus/prometheus",namespace="prometheus"}[5m]) == 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/Prometheus.md'
          summary: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
          title: 'Prometheus has failed scrapes that have exceeded the configured sample limit. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }}'
        }
        expression: 'increase(prometheus_target_scrapes_exceeded_sample_limit_total{job="prometheus/prometheus",namespace="prometheus"}[5m]) > 0'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          correlationId: 'PrometheusOperatorNotReady/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.controller }}'
          description: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
          info: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatornotready'
          summary: 'Prometheus operator not ready'
          title: 'Prometheus operator not ready namespace:{{ $labels.namespace }} controller:{{ $labels.controller }}'
        }
        expression: 'min by (cluster, controller, namespace) (max_over_time(prometheus_operator_ready{job="prometheus-operator",namespace="prometheus"}[5m])) == 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          correlationId: 'PrometheusOperatorRejectedResources/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.controller }}/{{ $labels.resource }}'
          description: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
          info: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatorrejectedresources'
          summary: 'Resources rejected by Prometheus operator'
          title: 'Resources rejected by Prometheus operator namespace:{{ $labels.namespace }} controller:{{ $labels.controller }} resource:{{ $labels.resource }}'
        }
        expression: 'min_over_time(prometheus_operator_managed_resources{job="prometheus-operator",namespace="prometheus",state="rejected"}[5m]) > 0'
        for: 'PT20M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: '(sum by (cluster) (max without (prometheus_replica) (rate(frontend_clusters_service_client_request_count{code=~"4..|5.."}[1h])))) / (sum by (cluster) (max without (prometheus_replica) (rate(frontend_clusters_service_client_request_count[1h])))) > 0.05'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        expression: '(sum by (cluster) (rate(otel_audit_log_send_errors_total{job="aro-hcp-frontend-metrics"}[1h])) / sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-frontend-metrics"}[1h]))) > 0.05'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'FrontendHttpRequestPanics'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FrontendHttpRequestPanics/{{ $labels.cluster }}'
          description: 'Frontend HTTP request handler has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          info: 'Frontend HTTP request handler has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend is panicking during HTTP request handling'
          title: 'Frontend is panicking during HTTP request handling'
        }
        expression: 'sum by (cluster) (increase(frontend_http_request_panics_total[5m])) > 0'
        for: 'PT1M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'BackendControllerQueueDepthHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'BackendControllerQueueDepthHigh/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Backend controller workqueue {{ $labels.name }} has had a depth > 10 for more than 15 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Backend controller workqueue {{ $labels.name }} has had a depth > 10 for more than 15 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Backend controller workqueue depth is high'
          title: 'Backend controller workqueue depth is high name:{{ $labels.name }}'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{namespace="aro-hcp"})) > 10'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          summary: 'Backend controller is panicking'
          title: 'Backend controller is panicking controller:{{ $labels.controller }}'
        }
        expression: 'sum by (controller, cluster) (increase(panic_total{namespace="aro-hcp"}[5m])) > 0'
        for: 'PT1M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'OrphanedMRGDetected'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'OrphanedMRGDetected/{{ $labels.cluster }}/{{ $labels.location }}'
          description: 'Found {{ printf "%.0f" $value }} orphaned cluster managed resource groups in location {{ $labels.location }} over the last 10 minutes. Orphaned MRGs should not exist - investigate why cluster deletion left resources behind.'
          info: 'Found {{ printf "%.0f" $value }} orphaned cluster managed resource groups in location {{ $labels.location }} over the last 10 minutes. Orphaned MRGs should not exist - investigate why cluster deletion left resources behind.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Orphaned cluster managed resource groups detected'
          title: 'Orphaned cluster managed resource groups detected location:{{ $labels.location }}'
        }
        expression: 'sum by (location, cluster) (max without (prometheus_replica) (increase(aro_hcp_orphaned_managed_resource_groups_found_total[10m]))) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'OrphanedMRGDeletionFailing'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'OrphanedMRGDeletionFailing/{{ $labels.cluster }}/{{ $labels.location }}'
          description: 'Orphaned cluster managed resource group deletion has failed {{ printf "%.0f" $value }} time(s) in location {{ $labels.location }} over the last 10 minutes. Deletion should succeed - investigate Azure permissions or resource locks.'
          info: 'Orphaned cluster managed resource group deletion has failed {{ printf "%.0f" $value }} time(s) in location {{ $labels.location }} over the last 10 minutes. Deletion should succeed - investigate Azure permissions or resource locks.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Orphaned cluster managed resource group deletion is failing'
          title: 'Orphaned cluster managed resource group deletion is failing location:{{ $labels.location }}'
        }
        expression: 'sum by (location, cluster) (max without (prometheus_replica) (increase(aro_hcp_orphaned_managed_resource_groups_deletion_failed_total[10m]))) > 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource fleet 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'fleet'
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
        alert: 'FleetControllerRetryHotLoop'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FleetControllerRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Fleet controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Fleet controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'TBD'
          summary: 'Fleet controller workqueue retry hot loop'
          title: 'Fleet controller workqueue retry hot loop name:{{ $labels.name }}'
        }
        expression: '(sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{namespace="fleet"}[10m]))) / sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{namespace="fleet"}[10m])))) > 0.5'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'FleetControllerQueueDepthHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FleetControllerQueueDepthHigh/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Fleet controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Fleet controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'TBD'
          summary: 'Fleet controller workqueue depth is high'
          title: 'Fleet controller workqueue depth is high name:{{ $labels.name }}'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{namespace="fleet"})) > 10'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'FleetControllerPanic'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FleetControllerPanic/{{ $labels.cluster }}/{{ $labels.controller }}'
          description: 'Fleet controller {{ $labels.controller }} has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          info: 'Fleet controller {{ $labels.controller }} has panicked {{ printf "%.0f" $value }} time(s) in the last 5 minutes.'
          runbook_url: 'TBD'
          summary: 'Fleet controller is panicking'
          title: 'Fleet controller is panicking controller:{{ $labels.controller }}'
        }
        expression: 'sum by (controller, cluster) (increase(panic_total{namespace="fleet"}[5m])) > 0'
        for: 'PT1M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: '(sum by (cluster) (rate(otel_audit_log_send_errors_total{job="aro-hcp-admin-api-metrics"}[1h])) / sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-admin-api-metrics"}[1h]))) > 0.05'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: 'sum(rate(rest_api_inbound_request_count{code=~"5..",namespace="maestro"}[5m])) / sum(rate(rest_api_inbound_request_count{namespace="maestro"}[5m])) > 0.05'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: 'sum(rate(grpc_server_processed_total{code!="OK",namespace="maestro"}[5m])) / sum(rate(grpc_server_processed_total{namespace="maestro"}[5m])) > 0.05'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: 'sum(rate(spec_controller_event_reconcile_total{namespace="maestro",status="error"}[5m])) / sum(rate(spec_controller_event_reconcile_total{namespace="maestro"}[5m])) > 0.1'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'MaestroServerNoReadyReplicas'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'MaestroServerNoReadyReplicas/{{ $labels.cluster }}'
          description: 'No maestro-server replicas have been Ready for 10 minutes. Readiness is gated on Postgres connectivity, so maestro is likely unable to reach its database; Clusters Service will get 503 and HCP cluster provisioning will stall.'
          info: 'No maestro-server replicas have been Ready for 10 minutes. Readiness is gated on Postgres connectivity, so maestro is likely unable to reach its database; Clusters Service will get 503 and HCP cluster provisioning will stall.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'No maestro-server replicas are Ready'
          title: 'No maestro-server replicas are Ready'
        }
        expression: 'kube_deployment_status_replicas_available{deployment="maestro",namespace="maestro"} == 0 and kube_deployment_spec_replicas{deployment="maestro",namespace="maestro"} > 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
        alert: 'MaestroServerReplicaNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'MaestroServerReplicaNotReady/{{ $labels.cluster }}'
          description: 'At least one maestro-server replica has been NotReady for 15 minutes (available {{ $value }} below desired). Readiness is gated on Postgres connectivity, so a persistently NotReady replica usually indicates database connectivity problems.'
          info: 'At least one maestro-server replica has been NotReady for 15 minutes (available {{ $value }} below desired). Readiness is gated on Postgres connectivity, so a persistently NotReady replica usually indicates database connectivity problems.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/maestro/index.html'
          summary: 'A maestro-server replica is not Ready'
          title: 'A maestro-server replica is not Ready'
        }
        expression: 'kube_deployment_status_replicas_available{deployment="maestro",namespace="maestro"} > 0 and kube_deployment_status_replicas_available{deployment="maestro",namespace="maestro"} < kube_deployment_spec_replicas{deployment="maestro",namespace="maestro"}'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubeApplier 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kube-applier'
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
        alert: 'KubeApplierReconcileStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeApplierReconcileStuck/{{ $labels.cluster }}'
          description: 'kube-applier on cluster {{ $labels.cluster }} has {{ $labels.type }} desires pending but no controller processed any items for more than 15 minutes.'
          info: 'kube-applier on cluster {{ $labels.cluster }} has {{ $labels.type }} desires pending but no controller processed any items for more than 15 minutes.'
          runbook_url: 'TBD'
          summary: 'kube-applier on {{ $labels.cluster }}: {{ $labels.type }} controllers are not processing items'
          title: 'kube-applier on {{ $labels.cluster }}: {{ $labels.type }} controllers are not processing items'
        }
        expression: '(sum without (condition) (max without (prometheus_replica) (kube_applier_desires{namespace="kube-applier"})) > 0) and on (cluster, namespace) (sum without (prometheus_replica, name) (increase(workqueue_work_duration_seconds_count{name=~".*Desire.*",namespace="kube-applier"}[15m])) == 0)'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource leaderelection 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'leaderelection'
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
        alert: 'LeaderElectionLeaseStale'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'LeaderElectionLeaseStale/{{ $labels.cluster }}'
          description: 'Leader election lease {{ $labels.lease }} in namespace {{ $labels.namespace }} on cluster {{ $labels.cluster }} has not been renewed for more than 5 minutes. The component may have lost leadership or stopped running.'
          info: 'Leader election lease {{ $labels.lease }} in namespace {{ $labels.namespace }} on cluster {{ $labels.cluster }} has not been renewed for more than 5 minutes. The component may have lost leadership or stopped running.'
          runbook_url: 'TBD'
          summary: 'Leader election lease {{ $labels.lease }} in {{ $labels.namespace }} on {{ $labels.cluster }} stale for more than 5 minutes'
          title: 'Leader election lease {{ $labels.lease }} in {{ $labels.namespace }} on {{ $labels.cluster }} stale for more than 5 minutes'
        }
        expression: 'time() - max without (prometheus_replica) (kube_lease_renew_time{namespace!~"kube-system|kube-public|kube-node-lease|default"}) > 300'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource workqueueRetryhotloop 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'workqueue-retryhotloop'
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
        alert: 'WorkqueueControllerRetryHotLoop'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'WorkqueueControllerRetryHotLoop/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.name }}'
          description: 'Workqueue {{ $labels.name }} in namespace {{ $labels.namespace }} on cluster {{ $labels.cluster }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Workqueue {{ $labels.name }} in namespace {{ $labels.namespace }} on cluster {{ $labels.cluster }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'TBD'
          summary: 'Workqueue {{ $labels.name }} in {{ $labels.namespace }} on {{ $labels.cluster }} retry hot loop'
          title: 'Workqueue {{ $labels.name }} in {{ $labels.namespace }} on {{ $labels.cluster }} retry hot loop'
        }
        expression: '(sum by (name, namespace, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{namespace!~"kube-system|kube-public|kube-node-lease|default|monitoring"}[10m]))) / sum by (name, namespace, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{namespace!~"kube-system|kube-public|kube-node-lease|default|monitoring"}[10m])))) > 0.5'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        expression: 'group by (cluster) (up{job="kube-state-metrics"}) unless on (cluster) group by (cluster) (up{job="arobit-forwarder",namespace="arobit"} == 1)'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Fluent Bit input ingestion paused due to backpressure. pod:{{ $labels.pod }} cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, pod) (fluentbit_input_ingestion_paused) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'High Kusto output retries pod:{{ $labels.pod }} cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, pod) (increase(fluentbit_output_retries_total{name=~"azure_kusto.*"}[5m])) > 3'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Unrecoverable Kusto output errors - log data is being dropped. pod:{{ $labels.pod }} cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, pod) (increase(fluentbit_output_errors_total{name=~"azure_kusto.*"}[5m])) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Kusto output retries exhausted - chunks discarded after max retries. pod:{{ $labels.pod }} cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, pod) (increase(fluentbit_output_retries_failed_total{name=~"azure_kusto.*"}[5m])) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-inbound-customerapi"} / 32 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-inbound-svc"} / 18 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-outbound-cx"} / 8 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-nonprod-outbound-svc"} / 18 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{region!="uswest2",service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-customerapi"} / 64 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{region="uswest2",service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-customerapi"} / 128 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-cx"} / 32 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-inbound-svc"} / 32 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-outbound-cx"} / 32 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          title: 'Service Tag IP Usage is reaching 80% cluster:{{ $labels.cluster }}'
        }
        expression: 'public_ip_count_by_region_service_tag{service_tag_type="FirstPartyUsage",service_tag_value="/aro-hcp-prod-outbound-svc"} / 32 > 0.8'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          correlationId: 'HCPClusterStuckDeleting/{{ $labels.cluster }}/{{ $labels.resource_id }}'
          description: '''Cluster {{ $labels.resource_id }} has been in a deleting state for more than 3 hours. 
This may indicate that finalizers are stuck or resources are failing to cleanup.
'''
          info: '''Cluster {{ $labels.resource_id }} has been in a deleting state for more than 3 hours. 
This may indicate that finalizers are stuck or resources are failing to cleanup.
'''
          runbook_url: 'TBD'
          summary: '{{ $labels.cluster }}: Cluster {{ $labels.resource_id }} stuck deleting'
          title: '{{ $labels.cluster }}: Cluster {{ $labels.resource_id }} stuck deleting'
        }
        expression: '(max by (resource_id, subscription_id, cluster) (time() - backend_resource_operation_start_time_seconds{operation_type="delete",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) and max by (resource_id, subscription_id, cluster) (backend_resource_operation_phase_info{operation_type="delete",phase="deleting",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) == 1) > 10800'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hcpTestClustersRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-test-clusters-rules'
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
        alert: 'HCPClusterOlderThan3Hours'
        enabled: true
        labels: {
          severity: '3'
        }
        annotations: {
          correlationId: 'IntStgClusterOlderThan3Hours{{ $labels.resource_id }}'
          description: '''HCP cluster {{ $labels.resource_id }} in {{ $labels.environment }} on service cluster {{ $labels.cluster }} has existed for more than 3 hours.
'''
          info: '''HCP cluster {{ $labels.resource_id }} in {{ $labels.environment }} on service cluster {{ $labels.cluster }} has existed for more than 3 hours.
'''
          runbook_url: 'TBD'
          summary: 'HCP in {{ $labels.environment }} is older than 3h'
          title: 'HCP in {{ $labels.environment }} is older than 3h resource_id:{{ $labels.resource_id }} cluster:{{ $labels.cluster }}'
        }
        expression: 'max by (cluster, resource_id, environment) (time() - backend_cluster_created_time_seconds{environment=~"int|stg"}) > 3 * 3600'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Container {{ $labels.container }} was OOMKilled namespace:{{ $labels.namespace }} pod:{{ $labels.pod }} cluster:{{ $labels.cluster }}'
        }
        expression: 'kube_pod_container_status_last_terminated_reason{job="kube-state-metrics",reason="OOMKilled"} == 1'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
          title: 'Node under memory pressure node:{{ $labels.node }}'
        }
        expression: 'kube_node_status_condition{condition="MemoryPressure",status="true"} == 1'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource imageRegistryPolicy 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'image-registry-policy'
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
        alert: 'ImageRegistryPolicyDenied'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'ImageRegistryPolicyDenied/{{ $labels.cluster }}'
          description: 'The image-registry-allowlist-policy on cluster {{ $labels.cluster }} has denied {{ $value }} pod admission(s) in the last 15 minutes. This means pods with images from non-approved registries were blocked from running.'
          info: 'The image-registry-allowlist-policy on cluster {{ $labels.cluster }} has denied {{ $value }} pod admission(s) in the last 15 minutes. This means pods with images from non-approved registries were blocked from running.'
          runbook_url: 'TBD'
          summary: 'Image registry policy denied pod admission'
          title: 'Image registry policy denied pod admission cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, policy, policy_binding) (increase(apiserver_validating_admission_policy_check_total{enforcement_action="deny",policy="image-registry-allowlist-policy",validation_result="denied"}[15m])) > 0'
        for: 'PT1M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'ImageRegistryPolicyAuditViolation'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'ImageRegistryPolicyAuditViolation/{{ $labels.cluster }}'
          description: 'The image-registry-allowlist-policy on cluster {{ $labels.cluster }} has logged {{ $value }} audit violation(s) in the last 15 minutes. Pods with images from non-approved registries are running but not blocked. Review kubernetesEvents in Kusto for details.'
          info: 'The image-registry-allowlist-policy on cluster {{ $labels.cluster }} has logged {{ $value }} audit violation(s) in the last 15 minutes. Pods with images from non-approved registries are running but not blocked. Review kubernetesEvents in Kusto for details.'
          runbook_url: 'TBD'
          summary: 'Image registry policy audit violation detected'
          title: 'Image registry policy audit violation detected cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (cluster, policy, policy_binding) (increase(apiserver_validating_admission_policy_check_total{enforcement_action="audit",policy="image-registry-allowlist-policy",validation_result="denied"}[15m])) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
          correlationId: 'KustoLogsDataStale/{{ $labels.cluster }}/{{ $labels.table }}/{{ $labels.kusto_cluster }}'
          description: '''Kusto log data for table {{ $labels.table }} on cluster {{ $labels.cluster }} (Kusto cluster {{ $labels.kusto_cluster }}) is stale. Check the ingestion pipeline.
'''
          info: '''Kusto log data for table {{ $labels.table }} on cluster {{ $labels.cluster }} (Kusto cluster {{ $labels.kusto_cluster }}) is stale. Check the ingestion pipeline.
'''
          runbook_url: 'TBD'
          summary: 'Kusto log data is stale for {{ $labels.table }} on {{ $labels.cluster }}.'
          title: 'Kusto log data is stale for {{ $labels.table }} on {{ $labels.cluster }}. kusto_cluster:{{ $labels.kusto_cluster }}'
        }
        expression: 'kusto_logs_age_in_seconds{table!="systemdlogs"} > 3600 or kusto_logs_age_in_seconds{cluster!~".*-svc-.*",table="systemdlogs"} > 3600 or kusto_logs_age_in_seconds{cluster=~".*-svc-.*",table="systemdlogs"} > 7200'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
