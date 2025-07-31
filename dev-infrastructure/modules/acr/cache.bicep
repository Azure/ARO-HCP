@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string

@description('Name of the KV that contains the pull secrets for ACR caching.')
param keyVaultName string

type repositoryCache = {
  @description('Name for the cache rule and credential set')
  ruleName: string

  @description('Source repository URL (e.g., "quay.io/openshift-release-dev/*")')
  sourceRepo: string

  @description('Target repository name (e.g., "openshift-release-dev/*")')
  targetRepo: string

  @description('Key Vault secret identifier for the username')
  userIdentifier: string

  @description('Key Vault secret identifier for the password')
  passwordIdentifier: string

  @description('Login server for the repository')
  loginServer: string
}

@description('Array of Quay repositories to cache with their configurations')
param quayRepositoriesToCache repositoryCache[]

type purgeJob = {
  @description('Name of the purge job')
  name: string

  @description('Filter pattern for images to purge')
  purgeFilter: string

  @description('How long ago to purge images (e.g., "2d", "7d")')
  purgeAfter: string

  @description('Number of images to keep')
  imagesToKeep: int
}

@description('Array of purge job configurations')
param purgeJobs purgeJob[] = []

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

//
//   C A C H E   R U L E S
//

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource cachePullCredential 'Microsoft.ContainerRegistry/registries/credentialSets@2023-01-01-preview' = [
  for repo in quayRepositoriesToCache: {
    name: repo.ruleName
    parent: acrResource
    identity: {
      type: 'SystemAssigned'
    }
    properties: {
      authCredentials: [
        {
          name: 'Credential1'
          passwordSecretIdentifier: '${keyVault.properties.vaultUri}secrets/${repo.passwordIdentifier}'
          usernameSecretIdentifier: '${keyVault.properties.vaultUri}secrets/${repo.userIdentifier}'
        }
      ]
      loginServer: repo.loginServer
    }
  }
]

resource cacheRule 'Microsoft.ContainerRegistry/registries/cacheRules@2023-01-01-preview' = [
  for (repo, i) in quayRepositoriesToCache: {
    name: repo.ruleName
    parent: acrResource
    properties: {
      credentialSetResourceId: cachePullCredential[i].id
      sourceRepository: repo.sourceRepo
      targetRepository: repo.targetRepo
    }
  }
]

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for (repo, i) in quayRepositoriesToCache: {
    scope: keyVault
    name: guid(keyVault.id, 'quayPullSecrets', 'read', repo.ruleName, acrName)
    properties: {
      roleDefinitionId: subscriptionResourceId(
        'Microsoft.Authorization/roleDefinitions/',
        '4633458b-17de-408a-b874-0445c86b69e6'
      )
      principalId: cachePullCredential[i].identity.principalId
      principalType: 'ServicePrincipal'
    }
  }
]

//
//   P U R G E   J O B S
//

resource purgeCached 'Microsoft.ContainerRegistry/registries/tasks@2019-04-01' = [
  for purgeJob in purgeJobs: {
    name: purgeJob.name
    location: location
    parent: acrResource
    properties: {
      agentConfiguration: {
        cpu: 2
      }
      platform: {
        architecture: 'amd64'
        os: 'Linux'
      }
      step: {
        encodedTaskContent: base64(format(
          '''
version: v1.1.0
steps:
  - cmd: acr purge --filter "{0}" --keep {1} --ago {2}
    disableWorkingDirectoryOverride: true
    timeout: 3600
''',
          purgeJob.purgeFilter,
          purgeJob.imagesToKeep,
          purgeJob.purgeAfter
        ))
        type: 'EncodedTask'
      }
      timeout: 3600
      trigger: {
        timerTriggers: [
          {
            name: 'daily'
            schedule: '0 0 * * *'
          }
        ]
      }
    }
  }
]
