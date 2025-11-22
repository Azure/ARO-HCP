using '../templates/kusto-lookup.bicep'

param kustoName = '{{ .kusto.kustoName }}'

param manageInstance = {{ .kusto.manageInstance }}
