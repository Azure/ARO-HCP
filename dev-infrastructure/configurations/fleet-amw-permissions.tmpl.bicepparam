using '../templates/fleet-amw-permissions.bicep'

param fleetMIName = '{{ .fleet.managedIdentityName }}'
param fleetMIResourceGroup = '{{ .svc.rg }}'
param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
