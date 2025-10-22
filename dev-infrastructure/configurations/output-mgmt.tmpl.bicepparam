using '../templates/output-mgmt.bicep'

param mgmtClusterName = '{{ .mgmt.aks.name }}'
param backupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccount.name }}'
param veleroMsiName = 'velero'
param oadpControllerMsiName = 'openshift-adp-controller-manager'
