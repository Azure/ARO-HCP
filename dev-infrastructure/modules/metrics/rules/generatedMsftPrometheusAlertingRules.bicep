#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource msftKubernetesApps 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msft-kubernetes-apps'
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
        alert: 'KubePodCrashLooping'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodCrashLooping/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.container }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          info: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping'
          summary: 'Pod is crash looping.'
          title: 'Pod is crash looping. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }} container:{{ $labels.container }}'
        }
        expression: 'max_over_time(kube_pod_container_status_waiting_reason{job="kube-state-metrics",namespace=~"billing|credential-refresher",reason="CrashLoopBackOff"}[5m]) >= 1'
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
        alert: 'KubePodNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodNotReady/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 15 minutes.'
          info: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodnotready'
          summary: 'Pod has been in a non-ready state for more than 15 minutes.'
          title: 'Pod has been in a non-ready state for more than 15 minutes. namespace:{{ $labels.namespace }} pod:{{ $labels.pod }}'
        }
        expression: 'sum by (namespace, pod, cluster) (max by (namespace, pod, cluster) (kube_pod_status_phase{job="kube-state-metrics",namespace=~"billing|credential-refresher",phase=~"Pending|Unknown|Failed"}) * on (namespace, pod, cluster) group_left (owner_kind) topk by (namespace, pod, cluster) (1, max by (namespace, pod, owner_kind, cluster) (kube_pod_owner{namespace=~"billing|credential-refresher",owner_kind!="Job"}))) > 0'
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
        alert: 'KubeDeploymentGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentGenerationMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          info: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentgenerationmismatch'
          summary: 'Deployment generation mismatch due to possible roll-back'
          title: 'Deployment generation mismatch due to possible roll-back namespace:{{ $labels.namespace }} deployment:{{ $labels.deployment }}'
        }
        expression: 'kube_deployment_status_observed_generation{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_deployment_metadata_generation{job="kube-state-metrics",namespace=~"billing|credential-refresher"}'
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
        alert: 'KubeDeploymentReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 30 minutes.'
          info: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 30 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentreplicasmismatch'
          summary: 'Deployment has not matched the expected number of replicas.'
          title: 'Deployment has not matched the expected number of replicas. namespace:{{ $labels.namespace }} deployment:{{ $labels.deployment }}'
        }
        expression: '(kube_deployment_spec_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > kube_deployment_status_replicas_available{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) and (changes(kube_deployment_status_replicas_updated{job="kube-state-metrics",namespace=~"billing|credential-refresher"}[10m]) == 0)'
        for: 'PT30M'
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
        alert: 'KubeDeploymentRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentRolloutStuck/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 30 minutes.'
          info: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 30 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentrolloutstuck'
          summary: 'Deployment rollout is not progressing.'
          title: 'Deployment rollout is not progressing. namespace:{{ $labels.namespace }} deployment:{{ $labels.deployment }}'
        }
        expression: 'kube_deployment_status_condition{condition="Progressing",job="kube-state-metrics",namespace=~"billing|credential-refresher",status="false"} != 0'
        for: 'PT30M'
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
        alert: 'KubeStatefulSetReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          info: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetreplicasmismatch'
          summary: 'StatefulSet has not matched the expected number of replicas.'
          title: 'StatefulSet has not matched the expected number of replicas. namespace:{{ $labels.namespace }} statefulset:{{ $labels.statefulset }}'
        }
        expression: '(kube_statefulset_status_replicas_ready{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_statefulset_status_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) and (changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics",namespace=~"billing|credential-refresher"}[10m]) == 0)'
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
        alert: 'KubeStatefulSetGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetGenerationMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          info: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetgenerationmismatch'
          summary: 'StatefulSet generation mismatch due to possible roll-back'
          title: 'StatefulSet generation mismatch due to possible roll-back namespace:{{ $labels.namespace }} statefulset:{{ $labels.statefulset }}'
        }
        expression: 'kube_statefulset_status_observed_generation{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_statefulset_metadata_generation{job="kube-state-metrics",namespace=~"billing|credential-refresher"}'
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
        alert: 'KubeStatefulSetUpdateNotRolledOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetUpdateNotRolledOut/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          info: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetupdatenotrolledout'
          summary: 'StatefulSet update has not been rolled out.'
          title: 'StatefulSet update has not been rolled out. namespace:{{ $labels.namespace }} statefulset:{{ $labels.statefulset }}'
        }
        expression: '(max by (namespace, statefulset, job, cluster) (kube_statefulset_status_current_revision{job="kube-state-metrics",namespace=~"billing|credential-refresher"} unless kube_statefulset_status_update_revision{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) * (kube_statefulset_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_statefulset_status_replicas_updated{job="kube-state-metrics",namespace=~"billing|credential-refresher"})) and (changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics",namespace=~"billing|credential-refresher"}[5m]) == 0)'
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
        alert: 'KubeDaemonSetRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetRolloutStuck/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          info: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetrolloutstuck'
          summary: 'DaemonSet rollout is stuck.'
          title: 'DaemonSet rollout is stuck. namespace:{{ $labels.namespace }} daemonset:{{ $labels.daemonset }}'
        }
        expression: '((kube_daemonset_status_current_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) or (kube_daemonset_status_number_misscheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != 0) or (kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) or (kube_daemonset_status_number_available{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"})) and (changes(kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"}[5m]) == 0)'
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
        alert: 'KubeContainerWaiting'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeContainerWaiting/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          info: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontainerwaiting'
          summary: 'Pod container waiting longer than 1 hour'
          title: 'Pod container waiting longer than 1 hour namespace:{{ $labels.namespace }} pod:{{ $labels.pod }}'
        }
        expression: 'sum by (namespace, pod, container, cluster) (kube_pod_container_status_waiting_reason{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) > 0'
        for: 'PT1H'
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
        alert: 'KubeDaemonSetNotScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetNotScheduled/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          info: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetnotscheduled'
          summary: 'DaemonSet pods are not scheduled.'
          title: 'DaemonSet pods are not scheduled. namespace:{{ $labels.namespace }} daemonset:{{ $labels.daemonset }}'
        }
        expression: 'kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} - kube_daemonset_status_current_number_scheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > 0'
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
        alert: 'KubeDaemonSetMisScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetMisScheduled/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          info: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetmisscheduled'
          summary: 'DaemonSet pods are misscheduled.'
          title: 'DaemonSet pods are misscheduled. namespace:{{ $labels.namespace }} daemonset:{{ $labels.daemonset }}'
        }
        expression: 'kube_daemonset_status_number_misscheduled{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > 0'
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
        alert: 'KubeJobNotCompleted'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobNotCompleted/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.job_name }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          info: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobnotcompleted'
          summary: 'Job did not complete in time'
          title: 'Job did not complete in time namespace:{{ $labels.namespace }} job_name:{{ $labels.job_name }}'
        }
        expression: 'time() - max by (namespace, job_name, cluster) (kube_job_status_start_time{job="kube-state-metrics",namespace=~"billing|credential-refresher"} and kube_job_status_active{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > 0) > 43200'
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
        alert: 'KubeJobFailed'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobFailed/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.job_name }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          info: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobfailed'
          summary: 'Job failed to complete.'
          title: 'Job failed to complete. namespace:{{ $labels.namespace }} job_name:{{ $labels.job_name }}'
        }
        expression: 'kube_job_failed{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > 0'
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
        alert: 'KubeHpaReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          info: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpareplicasmismatch'
          summary: 'HPA has not matched desired number of replicas.'
          title: 'HPA has not matched desired number of replicas. namespace:{{ $labels.namespace }}'
        }
        expression: '(kube_horizontalpodautoscaler_status_desired_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} != kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} > kube_horizontalpodautoscaler_spec_min_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} < kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}) and changes(kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}[15m]) == 0'
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
        alert: 'KubeHpaMaxedOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaMaxedOut/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          info: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpamaxedout'
          summary: 'HPA is running at max replicas'
          title: 'HPA is running at max replicas namespace:{{ $labels.namespace }}'
        }
        expression: 'kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"} == kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics",namespace=~"billing|credential-refresher"}'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource msftKubernetesResources 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msft-kubernetes-resources'
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
          title: 'Namespace quota is going to be full. namespace:{{ $labels.namespace }} resource:{{ $labels.resource }}'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="hard"} > 0) > 0.9 < 1'
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
          title: 'Namespace quota is fully used. namespace:{{ $labels.namespace }} resource:{{ $labels.resource }}'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="hard"} > 0) == 1'
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
          title: 'Namespace quota has exceeded the limits. namespace:{{ $labels.namespace }} resource:{{ $labels.resource }}'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",namespace=~"billing|credential-refresher",type="hard"} > 0) > 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource msftKubernetesStorage 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msft-kubernetes-storage'
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
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          info: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
          title: 'PersistentVolume is filling up. persistentvolumeclaim:{{ $labels.persistentvolumeclaim }} namespace:{{ $labels.namespace }}'
        }
        expression: '(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} / kubelet_volume_stats_capacity_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}) < 0.03 and kubelet_volume_stats_used_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} > 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany",namespace=~"billing|credential-refresher"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true",namespace=~"billing|credential-refresher"} == 1'
        for: 'PT1M'
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
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          info: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
          title: 'PersistentVolume is filling up. persistentvolumeclaim:{{ $labels.persistentvolumeclaim }} namespace:{{ $labels.namespace }}'
        }
        expression: '(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} / kubelet_volume_stats_capacity_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}) < 0.15 and kubelet_volume_stats_used_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} > 0 and predict_linear(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}[6h], 4 * 24 * 3600) < 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany",namespace=~"billing|credential-refresher"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true",namespace=~"billing|credential-refresher"} == 1'
        for: 'PT1H'
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
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          info: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
          title: 'PersistentVolumeInodes are filling up. persistentvolumeclaim:{{ $labels.persistentvolumeclaim }} namespace:{{ $labels.namespace }}'
        }
        expression: '(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} / kubelet_volume_stats_inodes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}) < 0.03 and kubelet_volume_stats_inodes_used{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} > 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany",namespace=~"billing|credential-refresher"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true",namespace=~"billing|credential-refresher"} == 1'
        for: 'PT1M'
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
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          info: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
          title: 'PersistentVolumeInodes are filling up. persistentvolumeclaim:{{ $labels.persistentvolumeclaim }} namespace:{{ $labels.namespace }}'
        }
        expression: '(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} / kubelet_volume_stats_inodes{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}) < 0.15 and kubelet_volume_stats_inodes_used{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"} > 0 and predict_linear(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics",namespace=~"billing|credential-refresher"}[6h], 4 * 24 * 3600) < 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany",namespace=~"billing|credential-refresher"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true",namespace=~"billing|credential-refresher"} == 1'
        for: 'PT1H'
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
          title: 'PersistentVolume is having issues with provisioning. persistentvolume:{{ $labels.persistentvolume }} phase:{{ $labels.phase }}'
        }
        expression: 'kube_persistentvolume_status_phase{job="kube-state-metrics",namespace=~"billing|credential-refresher",phase=~"Failed|Pending"} > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource msftMise 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msft-mise'
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
        alert: 'MiseEnvoyScrapeDown'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'MiseEnvoyScrapeDown/{{ $labels.cluster }}'
          description: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
          info: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/mise-tsg.html'
          summary: 'Envoy scrape target down for namespace=mise'
          title: 'Envoy scrape target down for namespace=mise'
        }
        expression: 'group by (cluster) (up{cluster=~".*-svc(-[0-9]+)?$",job="kube-state-metrics"}) unless on (cluster) group by (cluster) (up{container="istio-proxy",endpoint="http-envoy-prom",namespace="mise"} == 1)'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource msftMsiCredentialRefresher 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'msft-msi-credential-refresher'
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
        alert: 'ClusterCredentialExpiringSoon'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'ClusterCredentialExpiringSoon/{{ $labels.cluster }}'
          description: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} are expiring in less than 30 days.'
          info: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} are expiring in less than 30 days.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/troubleshooting/tsgs/credential-refresher-expiring-cert'
          summary: 'Customer cluster credential expiring in less than 30 days'
          title: 'Customer cluster credential expiring in less than 30 days'
        }
        expression: 'sum by (cluster) (increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^30([.]0)?$"}[30m])) - sum by (cluster) (increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^0([.]0)?$"}[30m])) > 0'
        for: 'PT5M'
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
        alert: 'ClusterCredentialExpired'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'ClusterCredentialExpired/{{ $labels.cluster }}'
          description: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} expired.'
          info: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} expired.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/troubleshooting/tsgs/credential-refresher-expiring-cert'
          summary: 'Customer cluster credential expired'
          title: 'Customer cluster credential expired'
        }
        expression: 'sum by (cluster) (increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^0([.]0)?$"}[30m])) - sum by (cluster) (increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^-90([.]0)?$"}[30m])) > 0'
        for: 'PT5M'
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
        alert: 'ClusterCredentialNotRenewable'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'ClusterCredentialNotRenewable/{{ $labels.cluster }}'
          description: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} are no longer renewable.'
          info: 'Credential(s) for customer cluster monitored by {{ $labels.cluster }} are no longer renewable.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/troubleshooting/tsgs/credential-refresher-expiring-cert'
          summary: 'Customer cluster credential is no longer renewable'
          title: 'Customer cluster credential is no longer renewable'
        }
        expression: 'sum by (cluster) (increase(credential_refresher_days_until_msi_credential_expiration_bucket{le=~"^-90([.]0)?$"}[30m])) > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
