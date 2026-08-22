using '../templates/kusto.bicep'

param location = '{{ .kusto.location }}'

param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param kustoName = '{{ .kusto.kustoName }}'

param geoShortId = '{{ .azureGeoShortId }}'

param environmentName = '{{ .environmentName }}'

param enableGrafanaIntegration = {{ .monitoring.adxKustoAccessEnabled }}

param grafanaPrincipalId = '__grafanaPrincipalId__'

param manageInstance = {{ .kusto.manageInstance }}

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param hostedControlPlaneLogsDatabase = '{{ .kusto.hostedControlPlaneLogsDatabase }}'

param monitoringEventsDatabase = '{{ .kusto.monitoringEventsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

param viewerIdentities = '{{ .kusto.viewerIdentities }}'

param autoScaleMin = {{ .kusto.autoScaleMin }}

param autoScaleMax = {{ .kusto.autoScaleMax }}

param enableAutoScale = {{ .kusto.enableAutoScale }}
