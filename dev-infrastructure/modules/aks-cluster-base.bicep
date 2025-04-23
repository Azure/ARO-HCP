import {
  csvToArray
  parseIPServiceTag
} from '../modules/common.bicep'

// Constants
param aksClusterName string
param aksNodeResourceGroupName string
param aksEtcdKVEnableSoftDelete bool

// Metrics
param dcrId string
param metricLabelsAllowlist string = ''
param metricAnnotationsAllowList string = ''

// System agentpool spec(Infra)
param systemAgentMinCount int = 2
param systemAgentMaxCount int = 3
param systemAgentVMSize string = 'Standard_D2s_v3'

// User agentpool spec (Worker)
param userAgentMinCount int = 1
param userAgentMaxCount int = 3
param userAgentVMSize string = 'Standard_D2s_v3'
param userAgentPoolAZCount int = 3

param serviceCidr string = '10.130.0.0/16'
param dnsServiceIP string = '10.130.0.10'

// Passed Params and Overrides
param location string

@description('List of Availability Zones to use for the AKS cluster')
param locationAvailabilityZones array
var locationHasAvailabilityZones = length(locationAvailabilityZones) > 0

param kubernetesVersion string
param deployIstio bool
param istioVersions array = []
param vnetAddressPrefix string
param subnetPrefix string
param podSubnetPrefix string
param clusterType string
param workloadIdentities array
param nodeSubnetNSGId string
param networkDataplane string
param networkPolicy string
param enableSwiftV2 bool

@description('Istio Ingress Gateway Public IP Address resource name')
param istioIngressGatewayIPAddressName string = ''

@description('IPTags to be set on the cluster outbound IP address in the format of ipTagType:tag,ipTagType:tag')
param aksClusterOutboundIPAddressIPTags string = ''
var aksClusterOutboundIPAddressIPTagsArray = [
  for tag in csvToArray(aksClusterOutboundIPAddressIPTags): parseIPServiceTag(tag)
]

@description('IPTags to be set on the Istio Ingress Gateway IP address in the format of ipTagType:tag,ipTagType:tag')
param istioIngressGatewayIPAddressIPTags string = ''
var istioIngressGatewayIPAddressIPTagsArray = [
  for tag in csvToArray(istioIngressGatewayIPAddressIPTags): parseIPServiceTag(tag)
]

@maxLength(24)
param aksKeyVaultName string

param logAnalyticsWorkspaceId string = ''

// Local Params
@description('Optional DNS prefix to use with hosted Kubernetes API server FQDN.')
param dnsPrefix string = aksClusterName

@description('Disk size (in GB) to provision for each of the agent pool nodes. This value ranges from 0 to 1023. Specifying 0 will apply the default disk size for that agentVMSize.')
@minValue(0)
@maxValue(1023)
param systemOsDiskSizeGB int
param userOsDiskSizeGB int

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

// Tag Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4a9ae827-6dc8-4573-8ac7-8239d42aa03f.html
var tagContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4a9ae827-6dc8-4573-8ac7-8239d42aa03f'
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
    purpose: 'etcd-encryption'
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

resource deploymentMsiNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, networkContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

resource deploymentMsiTagContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, tagContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: tagContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

var vnetName = 'aks-net'

module vnetCreation '../modules/network/vnet.bicep' = {
  name: 'vnet-${vnetName}-creation'
  params: {
    location: location
    vnetName: vnetName
    vnetAddressPrefix: vnetAddressPrefix
    enableSwift: enableSwiftV2
    deploymentMsiId: deploymentMsiId
  }
  dependsOn: [
    deploymentMsiNetworkContributorRoleAssignment
    deploymentMsiTagContributorRoleAssignment
  ]
}

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
  dependsOn: [
    vnetCreation
  ]
}

