using '../templates/kusto-lookup.bicep'

param kustoName = '{{ .kusto.name }}'

param manageInstance = {{ .kusto.manageInstance }}
