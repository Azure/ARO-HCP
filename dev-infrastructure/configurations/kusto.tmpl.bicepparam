using '../templates/kusto.bicep'


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

param auditLogsEventHubNamespaceName = '{{ .kusto.auditLogsEventHub.namespace }}'

param auditLogsEventHubName = '{{ .kusto.auditLogsEventHub.name }}'

param auditLogsKustoConsumerGroupName = '{{ .kusto.auditLogsEventHub.kustoConsumerGroupName }}'

param auditLogsDiagnosticSettingsRuleName = '{{ .kusto.auditLogsEventHub.authRuleName }}'
