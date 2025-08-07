#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource kubernetesApps 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-apps'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePodCrashLooping'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: "CrashLoopBackOff").'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping'
          summary: 'Pod is crash looping.'
        }
        expression: 'max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff", job="kube-state-metrics"}[5m]) >= 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePodNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} has been in a non-ready state for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodnotready'
          summary: 'Pod has been in a non-ready state for more than 15 minutes.'
        }
        expression: 'sum by (namespace, pod, cluster) (   max by(namespace, pod, cluster) (     kube_pod_status_phase{job="kube-state-metrics", phase=~"Pending|Unknown|Failed"}   ) * on(namespace, pod, cluster) group_left(owner_kind) topk by(namespace, pod, cluster) (     1, max by(namespace, pod, owner_kind, cluster) (kube_pod_owner{owner_kind!="Job"})   ) ) > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDeploymentGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Deployment generation for {{ $labels.namespace }}/{{ $labels.deployment }} does not match, this indicates that the Deployment has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentgenerationmismatch'
          summary: 'Deployment generation mismatch due to possible roll-back'
        }
        expression: 'kube_deployment_status_observed_generation{job="kube-state-metrics"}   != kube_deployment_metadata_generation{job="kube-state-metrics"}'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDeploymentReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Deployment {{ $labels.namespace }}/{{ $labels.deployment }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentreplicasmismatch'
          summary: 'Deployment has not matched the expected number of replicas.'
        }
        expression: '(   kube_deployment_spec_replicas{job="kube-state-metrics"}     >   kube_deployment_status_replicas_available{job="kube-state-metrics"} ) and (   changes(kube_deployment_status_replicas_updated{job="kube-state-metrics"}[10m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDeploymentRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Rollout of deployment {{ $labels.namespace }}/{{ $labels.deployment }} is not progressing for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedeploymentrolloutstuck'
          summary: 'Deployment rollout is not progressing.'
        }
        expression: 'kube_deployment_status_condition{condition="Progressing", status="false",job="kube-state-metrics"} != 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeStatefulSetReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} has not matched the expected number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetreplicasmismatch'
          summary: 'StatefulSet has not matched the expected number of replicas.'
        }
        expression: '(   kube_statefulset_status_replicas_ready{job="kube-state-metrics"}     !=   kube_statefulset_status_replicas{job="kube-state-metrics"} ) and (   changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[10m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeStatefulSetGenerationMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'StatefulSet generation for {{ $labels.namespace }}/{{ $labels.statefulset }} does not match, this indicates that the StatefulSet has failed but has not been rolled back.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetgenerationmismatch'
          summary: 'StatefulSet generation mismatch due to possible roll-back'
        }
        expression: 'kube_statefulset_status_observed_generation{job="kube-state-metrics"}   != kube_statefulset_metadata_generation{job="kube-state-metrics"}'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeStatefulSetUpdateNotRolledOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'StatefulSet {{ $labels.namespace }}/{{ $labels.statefulset }} update has not been rolled out.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubestatefulsetupdatenotrolledout'
          summary: 'StatefulSet update has not been rolled out.'
        }
        expression: '(   max by(namespace, statefulset, job, cluster) (     kube_statefulset_status_current_revision{job="kube-state-metrics"}       unless     kube_statefulset_status_update_revision{job="kube-state-metrics"}   )     *   (     kube_statefulset_replicas{job="kube-state-metrics"}       !=     kube_statefulset_status_replicas_updated{job="kube-state-metrics"}   ) )  and (   changes(kube_statefulset_status_replicas_updated{job="kube-state-metrics"}[5m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDaemonSetRolloutStuck'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} has not finished or progressed for at least 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetrolloutstuck'
          summary: 'DaemonSet rollout is stuck.'
        }
        expression: '(   (     kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"}      !=     kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}   ) or (     kube_daemonset_status_number_misscheduled{job="kube-state-metrics"}      !=     0   ) or (     kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"}      !=     kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}   ) or (     kube_daemonset_status_number_available{job="kube-state-metrics"}      !=     kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}   ) ) and (   changes(kube_daemonset_status_updated_number_scheduled{job="kube-state-metrics"}[5m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeContainerWaiting'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'pod/{{ $labels.pod }} in namespace {{ $labels.namespace }} on container {{ $labels.container}} has been in waiting state for longer than 1 hour.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontainerwaiting'
          summary: 'Pod container waiting longer than 1 hour'
        }
        expression: 'sum by (namespace, pod, container, cluster) (kube_pod_container_status_waiting_reason{job="kube-state-metrics"}) > 0'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDaemonSetNotScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are not scheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetnotscheduled'
          summary: 'DaemonSet pods are not scheduled.'
        }
        expression: 'kube_daemonset_status_desired_number_scheduled{job="kube-state-metrics"}   - kube_daemonset_status_current_number_scheduled{job="kube-state-metrics"} > 0'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeDaemonSetMisScheduled'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: '{{ $value }} Pods of DaemonSet {{ $labels.namespace }}/{{ $labels.daemonset }} are running where they are not supposed to run.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubedaemonsetmisscheduled'
          summary: 'DaemonSet pods are misscheduled.'
        }
        expression: 'kube_daemonset_status_number_misscheduled{job="kube-state-metrics"} > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeJobNotCompleted'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ "43200" | humanizeDuration }} to complete.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobnotcompleted'
          summary: 'Job did not complete in time'
        }
        expression: 'time() - max by(namespace, job_name, cluster) (kube_job_status_start_time{job="kube-state-metrics"}   and kube_job_status_active{job="kube-state-metrics"} > 0) > 43200'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeJobFailed'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} failed to complete. Removing failed job after investigation should clear this alert.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobfailed'
          summary: 'Job failed to complete.'
        }
        expression: 'kube_job_failed{job="kube-state-metrics"}  > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeHpaReplicasMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has not matched the desired number of replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpareplicasmismatch'
          summary: 'HPA has not matched desired number of replicas.'
        }
        expression: '(kube_horizontalpodautoscaler_status_desired_replicas{job="kube-state-metrics"}   != kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"})   and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}   > kube_horizontalpodautoscaler_spec_min_replicas{job="kube-state-metrics"})   and (kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}   < kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"})   and changes(kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}[15m]) == 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeHpaMaxedOut'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'HPA {{ $labels.namespace }}/{{ $labels.horizontalpodautoscaler  }} has been running at max replicas for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubehpamaxedout'
          summary: 'HPA is running at max replicas'
        }
        expression: 'kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"}   == kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeCPUOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Cluster {{ $labels.cluster }} has overcommitted CPU resource requests for Pods by {{ $value }} CPU shares and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuovercommit'
          summary: 'Cluster has overcommitted CPU resource requests.'
        }
        expression: 'sum(namespace_cpu:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster) - max(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster)) > 0 and (sum(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster) - max(kube_node_status_allocatable{job="kube-state-metrics",resource="cpu"}) by (cluster)) > 0'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeMemoryOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Cluster {{ $labels.cluster }} has overcommitted memory resource requests for Pods by {{ $value | humanize }} bytes and cannot tolerate node failure.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
        }
        expression: 'sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster) - max(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster)) > 0 and (sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster) - max(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster)) > 0'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeCPUQuotaOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Cluster {{ $labels.cluster }}  has overcommitted CPU resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecpuquotaovercommit'
          summary: 'Cluster has overcommitted CPU resource requests.'
        }
        expression: 'sum(min without(resource) (kube_resourcequota{job="kube-state-metrics", type="hard", resource=~"(cpu|requests.cpu)"})) by (cluster)   / sum(kube_node_status_allocatable{resource="cpu", job="kube-state-metrics"}) by (cluster)   > 1.5'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeMemoryQuotaOvercommit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Cluster {{ $labels.cluster }}  has overcommitted memory resource requests for Namespaces.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubememoryquotaovercommit'
          summary: 'Cluster has overcommitted memory resource requests.'
        }
        expression: 'sum(min without(resource) (kube_resourcequota{job="kube-state-metrics", type="hard", resource=~"(memory|requests.memory)"})) by (cluster)   / sum(kube_node_status_allocatable{resource="memory", job="kube-state-metrics"}) by (cluster)   > 1.5'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeQuotaAlmostFull'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaalmostfull'
          summary: 'Namespace quota is going to be full.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"}   / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0)   > 0.9 < 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeQuotaFullyUsed'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotafullyused'
          summary: 'Namespace quota is fully used.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"}   / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0)   == 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeQuotaExceeded'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Namespace {{ $labels.namespace }} is using {{ $value | humanizePercentage }} of its {{ $labels.resource }} quota.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubequotaexceeded'
          summary: 'Namespace quota has exceeded the limits.'
        }
        expression: 'kube_resourcequota{job="kube-state-metrics", type="used"}   / ignoring(instance, job, type) (kube_resourcequota{job="kube-state-metrics", type="hard"} > 0)   > 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'CPUThrottlingHigh'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          description: '{{ $value | humanizePercentage }} throttling of CPU in namespace {{ $labels.namespace }} for container {{ $labels.container }} in pod {{ $labels.pod }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh'
          summary: 'Processes experience elevated CPU throttling.'
        }
        expression: 'sum(increase(container_cpu_cfs_throttled_periods_total{container!="", }[5m])) by (cluster, container, pod, namespace)   / sum(increase(container_cpu_cfs_periods_total{}[5m])) by (cluster, container, pod, namespace)   > ( 25 / 100 )'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is only {{ $value | humanizePercentage }} free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
        }
        expression: '(   kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"}     /   kubelet_volume_stats_capacity_bytes{job="kubelet", metrics_path="/metrics"} ) < 0.03 and kubelet_volume_stats_used_bytes{job="kubelet", metrics_path="/metrics"} > 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePersistentVolumeFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to fill up within four days. Currently {{ $value | humanizePercentage }} is available.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumefillingup'
          summary: 'PersistentVolume is filling up.'
        }
        expression: '(   kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"}     /   kubelet_volume_stats_capacity_bytes{job="kubelet", metrics_path="/metrics"} ) < 0.15 and kubelet_volume_stats_used_bytes{job="kubelet", metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_available_bytes{job="kubelet", metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} only has {{ $value | humanizePercentage }} free inodes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
        }
        expression: '(   kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"}     /   kubelet_volume_stats_inodes{job="kubelet", metrics_path="/metrics"} ) < 0.03 and kubelet_volume_stats_inodes_used{job="kubelet", metrics_path="/metrics"} > 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePersistentVolumeInodesFillingUp'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Based on recent sampling, the PersistentVolume claimed by {{ $labels.persistentvolumeclaim }} in Namespace {{ $labels.namespace }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} is expected to run out of inodes within four days. Currently {{ $value | humanizePercentage }} of its inodes are free.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeinodesfillingup'
          summary: 'PersistentVolumeInodes are filling up.'
        }
        expression: '(   kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"}     /   kubelet_volume_stats_inodes{job="kubelet", metrics_path="/metrics"} ) < 0.15 and kubelet_volume_stats_inodes_used{job="kubelet", metrics_path="/metrics"} > 0 and predict_linear(kubelet_volume_stats_inodes_free{job="kubelet", metrics_path="/metrics"}[6h], 4 * 24 * 3600) < 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode="ReadOnlyMany"} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts="true"} == 1'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubePersistentVolumeErrors'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The persistent volume {{ $labels.persistentvolume }} {{ with $labels.cluster -}} on Cluster {{ . }} {{- end }} has status {{ $labels.phase }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepersistentvolumeerrors'
          summary: 'PersistentVolume is having issues with provisioning.'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeVersionMismatch'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'There are {{ $value }} different semantic versions of Kubernetes components running.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeversionmismatch'
          summary: 'Different semantic versions of Kubernetes components running.'
        }
        expression: 'count by (cluster) (count by (git_version, cluster) (label_replace(kubernetes_build_info{job!~"kube-dns|coredns"},"git_version","$1","git_version","(v[0-9]*.[0-9]*).*"))) > 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeClientErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubernetes API server client \'{{ $labels.job }}/{{ $labels.instance }}\' is experiencing {{ $value | humanizePercentage }} errors.\''
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclienterrors'
          summary: 'Kubernetes API server client is experiencing errors.'
        }
        expression: '(sum(rate(rest_client_requests_total{job="apiserver",code=~"5.."}[5m])) by (cluster, instance, job, namespace)   / sum(rate(rest_client_requests_total{job="apiserver"}[5m])) by (cluster, instance, job, namespace)) > 0.01'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '1h'
          severity: 'critical'
          short: '5m'
        }
        annotations: {
          description: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate1h) > (14.40 * 0.01000) and sum(apiserver_request:burnrate5m) > (14.40 * 0.01000)'
        for: 'PT2M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'critical'
          short: '30m'
        }
        annotations: {
          description: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate6h) > (6.00 * 0.01000) and sum(apiserver_request:burnrate30m) > (6.00 * 0.01000)'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '1d'
          severity: 'warning'
          short: '2h'
        }
        annotations: {
          description: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
        }
        expression: 'sum(apiserver_request:burnrate1d) > (3.00 * 0.01000) and sum(apiserver_request:burnrate2h) > (3.00 * 0.01000)'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPIErrorBudgetBurn'
        enabled: true
        labels: {
          long: '3d'
          severity: 'warning'
          short: '6h'
        }
        annotations: {
          description: 'The API server is burning too much error budget.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'The API server is burning too much error budget.'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 7.0 days.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
        }
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="apiserver"} > 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="apiserver"}[5m]))) < 604800'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'A client certificate used to authenticate to kubernetes apiserver is expiring in less than 24.0 hours.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclientcertificateexpiration'
          summary: 'Client certificate is about to expire.'
        }
        expression: 'apiserver_client_certificate_expiration_seconds_count{job="apiserver"} > 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job="apiserver"}[5m]))) < 86400'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAggregatedAPIErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has reported errors. It has appeared unavailable {{ $value | humanize }} times averaged over the past 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapierrors'
          summary: 'Kubernetes aggregated API has reported errors.'
        }
        expression: 'sum by(name, namespace, cluster)(increase(aggregator_unavailable_apiservice_total{job="apiserver"}[10m])) > 4'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAggregatedAPIDown'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubernetes aggregated API {{ $labels.name }}/{{ $labels.namespace }} has been only {{ $value | humanize }}% available over the last 10m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeaggregatedapidown'
          summary: 'Kubernetes aggregated API is down.'
        }
        expression: '(1 - max by(name, namespace, cluster)(avg_over_time(aggregator_unavailable_apiservice{job="apiserver"}[10m]))) * 100 < 85'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPIDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'KubeAPI has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapidown'
          summary: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="apiserver"} == 1)'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeAPITerminatedRequests'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapiterminatedrequests'
          summary: 'The kubernetes apiserver has terminated {{ $value | humanizePercentage }} of its incoming requests.'
        }
        expression: 'sum(rate(apiserver_request_terminations_total{job="apiserver"}[10m]))  / (  sum(rate(apiserver_request_total{job="apiserver"}[10m])) + sum(rate(apiserver_request_terminations_total{job="apiserver"}[10m])) ) > 0.20'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeNodeNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: '{{ $labels.node }} has been unready for more than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodenotready'
          summary: 'Node is not ready.'
        }
        expression: 'kube_node_status_condition{job="kube-state-metrics",condition="Ready",status="true"} == 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeNodeUnreachable'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: '{{ $labels.node }} is unreachable and some workloads may be rescheduled.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodeunreachable'
          summary: 'Node is unreachable.'
        }
        expression: '(kube_node_spec_taint{job="kube-state-metrics",key="node.kubernetes.io/unreachable",effect="NoSchedule"} unless ignoring(key,value) kube_node_spec_taint{job="kube-state-metrics",key=~"ToBeDeletedByClusterAutoscaler|cloud.google.com/impending-node-termination|aws-node-termination-handler/spot-itn"}) == 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletTooManyPods'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          description: 'Kubelet \'{{ $labels.node }}\' is running at {{ $value | humanizePercentage }} of its Pod capacity.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubelettoomanypods'
          summary: 'Kubelet is running at capacity.'
        }
        expression: 'count by(cluster, node) (   (kube_pod_status_phase{job="kube-state-metrics",phase="Running"} == 1) * on(instance,pod,namespace,cluster) group_left(node) topk by(instance,pod,namespace,cluster) (1, kube_pod_info{job="kube-state-metrics"}) ) / max by(cluster, node) (   kube_node_status_capacity{job="kube-state-metrics",resource="pods"} != 1 ) > 0.95'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeNodeReadinessFlapping'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'The readiness status of node {{ $labels.node }} has changed {{ $value }} times in the last 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubenodereadinessflapping'
          summary: 'Node readiness status is flapping.'
        }
        expression: 'sum(changes(kube_node_status_condition{job="kube-state-metrics",status="true",condition="Ready"}[15m])) by (cluster, node) > 2'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletPlegDurationHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletplegdurationhigh'
          summary: 'Kubelet Pod Lifecycle Event Generator is taking too long to relist.'
        }
        expression: 'node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile{quantile="0.99"} >= 10'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletPodStartUpLatencyHigh'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubelet Pod startup 99th percentile latency is {{ $value }} seconds on node {{ $labels.node }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletpodstartuplatencyhigh'
          summary: 'Kubelet Pod startup latency is too high.'
        }
        expression: 'histogram_quantile(0.99, sum(rate(kubelet_pod_worker_duration_seconds_bucket{job="kubelet", metrics_path="/metrics"}[5m])) by (cluster, instance, le)) * on(cluster, instance) group_left(node) kubelet_node_name{job="kubelet", metrics_path="/metrics"} > 60'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 604800'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletClientCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Client certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificateexpiration'
          summary: 'Kubelet client certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_client_ttl_seconds < 86400'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 604800'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletServerCertificateExpiration'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Server certificate for Kubelet on node {{ $labels.node }} expires in {{ $value | humanizeDuration }}.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificateexpiration'
          summary: 'Kubelet server certificate is about to expire.'
        }
        expression: 'kubelet_certificate_manager_server_ttl_seconds < 86400'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletClientCertificateRenewalErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its client certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletclientcertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its client certificate.'
        }
        expression: 'increase(kubelet_certificate_manager_client_expiration_renew_errors[5m]) > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletServerCertificateRenewalErrors'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Kubelet on node {{ $labels.node }} has failed to renew its server certificate ({{ $value | humanize }} errors in the last 5 minutes).'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletservercertificaterenewalerrors'
          summary: 'Kubelet has failed to renew its server certificate.'
        }
        expression: 'increase(kubelet_server_expiration_renew_errors[5m]) > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeletDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Kubelet has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeletdown'
          summary: 'Target disappeared from Prometheus target discovery.'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeSchedulerDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'KubeScheduler has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeschedulerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="kube-scheduler"} == 1)'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'KubeControllerManagerDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'KubeControllerManager has disappeared from Prometheus target discovery.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubecontrollermanagerdown'
          summary: 'Target disappeared from Prometheus target discovery.'
        }
        expression: 'absent(up{job="kube-controller-manager"} == 1)'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusJobUp'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: '''Prometheus has not been reachable for the past 5 minutes.
