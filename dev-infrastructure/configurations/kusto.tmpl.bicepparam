using '../templates/kusto.bicep'


param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param manageInstance = {{ .kusto.manageInstance }}

param environmentName = '{{ .environmentName }}'

param geoShortId = '{{ .geoShortId }}'

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param hostedControlPlaneLogsDatabase = '{{ .kusto.hostedControlPlaneLogsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

