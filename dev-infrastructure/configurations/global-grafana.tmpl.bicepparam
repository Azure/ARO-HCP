using '../templates/global-grafana.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'
param grafanaAdminGroupPrincipalId = '{{ .monitoring.grafanaAdminGroupPrincipalId }}'
param grafanaZoneRedundantMode = '{{ .monitoring.grafanaZoneRedundantMode }}'
