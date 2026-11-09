using '../templates/dev-roleassignments-svc.bicep'

param aksClusterName = '{{ .svc.aks.name }}'
param sharedKvNames = ['{{ .serviceKeyVault.name }}']
param sharedKvResourceGroup = '{{ .serviceKeyVault.rg }}'
param principalID = ''
