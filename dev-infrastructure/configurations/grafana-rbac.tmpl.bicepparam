using '../templates/grafana-rbac.bicep'

param grafanaName = '{{ .monitoring.grafanaName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param grafanaRoles = '{{ .monitoring.grafanaRoles }}'
param azureFrontDoorProfileName = '{{ .oidc.frontdoor.name }}'
param azureFrontDoorManage = {{ .oidc.frontdoor.manage }}
