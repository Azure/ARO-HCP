using '../templates/output-mgmt-cluster.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param managementIdentityResourceGroups = '{{ .infrastructureIdentities.managementResourceGroups }}'
param stampIdentifier = '{{ .mgmt.stampIdentifier }}'

param aksClusterName = '{{ .mgmt.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'
