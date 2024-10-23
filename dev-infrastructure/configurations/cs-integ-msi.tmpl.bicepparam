using '../templates/cs-integration-msi.bicep'

param namespaceFormatString = 'sandbox-jenkins-{0}-aro-hcp'
param clusterServiceManagedIdentityName = 'clusters-service'
param clusterName = '{{ .aksName }}'
