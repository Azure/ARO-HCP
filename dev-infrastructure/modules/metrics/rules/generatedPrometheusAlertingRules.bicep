#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource prometheusWipRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'prometheus-wip-rules'
  location: resourceGroup().location
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
          description: '''Prometheus has not been reachable for the past 5 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 5 minutes.'
          title: '''Prometheus has not been reachable for the past 5 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
        }
        expression: 'min by (job, namespace) (up{job="prometheus/prometheus",namespace="prometheus"}) == 0'
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
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 1 day.'
          title: '''Prometheus has been unreachable for more than 5% of the time over the past 24 hours.
This may indicate that the Prometheus server is down, experiencing network issues, or stuck in a crash loop.
Please check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
        }
        expression: 'avg by (job, namespace) (avg_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d])) < 0.95'
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
          runbook_url: 'TBD'
          summary: 'Prometheus pending sample rate is above 40%.'
          title: '''The pending sample rate of Prometheus remote storage is above 40% for the last 15 minutes.
This means that more than 40% of samples are waiting to be sent to remote storage, which may indicate
a bottleneck or issue with the remote write endpoint, network connectivity, or Prometheus performance.
If this condition persists, it could lead to increased memory usage and potential data loss if the buffer overflows.
Investigate the health and performance of the remote storage endpoint, network latency, and Prometheus resource utilization.
'''
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
          runbook_url: 'TBD'
          summary: 'Prometheus failed sample rate to remote storage is above 10%.'
          title: '''The failed sample rate for Prometheus remote storage has exceeded 10% over the past 15 minutes.
