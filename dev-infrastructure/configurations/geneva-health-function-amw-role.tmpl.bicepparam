using '../modules/geneva-health-function/amw-role-assignment.bicep'

param deploy = {{ .genevaHealthFunction.deploy }}
param amwAccountName = '{{ .monitoring.hcpWorkspaceName }}'
param managedIdentityPrincipalId = '__managedIdentityPrincipalId__'
