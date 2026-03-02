using '../templates/kusto-lookup.bicep'

param kustoName = '{{ .kusto.kustoName }}'

param kustoEnabled = {{ .arobit.kusto.enabled }}
