using '../modules/sessiongate/sessiongate-lookup.bicep'

param sessiongateMsiName = '{{ .sessiongate.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param aksClusterName = '{{ .svc.aks.name }}'
