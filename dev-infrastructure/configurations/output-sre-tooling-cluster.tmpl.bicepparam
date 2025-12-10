using '../templates/output-sre-tooling-cluster.bicep'

param aksClusterName = '{{ .sretooling.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'

param adminApiMIName = '{{ .sretooling.adminApi.managedIdentityName }}'
