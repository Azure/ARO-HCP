using '../modules/metrics/metrics.bicep'

param monitorName = '{{ .monitoringWorkspaceName }}'
param grafanaName = '{{ .grafanaName }}'
param msiName = '{{ .monitoringMsiName }}'
param grafanaAdminGroupPrincipalId = '{{ .grafanaAdminGroupPrincipalId }}'
param globalResourceGroup = '{{ .regionRG }}'
