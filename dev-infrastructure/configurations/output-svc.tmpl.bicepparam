using '../templates/output-svc.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'

param csMIName = '{{ .clustersService.managedIdentityName }}'
param msiRefresherMIName = '{{ .msiCredentialsRefresher.managedIdentityName }}'
param adminApiMIName = '{{ .adminApi.managedIdentityName }}'
param backendMIName = '{{ .backend.managedIdentityName }}'
param sessiongateMIName = '{{ .sessiongate.managedIdentityName }}'
param fleetMIName = '{{ .fleet.managedIdentityName }}'
param exporterMIName = '{{ .customExporter.managedIdentityName }}'
