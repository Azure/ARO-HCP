using '../modules/geneva-health-function/amw-role-assignment.bicep'

param amwAccountName = '{{ .monitoring.hcpWorkspaceName }}'
param managedIdentityPrincipalId = '__managedIdentityPrincipalId__'
