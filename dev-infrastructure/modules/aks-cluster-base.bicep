import {
  csvToArray
  parseIPServiceTag
} from '../modules/common.bicep'

// Constants
param aksClusterName string
param aksNodeResourceGroupName string
param aksEtcdKVEnableSoftDelete bool

// Metrics
param metricLabelsAllowlist string = ''
param metricAnnotationsAllowList string = ''

// System agentpool spec (Infra)
param systemAgentPoolName string
param systemAgentMinCount int
param systemAgentMaxCount int
param systemAgentVMSize string
param systemAgentPoolZones array
param systemZoneRedundantMode string

// User agentpool spec (Worker)
param userAgentPoolName string
param userAgentMinCount int
param userAgentMaxCount int
param userAgentVMSize string
param userAgentPoolZones array
param userAgentPoolCount int
param userZoneRedundantMode string

// User agentpool spec (Infra)
param infraAgentPoolName string
param infraAgentMinCount int
param infraAgentMaxCount int
param infraAgentVMSize string
param infraAgentPoolZones array
param infraAgentPoolCount int
param infraZoneRedundantMode string

param serviceCidr string = '10.130.0.0/16'
param dnsServiceIP string = '10.130.0.10'

// Passed Params and Overrides
param location string

@description('The resource group hosting IP Addresses of the AKS Clusters')
param ipResourceGroup string
param ipZones array

param kubernetesVersion string
param deployIstio bool
param istioVersions array = []
param vnetName string
param nodeSubnetId string
param podSubnetPrefix string
param clusterType string
param workloadIdentities array
param networkDataplane string
param networkPolicy string
param enableSwiftV2Nodepools bool

param aksClusterUserDefinedManagedIdentityName string

@description('IPTags to be set on the cluster outbound IP address in the format of ipTagType:tag,ipTagType:tag')
param aksClusterOutboundIPAddressIPTags string = ''

@maxLength(24)
param aksKeyVaultName string

// KV tagging
param aksKeyVaultTagName string
param aksKeyVaultTagValue string

// Owning team tag
param owningTeamTagValue string

// Local Params
@description('Optional DNS prefix to use with hosted Kubernetes API server FQDN.')
param dnsPrefix string = aksClusterName

@description('Disk size (in GB) to provision for each of the agent pool nodes. This value ranges from 0 to 1023. Specifying 0 will apply the default disk size for that agentVMSize.')
@minValue(0)
@maxValue(1023)
param systemOsDiskSizeGB int
param userOsDiskSizeGB int
param infraOsDiskSizeGB int

@description('The resource IDs of ACR instances that the AKS cluster will pull images from')
param pullAcrResourceIds array = []

@description('MSI that will take actions on the AKS cluster during service deployment time')
param deploymentMsiId string

@description('Perform cryptographic operations using keys. Only works for key vaults that use the Azure role-based access control permission model.')
var keyVaultCryptoUserId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

// Azure Kubernetes Service RBAC Cluster Admin Role
// https://www.azadvertizer.net/azrolesadvertizer/b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b.html
var aksClusterAdminRBACRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

// Network Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

import * as res from '../modules/resource.bicep'

//
//   E T C D   K E Y V A U L T
//

module aks_keyvault_builder '../modules/keyvault/keyvault.bicep' = {
  name: aksKeyVaultName
  params: {
    location: location
    keyVaultName: aksKeyVaultName
    // todo: change for higher environments
    private: false
    enableSoftDelete: aksEtcdKVEnableSoftDelete
    tagKey: aksKeyVaultTagName
    tagValue: aksKeyVaultTagValue
  }
}

resource aks_keyvault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: aks_keyvault_builder.name
}

resource aks_etcd_kms 'Microsoft.KeyVault/vaults/keys@2023-07-01' = {
  parent: aks_keyvault
  name: 'aks-etcd-encryption'
  properties: {
    kty: 'RSA'
    keyOps: [
      'encrypt'
      'decrypt'
    ]
    keySize: 2048
    rotationPolicy: {
      lifetimeActions: [
        {
          action: {
            type: 'notify'
          }
          trigger: {
            timeBeforeExpiry: 'P30D'
          }
        }
      ]
    }
  }
}

