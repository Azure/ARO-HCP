@description('The name of the AKS Managed Cluster resource.')
param aksClusterName string = 'aro-hcp-cluster-001'

@description('Optional DNS prefix to use with hosted Kubernetes API server FQDN.')
param dnsPrefix string = aksClusterName

@description('The number of agent nodes for the cluster.')
param agentCount int = 2

@description('The min number of agent nodes.')
param agentMinCount int = 2

@description('The max number of agent nodes.')
param agentMaxCount int = 3

@description('The size of the Virtual Machine.')
param agentVMSize string = 'Standard_D2s_v3'

@description('Disk size (in GB) to provision for each of the agent pool nodes. This value ranges from 0 to 1023. Specifying 0 will apply the default disk size for that agentVMSize.')
@minValue(0)
@maxValue(1023)
param osDiskSizeGB int = 32

@description('Maximum number of pods that can run on a node.')
param maxPods int = 100

@description('The version of Kubernetes.')
param kubernetesVersion string = '1.27.7'

@description('Network plugin used for building Kubernetes network.')
@allowed([
  'azure'
  'kubenet'
])
param networkPlugin string = 'azure'

@description('boolean flag to turn on and off of RBAC')
param enableRBAC bool = true

@description('Name of the VNET that will contain the AKS cluster and related resources.')
param vnetName string = 'aks-net'

@description('VNET address prefix')
param vnetAddressPrefix string = '10.128.0.0/14'

@description('Subnet name that will contain the App Service Environment')
param vnetSubnetName string = 'ClusterSubnet-001'

@description('Subnet address prefix')
param subnetPrefix string = '10.128.8.0/21'

@description('Specifies the name of the subnet hosting the pods of the AKS cluster.')
param podSubnetName string = 'PodSubnet-001'

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string = '10.128.64.0/18'

@description('A CIDR notation IP range from which to assign service cluster IPs.')
param serviceCidr string = '10.130.0.0/16'

@description('Containers DNS server IP address.')
param dnsServiceIP string = '10.130.0.10'

@description('A CIDR notation IP for Docker bridge.')
param dockerBridgeCidr string = '172.17.0.1/16'

@description('boolean flag to turn on and off nodepool autoscaling')
param nodePoolEnableAutoScaling bool = true

@description('Specifies the scan interval of the auto-scaler of the AKS cluster.')
param autoScalerProfileScanInterval string = '10s'

@description('Specifies the scale down delay after add of the auto-scaler of the AKS cluster.')
param autoScalerProfileScaleDownDelayAfterAdd string = '10m'

@description('Specifies the scale down delay after delete of the auto-scaler of the AKS cluster.')
param autoScalerProfileScaleDownDelayAfterDelete string = '20s'

@description('Specifies scale down delay after failure of the auto-scaler of the AKS cluster.')
param autoScalerProfileScaleDownDelayAfterFailure string = '3m'

@description('Specifies the scale down unneeded time of the auto-scaler of the AKS cluster.')
param autoScalerProfileScaleDownUnneededTime string = '10m'

@description('Specifies the scale down unready time of the auto-scaler of the AKS cluster.')
param autoScalerProfileScaleDownUnreadyTime string = '20m'

@description('Specifies the utilization threshold of the auto-scaler of the AKS cluster.')
param autoScalerProfileUtilizationThreshold string = '0.5'

@description('Specifies the max graceful termination time interval in seconds for the auto-scaler of the AKS cluster.')
param autoScalerProfileMaxGracefulTerminationSec string = '600'

@description('The resource group to peer the aks cluster to')
param vpnrg string

param location string=resourceGroup().location

param createdByConfigTag string='None'

var aksNsgId = aks_nsg.id
var aksPodNsgId = aks_pod_nsg.id
var vnetId = vnet.id
var vnetSubnetId = resourceId('Microsoft.Network/virtualNetworks/subnets', vnetName, vnetSubnetName)
var podSubnetId = resourceId('Microsoft.Network/virtualNetworks/subnets', vnetName, podSubnetName)
var aksClusterAdminRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', '0ab0b1a8-8aac-4efd-b8c2-3ee1fb270be8')
var rpServicePrincipalId = aro_rp_location.id
var aksClusterAdminRoleAssignmentName = guid(location, aksClusterName, aksClusterAdminRoleId, rpServicePrincipalId)
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b')
var aksClusterRbacClusterAdminRoleAssignmentName = guid(location, aksClusterName, aksClusterRbacClusterAdminRoleId, rpServicePrincipalId)
var networkContributorRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', '4d97b98b-1d4f-4787-a291-c67834d212e7')
var aksClusterUserDefinedManagedIdentityName = '${aksClusterName}-msi'
var aksClusterUserDefinedManagedIdentityId = aksClusterUserDefinedManagedIdentity.id
var aksNetworkContributorRoleAssignmentName = guid(aksClusterUserDefinedManagedIdentityId, networkContributorRoleId, vnetSubnetId)

