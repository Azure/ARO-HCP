using '../templates/dev-acr.bicep'

param acrName = 'arohcpdev'
param acrSku = 'Premium'
param location = 'westus3'

param quayRepositoriesToCache = [
  {
    ruleName: 'openshiftReleaseDev'
    sourceRepo: 'quay.io/openshift-release-dev/*'
    targetRepo: 'openshift-release-dev/*'
    userIdentifier: 'quay-username'
    passwordIdentifier: 'quay-password'
  }
  {
    ruleName: 'csSandboxImages'
    sourceRepo: 'quay.io/app-sre/ocm-clusters-service-sandbox'
    targetRepo: 'app-sre/ocm-clusters-service-sandbox'
    userIdentifier: 'quay-componentsync-username'
    passwordIdentifier: 'quay-componentsync-password'
  }
]

param purgeJobs = [
  {
    name: 'ocm-clusters-service-sandbox-purge'
    purgeFilter: 'quay.io/app-sre/ocm-clusters-service-sandbox:.*'
    purgeAfter: '2d'
    imagesToKeep: 1
  }
  {
    name: 'openshift-release-dev-purge'
    purgeFilter: 'quay.io/openshift-release-dev/.*:.*'
    purgeAfter: '2d'
    imagesToKeep: 1
  }
  {
    name: 'arohcpfrontend-purge'
    purgeFilter: 'arohcpfrontend:.*'
    purgeAfter: '7d'
    imagesToKeep: 3
  }
]

param keyVaultName = 'aro-hcp-dev-global-kv'
