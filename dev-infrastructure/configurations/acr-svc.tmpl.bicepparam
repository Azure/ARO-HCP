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
    name: 'sandbox-images-purge'
    purgeFilters: [
      'app-sre/ocm-clusters-service-sandbox:.*'
      'app-sre/aro-hcp-clusters-service-sandbox:.*'
    ]
    purgeAfter: '2d'
    imagesToKeep: 1
  }
  {
    name: 'service-images-purge'
    purgeFilters: [
      'arohcp.*:.*'
      'fleet:.*'
      'kube-applier:.*'
      'mgmt-agent:.*'
      'test-.*:.*'
    ]
    purgeAfter: '7d'
    imagesToKeep: 3
  }
]

param repositoryDeleteJobs = [
  {
    name: 'ci-repositories-delete'
    repositoryPattern: '^ci-op-[a-z0-9]+/pipeline$'
    deleteAfterDays: 30
  }
]

param keyVaultName = '{{ .serviceKeyVault.name }}'

param location = '{{ .global.region }}'
