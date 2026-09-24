using '../templates/output-region.bicep'

param azureMonitorWorkspaceName = '{{ .monitoring.svcWorkspaceName }}'
param hcpAzureMonitorWorkspaceName = '{{ .monitoring.hcpWorkspaceName }}'
param svcWorkspaceResourceId = '{{ .monitoring.svcWorkspaceResourceId }}'
param hcpWorkspaceResourceId = '{{ .monitoring.hcpWorkspaceResourceId }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
