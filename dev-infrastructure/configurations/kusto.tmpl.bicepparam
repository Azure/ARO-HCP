using '../templates/kusto.bicep'

param location = '{{ .kusto.location }}'

param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param kustoName = '{{ .kusto.kustoName }}'

param manageInstance = {{ .kusto.manageInstance }}

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param hostedControlPlaneLogsDatabase = '{{ .kusto.hostedControlPlaneLogsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

param autoScaleMin = {{ .kusto.autoScaleMin }}

param autoScaleMax = {{ .kusto.autoScaleMax }}

param enableAutoScale = {{ .kusto.enableAutoScale }}

param crossClusterServiceLogsScript = {{ if .kusto.crossClusterServiceLogsScript }}'''{{ .kusto.crossClusterServiceLogsScript }}'''{{ else }}''{{ end }}

param crossClusterHostedControlPlaneLogsScript = {{ if .kusto.crossClusterHostedControlPlaneLogsScript }}'''{{ .kusto.crossClusterHostedControlPlaneLogsScript }}'''{{ else }}''{{ end }}
