using '../templates/monitoring.bicep'

param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

param manageConnection = {{ .monitoring.icm.manageConnection }}
param alertsEnabled = {{ .monitoring.alertsEnabled }}
param alertSeverityCeiling = {{ .monitoring.alertSeverityCeiling }}
param icmEnvironment = '{{ .monitoring.icm.environment }}'
param icmConnectionName = '{{ .monitoring.icm.connectionName }}'
param icmConnectionId = '{{ .monitoring.icm.connectionId }}'
param icmEnabledSRE = {{ .monitoring.icm.sre.enabled }}
param icmActionGroupNameSRE = '{{ .monitoring.icm.sre.actionGroupName }}'
param icmActionGroupShortNameSRE = '{{ .monitoring.icm.sre.actionGroupShortName }}'
param icmRoutingIdSRE = '{{ .monitoring.icm.sre.routingId }}'
param icmAutomitigationEnabledSRE = '{{ .monitoring.icm.sre.automitigationEnabled }}'
param icmEnabledSL = {{ .monitoring.icm.sl.enabled }}
param icmActionGroupNameSL = '{{ .monitoring.icm.sl.actionGroupName }}'
param icmActionGroupShortNameSL = '{{ .monitoring.icm.sl.actionGroupShortName }}'
param icmRoutingIdSL = '{{ .monitoring.icm.sl.routingId }}'
param icmAutomitigationEnabledSL = '{{ .monitoring.icm.sl.automitigationEnabled }}'
param icmEnabledRP = {{ .monitoring.icm.rp.enabled }}
param icmActionGroupNameRP = '{{ .monitoring.icm.rp.actionGroupName }}'
param icmActionGroupShortNameRP = '{{ .monitoring.icm.rp.actionGroupShortName }}'
param icmRoutingIdRP = '{{ .monitoring.icm.rp.routingId }}'
param icmAutomitigationEnabledRP = '{{ .monitoring.icm.rp.automitigationEnabled }}'
param icmEnabledMSFT = {{ .monitoring.icm.msft.enabled }}
param icmActionGroupNameMSFT = '{{ .monitoring.icm.msft.actionGroupName }}'
param icmActionGroupShortNameMSFT = '{{ .monitoring.icm.msft.actionGroupShortName }}'
param icmRoutingIdMSFT = '{{ .monitoring.icm.msft.routingId }}'
param icmAutomitigationEnabledMSFT = '{{ .monitoring.icm.msft.automitigationEnabled }}'
param eventHubAlertingEnabled = {{ .monitoring.eventHubAlerting.enabled }}
param alertEventsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param alertEventsEventHubName = '{{ .alertEventsEventHub.name }}'
