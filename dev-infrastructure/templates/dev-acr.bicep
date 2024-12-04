/*
Setup caching rules and purge jobs for Azure Container Registry.
Used in DEV environment only.
Depends on ACRs being provisioned beforehands by the global-acr.bicep template.
*/

@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('List of quay repositories to cache in the Azure Container Registry.')
param quayRepositoriesToCache array = []

@description('List of jobs to run to purge old images from Azure Container Registry')
param purgeJobs array = []

@description('Name of the global key vault.')
param keyVaultName string = ''

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

resource pullCredential 'Microsoft.ContainerRegistry/registries/credentialSets@2023-01-01-preview' = [
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
      loginServer: 'quay.io'
    }
  }
]

resource cacheRule 'Microsoft.ContainerRegistry/registries/cacheRules@2023-01-01-preview' = [
  for (repo, i) in quayRepositoriesToCache: {
    name: repo.ruleName
    parent: acrResource
    properties: {
      credentialSetResourceId: pullCredential[i].id
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
      principalId: pullCredential[i].identity.principalId
      principalType: 'ServicePrincipal'
    }
  }
]

resource purgeCached 'Microsoft.ContainerRegistry/registries/tasks@2019-04-01' = [
  for purgeJob in purgeJobs: {
    name: '${purgeJob.name}'
    location: resourceGroup().location
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
