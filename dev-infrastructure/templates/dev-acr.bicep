@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string = resourceGroup().location

@description('Service tier of the Azure Container Registry.')
param acrSku string

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool


@description('Vew all resources, but does not allow you to make any changes.')
var keyVaultReadUserId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource acrUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${acrName}-msi'
  location: location
}


resource acrKeyVaultReadRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(keyVaultReadUserId)
  scope: 'service-kv-aro-hcp-dev'
  properties: {
    roleDefinitionId: keyVaultReadUserId
    principalId: acrUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}


resource acrResource 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: acrName
  location: location
  tags: {
    persist: toLower(string(persist))
  }
  sku: {
    name: acrSku
  }
  properties: {
    adminUserEnabled: false
    // Premium-only feature
    // https://azure.microsoft.com/en-us/blog/azure-container-registry-mitigating-data-exfiltration-with-dedicated-data-endpoints/
    dataEndpointEnabled: false
    encryption: {
      // The naming of this field is misleading - it disables encryption with a customer-managed key.
      // Data in Azure Container Registry is encrypted, just with an Azure-managed key.
      // https://learn.microsoft.com/en-us/azure/container-registry/tutorial-enable-customer-managed-keys#show-encryption-status
      status: 'disabled'
    }
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${acrUserDefinedManagedIdentity.id}': {}
    }
  }
}


resource quayCredentials 'Microsoft.ContainerRegistry/registries/credentialSets@2023-01-01-preview' = {
  name: 'string'
  parent: acrResource
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${acrUserDefinedManagedIdentity.id}': {}
    }
  }
  properties: {
    authCredentials: [
      {
        name: 'TODO'
        passwordSecretIdentifier: 'TODO'
        usernameSecretIdentifier: 'TODO'
      }
    ]
    loginServer: 'TODO'
  }
}


resource quayCacheRule 'Microsoft.ContainerRegistry/registries/cacheRules@2023-01-01-preview' = {
  name: 'string'
  parent: acrResource
  properties: {
    credentialSetResourceId: 'string'
    sourceRepository: 'quay.io/*'
    targetRepository: '*'
  }
}

// https://learn.microsoft.com/en-us/azure/container-registry/container-registry-tasks-overview
resource acrPurgeTask 'Microsoft.ContainerRegistry/registries/tasks@2019-04-01' = {
  name: '${acrName}-purge'
  location: location
  parent: acrResource
  tags: {
    persist: toLower(string(persist))
  }
  properties: {
    agentConfiguration: {
      cpu: 2
    }
    platform: {
      architecture: 'amd64'
      os: 'Linux'
    }
    step: {
      encodedTaskContent: base64('acr purge --ago 7d')
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
