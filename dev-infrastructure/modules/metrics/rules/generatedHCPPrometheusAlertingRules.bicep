#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource kubernetesApps 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-apps'
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
        alert: 'KubePodCrashLooping'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodCrashLooping/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping'
          summary: 'Pod is crash looping.'
          title: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
        }
        expression: 'max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff", job="kube-state-metrics"}[5m]) >= 1'
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
        alert: 'KubePodNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodNotReady/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodnotready'
          summary: 'Pod has been in a non-ready state for more than 15 minutes.'
          title: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 15 minutes.'
        }
        expression: 'sum by (namespace, pod, cluster) ( max by(namespace, pod, cluster) ( kube_pod_status_phase{job="kube-state-metrics", phase=~"Pending|Unknown|Failed"} ) * on(namespace, pod, cluster) group_left(owner_kind) topk by(namespace, pod, cluster) ( 1, max by(namespace, pod, owner_kind, cluster) (kube_pod_owner{owner_kind!="Job"}) ) ) > 0'
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
        alert: 'KubeDeploymentGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentGenerationMismatch/{{ $labels.cluster }}/{{ $labels.deployment }}/{{ $labels.namespace }}'
          description: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentgenerationmismatch'
          summary: 'Deployment generation mismatch due to possible roll-back'
          title: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
        }
        expression: 'kube_deployment_status_observed_generation{job="kube-state-metrics"} != kube_deployment_metadata_generation{job="kube-state-metrics"}'
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
        alert: 'KubeDeploymentReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentReplicasMismatch/{{ $labels.cluster }}/{{ $labels.deployment }}/{{ $labels.namespace }}'
          description: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentreplicasmismatch'
          summary: 'Deployment has not matched the expected number of replicas.'
          title: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 15 minutes.'
        }
        expression: '( kube_deployment_spec_replicas{job="kube-state-metrics"} > kube_deployment_status_replicas_available{job="kube-state-metrics"} ) and ( changes(kube_deployment_status_replicas_updated{job="kube-state-metrics"}[10m]) == 0 )'
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
        alert: 'KubeDeploymentRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentRolloutStuck/{{ $labels.cluster }}/{{ $labels.deployment }}/{{ $labels.namespace }}'
          description: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentrolloutstuck'
          summary: 'Deployment rollout is not progressing.'
          title: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 15 minutes.'
        }
        expression: 'kube_deployment_status_condition{condition="Progressing", status="false",job="kube-state-metrics"} != 0'
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
        alert: 'KubeStatefulSetReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetreplicasmismatch'
          summary: 'StatefulSet has not matched the expected number of replicas.'
          title: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
        }
        expression: '( kube_statefulset_status_replicas_ready{job="kube-state-metrics"} != kube_statefulset_status_replicas{job="kube-state-metrics"} ) and ( changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[10m]) == 0 )'
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
        alert: 'KubeStatefulSetGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetGenerationMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetgenerationmismatch'
          summary: 'StatefulSet generation mismatch due to possible roll-back'
          title: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
        }
        expression: 'kube_statefulset_status_observed_generation{job="kube-state-metrics"} != kube_statefulset_metadata_generation{job="kube-state-metrics"}'
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
        alert: 'KubeStatefulSetUpdateNotRolledOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetUpdateNotRolledOut/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetupdatenotrolledout'
          summary: 'StatefulSet update has not been rolled out.'
          title: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
        }
        expression: '( max by(namespace, statefulset, job, cluster) ( kube_statefulset_status_current_revision{job="kube-state-metrics"} unless kube_statefulset_status_update_revision{job="kube-state-metrics"} ) * ( kube_statefulset_replicas{job="kube-state-metrics"} != kube_statefulset_status_replicas_updated{job="kube-state-metrics"} ) )  and ( changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[5m]) == 0 )'
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
        alert: 'KubeDaemonSetRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetRolloutStuck/{{ $labels.cluster }}/{{ $labels.daemonset }}/{{ $labels.namespace }}'
          description: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetrolloutstuck'
          summary: 'DaemonSet rollout is stuck.'
          title: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
        }
        expression: '( ( kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"} ) or ( kube_daemonset_status_number_misscheduled{job="kube-state-metrics"} != 0 ) or ( kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"} ) or ( kube_daemonset_status_number_available{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"} ) ) and ( changes(kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"}[5m]) == 0 )'
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
        alert: 'KubeContainerWaiting'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeContainerWaiting/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontainerwaiting'
          summary: 'Pod container waiting longer than 1 hour'
          title: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
        }
        expression: 'sum by (namespace, pod, container, cluster) (kube_pod_container_status_waiting_reason{job="kube-state-metrics"}) > 0'
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
        alert: 'KubeDaemonSetNotScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetNotScheduled/{{ $labels.cluster }}/{{ $labels.daemonset }}/{{ $labels.namespace }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetnotscheduled'
          summary: 'DaemonSet pods are not scheduled.'
          title: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
        }
        expression: 'kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"} - kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"} > 0'
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
        alert: 'KubeDaemonSetMisScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetMisScheduled/{{ $labels.cluster }}/{{ $labels.daemonset }}/{{ $labels.namespace }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetmisscheduled'
          summary: 'DaemonSet pods are misscheduled.'
          title: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
        }
        expression: 'kube_daemonset_status_number_misscheduled{job="kube-state-metrics"} > 0'
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
        alert: 'KubeJobNotCompleted'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobNotCompleted/{{ $labels.cluster }}/{{ $labels.job_name }}/{{ $labels.namespace }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobnotcompleted'
          summary: 'Job did not complete in time'
          title: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
        }
        expression: 'time() - max by(namespace, job_name, cluster) (kube_job_status_start_time{job="kube-state-metrics"} and kube_job_status_active{job="kube-state-metrics"} > 0) > 43200'
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
        alert: 'KubeJobFailed'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobFailed/{{ $labels.cluster }}/{{ $labels.job_name }}/{{ $labels.namespace }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobfailed'
          summary: 'Job failed to complete.'
          title: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
        }
        expression: 'kube_job_failed{job="kube-state-metrics"}  > 0'
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
        alert: 'KubeHpaReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaReplicasMismatch/{{ $labels.cluster }}/{{ $labels.horizontalpodautoscaler }}/{{ $labels.namespace }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpareplicasmismatch'
          summary: 'HPA has not matched desired number of replicas.'
          title: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
        }
        expression: '(kube_horizontalpodautoscaler_status_desired_replicas{job="kube-state-metrics"} != kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} > kube_horizontalpodautoscaler_spec_min_replicas{job="kube-state-metrics"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} < kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}) and changes(kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}[15m]) == 0'
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
        alert: 'KubeHpaMaxedOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaMaxedOut/{{ $labels.cluster }}/{{ $labels.horizontalpodautoscaler }}/{{ $labels.namespace }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpamaxedout'
          summary: 'HPA is running at max replicas'
          title: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
        }
        expression: 'kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} == kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

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
        alert: 'KubeCPUOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeCPUOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests for Pods by {{ $value }} CPU shares and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuovercommit'
          summary: 'Cluster has overcommitted CPU resource requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests for Pods by {{ $value }} CPU shares and cannot tolerate node failure.'
        }
        expression: 'sum(namespace_cpu:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster) - max(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster)) > 0 and (sum(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster) - max(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster)) > 0'
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
        alert: 'KubeMemoryOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuquotaovercommit'
          summary: 'Cluster has overcommitted CPU resource requests.'
          title: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryquotaovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
          title: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaalmostfull'
          summary: 'Namespace quota is going to be full.'
          title: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotafullyused'
          summary: 'Namespace quota is fully used.'
          title: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaexceeded'
          summary: 'Namespace quota has exceeded the limits.'
          title: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"} / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0) > 1'
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
        alert: 'CPUThrottlingHigh'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'CPUThrottlingHigh/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: '{{ $value | humanizePercentage }} throttling of CPU in namespace {{ $labels.namespace }} for container {{ $labels.container }} in pod {{ $labels.pod }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh'
          summary: 'Processes experience elevated CPU throttling.'
          title: '{{ $value | humanizePercentage }} throttling of CPU in namespace {{ $labels.namespace }} for container {{ $labels.container }} in pod {{ $labels.pod }}.'
        }
        expression: 'sum(increase(container_cpu_cfs_throttled_periods_total{container!="", }[5m])) by (cluster, container, pod, namespace) / sum(increase(container_cpu_cfs_periods_total{}[5m])) by (cluster, container, pod, namespace) > ( 25 / 100 )'
        for: 'PT15M'
        severity: 4
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
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
          title: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
        }
        expression: '( kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"} / kubelet_volume_stats_capacity_bytes{job="kubelet", metrics_path="/metrics"} ) < 0.03 and kubelet_volume_stats_used_bytes{job="kubelet", metrics_path="/metrics"} > 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1M'
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
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
          title: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
        }
        expression: '( kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"} / kubelet_volume_stats_capacity_bytes{job="kubelet", metrics_path="/metrics"} ) < 0.15 and kubelet_volume_stats_used_bytes{job="kubelet", metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
          title: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
        }
        expression: '( kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"} / kubelet_volume_stats_inodes{job="kubelet", metrics_path="/metrics"} ) < 0.03 and kubelet_volume_stats_inodes_used{job="kubelet", metrics_path="/metrics"} > 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1M'
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
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
          title: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
        }
        expression: '( kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"} / kubelet_volume_stats_inodes{job="kubelet", metrics_path="/metrics"} ) < 0.15 and kubelet_volume_stats_inodes_used{job="kubelet", metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
        alert: 'KubePersistentVolumeErrors'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeErrors/{{ $labels.cluster }}/{{ $labels.persistentvolume }}/{{ $labels.phase }}'
          description: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeerrors'
          summary: 'PersistentVolume is having issues with provisioning.'
          title: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeversionmismatch'
          summary: 'Different semantic versions of Kubernetes components running.'
          title: 'There are {{ $value }} different semantic versions of Kubernetes components running.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclienterrors'
          summary: 'Kubernetes API server client is experiencing errors.'
          title: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
          title: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 7.0 days.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
          title: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 24.0 hours.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapierrors'
          summary: 'Kubernetes aggregated API has reported errors.'
          title: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapidown'
          summary: 'Kubernetes aggregated API is down.'
          title: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapidown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'KubeAPI has disappeared from Prometheus target discovery.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodenotready'
          summary: 'Node is not ready.'
          title: '{{ $labels.node }} has been unready for more than 15 minutes.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodeunreachable'
          summary: 'Node is unreachable.'
          title: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubelettoomanypods'
          summary: 'Kubelet is running at capacity.'
          title: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodereadinessflapping'
          summary: 'Node readiness status is flapping.'
          title: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletplegdurationhigh'
          summary: 'Kubelet Pod Lifecycle Event Generator is taking too long to relist.'
          title: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletpodstartuplatencyhigh'
          summary: 'Kubelet Pod startup latency is too high.'
          title: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
          title: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
          title: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
          title: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
          title: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its client certificate.'
          title: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its server certificate.'
          title: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'Kubelet has disappeared from Prometheus target discovery.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeschedulerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'KubeScheduler has disappeared from Prometheus target discovery.'
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
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontrollermanagerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
          title: 'KubeControllerManager has disappeared from Prometheus target discovery.'
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

