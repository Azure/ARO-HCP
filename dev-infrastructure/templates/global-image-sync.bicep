import {
  csvToArray
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

@description('The image to use for the oc-mirror job')
param ocMirrorImage string

@description('Defines if the oc-mirror job should be enabled')
param ocMirrorEnabled bool

@description('The name of the pull secret for the oc-mirror job')
param ocpPullSecretName string

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
  for secretName in [ocpPullSecretName]: {
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
//  O P E R A T O R   M I R R O R   J O B
//

// this is v2alpha1 syntax for oc-mirror 4.16, which we use until 4.18+ offers
// a way to not rebuild the catalogs, which fails in ACA

var operatorMirrorJobConfiguration = [
  {
    name: 'acm-mirror'
    cron: '0 10 * * *'
    timeout: 4 * 60 * 60
    retryLimit: 3
    targetRegistry: svcAcrName
    imageSetConfig: {
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
                  {
                    name: 'multicluster-engine.v2.8.1'
                  }
                  {
                    name: 'multicluster-engine.v2.8.2'
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
    compatibility: 'NOCATALOG'
  }
]

//
//  O C P   M I R R O R   J O B
//

var ocpMirrorDefinitions = [
  {
    name: 'oc-mirror-4-18'
    major: '4.18'
    channels: [
      {
        name: 'stable-4.18'
        type: 'ocp'
        full: true
        minVersion: '4.18.1'
        maxVersion: '4.18.9'
      }
    ]
  }
  {
    name: 'oc-mirror-4-19'
    major: '4.19'
    channels: [
      {
        name: 'candidate-4.19'
        type: 'ocp'
        full: true
        minVersion: '4.19.0-rc.2'
      }
    ]
  }
]
var ocpMirrorJobConfiguration = [
  for job in ocpMirrorDefinitions: {
    name: job.name
    cron: '0 * * * *'
    timeout: 4 * 60 * 60
    retryLimit: 3
    targetRegistry: ocpAcrName
    imageSetConfig: {
      kind: 'ImageSetConfiguration'
      apiVersion: 'mirror.openshift.io/v2alpha1'
      mirror: {
        platform: {
          architectures: ['multi', 'amd64', 'arm64']
          channels: job.channels
          graph: true
        }
        additionalImages: [
          { name: 'registry.redhat.io/redhat/redhat-operator-index:v${job.major}' }
          { name: 'registry.redhat.io/redhat/certified-operator-index:v${job.major}' }
          { name: 'registry.redhat.io/redhat/community-operator-index:v${job.major}' }
          { name: 'registry.redhat.io/redhat/redhat-marketplace-index:v${job.major}' }
        ]
      }
    }
    compatibility: 'LATEST'
  }
]

var ocMirrorJobConfiguration = ocMirrorEnabled ? union(ocpMirrorJobConfiguration, operatorMirrorJobConfiguration) : []

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
