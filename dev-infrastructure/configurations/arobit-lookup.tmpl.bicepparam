using '../templates/arobit-lookup.bicep'

param msiName = '{{ .logs.mdsd.msiName }}'
param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'
