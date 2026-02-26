using '../templates/kusto-lookup.bicep'

param kustoName = '{{ .kusto.kustoName }}'

param kustoEnabled = {{ .arobit.kusto.enabled }}

param auditLogsEventHubNamespaceName = '{{ .kusto.auditLogsEventHub.namespace }}'

param auditLogsEventHubName = '{{ .kusto.auditLogsEventHub.name }}'

param auditLogsEventHubAuthRuleName = '{{ .kusto.auditLogsEventHub.authRuleName }}'
