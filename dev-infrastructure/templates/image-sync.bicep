@description('Azure Region Location')
param location string = resourceGroup().location

@description('Specifies the name of the container app environment.')
param containerAppEnvName string = 'image-sync-env-${uniqueString(resourceGroup().id)}'

@description('Specifies the name of the log analytics workspace.')
param containerAppLogAnalyticsName string = 'containerapp-log-${uniqueString(resourceGroup().id)}'

@description('Specifies the name of the user assigned managed identity.')
param imageSyncManagedIdentity string = 'image-sync-${uniqueString(resourceGroup().id)}'

@description('Resource group of the ACR containerapps will get permissions on')
param acrResourceGroup string

@description('Name of the pull secret')
param requiredSecretNames array

@description('Name of the keyvault where the pull secret is stored')
param keyVaultName string

@description('Name of the KeyVault RG')
param keyVaultResourceGroup string = 'global'

resource logAnalytics 'Microsoft.OperationalInsights/workspaces@2021-06-01' = {
  name: containerAppLogAnalyticsName
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
  }
}

resource containerAppEnvironment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: containerAppEnvName
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalytics.properties.customerId
        sharedKey: logAnalytics.listKeys().primarySharedKey
      }
    }
  }
}

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: imageSyncManagedIdentity
  location: location
}

module acrContributorRole '../modules/acr-permissions.bicep' = {
  name: guid(imageSyncManagedIdentity, 'acr', 'readwrite')
  scope: resourceGroup(acrResourceGroup)
  params: {
    principalId: uami.properties.principalId
    grantPushAccess: true
    acrResourceGroupid: acrResourceGroup
  }
}

module acrPullRole '../modules/acr-permissions.bicep' = {
  name: guid(imageSyncManagedIdentity, 'acr', 'pull')
  scope: resourceGroup(acrResourceGroup)
  params: {
    principalId: uami.properties.principalId
    acrResourceGroupid: acrResourceGroup
  }
}

module pullSecretPermission '../modules/keyvault/keyvault-secret-access.bicep' = [
  for secretName in requiredSecretNames: {
    name: '${secretName}-access'
    scope: resourceGroup(keyVaultResourceGroup)
    params: {
      keyVaultName: keyVaultName
      secretName: secretName
      roleName: 'Key Vault Secrets User'
      managedIdentityPrincipalId: uami.properties.principalId
    }
  }
]
