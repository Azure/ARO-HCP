#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource svcKubernetesApps 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-apps'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodCrashLooping/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.container }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          info: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping'
          summary: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} (container {{ $labels.container }}) is crash looping.'
          title: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} (container {{ $labels.container }}) is crash looping.'
        }
        expression: 'max_over_time(kube_pod_container_status_waiting_reason{job="kube-state-metrics",reason="CrashLoopBackOff"}[5m]) >= 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodNotReady/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 5 minutes.'
          info: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 5 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodnotready'
          summary: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for more than 5 minutes.'
          title: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for more than 5 minutes.'
        }
        expression: 'sum by (namespace, pod, cluster) (max by (namespace, pod, cluster) (kube_pod_status_phase{job="kube-state-metrics",namespace!~"klusterlet-.*",phase=~"Pending|Unknown|Failed",prometheus="prometheus/prometheus"}) * on (namespace, pod, cluster) group_left (owner_kind) topk by (namespace, pod, cluster) (1, max by (namespace, pod, owner_kind, cluster) (kube_pod_owner{owner_kind!="Job",prometheus="prometheus/prometheus"}))) > 0'
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
        alert: 'KubeDeploymentGenerationMismatch'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentGenerationMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          info: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentgenerationmismatch'
          summary: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} generation mismatch due to possible roll-back'
          title: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} generation mismatch due to possible roll-back'
        }
        expression: 'kube_deployment_status_observed_generation{job="kube-state-metrics"} != kube_deployment_metadata_generation{job="kube-state-metrics"}'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 30 minutes.'
          info: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 30 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentreplicasmismatch'
          summary: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas.'
          title: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas.'
        }
        expression: '(kube_deployment_spec_replicas{job="kube-state-metrics"} > kube_deployment_status_replicas_available{job="kube-state-metrics"}) and (changes(kube_deployment_status_replicas_updated{job="kube-state-metrics"}[10m]) == 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDeploymentRolloutStuck/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.deployment }}'
          description: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 30 minutes.'
          info: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 30 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentrolloutstuck'
          summary: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} rollout is not progressing.'
          title: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} rollout is not progressing.'
        }
        expression: 'kube_deployment_status_condition{condition="Progressing",job="kube-state-metrics",status="false"} != 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          info: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetreplicasmismatch'
          summary: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas.'
          title: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas.'
        }
        expression: '(kube_statefulset_status_replicas_ready{job="kube-state-metrics"} != kube_statefulset_status_replicas{job="kube-state-metrics"}) and (changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[10m]) == 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetGenerationMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          info: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetgenerationmismatch'
          summary: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} generation mismatch due to possible roll-back'
          title: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} generation mismatch due to possible roll-back'
        }
        expression: 'kube_statefulset_status_observed_generation{job="kube-state-metrics"} != kube_statefulset_metadata_generation{job="kube-state-metrics"}'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeStatefulSetUpdateNotRolledOut/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.statefulset }}'
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          info: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetupdatenotrolledout'
          summary: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          title: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
        }
        expression: '(max by (namespace, statefulset, job, cluster) (kube_statefulset_status_current_revision{job="kube-state-metrics"} unless kube_statefulset_status_update_revision{job="kube-state-metrics"}) * (kube_statefulset_replicas{job="kube-state-metrics"} != kube_statefulset_status_replicas_updated{job="kube-state-metrics"})) and (changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[5m]) == 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetRolloutStuck/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          info: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetrolloutstuck'
          summary: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} rollout is stuck.'
          title: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} rollout is stuck.'
        }
        expression: '((kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}) or (kube_daemonset_status_number_misscheduled{job="kube-state-metrics"} != 0) or (kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}) or (kube_daemonset_status_number_available{job="kube-state-metrics"} != kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"})) and (changes(kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"}[5m]) == 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeContainerWaiting/{{ $labels.cluster }}/{{ $labels.pod }}/{{ $labels.namespace }}/{{ $labels.container }}'
          description: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          info: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontainerwaiting'
          summary: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} container {{ $labels.container }} waiting longer than 1 hour'
          title: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} container {{ $labels.container }} waiting longer than 1 hour'
        }
        expression: 'sum by (namespace, pod, container, cluster) (kube_pod_container_status_waiting_reason{job="kube-state-metrics"}) > 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetNotScheduled/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          info: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetnotscheduled'
          summary: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} pods are not scheduled.'
          title: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} pods are not scheduled.'
        }
        expression: 'kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"} - kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"} > 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeDaemonSetMisScheduled/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.daemonset }}'
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          info: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetmisscheduled'
          summary: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} pods are misscheduled.'
          title: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} pods are misscheduled.'
        }
        expression: 'kube_daemonset_status_number_misscheduled{job="kube-state-metrics"} > 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobNotCompleted/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.job_name }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          info: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobnotcompleted'
          summary: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} did not complete in time'
          title: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} did not complete in time'
        }
        expression: 'time() - max by (namespace, job_name, cluster) (kube_job_status_start_time{job="kube-state-metrics"} and kube_job_status_active{job="kube-state-metrics"} > 0) > 43200'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeJobFailed/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.job_name }}'
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          info: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobfailed'
          summary: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete.'
          title: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete.'
        }
        expression: 'kube_job_failed{job="kube-state-metrics"} > 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaReplicasMismatch/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          info: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpareplicasmismatch'
          summary: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }} has not matched desired number of replicas.'
          title: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }} has not matched desired number of replicas.'
        }
        expression: '(kube_horizontalpodautoscaler_status_desired_replicas{job="kube-state-metrics"} != kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} > kube_horizontalpodautoscaler_spec_min_replicas{job="kube-state-metrics"}) and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} < kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}) and changes(kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}[15m]) == 0'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeHpaMaxedOut/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }}'
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          info: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpamaxedout'
          summary: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }} is running at max replicas'
          title: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler }} is running at max replicas'
        }
        expression: 'kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} == kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesResources 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-resources'
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
        alert: 'KubeCPUOvercommit'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeCPUOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests for Pods by {{ $value }} CPU shares and cannot tolerate node failure.'
          info: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests for Pods by {{ $value }} CPU shares and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuovercommit'
          summary: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests.'
        }
        expression: 'sum by (cluster) (namespace_cpu:kube_pod_container_resource_requests:sum) - (sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) - max by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"})) > 0 and (sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) - max by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"})) > 0'
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
        alert: 'KubeMemoryOvercommit'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          info: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryovercommit'
          summary: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests.'
        }
        expression: 'sum by (cluster) (namespace_memory:kube_pod_container_resource_requests:sum) - (sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="memory"}) - max by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="memory"})) > 0 and (sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="memory"}) - max by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="memory"})) > 0'
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
        alert: 'KubeCPUQuotaOvercommit'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeCPUQuotaOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
          info: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuquotaovercommit'
          summary: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource quota requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource quota requests.'
        }
        expression: 'sum by (cluster) (min without (resource) (kube_resourcequota{job="kube-state-metrics",resource=~"(cpu|requests.cpu)",type="hard"})) / sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) > 1.5'
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
        alert: 'KubeMemoryQuotaOvercommit'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeMemoryQuotaOvercommit/{{ $labels.cluster }}'
          description: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
          info: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryquotaovercommit'
          summary: 'Cluster {{ $labels.cluster }} has overcommitted memory resource quota requests.'
          title: 'Cluster {{ $labels.cluster }} has overcommitted memory resource quota requests.'
        }
        expression: 'sum by (cluster) (min without (resource) (kube_resourcequota{job="kube-state-metrics",resource=~"(memory|requests.memory)",type="hard"})) / sum by (cluster) (kube_node_status_allocatable{job="kube-state-metrics",resource="memory"}) > 1.5'
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
        alert: 'KubeQuotaAlmostFull'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeQuotaAlmostFull/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaalmostfull'
          summary: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} is going to be full.'
          title: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} is going to be full.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",type="hard"} > 0) > 0.9 < 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeQuotaFullyUsed/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotafullyused'
          summary: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} is fully used.'
          title: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} is fully used.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",type="hard"} > 0) == 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeQuotaExceeded/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.resource }}'
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          info: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaexceeded'
          summary: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} has exceeded the limits.'
          title: 'Namespace {{ $labels.namespace }} quota for {{ $labels.resource }} has exceeded the limits.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics",type="used"} / ignoring (instance, job, type) (kube_resourcequota{job="kube-state-metrics",type="hard"} > 0) > 1'
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
        alert: 'CPUThrottlingHigh'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'info'
        }
        annotations: {
          correlationId: 'CPUThrottlingHigh/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.container }}/{{ $labels.pod }}'
          description: '{{ $value | humanizePercentage }} throttling of CPU in namespace {{ $labels.namespace }} for container {{ $labels.container }} in pod {{ $labels.pod }}.'
          info: '{{ $value | humanizePercentage }} throttling of CPU in namespace {{ $labels.namespace }} for container {{ $labels.container }} in pod {{ $labels.pod }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh'
          summary: 'Processes in {{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.container }} experience elevated CPU throttling.'
          title: 'Processes in {{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.container }} experience elevated CPU throttling.'
        }
        expression: 'sum by (cluster, container, pod, namespace) (increase(container_cpu_cfs_throttled_periods_total{container!=""}[5m])) / sum by (cluster, container, pod, namespace) (increase(container_cpu_cfs_periods_total[5m])) > (25 / 100)'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesStorage 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-storage'
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
          component: 'kubernetes-infrastructure'
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          info: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} is filling up.'
          title: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} is filling up.'
        }
        expression: '(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics"} / kubelet_volume_stats_capacity_bytes{job="kubelet",metrics_path="/metrics"}) < 0.03 and kubelet_volume_stats_used_bytes{job="kubelet",metrics_path="/metrics"} > 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          info: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} is filling up.'
          title: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} is filling up.'
        }
        expression: '(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics"} / kubelet_volume_stats_capacity_bytes{job="kubelet",metrics_path="/metrics"}) < 0.15 and kubelet_volume_stats_used_bytes{job="kubelet",metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_available_bytes{job="kubelet",metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          info: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} inodes are filling up.'
          title: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} inodes are filling up.'
        }
        expression: '(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics"} / kubelet_volume_stats_inodes{job="kubelet",metrics_path="/metrics"}) < 0.03 and kubelet_volume_stats_inodes_used{job="kubelet",metrics_path="/metrics"} > 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeInodesFillingUp/{{ $labels.cluster }}/{{ $labels.persistentvolumeclaim }}/{{ $labels.namespace }}'
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          info: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} inodes are filling up.'
          title: 'PersistentVolumeClaim {{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} on {{ $labels.cluster }} inodes are filling up.'
        }
        expression: '(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics"} / kubelet_volume_stats_inodes{job="kubelet",metrics_path="/metrics"}) < 0.15 and kubelet_volume_stats_inodes_used{job="kubelet",metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_inodes_free{job="kubelet",metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{access_mode="ReadOnlyMany"} == 1 unless on (cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
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
          component: 'kubernetes-infrastructure'
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubePersistentVolumeErrors/{{ $labels.cluster }}/{{ $labels.persistentvolume }}/{{ $labels.phase }}'
          description: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          info: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeerrors'
          summary: 'PersistentVolume {{ $labels.persistentvolume }} on {{ $labels.cluster }} is having issues ({{ $labels.phase }}).'
          title: 'PersistentVolume {{ $labels.persistentvolume }} on {{ $labels.cluster }} is having issues ({{ $labels.phase }}).'
        }
        expression: 'kube_persistentvolume_status_phase{job="kube-state-metrics",phase=~"Failed|Pending"} > 0'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesSystem 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-system'
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
        alert: 'KubeVersionMismatch'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'count by (cluster) (count by (git_version, cluster) (label_replace(kubernetes_build_info{job!~"kube-dns|coredns"}, "git_version", "$1", "git_version", "(v[0-9]*.[0-9]*).*"))) > 1'
        for: 'PT1H30M'
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
        alert: 'KubeClientErrors'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeClientErrors/{{ $labels.cluster }}/{{ $labels.job }}/{{ $labels.instance }}'
          description: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
          info: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclienterrors'
          summary: 'Kubernetes API server client {{ $labels.job }}/{{ $labels.instance }} is experiencing errors.'
          title: 'Kubernetes API server client {{ $labels.job }}/{{ $labels.instance }} is experiencing errors.'
        }
        expression: '(sum by (cluster, instance, job, namespace) (rate(rest_client_requests_total{code=~"5..",job="controlplane-apiserver"}[5m])) / sum by (cluster, instance, job, namespace) (rate(rest_client_requests_total{job="controlplane-apiserver"}[5m]))) > 0.01'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubeApiserverSlos 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kube-apiserver-slos'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'sum(apiserver_request:burnrate1h) > (14.4 * 0.01) and sum(apiserver_request:burnrate5m) > (14.4 * 0.01)'
        for: 'PT2M'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'sum(apiserver_request:burnrate6h) > (6 * 0.01) and sum(apiserver_request:burnrate30m) > (6 * 0.01)'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'sum(apiserver_request:burnrate1d) > (3 * 0.01) and sum(apiserver_request:burnrate2h) > (3 * 0.01)'
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
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'sum(apiserver_request:burnrate3d) > (1 * 0.01) and sum(apiserver_request:burnrate6h) > (1 * 0.01)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesSystemApiserver 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-system-apiserver'
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
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="controlplane-apiserver"} > 0 and on (job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="controlplane-apiserver"}[5m]))) < 604800'
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
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="controlplane-apiserver"} > 0 and on (job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="controlplane-apiserver"}[5m]))) < 86400'
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
        alert: 'KubeAggregatedAPIErrors'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeAggregatedAPIErrors/{{ $labels.cluster }}/{{ $labels.name }}/{{ $labels.namespace }}'
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
          info: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapierrors'
          summary: 'Kubernetes aggregated API {{ $labels.namespace }}/{{ $labels.name }} has reported errors.'
          title: 'Kubernetes aggregated API {{ $labels.namespace }}/{{ $labels.name }} has reported errors.'
        }
        expression: 'sum by (name, namespace, cluster) (increase(aggregator_unavailable_apiservice_total{job="controlplane-apiserver"}[10m])) > 4'
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
        alert: 'KubeAggregatedAPIDown'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeAggregatedAPIDown/{{ $labels.cluster }}/{{ $labels.name }}/{{ $labels.namespace }}'
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
          info: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapidown'
          summary: 'Kubernetes aggregated API {{ $labels.namespace }}/{{ $labels.name }} is down.'
          title: 'Kubernetes aggregated API {{ $labels.namespace }}/{{ $labels.name }} is down.'
        }
        expression: '(1 - max by (name, namespace, cluster) (avg_over_time(aggregator_unavailable_apiservice{job="controlplane-apiserver"}[10m]))) * 100 < 85'
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
        alert: 'KubeAPIDown'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'count by (cluster) (up{job="controlplane-apiserver"} == 1) == 0'
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
        alert: 'KubeAPITerminatedRequests'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'sum(rate(apiserver_request_terminations_total{job="controlplane-apiserver"}[10m])) / (sum(rate(apiserver_request_total{job="controlplane-apiserver"}[10m])) + sum(rate(apiserver_request_terminations_total{job="controlplane-apiserver"}[10m]))) > 0.2'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesSystemKubelet 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-system-kubelet'
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
        alert: 'KubeNodeNotReady'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeNotReady/{{ $labels.cluster }}/{{ $labels.node }}'
          description: '{{ $labels.node }} has been unready for more than 30 minutes.'
          info: '{{ $labels.node }} has been unready for more than 30 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodenotready'
          summary: 'Node {{ $labels.node }} is not ready.'
          title: 'Node {{ $labels.node }} is not ready.'
        }
        expression: 'kube_node_status_condition{condition="Ready",job="kube-state-metrics",status="true"} == 0'
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
        alert: 'KubeNodeUnreachable'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeUnreachable/{{ $labels.cluster }}/{{ $labels.node }}'
          description: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
          info: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodeunreachable'
          summary: 'Node {{ $labels.node }} is unreachable.'
          title: 'Node {{ $labels.node }} is unreachable.'
        }
        expression: '(kube_node_spec_taint{effect="NoSchedule",job="kube-state-metrics",key="node.kubernetes.io/unreachable"} unless ignoring (key, value) kube_node_spec_taint{job="kube-state-metrics",key=~"ToBeDeletedByClusterAutoscaler|cloud.google.com/impending-node-termination|aws-node-termination-handler/spot-itn"}) == 1'
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
        alert: 'KubeletTooManyPods'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'info'
        }
        annotations: {
          correlationId: 'KubeletTooManyPods/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
          info: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubelettoomanypods'
          summary: 'Kubelet on node {{ $labels.node }} is running at capacity.'
          title: 'Kubelet on node {{ $labels.node }} is running at capacity.'
        }
        expression: 'count by (cluster, node) ((kube_pod_status_phase{job="kube-state-metrics",phase="Running"} == 1) * on (instance, pod, namespace, cluster) group_left (node) topk by (instance, pod, namespace, cluster) (1, kube_pod_info{job="kube-state-metrics"})) / max by (cluster, node) (kube_node_status_capacity{job="kube-state-metrics",resource="pods"} != 1) > 0.95'
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
        alert: 'KubeNodeReadinessFlapping'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeNodeReadinessFlapping/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
          info: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodereadinessflapping'
          summary: 'Node {{ $labels.node }} readiness status is flapping.'
          title: 'Node {{ $labels.node }} readiness status is flapping.'
        }
        expression: 'sum by (cluster, node) (changes(kube_node_status_condition{condition="Ready",job="kube-state-metrics",status="true"}[15m])) > 2'
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
        alert: 'KubeletPlegDurationHigh'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletPlegDurationHigh/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
          info: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletplegdurationhigh'
          summary: 'Kubelet on node {{ $labels.node }} Pod Lifecycle Event Generator is taking too long to relist.'
          title: 'Kubelet on node {{ $labels.node }} Pod Lifecycle Event Generator is taking too long to relist.'
        }
        expression: 'node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile{quantile="0.99"} >= 10'
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
        alert: 'KubeletPodStartUpLatencyHigh'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletPodStartUpLatencyHigh/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
          info: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletpodstartuplatencyhigh'
          summary: 'Kubelet on node {{ $labels.node }} Pod startup latency is too high.'
          title: 'Kubelet on node {{ $labels.node }} Pod startup latency is too high.'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, instance, le) (rate(kubelet_pod_worker_duration_seconds_bucket{job="kubelet",metrics_path="/metrics"}[5m]))) * on (cluster, instance) group_left (node) kubelet_node_name{job="kubelet",metrics_path="/metrics"} > 60'
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
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet on node {{ $labels.node }} client certificate is about to expire.'
          title: 'Kubelet on node {{ $labels.node }} client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 604800'
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
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet on node {{ $labels.node }} client certificate is about to expire.'
          title: 'Kubelet on node {{ $labels.node }} client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 86400'
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
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet on node {{ $labels.node }} server certificate is about to expire.'
          title: 'Kubelet on node {{ $labels.node }} server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 604800'
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
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'critical'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateExpiration/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          info: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet on node {{ $labels.node }} server certificate is about to expire.'
          title: 'Kubelet on node {{ $labels.node }} server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 86400'
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
        alert: 'KubeletClientCertificateRenewalErrors'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletClientCertificateRenewalErrors/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          info: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificaterenewalerrors'
          summary: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate.'
          title: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate.'
        }
        expression: 'increase(kubelet_certificate_manager_client_expiration_renew_errors[5m]) > 0'
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
        alert: 'KubeletServerCertificateRenewalErrors'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeletServerCertificateRenewalErrors/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          info: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificaterenewalerrors'
          summary: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate.'
          title: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate.'
        }
        expression: 'increase(kubelet_server_expiration_renew_errors[5m]) > 0'
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
        alert: 'KubeletDown'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'count by (cluster) (up{job="kubelet",metrics_path="/metrics"} == 1) == 0'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesSystemScheduler 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-system-scheduler'
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
        alert: 'KubeSchedulerDown'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'count by (cluster) (up{job="controlplane-kube-scheduler"} == 1) == 0'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcKubernetesSystemControllerManager 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-kubernetes-system-controller-manager'
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
        alert: 'KubeControllerManagerDown'
        enabled: true
        labels: {
          component: 'kubernetes-infrastructure'
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
        expression: 'count by (cluster) (up{job="controlplane-kube-controller-manager"} == 1) == 0'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcFrontendPathLatency 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-frontend-path-latency'
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
        alert: 'FrontendPathLatency'
        enabled: true
        labels: {
          component: 'frontend'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FrontendPathLatency/{{ $labels.cluster }}/{{ $labels.method }}/{{ $labels.route }}'
          description: 'More than 5% of frontend requests for {{ $labels.method }} {{ $labels.route }} exceeded 1 second over the past 30 minutes (cluster {{ $labels.cluster }}).'
          info: 'More than 5% of frontend requests for {{ $labels.method }} {{ $labels.route }} exceeded 1 second over the past 30 minutes (cluster {{ $labels.cluster }}).'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend latency is high: over 5% of {{ $labels.method }} {{ $labels.route }} requests on cluster {{ $labels.cluster }} exceeded 1 second over the past 30 minutes'
          title: 'Frontend latency is high: over 5% of {{ $labels.method }} {{ $labels.route }} requests on cluster {{ $labels.cluster }} exceeded 1 second over the past 30 minutes'
        }
        expression: '((sum by (route, method, cluster) (rate(frontend_http_requests_duration_seconds_count{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m])) - sum by (route, method, cluster) (rate(frontend_http_requests_duration_seconds_bucket{le="1",route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m]))) / sum by (route, method, cluster) (rate(frontend_http_requests_duration_seconds_count{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m]))) > 0.05 and on (route, method, cluster) (sum by (route, method, cluster) (max without (prometheus_replica) (increase(frontend_http_requests_duration_seconds_count{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m])))) >= 50 and on (route, method, cluster) (sum by (route, method, cluster) (max without (prometheus_replica) (increase(frontend_http_requests_duration_seconds_count{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m]))) - sum by (route, method, cluster) (max without (prometheus_replica) (increase(frontend_http_requests_duration_seconds_bucket{le="1",route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m])))) >= 3'
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
        alert: 'FrontendPathMedianLatency'
        enabled: true
        labels: {
          component: 'frontend'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'FrontendPathMedianLatency/{{ $labels.cluster }}/{{ $labels.method }}/{{ $labels.route }}'
          description: 'The median (p50) of frontend request latency for {{ $labels.method }} {{ $labels.route }} has exceeded 250ms over the past 30 minutes (cluster {{ $labels.cluster }}).'
          info: 'The median (p50) of frontend request latency for {{ $labels.method }} {{ $labels.route }} has exceeded 250ms over the past 30 minutes (cluster {{ $labels.cluster }}).'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend median latency is high: p50 exceeds 250ms for {{ $labels.method }} {{ $labels.route }} on cluster {{ $labels.cluster }} over the past 30 minutes'
          title: 'Frontend median latency is high: p50 exceeds 250ms for {{ $labels.method }} {{ $labels.route }} on cluster {{ $labels.cluster }} over the past 30 minutes'
        }
        expression: 'histogram_quantile(0.5, sum by (le, route, method, cluster) (rate(frontend_http_requests_duration_seconds_bucket{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m]))) > 0.25 and on (route, method, cluster) (sum by (route, method, cluster) (max without (prometheus_replica) (increase(frontend_http_requests_duration_seconds_count{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m])))) >= 10'
        for: 'PT1M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource svcServiceMemoryResourcesRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'svc-service-memory-resources-rules'
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
        alert: 'ServiceMemoryDrift'
        enabled: true
        labels: {
          component: 'service-memory'
          severity: 'warning'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'ServiceMemoryDrift/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.pod }}/{{ $labels.namespace }}'
          description: '''Container {{ $labels.container }} in pod {{ $labels.pod }} (namespace {{ $labels.namespace }}) on cluster {{ $labels.cluster }} is using {{ $value | humanizePercentage }} of its memory request for more than 15 minutes.
This may indicate a memory leak or workload growth that requires right-sizing the request in config.yaml.
'''
          info: '''Container {{ $labels.container }} in pod {{ $labels.pod }} (namespace {{ $labels.namespace }}) on cluster {{ $labels.cluster }} is using {{ $value | humanizePercentage }} of its memory request for more than 15 minutes.
This may indicate a memory leak or workload growth that requires right-sizing the request in config.yaml.
'''
          owning_team: 'hcp-sl'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/service-memory-resources.md'
          summary: '{{ $labels.container }} in {{ $labels.namespace }} exceeds 1.5x its memory request on cluster {{ $labels.cluster }}. pod:{{ $labels.pod }}'
          title: '{{ $labels.container }} in {{ $labels.namespace }} exceeds 1.5x its memory request on cluster {{ $labels.cluster }}. pod:{{ $labels.pod }}'
        }
        expression: '(container_memory_working_set_bytes{container!="",namespace=~"aro-hcp|aro-hcp-admin-api|aro-hcp-exporter|arobit|clusters-service|fleet|kube-applier|maestro|mgmt-agent|prometheus|secret-sync-controller|sessiongate"} / on (namespace, pod, container, cluster) group_left () max by (namespace, pod, container, cluster) (kube_pod_container_resource_requests{job="kube-state-metrics",namespace=~"aro-hcp|aro-hcp-admin-api|aro-hcp-exporter|arobit|clusters-service|fleet|kube-applier|maestro|mgmt-agent|prometheus|secret-sync-controller|sessiongate",resource="memory"})) > 1.5'
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
        alert: 'ServiceMemoryTrend'
        enabled: true
        labels: {
          component: 'service-memory'
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'ServiceMemoryTrend/{{ $labels.cluster }}/{{ $labels.container }}/{{ $labels.pod }}/{{ $labels.namespace }}'
          description: '''Container {{ $labels.container }} in pod {{ $labels.pod }} (namespace {{ $labels.namespace }}) on cluster {{ $labels.cluster }} memory is growing steadily.
At the current rate over the past 6 hours, it will exceed 2x its memory request within 4 hours.
Investigate for potential memory leaks or increased workload.
'''
          info: '''Container {{ $labels.container }} in pod {{ $labels.pod }} (namespace {{ $labels.namespace }}) on cluster {{ $labels.cluster }} memory is growing steadily.
At the current rate over the past 6 hours, it will exceed 2x its memory request within 4 hours.
Investigate for potential memory leaks or increased workload.
'''
          owning_team: 'hcp-sl'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/service-memory-resources.md'
          summary: '{{ $labels.container }} in {{ $labels.namespace }} memory trending toward 2x its request on cluster {{ $labels.cluster }}. pod:{{ $labels.pod }}'
          title: '{{ $labels.container }} in {{ $labels.namespace }} memory trending toward 2x its request on cluster {{ $labels.cluster }}. pod:{{ $labels.pod }}'
        }
        expression: '(predict_linear(container_memory_working_set_bytes{container!="",namespace=~"aro-hcp|aro-hcp-admin-api|aro-hcp-exporter|arobit|clusters-service|fleet|kube-applier|maestro|mgmt-agent|prometheus|secret-sync-controller|sessiongate"}[6h], 4 * 3600) / on (namespace, pod, container, cluster) group_left () max by (namespace, pod, container, cluster) (kube_pod_container_resource_requests{job="kube-state-metrics",namespace=~"aro-hcp|aro-hcp-admin-api|aro-hcp-exporter|arobit|clusters-service|fleet|kube-applier|maestro|mgmt-agent|prometheus|secret-sync-controller|sessiongate",resource="memory"})) > 2'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
