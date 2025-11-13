using '../templates/kusto.bicep'


param sku = '{{ .kusto.sku }}'
param tier = '{{ .kusto.tier }}'

param manageInstance = {{ .kusto.manageInstance }}

param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'

param customerLogsDatabase = '{{ .kusto.customerLogsDatabase }}'

param adminGroups = '{{ .kusto.adminGroups }}'

param viewerGroups = '{{ .kusto.viewerGroups }}'

