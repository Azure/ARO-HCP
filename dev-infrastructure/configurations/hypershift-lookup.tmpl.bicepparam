using '../templates/hypershift-lookup.bicep'

param aksClusterName = '{{ .mgmt.aks.name }}'
param etcdBackupMsiName = 'etcd-backup'

