using '../templates/dev-cs-integration-msi.bicep'

param namespaceFormatString = 'sandbox-jenkins-{0}-aro-hcp'
param clusterServiceManagedIdentityName = '{{ .clusterService.managedIdentityName }}'
param clusterName = '{{ .svc.aks.name }}'
param clusterServiceServiceAccountName = '{{ .clusterService.k8s.serviceAccountName }}'
