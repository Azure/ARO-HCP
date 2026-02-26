#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource kubernetesResources 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-resources'
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
        alert: 'KubeMemoryOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          info: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
          title: 'Cluster has overcommitted memory resource requests.'
        }
        expression: 'sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster) - max(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster)) > 0 and (sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster) - max(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster)) > 0'
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
        alert: 'KubeCPUQuotaOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeCPUQuotaOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
          info: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuquotaovercommit'
          summary: 'Cluster has overcommitted CPU resource requests.'
          title: 'Cluster has overcommitted CPU resource requests.'
        }
        expression: 'sum(min without(resource) (kube_resourcequota{job="kube-state-metrics", type="hard", resource=~"(cpu|requests.cpu)"})) by (cluster) / sum(kube_node_status_allocatable{resource="cpu", job="kube-state-metrics"}) by (cluster) > 1.5'
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
        alert: 'KubeMemoryQuotaOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryQuotaOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
          info: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryquotaovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
          title: 'Cluster has overcommitted memory resource requests.'
        }
        expression: 'sum(min without(resource) (kube_resourcequota{job="kube-state-metrics", type="hard", resource=~"(memory|requests.memory)"})) by (cluster) / sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster) > 1.5'
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
        alert: 'KubeQuotaAlmostFull'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeQuotaAlmostFull/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaalmostfull'
          summary: 'Namespace quota is going to be full.'
          title: 'Namespace quota is going to be full.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"} / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0) > 0.9 < 1'
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
        alert: 'KubeQuotaFullyUsed'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeQuotaFullyUsed/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotafullyused'
          summary: 'Namespace quota is fully used.'
          title: 'Namespace quota is fully used.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"} / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0) == 1'
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
        alert: 'KubeQuotaExceeded'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeQuotaExceeded/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaexceeded'
          summary: 'Namespace quota has exceeded the limits.'
          title: 'Namespace quota has exceeded the limits.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"} / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0) > 1'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesStorage 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-storage'
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
        alert: 'KubePersistentVolumeErrors'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeErrors/{{ $labels.cluster }}/{{ $labels.persistentvolume }}/{{ $labels.phase }}'
          description: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          info: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeerrors'
          summary: 'PersistentVolume is having issues with provisioning.'
          title: 'PersistentVolume is having issues with provisioning.'
        }
        expression: 'kube_persistentvolume_status_phase{phase=~"Failed|Pending",job="kube-state-metrics"} > 0'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesSystem 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-system'
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
        alert: 'KubeVersionMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeVersionMismatch/{{ $labels.cluster }}'
          description: 'There are {{ $value }} different semantic versions of Kubernetes components running.'
          info: 'There are {{ $value }} different semantic versions of Kubernetes components running.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeversionmismatch'
          summary: 'Different semantic versions of Kubernetes components running.'
          title: 'Different semantic versions of Kubernetes components running.'
        }
        expression: 'count by (cluster) (count by (git_version, cluster) (label_replace(kubernetes_build_info{job!~"kube-dns|coredns"},"git_version","$1","git_version","(v[0-9]*.[0-9]*).*"))) > 1'
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
        alert: 'KubeClientErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeClientErrors/{{ $labels.cluster }}/{{ $labels.instance }}/{{ $labels.job }}'
          description: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
          info: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclienterrors'
          summary: 'Kubernetes API server client is experiencing errors.'
          title: 'Kubernetes API server client is experiencing errors.'
        }
        expression: '(sum(rate(rest_client_requests_total{job="controlplane-apiserver",code=~"5.."}[5m])) by (cluster, instance, job, namespace) / sum(rate(rest_client_requests_total{job="controlplane-apiserver"}[5m])) by (cluster, instance, job, namespace)) > 0.01'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubeApiserverSlos 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kube-apiserver-slos'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '1h'
          severity: 'critical'
          short: '5m'
        }
        annotations: {
          correlationId: 'KubeAPIErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'The API server is burning too much error budget.'
          info: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
          title: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate1h) > (14.40 * 0.01000) and sum(apiserver_request:burnrate5m) > (14.40 * 0.01000)'
        for: 'PT2M'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'critical'
          short: '30m'
        }
        annotations: {
          correlationId: 'KubeAPIErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'The API server is burning too much error budget.'
          info: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
          title: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate6h) > (6.00 * 0.01000) and sum(apiserver_request:burnrate30m) > (6.00 * 0.01000)'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '1d'
          severity: 'warning'
          short: '2h'
        }
        annotations: {
          correlationId: 'KubeAPIErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'The API server is burning too much error budget.'
          info: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
          title: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate1d) > (3.00 * 0.01000) and sum(apiserver_request:burnrate2h) > (3.00 * 0.01000)'
        for: 'PT1H'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '3d'
          severity: 'warning'
          short: '6h'
        }
        annotations: {
          correlationId: 'KubeAPIErrorBudgetBurn/{{ $labels.cluster }}'
          description: 'The API server is burning too much error budget.'
          info: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
          title: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate3d) > (1.00 * 0.01000) and sum(apiserver_request:burnrate6h) > (1.00 * 0.01000)'
        for: 'PT3H'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesSystemApiserver 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-system-apiserver'
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
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeClientCertificateExpiration/{{ $labels.cluster }}'
          description: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 7.0 days.'
          info: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 7.0 days.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
          title: 'Client certificate is about to expire.'
        }
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="controlplane-apiserver"} > 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="controlplane-apiserver"}[5m]))) < 604800'
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
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeClientCertificateExpiration/{{ $labels.cluster }}'
          description: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 24.0 hours.'
          info: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 24.0 hours.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
          title: 'Client certificate is about to expire.'
        }
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="controlplane-apiserver"} > 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="controlplane-apiserver"}[5m]))) < 86400'
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
        alert: 'KubeAggregatedAPIErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeAggregatedAPIErrors/{{ $labels.cluster }}/{{ $labels.name }}/{{ $labels.namespace }}'
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
          info: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapierrors'
          summary: 'Kubernetes aggregated API has reported errors.'
          title: 'Kubernetes aggregated API has reported errors.'
        }
        expression: 'sum by(name, namespace, cluster)(increase(aggregator_unavailable_apiservice_total{job="controlplane-apiserver"}[10m])) > 4'
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
        alert: 'KubeAggregatedAPIDown'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeAggregatedAPIDown/{{ $labels.cluster }}/{{ $labels.name }}/{{ $labels.namespace }}'
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
          info: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapidown'
          summary: 'Kubernetes aggregated API is down.'
          title: 'Kubernetes aggregated API is down.'
        }
        expression: '(1 - max by(name, namespace, cluster)(avg_over_time(aggregator_unavailable_apiservice{job="controlplane-apiserver"}[10m]))) * 100 < 85'
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
        alert: 'KubeAPIDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeAPIDown/{{ $labels.cluster }}'
          description: 'KubeAPI has disappeared from Prometheus target discovery.'
          info: 'KubeAPI has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapidown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="controlplane-apiserver"} == 1)'
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
        alert: 'KubeAPITerminatedRequests'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeAPITerminatedRequests/{{ $labels.cluster }}'
          description: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
          info: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapiterminatedrequests'
          summary: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
          title: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
        }
        expression: 'sum(rate(apiserver_request_terminations_total{job="controlplane-apiserver"}[10m]))  / (  sum(rate(apiserver_request_total{job="controlplane-apiserver"}[10m])) + sum(rate(apiserver_request_terminations_total{job="controlplane-apiserver"}[10m])) ) > 0.20'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesSystemKubelet 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-system-kubelet'
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
        alert: 'KubeNodeNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeNotReady/{{ $labels.cluster }}/{{ $labels.node }}'
          description: '{{ $labels.node }} has been unready for more than 15 minutes.'
          info: '{{ $labels.node }} has been unready for more than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodenotready'
          summary: 'Node is not ready.'
          title: 'Node is not ready.'
        }
        expression: 'kube_node_status_condition{job="kube-state-metrics",condition="Ready",status="true"} == 0'
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
        alert: 'KubeNodeUnreachable'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeUnreachable/{{ $labels.cluster }}/{{ $labels.node }}'
          description: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
          info: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodeunreachable'
          summary: 'Node is unreachable.'
          title: 'Node is unreachable.'
        }
        expression: '(kube_node_spec_taint{job="kube-state-metrics",key="node.kubernetes.io/unreachable",effect="NoSchedule"} unless ignoring(key,value) kube_node_spec_taint{job="kube-state-metrics",key=~"ToBeDeletedByClusterAutoscaler|cloud.google.com/impending-node-termination|aws-node-termination-handler/spot-itn"}) == 1'
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
        alert: 'KubeletTooManyPods'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeletTooManyPods/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
          info: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubelettoomanypods'
          summary: 'Kubelet is running at capacity.'
          title: 'Kubelet is running at capacity.'
        }
        expression: 'count by(cluster, node) ( (kube_pod_status_phase{job="kube-state-metrics",phase="Running"} == 1) * on(instance,pod,namespace,cluster) group_left(node) topk by(instance,pod,namespace,cluster) (1, kube_pod_info{job="kube-state-metrics"}) ) / max by(cluster, node) ( kube_node_status_capacity{job="kube-state-metrics",resource="pods"} != 1 ) > 0.95'
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
        alert: 'KubeNodeReadinessFlapping'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeReadinessFlapping/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
          info: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodereadinessflapping'
          summary: 'Node readiness status is flapping.'
          title: 'Node readiness status is flapping.'
        }
        expression: 'sum(changes(kube_node_status_condition{job="kube-state-metrics",status="true",condition="Ready"}[15m])) by (cluster, node) > 2'
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
        alert: 'KubeletPlegDurationHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletPlegDurationHigh/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
          info: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletplegdurationhigh'
          summary: 'Kubelet Pod Lifecycle Event Generator is taking too long to relist.'
          title: 'Kubelet Pod Lifecycle Event Generator is taking too long to relist.'
        }
        expression: 'node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile{quantile="0.99"} >= 10'
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
        alert: 'KubeletPodStartUpLatencyHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletPodStartUpLatencyHigh/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
          info: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletpodstartuplatencyhigh'
          summary: 'Kubelet Pod startup latency is too high.'
          title: 'Kubelet Pod startup latency is too high.'
        }
        expression: 'histogram_quantile(0.99, sum(rate(kubelet_pod_worker_duration_seconds_bucket{job="kubelet", metrics_path="/metrics"}[5m])) by (cluster, instance, le)) * on(cluster, instance) group_left(node) kubelet_node_name{job="kubelet", metrics_path="/metrics"} > 60'
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
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
          title: 'Kubelet client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 604800'
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
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
          title: 'Kubelet client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 86400'
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
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
          title: 'Kubelet server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 604800'
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
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
          title: 'Kubelet server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 86400'
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
        alert: 'KubeletClientCertificateRenewalErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateRenewalErrors/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          info: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its client certificate.'
          title: 'Kubelet has failed to renew its client certificate.'
        }
        expression: 'increase(kubelet_certificate_manager_client_expiration_renew_errors[5m]) > 0'
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
        alert: 'KubeletServerCertificateRenewalErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateRenewalErrors/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          info: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its server certificate.'
          title: 'Kubelet has failed to renew its server certificate.'
        }
        expression: 'increase(kubelet_server_expiration_renew_errors[5m]) > 0'
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
        alert: 'KubeletDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeletDown/{{ $labels.cluster }}'
          description: 'Kubelet has disappeared from Prometheus target discovery.'
          info: 'Kubelet has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="kubelet", metrics_path="/metrics"} == 1)'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesSystemScheduler 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-system-scheduler'
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
        alert: 'KubeSchedulerDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeSchedulerDown/{{ $labels.cluster }}'
          description: 'KubeScheduler has disappeared from Prometheus target discovery.'
          info: 'KubeScheduler has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeschedulerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="controlplane-kube-scheduler"} == 1)'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubernetesSystemControllerManager 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-system-controller-manager'
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
        alert: 'KubeControllerManagerDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeControllerManagerDown/{{ $labels.cluster }}'
          description: 'KubeControllerManager has disappeared from Prometheus target discovery.'
          info: 'KubeControllerManager has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontrollermanagerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="controlplane-kube-controller-manager"} == 1)'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

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
          info: '''Prometheus has not been reachable for the past 5 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 5 minutes.'
          title: 'Prometheus is unreachable for 5 minutes.'
        }
        expression: 'group by (cluster) (up{job="kube-state-metrics"}) unless on(cluster) group by (cluster) (up{job="prometheus/prometheus",namespace="prometheus"} == 1)'
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
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} failed to send {{ printf "%.1f" $value }}% of the samples to {{ $labels.remote_name}}:{{ $labels.url }}'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusremotestoragefailures'
          summary: 'Prometheus fails to send samples to remote storage.'
          title: 'Prometheus fails to send samples to remote storage.'
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
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} is not ingesting samples.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusnotingestingsamples'
          summary: 'Prometheus is not ingesting samples.'
          title: 'Prometheus is not ingesting samples.'
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
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to reload its configuration.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusbadconfig'
          summary: 'Failed Prometheus configuration reload.'
          title: 'Failed Prometheus configuration reload.'
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
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to evaluate {{ printf "%.0f" $value }} rules in the last 5m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusrulefailures'
          summary: 'Prometheus is failing rule evaluations.'
          title: 'Prometheus is failing rule evaluations.'
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
          info: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusscrapesamplelimithit'
          summary: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
          title: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
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
          info: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
          runbook_url: 'TBD'
          summary: 'Envoy scrape target down for namespace=mise'
          title: 'Envoy scrape target down for namespace=mise'
        }
        expression: 'group by (cluster) (up{job="kube-state-metrics", cluster=~".*-svc(-[0-9]+)?$"}) unless on(cluster) group by (cluster) (up{endpoint="http-envoy-prom", container="istio-proxy", namespace="mise"} == 1)'
        for: 'PT5M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource msiCredentialRefresher 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msi-credential-refresher'
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
        alert: 'ClusterCredentialExpiringSoon'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'ClusterCredentialExpiringSoon/{{ $labels.cluster }}'
          description: 'Cluster credential for cluster {{ $labels.cluster }} is expiring in less than 30 days.'
          info: 'Cluster credential for cluster {{ $labels.cluster }} is expiring in less than 30 days.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/doc/tsgs/credential-refresher-expiring-cert'
          summary: 'Cluster credential expiring in less than 30 days'
          title: 'Cluster credential expiring in less than 30 days'
        }
        expression: 'increase(credential_refresher_days_until_msi_credential_expiration_bucket{le="14"}[30m]) > 0'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
