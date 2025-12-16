using '../templates/output-svc-cluster.bicep'

param aksClusterName = '{{ .svc.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'

param adminApiMIName = '{{ .adminApi.managedIdentityName }}'