This may indicate that the Prometheus server is down, unreachable due to network issues, or experiencing a crash loop.
Check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 5 minutes.'
        }
        expression: 'min by (job, namespace) (up{job="prometheus/prometheus",namespace="prometheus"}) == 0'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusUptime'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: '''Prometheus has been unreachable for more than 5% of the time over the past 24 hours.
This may indicate that the Prometheus server is down, experiencing network issues, or stuck in a crash loop.
Please check the status of the Prometheus pods, service endpoints, and network connectivity.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus is unreachable for 1 day.'
        }
        expression: 'avg by (job, namespace) (avg_over_time(up{job="prometheus/prometheus",namespace="prometheus"}[1d])) < 0.95'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusPendingRate'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: '''The pending sample rate of Prometheus remote storage is above 40% for the last 15 minutes.
This means that more than 40% of samples are waiting to be sent to remote storage, which may indicate
a bottleneck or issue with the remote write endpoint, network connectivity, or Prometheus performance.
If this condition persists, it could lead to increased memory usage and potential data loss if the buffer overflows.
Investigate the health and performance of the remote storage endpoint, network latency, and Prometheus resource utilization.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus pending sample rate is above 40%.'
        }
        expression: '(   prometheus_remote_storage_samples_pending   /   prometheus_remote_storage_samples_in_flight ) > 0.4'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusFailedRate'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: '''The failed sample rate for Prometheus remote storage has exceeded 10% over the past 15 minutes.
