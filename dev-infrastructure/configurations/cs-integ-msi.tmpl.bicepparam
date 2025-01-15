using '../templates/cs-integration-msi.bicep'

param namespaceFormatString = 'sandbox-jenkins-{0}-aro-hcp'
param clusterServiceManagedIdentityName = '{{ .clusterService.managedIdentityName }}'
param clusterName = '{{ .aksName }}'
param clusterServiceServiceAccountName = '{{ .clusterService.k8s.serviceAccountName }}'
