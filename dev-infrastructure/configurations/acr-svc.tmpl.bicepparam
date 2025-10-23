using '../templates/dev-acr.bicep'

param acrName = '{{ .acr.svc.name }}'

param quayRepositoriesToCache = [
  {
    ruleName: 'csSandboxImages'
    sourceRepo: 'quay.io/app-sre/ocm-clusters-service-sandbox'
    targetRepo: 'app-sre/ocm-clusters-service-sandbox'
    userIdentifier: 'quay-componentsync-username'
    passwordIdentifier: 'quay-componentsync-password'
  }
  {
    ruleName: 'aroHcpCsSandboxImages'
    sourceRepo: 'quay.io/app-sre/aro-hcp-clusters-service-sandbox'
    targetRepo: 'app-sre/aro-hcp-clusters-service-sandbox'
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
    name: 'aro-hcp-clusters-service-sandbox-purge'
    purgeFilter: 'quay.io/app-sre/aro-hcp-clusters-service-sandbox:.*'
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

param keyVaultName = '{{ .serviceKeyVault.name }}'

param location = '{{ .global.region }}'
