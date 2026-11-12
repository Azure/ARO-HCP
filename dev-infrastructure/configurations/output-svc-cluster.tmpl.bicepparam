using '../templates/output-svc-cluster.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'

param aksClusterName = '{{ .svc.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'

param adminApiMIName = '{{ .adminApi.managedIdentityName }}'
