using '../templates/backend-amw-permissions.bicep'

param backendMIName = '{{ .backend.managedIdentityName }}'
param backendMIResourceGroup = '{{ .svc.rg }}'
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