resource aks_keyvault_crypto_user 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksClusterUserDefinedManagedIdentity.id, keyVaultCryptoUserId, aks_keyvault.id)
  scope: aks_keyvault
  properties: {
    roleDefinitionId: keyVaultCryptoUserId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

//
//   N E T W O R K
//

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
}

resource aksPodSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: 'PodSubnet-001'
  properties: {
    addressPrefix: podSubnetPrefix
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.Storage'
      }
    ]
    defaultOutboundAccess: false
    delegations: [
      {
        name: 'AKS'
        properties: {
          serviceName: 'Microsoft.ContainerService/managedClusters'
        }
      }
    ]
  }
}

//
//   E G R E S S   A N D   I N G R E S S
//

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: aksClusterUserDefinedManagedIdentityName
}

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(aksClusterUserDefinedManagedIdentity.id, networkContributorRoleId, nodeSubnetId)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

var aksClusterOutboundIPAddressName = '${aksClusterName}-outbound-ip'
module aksClusterOutboundIPAddress '../modules/network/publicipaddress.bicep' = {
  name: aksClusterOutboundIPAddressName
  scope: resourceGroup(ipResourceGroup)
  params: {
    name: aksClusterOutboundIPAddressName
    ipTags: aksClusterOutboundIPAddressIPTags
    location: location
    zones: length(ipZones) > 0 ? ipZones : null
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: networkContributorRoleId
    }
  }
}

//
//   A K S   C L U S T E R
//

// conditional advanced networking. only supported with cilium
var advancedNetworking = networkDataplane == 'cilium'
  ? {
      enabled: true
      observability: {
        enabled: true
      }
    }
  : null

var swiftNodepoolTags = enableSwiftV2Nodepools
  ? {
      'aks-nic-enable-multi-tenancy': 'true'
    }
  : null

var systemPoolZonesArray = systemZoneRedundantMode == 'Enabled' || (systemZoneRedundantMode == 'Auto' && length(systemAgentPoolZones) > 0)
  ? systemAgentPoolZones
  : null

