import {
  csvToArray
  determineZoneRedundancy
  determineZoneRedundancyForRegion
  getLocationAvailabilityZonesCSV
  parseIPServiceTag
} from '../modules/common.bicep'

@description('Azure Region Location')
param location string

@description('Specifies the name of the container app environment.')
param containerAppEnvName string

@description('Container app public IP service tags')
param containerAppOutboundServiceTags string
var containerAppOutboundServiceTagsArray = [
  for tag in (csvToArray(containerAppOutboundServiceTags)): parseIPServiceTag(tag)
]

@description('Specifies the name of the log analytics workspace.')
param containerAppLogAnalyticsName string = 'containerapp-log'

@description('Specifies the name of the user assigned managed identity.')
param imageSyncManagedIdentity string = 'image-sync'

@description('Resource group of the ACR containerapps will get permissions on')
param acrResourceGroup string

@description('Name of the service component ACR registry')
param svcAcrName string

@description('Name of the OCP ACR registry')
param ocpAcrName string

@description('Name of the keyvault where the pull secret is stored')
param keyVaultName string

@description('The name of the pull secret for the component sync job')
param componentSyncPullSecretName string

@description('The image to use for the component sync job')
param componentSyncImage string

@description('Defines if the component sync job should be enabled')
param componentSyncEnabed bool

@description('A CSV of the repositories to sync')
param repositoriesToSync string

@description('The number of tags to sync per image in the repo list')
param numberOfTags int = 10

@description('The image to use for the oc-mirror job')
param ocMirrorImage string

@description('Defines if the oc-mirror job should be enabled')
param ocMirrorEnabled bool

@description('The name of the pull secret for the oc-mirror job')
param ocpPullSecretName string

@description('Secret configuration to pass into component sync')
#disable-next-line secure-secrets-in-params // Doesn't contain a secret
param componentSyncSecrets string

var csSecrets = [
  for secret in csvToArray(componentSyncSecrets): {
    registry: split(secret, ':')[0]
    secret: split(secret, ':')[1]
  }
]

var bearerSecrets = [for css in csSecrets: '${css.secret}']

var secretsFolder = '/etc/containers'

var secretWithFolderPrefix = [
  for css in csSecrets: {
    registry: css.registry
    secretFile: '/auth/${css.secret}'
  }
]

var secretVar = {
  secrets: secretWithFolderPrefix
}

resource kv 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: keyVaultName
}

//
// Container App Infra
//

var locationAvailabilityZones = csvToArray(getLocationAvailabilityZonesCSV(location))
var locationHasAvailabilityZones = length(locationAvailabilityZones) > 0
module containerAppOutboundPublicIP '../modules/network/publicipaddress.bicep' = {
  name: 'containerapp-nat-gateway-ip'
  params: {
    name: 'containerapp-nat-gateway-ip'
    ipTags: containerAppOutboundServiceTagsArray
    location: location
    zones: locationHasAvailabilityZones ? locationAvailabilityZones : null
  }
}

resource containerAppNatGateway 'Microsoft.Network/natGateways@2023-02-01' = {
  name: 'containerapp-nat-gateway'
  location: location
  sku: {
    name: 'Standard'
  }
  properties: {
    publicIpAddresses: [
      {
        id: containerAppOutboundPublicIP.outputs.resourceId
      }
    ]
    idleTimeoutInMinutes: 4
  }
}

resource containerVnet 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: 'containerapp-vnet'
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: [
        '10.0.0.0/16'
      ]
    }
    subnets: [
      {
        name: 'containerapp-subnet'
        properties: {
          defaultOutboundAccess: false
          addressPrefix: '10.0.0.0/23'
          delegations: [
            {
              name: 'Microsoft.App.environments'
              properties: {
                serviceName: 'Microsoft.App/environments'
              }
              type: 'Microsoft.Network/virtualNetworks/subnets/delegations'
            }
          ]
          natGateway: {
            id: containerAppNatGateway.id
          }
        }
        type: 'Microsoft.Network/virtualNetworks/subnets'
      }
      {
        name: 'acr-pe-subnet'
        properties: {
          defaultOutboundAccess: false
          addressPrefix: '10.0.2.0/24'
        }
        type: 'Microsoft.Network/virtualNetworks/subnets'
      }
    ]
  }
}

var containerAppSubnetId = containerVnet.properties.subnets[0].id
var acrPeSubnetId = containerVnet.properties.subnets[1].id

module svcAcrPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: 'svc-acr-pe'
  params: {
    location: location
    subnetIds: [acrPeSubnetId]
    vnetId: containerVnet.id
    privateLinkServiceId: resourceId(acrResourceGroup, 'Microsoft.ContainerRegistry/registries', svcAcrName)
    serviceType: 'acr'
    groupId: 'registry'
  }
}

module ocpAcrPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: 'ocp-acr-pe'
  params: {
    location: location
    subnetIds: [acrPeSubnetId]
    vnetId: containerVnet.id
    privateLinkServiceId: resourceId(acrResourceGroup, 'Microsoft.ContainerRegistry/registries', ocpAcrName)
    serviceType: 'acr'
    groupId: 'registry'
  }
  dependsOn: [
    svcAcrPrivateEndpoint
  ]
}

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
    vnetConfiguration: {
      infrastructureSubnetId: containerAppSubnetId
    }
    workloadProfiles: [
      {
        name: 'Consumption'
        workloadProfileType: 'Consumption'
      }
    ]
    zoneRedundant: locationHasAvailabilityZones
  }
}

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: imageSyncManagedIdentity
  location: location
}

// ACR permissions

module acrPushPullPermissions '../modules/acr/acr-permissions.bicep' = [
  for acrName in [svcAcrName, ocpAcrName]: {
    name: '${imageSyncManagedIdentity}-${acrName}-acr-pushpull'
    scope: resourceGroup(acrResourceGroup)
    params: {
      principalId: uami.properties.principalId
      grantPushAccess: true
      grantPullAccess: true
      acrName: acrName
    }
  }
]

module pullSecretPermission '../modules/keyvault/keyvault-secret-access.bicep' = [
  for secretName in union([componentSyncPullSecretName, ocpPullSecretName], bearerSecrets): {
    name: guid(imageSyncManagedIdentity, location, keyVaultName, secretName, 'secret-user')
    params: {
      keyVaultName: keyVaultName
      secretName: secretName
      roleName: 'Key Vault Secrets User'
      managedIdentityPrincipalId: uami.properties.principalId
    }
    dependsOn: [
      kv
    ]
  }
]

//
// Component sync job
//

var componentSyncJobName = 'component-sync'

var componentSecretsArray = [
  for bearerSecretName in bearerSecrets: {
    name: bearerSecretName
    keyVaultUrl: 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${bearerSecretName}'
    identity: uami.id
  }
]

var componentSecretVolumesArray = [
  for bearerSecretName in bearerSecrets: {
    name: bearerSecretName
    storageType: 'Secret'
    secrets: [
      { secretRef: bearerSecretName }
    ]
  }
]

var componentSecretVolumeMountsArray = [
  for bearerSecretName in bearerSecrets: {
    volumeName: bearerSecretName
    mountPath: '/tmp/secrets/${bearerSecretName}'
  }
]

resource componentSyncJob 'Microsoft.App/jobs@2024-03-01' = if (componentSyncEnabed) {
  name: componentSyncJobName
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
      secrets: union(
        [
          {
            name: 'pull-secrets'
            keyVaultUrl: 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${componentSyncPullSecretName}'
            identity: uami.id
          }
        ],
        componentSecretsArray
      )
    }
    template: {
      containers: [
        {
          name: componentSyncJobName
          image: componentSyncImage
          volumeMounts: [
            { volumeName: 'pull-secrets-updated', mountPath: '/auth' }
          ]
          env: [
            { name: 'NUMBER_OF_TAGS', value: '${numberOfTags}' }
            { name: 'REPOSITORIES', value: repositoriesToSync }
            { name: 'ACR_TARGET_REGISTRY', value: '${svcAcrName}${environment().suffixes.acrLoginServer}' }
            { name: 'TENANT_ID', value: tenant().tenantId }
            { name: 'DOCKER_CONFIG', value: '/auth' }
            { name: 'MANAGED_IDENTITY_CLIENT_ID', value: uami.properties.clientId }
            { name: 'SECRETS', value: string(secretVar) }
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
            'cat /tmp/secret-orig/pull-secrets |base64 -d > /etc/containers/config.json && cd /tmp/secrets; for file in $(find . -type f); do export fn=$(basename $file); cat $file | base64 -d > ${secretsFolder}/$fn; done;'
          ]
          volumeMounts: union(
            [
              { volumeName: 'pull-secrets-updated', mountPath: '/etc/containers' }
              { volumeName: 'pull-secrets', mountPath: '/tmp/secret-orig' }
            ],
            componentSecretVolumeMountsArray
          )
        }
      ]
      volumes: union(
        [
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
        ],
        componentSecretVolumesArray
      )
    }
  }
  dependsOn: [
    kv
    acrPushPullPermissions
    ocpAcrPrivateEndpoint
    svcAcrPrivateEndpoint
  ]
}

// oc-mirror jobs

