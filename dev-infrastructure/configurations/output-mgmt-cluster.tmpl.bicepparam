using '../templates/output-mgmt-cluster.bicep'

param aksClusterName = '{{ .mgmt.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'

