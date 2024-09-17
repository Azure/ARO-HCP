@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string = resourceGroup().location

@description('Service tier of the Azure Container Registry.')
param acrSku string

@description('List of quay repositories to cache in the Azure Container Registry.')
param quayRepositoriesToCache array = []

@description('Name of the global key vault.')
param keyVaultName string = ''

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: acrName
  location: location
  sku: {
    name: acrSku
  }
  properties: {
    adminUserEnabled: false
    anonymousPullEnabled: false
    // Premium-only feature
    // https://azure.microsoft.com/en-us/blog/azure-container-registry-mitigating-data-exfiltration-with-dedicated-data-endpoints/
    dataEndpointEnabled: false
    encryption: {
      // The naming of this field is misleading - it disables encryption with a customer-managed key.
      // Data in Azure Container Registry is encrypted, just with an Azure-managed key.
      // https://learn.microsoft.com/en-us/azure/container-registry/tutorial-enable-customer-managed-keys#show-encryption-status
      status: 'disabled'
    }
    policies: {
      azureADAuthenticationAsArmPolicy: {
        status: 'enabled'
      }
      softDeletePolicy: {
        retentionDays: 7
        status: 'disabled'
      }
    }
  }
}

// https://learn.microsoft.com/en-us/azure/container-registry/container-registry-tasks-overview
resource acrPurgeTask 'Microsoft.ContainerRegistry/registries/tasks@2019-04-01' = {
  name: '${acrName}-purge'
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
      encodedTaskContent: base64('''
version: v1.1.0
steps: 
  - cmd: acr purge --filter "arohcpfrontend:.*" --keep 3 --ago 7d
    disableWorkingDirectoryOverride: true
    timeout: 3600
''')
      type: 'EncodedTask'
    }
    timeout: 3600
    trigger: {
      timerTriggers: [
        {
          name: 'weekly'
          schedule: '0 0 * * *'
        }
      ]
    }
  }
}

@description('Login server property for later use')
output loginServer string = acrResource.properties.loginServer

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
    name: guid(keyVault.id, 'quayPullSecrets', 'read', repo.ruleName)
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
  for repo in quayRepositoriesToCache: {
    name: '${repo.ruleName}-purge'
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
          repo.purgeFilter,
          repo.imagesToKeep,
          repo.purgeAfter
        ))
        type: 'EncodedTask'
      }
      timeout: 3600
      trigger: {
        timerTriggers: [
          {
            name: 'daily'
            schedule: '0 * * * *'
          }
        ]
      }
    }
  }
]
