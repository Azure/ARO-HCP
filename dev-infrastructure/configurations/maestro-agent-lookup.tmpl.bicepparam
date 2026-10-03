using '../modules/maestro/maestro-agent-lookup.bicep'

param msiName = '{{ .maestro.agent.managedIdentityName }}'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param managementIdentityResourceGroups = '{{ .infrastructureIdentities.managementResourceGroups }}'
param stampIdentifier = '{{ .mgmt.stampIdentifier }}'
