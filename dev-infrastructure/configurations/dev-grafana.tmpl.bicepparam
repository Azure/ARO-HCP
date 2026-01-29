using '../templates/dev-grafana.bicep'

param location = '{{ .global.region }}'
param globalMSIName = '{{ .global.globalMSIName }}'

param grafanaName = '{{ .monitoring.grafanaName }}'
param grafanaMajorVersion = '{{ .monitoring.grafanaMajorVersion }}'
param grafanaZoneRedundantMode = '{{ .monitoring.grafanaZoneRedundantMode }}'
param grafanaRoles = '{{ .monitoring.grafanaRoles }}'
param crossTenantSecurityGroup = '{{ .monitoring.crossTenantSecurityGroup }}'