This indicates that more than 10% of samples are not being successfully sent to remote storage, which could be caused by
issues with the remote write endpoint, network instability, or Prometheus resource constraints.
Persistent failures may result in increased memory usage and potential data loss if the buffer overflows.
Please check the health and performance of the remote storage endpoint, network connectivity, and Prometheus resource utilization.
'''
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
  location: resourceGroup().location
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusremotestoragefailures'
          summary: 'Prometheus fails to send samples to remote storage.'
          title: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} failed to send {{ printf "%.1f" $value }}% of the samples to {{ $labels.remote_name}}:{{ $labels.url }}'
        }
        expression: '((rate(prometheus_remote_storage_failed_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) / ((rate(prometheus_remote_storage_failed_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) + (rate(prometheus_remote_storage_succeeded_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m])))) * 100 > 1'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusnotingestingsamples'
          summary: 'Prometheus is not ingesting samples.'
          title: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} is not ingesting samples.'
        }
        expression: '(sum without (type) (rate(prometheus_tsdb_head_samples_appended_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) <= 0 and (sum without (scrape_job) (prometheus_target_metadata_cache_entries{job="prometheus-prometheus",namespace="prometheus"}) > 0 or sum without (rule_group) (prometheus_rule_group_rules{job="prometheus-prometheus",namespace="prometheus"}) > 0))'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusbadconfig'
          summary: 'Failed Prometheus configuration reload.'
          title: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to reload its configuration.'
        }
        expression: 'max_over_time(prometheus_config_last_reload_successful{job="prometheus-prometheus",namespace="prometheus"}[5m]) == 0'
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
        alert: 'PrometheusRuleFailures'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'PrometheusRuleFailures/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to evaluate {{ printf "%.0f" $value }} rules in the last 5m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusrulefailures'
          summary: 'Prometheus is failing rule evaluations.'
          title: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to evaluate {{ printf "%.0f" $value }} rules in the last 5m.'
        }
        expression: 'increase(prometheus_rule_evaluation_failures_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) > 0'
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
        alert: 'PrometheusScrapeSampleLimitHit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'PrometheusScrapeSampleLimitHit/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusscrapesamplelimithit'
          summary: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
          title: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
        }
        expression: 'increase(prometheus_target_scrapes_exceeded_sample_limit_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) > 0'
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
  location: resourceGroup().location
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatornotready'
          summary: 'Prometheus operator not ready'
          title: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatorrejectedresources'
          summary: 'Resources rejected by Prometheus operator'
          title: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
        }
        expression: 'min_over_time(prometheus_operator_managed_resources{job="prometheus-operator",namespace="prometheus",state="rejected"}[5m]) > 0'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource mise 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mise'
  location: resourceGroup().location
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
        alert: 'MiseEnvoyScrapeDown'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'MiseEnvoyScrapeDown/{{ $labels.cluster }}'
          description: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
          runbook_url: 'TBD'
          summary: 'Envoy scrape target down for namespace=mise'
          title: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
        }
        expression: 'absent(up{job="envoy-stats", namespace="mise"}) or (up{job="envoy-stats", namespace="mise"} == 0)'
        for: 'PT5M'
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
  location: resourceGroup().location
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
          description: 'The 95th percentile of frontend request latency has exceeded 1 second over the past hour.'
          runbook_url: 'TBD'
          summary: 'Frontend latency is high: 95th percentile exceeds 1 second'
          title: 'The 95th percentile of frontend request latency has exceeded 1 second over the past hour.'
        }
        expression: 'histogram_quantile(0.95, rate(frontend_http_requests_duration_seconds_bucket[1h])) > 1'
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
        alert: 'FrontendClusterServiceErrorRate'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendClusterServiceErrorRate/{{ $labels.cluster }}'
          description: 'The Frontend Cluster Service 5xx error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'TBD'
          summary: 'High 4xx|5xx Error Rate on Frontend Cluster Service'
          title: 'The Frontend Cluster Service 5xx error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
        }
        expression: '(sum(max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count{code=~"4..|5.."}[1h])))) / (sum(max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count[1h])))) > 0.05'
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
        alert: 'FrontendHealthAvailability'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendHealthAvailability/{{ $labels.cluster }}'
          description: 'The Frontend has been unavailable for more than 5 minutes in the last hour.'
          runbook_url: 'TBD'
          summary: 'High unavailability on the Frontend'
          title: 'The Frontend has been unavailable for more than 5 minutes in the last hour.'
        }
        expression: '(1 - (sum_over_time(frontend_health[1h]) / 3600)) >= (300 / 3600)'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpCsSloAvailabilityAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cs_slo_availability_alerts'
  location: resourceGroup().location
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API availability error budget burn rate is too high'
          title: 'API is rapidly burning its 28 day availability error budget (99% SLO)'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 )'
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day availability error budget (99% SLO)'
          title: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934 and sum by(cluster, namespace, service) (max without(prometheus_replica) (availability:api_inbound_request_count:burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934'
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API P99 latency error budget burn rate is too high'
          title: 'API is rapidly burning its 28 day 1s latency error budget (99% SLO)'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 )'
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day 1s latency error budget (99% SLO)'
          title: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p99_burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934'
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'Cluster Service API P90 latency error budget burn rate is too high'
          title: 'API is rapidly burning its 28 day 0.1s latency error budget (90% SLO)'
        }
        expression: '( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > 13.44 ) or ( sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 5.6 )'
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
          runbook_url: 'aka.ms/arohcp-runbook/cs-slo-monitoring'
          summary: 'API is slowly but steadily burning its 28 day 0.1s latency error budget (90% SLO)'
          title: 'This indicates persistent underperformance that needs investigation to avoid an SLO breach. The alert will fire if the current burn rate exceeds 0.934 times the allowed rate for the last 6 hours and 3 days.'
        }
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934 and sum by(cluster, namespace, service) (max without(prometheus_replica) (latency:api_inbound_request_duration:p90_burnrate3d{namespace="clusters-service", service="clusters-service-metrics"})) > 0.934'
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
  location: resourceGroup().location
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
        alert: 'BackendLatency'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'BackendLatency/{{ $labels.cluster }}'
          description: 'The 95th percentile of backend request latency has exceeded 1 second over the past hour.'
          runbook_url: 'TBD'
          summary: 'Backend latency is high: 95th percentile exceeds 1 second'
          title: 'The 95th percentile of backend request latency has exceeded 1 second over the past hour.'
        }
        expression: 'histogram_quantile(0.95, rate(backend_operations_duration_seconds_bucket[1h])) > 1'
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
        alert: 'BackendOperationErrorRate'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'BackendOperationErrorRate/{{ $labels.cluster }}'
          description: 'The Backend operation error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'TBD'
          summary: 'High Error Rate on Backend Operations'
          title: 'The Backend operation error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
        }
        expression: '(sum(rate(backend_failed_operations_total[1h]))) / (sum(rate(backend_operations_total[1h]))) > 0.05'
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
        alert: 'BackendHealthAvailability'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'BackendHealthAvailability/{{ $labels.cluster }}'
          description: 'The Backend has been unavailable for more than 5 minutes in the last hour.'
          runbook_url: 'TBD'
          summary: 'High unavailability on the Backend'
          title: 'The Backend has been unavailable for more than 5 minutes in the last hour.'
        }
        expression: '(1 - (sum_over_time(backend_health[1h]) / 3600)) >= (300 / 3600)'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
