using '../templates/kusto.bicep'

param location = '{{ .kusto.location }}'

param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param kustoName = '{{ .kusto.kustoName }}'

param geoShortId = '{{ .azureGeoShortId }}'

param enableGrafanaIntegration = {{ .monitoring.adxKustoAccessEnabled }}

param grafanaPrincipalId = '__grafanaPrincipalId__'

param manageInstance = {{ .kusto.manageInstance }}

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param monitoringEventsDatabase = '{{ .kusto.monitoringEventsDatabase }}'

param allAroHcpLogsDatabase = '{{ .kusto.allAroHcpLogsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

param viewerIdentities = '{{ .kusto.viewerIdentities }}'

param globalMSIName = '{{ .global.globalMSIName }}'

param globalMSIResourceGroup = '{{ .global.rg }}'

param environment = '{{ .environmentName }}'

param autoScaleMin = {{ .kusto.autoScaleMin }}

param autoScaleMax = {{ .kusto.autoScaleMax }}

param enableAutoScale = {{ .kusto.enableAutoScale }}
