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

@description('Name of the service component ACR registry')
param svcAcrName string

@description('Name of the keyvault where the pull secret is stored')
param keyVaultName string

@description('Name of the KeyVault RG')
param keyVaultResourceGroup string = 'global'

@description('The name of the pull secret')
param pullSecretName string

@description('The name of the Quay API bearer token secret')
param bearerSecretName string

@description('The image to use for the component sync job')
param componentSyncImage string

@description('A CSV of the repositories to sync')
param repositoriesToSync string

@description('The number of tags to sync per image in the repo list')
param numberOfTags int = 10

//
// Container App Infra
//

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

// TODO: define permissions on ACR level instead of RG level
// ACRs can be in different RGs or even subscriptions. ideally we should
// be able to deal with ACR resource IDs as input instead of RG and ACR names

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
  for secretName in [pullSecretName, bearerSecretName]: {
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

//
// Component sync job
//

var jobName = 'component-sync'
var pullSecretFile = 'quayio-auth.json'

resource componentSyncJob 'Microsoft.App/jobs@2024-03-01' = {
  name: jobName
  location: location

  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }

  properties: {
    environmentId: containerAppEnvironment.id
    configuration: {
      eventTriggerConfig: {}
      triggerType: 'Schedule'
      scheduleTriggerConfig: {
        cronExpression: '*/5 * * * *'
        parallelism: 1
      }
      replicaTimeout: 60 * 60
      registries: [
        {
          identity: uami.id
          server: '${svcAcrName}${environment().suffixes.acrLoginServer}'
        }
      ]
      secrets: [
        {
          name: 'pull-secrets'
          keyVaultUrl: 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${pullSecretName}'
          identity: uami.id
        }
        {
          name: 'bearer-secret'
          keyVaultUrl: 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${bearerSecretName}'
          identity: uami.id
        }
      ]
    }
    template: {
      containers: [
        {
          name: jobName
          image: componentSyncImage
          volumeMounts: [
            { volumeName: 'pull-secrets-updated', mountPath: '/auth' }
          ]
          env: [
            { name: 'NUMBER_OF_TAGS', value: '${numberOfTags}' }
            { name: 'REPOSITORIES', value: repositoriesToSync }
            { name: 'QUAY_SECRET_FILE', value: '/auth/${pullSecretFile}' }
            { name: 'ACR_REGISTRY', value: '${svcAcrName}${environment().suffixes.acrLoginServer}' }
            { name: 'TENANT_ID', value: tenant().tenantId }
            { name: 'DOCKER_CONFIG', value: '/auth' }
            { name: 'MANAGED_IDENTITY_CLIENT_ID', value: uami.properties.clientId }
          ]
        }
      ]
      initContainers: [
        {
          name: 'decodesecrets'
          image: 'mcr.microsoft.com/azure-cli:cbl-mariner2.0'
          command: [
            '/bin/sh'
          ]
          args: [
            '-c'
            'cat /tmp/secret-orig/pull-secrets |base64 -d > /etc/containers/config.json && cat /tmp/bearer-secret/bearer-secret | base64 -d > /etc/containers/${pullSecretFile}'
          ]
          volumeMounts: [
            { volumeName: 'pull-secrets-updated', mountPath: '/etc/containers' }
            { volumeName: 'pull-secrets', mountPath: '/tmp/secret-orig' }
            { volumeName: 'bearer-secret', mountPath: '/tmp/bearer-secret' }
          ]
        }
      ]
      volumes: [
        {
          name: 'pull-secrets-updated'
          storageType: 'EmptyDir'
        }
        {
          name: 'pull-secrets'
          storageType: 'Secret'
          secrets: [
            { secretRef: 'pull-secrets' }
          ]
        }
        {
          name: 'bearer-secret'
          storageType: 'Secret'
          secrets: [
            { secretRef: 'bearer-secret' }
          ]
        }
      ]
    }
  }
}