var ocpMirrorConfig = {
  kind: 'ImageSetConfiguration'
  apiVersion: 'mirror.openshift.io/v2alpha1'
  mirror: {
    platform: {
      architectures: ['multi', 'amd64', 'arm64']
      channels: [
        {
          name: 'stable-4.17'
          type: 'ocp'
          full: true
          minVersion: '4.17.0'
          maxVersion: '4.17.0'
        }
        {
          name: 'stable-4.18'
          type: 'ocp'
          full: true
          minVersion: '4.18.1'
          maxVersion: '4.18.9'
        }
      ]
      graph: true
    }
    additionalImages: [
      { name: 'registry.redhat.io/redhat/redhat-operator-index:v4.16' }
      { name: 'registry.redhat.io/redhat/certified-operator-index:v4.16' }
      { name: 'registry.redhat.io/redhat/community-operator-index:v4.16' }
      { name: 'registry.redhat.io/redhat/redhat-marketplace-index:v4.16' }
      { name: 'registry.redhat.io/redhat/redhat-operator-index:v4.17' }
      { name: 'registry.redhat.io/redhat/certified-operator-index:v4.17' }
      { name: 'registry.redhat.io/redhat/community-operator-index:v4.17' }
      { name: 'registry.redhat.io/redhat/redhat-marketplace-index:v4.17' }
      { name: 'registry.redhat.io/redhat/redhat-operator-index:v4.18' }
      { name: 'registry.redhat.io/redhat/certified-operator-index:v4.18' }
      { name: 'registry.redhat.io/redhat/community-operator-index:v4.18' }
      { name: 'registry.redhat.io/redhat/redhat-marketplace-index:v4.18' }
    ]
  }
}

// this is v2alpha1 syntax for oc-mirror 4.16, which we use until 4.18+ offers
// a way to not rebuild the catalogs, which fails in ACA
var acmMirrorConfig = {
  kind: 'ImageSetConfiguration'
  apiVersion: 'mirror.openshift.io/v2alpha1'
  mirror: {
    operators: [
      {
        catalog: 'registry.redhat.io/redhat/redhat-operator-index:v4.16'
        packages: [
          {
            name: 'multicluster-engine'
            bundles: [
              {
                name: 'multicluster-engine.v2.7.0'
              }
              {
                name: 'multicluster-engine.v2.8.0'
              }
            ]
          }
          {
            name: 'advanced-cluster-management'
            bundles: [
              {
                name: 'advanced-cluster-management.v2.12.0'
              }
              {
                name: 'advanced-cluster-management.v2.13.0'
              }
            ]
          }
        ]
      }
    ]
  }
}

var ocMirrorJobConfiguration = ocMirrorEnabled
  ? [
      {
        name: 'oc-mirror'
        cron: '0 * * * *'
        timeout: 4 * 60 * 60
        retryLimit: 3
        targetRegistry: ocpAcrName
        imageSetConfig: ocpMirrorConfig
        compatibility: 'LATEST'
      }
      {
        name: 'acm-mirror'
        cron: '0 10 * * *'
        timeout: 4 * 60 * 60
        retryLimit: 3
        targetRegistry: svcAcrName
        imageSetConfig: acmMirrorConfig
        compatibility: 'NOCATALOG'
      }
    ]
  : []

resource ocMirrorJobs 'Microsoft.App/jobs@2024-03-01' = [
  for i in range(0, length(ocMirrorJobConfiguration)): {
    name: ocMirrorJobConfiguration[i].name
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
        manualTriggerConfig: {
          parallelism: 1
        }
        scheduleTriggerConfig: {
          cronExpression: ocMirrorJobConfiguration[i].cron
          parallelism: 1
        }
        replicaTimeout: ocMirrorJobConfiguration[i].timeout
        replicaRetryLimit: ocMirrorJobConfiguration[i].retryLimit
        registries: [
          {
            identity: uami.id
            server: '${svcAcrName}${environment().suffixes.acrLoginServer}'
          }
        ]
        secrets: [
          {
            name: 'pull-secrets'
            keyVaultUrl: 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${ocpPullSecretName}'
            identity: uami.id
          }
        ]
      }
      template: {
        containers: [
          {
            name: 'oc-mirror'
            image: ocMirrorImage
            volumeMounts: [
              { volumeName: 'pull-secrets-updated', mountPath: '/etc/containers' }
            ]
            env: [
              { name: 'IMAGE_SET_CONFIG', value: base64(string(ocMirrorJobConfiguration[i].imageSetConfig)) }
              { name: 'REGISTRY', value: ocMirrorJobConfiguration[i].targetRegistry }
              {
                name: 'REGISTRY_URL'
                value: '${ocMirrorJobConfiguration[i].targetRegistry}${environment().suffixes.acrLoginServer}'
              }
              { name: 'XDG_RUNTIME_DIR', value: '/etc' }
              { name: 'AZURE_CLIENT_ID', value: uami.properties.clientId }
              {
                name: 'APPSETTING_WEBSITE_SITE_NAME'
                value: 'workaround - https://github.com/microsoft/azure-container-apps/issues/502'
              }
              { name: 'OC_MIRROR_COMPATIBILITY', value: ocMirrorJobConfiguration[i].compatibility }
            ]
            resources: {
              cpu: 2
              memory: '4Gi'
            }
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
              'cat /tmp/secret-orig/pull-secrets | base64 -d > /etc/containers/auth.json'
            ]
            volumeMounts: [
              { volumeName: 'pull-secrets-updated', mountPath: '/etc/containers' }
              { volumeName: 'pull-secrets', mountPath: '/tmp/secret-orig' }
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
        ]
      }
    }
    dependsOn: [
      kv
      acrPushPullPermissions
      ocpAcrPrivateEndpoint
      svcAcrPrivateEndpoint
    ]
  }
]
