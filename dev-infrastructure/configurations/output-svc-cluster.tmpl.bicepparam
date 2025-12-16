using '../templates/output-svc-cluster.bicep'

param logsMSI = '{{ .logs.mdsd.msiName }}'

param adminApiMIName = '{{ .adminApi.managedIdentityName }}'
