using '../templates/output-mgmt-cluster.bicep'

param aksClusterName = '{{ .mgmt.aks.name }}'

param logsMSI = '{{ .logs.mdsd.msiName }}'

param svcWorkspaceLocation = '__svcWorkspaceLocation__'
param hcpWorkspaceLocation = '__hcpWorkspaceLocation__'
