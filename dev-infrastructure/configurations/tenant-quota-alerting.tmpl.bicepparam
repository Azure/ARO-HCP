using '../ops-tools/tenant-quota/alerting.bicep'

param azureMonitorWorkspaceId = '__azureMonitorWorkspaceId__'
param sharedActionGroupId = '__sharedActionGroupId__'
param alertingEnabled = {{ .opstool.alerting.enabled }}
