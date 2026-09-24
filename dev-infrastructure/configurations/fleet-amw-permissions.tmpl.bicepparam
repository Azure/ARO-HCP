using '../templates/fleet-amw-permissions.bicep'

param fleetMIName = '{{ .fleet.managedIdentityName }}'
param fleetMIResourceGroup = '{{ .svc.rg }}'
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
param svcWorkspaceResourceId = '{{ .monitoring.svcWorkspaceResourceId }}'
param hcpWorkspaceResourceId = '{{ .monitoring.hcpWorkspaceResourceId }}'
