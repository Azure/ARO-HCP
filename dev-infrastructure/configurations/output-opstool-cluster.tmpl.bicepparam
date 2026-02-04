using '../templates/output-opstool-cluster.bicep'

param aksClusterName = '{{ .opstool.aks.name }}'
param workloadKVName = '{{ .opstool.keyVault.name }}'
param azureMonitorWorkspaceName = '{{ .opstool.monitoring.workspaceName }}'
