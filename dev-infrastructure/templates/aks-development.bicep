@description('The name of the AKS Managed Cluster resource.')
param aksClusterName string = 'aro-hcp-cluster-001'

@description('Optional DNS prefix to use with hosted Kubernetes API server FQDN.')
param dnsPrefix string = aksClusterName

@description('(Optional) boolean flag to configure public/private AKS Cluster')
param enablePrivateCluster bool = true

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
param kubernetesVersion string = '1.29.2'

@description('VNET address prefix')
param vnetAddressPrefix string = '10.128.0.0/14'

@description('Subnet address prefix')
param subnetPrefix string = '10.128.8.0/21'

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string = '10.128.64.0/18'

@description('A CIDR notation IP range from which to assign service cluster IPs.')
param serviceCidr string = '10.130.0.0/16'

@description('Containers DNS server IP address.')
param dnsServiceIP string = '10.130.0.10'

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

@description('Current user id')
param currentUserId string

param location string = resourceGroup().location
param createdByConfigTag string = 'None'

var aksClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '0ab0b1a8-8aac-4efd-b8c2-3ee1fb270be8'
)
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

resource aro_rp_mi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: 'aro-rp-${location}'
}

resource aks_nsg 'Microsoft.Network/networkSecurityGroups@2023-09-01' = {
  name: 'aks-nsg'
  location: location
}

resource aks_pod_nsg 'Microsoft.Network/networkSecurityGroups@2023-09-01' = {
  name: 'aks-pod-nsg'
  location: location
}

resource vnet 'Microsoft.Network/virtualNetworks@2023-09-01' = {
  location: location
  name: 'aks-net'
  tags: {
    sharedhcp: 'true'
  }
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
  }
}

resource aksNodeSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-09-01' = {
  parent: vnet
  name: 'ClusterSubnet-001'
  properties: {
    addressPrefix: subnetPrefix
    networkSecurityGroup: {
      id: aks_nsg.id
    }
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
  }
}

resource aksPodSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-09-01' = {
  parent: vnet
  name: 'PodSubnet-001'
  properties: {
    addressPrefix: podSubnetPrefix
    networkSecurityGroup: {
      id: aks_pod_nsg.id
    }
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
}

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

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' = {
  location: location
  name: aksClusterName
  tags: {
    CreatedByConfig: createdByConfigTag
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${aksClusterUserDefinedManagedIdentity.id}': {}
    }
  }
  properties: {
    nodeResourceGroup: '${resourceGroup().name}-aks1'
    apiServerAccessProfile: {
      enablePrivateCluster: enablePrivateCluster
    }
    addonProfiles: {
      azureKeyvaultSecretsProvider: {
        enabled: true
      }
    }
    kubernetesVersion: kubernetesVersion
    enableRBAC: true
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
        vnetSubnetID: aksNodeSubnet.id
        podSubnetID: aksPodSubnet.id
        maxPods: maxPods
      }
    ]
    networkProfile: {
      networkDataplane: 'cilium'
      networkPolicy: 'cilium'
      networkPlugin: 'azure'
      serviceCidr: serviceCidr
      dnsServiceIP: dnsServiceIP
    }
    aadProfile: {
      managed: true
      enableAzureRBAC: true
    }
    autoScalerProfile: {
      'balance-similar-node-groups': 'false'
      'scan-interval': autoScalerProfileScanInterval
      'scale-down-delay-after-add': autoScalerProfileScaleDownDelayAfterAdd
      'scale-down-delay-after-delete': autoScalerProfileScaleDownDelayAfterDelete
      'scale-down-delay-after-failure': autoScalerProfileScaleDownDelayAfterFailure
      'scale-down-unneeded-time': autoScalerProfileScaleDownUnneededTime
      'scale-down-unready-time': autoScalerProfileScaleDownUnreadyTime
      'scale-down-utilization-threshold': autoScalerProfileUtilizationThreshold
      'skip-nodes-with-local-storage': 'false'
      'max-graceful-termination-sec': autoScalerProfileMaxGracefulTerminationSec
      'max-node-provision-time': '15m'
    }
    autoUpgradeProfile: {
      upgradeChannel: 'node-image'
    }
  }
}

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' =
  if (length(currentUserId) > 0) {
    scope: aksCluster
    name: guid(location, aksClusterName, aksClusterAdminRoleId, currentUserId)
    properties: {
      roleDefinitionId: aksClusterAdminRoleId
      principalId: currentUserId
      principalType: 'User'
    }
  }

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksRbacClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' =
  if (length(currentUserId) > 0) {
    scope: aksCluster
    name: guid(location, aksClusterName, aksClusterRbacClusterAdminRoleId, currentUserId)
    properties: {
      roleDefinitionId: aksClusterRbacClusterAdminRoleId
      principalId: currentUserId
      principalType: 'User'
    }
  }