resource aksNodeSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: 'ClusterSubnet-001'
  properties: {
    addressPrefix: subnetPrefix
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.AzureCosmosDB'
      }
      {
        service: 'Microsoft.ContainerRegistry'
      }
      {
        service: 'Microsoft.Storage'
      }
      {
        service: 'Microsoft.KeyVault'
      }
    ]
    networkSecurityGroup: {
      id: nodeSubnetNSGId
    }
  }
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
    delegations: [
      {
        name: 'AKS'
        properties: {
          serviceName: 'Microsoft.ContainerService/managedClusters'
        }
      }
    ]
  }
  dependsOn: [
    aksNodeSubnet
  ]
}

//
//   E G R E S S   A N D   I N G R E S S
//

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${aksClusterName}-msi'
  location: location
}

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(aksClusterUserDefinedManagedIdentity.id, networkContributorRoleId, aksNodeSubnet.id)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

module istioIngressGatewayIPAddress '../modules/network/publicipaddress.bicep' = if (deployIstio) {
  name: istioIngressGatewayIPAddressName
  params: {
    name: istioIngressGatewayIPAddressName
    ipTags: istioIngressGatewayIPAddressIPTagsArray
    location: location
    zones: locationHasAvailabilityZones ? locationAvailabilityZones : null
    // Role Assignment needed for the public IP address to be used on the Load Balancer
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: networkContributorRoleId
    }
  }
}

