using '../templates/monitoring.bicep'

param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

param manageConnection = {{ .monitoring.icm.manageConnection }}
param icmEnvironment = '{{ .monitoring.icm.environment }}'
param icmConnectionName = '{{ .monitoring.icm.connectionName }}'
param icmConnectionId = '{{ .monitoring.icm.connectionId }}'
param icmActionGroupNameSRE = '{{ .monitoring.icm.sre.actionGroupName }}'
param icmActionGroupShortNameSRE = '{{ .monitoring.icm.sre.actionGroupShortName }}'
param icmRoutingIdSRE = '{{ .monitoring.icm.sre.routingId }}'
param icmAutomitigationEnabledSRE = '{{ .monitoring.icm.sre.automitigationEnabled }}'
param icmActionGroupNameSL = '{{ .monitoring.icm.sl.actionGroupName }}'
param icmActionGroupShortNameSL = '{{ .monitoring.icm.sl.actionGroupShortName }}'
param icmRoutingIdSL = '{{ .monitoring.icm.sl.routingId }}'
param icmAutomitigationEnabledSL = '{{ .monitoring.icm.sl.automitigationEnabled }}'
