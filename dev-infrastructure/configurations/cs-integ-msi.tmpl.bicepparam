using '../templates/dev-cs-integration-msi.bicep'

param namespaceFormatString = 'sandbox-jenkins-{0}-aro-hcp'
param clusterServiceManagedIdentityName = '{{ .clustersService.managedIdentityName }}'
param clusterName = '{{ .svc.aks.name }}'
param clusterServiceServiceAccountName = '{{ .clustersService.k8s.serviceAccountName }}'
