param azureMonitoring string

resource kubernetesApps 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kubernetes-apps'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        alert: 'KubePodCrashLooping'
		enabled: true
		labels: {
			severity: 'warning'
		}
		annotations: {
			description: 'Pod {{ $labels.namespace }}/{{ $labels.pod }} ({{ $labels.container }}) is in waiting state (reason: &#34;CrashLoopBackOff&#34;).'
			runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping'
			summary: 'Pod is crash looping.'
		}
		expression: 'max_over_time(kube_pod_container_status_waiting_reason{reason=&#34;CrashLoopBackOff&#34;, job=&#34;kube-state-metrics&#34;}[5m]) &gt;= 1'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'sum by (namespace, pod, cluster) (   max by(namespace, pod, cluster) (     kube_pod_status_phase{job=&#34;kube-state-metrics&#34;, phase=~&#34;Pending|Unknown|Failed&#34;}   ) * on(namespace, pod, cluster) group_left(owner_kind) topk by(namespace, pod, cluster) (     1, max by(namespace, pod, owner_kind, cluster) (kube_pod_owner{owner_kind!=&#34;Job&#34;})   ) ) &gt; 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'kube_deployment_status_observed_generation{job=&#34;kube-state-metrics&#34;}   != kube_deployment_metadata_generation{job=&#34;kube-state-metrics&#34;}'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(   kube_deployment_spec_replicas{job=&#34;kube-state-metrics&#34;}     &gt;   kube_deployment_status_replicas_available{job=&#34;kube-state-metrics&#34;} ) and (   changes(kube_deployment_status_replicas_updated{job=&#34;kube-state-metrics&#34;}[10m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'kube_deployment_status_condition{condition=&#34;Progressing&#34;, status=&#34;false&#34;,job=&#34;kube-state-metrics&#34;} != 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(   kube_statefulset_status_replicas_ready{job=&#34;kube-state-metrics&#34;}     !=   kube_statefulset_status_replicas{job=&#34;kube-state-metrics&#34;} ) and (   changes(kube_statefulset_status_replicas_updated{job=&#34;kube-state-metrics&#34;}[10m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'kube_statefulset_status_observed_generation{job=&#34;kube-state-metrics&#34;}   != kube_statefulset_metadata_generation{job=&#34;kube-state-metrics&#34;}'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(   max by(namespace, statefulset, job, cluster) (     kube_statefulset_status_current_revision{job=&#34;kube-state-metrics&#34;}       unless     kube_statefulset_status_update_revision{job=&#34;kube-state-metrics&#34;}   )     *   (     kube_statefulset_replicas{job=&#34;kube-state-metrics&#34;}       !=     kube_statefulset_status_replicas_updated{job=&#34;kube-state-metrics&#34;}   ) )  and (   changes(kube_statefulset_status_replicas_updated{job=&#34;kube-state-metrics&#34;}[5m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(   (     kube_daemonset_status_current_number_scheduled{job=&#34;kube-state-metrics&#34;}      !=     kube_daemonset_status_desired_number_scheduled{job=&#34;kube-state-metrics&#34;}   ) or (     kube_daemonset_status_number_misscheduled{job=&#34;kube-state-metrics&#34;}      !=     0   ) or (     kube_daemonset_status_updated_number_scheduled{job=&#34;kube-state-metrics&#34;}      !=     kube_daemonset_status_desired_number_scheduled{job=&#34;kube-state-metrics&#34;}   ) or (     kube_daemonset_status_number_available{job=&#34;kube-state-metrics&#34;}      !=     kube_daemonset_status_desired_number_scheduled{job=&#34;kube-state-metrics&#34;}   ) ) and (   changes(kube_daemonset_status_updated_number_scheduled{job=&#34;kube-state-metrics&#34;}[5m])     ==   0 )'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'sum by (namespace, pod, container, cluster) (kube_pod_container_status_waiting_reason{job=&#34;kube-state-metrics&#34;}) &gt; 0'
        for: 'PT1H'
        severity: 3
      }
      {
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
		expression: 'kube_daemonset_status_desired_number_scheduled{job=&#34;kube-state-metrics&#34;}   - kube_daemonset_status_current_number_scheduled{job=&#34;kube-state-metrics&#34;} &gt; 0'
        for: 'PT10M'
        severity: 3
      }
      {
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
		expression: 'kube_daemonset_status_number_misscheduled{job=&#34;kube-state-metrics&#34;} &gt; 0'
        for: 'PT15M'
        severity: 3
      }
      {
        alert: 'KubeJobNotCompleted'
		enabled: true
		labels: {
			severity: 'warning'
		}
		annotations: {
			description: 'Job {{ $labels.namespace }}/{{ $labels.job_name }} is taking more than {{ &#34;43200&#34; | humanizeDuration }} to complete.'
			runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubejobnotcompleted'
			summary: 'Job did not complete in time'
		}
		expression: 'time() - max by(namespace, job_name, cluster) (kube_job_status_start_time{job=&#34;kube-state-metrics&#34;}   and kube_job_status_active{job=&#34;kube-state-metrics&#34;} &gt; 0) &gt; 43200'
        severity: 3
      }
      {
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
		expression: 'kube_job_failed{job=&#34;kube-state-metrics&#34;}  &gt; 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(kube_horizontalpodautoscaler_status_desired_replicas{job=&#34;kube-state-metrics&#34;}   != kube_horizontalpodautoscaler_status_current_replicas{job=&#34;kube-state-metrics&#34;})   and (kube_horizontalpodautoscaler_status_current_replicas{job=&#34;kube-state-metrics&#34;}   &gt; kube_horizontalpodautoscaler_spec_min_replicas{job=&#34;kube-state-metrics&#34;})   and (kube_horizontalpodautoscaler_status_current_replicas{job=&#34;kube-state-metrics&#34;}   &lt; kube_horizontalpodautoscaler_spec_max_replicas{job=&#34;kube-state-metrics&#34;})   and changes(kube_horizontalpodautoscaler_status_current_replicas{job=&#34;kube-state-metrics&#34;}[15m]) == 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'kube_horizontalpodautoscaler_status_current_replicas{job=&#34;kube-state-metrics&#34;}   == kube_horizontalpodautoscaler_spec_max_replicas{job=&#34;kube-state-metrics&#34;}'
        for: 'PT15M'
        severity: 3
      }]
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
		expression: 'sum(namespace_cpu:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{job=&#34;kube-state-metrics&#34;,resource=&#34;cpu&#34;}) by (cluster) - max(kube_node_status_allocatable{job=&#34;kube-state-metrics&#34;,resource=&#34;cpu&#34;}) by (cluster)) &gt; 0 and (sum(kube_node_status_allocatable{job=&#34;kube-state-metrics&#34;,resource=&#34;cpu&#34;}) by (cluster) - max(kube_node_status_allocatable{job=&#34;kube-state-metrics&#34;,resource=&#34;cpu&#34;}) by (cluster)) &gt; 0'
        for: 'PT10M'
        severity: 3
      }
      {
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
		expression: 'sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{resource=&#34;memory&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster) - max(kube_node_status_allocatable{resource=&#34;memory&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster)) &gt; 0 and (sum(kube_node_status_allocatable{resource=&#34;memory&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster) - max(kube_node_status_allocatable{resource=&#34;memory&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster)) &gt; 0'
        for: 'PT10M'
        severity: 3
      }
      {
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
		expression: 'sum(min without(resource) (kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;hard&#34;, resource=~&#34;(cpu|requests.cpu)&#34;})) by (cluster)   / sum(kube_node_status_allocatable{resource=&#34;cpu&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster)   &gt; 1.5'
        for: 'PT5M'
        severity: 3
      }
      {
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
		expression: 'sum(min without(resource) (kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;hard&#34;, resource=~&#34;(memory|requests.memory)&#34;})) by (cluster)   / sum(kube_node_status_allocatable{resource=&#34;memory&#34;, job=&#34;kube-state-metrics&#34;}) by (cluster)   &gt; 1.5'
        for: 'PT5M'
        severity: 3
      }
      {
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
		expression: 'kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;used&#34;}   / ignoring(instance, job, type) (kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;hard&#34;} &gt; 0)   &gt; 0.9 &lt; 1'
        for: 'PT15M'
        severity: 4
      }
      {
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
		expression: 'kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;used&#34;}   / ignoring(instance, job, type) (kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;hard&#34;} &gt; 0)   == 1'
        for: 'PT15M'
        severity: 4
      }
      {
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
		expression: 'kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;used&#34;}   / ignoring(instance, job, type) (kube_resourcequota{job=&#34;kube-state-metrics&#34;, type=&#34;hard&#34;} &gt; 0)   &gt; 1'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'sum(increase(container_cpu_cfs_throttled_periods_total{container!=&#34;&#34;, }[5m])) by (cluster, container, pod, namespace)   / sum(increase(container_cpu_cfs_periods_total{}[5m])) by (cluster, container, pod, namespace)   &gt; ( 25 / 100 )'
        for: 'PT15M'
        severity: 4
      }]
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
		expression: '(   kubelet_volume_stats_available_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}     /   kubelet_volume_stats_capacity_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} ) &lt; 0.03 and kubelet_volume_stats_used_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} &gt; 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode=&#34;ReadOnlyMany&#34;} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts=&#34;true&#34;} == 1'
        for: 'PT1M'
        severity: 2
      }
      {
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
		expression: '(   kubelet_volume_stats_available_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}     /   kubelet_volume_stats_capacity_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} ) &lt; 0.15 and kubelet_volume_stats_used_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} &gt; 0 and predict_linear(kubelet_volume_stats_available_bytes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}[6h], 4 * 24 * 3600) &lt; 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode=&#34;ReadOnlyMany&#34;} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts=&#34;true&#34;} == 1'
        for: 'PT1H'
        severity: 3
      }
      {
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
		expression: '(   kubelet_volume_stats_inodes_free{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}     /   kubelet_volume_stats_inodes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} ) &lt; 0.03 and kubelet_volume_stats_inodes_used{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} &gt; 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode=&#34;ReadOnlyMany&#34;} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts=&#34;true&#34;} == 1'
        for: 'PT1M'
        severity: 2
      }
      {
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
		expression: '(   kubelet_volume_stats_inodes_free{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}     /   kubelet_volume_stats_inodes{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} ) &lt; 0.15 and kubelet_volume_stats_inodes_used{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} &gt; 0 and predict_linear(kubelet_volume_stats_inodes_free{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}[6h], 4 * 24 * 3600) &lt; 0 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_access_mode{ access_mode=&#34;ReadOnlyMany&#34;} == 1 unless on(cluster, namespace, persistentvolumeclaim) kube_persistentvolumeclaim_labels{label_excluded_from_alerts=&#34;true&#34;} == 1'
        for: 'PT1H'
        severity: 3
      }
      {
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
		expression: 'kube_persistentvolume_status_phase{phase=~&#34;Failed|Pending&#34;,job=&#34;kube-state-metrics&#34;} &gt; 0'
        for: 'PT5M'
        severity: 2
      }]
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
		expression: 'count by (cluster) (count by (git_version, cluster) (label_replace(kubernetes_build_info{job!~&#34;kube-dns|coredns&#34;},&#34;git_version&#34;,&#34;$1&#34;,&#34;git_version&#34;,&#34;(v[0-9]*.[0-9]*).*&#34;))) &gt; 1'
        for: 'PT15M'
        severity: 3
      }
      {
        alert: 'KubeClientErrors'
		enabled: true
		labels: {
			severity: 'warning'
		}
		annotations: {
			description: 'Kubernetes API server client \&#39;{{ $labels.job }}/{{ $labels.instance }}\&#39; is experiencing {{ $value | humanizePercentage }} errors.\&#39;'
			runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeclienterrors'
			summary: 'Kubernetes API server client is experiencing errors.'
		}
		expression: '(sum(rate(rest_client_requests_total{job=&#34;apiserver&#34;,code=~&#34;5..&#34;}[5m])) by (cluster, instance, job, namespace)   / sum(rate(rest_client_requests_total{job=&#34;apiserver&#34;}[5m])) by (cluster, instance, job, namespace)) &gt; 0.01'
        for: 'PT15M'
        severity: 3
      }]
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
		expression: 'sum(apiserver_request:burnrate1h) &gt; (14.40 * 0.01000) and sum(apiserver_request:burnrate5m) &gt; (14.40 * 0.01000)'
        for: 'PT2M'
        severity: 2
      }
      {
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
		expression: 'sum(apiserver_request:burnrate6h) &gt; (6.00 * 0.01000) and sum(apiserver_request:burnrate30m) &gt; (6.00 * 0.01000)'
        for: 'PT15M'
        severity: 2
      }
      {
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
		expression: 'sum(apiserver_request:burnrate1d) &gt; (3.00 * 0.01000) and sum(apiserver_request:burnrate2h) &gt; (3.00 * 0.01000)'
        for: 'PT1H'
        severity: 3
      }
      {
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
		expression: 'sum(apiserver_request:burnrate3d) &gt; (1.00 * 0.01000) and sum(apiserver_request:burnrate6h) &gt; (1.00 * 0.01000)'
        for: 'PT3H'
        severity: 3
      }]
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
		expression: 'apiserver_client_certificate_expiration_seconds_count{job=&#34;apiserver&#34;} &gt; 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job=&#34;apiserver&#34;}[5m]))) &lt; 604800'
        for: 'PT5M'
        severity: 3
      }
      {
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
		expression: 'apiserver_client_certificate_expiration_seconds_count{job=&#34;apiserver&#34;} &gt; 0 and on(job) histogram_quantile(0.01, sum by (job, le) (rate(apiserver_client_certificate_expiration_seconds_bucket{job=&#34;apiserver&#34;}[5m]))) &lt; 86400'
        for: 'PT5M'
        severity: 2
      }
      {
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
		expression: 'sum by(name, namespace, cluster)(increase(aggregator_unavailable_apiservice_total{job=&#34;apiserver&#34;}[10m])) &gt; 4'
        severity: 3
      }
      {
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
		expression: '(1 - max by(name, namespace, cluster)(avg_over_time(aggregator_unavailable_apiservice{job=&#34;apiserver&#34;}[10m]))) * 100 &lt; 85'
        for: 'PT5M'
        severity: 3
      }
      {
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
		expression: 'absent(up{job=&#34;apiserver&#34;} == 1)'
        for: 'PT15M'
        severity: 2
      }
      {
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
		expression: 'sum(rate(apiserver_request_terminations_total{job=&#34;apiserver&#34;}[10m]))  / (  sum(rate(apiserver_request_total{job=&#34;apiserver&#34;}[10m])) &#43; sum(rate(apiserver_request_terminations_total{job=&#34;apiserver&#34;}[10m])) ) &gt; 0.20'
        for: 'PT5M'
        severity: 3
      }]
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
		expression: 'kube_node_status_condition{job=&#34;kube-state-metrics&#34;,condition=&#34;Ready&#34;,status=&#34;true&#34;} == 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: '(kube_node_spec_taint{job=&#34;kube-state-metrics&#34;,key=&#34;node.kubernetes.io/unreachable&#34;,effect=&#34;NoSchedule&#34;} unless ignoring(key,value) kube_node_spec_taint{job=&#34;kube-state-metrics&#34;,key=~&#34;ToBeDeletedByClusterAutoscaler|cloud.google.com/impending-node-termination|aws-node-termination-handler/spot-itn&#34;}) == 1'
        for: 'PT15M'
        severity: 3
      }
      {
        alert: 'KubeletTooManyPods'
		enabled: true
		labels: {
			severity: 'info'
		}
		annotations: {
			description: 'Kubelet \&#39;{{ $labels.node }}\&#39; is running at {{ $value | humanizePercentage }} of its Pod capacity.'
			runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubelettoomanypods'
			summary: 'Kubelet is running at capacity.'
		}
		expression: 'count by(cluster, node) (   (kube_pod_status_phase{job=&#34;kube-state-metrics&#34;,phase=&#34;Running&#34;} == 1) * on(instance,pod,namespace,cluster) group_left(node) topk by(instance,pod,namespace,cluster) (1, kube_pod_info{job=&#34;kube-state-metrics&#34;}) ) / max by(cluster, node) (   kube_node_status_capacity{job=&#34;kube-state-metrics&#34;,resource=&#34;pods&#34;} != 1 ) &gt; 0.95'
        for: 'PT15M'
        severity: 4
      }
      {
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
		expression: 'sum(changes(kube_node_status_condition{job=&#34;kube-state-metrics&#34;,status=&#34;true&#34;,condition=&#34;Ready&#34;}[15m])) by (cluster, node) &gt; 2'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile{quantile=&#34;0.99&#34;} &gt;= 10'
        for: 'PT5M'
        severity: 3
      }
      {
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
		expression: 'histogram_quantile(0.99, sum(rate(kubelet_pod_worker_duration_seconds_bucket{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;}[5m])) by (cluster, instance, le)) * on(cluster, instance) group_left(node) kubelet_node_name{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} &gt; 60'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'kubelet_certificate_manager_client_ttl_seconds &lt; 604800'
        severity: 3
      }
      {
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
		expression: 'kubelet_certificate_manager_client_ttl_seconds &lt; 86400'
        severity: 2
      }
      {
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
		expression: 'kubelet_certificate_manager_server_ttl_seconds &lt; 604800'
        severity: 3
      }
      {
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
		expression: 'kubelet_certificate_manager_server_ttl_seconds &lt; 86400'
        severity: 2
      }
      {
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
		expression: 'increase(kubelet_certificate_manager_client_expiration_renew_errors[5m]) &gt; 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'increase(kubelet_server_expiration_renew_errors[5m]) &gt; 0'
        for: 'PT15M'
        severity: 3
      }
      {
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
		expression: 'absent(up{job=&#34;kubelet&#34;, metrics_path=&#34;/metrics&#34;} == 1)'
        for: 'PT15M'
        severity: 2
      }]
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
		expression: 'absent(up{job=&#34;kube-scheduler&#34;} == 1)'
        for: 'PT15M'
        severity: 2
      }]
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
		expression: 'absent(up{job=&#34;kube-controller-manager&#34;} == 1)'
        for: 'PT15M'
        severity: 2
      }]
    scopes: [
      azureMonitoring
    ]
  }
}
