using '../templates/custom-metrics-collector-identity.bicep'

param identityName = 'custom-metrics-collector'
param aksClusterName = '{{ .sretooling.aks.name }}'
param namespace = 'tenant-quota'
param serviceAccountName = 'custom-metrics-collector'

