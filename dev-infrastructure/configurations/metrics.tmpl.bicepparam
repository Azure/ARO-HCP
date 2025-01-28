using '../modules/metrics/metrics.bicep'

param monitorName = '{{ .monitoring.workspaceName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'
param msiName = '{{ .monitoring.msiName }}'
param globalResourceGroup = '{{ .global.rg }}'
