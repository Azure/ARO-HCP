using '../templates/output-region.bicep'

param azureMonitorWorkspaceName = '{{ .monitoring.svcWorkspaceName }}'
param hcpAzureMonitorWorkspaceName = '{{ .monitoring.hcpWorkspaceName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param enableLogAnalytics = {{ .logs.loganalytics.enable }}
