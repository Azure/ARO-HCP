@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string = resourceGroup().location

@description('Service tier of the Azure Container Registry.')
param acrSku string

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

// Assign the AcrPull Role to the ${acrName}-pull-identity
var acrPullRoleId = '7f951dda-4ed3-4680-a7ca-43fe172d538d'

resource acrMsi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${acrName}-pull-identity'
  location: resourceGroup().location
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acrName, acrMsi.name, acrPullRoleId)
  scope: acrResource
  properties: {
    principalId: acrMsi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', acrPullRoleId)
  }
}

resource tokenManagementRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(resourceGroup().id, 'token-creation-role')
  properties: {
    roleName: 'ACR Manage Tokens'
    type: 'customRole'
    assignableScopes: [
      subscription().id
    ]
    description: 'This role allows the management of tokens in the ACR'
    permissions: [
      {
        actions: [
          'Microsoft.ContainerRegistry/registries/tokens/read'
          'Microsoft.ContainerRegistry/registries/tokens/write'
          'Microsoft.ContainerRegistry/registries/tokens/delete'
          'Microsoft.ContainerRegistry/registries/generateCredentials/action'
          'Microsoft.ContainerRegistry/registries/tokens/operationStatuses/read'
          'Microsoft.ContainerRegistry/registries/scopeMaps/read'
        ]
      }
    ]
  }
}
