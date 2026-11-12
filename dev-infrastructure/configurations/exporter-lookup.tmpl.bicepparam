using '../templates/exporter-lookup.bicep'

param msiName = '{{ .customExporter.managedIdentityName }}'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'
