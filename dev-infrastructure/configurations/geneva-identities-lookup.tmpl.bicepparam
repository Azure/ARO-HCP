using '../templates/geneva-identities-lookup.bicep'

param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param genevaActionApplicationManage = {{ .geneva.actions.application.manage }}
