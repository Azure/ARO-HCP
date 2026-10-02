using '../templates/fleet-amw-permissions.bicep'

param fleetMIName = '{{ .fleet.managedIdentityName }}'
param fleetMIResourceGroup = {{ .infrastructureIdentities.useLeased }} ? '{{ .infrastructureIdentities.serviceResourceGroup }}' : '{{ .svc.rg }}'
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
