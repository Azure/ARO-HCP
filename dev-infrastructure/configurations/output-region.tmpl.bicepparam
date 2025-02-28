using '../templates/output-region.bicep'

param azureMonitorWorkspaceName = '{{ .monitoring.workspaceName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param enableLogAnalytics = {{ .logs.loganalytics.enable }}
