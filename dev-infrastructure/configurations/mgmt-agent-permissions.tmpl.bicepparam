using '../templates/mgmt-agent-permissions.bicep'

param mgmtAgentPrincipalId = '__mgmtAgentPrincipalId__'
param deploymentMsiId = '__deploymentMsiId__'

param aksClusterName = '{{ .mgmt.aks.name }}'
param aksResourceGroupName = '{{ .mgmt.rg }}'
param nodeMitigationEnabled = {{ .mgmtAgent.nodeMitigation.enabled }}
