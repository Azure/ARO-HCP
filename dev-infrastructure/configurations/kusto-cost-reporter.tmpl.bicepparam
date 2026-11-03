using '../templates/kusto-cost-reporter.bicep'

param enabled = {{ .kusto.costReporting.enabled }}
param identityName = '{{ .kusto.costReporting.identityName }}'
