using '../templates/monitoring.bicep'

param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

param devAlertingEmails = '{{ .monitoring.devAlertingEmails }}'

param icmEnvironment = '{{ .monitoring.icm.environment }}'
param icmActionGroupName = '{{ .monitoring.icm.actionGroupName }}'
param icmActionGroupShortName = '{{ .monitoring.icm.actionGroupShortName }}'
param icmRoutingId = '{{ .monitoring.icm.routingId }}'
param icmConnectionName = '{{ .monitoring.icm.connectionName }}'
param icmConnectionId = '{{ .monitoring.icm.connectionId }}'
param icmAutomitigationEnabled = '{{ .monitoring.icm.automitigationEnabled }}'
