using '../modules/sessiongate/sessiongate-lookup.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'

param sessiongateMsiName = '{{ .sessiongate.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param aksClusterName = '{{ .svc.aks.name }}'