resource kasMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kas-monitor-rules'
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
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'warning'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels.probe_url }}'
          description: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
          title: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[30m])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[30m]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[30m])) > 5 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[1h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[1h]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[1h])) > 60'
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
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'warning'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels.probe_url }}'
          description: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
          title: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[30m])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[30m]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[30m])) > 30 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[6h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[6h]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[6h])) > 360'
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
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1d'
          severity: 'info'
          short_window: '2h'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels.probe_url }}'
          description: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
          title: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[2h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[2h]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[2h])) > 120 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[1d])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[1d]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[1d])) > 1440'
        for: 'PT1H'
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
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'info'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels.probe_url }}'
          description: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
          title: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[6h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[6h]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[6h])) > 360 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{}[3d])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[3d]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{}[3d])) > 4320'
        for: 'PT3H'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource mgmtCapacityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mgmt-capacity.rules'
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
        alert: 'MgmtClusterHCPCapacityWarning'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityWarning/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is approaching limit (60% threshold).'
          title: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
        }
        expression: '( count(kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) by (cluster) / 60 ) > 0.60'
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
        alert: 'MgmtClusterHCPCapacityCritical'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityCritical/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is critically high (85% threshold).'
          title: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
        }
        expression: '( count(kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) by (cluster) / 60 ) > 0.85'
        for: 'PT5M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
