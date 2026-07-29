using '../templates/monitoring.bicep'

param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'
param region = '{{ .region }}'

param actionGroupSL = '__actionGroupSL__'
param actionGroupSRE = '__actionGroupSRE__'
param actionGroupRP = '__actionGroupRP__'
param actionGroupMSFT = '__actionGroupMSFT__'

param alertsEnabled = {{ .monitoring.alertsEnabled }}
param alertSeverityCeiling = {{ .monitoring.alertSeverityCeiling }}
param icmEnabledSRE = {{ .monitoring.icm.sre.enabled }}
param icmEnabledSL = {{ .monitoring.icm.sl.enabled }}
param icmEnabledRP = {{ .monitoring.icm.rp.enabled }}
param icmEnabledMSFT = {{ .monitoring.icm.msft.enabled }}
param eventHubAlertingEnabled = {{ .monitoring.eventHubAlerting.enabled }}
param alertEventsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param alertEventsEventHubName = '{{ .alertEventsEventHub.name }}'
