using '../templates/dev-acr.bicep'

param acrName = 'arohcpocpdev'
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
]

param purgeJobs = [
  {
    name: 'openshift-release-dev-purge'
    purgeFilter: 'quay.io/openshift-release-dev/.*:.*'
    purgeAfter: '2d'
    imagesToKeep: 1
  }
]

param keyVaultName = 'aro-hcp-dev-global-kv'
