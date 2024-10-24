@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Container App Environment')
param environmentName string

@description('Name of the Container App Job')
param jobName string

@description('Container image to use for the job')
param containerImage string

@description('Name of the user assigned managed identity')
param imageSyncManagedIdentity string

@description('DNS Name of the ACR')
param acrDnsName string

@description('URL of the pull secret')
param pullSecretUrl string

@description('URL of the bearer secret')
param bearerSecretUrl string

resource containerAppEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: environmentName
}

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: imageSyncManagedIdentity
}

resource symbolicname 'Microsoft.App/jobs@2024-03-01' = {
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
      triggerType: 'Manual'
      replicaTimeout: 60 * 60
      registries: [
        {
          identity: uami.id
          server: acrDnsName
        }
      ]
      secrets: [
        {
          name: 'pull-secrets'
          keyVaultUrl: pullSecretUrl
          identity: uami.id
        }
        {
          name: 'bearer-secret'
          keyVaultUrl: bearerSecretUrl
          identity: uami.id
        }
      ]
    }
    template: {
      containers: [
        {
          name: jobName
          image: containerImage
          volumeMounts: [
            { volumeName: 'pull-secrets-updated', mountPath: '/auth' }
          ]
          env: [
            { name: 'MANAGED_IDENTITY_CLIENT_ID', value: uami.properties.clientId }
            { name: 'DOCKER_CONFIG', value: '/auth' }
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
            'cat /tmp/secret-orig/pull-secrets |base64 -d > /etc/containers/config.json && cat /tmp/bearer-secret/bearer-secret | base64 -d > /etc/containers/quayio-auth.json'
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
