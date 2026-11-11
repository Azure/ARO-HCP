using '../../tooling/image-updater/alerting.bicep'

param azureMonitorWorkspaceId = '__azureMonitorWorkspaceId__'
param sharedActionGroupId = '__sharedActionGroupId__'
// Keep disabled until the rules have been validated against live metrics in
// the Azure Monitor Workspace query explorer, then align with the
// environment-level toggle: {{ .opstool.alerting.enabled }}
param alertingEnabled = false
param maxConsecutiveFailures = 3
param weekdayStallSeconds = 32400
param mondayStallSeconds = 208800
param maxRunDurationSeconds = 3600
