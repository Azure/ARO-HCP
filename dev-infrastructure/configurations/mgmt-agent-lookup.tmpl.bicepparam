using '../modules/mgmt-agent/mgmt-agent-lookup.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param managementIdentityResourceGroups = '{{ .infrastructureIdentities.managementResourceGroups }}'
param stampIdentifier = '{{ .mgmt.stampIdentifier }}'

param msiName = '{{ .mgmtAgent.managedIdentityName }}'
