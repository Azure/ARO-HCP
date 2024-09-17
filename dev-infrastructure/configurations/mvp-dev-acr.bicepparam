using '../templates/dev-acr.bicep'

param acrName = 'arohcpdev'
param acrSku = 'Premium'
param location = 'westus3'

param quayRepositoriesToCache = [
  {
    ruleName: 'openshiftReleaseDev'
    sourceRepo: 'quay.io/openshift-release-dev/*'
    targetRepo: 'openshift-release-dev/*'
    purgeFilter: 'quay.io/openshift-release-dev/.*:.*'
    purgeAfter: '2d'
    imagesToKeep: 1
    userIdentifier: 'quay-username'
    passwordIdentifier: 'quay-password'
  }
  {
    ruleName: 'csSandboxImages'
    sourceRepo: 'quay.io/app-sre/ocm-clusters-service-sandbox'
    targetRepo: 'app-sre/ocm-clusters-service-sandbox'
    purgeFilter: 'quay.io/app-sre/ocm-clusters-service-sandbox:.*'
    purgeAfter: '2d'
    imagesToKeep: 1
    userIdentifier: 'quay-componentsync-username'
    passwordIdentifier: 'quay-componentsync-password'
  }
]

param keyVaultName = 'aro-hcp-dev-global-kv'