resource aro_rp_location 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' = {
  location: location
  name: 'aro-rp-${location}'
}

resource aks_nsg 'Microsoft.Network/networkSecurityGroups@2023-04-01' = {
  properties: {}
  name: 'aks-nsg'
  location: location
}

resource aks_pod_nsg 'Microsoft.Network/networkSecurityGroups@2023-04-01' = {
  properties: {}
  name: 'aks-pod-nsg'
  location: location
}

resource vnet 'Microsoft.Network/virtualNetworks@2023-04-01' = {
  location: location
  name: vnetName
  tags: {
    sharedhcp: 'true'
  }
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
    subnets: [
      {
        name: vnetSubnetName
        properties: {
          addressPrefix: subnetPrefix
          networkSecurityGroup: {
            id: aksNsgId
          }
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
        }
      }
      {
        name: podSubnetName
        properties: {
          addressPrefix: podSubnetPrefix
          networkSecurityGroup: {
            id: aksPodNsgId
          }
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
      }
    ]
  }  
}

module nestedPeeringTemplate './nested-aks-peering-creation.bicep' = {
  name: 'nestedTemplate1'
  scope: resourceGroup(vpnrg)
  params: {
    vnetId: vnetId
    vnetName: vnetName    
  }
}

resource vnetName_peering_dev_vpn_vnet 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2023-04-01' = {
  parent: vnet
  name: 'peering-dev-vpn-vnet'
  properties: {
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: true
    allowGatewayTransit: false
    useRemoteGateways: true
    remoteVirtualNetwork: {
      id: resourceId(vpnrg, 'Microsoft.Network/virtualNetworks', 'dev-vpn-vnet')
    }
  }
}

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' = {
  name: aksClusterUserDefinedManagedIdentityName
  location: location
}

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: aksNetworkContributorRoleAssignmentName
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }  
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2022-03-01' = {
  location: location
  name: aksClusterName
  tags: {
    CreatedByConfig: createdByConfigTag
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${aksClusterUserDefinedManagedIdentityId}': {}
    }
  }
  properties: {
    nodeResourceGroup: '${resourceGroup().name}-aks1'
    apiServerAccessProfile: {
      enablePrivateCluster: true
    }
    addonProfiles: {
      azureKeyvaultSecretsProvider: {
        enabled: true
      }
    }
    kubernetesVersion: kubernetesVersion
    enableRBAC: enableRBAC
    dnsPrefix: dnsPrefix
    agentPoolProfiles: [
      {
        name: 'systempool'
        osType: 'Linux'
        mode: 'System'
        orchestratorVersion: kubernetesVersion
        enableAutoScaling: nodePoolEnableAutoScaling
        enableEncryptionAtHost: true
        enableFIPS: true
        osDiskType: 'Ephemeral'
        osDiskSizeGB: osDiskSizeGB
        count: agentCount
        minCount: agentMinCount
        maxCount: agentMaxCount
        vmSize: agentVMSize
        vnetSubnetID: vnetSubnetId
        podSubnetID: podSubnetId
        maxPods: maxPods
      }
    ]
    networkProfile: {
      networkPlugin: networkPlugin
      serviceCidr: serviceCidr
      dnsServiceIP: dnsServiceIP
      dockerBridgeCidr: dockerBridgeCidr
    }
    aadProfile: {
      managed: true
      enableAzureRBAC: true
    }
    autoScalerProfile: {
      'scan-interval': autoScalerProfileScanInterval
      'scale-down-delay-after-add': autoScalerProfileScaleDownDelayAfterAdd
      'scale-down-delay-after-delete': autoScalerProfileScaleDownDelayAfterDelete
      'scale-down-delay-after-failure': autoScalerProfileScaleDownDelayAfterFailure
      'scale-down-unneeded-time': autoScalerProfileScaleDownUnneededTime
      'scale-down-unready-time': autoScalerProfileScaleDownUnreadyTime
      'scale-down-utilization-threshold': autoScalerProfileUtilizationThreshold
      'max-graceful-termination-sec': autoScalerProfileMaxGracefulTerminationSec
    }
    autoUpgradeProfile: {
      upgradeChannel: 'node-image'
    }
  }
  dependsOn: [
    vnet    
  ]
}

resource aksClusterRbacClusterAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: aksClusterRbacClusterAdminRoleAssignmentName
  properties: {
    roleDefinitionId: aksClusterRbacClusterAdminRoleId
    principalId: reference(rpServicePrincipalId, '2018-11-30').principalId
    principalType: 'ServicePrincipal'
  } 
}

resource aksClusterAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: aksCluster
  name: aksClusterAdminRoleAssignmentName
  properties: {
    roleDefinitionId: aksClusterAdminRoleId
    principalId: reference(rpServicePrincipalId, '2018-11-30').principalId
    principalType: 'ServicePrincipal'
  }  
}
