using '../templates/dev-acr.bicep'

param acrName = '{{ .acr.ocp.name }}'

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

param keyVaultName = '{{ .serviceKeyVault.name }}'

param location = '{{ .global.region }}'