resource aksCluster 'Microsoft.ContainerService/managedClusters@2025-07-02-preview' = {
  location: location
  name: aksClusterName
  sku: {
    name: 'Base'
    tier: 'Standard'
  }
  tags: {
    persist: 'true'
    clusterType: clusterType
    owningTeam: owningTeamTagValue
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${aksClusterUserDefinedManagedIdentity.id}': {}
    }
  }
  properties: {
    aadProfile: {
      managed: true
      enableAzureRBAC: true
    }
    addonProfiles: {
      azureKeyvaultSecretsProvider: {
        enabled: true
        config: {
          enableSecretRotation: 'true'
          rotationPollInterval: '1h'
        }
      }
      omsagent: {
        enabled: false
      }
    }
    agentPoolProfiles: [
      {
        name: systemAgentPoolName
        osType: 'Linux'
        osSKU: 'AzureLinux'
        mode: 'System'
        enableAutoScaling: true
        enableEncryptionAtHost: true
        enableFIPS: true
        enableNodePublicIP: false
        kubeletDiskType: 'OS'
        osDiskType: 'Ephemeral'
        osDiskSizeGB: systemOsDiskSizeGB
        minCount: systemAgentMinCount
        maxCount: systemAgentMaxCount
        vmSize: systemAgentVMSize
        type: 'VirtualMachineScaleSets'
        upgradeSettings: {
          maxSurge: '10%'
        }
        vnetSubnetID: nodeSubnetId
        podSubnetID: aksPodSubnet.id
        maxPods: 100
        availabilityZones: systemPoolZonesArray
        securityProfile: {
          enableSecureBoot: false
          enableVTPM: false
        }
        nodeLabels: {
          'aro-hcp.azure.com/role': 'system'
        }
        nodeTaints: [
          'CriticalAddonsOnly=true:NoSchedule'
        ]
        tags: swiftNodepoolTags
      }
    ]
    autoScalerProfile: {
      'balance-similar-node-groups': 'true'
      'daemonset-eviction-for-occupied-nodes': true
      'scan-interval': '10s'
      'scale-down-delay-after-add': '10m'
      'scale-down-delay-after-delete': '20s'
      'scale-down-delay-after-failure': '3m'
      'scale-down-unneeded-time': '10m'
      'scale-down-unready-time': '20m'
      'scale-down-utilization-threshold': '0.5'
      'skip-nodes-with-local-storage': 'false'
      'max-graceful-termination-sec': '600'
      'max-node-provision-time': '15m'
    }
    autoUpgradeProfile: {
      nodeOSUpgradeChannel: 'NodeImage'
      upgradeChannel: 'patch'
    }
    azureMonitorProfile: {
      metrics: {
        enabled: true
        kubeStateMetrics: {
          metricLabelsAllowlist: metricLabelsAllowlist
          metricAnnotationsAllowList: metricAnnotationsAllowList
        }
      }
    }
    disableLocalAccounts: true
    dnsPrefix: dnsPrefix
    enableRBAC: true
    kubernetesVersion: kubernetesVersion
    metricsProfile: {
      costAnalysis: {
        enabled: false
      }
    }
    networkProfile: {
      advancedNetworking: advancedNetworking
      ipFamilies: ['IPv4']
      loadBalancerSku: 'standard'
      loadBalancerProfile: {
        outboundIPs: {
          publicIPs: [
            {
              id: resourceId(ipResourceGroup, 'Microsoft.Network/publicIPAddresses', aksClusterOutboundIPAddressName)
            }
          ]
        }
      }
      networkDataplane: networkDataplane
      networkPolicy: networkPolicy
      networkPlugin: 'azure'
      serviceCidr: serviceCidr
      serviceCidrs: [serviceCidr]
      dnsServiceIP: dnsServiceIP
    }
    nodeResourceGroup: aksNodeResourceGroupName
    oidcIssuerProfile: {
      enabled: true
    }
    securityProfile: {
      azureKeyVaultKms: {
        enabled: true
        keyId: aks_etcd_kms.properties.keyUriWithVersion
        keyVaultNetworkAccess: 'Public'
      }
      imageCleaner: {
        enabled: true
        intervalHours: 24
      }
      workloadIdentity: {
        enabled: true
      }
    }
    servicePrincipalProfile: {
      clientId: 'msi'
    }
    serviceMeshProfile: (deployIstio)
      ? {
          mode: 'Istio'
          istio: {
            components: {
              ingressGateways: [
                {
                  enabled: true
                  mode: 'External'
                }
              ]
            }
            revisions: istioVersions
          }
        }
      : null
    // TODO: ops-ingress phase 2: enable k8s gateway api
    // ingressProfile: deployIstio
    //   ? {
    //       gatewayAPI: {
    //         installation: 'Standard'
    //       }
    //     }
    //   : null
    storageProfile: {
      diskCSIDriver: {
        enabled: true
      }
      fileCSIDriver: {
        enabled: true
      }
      snapshotController: {
        enabled: true
      }
    }
    supportPlan: 'KubernetesOfficial'
  }
  dependsOn: [
    aksNetworkContributorRoleAssignment
    aks_keyvault_crypto_user
    aksClusterOutboundIPAddress
  ]
}

resource maintenanceWindows 'Microsoft.ContainerService/managedClusters/maintenanceConfigurations@2025-08-02-preview' = [
  for maintenanceType in ['default', 'aksManagedAutoUpgradeSchedule', 'aksManagedNodeOSUpgradeSchedule']: {
    parent: aksCluster
    name: maintenanceType
    properties: {
      maintenanceWindow: {
        durationHours: 10
        startTime: '15:00'
        notAllowedDates: [
          {
            start: '2025-11-16'
            end: '2025-11-22'
          }
          {
            start: '2025-11-24'
            end: '2025-12-03'
          }
          {
            start: '2025-12-22'
            end: '2026-01-13'
          }
          {
            start: '2026-02-16'
            end: '2026-02-20'
          }
        ]
        schedule: {
          weekly: {
            dayOfWeek: 'Monday'
            intervalWeeks: 1
          }
        }
      }
    }
  }
]

