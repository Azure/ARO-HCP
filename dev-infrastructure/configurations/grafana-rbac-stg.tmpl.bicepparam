using '../templates/grafana-rbac.bicep'

param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param grafanaRoles = '{{ .monitoring.grafanaRoles }}'
param azureFrontDoorProfileName = '{{ .stgGlobalV2.frontDoorName }}'
param azureFrontDoorManage = {{ .oidc.frontdoor.manage }}
