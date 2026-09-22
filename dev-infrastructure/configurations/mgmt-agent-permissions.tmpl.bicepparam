using '../templates/mgmt-agent-permissions.bicep'

param mgmtAgentPrincipalId = '__mgmtAgentPrincipalId__'

param aksClusterName = '{{ .mgmt.aks.name }}'
param aksResourceGroupName = '{{ .mgmt.rg }}'
param environmentName = '{{ .environmentName }}'
param nodeMitigationEnabled = {{ .mgmtAgent.nodeMitigation.enabled }}