module userAgentPools '../modules/aks/pool.bicep' = {
  name: 'user-agent-pools'
  params: {
    aksClusterName: aksCluster.name
    poolBaseName: userAgentPoolName
    poolZones: userAgentPoolZones
    poolCount: userAgentPoolCount
    poolRole: 'worker'
    enableSwiftV2: enableSwiftV2Nodepools
    minCount: userAgentMinCount
    maxCount: userAgentMaxCount
    vmSize: userAgentVMSize
    osDiskSizeGB: userOsDiskSizeGB
    vnetSubnetId: nodeSubnetId
    podSubnetId: aksPodSubnet.id
    zoneRedundantMode: userZoneRedundantMode
    maxPods: 225
  }
}

module infraAgentPools '../modules/aks/pool.bicep' = {
  name: 'infra-agent-pools'
  params: {
    aksClusterName: aksCluster.name
    poolBaseName: infraAgentPoolName
    poolZones: infraAgentPoolZones
    poolCount: infraAgentPoolCount
    poolRole: 'infra'
    enableSwiftV2: false
    minCount: infraAgentMinCount
    maxCount: infraAgentMaxCount
    vmSize: infraAgentVMSize
    osDiskSizeGB: infraOsDiskSizeGB
    vnetSubnetId: nodeSubnetId
    podSubnetId: aksPodSubnet.id
    zoneRedundantMode: infraZoneRedundantMode
    maxPods: 225
    taints: [
      'infra=true:NoSchedule'
    ]
  }
}

//
// ACR Pull Permissions on the own resource group and the resource groups provided
// by acrResourceGroups
//

var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

var acrReferences = [for acrId in pullAcrResourceIds: res.acrRefFromId(acrId)]

module acrPullRole 'acr/acr-permissions.bicep' = [
  for acrRef in acrReferences: {
    name: guid(acrRef.name, aksCluster.id, acrPullRoleDefinitionId)
    scope: resourceGroup(acrRef.resourceGroup.subscriptionId, acrRef.resourceGroup.name)
    params: {
      principalIds: [aksCluster.properties.identityProfile.kubeletidentity.objectId]
      acrName: acrRef.name
      grantPullAccess: true
    }
  }
]

//
//   W O R K L O A D   I D E N T I T I E S
//

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for wi in workloadIdentities: {
    name: wi.value.uamiName
  }
]

resource uami_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: uami[i]
    name: '${workloadIdentities[i].value.uamiName}-${location}-fedcred'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]

//
//  A C R   P U L L   C O N T R O L L E R
//

resource pullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: 'image-puller'
}

module acrPullerRoles 'acr/acr-permissions.bicep' = [
  for acrRef in acrReferences: {
    name: guid(acrRef.name, aksCluster.id, acrPullRoleDefinitionId, 'puller-identity')
    scope: resourceGroup(acrRef.resourceGroup.subscriptionId, acrRef.resourceGroup.name)
    params: {
      principalIds: [pullerIdentity.properties.principalId]
      acrName: acrRef.name
      grantPullAccess: true
    }
  }
]

@batchSize(1)
resource puller_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: pullerIdentity
    name: '${workloadIdentities[i].value.uamiName}-${location}-puller-fedcred'
    properties: {
      audiences: [
        'api://AzureCRTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]

// grant aroDevopsMsi the aksClusterAdmin role on the aksCluster so it can
// deploy services to the cluster
resource aroDevopsMSIClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksCluster.id, deploymentMsiId, aksClusterAdminRBACRoleId)
  scope: aksCluster
  properties: {
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: aksClusterAdminRBACRoleId
  }
}

// Outputs
output aksOidcIssuerUrl string = aksCluster.properties.oidcIssuerProfile.issuerURL
output aksClusterName string = aksClusterName
output aksClusterKeyVaultSecretsProviderPrincipalId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId
output aksClusterManagedIdentityPrincipalId string = aksClusterUserDefinedManagedIdentity.properties.principalId
output etcKeyVaultId string = aks_keyvault_builder.outputs.kvId