var aksClusterOutboundIPAddressName = 'aro-hcp-cluster-egress'
module aksClusterOutboundIPAddress '../modules/network/publicipaddress.bicep' = {
  name: aksClusterOutboundIPAddressName
  params: {
    name: aksClusterOutboundIPAddressName
    ipTags: aksClusterOutboundIPAddressIPTagsArray
    location: location
    zones: locationHasAvailabilityZones ? locationAvailabilityZones : null
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

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' = {
  location: location
  name: aksClusterName
  sku: {
    name: 'Base'
    tier: 'Standard'
  }
  tags: {
    persist: 'true'
    clusterType: clusterType
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
          rotationPollInterval: '5m'
        }
      }
      omsagent: (logAnalyticsWorkspaceId != '')
        ? {
            enabled: true
            config: {
              logAnalyticsWorkspaceResourceID: logAnalyticsWorkspaceId
            }
          }
        : {
            enabled: false
          }
    }
    agentPoolProfiles: [
      {
        name: 'system'
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
        count: systemAgentMinCount
        minCount: systemAgentMinCount
        maxCount: systemAgentMaxCount
        vmSize: systemAgentVMSize
        type: 'VirtualMachineScaleSets'
        upgradeSettings: {
          maxSurge: '10%'
        }
        vnetSubnetID: aksNodeSubnet.id
        podSubnetID: aksPodSubnet.id
        maxPods: 100
        availabilityZones: locationHasAvailabilityZones ? locationAvailabilityZones : null
        securityProfile: {
          enableSecureBoot: false
          enableVTPM: false
        }
        nodeTaints: [
          'CriticalAddonsOnly=true:NoSchedule'
        ]
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
      ipFamilies: ['IPv4']
      loadBalancerSku: 'standard'
      loadBalancerProfile: {
        outboundIPs: {
          publicIPs: [
            {
              id: resourceId('Microsoft.Network/publicIPAddresses', aksClusterOutboundIPAddressName)
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

//
//   O B S E R V A B I L I T Y
//

resource aksDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2017-05-01-preview' = if (logAnalyticsWorkspaceId != '') {
  scope: aksCluster
  name: aksClusterName
  properties: {
    logs: [
      {
        category: 'kube-audit'
        enabled: true
      }
      {
        category: 'kube-audit-admin'
        enabled: true
      }
    ]
    workspaceId: logAnalyticsWorkspaceId
  }
}

resource aksClusterDcr 'Microsoft.Insights/dataCollectionRules@2023-03-11' = if (logAnalyticsWorkspaceId != '') {
  name: '${aksClusterName}-dcr'
  location: location
  kind: 'Linux'
  properties: {
    dataSources: {
      extensions: [
        {
          name: 'ContainerInsightsExtension'
          streams: [
            'Microsoft-ContainerLog'
            'Microsoft-ContainerLogV2'
            'Microsoft-KubeEvents'
            'Microsoft-KubePodInventory'
          ]
          extensionSettings: {
            dataCollectionSettings: {
              interval: '1m'
              namespaceFilteringMode: 'Off'
              enableContainerLogV2: true
            }
          }
          extensionName: 'ContainerInsights'
        }
      ]
    }
    destinations: {
      logAnalytics: [
        {
          name: 'ContainerInsightsWorkspace'
          workspaceResourceId: logAnalyticsWorkspaceId
        }
      ]
    }
    dataFlows: [
      {
        destinations: [
          'ContainerInsightsWorkspace'
        ]
        streams: [
          'Microsoft-ContainerLog'
          'Microsoft-ContainerLogV2'
          'Microsoft-KubeEvents'
          'Microsoft-KubePodInventory'
        ]
      }
    ]
  }
}

resource aksClusterDcra 'Microsoft.Insights/dataCollectionRuleAssociations@2023-03-11' = if (logAnalyticsWorkspaceId != '') {
  name: '${aksClusterName}-dcra'
  scope: aksCluster
  properties: {
    description: 'Association of data collection rule. Deleting this association will break the data collection for this AKS Cluster.'
    dataCollectionRuleId: aksClusterDcr.id
  }
}

resource userAgentPools 'Microsoft.ContainerService/managedClusters/agentPools@2024-10-01' = [
  for i in range(0, userAgentPoolAZCount): {
    parent: aksCluster
    name: 'user${take(string(i+1), 8)}'
    properties: {
      osType: 'Linux'
      osSKU: 'AzureLinux'
      mode: 'User'
      enableAutoScaling: true
      enableEncryptionAtHost: true
      enableFIPS: true
      enableNodePublicIP: false
      kubeletDiskType: 'OS'
      osDiskType: 'Ephemeral'
      osDiskSizeGB: userOsDiskSizeGB
      count: userAgentMinCount
      minCount: userAgentMinCount
      maxCount: userAgentMaxCount
      vmSize: userAgentVMSize
      type: 'VirtualMachineScaleSets'
      upgradeSettings: {
        maxSurge: '10%'
      }
      vnetSubnetID: aksNodeSubnet.id
      podSubnetID: aksPodSubnet.id
      maxPods: 225
      availabilityZones: locationHasAvailabilityZones ? [locationAvailabilityZones[i]] : null
      securityProfile: {
        enableSecureBoot: false
        enableVTPM: false
      }
      tags: enableSwiftV2
        ? {
            'aks-nic-enable-multi-tenancy': 'true'
          }
        : null
    }
  }
]

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
      principalId: aksCluster.properties.identityProfile.kubeletidentity.objectId
      acrName: acrRef.name
      grantPullAccess: true
    }
  }
]

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = [
  for wi in workloadIdentities: {
    location: location
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
      principalId: pullerIdentity.properties.principalId
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

// metrics dcr association
resource azuremonitormetrics_dcra_clusterResourceId 'Microsoft.Insights/dataCollectionRuleAssociations@2022-06-01' = {
  name: '${resourceGroup().name}-${aksCluster.name}-dcra'
  scope: aksCluster
  properties: {
    description: 'Association of data collection rule. Deleting this association will break the data collection for this AKS Cluster.'
    dataCollectionRuleId: dcrId
  }
}

// Outputs
output userAssignedIdentities array = [
  for i in range(0, length(workloadIdentities)): {
    uamiID: uami[i].id
    uamiName: workloadIdentities[i].value.uamiName
    uamiClientID: uami[i].properties.clientId
    uamiPrincipalID: uami[i].properties.principalId
  }
]
output aksVnetId string = vnet.id
output aksNodeSubnetId string = aksNodeSubnet.id
output aksOidcIssuerUrl string = aksCluster.properties.oidcIssuerProfile.issuerURL
output aksClusterName string = aksClusterName
output aksClusterKeyVaultSecretsProviderPrincipalId string = aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId
output istioIngressGatewayIPAddress string = deployIstio ? istioIngressGatewayIPAddress.outputs.ipAddress : ''
