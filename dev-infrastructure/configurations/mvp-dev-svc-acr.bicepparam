using '../templates/dev-acr.bicep'

param acrName = 'arohcpsvcdev'
param acrSku = 'Premium'
param location = 'westus3'

param quayRepositoriesToCache = [
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
    name: 'arohcpfrontend-purge'
    purgeFilter: 'arohcpfrontend:.*'
    purgeAfter: '7d'
    imagesToKeep: 3
  }
]

param keyVaultName = 'aro-hcp-dev-global-kv'