This indicates that more than 10% of samples are not being successfully sent to remote storage, which could be caused by
issues with the remote write endpoint, network instability, or Prometheus resource constraints.
Persistent failures may result in increased memory usage and potential data loss if the buffer overflows.
Please check the health and performance of the remote storage endpoint, network connectivity, and Prometheus resource utilization.
'''
          runbook_url: 'TBD'
          summary: 'Prometheus failed sample rate to remote storage is above 10%.'
        }
        expression: '(   rate(prometheus_remote_storage_samples_failed_total[5m])   /   rate(prometheus_remote_storage_samples_total[5m]) ) > 0.1'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusRemoteStorageFailures'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} failed to send {{ printf "%.1f" $value }}% of the samples to {{ $labels.remote_name}}:{{ $labels.url }}'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusremotestoragefailures'
          summary: 'Prometheus fails to send samples to remote storage.'
        }
        expression: '((rate(prometheus_remote_storage_failed_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) / ((rate(prometheus_remote_storage_failed_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_failed_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) + (rate(prometheus_remote_storage_succeeded_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) or rate(prometheus_remote_storage_samples_total{job="prometheus-prometheus",namespace="prometheus"}[5m])))) * 100 > 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusNotIngestingSamples'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} is not ingesting samples.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusnotingestingsamples'
          summary: 'Prometheus is not ingesting samples.'
        }
        expression: '(sum without (type) (rate(prometheus_tsdb_head_samples_appended_total{job="prometheus-prometheus",namespace="prometheus"}[5m])) <= 0 and (sum without (scrape_job) (prometheus_target_metadata_cache_entries{job="prometheus-prometheus",namespace="prometheus"}) > 0 or sum without (rule_group) (prometheus_rule_group_rules{job="prometheus-prometheus",namespace="prometheus"}) > 0))'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusBadConfig'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to reload its configuration.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusbadconfig'
          summary: 'Failed Prometheus configuration reload.'
        }
        expression: 'max_over_time(prometheus_config_last_reload_successful{job="prometheus-prometheus",namespace="prometheus"}[5m]) == 0'
        for: 'PT10M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusRuleFailures'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed to evaluate {{ printf "%.0f" $value }} rules in the last 5m.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusrulefailures'
          summary: 'Prometheus is failing rule evaluations.'
        }
        expression: 'increase(prometheus_rule_evaluation_failures_total{job="prometheus-prometheus",namespace="prometheus"}[5m]) > 0'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusScrapeSampleLimitHit'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Prometheus {{$labels.namespace}}/{{$labels.pod}} has failed {{ printf "%.0f" $value }} scrapes in the last 5m because some targets exceeded the configured sample_limit.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusscrapesamplelimithit'
          summary: 'Prometheus has failed scrapes that have exceeded the configured sample limit.'
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
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusOperatorNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Prometheus operator in {{ $labels.namespace }} namespace isn\'t ready to reconcile {{ $labels.controller }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatornotready'
          summary: 'Prometheus operator not ready'
        }
        expression: 'min by (cluster, controller, namespace) (max_over_time(prometheus_operator_ready{job="prometheus-operator",namespace="prometheus"}[5m])) == 0'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'PrometheusOperatorRejectedResources'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          description: 'Prometheus operator in {{ $labels.namespace }} namespace rejected {{ printf "%0.0f" $value }} {{ $labels.controller }}/{{ $labels.resource }} resources.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatorrejectedresources'
          summary: 'Resources rejected by Prometheus operator'
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
