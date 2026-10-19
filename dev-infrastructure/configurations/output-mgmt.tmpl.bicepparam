using '../templates/output-mgmt.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param managementIdentityResourceGroups = '{{ .infrastructureIdentities.managementResourceGroups }}'
param stampIdentifier = '{{ .mgmt.stampIdentifier }}'

param mgmtClusterName = '{{ .mgmt.aks.name }}'
param backupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccount.name }}'
param veleroMsiName = 'velero'
